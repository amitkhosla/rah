# RAH Developer Guide

## Getting Started

### Prerequisites
- Go 1.26+ (for generics support)
- Git for version control
- (Optional) Docker for running datastore backends

### Quick Setup

```bash
# Clone repository
git clone <repo-url>
cd rah

# Verify Go version
go version  # Should be 1.26+

# Download dependencies
go mod download

# Build the gateway
go build -o bin/rah-gateway ./cmd/rah-gateway/

# Run the gateway
./bin/rah-gateway -port 8080 -mport 8081
```

### Configuration

RAH uses environment variables and config files for setup.

**Default Configuration** (in main.go):
```go
cfg := config.GlobalLayout{
    MaxBytesSlots: 32,
    MaxIntsSlots:  16,
    DefaultLimits: config.ResourceLimit{
        MaxBodySize: 1024 * 1024,  // 1MB
    },
}

dataStores := config.DataStoreConfig{
    Stores: map[string]config.StoreConfig{
        "local_disk": {
            Name:    "local_disk",
            Kind:    config.StoreDisk,
            Enabled: true,
            Connection: config.StoreConnection{
                Path: "/var/lib/rah",
            },
        },
    },
    Bindings: map[config.DataDomain]string{
        config.DomainAPIDefinitions: "local_disk",
        config.DomainFlows:          "local_disk",
        // ... more bindings
    },
}
```

**To change backends**:
1. Edit datastore config in `cmd/rah-gateway/main.go`
2. Add appropriate StoreConfig entry
3. Update Bindings map to route domains to stores
4. Rebuild: `go build -o bin/rah-gateway ./cmd/rah-gateway/`

---

## Development Workflow

### 1. Understanding the Codebase

**Start here**:
```bash
# Read architecture overview
cat docs/ARCHITECTURE.md

# Understand your package
cat docs/packages/engine.md      # or relevant package
cat docs/dependency-graph.md     # understand dependencies
```

**Then**:
```bash
# Look at actual code
less internal/engine/executor.go
less internal/control/compiler.go
```

### 2. Running Tests

```bash
go test -skip=. ./internal/...

# Run specific package tests (if not skipping)
go test -v ./internal/engine/

# Benchmark specific functionality
go test -bench=. -benchmem ./internal/engine/
```

### 3. Performance Testing

```bash
# Generate CPU profile
go test -cpuprofile=cpu.prof -bench=. -benchtime=10s ./internal/engine/

# Analyze profile
go tool pprof cpu.prof

# Generate memory profile
go test -memprofile=mem.prof -bench=. -benchtime=10s ./internal/engine/
go tool pprof mem.prof
```

### 4. Making Changes

**Example: Add new instruction type**

```bash
# 1. Read documentation
cat docs/packages/engine.md
cat docs/packages/control.md

# 2. Create instruction
# File: internal/engine/steps/my_step.go
cat > internal/engine/steps/my_step.go << 'EOF'
package steps

import (
    "rah/internal/engine"
    "rah/internal/rctx"
)

func MyStep(slotIndex int, param string) engine.Instruction {
    return engine.Instruction{
        Name: "MY_STEP",
        Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
            // Implementation here
            return state.PC + 1
        },
    }
}
EOF

# 3. Register in compiler
# Edit: internal/control/compiler.go
# Add case in compileStep():
# case "my_step":
#     slot := c.getSlot(step.Variable)
#     c.GlobalTable = append(c.GlobalTable, steps.MyStep(slot, step.Param))

# 4. Test the change
go test -v ./internal/control/

# 5. Update documentation
# Edit: docs/packages/engine.md
# Add MyStep to step compilation section
```

### 5. Using Pattern Matching in Flows

Pattern matching allows conditional routing based on regex patterns. See `docs/packages/pattern_matching.md` for full details and examples.

**Quick Start**:

