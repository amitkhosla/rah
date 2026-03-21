# Config Package

## Purpose
Defines **configuration types and schemas** for RAH. Provides the data structures that describe APIs, flows, datastores, cache policies, and resource limits. Also handles configuration validation and parsing.

## Files
- **config_types.go**: Main configuration structures (GlobalLayout, ResourceLimit, etc.)
- **datastore.go**: Datastore-specific configuration (StoreConfig, DataDomain, Bindings)
- **datastore_test.go**: Configuration validation tests

## Key Types

### Global Configuration
- **GlobalLayout**: Top-level request/response configuration
  - MaxBytesSlots, MaxIntsSlots, MaxBoolsSlots: Slot limits per request
  - MaxHeapBytes: Maximum heap allocation per request
  - DefaultLimits: Default resource limits for all APIs

- **ResourceLimit**: Per-API resource constraints
  - MaxHeaderSize: Maximum header value size (e.g., 16KB)
  - MaxHeaderCount: Maximum number of headers (e.g., 50)
  - MaxBodySize: Maximum request body (e.g., 1MB for SME, 1GB for Enterprise)

### Datastore Configuration
- **DataDomain**: Enum for logical data domains
  - DomainAPIDefinitions: API routes and configuration
  - DomainFlows: Workflow definitions
  - DomainTenantRegistry: Tenant identity and properties
  - DomainCache: Cache entries
  - DomainRateLimit: Rate limit state
  - DomainCustomerData: Customer-specific data
  - DomainInstances: Gateway instances

- **StoreKind**: Enum for backend types
  - StoreDisk, StoreFile, StoreRedis, StorePostgreSQL
  - StoreMongoDB, StoreCassandra, StoreDragonfly

- **StoreConfig**: Single datastore configuration
  - Name: Identifier (e.g., "local_disk", "redis_cluster")
  - Kind: Backend type
  - Enabled: Whether to use this store
  - Connection: Connection details (URL, auth, credentials)
  - TTL: Default entry TTL
  - Quota: Quota limits per domain

- **StoreConnection**: Backend-specific connection info
  - Path: Local filesystem path
  - Host, Port: Network address
  - Username, Password: Authentication
  - TLS: TLS/SSL configuration

- **DataStoreConfig**: Collection of stores and bindings
  - Stores: Map of StoreKey → StoreConfig
  - Bindings: Map of Domain → StoreKey (routing rules)

### API Configuration
- **APIDefinition**: API route and upstream mapping
  - Name: API identifier
  - Path: HTTP route pattern (e.g., /api/v1/users/:id)
  - Upstream: Target backend URL
  - FlowName: Compiled flow to execute
  - EntryPoint: Instruction index (set by compiler)
  - Method: HTTP method filter
  - Limits: API-specific ResourceLimit overrides

- **GatewayConfig**: Top-level gateway configuration
  - Apis: List of APIDefinition
  - Flows: Map of flow name → StepConfig list
  - Cache: Cache configuration
  - RateLimit: Rate limiting configuration
  - Datastore: DataStoreConfig

## Responsibilities
1. **Type Definitions**: Provide schema for all configuration aspects
2. **Validation**: Validate configuration constraints (e.g., MaxBodySize > 0)
3. **Parsing**: Load configuration from YAML/JSON files
4. **Domain Routing**: Map logical domains to physical datastores
5. **Resource Limits**: Define per-API and global constraints

## Dependencies
- **datastore**: Uses domain constants
- **engine**: May reference flow structure definitions
- **control**: Uses GatewayConfig for compilation

## Example Configuration Flow
```yaml
Global:
  MaxBytesSlots: 32
  MaxIntsSlots: 16
  DefaultLimits:
    MaxBodySize: 1MB

DataStores:
  Stores:
    local_disk:
      Kind: Disk
      Connection:
        Path: /var/lib/rah
  Bindings:
    APIDefinitions: local_disk
    Flows: local_disk
    Cache: local_disk

APIs:
  - Name: GetUser
    Path: /api/v1/users/:id
    Upstream: http://user-service:8080
    FlowName: FetchUserFlow
    Limits:
      MaxBodySize: 512KB

Flows:
  FetchUserFlow:
    - Action: http_call
      Upstream: ${.upstream}/users/${.id}
    - Action: cache_write
      Key: user_${.id}
```
