# rah
This is an API / AI gateway which helps integrate different apps.

## Configurable data stores
Rah now supports configurable backends per logical data domain so each customer can pick the right performance/cost profile.

Supported store kinds:
- `disk`
- `redis`
- `mongodb`
- `dragonfly`
- `postgresql`
- `cassandra`

Required domain bindings:
- `api_definitions`
- `flows`
- `tenant_data`
- `cache`

Optional domain bindings:
- `rate_limit`
- `customer_data`

Management endpoint:
- `GET /config/datastores` to view active config + supported kinds
- `POST /config/datastores` to atomically switch bindings/configuration

### Tenant-first key model
All key/value operations are tenant-scoped using the tenant name as the leading namespace (for example in Redis hash keys) to avoid dependency on local numeric tenant IDs.

Key shape:
- `tenant:{tenant_name}:{domain}:{key}`

### Store interface and pooling
- The gateway uses a generic `KeyValueStore` interface so backend implementations can live in separate files and evolve independently.
- Each store exposes pool telemetry (`max_open`, `in_use`, `waiters`) and uses bounded connection acquisition before each operation.
- `GET /config/datastores` now also returns current pool stats.

### RegistryStore bootstrap and instance sharing
- `tenant_data` is treated as **registryStore**; if configured, gateway reads tenant bootstrap data from it during startup.
- `api_definitions` and `flows` are mandatory domains and should stay separate from `tenant_data` (all three are required for control/data plane correctness).
- Optional `instances` domain can be enabled to register/list active gateway instances for cross-instance sharing/coordination.

### Global (non-tenant) control-plane data
- `api_definitions` and `flows` are stored/read under a global system scope (`__global__`) because they are platform-level artifacts, not tenant records.
- `tenant_data` remains tenant-scoped (registryStore), while control-plane boot now reads APIs + flows + registry snapshots when configured.

### Default store behavior
- Default `disk` store is file-backed and persists records under the configured path (for example `/var/lib/rah/store_flows.json`, `/var/lib/rah/store_api_definitions.json`).
- On startup, gateway bootstraps control-plane from datastore snapshots:
  - `flows`: global flow definitions
  - `api_definitions`: global API definitions
  - `tenant_data` (registryStore): tenant-scoped registry records


### Runtime reconfiguration policy
- `POST /config/datastores` **cannot** change startup-managed domains:
  - `api_definitions`
  - `flows`
- These two must be configured externally at startup (env/file/deployment config) because they define the control-plane execution graph.
- Runtime updates are intended for mutable domains such as:
  - `tenant_data` (registryStore)
  - `cache`
  - `rate_limit`
  - `customer_data`
  - `instances`

### Extracting from PostgreSQL / Redis / other stores
- Current `redis/postgresql/mongodb/cassandra/dragonfly` adapters are placeholders behind the same `KeyValueStore` interface.
- To wire a real backend, implement `KeyValueStore` methods in the corresponding `*_store.go` file:
  - `Put/Get/Delete/ListKeys`
  - `PoolStats/Close`
- Keep key shape contract unchanged (`tenant:{tenant}:{domain}:{key}`) so control-plane bootstrap and runtime ops stay portable.
- For customer-specific persistence (request/response caching, audit payloads, custom records), use `customer_data` binding and call manager CRUD on that domain.