```yaml
action: if
condition:
  type: pattern_match
  source: header          # or: query, body, path
  sourceKey: x-service    # header/query/body field name
  pattern: "^api-"        # regex pattern
  flags: "i"              # optional: i, m, s, x
then_steps:
  - action: http_call
    upstream_url: "https://api-backend"
else_steps:
  - action: http_call
    upstream_url: "https://web-backend"
```

**Key Points**:

- Patterns are **compiled at deploy time** (bake-time), not per-request
- Runtime matching is ~400-500ns (well within <5µs latency budget)
- Use `flags: "i"` for case-insensitive matching
- Supports full Go `regexp` syntax (RE2 dialect)
- Pattern errors fail at deployment with clear messages

**Common Examples**:

- Service routing: `"^(api|data)-"` matches "api-gateway", "data-processor"
- Email validation: `"^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$"`
- UUID v4: `"^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"`
- SemVer: `"^v\\d+\\.\\d+\\.\\d+(-[a-z0-9.]+)?$"` with flags: "i"

### 6. Debugging Issues

**Performance regression**:
```bash
# 1. Identify regression
go test -bench=. -benchmem ./internal/engine/ > new.txt
# Compare with old run

# 2. Profile to find bottleneck
go test -cpuprofile=cpu.prof -bench=. ./internal/engine/
go tool pprof cpu.prof

# 3. Check if issue is:
#    - Lock contention (high mutex lock time)
#    - Allocation (high malloc/free)
#    - Cache misses (stalls in profiles)

# 4. Review docs/latency-design.md for solution pattern
```

**Execution errors**:
```bash
# 1. Check instruction PC calculation
# Each instruction must return next PC correctly
# -1 (StopPlan) exits execution

# 2. Verify instruction Action signature
# Must be: func(ctx *rctx.Context, state *ExecutionState) int16

# 3. Check slot allocation
# Compiler manages slot indices via slotMap
# Instructions access slots via index
```

**Tenant isolation bugs**:
```bash
# 1. Verify TenantID used correctly
# - Registry lookups use TenantID
# - Cache operations pass TenantID
# - Datastore queries include tenant prefix

# 2. Check registry matrix access
# valueID := matrix[tenantID * stride + keyID]

# 3. Verify cache sharding by tenantID
# shardID := tenantID & 0x0F
```

---

## Common Tasks

### Add New Instruction Type
See example above in section 4.

### Add New Datastore Backend

**File structure**:
```bash
# Create: internal/datastore/mybackend_store.go
cat > internal/datastore/mybackend_store.go << 'EOF'
package datastore

import "rah/internal/config"

func newMyBackendStore(cfg config.StoreConfig, domain string) KeyValueStore {
    return &myBackendStore{
        config: cfg,
        domain: domain,
    }
}

type myBackendStore struct {
    config config.StoreConfig
    domain string
}

func (s *myBackendStore) Get(ctx interface{}, key string, tenant Tenant) ([]byte, error) {
    // Implementation
    return nil, nil
}

func (s *myBackendStore) Set(ctx interface{}, key string, tenant Tenant, value []byte) error {
    // Implementation
    return nil
}

// ... implement other KeyValueStore methods
EOF

# Register in factory: internal/datastore/factory.go
# Add to StoreKind enum in config_types.go
```

### Add New Configuration Option

**Steps**:
```bash
# 1. Define type in internal/config/config_types.go
# type MyFeatureConfig struct { ... }

# 2. Add to GatewayConfig
# MyFeature MyFeatureConfig

# 3. Use in control/compiler.go or other packages
# if cfg.MyFeature.Enabled { ... }

# 4. Document in docs/packages/config.md
```

### Extend Cache

**To add size class or TTL tier**:
```bash
# 1. Modify cache initialization in control
# sizeClasses := []uint32{64, 256, 1024, ...}  // Add new size
# ttlTiers := []uint32{60, 300, 3600, ...}    // Add new TTL

# 2. Test cache operations with new class
# 3. Update docs/packages/cache.md with new layout
```

