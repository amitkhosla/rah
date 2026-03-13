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
- **KeyValueStore**: Interface with Get(ctx, key, bucket), Set(ctx, key, bucket, value), Delete(ctx, key, bucket), List(ctx, bucket)
- **Tenant**: Alias for bucket/namespace isolation
- **Domain**: Enum for data domains (APIDefinitions, Flows, TenantRegistry, Cache, RateLimit, CustomerData, Instances)

## Responsibilities
1. **Factory Pattern**: NewStore() creates appropriate backend based on config
2. **Unified Interface**: All backends implement KeyValueStore contract
3. **Tenant Isolation**: Bucket parameter enables multi-tenant namespacing
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
- **Tenant isolation**: Achieved via bucket/prefix, not authentication

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
