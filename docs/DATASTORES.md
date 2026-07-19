# Datastore Configuration

RAH supports pluggable backends per logical data domain, so you can pick the right performance and cost profile for each type of data.

## Supported store kinds

| Kind | Notes |
|---|---|
| `disk` | File-backed, zero dependencies. Default for local and single-node deployments. |
| `redis` | Recommended for production cache and rate-limit domains. |
| `dragonfly` | Redis-compatible, higher throughput for cache-heavy workloads. |
| `postgresql` | Recommended for tenant data and audit trails. |
| `mongodb` | Supported; see production readiness notes below. |
| `cassandra` | Supported; see production readiness notes below. |

## Domain bindings

Each domain must be bound to exactly one store kind. Domains marked **required** must be configured; optional domains fall back to the default disk store if omitted.

| Domain | Required | Purpose |
|---|---|---|
| `api_definitions` | Yes | Flow routing table — control-plane boot |
| `flows` | Yes | Compiled flow definitions — control-plane boot |
| `tenant_data` | Yes | Tenant registry (upstream URLs, credentials, metadata) |
| `cache` | Yes | Response and token cache |
| `rate_limit` | No | Rate limit counters (defaults to `cache` store if omitted) |
| `customer_data` | No | Application-specific per-tenant records |
| `instances` | No | Cross-instance registration for multi-gateway coordination |

## Tenant-first key model

All key-value operations are scoped by tenant name to avoid dependency on internal numeric IDs:

```
tenant:{tenant_name}:{domain}:{key}
```

Control-plane data (`api_definitions`, `flows`) is stored under the system scope `__global__` because it is platform-level, not tenant-level.

## Management API

```
GET  /config/datastores    View active bindings and pool stats
POST /config/datastores    Atomically switch bindings (see constraints below)
```

`POST /config/datastores` returns pool telemetry per store (`max_open`, `in_use`, `waiters`).

## Runtime reconfiguration constraints

`api_definitions` and `flows` **cannot** be changed at runtime via the management API. They define the control-plane execution graph and must be configured at startup (env/file/deployment config). All other domains can be reconfigured live.

## Startup bootstrap sequence

On startup the gateway reads from the configured datastores in this order:

1. `flows` — global flow definitions
2. `api_definitions` — global API route table
3. `tenant_data` — tenant registry snapshots

## Store interface

All backends implement the same `KeyValueStore` interface:

```go
Put(ctx context.Context, tenant Tenant, key string, value []byte) error
Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error)
Delete(ctx context.Context, tenant Tenant, key string) error
ListKeys(ctx context.Context, tenant Tenant, prefix string) ([]string, error)
Kind() string
Name() string
PoolStats() PoolStats
Close() error
```

To add a custom backend, implement this interface in a new `*_store.go` file and register it in `internal/datastore/factory.go`.

## Storage Middleware

RAH provides transparent wrapper layers that sit atop any backend to add functionality:

### EncryptingStore
Transparent AES-256-GCM encryption on any backend. Per-tenant key derivation via HKDF-SHA256 ensures that the master key alone cannot decrypt a specific tenant's data without performing the tenant-specific derivation step. Key rotation is supported via versioned keys—old and new trusted keys can coexist simultaneously with zero downtime.

- Enabled per-domain via `encryption.enabled` in StoreConfig
- Wire format: [magic:2][version:1][nonce:12][ciphertext:N][gcm_tag:16]
- Migration-safe: values without the magic prefix are passed through as plaintext

### EventingStore
Emits ingest events (KindDBPut, KindDBDelete) after every successful write. Enables cross-instance synchronisation and audit CDC (Change Data Capture) pipelines.

- Automatically wired after the ingest pipeline initialises
- Stamped with source instance fingerprint (Model field) to prevent write-emit-consume-write loops
- Delegates MultiGet/MultiPut to inner backend if available, otherwise falls back to sequential operations

### PostgresBatchingStore
PostgreSQL-only wrapper that coalesces concurrent requests into batched MultiGet/MultiPut queries. Reduces round-trips for high-concurrency workloads.

- Enabled via `connection.batch_enabled: true` and `connection.batch_max_keys`
- Read-your-writes guarantee: keys written via Put are immediately visible to subsequent Get calls via shared write buffer

## Production readiness notes

| Store | Status |
|---|---|
| `disk` | Production-ready. File-backed with JSON serialization. |
| `redis` | Production-ready. Connection pooling, bounded acquisition. |
| `dragonfly` | Production-ready. Same adapter as Redis. |
| `postgresql` | Production-ready. pgx connection pool. |
| `mongodb` | **Falls back to in-memory store.** Not suitable for production. |
| `cassandra` | **Falls back to in-memory store.** Not suitable for production. |

MongoDB and Cassandra emit a startup warning when selected. Use Redis or PostgreSQL for production deployments.