### Add Observability Metrics

```bash
# 1. Define metric type in internal/observability/telemetry.go
# type MyMetric struct { ... }

# 2. Export via GetMetrics()
# 3. Use in request path:
#    if obs := ctx.Obs; obs != nil {
#        obs.RecordCustom(...)
#    }

# 4. Document in docs/packages/observability.md
```

---

## Project Structure

```
rah\
├── cmd/
│   └── rah-gateway/
│       └── main.go                    Entry point
├── internal/
│   ├── engine/
│   │   ├── executor.go               Execution loop
│   │   ├── plan.go                   Plan structure
│   │   ├── api_definition.go         API config
│   │   ├── manager.go                Flow management
│   │   ├── steps/                    Instruction library
│   │   │   ├── http_call.go
│   │   │   ├── cache_read.go
│   │   │   ├── bind_input.go
│   │   │   └── ... other steps
│   │   └── engine_test.go
│   │
│   ├── control/
│   │   ├── compiler.go               Flow compilation
│   │   ├── config.go                 Control config
│   │   ├── cache_manager.go          Cache management
│   │   └── ... feature files
│   │
│   ├── router/
│   │   ├── router.go                 Main router
│   │   ├── route_node.go             Node structure
│   │   ├── builder_node.go           Builder
│   │   └── router_test.go
│   │
│   ├── cache/
│   │   ├── cache_manager.go          Main cache
│   │   ├── cache_types.go            Type definitions
│   │   ├── hash.go                   Hashing
│   │   └── cache_manager_test.go
│   │
│   ├── datastore/
│   │   ├── factory.go                Store factory
│   │   ├── redis_store.go            Redis backend
│   │   ├── postgresql_store.go       PostgreSQL backend
│   │   ├── mongodb_store.go          MongoDB backend
│   │   ├── cassandra_store.go        Cassandra backend
│   │   ├── dragonfly_store.go        Dragonfly backend
│   │   ├── disk_store.go             Disk backend
│   │   └── file_store.go             File backend
│   │
│   ├── registry/
│   │   ├── types.go                  Registry structures
│   │   ├── registry_manager.go       Management operations
│   │   └── registry_engine.go        Query engine
│   │
│   ├── rctx/
│   │   ├── context.go                Request context
│   │   └── context_test.go
│   │
│   ├── config/
│   │   ├── config_types.go           Type definitions
│   │   ├── datastore.go              Datastore config
│   │   └── datastore_test.go
│   │
│   ├── observability/
│   │   ├── telemetry.go              Metrics and tracing
│   │   └── telemetry_test.go
│   │
│   ├── smartcache/
│   │   ├── big_index.go              Large key index
│   │   ├── fixed_index.go            Small key index
│   │   ├── fixed_registry.go         Registry index
│   │   ├── lane1.go                  Primary lane
│   │   └── lane2.go                  Secondary lane
│   │
│   └── clock/
│       └── clock.go                  Clock abstraction
│
├── docs/
│   ├── ARCHITECTURE.md               System overview
│   ├── DEVELOPER_GUIDE.md            This file
│   ├── latency-design.md             Performance strategies
│   ├── dependency-graph.md           Package dependencies
│   ├── packages/
│   │   ├── engine.md
│   │   ├── control.md
│   │   ├── router.md
│   │   ├── cache.md
│   │   ├── datastore.md
│   │   ├── rctx.md
│   │   ├── config.md
│   │   ├── observability.md
│   │   ├── registry.md
│   │   ├── smartcache.md
│   │   └── clock.md
│   └── ... other docs
│
├── go.mod                            Go module definition
├── go.sum                            Dependency checksums
├── README.md                         Project README
├── claude.md                         Project-level Claude instructions
└── bin/
    └── rah-gateway                   Compiled binary
```

---

## IDE Setup

### VS Code / VS Code Insiders

