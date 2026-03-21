# RCTX Package

## Purpose
Provides the **request context** (data plane) — the central structure that flows through the execution engine, carrying all request state, buffer memory, slots, and response data. Designed for zero-allocation in the steady state via an inline 1KB arena and a single pool-borrowed 4KB overflow block.

## Files
- **context.go**: Main Context structure, helper methods, arena allocator, Reset/Finalize lifecycle
- **arena.go**: Arena sizing constants, `arenaBlock` type and `arenaPool`
- **txid.go**: `TxIDGenerator` for globally-unique transaction IDs
- **context_test.go**: Context lifecycle, buffer management, streaming vs buffered mode tests
- **arena_test.go**: Arena allocator, inline vs ext-block paths, Reset tests

---

## Key Types

### Core Context
- **Context**: Central data structure for a single request. Pool-allocated; Reset() before reuse.
  - `ApiId`: Matched API identifier
  - `Match`: RouteMatch with path parameters and execution plan
  - `Method`, `Path`, `RemainingPath`: Request metadata ([]byte, zero-copy views into metadataBuffer)
  - `RawQuery`: Query string (unsafe.Slice into the original string — no copy)
  - `InternalTxID [2]uint64`: Globally-unique transaction ID assigned per request by FlowManager. Use `rctx.FormatTxID` to format as hex string.

### Slots (Logic Arena)
- **ByteSlots** `[][]byte`: Variable-length binary data (headers, bodies, computed values). Backed by inline `byteSlotBase [48][]byte` — no heap allocation.
- **IntSlots** `[]int64`: Integer state (timestamps, counters, IDs). Backed by inline `intSlotBase [16]int64`.
- **BoolSlots** `[]bool`: Boolean flags and conditions. Backed by inline `boolSlotBase [8]bool`.

Slots start at index 0 (compiler `nextSlot = 0`). The compiler enforces a hard cap of `BaseByteSlots = 48` at bake time — flows requiring more must be split.

### Arena Allocator
The arena replaces per-request `make([]byte, n)` calls for slot data.

| Field | Type | Description |
|-------|------|-------------|
| `arenaInline` | `[1024]byte` | 1KB inline block embedded in Context. No heap or pool allocation. |
| `arenaUsed` | `int32` | Bytes consumed in `arenaInline`. |
| `arenaExt` | `*arenaBlock` | Single pool-borrowed 4KB block. Nil in the common case. |
| `ArenaOverflowed` | `bool` | True if ext block was borrowed or heap fallback occurred. |
| `InternalTxID` | `[2]uint64` | Globally-unique transaction ID. Set per-request by FlowManager. |

### TxIDGenerator (txid.go)
```go
gen := rctx.NewTxIDGenerator()         // once at startup; panics on entropy failure
id  := gen.Generate(ctx.RequestStartNs) // ~7ns, one atomic increment
s   := rctx.FormatTxID(id)             // "016x016x" hex string (allocates)
fp  := gen.Fingerprint()               // instance fingerprint for log correlation
```

**Layout of `[2]uint64` transaction ID:**
- Word0 = `fingerprint[0] XOR (epochMs<<32 | counter_low32)`
- Word1 = `fingerprint[1]`

Properties:
- Unique per gateway instance via `crypto/rand` fingerprint seeded at startup
- Time-ordered within instance (~1ms resolution from `RequestStartNs`, no syscall)
- Counter wraps at 2³² per millisecond — impossible to exhaust

---

## Slot Memory Tiers

Every slot write follows this 2-level strategy:

```
WriteSlot(idx, data)
        │
        ├─ Tier 1 (common): len(data) ≤ SlotValueThreshold (256B)
        │       → ctx.Alloc() from arena (inline 1KB, then ext 4KB)
        │       → Zero heap allocation for small values
        │
        └─ Tier 2 (uncommon): len(data) > SlotValueThreshold
                → make([]byte, n) on heap
                → ctx.ArenaOverflowed = true
                → No DataStore round-trip — stays in-process
```

No sentinel markers, no DataStore tier. Slot indices must be within `[0, BaseByteSlots)`.

---

## Arena Allocator Detail

### Alloc(n int) []byte
Carves `n` bytes from the arena. No heap allocation for common values ≤ 1KB.

