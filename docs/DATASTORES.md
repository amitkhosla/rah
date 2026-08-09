# Datastore Configuration

RAH has two distinct data layers that serve different purposes:

1. **Internal datastore domains** — RAH's own control-plane and caching data (API definitions, tenant registry, rate-limit counters, observability records). Configured under `datastore:` in gateway config.
2. **Customer data sources** — your databases and Redis instances that flows query directly. Configured under `data_sources:` and `redis_sources:`. See [Customer Data Sources](#customer-data-sources) below.

These two layers are completely separate. Flows using `db_query` or `redis_get` steps access customer data sources, never RAH's internal stores.

---

## Internal Datastore Domains

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
| `instances` | No | Cross-instance registration for multi-gateway coordination |
| `obs_access_log` | No | Access log records |
| `obs_traces` | No | Request traces |

> **Note:** There is no `customer_data` domain. Customer application data is accessed via separate `data_sources` (Postgres) and `redis_sources` (Redis) configuration — not via RAH's internal datastore. See [Customer Data Sources](#customer-data-sources) below.

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

---

## Customer Data Sources

Customer data sources let flows read and write against your own Postgres and Redis instances. These are entirely separate from RAH's internal stores — RAH connects to your database, queries your tables, and enforces tenant isolation at the connection level.

### Postgres (`data_sources`)

```yaml
data_sources:
  - name: orders_db
    driver: postgres
    dsn_ref: env:ORDERS_DATABASE_URL   # env: reference or literal DSN
    max_connections: 20                # connection pool size (default: 20)
    query_timeout_sec: 30              # per-query timeout (default: 10)
    tenant_isolation: schema           # "schema" | "rls" | "" (no isolation)
    tenant_key: id                     # "id" | "alias" — which tenant identifier to use
    shared_schemas: [public, shared]   # schemas visible to all tenants (schema mode only)
    rls_variable: app.current_tenant   # session variable name (rls mode; default: app.current_tenant)

  - name: analytics_db
    driver: postgres
    dsn_ref: env:ANALYTICS_DATABASE_URL
    max_connections: 10
    tenant_isolation: rls
    tenant_key: alias
```

**Tenant isolation details:**

| Mode | Mechanism | Effect |
|------|-----------|--------|
| `schema` | `SET search_path = tenant_<id>, shared, public` | Each tenant's queries only see their own schema |
| `rls` | `SET LOCAL <rls_variable> = '<tenant>'` | Postgres RLS policies use the session variable to filter rows |
| *(empty)* | None | Queries execute without tenant scoping — manage isolation in SQL |

**Flow steps:**

```yaml
- action: db_query      # SELECT — returns JSON array
  key: orders_db
  value: "SELECT id, amount, status FROM orders WHERE customer_id = $1"
  vars: ["{customer_id}"]
  as: orders

- action: db_query_one  # SELECT expecting one row — returns JSON object; 404 if empty
  key: orders_db
  value: "SELECT * FROM customers WHERE id = $1"
  vars: ["{customer_id}"]
  as: customer

- action: db_exec       # INSERT / UPDATE / DELETE / DDL — returns affected row count
  key: orders_db
  value: "INSERT INTO events (type, payload, created_at) VALUES ($1, $2, now())"
  vars: ["{event_type}", "{event_body}"]
  as: rows_affected
```

There is no statement restriction — DDL (`CREATE TABLE`, `ALTER TABLE`, etc.) is accepted via `db_exec`.

**Named queries and batching:**

Define reusable parameterized queries at sync time:

```yaml
queries:
  get_orders_by_ids:
    sql: "SELECT id, customer_id, amount FROM orders WHERE id = ANY($1::bigint[])"
    batch_by: "$1"         # batch on this parameter
    batch_window: 500us    # collect requests for up to 500 µs (default)
    batch_max: 100         # max keys per batch (default)
```

Reference in a flow with the `query:` prefix:

```yaml
- action: db_query
  key: orders_db
  value: "query:get_orders_by_ids"
  vars: ["{order_id}"]
  as: order
```

Concurrent flow executions requesting different keys within the batch window are collapsed into a single `WHERE id = ANY($1)` query. Identical concurrent keys are deduplicated via singleflight.

### Redis (`redis_sources`)

```yaml
redis_sources:
  - name: sessions
    addr: "redis.example.com:6379"    # single node
    # addrs: [...]                    # use addrs[] for cluster mode
    password: env:REDIS_PASSWORD
    db: 0
    tls: true
    tenant_prefix: alias              # "id" | "alias" | "none"
    key_sep: ":"                      # separator between prefix and key (default: ":")

  - name: leaderboard
    addr: "redis-leaderboard:6379"
    tenant_prefix: id
```

All key operations are automatically namespaced: `{tenant_alias}:{your_key}` or `{tenant_id}:{your_key}` depending on `tenant_prefix`. The reserved prefix `_rah:` is blocked on all operations.

**Available flow steps (45+ operations):**

| Category | Steps |
|----------|-------|
| Strings | `redis_get`, `redis_put`, `redis_mget`, `redis_mput`, `redis_del`, `redis_exists` |
| Counters | `redis_incr`, `redis_decr` |
| Expiry | `redis_expire`, `redis_ttl`, `redis_persist` |
| Locks | `redis_lock`, `redis_unlock` |
| Pub/Sub | `redis_publish` |
| Sorted Sets | `redis_zadd`, `redis_zincrby`, `redis_zrange`, `redis_zrangebyscore`, `redis_zscore`, `redis_zrank`, `redis_zrem`, `redis_zpopmin`, `redis_zpopmax` |
| Hashes | `redis_hset`, `redis_hmset`, `redis_hget`, `redis_hmget`, `redis_hgetall`, `redis_hdel`, `redis_hincrby` |
| Lists | `redis_lpush`, `redis_rpush`, `redis_lpop`, `redis_rpop`, `redis_lrange`, `redis_llen` |
| Sets | `redis_sadd`, `redis_srem`, `redis_sismember`, `redis_smembers`, `redis_scard` |

```yaml
- action: redis_get
  key: sessions           # redis source name
  value: "session:{session_id}"
  as: session_data

- action: redis_zadd
  key: leaderboard
  value: "scores:{game_id}"
  member: "{user_id}"
  score: "{score}"
  mode: gt               # "nx" | "xx" | "gt" | "lt"
```