Install extensions:
- Go (golang.go)
- Code Spell Checker (streetsidesoftware.code-spell-checker)
- YAML (redhat.vscode-yaml)

Settings (.vscode/settings.json):
```json
{
    "go.useLanguageServer": true,
    "go.lintOnSave": "package",
    "editor.formatOnSave": true,
    "[go]": {
        "editor.defaultFormatter": "golang.go",
        "editor.formatOnSave": true
    }
}
```

### GoLand / IntelliJ IDEA

1. Open project: `File` → `Open` → `D:\GoLand\rah`
2. Go SDK: `File` → `Project Structure` → `SDK`
3. Run configurations: Create run config for `cmd/rah-gateway/main.go`

---

## Build and Deployment

### Local Build
```bash
go build -o bin/rah-gateway ./cmd/rah-gateway/
./bin/rah-gateway -port 8080 -mport 8081
```

### Docker Build
```dockerfile
FROM golang:1.26-alpine

WORKDIR /app
COPY . .

RUN go build -o rah-gateway ./cmd/rah-gateway/

EXPOSE 8080 8081

CMD ["./rah-gateway"]
```

```bash
docker build -t rah-gateway .
docker run -p 8080:8080 -p 8081:8081 rah-gateway
```

### Binary Flags
```
-port int
    Gateway Port (default 8080)
-mport int
    Management Port (default 8081)
```

---

## Performance Profiling

### CPU Profiling
```bash
go test -cpuprofile=cpu.prof -bench=BenchmarkExecute -benchtime=10s ./internal/engine/
go tool pprof -http=:8081 cpu.prof
```

### Memory Profiling
```bash
go test -memprofile=mem.prof -bench=BenchmarkExecute -benchtime=10s ./internal/engine/
go tool pprof -http=:8081 mem.prof
```

### Trace Profiling
```bash
go test -trace=trace.out -bench=BenchmarkExecute ./internal/engine/
go tool trace trace.out
```

### Analyzing Results
In pprof:
- `top`: Top functions by CPU/memory
- `list functionName`: Source code with costs
- `web`: Generate flame graph visualization

---

## Code Style

### Follow Go Conventions
- Use gofmt for formatting
- Use meaningful variable names
- Keep functions small and focused
- Document exported functions

### Comment Guidelines
```go
// Exported function comments start with function name
func Execute(ctx *Context, table []Instruction) {
    // Internal comments explain complex logic
}

// TODO(username): Consider pooling contexts
```

### Naming Conventions
- **Functions**: camelCase (Execute, NewCache)
- **Types**: PascalCase (CacheManager, RegistryNode)
- **Constants**: UPPER_CASE (StopPlan, DefaultTTL)
- **Private functions**: starts with lowercase (execute)

---

## Troubleshooting

### "go: no required module provides package"
→ Run `go mod tidy` to update dependencies

### "undefined: someType"
→ Check import paths, verify internal packages use full path `rah/internal/pkg`

### "test timeout"
→ Tests are complex; use `-skip=.` to skip them

### "performance regression in benchmarks"
→ Use CPU profiler to identify slow function, consult docs/latency-design.md

### "lock contention warnings"
→ Verify you're not taking mutex in hot path; use atomics instead

---

## Useful Commands

```bash
# Format code
go fmt ./...

# Lint code
golangci-lint run ./internal/...

# Check for errors
go vet ./...

# Update dependencies
go get -u

# Run specific test
go test -run TestCacheLookup ./internal/cache/

# Verbose test output
go test -v ./internal/engine/

# Coverage analysis
go test -cover ./internal/...
go test -coverprofile=coverage.out ./internal/...
go tool cover -html=coverage.out
```

---

## Resources

- **Documentation**: See [docs/](docs/) directory
- **Go Docs**: https://golang.org/doc/
- **Code Review**: Check git history for patterns
- **Performance**: Use benchmarks and profiles (not guessing)

---

**Happy developing!**
