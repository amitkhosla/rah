# Datastore Package

## Purpose
Provides a **pluggable key-value store abstraction** with multiple backend implementations. Allows RAH to operate independently of a specific datastore, supporting configuration data, flow definitions, API definitions, and tenant registries across different storage systems.

## Files
- **factory.go**: Factory function `NewStore()` that instantiates appropriate backend based on StoreConfig.Kind
- **redis_store.go**: Redis backend implementation (memcached-compatible protocol)
- **postgresql_store.go**: PostgreSQL JSONB backend for relational storage
- **mongodb_store.go**: MongoDB document store backend
- **cassandra_store.go**: Cassandra wide-column store backend
- **dragonfly_store.go**: Dragonfly in-memory datastore backend
- **disk_store.go**: File-based disk store backend
- **file_store.go**: Direct file system storage backend

## Key Types
- **KeyValueStore**: Core interface implemented by all backends:
  ```go
  type KeyValueStore interface {
      Put(ctx context.Context, tenant Tenant, key string, value []byte) error
      Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error)
      Delete(ctx context.Context, tenant Tenant, key string) error
      ListKeys(ctx context.Context, tenant Tenant, prefix string) ([]string, error)
      Kind() string
      Name() string
      PoolStats() PoolStats
      Close() error
  }
  ```
- **Tenant**: Stable tenant identifier (name/slug) for namespace isolation across backends
- **Domain**: Enum for data domains (APIDefinitions, Flows, TenantRegistry, Cache, RateLimit, CustomerData, Instances, SlotOverflow)

## Responsibilities
1. **Factory Pattern**: NewStore() creates appropriate backend based on config
2. **Unified Interface**: All backends implement KeyValueStore contract
3. **Tenant Isolation**: Tenant parameter enables multi-tenant namespacing
4. **Domain Routing**: Control plane maps domains to specific stores via bindings
5. **Connection Management**: Handle authentication, connection pooling per backend

## Dependencies
- **config**: Provides StoreConfig with Kind, Connection details
- **control**: DataStoreManager uses datastore backends
- **rctx**: May use context from requests for timeouts

## Backend Features
| Backend | Use Case | Strengths | Limits |
|---------|----------|-----------|--------|
| Redis | Cache, hot data | Ultra-fast, in-memory | No persistence |
| PostgreSQL | Relational data | ACID, complex queries | Slower than in-memory |
| MongoDB | Flexible schemas | Document-oriented, scalable | Network latency |
| Cassandra | Distributed, time-series | High availability, distributed | Complex setup |
| Dragonfly | Drop-in Redis replacement | Modern Redis, better memory | Less mature ecosystem |
| Disk | Local persistence | Single-node reliability | Slower than memory |
| File | Simple workflows | Zero dependencies | No concurrency support |

## Performance Notes
- **Network latency**: Redis/PostgreSQL/MongoDB/Cassandra add round-trip delays
- **Connection pooling**: Essential for production deployments
- **Local stores** (Disk, File): Lowest latency but limited to single instance
- **Tenant isolation**: Achieved via tenant/prefix separation, not authentication

## Store Wrappers (Middleware Decorators)

Store wrappers layer additional functionality on top of any backend without coupling to specific implementations.

### EncryptingStore
Wraps any KeyValueStore backend with transparent AES-256-GCM encryption.

**Wire format**: `[magic:2][version:1][nonce:12][ciphertext:N][gcm_tag:16]`
- **AAD** (Additional Authenticated Data): Full scoped key `tenant:{t}:{domain}:{key}` — binds ciphertext to its location, preventing key confusion attacks.
- **Migration safety**: Values without the magic prefix pass through as plaintext, enabling gradual encryption rollout.

**Key features**:
- **Key rotation**: Populate multiple version→AEAD entries. New values encrypt with the primary version; reads extract the version byte from wire format, enabling zero-downtime rotation without re-encrypting old data.
- **Per-tenant isolation (HKDF mode)**: Optional master keys are processed via HKDF-SHA256 to derive tenant-specific 32-byte subkeys. Master key material alone cannot decrypt a single tenant's data without the derivation step.
- **Bypass**: ZSetStore and DistributedStore operations bypass encryption entirely.

**Configuration**: Enable via `StoreConfig.Encryption` with key material.

### EventingStore
Wraps any KeyValueStore and emits ingest events after successful writes (Put, Delete, MultiPut, PutWithTTL).

**Purpose**: Enable cross-instance cache invalidation, change-data capture (CDC), and observability.

