# RCTX Package

## Purpose
Provides the **request context** (data plane) — the central structure that flows through the execution engine, carrying all request state, buffer memory, slots, and response data. Designed for zero-allocation in the steady state via an inline arena allocator and pool-borrowed overflow resources.

## Files
- **context.go**: Main Context structure, helper methods, arena allocator, slot-overflow helpers, Reset/Finalize lifecycle
- **arena.go**: Arena sizing constants, `SlotOverflowStore` interface, sentinel encoding, key builders, `arenaBlock`/`slotExtBlock` types and their `sync.Pool` pools
- **context_test.go**: Context lifecycle, buffer management, streaming vs buffered mode tests
- **arena_test.go**: Arena allocator, overflow path, sentinel helpers, slot-overflow key lifecycle tests

---

## Key Types

### Core Context
- **Context**: Central data structure for a single request. Pool-allocated; Reset() before reuse.
  - `ApiId`: Matched API identifier
  - `Match`: RouteMatch with path parameters and execution plan
  - `Method`, `Path`, `RemainingPath`: Request metadata ([]byte, zero-copy views into metadataBuffer)
  - `RawQuery`: Query string (unsafe.Slice into the original string — no copy)
  - `ReqID uint64`: Monotonic per-request counter assigned by FlowManager. Scopes DataStore keys so concurrent requests never collide.

### Slots (Logic Arena)
- **ByteSlots** `[][]byte`: Variable-length binary data (headers, bodies, computed values). Backed by inline `byteSlotBase [32][]byte` — no heap allocation.
- **IntSlots** `[]int64`: Integer state (timestamps, counters, IDs). Backed by inline `intSlotBase [16]int64`.
- **BoolSlots** `[]bool`: Boolean flags and conditions. Backed by inline `boolSlotBase [8]bool`.

Slot counts can grow beyond the inline base via a pool-borrowed `slotExtBlock` (GrowByteSlots), and beyond the pool extension via the DataStore tier. See **Slot Memory Tiers** below.

### Arena Allocator
The arena replaces per-request `make([]byte, n)` calls for slot data.

| Field | Type | Description |
|-------|------|-------------|
| `primary` | `arenaBlock` | Inline 4 096-byte block embedded in Context. No heap allocation. |
| `active` | `*arenaBlock` | Points to the block currently being written into (starts as `&primary`). |
| `extra` | `[4]*arenaBlock` | Up to 4 pool-borrowed overflow blocks (each 4 096 bytes). Nil until needed. |
| `extraN` | `int32` | Number of extra blocks currently borrowed. |
| `slotExt` | `*slotExtBlock` | Pool-borrowed block that extends ByteSlots/IntSlots/BoolSlots beyond inline capacity. |
| `ArenaOverflowed` | `bool` | True if any extra arena block was borrowed this request. |
| `SlotOverflowed` | `bool` | True if a slotExtBlock was borrowed this request. |

### Slot-Overflow Tracking
These fields are set by FlowManager; Context itself never holds a store reference.

| Field | Type | Description |
|-------|------|-------------|
| `ReqID` | `uint64` | Per-request ID for DataStore key scoping. |
| `slotDataSeq` | `uint32` | Auto-incrementing counter for value-overflow keys within a request. |
| `slotOverflowKeys` | `[]string` | Keys written to the DataStore this request. Deleted by FlowManager.ReturnContext before Pool.Put. Allocated lazily — nil for most requests. |

### SlotOverflowStore (interface, defined in arena.go)
```go
type SlotOverflowStore interface {
    SlotPut(key string, value []byte) error
    SlotGet(key string) ([]byte, error)
    SlotDelete(key string) error
}
```
Ephemeral backing store for values that cannot fit in the arena. Implementations must be concurrency-safe. Use `engine.NewSlotOverflowAdapter` to wrap any `datastore.KeyValueStore`.

---

## Slot Memory Tiers

Every slot write follows this three-tier cascade:

