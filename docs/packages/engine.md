# Engine Package

## Purpose
Provides the **execution engine** for RAH — a high-performance instruction interpreter that executes compiled workflows. The engine uses a flat instruction table with absolute jumps and a stack-based control plane, designed to minimize GC pressure and maintain nanosecond-level latency. It also manages the three-tier slot memory system (arena → pool-borrowed blocks → DataStore).

## Files
- **executor.go**: `Execute()` function — program counter loop, instruction dispatch, timing instrumentation
- **manager.go**: `FlowManager` — context pool, request lifecycle, overflow metrics, `ReturnContext`
- **registry.go**: `FlowRegistry` for named sub-flows; `HeaderRegistry` for slot index assignment
- **slot_manager.go**: `WriteSlot` / `ReadSlot` on `ExecutionState` — three-tier slot access
- **slot_overflow_adapter.go**: `SlotOverflowAdapter` — wraps `datastore.KeyValueStore` as `rctx.SlotOverflowStore`
- **plan.go**: `Plan` structure for compiled API definitions
- **api_definition.go**: `ApiDefinition` and `Endpoint` structures

---

## Key Types

### ExecutionState
The **control plane** for a single execution. Allocated on the goroutine stack — no GC pressure.

```go
type ExecutionState struct {
    LinkStack    [16]int16            // return addresses for sub-flow calls (max 16 deep)
    StackPtr     int8                 // current depth in LinkStack
    PC           int16                // current program counter (kept in sync each step)
    IsStopped    bool                 // set by StopPlan sentinel
    SlotOverflow rctx.SlotOverflowStore // nil in common case; set by Execute() from FlowManager
}
```

`SlotOverflow` is the store reference for this execution. It is propagated from `FlowManager` through `Execute()` and stored on `ExecutionState` so every instruction has access without holding a reference on `rctx.Context`.

### InstructionFunc / Instruction
```go
type InstructionFunc func(ctx *rctx.Context, state *ExecutionState) int16
type Instruction struct {
    Name   string
    Action InstructionFunc  // returns absolute ID of next instruction
}
```

### FlowManager
The top-level coordinator for request execution.

| Field | Type | Description |
|-------|------|-------------|
| `State` | `atomic.Pointer[EngineState]` | Lock-free snapshot of routes and compiled plans |
| `HeaderRegistry` | `*HeaderRegistry` | Maps header names → slot indices |
| `Pool` | `sync.Pool` | Context pool; `New` calls `ctx.InitSlots()` |
| `Config` | `config.GlobalLayout` | Global limits and settings |
| `Strategy` | `ExecutionStrategy` | Sync or parallel fan-out |
| `Metrics` | `OverflowMetrics` | Counters for arena and slot overflows |
| `reqCounter` | `atomic.Uint64` | Issues monotonic `ReqID` to each request for DataStore key scoping |
| `SlotOverflowStore` | `rctx.SlotOverflowStore` | Optional DataStore-backed overflow tier; nil in common case |

### OverflowMetrics
```go
type OverflowMetrics struct {
    ArenaOverflows atomic.Int64 // extra arenaBlock borrowed from pool this request
    SlotOverflows  atomic.Int64 // slotExtBlock borrowed from pool this request
}
```
Exposed via `/debug/arena`. Non-zero rates suggest the default arena or slot sizes need tuning.

### FlowRegistry
Holds named sub-flows callable from instructions (including from parallel goroutines).

```go
type FlowRegistry struct {
    staticFlows  map[string]func(ctx *rctx.Context) int16
    dynamicFlows map[string][]Instruction
    SlotOverflow rctx.SlotOverflowStore  // propagated from FlowManager
}
```
`SlotOverflow` is set once at startup so all sub-flows (including those spawned in parallel goroutines) share the same store reference without threading it through goroutine closures.

### SlotOverflowAdapter
Wraps any `datastore.KeyValueStore` to implement `rctx.SlotOverflowStore`.

```go
func NewSlotOverflowAdapter(
    store  datastore.KeyValueStore,
    tenant string,
    domain string,
) rctx.SlotOverflowStore
```

Keys are stored under `{domain}:{slot-ov/reqID/seq}` or `{domain}:{slot-idx/reqID/idx}`, keeping them isolated from application data. Typical usage:

```go
store, _ := datastoreMgr.GetStore(config.DomainSlotOverflow)
fm.SlotOverflowStore = engine.NewSlotOverflowAdapter(store, "system", "slot-overflow")
fm.Registry.SlotOverflow = fm.SlotOverflowStore
```

---

## Slot Management — Three-Tier Model

`WriteSlot` and `ReadSlot` on `ExecutionState` implement transparent slot access across all three tiers. Instructions call these methods rather than manipulating `ctx.ByteSlots` directly for any write that might overflow.