**Key features**:
- **Unconditional BatchStore**: Implements BatchStore interface for uniform MultiGet/MultiPut access regardless of backend.
- **Source tracking**: Instance fingerprint stamped as Model on every emitted event. Consumers compare against their own fingerprint to skip self-emitted events, preventing write-emit-consume-write loops when multiple instances share the same store.
- **No overhead on nil**: If pipeline is nil, returns the original store unchanged.

**Activation**: Applied via `DataStoreManager.SetStoreWrapper()` in the control plane.

### PostgresBatchingStore
Wraps PostgreSQL backends only and coalesces concurrent Get/Put/Delete calls from multiple goroutines into single batched MultiGet/MultiPut queries.

**Purpose**: Reduce round-trips for high-concurrency workloads on PostgreSQL.

**Key features**:
- **Read-your-writes guarantee**: Write buffer ensures keys written via Put are immediately visible to subsequent Get calls without a DB round-trip.
- **Pass-through**: ListKeys, PoolStats, Close, etc. pass through to inner store unchanged.

**Configuration**: Enable via `StoreConnection.BatchEnabled + BatchMaxKeys`.

## Optional Interfaces

Beyond the core KeyValueStore interface, backends may implement specialized interfaces for advanced features. Type-assert before use:
```go
if b, ok := store.(datastore.BatchStore); ok {
    b.MultiGet(...)  // Bulk operations in one round-trip
}
```

### BatchStore
Bulk operations in a single round-trip. Implemented by most backends; not available on file stores.

```go
type BatchStore interface {
    MultiGet(ctx context.Context, tenant Tenant, keys []string) (map[string][]byte, error)
    MultiPut(ctx context.Context, tenant Tenant, kvs map[string][]byte) error
}
```

### ExpiringStore
Time-to-Live (TTL) support on individual values. Implemented by Redis, Dragonfly, MongoDB, Cassandra; not available on PostgreSQL, Disk, File.

```go
type ExpiringStore interface {
    PutWithTTL(ctx context.Context, tenant Tenant, key string, value []byte, ttl time.Duration) error
}
```

### ZSetStore
Sorted-set operations for ordered collections. Redis/Dragonfly only. Used by rate-limiting tiers, alias management, and weighted service URL routing.

**Score semantics by use case**:
- **Aliases/Identifiers**: score = insertion Unix milliseconds (stable ordering)
- **Service URLs**: score = weight/priority (higher = preferred in load balancing)
- **Rate limit tiers**: score = tier level

```go
type ZSetStore interface {
    ZAdd(ctx context.Context, tenant Tenant, key string, members ...ZMember) error
    ZRem(ctx context.Context, tenant Tenant, key string, members ...string) error
    ZRange(ctx context.Context, tenant Tenant, key string) ([]ZMember, error)
    ZRangeByScore(ctx context.Context, tenant Tenant, key string, min, max float64) ([]ZMember, error)
    ZScore(ctx context.Context, tenant Tenant, key string, member string) (float64, bool, error)
    ZCard(ctx context.Context, tenant Tenant, key string) (int64, error)
}
```

### DistributedStore
Atomic coordination primitives for distributed systems. Redis/Dragonfly only. Used by distributed rate limiting and instance coordination.

```go
type DistributedStore interface {
    Increment(ctx context.Context, tenant Tenant, key string, delta int64) (int64, error)
    TryLock(ctx context.Context, tenant Tenant, key, token string, ttl time.Duration) (bool, error)
    Unlock(ctx context.Context, tenant Tenant, key, token string) error
}
```

## Data Domains
Each domain can bind to a different store:
- **APIDefinitions**: API routes and configuration
- **Flows**: Workflow step definitions
- **TenantRegistry**: Tenant identity and properties matrix
- **Cache**: Cache entries (if using datastore for cache backend)
- **RateLimit**: Rate limiting state
- **CustomerData**: Customer-specific data
- **Instances**: Gateway instance registration
- **SlotOverflow** *(ephemeral)*: Temporary storage for request slot values that exceed the in-memory arena. Keys are request-scoped (`slot-ov/{reqID}/{seq}` and `slot-idx/{reqID}/{idx}`) and are deleted by `FlowManager.ReturnContext` at the end of every request. Recommended backends: Disk or Dragonfly (low-latency, single-node); GCS/Redis for Cloud Run deployments. See `engine.NewSlotOverflowAdapter` to wire a store to this domain.

## Typical Topology
```
Control Plane
    ↓
DataStoreManager
    ↓
Bindings Map {Domain → StoreKey}
    ↓
Stores {StoreKey → KeyValueStore backend}
    ↓
[Redis|PostgreSQL|MongoDB|Cassandra|Dragonfly|Disk|File]
```
