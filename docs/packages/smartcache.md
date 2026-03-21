# Smartcache Package

## Purpose
Provides **advanced cache indexing** for RAH - specialized index structures for caching keys and values. Implements different indexing strategies (FixedIndex for small keys, BigIndex for large keys) to optimize for various workload patterns.

## Files
- **big_index.go**: Index for large keys (>16 bytes) with sharded hash tables
- **fixed_index.go**: Compact index for small fixed-size keys
- **fixed_registry.go**: Registry-based index for property lookups
- **lane1.go, lane2.go**: Secondary index lanes for multi-level indexing

## Key Types

### Index Strategies
- **BigIndex**: Handles keys > 16 bytes
  - Sharded across 16 buckets by tenant ID
  - Each shard uses hash table with open addressing
  - Lock-free reads via atomic.LoadUint64
  - Shard-level write locks

- **FixedIndex**: For fixed-size small keys
  - Optimized for keys ≤ 16 bytes
  - Direct array access with hash routing
  - Minimal overhead for common case

- **FixedRegistry**: Property-based indexing
  - Maps property names to cache keys
  - Uses radix tree structure
  - Integration with TenantRegistry

### Lane Indexing
- **Lane1**: Primary index for most queries
- **Lane2**: Secondary index for overflow or special cases
- Supports multi-level index fallback

## Responsibilities
1. **Key-Value Mapping**: Map cache keys to stored values
2. **Sharded Indexing**: Distribute load across shards to reduce contention
3. **Lock-Free Reads**: Fast lookups without locks
4. **Write Protection**: Shard-level locking for updates
5. **Collision Handling**: Open addressing with tombstone marking
6. **Tenant Isolation**: Separate index entries by tenant

## Dependencies
- **cache**: Used by cache manager for key lookup
- **registry**: May use property-based indexing
- **rctx**: Reads tenant information for shard routing

## BigIndex Internals
```
BigIndex:
  shards[16]: []*bigShard
    Each shard has:
      mu: RWMutex (write protection)
      data: []uint64 (hash table entries)
      mask: uint32 (for modulo addressing)

Hash calculation:
  h = hash(tenantID, key)
  shardID = tenantID & 0x0F (lower 4 bits)
  idx = h & shard.mask (power-of-2 mask)

Entry: uint64 (could encode pointer/offset)
```

## Operations

### Get (Lock-Free Read)
```go
ptr, exists := bigIndex.Get(tenantID, []byte("user:123"))
// Shards by tenantID, atomically loads value
// Zero locks on read path
```

### Set (Shard-Locked Write)
```go
success := bigIndex.Set(tenantID, []byte("user:123"), pointerValue)
// Locks only the shard, not entire index
// Allows concurrent writes to different shards
```

### Delete
```go
success := bigIndex.Delete(tenantID, []byte("user:123"))
// Marks entry as deleted (tombstone)
```

## Performance Characteristics

### Lock-Free Reads
- **Atomic.LoadUint64**: Single atomic operation
- **No RWMutex**: Readers never contend
- **Shard selection**: Determined by tenantID
- **Hash lookup**: O(1) expected

### Shard-Locked Writes
- **Per-shard lock**: 16 shards reduce contention by 16x
- **Lock duration**: Short (set/delete are fast)
- **Concurrent writes**: Different shards can write simultaneously

### Collision Resolution
- **Open addressing**: Linear probing from hash index
- **Tombstones**: Mark deleted entries without moving others
- **Rehashing**: May rebuild table if load factor too high

## Cache Integration
```
rctx.Context
    ↓
  cache_read step
    ↓
  tenantID + key → BigIndex.Get()
    ↓
  Lock-free atomic load
    ↓
  Pointer/Offset returned (or not found)
    ↓
  If found: Retrieve value from cache region
  If not found: Cache miss
```

## Sharding Example
```
16 shards indexed by tenantID:
  Shard 0: tenants with ID % 16 == 0
  Shard 1: tenants with ID % 16 == 1
  ...
  Shard 15: tenants with ID % 16 == 15

Benefit:
  If one tenant has high traffic:
    - Only its shard gets locked
    - Other 15 shards unaffected
    - Throughput increases by ~16x vs single lock
```

## Hash Function
```
stitchHash(tenantID, key):
  Use tenantID as seed
  Hash key bytes
  XOR with seed
  Result: 64-bit hash

Guarantees:
  - Same tenantID + key → same hash
  - Different tenants → likely different hashes
  - Collision resistance
```

## Performance Notes
- **Atomic operations**: No lock needed for reads
- **Shard-level locking**: Fine-grained concurrency
- **Memory layout**: Shards in array (cache-friendly)
- **Hash table**: Power-of-2 mask avoids expensive modulo
- **Pointer encoding**: uint64 can encode offset or actual pointer

## Scaling
```
With 16 shards:
  1 core saturated → 16x throughput improvement possible
  Diminishing returns after ~16 concurrent writers
  Best for high-concurrency, read-heavy workloads
```

## Integration Points
- **cache_manager**: Uses BigIndex for key lookups
- **rctx**: Provides tenantID for shard selection
- **control**: May compile cache operations using smartcache