```
WriteSlot(idx, data)
        │
        ├─ Tier 1 (common): len(data) ≤ ArenaBlockSize AND idx < len(ByteSlots)
        │       → Alloc from arena, store pointer in ByteSlots[idx]
        │       → Zero heap allocation
        │
        ├─ Tier 2 (uncommon): len(data) > ArenaBlockSize OR all arena blocks full
        │       → SlotOverflowStore.SlotPut(key, data)
        │       → ByteSlots[idx] = sentinel prefix + key  (2-byte 0x00FF + key string)
        │       → On read: sentinel detected → SlotOverflowStore.SlotGet(key)
        │       → Fallback to heap if store is nil or write fails
        │
        └─ Tier 3 (rare): idx ≥ len(ByteSlots)  (slot index beyond in-memory capacity)
                → SlotOverflowStore.SlotPut("slot-idx/{reqID}/{idx}", data)
                → On read: idx overflow detected → SlotOverflowStore.SlotGet(...)
                → No-op if store is nil
```

**Store-reference sentinel** — When a value is spilled to the DataStore, the slot holds a two-byte prefix `[0x00, 0xFF]` followed by the key string. This prefix is unreachable by normal HTTP values (invalid UTF-8, below 0x20). Helpers:
```go
IsSlotOverflowRef(b []byte) bool          // detects sentinel
DecodeSlotOverflowKey(b []byte) string    // extracts the key
ctx.EncodeSlotRef(key) []byte             // builds the sentinel (from arena)
```

**DataStore key formats:**
```
Tier 2 (value overflow): "slot-ov/{reqID}/{seq}"   — unique within a request via seq counter
Tier 3 (index overflow): "slot-idx/{reqID}/{idx}"  — deterministic from slot index
```

---

## Arena Allocator Detail

### Alloc(n int) []byte
Carves `n` bytes from the active arena block. No heap allocation in the common case.

```
primary (4KB inline)                        Alloc path
┌────────────────────────────┐
│ [used=1500] slot data ...  │  ← Alloc returns primary.buf[1500:1500+n]
│                            │
│        (free)              │
└────────────────────────────┘

       primary full?
           │  yes
           ▼
extra[0] = arenaPool.Get()   ← Borrowed from global sync.Pool
┌────────────────────────────┐
│ [used=0]                   │  ← Next Alloc returns extra[0].buf[0:n]
└────────────────────────────┘

       extra[0] full? (up to extra[3])
           │  all exhausted or n > 4096
           ▼
       make([]byte, n)        ← Heap fallback (rare)
```

ArenaBlockSize = 4 096 bytes (matches OS page size).
MaxExtraArenas = 4 (up to 16 KB total overflow arena).

### GrowByteSlots(needed int)
Borrows a `slotExtBlock` from `slotExtPool`, copies the existing inline base into it, and re-points `ByteSlots` at the larger backing array. Total capacity after grow: `ExtByteSlots = 64`.

### ReleaseOverflow()
Returns borrowed resources to their pools **before** `Pool.Put`. Must be called by FlowManager.ReturnContext.

```go
// Correct lifecycle (see FlowManager.ReturnContext):
ctx.TakeSlotOverflowKeys()          // get keys to delete from store
for _, k := range keys {
    store.SlotDelete(k)             // clean ephemeral store entries
}
ctx.ReleaseOverflow()               // return arenaBlocks + slotExtBlock to pools
pool.Put(ctx)                       // return Context to pool
```

---

## Pool Lifecycle

```
Pool.New
   └─ ctx.InitSlots()
         ├─ ctx.ByteSlots = ctx.byteSlotBase[:32]     // no make()
         ├─ ctx.IntSlots  = ctx.intSlotBase[:16]       // no make()
         ├─ ctx.BoolSlots = ctx.boolSlotBase[:8]       // no make()
         └─ ctx.active = &ctx.primary

Request start
   └─ ctx = pool.Get()
   └─ ctx.Reset(w)               // zero primary.used, nil slots, reset fields
   └─ ctx.ReqID = fm.reqCounter.Add(1)

Instruction execution
   └─ WriteSlot / ReadSlot via ExecutionState
   └─ ctx.Alloc() for header/query binding

Request end
   └─ FlowManager.ReturnContext(ctx)
         ├─ record ArenaOverflows / SlotOverflows metrics
         ├─ delete DataStore keys (slotOverflowKeys)
         ├─ ctx.ReleaseOverflow()
         └─ pool.Put(ctx)
```

