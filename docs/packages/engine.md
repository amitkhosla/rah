# Engine Package

## Purpose
Provides the **execution engine** for RAH — a high-performance instruction interpreter that executes compiled workflows. The engine uses a flat instruction table with absolute jumps and a stack-based control plane, designed to minimize GC pressure and maintain nanosecond-level latency. It manages a 2-tier slot memory system (arena → heap).

## Files
- **executor.go**: `Execute()` function — program counter loop, instruction dispatch, timing instrumentation
- **manager.go**: `FlowManager` — context pool, request lifecycle, overflow metrics, `ReturnContext`
- **registry.go**: `FlowRegistry` for named sub-flows
- **slot_manager.go**: `WriteSlot` / `ReadSlot` on `ExecutionState` — 2-tier slot access
- **steps/txid.go**: `StoreInternalTxID`, `BindCorrelationID` instructions
- **plan.go**: `Plan` structure for compiled API definitions
- **api_definition.go**: `ApiDefinition` and `Endpoint` structures

---

## Key Types

### ExecutionState
The **control plane** for a single execution. Allocated on the goroutine stack — no GC pressure.

```go
type ExecutionState struct {
    LinkStack          [16]int16  // return addresses for sub-flow calls (max 16 deep)
    StackPtr           int8       // current depth in LinkStack
    PC                 int16      // current program counter (kept in sync each step)
    IsStopped          bool       // set by StopPlan sentinel
    slotValueThreshold int        // WriteSlot threshold override; 0 = use rctx.SlotValueThreshold
}
```

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
| `TxIDGen` | `*rctx.TxIDGenerator` | Globally-unique TX ID generator; one per gateway process |
| `Pool` | `sync.Pool` | Context pool; `New` calls `ctx.InitSlots()` |
| `Config` | `config.GlobalLayout` | Global limits and settings |
| `Strategy` | `ExecutionStrategy` | Sync or parallel fan-out |
| `Metrics` | `OverflowMetrics` | Counter for arena overflows |

### OverflowMetrics
```go
type OverflowMetrics struct {
    ArenaOverflows atomic.Int64 // ext arenaBlock borrowed from pool this request
}
```
Exposed via `/debug/arena`. Non-zero rates suggest `ArenaInlineSize` needs tuning.

### FlowRegistry
Holds named sub-flows callable from instructions (including from parallel goroutines).

```go
type FlowRegistry struct {
    staticFlows  map[string]func(ctx *rctx.Context) int16
    dynamicFlows map[string][]Instruction
}
```

---

## Slot Management — 2-Tier Model

`WriteSlot` and `ReadSlot` on `ExecutionState` implement slot access. No DataStore round-trips — all data stays in-process.

```
WriteSlot(ctx, idx, data)
        │
        ├─ Tier 1 — Arena (fast path, zero alloc)
        │   len(data) ≤ SlotValueThreshold (256B)
        │   → ctx.Alloc(len(data)) + copy; ctx.ByteSlots[idx] = slice
        │
        └─ Tier 2 — Heap (large values)
            len(data) > SlotValueThreshold
            → make([]byte, n) + copy; ctx.ByteSlots[idx] = slice
            → ctx.ArenaOverflowed = true

ReadSlot(ctx, idx)
        └─ return ctx.ByteSlots[idx]  (nil if out of range or unset)
```

Out-of-range slot indices (`idx < 0` or `idx >= len(ctx.ByteSlots)`) are silent no-ops on write and return nil on read.

---

## Execute() Signature
```go
func Execute(
    ctx     *rctx.Context,
    table   []Instruction,
    startID int16,
)
```
`ExecutionState` is allocated on the stack for each execution. Sub-flows called via `FlowRegistry.Call` each create their own stack-local `ExecutionState`.

---

## Request Lifecycle (FlowManager)

```
ProcessRequest(ctx, req)
    1. Resolve API and sub-path
    2. ctx.InternalTxID = fm.TxIDGen.Generate(ctx.RequestStartNs)
    3. FlowManager.Extract(ctx, req)   ← sets ctx.Request; BindHeader zero-copies headers
    4. Execute(ctx, plan, 0)

ReturnContext(ctx)          ← always call this instead of pool.Put directly
    1. Increment ArenaOverflows metric if ctx.ArenaOverflowed
    2. ctx.ReleaseOverflow()           ← return ext arenaBlock to pool
    3. fm.Pool.Put(ctx)
```

---

## TX ID Instructions

### StoreInternalTxID(slotIdx int)
Formats `ctx.InternalTxID` (the gateway-assigned ID) as a 32-char hex string and stores it in `ByteSlots[slotIdx]`. Use to forward the internal ID upstream or inject it into a response header.

### BindCorrelationID(headerKey string, generateIfMissing bool, gen *rctx.TxIDGenerator, slotIdx int)
Reads `headerKey` from the incoming request headers and stores it in `ByteSlots[slotIdx]` (zero-copy). If the header is absent and `generateIfMissing=true`, generates a new ID using `gen`. Use for customer-driven correlation IDs (e.g. `X-Request-ID`).

---

## Responsibilities
1. **Execute()**: Core interpreter — dispatches instructions, updates PC, records timings
2. **WriteSlot / ReadSlot**: 2-tier slot access (arena or heap)
3. **FlowManager**: Context lifecycle, TX ID assignment
4. **ReturnContext**: Arena release → pool return
5. **FlowRegistry**: Named sub-flows including parallel fan-out
6. **StoreInternalTxID / BindCorrelationID**: TX ID exposure instructions

---

## Dependencies
- **rctx**: Context (data plane), TxIDGenerator, arena helpers
- **observability**: Records per-instruction timing events
- **engine/steps**: Built-in instruction implementations

---

## Performance Notes
- `ExecutionState` is stack-allocated — zero GC pressure
- Absolute jumps avoid branch prediction overhead from relative offset calculation
- `StopPlan (-1)` sentinel terminates execution without extra flags
- `LinkStack[16]` limits nested sub-flow calls to 16 levels (architectural choice)
- Per-instruction timing gated behind `ctx.Obs != nil` — ~0ns overhead when disabled
- `TxIDGen.Generate()` costs ~7ns per request (one atomic increment)
- `BindHeader` is zero-copy via `unsafe.Slice` — no arena allocation for headers

---

## Architecture Diagram
```
HTTP Request
      │
      ▼
FlowManager.ProcessRequest
      │
      ├─ ctx.InternalTxID = TxIDGen.Generate(ctx.RequestStartNs)
      ├─ Extract: ctx.Request set; BindHeader zero-copies from req.Header
      │
      ▼
Execute(ctx, plan, 0)
      │
      ▼
ExecutionState{}  ← stack-allocated, no GC
      │
      ▼  ┌──────────────────────────────────────────┐
      │  │  for pc in [0..len(table)):               │
      │  │    instruction.Action(ctx, &state) → pc  │
      │  │    ↓                                      │
      │  │  WriteSlot / ReadSlot                     │
      │  │    ├─ Tier 1: arena (≤256B, zero-alloc)  │
      │  │    └─ Tier 2: heap  (>256B, make)        │
      │  └──────────────────────────────────────────┘
      │
      ▼
FlowManager.ReturnContext(ctx)
      ├─ record ArenaOverflows metric
      ├─ ReleaseOverflow (return ext block to pool)
      └─ pool.Put(ctx)
```