```
arenaInline (1KB, always in Context struct)    Alloc path
┌──────────────────────────┐
│ [used=200] slot data ...  │  ← Alloc returns arenaInline[200:200+n]
│                           │
│        (free, 824B)       │
└──────────────────────────┘

       inline full?
           │  yes
           ▼
arenaExt = arenaPool.Get()   ← Pool-borrowed 4KB block (once per request max)
┌──────────────────────────┐
│ [used=0]                  │  ← Next Alloc returns arenaExt.buf[0:n]
└──────────────────────────┘

       ext block full? or n > 4096?
           │
           ▼
       make([]byte, n)        ← Heap fallback (very rare — value > 4KB)
```

Constants:
- `ArenaInlineSize = 1024` bytes always-inline
- `ArenaBlockSize = 4096` bytes for the ext pool block
- `SlotValueThreshold = 256` — WriteSlot threshold (arena vs heap)
- `BaseByteSlots = 48` — inline slot count

### ReleaseOverflow()
Returns the ext block to the pool **before** `Pool.Put`.

---

## Pool Lifecycle

```
Pool.New
   └─ ctx.InitSlots()
         ├─ ctx.ByteSlots = ctx.byteSlotBase[:48]    // no make()
         ├─ ctx.IntSlots  = ctx.intSlotBase[:16]      // no make()
         └─ ctx.BoolSlots = ctx.boolSlotBase[:8]      // no make()

Request start
   └─ ctx = pool.Get()
   └─ ctx.Reset(w)                    // zero arenaUsed, nil slots, reset fields
   └─ ctx.InternalTxID = fm.TxIDGen.Generate(ctx.RequestStartNs)

Instruction execution
   └─ WriteSlot / ReadSlot via ExecutionState
   └─ ctx.Alloc() for header/query binding

Request end
   └─ FlowManager.ReturnContext(ctx)
         ├─ record ArenaOverflows metric
         ├─ ctx.ReleaseOverflow()     // return ext block to pool
         └─ pool.Put(ctx)
```

---

## Responsibilities
1. **Request Metadata**: Holds method, path, query — zero-copy views into inline buffers
2. **Logic Slots**: Typed slots for instruction read/write, backed by 48-element inline arrays
3. **Arena Allocation**: 1KB inline → single 4KB pool block → heap fallback
4. **Transaction IDs**: `InternalTxID` assigned per request; expose via `StoreInternalTxID` or `BindCorrelationID` steps
5. **Body Buffering**: Optional buffering for transformation
6. **Header Mutations**: Tracks upstream header changes
7. **Tenant Isolation**: Carries TenantID through execution
8. **Observability**: Carries Telemetry and RequestTrace references

---

## Dependencies
- **observability**: Telemetry and tracing integration
- **sync**: Pool for arenaBlock (arena.go)
- **crypto/rand**, **encoding/binary**, **fmt**: TxIDGenerator (txid.go)

---

## Slot Layout Pattern
```
Per-Request Slot Allocation (compiler starts at 0):
ByteSlots[0]  = First flow variable / first BindHeader target
ByteSlots[1]  = Second flow variable
...
ByteSlots[47] = 48th variable (hard cap; compile error if exceeded)

Example flow:
ByteSlots[0]  = "Authorization" header value  (zero-copy unsafe.Slice)
ByteSlots[1]  = Computed auth token            (arena-backed)
ByteSlots[2]  = Large response body            (heap-backed, ArenaOverflowed=true)
```

---

## Request/Response Flow
```
HTTP Request
    ↓
Router.Lookup(path) → apiId
    ↓
ctx = pool.Get(); ctx.Reset(w)
    ↓
ctx.InternalTxID = fm.TxIDGen.Generate(ctx.RequestStartNs)
    ↓
FlowManager.Extract(ctx, req)          ← ctx.Request set; BindHeader uses zero-copy
    ↓
engine.Execute(ctx, plan, 0)           ← instructions read/write slots
    ↓
Instructions write ResponseStatus, headers, body
    ↓
ctx.Finalize()                         ← flush buffered response
    ↓
FlowManager.ReturnContext(ctx)
    ├─ record ArenaOverflows metric
    ├─ ReleaseOverflow()
    └─ pool.Put(ctx)
```

---

## Modes of Operation

### Streaming Mode (IsBuffered=false)
- No request buffering
- Response streamed directly to client
- Lower latency, higher throughput

### Buffering Mode (IsBuffered=true)
- Request buffered in RequestBuffer
- Response buffered in ResponseBuffer
- Required for transformation steps that inspect or modify the body
