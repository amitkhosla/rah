# Dependency Graph

## Package-Level Dependencies

### Startup Phase Dependencies

```
main (cmd/rah-gateway)
 │
 ├─→ config
 │    └─ (no dependencies)
 │
 ├─→ control
 │    ├─→ config
 │    ├─→ engine
 │    │    ├─→ rctx
 │    │    │    └─→ observability (optional)
 │    │    ├─→ observability
 │    │    └─→ clock
 │    ├─→ engine/steps
 │    │    ├─→ engine
 │    │    ├─→ datastore
 │    │    └─→ cache
 │    ├─→ router
 │    ├─→ datastore
 │    │    └─→ config
 │    ├─→ cache
 │    │    ├─→ smartcache
 │    │    ├─→ observability
 │    │    └─→ clock
 │    └─→ registry
 │
 ├─→ datastore
 │    └─→ config
 │
 └─→ router
      └─ (no dependencies)
```

### Runtime Phase Dependencies

```
HTTP Request
 │
 ├─→ router.Lookup(path)
 │    └─ (lock-free, no dependencies)
 │
 ├─→ rctx.Context creation
 │    └─→ observability (optional)
 │
 ├─→ registry.Lookup(tenant)
 │    └─ (lock-free, no dependencies)
 │
 ├─→ engine.Execute(ctx, table)
 │    ├─→ rctx.Context (input)
 │    ├─→ observability (optional)
 │    └─→ engine/steps (instruction functions)
 │         ├─→ cache
 │         │    ├─→ smartcache
 │         │    └─→ observability
 │         ├─→ datastore
 │         ├─→ observability
 │         └─→ clock
 │
 └─→ Response generation
      └─→ rctx.Context (output)
```

---

## Dependency Details

### By Package

#### main (cmd/rah-gateway)
- **Imports**: config, control, datastore, engine, observability, rctx, router
- **Import Phase**: Startup only
- **Dependency Count**: 7 packages
- **Graph Position**: Root node

#### control
- **Imports**:
  - config (configuration types)
  - engine (Instruction, FlowManager)
  - engine/steps (instruction builders)
  - router (route registration)
  - datastore (loading definitions)
  - cache (cache operations)
  - registry (tenant lookup)
  - observability (optional metrics)
  - clock (timestamps)
- **Import Phase**: Startup (compilation phase)
- **Dependency Count**: 9 packages
- **Graph Position**: Hub (depends on most others)
- **Critical Path**: config → engine → steps → observability

#### engine
- **Imports**: rctx, observability, clock
- **Import Phase**: Startup + Runtime (execution)
- **Dependency Count**: 3 packages
- **Graph Position**: Core execution engine
- **Hot Path**: Yes (every request)

#### router
- **Imports**: (none)
- **Import Phase**: Startup + Runtime (path lookup)
- **Dependency Count**: 0
- **Graph Position**: Independent (leaf node)
- **Hot Path**: Yes (every request)

#### rctx
- **Imports**: observability (optional)
- **Import Phase**: Runtime (request context)
- **Dependency Count**: 1 package (optional)
- **Graph Position**: Data carrier
- **Hot Path**: Yes (every request)

#### cache
- **Imports**: smartcache, observability, clock
- **Import Phase**: Startup + Runtime
- **Dependency Count**: 3 packages
- **Graph Position**: Optional (only if cache enabled)
- **Hot Path**: Yes (if enabled)

#### datastore
- **Imports**: config
- **Import Phase**: Startup + Runtime
- **Dependency Count**: 1 package
- **Graph Position**: Independent
- **Hot Path**: Yes (if accessed in workflow)

#### config
- **Imports**: (none)
- **Import Phase**: Startup only
- **Dependency Count**: 0
- **Graph Position**: Leaf node (no dependencies)

#### registry
- **Imports**: (none)
- **Import Phase**: Startup + Runtime
- **Dependency Count**: 0
- **Graph Position**: Independent (leaf node)
- **Hot Path**: Yes (for tenant lookup)

