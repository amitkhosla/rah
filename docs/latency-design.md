# Latency Design Document

## Executive Summary

RAH is designed for **nanosecond-aware** latency at every layer. This document outlines the strategies, trade-offs, and optimizations that keep request processing overhead to microseconds while supporting sophisticated workflow compilation and multi-tenant isolation.

---

## Latency Targets

| Component | Target | Achieved | Status |
|-----------|--------|----------|--------|
| Router lookup | <200ns | ~100ns | ✓ |
| Tenant ID lookup | <200ns | ~50-100ns | ✓ |
| Context creation | <500ns | ~100ns | ✓ |
| Per-instruction exec | <1µs | 100-500ns | ✓ |
| Cache read (hit) | <1µs | 500ns | ✓ |
| End-to-end (no upstream) | <5µs | 1-4µs | ✓ |

---

## Zero-GC Strategy

### Why Zero GC?
- **Stop-the-world pauses**: GC can pause all goroutines for milliseconds
- **Unpredictability**: Pause time varies with heap size and fragmentation
- **Tail latency**: p99/p999 latencies dominated by GC, not logic

### Implementation Strategies

#### 1. Stack Allocation for Hot Structures
```go
// ✓ GOOD: ExecutionState on stack, carries SlotOverflow store reference
func Execute(ctx *rctx.Context, table []Instruction, startID int16, store rctx.SlotOverflowStore) {
    state := ExecutionState{SlotOverflow: store}  // Stack-allocated, zero GC
    // ...
}

// ✗ BAD: Would allocate on heap
state := &ExecutionState{...}  // Triggers GC pressure
```

#### 2. Pre-allocated Slices
```go
// ✓ GOOD: Pre-allocated at startup
cache.regions = make([][]*Region, len(sizeClasses))
for i := 0; i < len(sizeClasses); i++ {
    cache.regions[i] = make([]*Region, len(ttlTiers))
}

// ✗ BAD: Dynamic allocation during request
cache.regions = append(cache.regions, &Region{...})
```

#### 3. Object Pooling with Arena-Based Allocation

Context pooling eliminates the per-request `make()` calls that dominated heap pressure in the old design.

```go
// Pool.New — one-time allocations only; slot arrays are inline, no make()
pool.New = func() any {
    ctx := &rctx.Context{
        MutationLog:     make([]rctx.HeaderMutation, 0, 16),
        ResponseHeaders: make([]rctx.HeaderMutation, 32),
    }
    ctx.InitSlots()  // wires ByteSlots/IntSlots/BoolSlots to inline arrays; no make()
    return ctx
}

// Request start — zero alloc for slot infrastructure
ctx := pool.Get().(*rctx.Context)
ctx.Reset(w)
ctx.ReqID = fm.reqCounter.Add(1)  // scope DataStore keys

// Slot data written without heap allocation (values ≤ 4KB)
s := ctx.Alloc(len(val))
copy(s, val)
ctx.ByteSlots[i] = s

// Request end — release borrowed resources before returning to pool
// (Must call ReturnContext, not pool.Put directly)
func (fm *FlowManager) ReturnContext(ctx *rctx.Context) {
    // 1. record ArenaOverflows / SlotOverflows metrics
    // 2. delete ephemeral DataStore slot keys
    // 3. ctx.ReleaseOverflow() — return extra arenaBlocks + slotExt to their pools
    // 4. pool.Put(ctx)
}
```

Old design (replaced):
```go
// ✗ BAD: three heap allocations on every Pool.New
ctx.ByteSlots = make([][]byte, 32)
ctx.IntSlots  = make([]int64, 16)
ctx.BoolSlots = make([]bool, 8)

// ✗ BAD: heap allocation for every header/query value
ctx.ByteSlots[i] = []byte(header.Get(key))
```

