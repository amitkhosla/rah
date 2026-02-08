# Cache Index Architecture: Sharded 4-Way Buckets

## 1. Why Sharding (1024 Shards)?
- **CPU Parallelism**: On a 2-core machine, 1024 shards virtually eliminate Mutex contention.
- **Memory Predictability**: Shard 0 (Global Tenant) can grow to 100MB while Shard 5 (Small Tenant) stays at 8KB.
- **GC Safety**: We store data in `[]uint64`. Go's GC sees 1024 objects regardless of having 10 million keys.

## 2. Why 4-Slot Buckets?
- **CPU Cache Lines**: A bucket of 4 slots (8 `uint64`s) is exactly 64 bytes. This matches a standard CPU Cache Line.
- **O(1) Access**: We jump to a bucket via bitwise math and scan only 64 bytes. This is the fastest possible lookup.

## 3. Handling Collisions (Tombstones vs. Shifting)
### Issues Identified:
- **Deletion Gaps**: Simply deleting a key (setting to 0) breaks the linear probe chain.
- **OOM**: Accumulating infinite tombstones leads to memory exhaustion.

### Decisions Made:
- **Tombstone Resurrection**: New `Put` operations actively look for tombstones and "resurrect" them, keeping the chain short.
- **Filter-on-Resize**: When a shard reaches 70% capacity, we double the size and **drop all tombstones**. This "deep cleans" the index.
- **Neighborhood Capping**: We limit scans to 4 buckets. If a neighborhood is full, we force a resize to maintain $O(1)$ performance.

## 4. Default Tenant Location
- **Decision**: The Default/Global tenant is assigned to **Shard 0**.
- **Reason**: This maintains code symmetry. The system doesn't need "if tenant == global" branches, which keeps the CPU instruction pipeline clean.