# RAH Architecture Overview

## System Introduction

RAH is a **configurable API/AI gateway** in Go optimized for nanosecond-level latency. It combines:
- **Execution engine** for compiled workflows
- **Zero-GC slab cache** with smart indexing
- **Multi-backend datastore** abstraction
- **Lock-free routing** with immutable snapshots
- **Tenant isolation** via registry matrix
- **Comprehensive observability** for monitoring

**Core Value**: RAH enables companies to build sophisticated multi-tenant API gateways with minimal latency overhead.

---

## Component Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      HTTP Request                                │
└──────────────────────────┬──────────────────────────────────────┘
                           ▼
                   ┌──────────────────┐
                   │  RahRouter       │ (lock-free lookup)
                   │  Lookup(path)    │ ─► apiId
                   └────────┬─────────┘
                            ▼
                   ┌──────────────────┐
                   │  rctx.Context    │ (request data plane)
                   │  - slots         │
                   │  - buffers       │
                   │  - tenant info   │
                   └────────┬─────────┘
                            ▼
          ┌─────────────────────────────────┐
          │  Registry (Tenant Lookup)       │
          │  Validate tenant, get config    │
          └────────┬────────────────────────┘
                   ▼
       ┌───────────────────────────┐
       │  engine.Execute()         │
       │  (instruction loop)       │
       │                           │
       │  ┌─────────────────────┐  │
       │  │ Instruction Table   │  │
       │  │ [compiled at init]  │  │
       │  └─────────────────────┘  │
       │           ▼               │
       │  ┌─────────────────────┐  │
       │  │ For each step:      │  │
       │  │ - cache_read        │  │
       │  │ - http_call         │  │
       │  │ - transform         │  │
       │  │ - cache_write       │  │
       │  └─────────────────────┘  │
       │           ▼               │
       │  ┌─────────────────────┐  │
       │  │ Datastore Access    │◄─┤─ [Pluggable Backends]
       │  │ (multi-backend)     │  │   - Redis
       │  └─────────────────────┘  │   - PostgreSQL
       │           ▼               │   - MongoDB
       │  ┌─────────────────────┐  │   - Cassandra
       │  │ Cache (zero-GC)     │◄─┤   - Dragonfly
       │  │ - BigIndex          │  │   - Disk
       │  │ - SmartCache        │  │   - File
       │  └─────────────────────┘  │
       │           ▼               │
       │  ┌─────────────────────┐  │
       │  │ Observability       │  │
       │  │ - Instruction time  │  │
       │  │ - Upstream latency  │  │
       │  │ - Metrics/Tracing   │  │
       │  └─────────────────────┘  │
       └───────────────┬───────────┘
                       ▼
        ┌──────────────────────────┐
        │ Upstream Call (optional) │
        │ (with instrumentation)   │
        └────────────┬─────────────┘
                     ▼
        ┌──────────────────────────┐
        │ Response Assembly        │
        │ - Status                 │
        │ - Headers                │
        │ - Body (transformed)     │
        └────────────┬─────────────┘
                     ▼
        ┌──────────────────────────┐
        │ Return rctx.Context      │
        │ to Pool                  │
        └──────────────────────────┘