#### 4. Offset-Based References (No Pointers)
```go
// ✓ GOOD: Offsets prevent GC scanning
type RegistryNode struct {
    PrefixOffset uint32  // Position in StringPool
    PrefixLen    uint16
    ChildBase    uint32  // Index in Nodes arena
    ChildCount   uint16
    Value        uint16
    Padding      uint16
}

// ✗ BAD: Pointers trigger GC marking
type BadNode struct {
    Prefix  *string  // Pointer → GC marks this
    Children []*BadNode  // All pointers → GC costs
}
```

#### 5. Atomic Values (No Allocations)
```go
// ✓ GOOD: Atomic pointer swap (no GC)
var snapshot atomic.Value
snapshot.Store(routerSnapshot{...})
newSnapshot := snapshot.Load()

// ✗ BAD: Creating new maps/slices
newRoutes := make(map[string]RouteNode)  // Allocation + GC
```

---

## Lock-Free Read Paths

### Why Lock-Free?
- **Contention**: Locks force context switches, CPU stalls
- **Cache coherency**: Lock acquisition causes cache invalidation
- **Tail latency**: One slow reader delays all others with lock contention

### Implementation: Reader-Writer Separation

#### Router (Lock-Free Reads)
```go
// Write path (rare, protected by mutex)
func (r *RahRouter) Add(path string, apiId uint32) {
    r.mu.Lock()
    r.builder.Add(path, apiId)
    nodes, table := r.builder.BakeToArena()
    r.snapshot.Store(routerSnapshot{arena: nodes, table: table})
    r.mu.Unlock()
}

// Read path (hot, zero locks)
func (r *RahRouter) Lookup(path string) uint32 {
    snap := r.snapshot.Load()  // Atomic load, no lock
    // ... traversal with zero locks
    return apiId
}
```

#### Cache Index (Lock-Free Reads)
```go
// Read path (hot, zero locks)
func (b *BigIndex) Get(tenantID uint16, key []byte) (uint64, bool) {
    shardID := tenantID & 0x0F
    shard := b.shards[shardID]
    h := b.stitchHash(tenantID, key)
    idx := h & shard.mask

    ptr := atomic.LoadUint64(&shard.data[idx])  // Atomic load, no lock
    return ptr != 0, ptr
}

// Write path (shard-locked only)
func (b *BigIndex) Set(tenantID uint16, key []byte, pointer uint64) bool {
    shard := b.shards[tenantID & 0x0F]
    shard.mu.Lock()  // Only lock affected shard
    atomic.StoreUint64(&shard.data[idx], pointer)
    shard.mu.Unlock()
    return true
}
```

#### Registry (Lock-Free Reads)
```go
// Read path (hot, zero locks)
func (s *GlobalState) Lookup(tenantAlias string) uint16 {
    registry := s.Active.Load()  // Atomic load, no lock
    // ... radix tree traversal
    return tenantID
}

// Write path (atomic swap, momentary inconsistency okay)
func (s *GlobalState) UpdateRegistry(newRegistry *TenantRegistry) {
    s.Active.Store(newRegistry)  // Atomic store
}
```

### Atomic Operations Overhead
```
Latency comparison (typical):
  Regular memory read:    ~1ns (L1 cache)
  Atomic load:           ~5-10ns (includes fence)
  Mutex lock contention: ~1000ns (context switch, cache invalidation)

Savings: atomic.Load vs mutex is 100-200x faster
```

---

## Instruction-Level Optimization

### Flat Table (No Function Call Overhead)
```go
// ✓ GOOD: Flat table with absolute jumps
GlobalTable: []Instruction{
    {Name: "BindInput", Action: bindInputFunc},  // 0
    {Name: "CacheRead", Action: cacheReadFunc},  // 1
    {Name: "If", Action: ifFunc},                // 2
    {Name: "HTTPCall", Action: httpCallFunc},    // 5
    {Name: "CacheWrite", Action: cacheWriteFunc},// 10
    {Name: "Stop", Action: stopFunc},            // 11
}

// Execution: Jump directly to index, no stack frame
pc = 0 → 1 → 2 → (if true) 5 → 10 → 11 (stop)

// ✗ BAD: Nested function calls
func executeFlow(ctx *Context) {
    bindInput(ctx)      // Stack frame, return address
    if cacheRead(ctx) {
        httpCall(ctx)   // Stack frame, return address
    }
    cacheWrite(ctx)
}
```