```
WriteSlot(ctx, idx, data)
        │
        ├─ Tier 1 — Arena (fast path, zero alloc)
        │   idx < len(ctx.ByteSlots) AND len(data) ≤ ArenaBlockSize
        │   → ctx.Alloc(len(data)) + copy; ctx.ByteSlots[idx] = slice
        │
        ├─ Tier 2 — DataStore: value overflow
        │   len(data) > ArenaBlockSize
        │   → store.SlotPut("slot-ov/{reqID}/{seq}", data)
        │   → ctx.ByteSlots[idx] = [0x00, 0xFF] + key  (sentinel)
        │   → fallback to heap if store nil or write fails
        │
        └─ Tier 3 — DataStore: index overflow
            idx ≥ len(ctx.ByteSlots)
            → store.SlotPut("slot-idx/{reqID}/{idx}", data)
            → no-op if store nil

ReadSlot(ctx, idx)
        │
        ├─ idx ≥ len(ctx.ByteSlots)         → store.SlotGet("slot-idx/...")
        ├─ IsSlotOverflowRef(ctx.ByteSlots[idx]) → store.SlotGet(key from sentinel)
        └─ default                           → ctx.ByteSlots[idx]
```

---

## Execute() Signature
```go
func Execute(
    ctx     *rctx.Context,
    table   []Instruction,
    startID int16,
    store   rctx.SlotOverflowStore,  // nil = no overflow store
)
```
`store` is placed on the stack-allocated `ExecutionState{SlotOverflow: store}` at the start of each execution. Sub-flows called via `FlowRegistry.Call` inherit `r.SlotOverflow` automatically.

---

## Request Lifecycle (FlowManager)

```
ProcessRequest(ctx, req)
    1. Assign ctx.ReqID = fm.reqCounter.Add(1)
    2. Resolve API and sub-path
    3. FlowManager.Extract(ctx, req)   ← header values → ctx.Alloc + ByteSlots
    4. Execute(ctx, plan, 0, fm.SlotOverflowStore)

ReturnContext(ctx)          ← always call this instead of pool.Put directly
    1. Increment ArenaOverflows / SlotOverflows metrics if set
    2. for _, key := range ctx.TakeSlotOverflowKeys():
           fm.SlotOverflowStore.SlotDelete(key)   ← clean ephemeral store entries
    3. ctx.ReleaseOverflow()           ← return arenaBlocks + slotExtBlock to pools
    4. fm.Pool.Put(ctx)
```

---

## Responsibilities
1. **Execute()**: Core interpreter — dispatches instructions, updates PC, records timings
2. **WriteSlot / ReadSlot**: Three-tier slot access with transparent store spill
3. **FlowManager**: Context lifecycle, request ID assignment, overflow store wiring
4. **ReturnContext**: Orchestrates store cleanup → pool-resource release → pool return
5. **FlowRegistry**: Named sub-flows, including parallel fan-out (inherits store automatically)
6. **SlotOverflowAdapter**: Bridges `datastore.KeyValueStore` → `rctx.SlotOverflowStore`
7. **HeaderRegistry**: Assigns stable slot indices to HTTP header names

---

## Dependencies
- **rctx**: Context (data plane), SlotOverflowStore interface, arena helpers
- **observability**: Records per-instruction timing events
- **datastore**: `KeyValueStore` wrapped by `SlotOverflowAdapter`
- **engine/steps**: Built-in instruction implementations

---

## Performance Notes
- `ExecutionState` is stack-allocated — zero GC pressure
- `SlotOverflow` on `ExecutionState` is a nil pointer check in the common case — ~1ns overhead
- Absolute jumps avoid branch prediction overhead from relative offset calculation
- `StopPlan (-1)` sentinel terminates execution without extra flags
- `LinkStack[16]` limits nested sub-flow calls to 16 levels (architectural choice)
- Per-instruction timing gated behind `ctx.Obs != nil` — ~0ns overhead when disabled
- `reqCounter.Add(1)` is a single atomic increment per request (~5ns)

---

## Architecture Diagram
```
HTTP Request
      │
      ▼
FlowManager.ProcessRequest
      │
      ├─ ctx.ReqID = reqCounter.Add(1)
      ├─ Extract headers → ctx.Alloc (arena, zero alloc)
      │
      ▼
Execute(ctx, plan, 0, SlotOverflowStore)
      │
      ▼
ExecutionState{SlotOverflow: store}  ← stack-allocated
      │
      ▼  ┌──────────────────────────────────────────┐
      │  │  for pc in [0..len(table)):               │
      │  │    instruction.Action(ctx, &state) → pc  │
      │  │    ↓                                      │
      │  │  WriteSlot / ReadSlot                     │
      │  │    ├─ Tier 1: arena (common)              │
      │  │    ├─ Tier 2: DataStore value overflow    │
      │  │    └─ Tier 3: DataStore index overflow    │
      │  └──────────────────────────────────────────┘
      │
      ▼
FlowManager.ReturnContext(ctx)
      ├─ record metrics
      ├─ delete DataStore slot keys
      ├─ ReleaseOverflow (return arena blocks + slotExt to pool)
      └─ pool.Put(ctx)
```