---

## Responsibilities
1. **Request Metadata**: Holds method, path, query — zero-copy views into inline buffers
2. **Logic Slots**: Typed slots for instruction read/write, backed by inline arrays
3. **Arena Allocation**: Carves slot data from inline primary arena; overflows to pool-borrowed blocks then DataStore
4. **Slot Overflow Tracking**: Accumulates DataStore keys for cleanup at request end
5. **Body Buffering**: Optional buffering for transformation
6. **Header Mutations**: Tracks upstream header changes
7. **Tenant Isolation**: Carries TenantID through execution
8. **Observability**: Carries Telemetry and RequestTrace references

---

## Dependencies
- **observability**: Telemetry and tracing integration
- **sync**: Pool for arenaBlock and slotExtBlock (arena.go)
- **fmt**: Key builders (arena.go)

---

## Slot Layout Pattern
```
Per-Request Slot Allocation:
ByteSlots[0-4]   → Internal reserved (method, path, etc.)
ByteSlots[5-N]   → Headers (registered by HeaderRegistry)
ByteSlots[N+1..] → Computed values, cache lookups, path params

Example:
ByteSlots[5]  = "Authorization" header value  (arena-backed)
ByteSlots[6]  = "X-Tenant-ID" header value    (arena-backed)
ByteSlots[10] = Computed auth token            (arena-backed)
ByteSlots[11] = Large response body            (DataStore-backed, sentinel in slot)
ByteSlots[70] = Slot index > 64               (DataStore-backed, slot-idx key)
```

---

## Request/Response Flow
```
HTTP Request
    ↓
Router.Lookup(path) → apiId
    ↓
ctx = pool.Get(); ctx.Reset(w); ctx.ReqID = reqCounter.Add(1)
    ↓
FlowManager.Extract(ctx, req)          ← headers → ctx.Alloc() + ByteSlots
    ↓
engine.Execute(ctx, plan, 0, store)    ← instructions read/write slots via arena
    ↓
Instructions write ResponseStatus, headers, body
    ↓
ctx.Finalize()                         ← flush buffered response
    ↓
FlowManager.ReturnContext(ctx)
    ├─ delete DataStore slot keys
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
- Allows transformations (compression, modification)

---

## Memory Efficiency

### Before (old design)
```go
// Pool.New — three heap allocations per Context
ctx.ByteSlots = make([][]byte, 32)    // heap
ctx.IntSlots  = make([]int64, 16)     // heap
ctx.BoolSlots = make([]bool, 8)       // heap

// Per-request — allocation for every header/query value
ctx.ByteSlots[i] = []byte(header.Get(key))  // heap
```

### After (current design)
```go
// Pool.New — zero heap allocations for slot infrastructure
ctx.InitSlots()  // wires slice headers to inline arrays, no make()

// Per-request — zero allocation for values ≤ 4096 bytes
s := ctx.Alloc(len(val))
copy(s, val)
ctx.ByteSlots[i] = s  // points into primary arenaBlock
```

### Memory sizing at 100K TPS (Little's Law: concurrent = TPS × latency)
```
10ms upstream → ~1 000 concurrent contexts
Primary arena (4KB) × 1 000 = 4 MB
arenaPool extras (4KB × 4 × overflow_rate) — pool-shared, not per-context
slotExtPool (1.8KB × overflow_rate) — pool-shared
DataStore tier — activated only for values > 4 KB or slot index > 64 (rare)
```

---

## Header Mutations
```
MutationLog: [HeaderMutation{...}, HeaderMutation{...}, ...]

HeaderMutation:
  Key:   []byte   (header name)
  Value: []byte   (new value)
  Op:    uint8    (0=Set, 1=Remove)
```

---

## Route Match
```
RouteMatch:
  Plan: any               (compiled execution plan — []engine.Instruction)
  ParamCount: int         (number of path parameters)
  Params: [10]ParamOffset (offsets into path for each param; max 10)

Example:
Path:  /users/123/posts/456
Route: /users/:id/posts/:postId
  Params[0]: ParamOffset{Start: 7, End: 10}   ("123")
  Params[1]: ParamOffset{Start: 17, End: 20}  ("456")
```