### Call Stack Savings
```
Function call overhead:
  Push return address:  ~1ns
  Register allocation:  ~1ns
  Function prologue:    ~2-5ns
  Function epilogue:    ~2-5ns
  Total per call:       ~10-15ns

With 100 instructions: 1-1.5µs savings (15-20% improvement)
```

---

## Memory Layout Optimization

### Cache-Line Alignment
```go
// ✓ GOOD: Entire structure fits in one cache line (~64 bytes)
type GlobalClock struct {
    ElapsedSec  uint32    // 4 bytes
    MinuteID    uint32    // 4 bytes
    HourID      uint32    // 4 bytes
    DayID       uint32    // 4 bytes
    UnixCurTime int64     // 8 bytes
    // Total: 24 bytes (fits in 64-byte cache line)
}

// ✗ BAD: False sharing
type BadClock struct {
    ElapsedSec  uint32    // 4 bytes
    Padding1    [60]byte  // False sharing
    MinuteID    uint32    // On different cache line
}
```

### Arena Allocation (Prefetch Efficiency)
```go
// ✓ GOOD: All nodes contiguous (prefetcher friendly)
arena := []RouteNode{
    {prefixOff: 0, prefixLen: 4, ...},   // Node 0
    {prefixOff: 4, prefixLen: 3, ...},   // Node 1 (adjacent memory)
    {prefixOff: 7, prefixLen: 6, ...},   // Node 2
}

// ✗ BAD: Scattered pointers (cache miss per access)
nodes := []*RouteNode{
    &RouteNode{...},  // Random memory location 1
    &RouteNode{...},  // Random memory location 2
}
```

### Prefetching Impact
```
Contiguous access:   CPU prefetcher loads next cache line automatically
Scattered pointers:  Each access is a cache miss (~200ns penalty)

With 100 nodes:
  Contiguous:   ~100 hits, 1-2 misses → ~1µs
  Scattered:    ~100 misses → ~20µs (20x slower)
```

---

## Context Propagation Efficiency

### Zero-Copy Metadata
```go
// ✓ GOOD: Byte slices (zero-copy views)
type Context struct {
    Method        []byte      // Slice into request buffer
    Path          []byte      // No allocation
    RemainingPath []byte      // Same
    RawQuery      []byte      // Same
}

// ✗ BAD: String copies
type BadContext struct {
    Method string  // Allocates and copies
    Path   string  // Allocates and copies
}

// Savings: 4 string allocations × ~50ns each = ~200ns per request
```

### Inline Arenas and Slot Arrays
```go
// ✓ GOOD: Metadata buffer, slot arrays, and primary arena all inline in Context
type Context struct {
    metadataBuffer [1024]byte      // method + path snapshot — no alloc for most requests
    byteSlotBase   [32][]byte      // backing storage for ByteSlots — no make()
    intSlotBase    [16]int64       // backing storage for IntSlots — no make()
    boolSlotBase   [8]bool         // backing storage for BoolSlots — no make()
    primary        arenaBlock      // 4096-byte arena for slot data — no alloc per request
}

// InitSlots wires the public slice headers to the inline arrays — no make() needed
func (ctx *Context) InitSlots() {
    ctx.ByteSlots = ctx.byteSlotBase[:32]  // slice header only, no allocation
    // ...
    ctx.active = &ctx.primary
}
```

Overflow statistics at typical workloads:
```
Primary arena (4KB) handles:  >99% of requests
Extra arenas (pool-borrowed):  <1% of requests (values or volumes > 4KB)
DataStore tier:               <0.1% of requests (single value > 4KB, or slot index > 64)
```

Each extra arena block is returned to the pool before `pool.Put` — no accumulation in long-lived contexts.