```

---

## Request Flow (Detailed)

### 1. **Routing Phase** (~50-100ns)
- HTTP request arrives with path
- RahRouter.Lookup(path) performs lock-free arena traversal
- Returns apiId via longest-prefix matching
- Zero allocations, atomic snapshot load

### 2. **Context Creation** (~100ns)
- Allocate rctx.Context from pool (zero alloc — context and slots are inline arrays, no make())
- Assign monotonic `ReqID` for DataStore key scoping
- Populate metadata (method, path, query, headers) — slot data carved from inline arena, zero heap allocation for values ≤ 4 KB

### 3. **Tenant Identification** (~50-100ns)
- Registry.LookupTenant(tenantAlias) via radix tree
- Get TenantID from immutable atomic snapshot
- Validate tenant quota if applicable

### 4. **Execution** (variable: 1-100µs+)
- Fetch compiled Plan from FragmentMap (index lookup)
- `ExecutionState` allocated on goroutine stack (no GC) — carries `SlotOverflow` store reference
- Execute instruction loop with absolute jumps
  - Each instruction is a function pointer returning absolute next PC
  - Slot reads/writes via `ExecutionState.WriteSlot` / `ReadSlot` — three-tier: arena → pool-borrowed block → DataStore

### 5. **Instruction Execution** (per-instruction: 100-1000ns)
Example flow:
```
Instruction 0: BindInput → Extract header into slot
Instruction 1: CacheRead → Lookup key, populate slot
Instruction 2: If (condition) → Jump to 5 or 10
Instruction 5: HTTPCall → Call upstream, populate slot
Instruction 10: CacheWrite → Store result in cache
Instruction 11: StopPlan → Exit execution
```

### 6. **Datastore Access** (backend-dependent)
- Local stores (Disk, File): µs latency
- In-memory (Redis, Dragonfly): 100-500µs (network)
- Relational (PostgreSQL): 1-10ms (query)
- Distributed (Cassandra): 10-50ms (consistency)

### 7. **Upstream Call** (variable: 10-500ms+)
- If instructions include http_call step
- Execute upstream request with instrumentation
- Record latency in observability
- Optional request/response transformation

### 8. **Response Assembly** (~500ns-1µs)
- Combine status, headers, body
- Apply header mutations (proxy transformations)
- Write to response writer (may buffer or stream)

### 9. **Cleanup** (~100ns + store deletes if overflow occurred)
- `FlowManager.ReturnContext(ctx)`:
  1. Record `ArenaOverflows` / `SlotOverflows` metrics
  2. Delete ephemeral DataStore keys written this request (only when slot overflow occurred)
  3. `ctx.ReleaseOverflow()` — return pool-borrowed arena blocks and slot extension back to their pools
  4. `pool.Put(ctx)` — make context available for next request

---

## Package Dependencies

### Direct Dependencies (Compilation Layers)
```
command (main.go)
  └─ control (compiler + setup)
     ├─ engine (execution)
     │  ├─ rctx (request context)
     │  ├─ observability (telemetry)
     │  ├─ clock (timing)
     │  └─ engine/steps (instruction builders)
     ├─ router (routing)
     ├─ config (configuration)
     ├─ datastore (multi-backend KV)
     │  └─ config
     ├─ registry (tenant management)
     └─ cache (zero-GC slab cache)
        ├─ smartcache (indexing)
        ├─ observability
        └─ clock

request-time
  ├─ router (path lookup)
  ├─ registry (tenant lookup)
  ├─ rctx (context carrier)
  ├─ engine (execution)
  │  ├─ steps (instruction types)
  │  ├─ observability
  │  └─ clock
  ├─ datastore (KV access)
  └─ cache (data lookup)