#### observability
- **Imports**: (none, but uses interfaces)
- **Import Phase**: Runtime only (optional)
- **Dependency Count**: 0
- **Graph Position**: Leaf node (no dependencies)

#### clock (ANTIPATTERN - SCHEDULED FOR REMOVAL)
- **Imports**: (stdlib only)
- **Import Phase**: Startup + Runtime
- **Dependency Count**: 0
- **Graph Position**: Leaf node (no dependencies)
- **Status**: ⚠️ **Anti-pattern** - Uses background goroutine to update global clock
- **Issue**: Unnecessary complexity, accuracy drift (±100ms), resource contention
- **Recommendation**: Remove and use `time.Now()` or request-scoped timestamps instead
- **See**: [docs/latency-design.md#clock-heartbeat-anti-pattern-to-remove](latency-design.md#clock-heartbeat-anti-pattern-to-remove)

#### smartcache
- **Imports**: (stdlib only)
- **Import Phase**: Startup (cache initialization)
- **Dependency Count**: 0
- **Graph Position**: Leaf node (no dependencies)

#### engine/steps
- **Imports**: engine, datastore, cache, observability, clock
- **Import Phase**: Startup (compilation)
- **Dependency Count**: 5 packages
- **Graph Position**: Instruction library
- **Note**: Sub-package of engine

---

## Circular Dependencies

### Checks
```
✓ No circular dependencies detected

Verified paths:
  control → engine → rctx: acyclic
  control → cache → smartcache: acyclic
  control → datastore → config: acyclic
  engine → observability: acyclic (one-way)
  rctx → observability: optional, one-way
```

---

## Import Statistics

### Total Unique Packages: 12

| Package | Incoming | Outgoing | Internal | External | Rank |
|---------|----------|----------|----------|----------|------|
| config | 3 | 0 | 0 | 0 | Leaf |
| observability | 5 | 0 | 0 | 0 | Leaf |
| clock | 3 | 0 | 0 | 1 | Leaf |
| smartcache | 1 | 0 | 0 | 1 | Leaf |
| registry | 2 | 0 | 0 | 1 | Leaf |
| router | 2 | 0 | 0 | 1 | Leaf |
| datastore | 4 | 1 | 0 | 0 | Mid |
| rctx | 4 | 1 | 0 | 0 | Mid |
| cache | 3 | 3 | 0 | 1 | Mid |
| engine | 5 | 3 | 0 | 0 | Hub |
| engine/steps | 3 | 5 | 0 | 0 | Hub |
| control | 1 | 9 | 0 | 0 | Root |

### Graph Layers

```
Layer 0 (Leaves - no internal dependencies):
  config, observability, clock, smartcache, registry, router

Layer 1 (Mid-level - depends on Layer 0):
  datastore (→ config)
  rctx (→ observability)
  cache (→ smartcache, observability, clock)

Layer 2 (Core logic - depends on Layers 0-1):
  engine (→ rctx, observability, clock)
  engine/steps (→ engine, datastore, cache, observability, clock)

Layer 3 (Compiler - depends on all previous):
  control (→ config, engine, engine/steps, router, datastore, cache, registry)

Layer 4 (Entry point):
  main (→ control, config, engine, observability, rctx, router, datastore)
```

---

## Critical Paths (Impact Analysis)

### Path 1: Configuration to Execution
```
config
  → control.Compiler
    → engine.Execute()
      → engine/steps (instructions)
        ↳ cache.Get()
        ↳ datastore.Get()
        ↳ observability.Record()

Impact: Change in config affects compilation, not runtime execution
Latency: Startup time only, zero runtime impact
```

### Path 2: Request Execution
```
router.Lookup()
  → rctx.Context
    → engine.Execute()
      → instruction actions
        ↳ cache.Read() (lock-free, sharded)
        ↳ datastore.Get() (backend-dependent)
        ↳ observability.Record() (optional)

Impact: Every request depends on these
Latency: Defines p50 latency (1-4µs typical)
Contention: Sharding and lock-free design limit impact
```

### Path 3: Tenant Lookup
```
registry.LookupTenant()
  → radix tree lookup
    → matrix array access
      → value pool lookup

Impact: Tenant isolation enforcement
Latency: 200-500ns typical
Contention: Zero (lock-free atomic pointer)
```

---

## Version Compatibility Risks

### Breaking Changes
- Changing `Instruction` signature breaks step builders
- Changing `Context` layout breaks existing steps
- Changing datastore interface requires all backends
- Changing registry matrix layout requires rebuild

### Backward Compatible Changes
- Adding new instruction types (append to engine/steps)
- Adding new datastore backends (factory pattern)
- Adding new cache size classes (circle buffer compatibility)
- Adding new clock metrics (additive only)

---

## External Dependencies

### Stdlib Only
- config, registry, router, smartcache, observability, clock
- **Impact**: No external dependency risks

### Minimal External
- engine: stdlib + internal
- datastore: backend libraries (Redis, PostgreSQL, MongoDB, Cassandra, etc.)

### Total External Count: ~8 (datastore backends)

```go
// datastore backends
import (
    "github.com/go-redis/redis"              // Redis
    "github.com/lib/pq"                      // PostgreSQL
    "go.mongodb.org/mongo-driver"            // MongoDB
    "github.com/gocql/gocql"                 // Cassandra
    "github.com/dragonflydb/dragonfly-go"    // Dragonfly
    // Disk and File backends use only stdlib
)
```

---

## Dependency Management Best Practices

### 1. **Avoid Circular Dependencies**
- ✓ Control → Engine → RCtx (acyclic)
- ✗ Engine → Control (would cause circular)

### 2. **Minimize Hot-Path Dependencies**
- ✓ Router, RCtx, Engine (minimal deps)
- ✗ Control on critical path (startup only)

### 3. **Keep Leaves Independent**
- ✓ Config, Registry, Clock, Observability (no internal deps)
- ✗ Adding deps to leaves cascades up the graph

### 4. **Version External Dependencies**
- Pin datastore backends to stable versions
- Test backend compatibility on major upgrades
- Consider feature flags for backend availability

---

## Recommended Module Structure

For import organization:
```
D:\GoLand\rah\
├── cmd\
│   └── rah-gateway\
│       └── main.go          (import control, config, ...)
├── internal\
│   ├── engine\
│   │   ├── executor.go
│   │   ├── plan.go
│   │   └── steps\           (instruction library)
│   ├── control\             (compiler)
│   ├── router\              (routing)
│   ├── cache\               (caching)
│   ├── datastore\           (KV store)
│   ├── registry\            (tenant management)
│   ├── rctx\                (request context)
│   ├── config\              (configuration)
│   ├── observability\       (telemetry)
│   ├── smartcache\          (indexing)
│   ├── clock\               (timing)
│   └── observability\       (telemetry)
└── docs\                    (this documentation)
```

---

## Testing Strategy by Dependency

| Component | Test Type | Deps Tested | Isolation |
|-----------|-----------|-------------|-----------|
| config | Unit | None | Full mock |
| registry | Unit | None | Full mock |
| clock | Unit | None | Full mock |
| router | Unit | None | Full mock |
| smartcache | Unit | None | Full mock |
| cache | Unit | smartcache, clock | Mocked clock |
| rctx | Unit | observability | Mocked obs |
| datastore | Integration | config | Real backends |
| engine | Unit | rctx | Mocked context |
| engine/steps | Integration | engine, datastore, cache | Real deps |
| control | Integration | All | Full system |
| main | E2E | All | Full system |

---

## Related Documentation
- **Architecture**: See [ARCHITECTURE.md](ARCHITECTURE.md)
- **Package Details**: See [packages/](packages/) directory