---

## Clock Heartbeat: Anti-Pattern to Remove

### The Real Problem: Unnecessary CPU Spikes
The original rationale focused on avoiding time.Now() latency, but the **real issue is unnecessary CPU spikes** from the background goroutine. This is an anti-pattern because:

1. **High CPU Overhead**: The background goroutine wakes up every 100ms, even when idle, causing constant CPU activity.
2. **Scheduler Thrashing**: Frequent wake-ups can cause unnecessary context switches and scheduler overhead.
3. **Inaccurate Timestamps**: Only updated every 100ms, ±100ms inaccuracy
4. **Unnecessary Complexity**: Added code for negligible benefit and is a constant background activity to the machine.

The goal should be to reduce unnecessary CPU usage and remove constant timers that impact overall performance, regardless of nanosecond latency.

### Current (Anti-Pattern) Approach
```go
// ✗ BAD (Current): Read global clock (avoids time.Now())
age := clock.CurrentClock.ElapsedSec - createdAt    // Might be 100ms stale

// Reason for clock: Avoid time.Now() cost (100-200ns)
// But the benefit is negligible compared to the complexity cost
```

### Recommended Alternatives

**Option 1: Direct time.Now() Calls (Recommended)**
```go
// ✓ GOOD: Direct call for accuracy
age := uint32(time.Now().Unix()) - createdAt    // 100-200ns, perfectly accurate
// Cost: 100-200ns per call
// Benefit: Accuracy, simplicity, no global state
// Trade-off: Acceptable for non-hot paths (worth the simplicity)
```

**Option 2: Request-Scoped Timestamp**
```go
// ✓ GOOD: Cache at request start, reuse throughout
// In rctx.Context:
startTime := time.Now().UnixNano()    // Single call per request
// ... later in instruction execution ...
age := (time.Now().UnixNano() - startTime) / 1_000_000_000    // Reuse cached start

// Cost: Single time.Now() call per request + atomic load in instructions
// Benefit: Consistency, accuracy, no global state
```

### Refactoring Steps
1. Remove `StartClockHeartbeat()` call from `cmd/rah-gateway/main.go`
2. Replace `clock.CurrentClock.ElapsedSec` reads with `time.Now()` or request-scoped values
3. Remove `internal/clock/clock.go` file
4. Remove clock dependency from `internal/engine`, `internal/cache`, `internal/control`
5. Update dependency-graph.md and package documentation

### Current (Anti-Pattern) Approach
```go
// ✗ BAD (Current): Read global clock (avoids time.Now())
age := clock.CurrentClock.ElapsedSec - createdAt    // Might be 100ms stale

// Reason for clock: Avoid time.Now() cost (100-200ns)
// But the benefit is negligible compared to the complexity cost
```

### Recommended Alternatives

**Option 1: Direct time.Now() Calls (Recommended)**
```go
// ✓ GOOD: Direct call for accuracy
age := uint32(time.Now().Unix()) - createdAt    // 100-200ns, perfectly accurate
// Cost: 100-200ns per call
// Benefit: Accuracy, simplicity, no global state
// Trade-off: Acceptable for non-hot paths (worth the simplicity)
```

**Option 2: Request-Scoped Timestamp**
```go
// ✓ GOOD: Cache at request start, reuse throughout
// In rctx.Context:
startTime := time.Now().UnixNano()    // Single call per request
// ... later in instruction execution ...
age := (time.Now().UnixNano() - startTime) / 1_000_000_000    // Reuse cached start

// Cost: Single time.Now() call per request + atomic load in instructions
// Benefit: Consistency, accuracy, no global state
```

### Refactoring Steps
1. Remove `StartClockHeartbeat()` call from `cmd/rah-gateway/main.go`
2. Replace `clock.CurrentClock.ElapsedSec` reads with `time.Now()` or request-scoped values
3. Remove `internal/clock/clock.go` file
4. Remove clock dependency from `internal/engine`, `internal/cache`, `internal/control`
5. Update dependency-graph.md and package documentation