```

### Dependency Matrix
| Package | Depends On | Used By | Startup/Runtime |
|---------|-----------|---------|-----------------|
| engine | rctx, observability, clock, steps | control, router | Runtime |
| router | - | main, control | Startup, Runtime |
| rctx | observability | engine, control | Runtime |
| cache | smartcache, observability, clock | control, engine | Runtime |
| datastore | config | control, main | Runtime |
| control | engine, router, config, datastore, cache | main | Startup |
| config | - | control, datastore | Startup |
| registry | - | control, rctx | Startup, Runtime |
| observability | - | engine, rctx, cache | Runtime |
| smartcache | - | cache | Startup |
| clock | - | cache, control, observability | Runtime |

---

## Key Design Decisions

### 1. **Flat Instruction Table**
- **Why**: Absolute jumps faster than nested function calls
- **Trade-off**: Compiler complexity vs execution speed
- **Result**: 1-2% latency improvement on call-heavy flows

### 2. **Stack-Allocated ExecutionState**
- **Why**: Zero GC pressure during execution
- **Design**: LinkStack[16] for call returns, PC, IsStopped flag
- **Limit**: Max 16 nested function calls (architectural choice)

### 3. **Lock-Free Routing**
- **Why**: Hot path cannot take locks
- **Implementation**: Atomic snapshot of immutable arena
- **Write cost**: Rebuild entire arena on route change (rare operation)
- **Read benefit**: Zero-lock Lookup() operations

### 4. **Deterministic Cache Memory**
- **Why**: Predictable footprint, no GC pauses
- **Implementation**: Preallocate 2D region matrix [sizeClass][ttlTier]
- **Trade-off**: Waste if some size/TTL combinations unused

### 5. **Tenant Matrix Registry**
- **Why**: O(1) property lookups after tenant ID obtained
- **Implementation**: Matrix[TenantID * Stride + PropertyID] = ValueID
- **Scaling**: Limited to ~64K tenants per instance

### 6. **Pluggable Datastore**
- **Why**: Different domains have different requirements
- **Design**: KeyValueStore interface with factory
- **Binding**: Domains → Stores configured at startup
- **Examples**: Cache queries hit Redis, configs hit Disk

### 7. **Per-Instruction Timing**
- **Why**: Identify bottleneck steps
- **Conditional**: Only recorded if observability.InstructionTimingEnabled()
- **Overhead**: ~10-50ns per instruction if enabled

### 8. **Arena-Based Slot Memory with DataStore Overflow**
- **Why**: Eliminate per-request `make()` calls for slot data; maintain predictable GC behaviour
- **Design**: Three tiers for every slot write:
  1. **Inline primary arena** (4 096 bytes embedded in Context) — covers the vast majority of requests; zero heap allocation
  2. **Pool-borrowed overflow arenas** (up to 4 × 4 096 bytes, returned before `pool.Put`) — handles requests with large but bounded slot data
  3. **DataStore spill** (disk / GCS / Redis) — activated only when a single value exceeds 4 096 bytes, or when slot index exceeds 64; keys are request-scoped via `ReqID` and deleted at request end
- **Slot headers**: `ByteSlots / IntSlots / BoolSlots` backed by inline arrays in Context — no `make()` at pool creation
- **Store propagation**: `SlotOverflowStore` reference lives on stack-allocated `ExecutionState` and in `FlowRegistry`; `rctx.Context` holds only a `ReqID` and key-tracking slice
- **Monitoring**: `OverflowMetrics.ArenaOverflows` / `SlotOverflows` exposed at `/debug/arena`; non-zero rates signal that default arena or slot sizing needs tuning

---

## Performance Characteristics

### Typical Request Latency Breakdown (Best Case)
```
                   Latency    Percentage
Router lookup      ~100ns     1-2%
Tenant ID lookup   ~50ns      <1%
Context creation   ~100ns     1-2%
Instruction exec   ~1µs       10-20%
Cache read         ~500ns     5-10%
Response assembly  ~1µs       10-20%
Write headers      ~500ns     5-10%
──────────────────────────────────────
Total (no upstream) ~4µs      100%

With upstream call ~100-500ms (2000-10000x slower)
  Gateway overhead: 0.4-5% of total latency
```

### Scaling Characteristics
| Scenario | Improvement | Key Factor |
|----------|-------------|-----------|
| Read-heavy cache | 16x | 16-shard lock-free BigIndex |
| Multi-tenant | Sublinear | Registry matrix O(1) lookup |
| Many routes | No degradation | Arena pre-computed, immutable |
| High concurrency | Linear to #cores | Lock-free reads, sharded writes |

---

## Operational Considerations

### Startup Time
1. Load configuration (Datastore bindings)
2. Initialize Datastore managers
3. Read API definitions from store
4. Compile all flows into GlobalTable
5. Build RahRouter from API paths
6. Load tenant registry
7. Initialize cache regions
8. Start clock heartbeat

**Total**: 10ms - 1s depending on configuration complexity

### Memory Usage
```
Fixed costs:
  - Cache regions:       configured via SizeClasses × TTLTiers
  - Router arena:        ~1KB per 100 routes
  - Registry:            ~100B per tenant property
  - Instruction table:   ~100B per instruction

