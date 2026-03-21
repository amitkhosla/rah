# Registry Package

## Purpose
Implements **tenant identity and property management** using a lock-free radix tree with a matrix-based lookup. Enables fast tenant identification by alias and property lookups without dynamic allocation or GC pressure.

## Files
- **registry_engine.go**: Radix tree construction and query logic
- **registry_manager.go**: Management operations (add/update tenant)
- **types.go**: Core data structures (RegistryNode, TenantRegistry, GlobalState)

## Key Types

### Data Structures
- **RegistryNode**: 16-byte radix tree node (offset-based, no pointers)
  - PrefixOffset: Position in StringPool
  - PrefixLen: Length of prefix
  - ChildBase: Index of first child in Nodes arena
  - ChildCount: Number of children
  - Value: ID (TenantID, KeyID, or ValueID)
  - Padding: Alignment to 16 bytes

- **TenantRegistry**: Immutable registry snapshot
  - Identity: Radix tree for Alias → TenantID
  - Properties: Radix tree for KeyName → KeyID
  - Matrix: Flat [TenantID * Stride + KeyID] = ValueID
  - Stride: Total unique keys (matrix width)
  - MaxTenants: Capacity of matrix
  - ValuePool: Actual data (URLs, JSON, etc.) indexed by ValueID
  - StringPool: Raw bytes for radix prefixes

- **GlobalState**: Active registry and free-slot tracker
  - Active: atomic.Pointer[TenantRegistry] (immutable snapshots)
  - FreeSlots: Stack of deleted TenantIDs for reuse

### Query Pattern
```
Tenant lookup:
  Tenant("acme") → Walk Identity radix → TenantID = 5

Property lookup for that tenant:
  Property("api_key") → Walk Properties radix → KeyID = 2

Value retrieval:
  Matrix[5 * Stride + 2] → ValueID = 42
  ValuePool[42] → actual value (URL, JSON)
```

## Responsibilities
1. **Tenant Identity**: Map tenant alias to TenantID
2. **Property Registry**: Map property names to KeyID
3. **Value Storage**: Store actual property values (URLs, secrets, config)
4. **Lock-Free Reads**: Atomic pointer to immutable registry
5. **Atomic Updates**: Replace entire registry atomically on changes
6. **Radix Trees**: Efficient prefix-based lookups

## Dependencies
- **No external dependencies**: Uses only stdlib (sync/atomic)
- Used by: control plane, rctx for tenant isolation

## Radix Tree Design
```
Identity radix:
  ""
  ├─ "a" (children: ["c"])
  │  └─ "c" → value: 1 (acme)
  ├─ "b" (children: ["e"])
  │  └─ "e" → value: 2 (betas)
  └─ "c" (children: ["a"])
     └─ "a" → value: 3 (cobalt)

StringPool: [acmebetascobalt...]
  Offsets: 0, 3, 7, 14

Nodes: [RegistryNode, RegistryNode, RegistryNode, ...]
  Each node references StringPool offset
```

## Matrix Layout
```
TenantID \ KeyID    0           1          2          3
         0    (empty)     (empty)    (empty)    (empty)
         1    ValueID:10  ValueID:20 ValueID:30 ValueID:40
         2    ValueID:11  ValueID:21 ValueID:31 ValueID:41
         3    ValueID:12  ValueID:22 ValueID:32 ValueID:42
         4    ValueID:13  ValueID:23 ValueID:33 ValueID:43
         5    ValueID:14  ValueID:24 ValueID:34 ValueID:44

Query example:
  Tenant 5, Property 2 (api_key):
  ValueID = Matrix[5 * Stride + 2] = 34
  Value = ValuePool[34]
```

## Update Semantics
1. **Immutable Snapshots**: Never modify existing TenantRegistry
2. **Full Rebuild**: Add/remove tenant rebuilds entire registry
3. **Atomic Store**: New registry becomes active atomically
4. **Readers See Consistent State**: No torn reads or intermediate states
5. **Free Slot Reuse**: Deleted TenantIDs pushed to FreeSlots stack

## Performance Notes
- **Lock-free reads**: Atomic load of registry pointer
- **No allocations**: Radix tree and matrix pre-allocated
- **Offset-based nodes**: No pointers, avoids GC scanning
- **Radix tree**: O(log n) lookups, prefix compression
- **Matrix lookups**: O(1) after tenant/property identified
- **Atomic updates**: Entire registry replaced on any change

## Operations

### Lookup Tenant by Alias
```go
registry := registry.State.Active.Load()
tenantID := registry.LookupTenant("acme")  // O(log n)
```

### Lookup Property by Name
```go
propertyID := registry.LookupProperty("api_key")  // O(log n)
```

### Get Property Value
```go
valueID := registry.Matrix[tenantID * registry.Stride + propertyID]
value := registry.ValuePool[valueID]  // O(1)
```

## Typical Use Cases
1. **Tenant Identification**: Map incoming request to TenantID
2. **Configuration Lookup**: Get tenant-specific URLs, API keys, limits
3. **Isolation**: Enforce tenant boundaries in cache, datastore
4. **Multi-tenancy**: Support multiple independent tenants in single instance

## Memory Efficiency
- Stride optimization: Only allocate MaxTenants × UniqueProperties slots
- Radix tree compression: Shared prefixes reduce memory
- Single StringPool: All prefixes share storage
- ValuePool indexed: Avoid duplicate values
