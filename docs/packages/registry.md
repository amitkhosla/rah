# Registry Package

## Purpose
Implements **tenant identity and property management** using a lock-free hash table for alias lookup and radix trees for property key lookups, with a matrix-based value storage. Enables fast tenant identification and property lookups without dynamic allocation or GC pressure.

## Files
- **registry_engine.go**: Radix tree construction and query logic
- **registry_manager.go**: Management operations (add/update tenant)
- **types.go**: Core data structures (RegistryNode, TenantRegistry, GlobalState)

## Key Types

### Data Structures
- **AliasTable**: Open-addressing hash table with linear probing for alias → TenantID lookup
  - Slots: Power-of-2 length array, ≤75% max load factor
  - Mask: len(Slots) - 1 for fast modulo
  - StringArena: Packed alias bytes (no GC pointers)
  - Seed: maphash.Seed for consistent hash values
  - Lookup cost: ~40–70 ns, zero allocation

- **RegistryNode**: 16-byte radix tree node for property key lookups (offset-based, no pointers)
  - PrefixOffset: Position in StringPool
  - PrefixLen: Length of prefix
  - ChildBase: Index of first child in Nodes arena
  - ChildCount: Number of children
  - Value: ID (KeyID or ValueID)
  - Padding: Alignment to 16 bytes

- **TenantRegistry**: Immutable registry snapshot
  - Aliases: AliasTable for Alias → TenantID (hash table, not radix)
  - URLs: PropStore with radix tree for property key names → KeyID
  - IDs: PropStore for identifier key names → KeyID
  - Meta: PropStore for metadata key names → KeyID
  - ValuePool: Actual data (URLs, JSON, etc.) indexed by ValueID
  - RateLimitConfigs: System-wide rate limit defaults by RateLimitConfigID
  - TenantModifiers: Per-tenant rate limit scale/block overrides (sparse array indexed by TenantID)
  - TenantRateLimits: Per-tenant V1 rate limit overrides (sparse)
  - TenantV2Overrides: Per-tenant V2 config overrides (sparse)

- **GlobalState**: Active registry and free-slot tracker
  - Active: atomic.Pointer[TenantRegistry] (immutable snapshots)
  - FreeSlots: Stack of deleted TenantIDs for reuse

### Query Pattern
```
Tenant lookup (AliasTable hash table):
  AliasTable.Lookup("acme") → hash + linear probe → TenantID = 5 (~40–70 ns)

Property lookup (radix tree):
  findKeyID(URLs.Keys, "api_key") → walk radix tree → KeyID = 2 (~50–100 ns)

Value retrieval (matrix O(1)):
  URLs.Matrix[5 * Stride + 2] → ValueID = 42
  ValuePool[42] → actual value (URL, JSON) (~2–5 ns)
```

## Responsibilities
1. **Tenant Identity**: Map tenant alias to TenantID via AliasTable (hash table)
2. **Property Registry**: Map property names to KeyID via radix trees
3. **Value Storage**: Store actual property values (URLs, secrets, config)
4. **Rate Limit Config**: Store system-wide and per-tenant rate limit settings
5. **Lock-Free Reads**: Atomic pointer to immutable registry
6. **Atomic Updates**: Replace entire registry atomically on changes

## Dependencies
- **No external dependencies**: Uses only stdlib (sync/atomic)
- Used by: control plane, rctx for tenant isolation

## AliasTable Design (Hash Table)
```
Alias lookup uses open-addressing hash table with linear probing:
  AliasTable.Slots (power-of-2 length, ≤75% load factor):
    [Slot0, Slot1, Slot2, ..., SlotN]
    
  Each slot (16 bytes):
    Hash uint64          — maphash digest for fast rejection
    Offset uint32        — byte offset in StringArena
    Len uint16           — alias length
    TenantID uint16      — result (0 = empty slot)
  
  Lookup("acme"):
    1. hash = maphash.String(seed, "acme")
    2. idx = hash & mask
    3. Probe linear until hash match + string compare succeeds or empty slot found
    4. Cost: ~40–70 ns, zero allocation
```

## Radix Tree Design (Property Keys)
```
Property key lookup uses radix tree for URLs, IDs, Meta:
  Root
  ├─ "api" (children: ["_key"])
  │  └─ "_key" → KeyID: 1
  ├─ "db" (children: ["_url"])
  │  └─ "_url" → KeyID: 2
  └─ "tenant" (children: ["_id"])
     └─ "_id" → KeyID: 3

StringPool: [api_keydb_urltenant_id...]
  Offsets: 0, 3, 10, 17

Nodes: [RegistryNode, RegistryNode, RegistryNode, ...]
  Each node references StringPool offset
  
  Lookup cost: ~50–100 ns (management plane only)
```

## Matrix and Value Pool Layout
```
PropStore.Matrix layout (URLs, IDs, Meta each have their own):
TenantID \ KeyID    0           1          2          3
         0    (empty)     (empty)    (empty)    (empty)
         1    ValueID:10  ValueID:20 ValueID:30 ValueID:40
         2    ValueID:11  ValueID:21 ValueID:31 ValueID:41
         3    ValueID:12  ValueID:22 ValueID:32 ValueID:42
         4    ValueID:13  ValueID:23 ValueID:33 ValueID:43
         5    ValueID:14  ValueID:24 ValueID:34 ValueID:44

Query example (after resolving TenantID and KeyID):
  Tenant 5, Property 2 (api_key):
  ValueID = Matrix[5 * Stride + 2] = 34
  Value = ValuePool[34] (e.g., "https://api.service.com")
  
  Cost: ~2–5 ns (one atomic load + array bounds check)
```

## Update Semantics
1. **Immutable Snapshots**: Never modify existing TenantRegistry
2. **Full Rebuild**: Add/remove tenant rebuilds entire registry
3. **Atomic Store**: New registry becomes active atomically
4. **Readers See Consistent State**: No torn reads or intermediate states
5. **Free Slot Reuse**: Deleted TenantIDs pushed to FreeSlots stack

## Performance Notes
- **Lock-free reads**: Atomic load of registry pointer
- **No allocations**: AliasTable, radix tree, and matrix pre-allocated
- **Alias lookup**: ~40–70 ns via open-addressing hash table (hot path)
- **Property lookup**: ~50–100 ns via radix tree walk (management plane only)
- **Value retrieval**: ~2–5 ns O(1) matrix + pool lookup after resolution
- **Offset-based nodes**: No pointers, avoids GC scanning in radix tree
- **Atomic updates**: Entire registry replaced on any change

## Operations

### Lookup Tenant by Alias (Hot Path)
```go
registry := State.Active.Load()
tenantID, ok := registry.Aliases.Lookup("acme")  // ~40–70 ns, zero allocation
```

### Lookup Property Key by Name (Management Plane)
```go
keyID, ok := findKeyID(registry.URLs.Keys, registry.URLs.StringPool, "api_key")  // ~50–100 ns
```

### Get Property Value (Hot Path, Post-Resolution)
```go
// After resolving tenantID and keyID (at bake time when possible)
value, ok := GetURLByKeyID(tenantID, keyID)  // ~2–5 ns
```

### Get URL by Key Name (Runtime Fallback)
```go
// Only use when key name is not known at compile time
value, ok := GetURLByKeyName(registry, tenantID, "api_key")  // radix walk + matrix lookup
```

### Get Identifier or Metadata by KeyID
```go
identifier, ok := GetIDByKeyID(tenantID, keyID)  // ~2–5 ns
metadata, ok := GetMetaByKeyID(tenantID, keyID)  // ~2–5 ns
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