---

## Multitenancy Overhead

### Registry Matrix Lookups (O(1))
```go
// ✓ GOOD: O(1) matrix lookup after tenant ID obtained
tenantID := registry.LookupTenant("acme")      // O(log n) radix lookup
keyID := registry.LookupProperty("api_key")    // O(log n) radix lookup
valueID := matrix[tenantID * stride + keyID]   // O(1) array access
value := valuePool[valueID]                    // O(1) lookup

// Total: 2 log(n) operations + 2 O(1) operations
// Typical: ~200ns for full lookup + retrieval

// ✗ BAD: Full map lookup per property
config := getConfig(tenantID, propertyName)    // HashMap lookup O(1) but cached,
// ...plus string hashing overhead (~50ns per lookup)
```

---

## Batch Processing

### Per-Request Overhead
```
Best case (no upstream):
  Router lookup:         100ns
  Context creation:      100ns
  Instruction execution: 1000ns
  Response assembly:     500ns
  ──────────────────────────────
  Total:                 1700ns (1.7µs)

With 1 upstream call (100ms):
  Overhead:               1.7µs
  Upstream:             100ms
  Total:                100.0017ms
  Overhead percentage:   0.0017% (negligible)

With 10 upstream calls (100ms each):
  Overhead:               1.7µs
  Upstreams:            1000ms
  Total:                1000.0017ms
  Overhead percentage:   0.00017% (negligible)
```

---

## Observability Impact

### Conditional Instrumentation
```go
// ✓ GOOD: Only measure if observability enabled
if shouldMeasure {
    started := time.Now()
    pc = current.Action(ctx, &state)
    duration := time.Since(started)
    ctx.Obs.RecordInstruction(current.Name, duration)
}

// Overhead when disabled: ~0ns (branch prediction)
// Overhead when enabled: ~100-200ns per instruction
// (optional, can be disabled for production)
```

---

## GC Tuning

### Runtime Configuration
```go
// Suppress GC if latency-critical
runtime.GOMAXPROCS(runtime.NumCPU())
debug.SetMaxStack(1 << 20)  // 1MB stack per goroutine
// (Allows more stack variables, less heap allocation)

// Monitor GC
import "runtime/debug"
debug.SetGCPercent(25)  // More aggressive GC (can reduce pause time)
```

---

## Benchmarking Methodology

### Microbenchmarks
```bash
go test -bench=. -benchmem -cpuprofile=cpu.prof

Expected:
  BenchmarkRouterLookup    1000000    1234 ns/op    0 B/op    0 allocs/op
  BenchmarkCacheRead       100000     12345 ns/op   0 B/op    0 allocs/op
  BenchmarkExecute         50000      23456 ns/op   0 B/op    0 allocs/op
```

### Latency Percentiles
```
Load test with varying concurrency:
  p50:  2-3µs
  p99:  5-10µs (occasional GC)
  p999: 50-100µs (GC pause)

Good result: p99 within 5x p50
```

---

## Worst-Case Scenarios

### GC Pause
```
Typical: 1-5 goroutines get paused during GC
Pause time: 10-100ms (depends on heap size)
Requests affected: Those landing during GC pause

Mitigation: Keep heap small, use pooling
```

### Lock Contention
```
Shard lock (cache write): 1-5µs hold time
Multiple writers to same shard: Queuing
Mitigation: Use 16 shards (CPU core parity)
```

### Context Switch
```
Goroutine swap: ~1-5µs overhead
High concurrency without lock-free design: Frequent switches
Mitigation: Minimize locks, use atomic operations
```

---

## Related Documentation
- **Architecture**: See [ARCHITECTURE.md](ARCHITECTURE.md)
- **Cache Design**: See [packages/cache.md](packages/cache.md)
- **Engine Details**: See [packages/engine.md](packages/engine.md)
- **Performance Testing**: Use CPU/memory profiles with `go test -cpuprofile`, `go tool pprof`