Per-request (new arena design):
  - Context struct:      ~6.5KB (includes 4KB inline primary arena + inline slot arrays)
  - Arena overflow pool: shared across all contexts; borrowed as needed, returned before pool.Put
  - Slot ext pool:       ~1.8KB per borrowed block; returned before pool.Put
  - DataStore spill:     only when single value > 4KB or slot index > 64 (rare)

At 100K TPS, 10ms upstream (Little's Law: ~1 000 concurrent):
  - Active Context pool: ~1 000 × 6.5KB ≈ 6.5MB
  - Extra arenas (pool): shared, not per-context — negligible at typical overflow rates
```

### Concurrency Model
- **Router reads**: Lock-free, atomic snapshots
- **Cache reads**: Lock-free, atomic loads per shard
- **Cache writes**: Shard-level locks (16 shards)
- **Datastore**: Backend-dependent (typically pooled connections)
- **Registry**: Lock-free atomic pointer swap

---

## Extension Points

### Adding Custom Instruction Types
1. Define function in engine/steps/
2. Create builder function
3. Register in Compiler.compileStep()
4. Implement InstructionFunc signature

### Adding Datastore Backend
1. Implement KeyValueStore interface
2. Create factory function in datastore/
3. Add to StoreKind enum in config
4. Register in datastore.NewStore()

### Custom Observability Export
1. Implement trace recorder interface
2. Hook into observability.AppendInstructionEvent()
3. Export via observability.GetMetrics()

### Tenant Registry Updates
1. Build new TenantRegistry immutably
2. Atomic store to GlobalState.Active
3. Push deleted IDs to FreeSlots

---

## Related Documentation

- **Engine Details**: See [packages/engine.md](packages/engine.md)
- **Cache Design**: See [packages/cache.md](packages/cache.md)
- **Routing Algorithm**: See [packages/router.md](packages/router.md)
- **Compiler Flow**: See [packages/control.md](packages/control.md)
- **Latency Optimization**: See [latency-design.md](latency-design.md)
- **Package Dependencies**: See [dependency-graph.md](dependency-graph.md)

---

## Glossary

| Term | Definition |
|------|-----------|
| **Data Plane** | Request context and execution (rctx.Context) |
| **Control Plane** | ExecutionState with PC and link stack |
| **Instruction** | Single unit of work with name and action function |
| **Fragment** | Named subflow that can be called via instruction |
| **Plan** | Compiled API definition with instruction list |
| **Tenant** | Logical isolation unit (user/customer) |
| **Domain** | Logical data category (APIs, Flows, Cache, etc.) |
| **Store** | Physical backend (Redis, PostgreSQL, etc.) |
| **Region** | Circular buffer for cache entries of size class × TTL |
| **Snapshot** | Immutable tree/data structure (router, registry) |
| **StopPlan** | Sentinel value (-1) to terminate execution |
| **Shard** | Partition of index for concurrency (cache, smartcache) |
| **Primary Arena** | Inline 4 096-byte block embedded in Context — slot data is carved from here first |
| **Overflow Arena** | Pool-borrowed 4 096-byte block used when primary fills; released before pool.Put |
| **SlotExtBlock** | Pool-borrowed block extending ByteSlots/IntSlots/BoolSlots beyond the inline base count |
| **SlotOverflowStore** | DataStore-backed third tier for slot values exceeding arena capacity or slot indices beyond 64 |
| **Sentinel** | Two-byte prefix `[0x00, 0xFF]` stored in a ByteSlot to signal the real value lives in the DataStore |
| **ReqID** | Monotonic per-request uint64 that scopes DataStore keys so concurrent requests never collide |
| **ReturnContext** | FlowManager method that orchestrates store key cleanup → overflow release → pool.Put |
