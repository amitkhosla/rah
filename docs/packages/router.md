# Router Package

## Purpose
Implements a **fast, lock-free HTTP path routing** system using an immutable arena-based tree snapshot. Designed for nanosecond-level path matching with minimal allocations and GC pressure.

## Files
- **router.go**: Main RahRouter with atomic snapshot updates and lock-free lookup
- **route_node.go**: RouteNode structure for arena-stored tree nodes
- **builder_node.go**: BuilderNode for flexible tree construction before baking
- **router_test.go**: Test coverage for routing logic

## Key Types
- **RahRouter**: Main router with builder (for writes) and atomic snapshot (for reads)
- **routerSnapshot**: Immutable snapshot containing arena and string table
- **RouteNode**: 64-byte cache-line-aligned tree node with prefix metadata, bitmask child lookup, and arena offset (no pointers!)
- **BuilderNode**: Mutable tree node for construction phase

## Responsibilities
1. **Path Matching**: Lookup(path) returns apiId using longest-prefix matching
2. **Boundary Detection**: Only match at segment boundaries (next char is '/' or end)
3. **Atomic Snapshots**: Add() builds in mutable tree, bakes to immutable arena, atomic store
4. **Lock-Free Reads**: Lookup uses NO locks, just atomic load of snapshot
5. **Arena Allocation**: All nodes stored in single contiguous arena (cache-friendly)

## Dependencies
- **config**: May read route definitions
- **control**: Compiler populates router with API routes
- **engine**: Uses route lookup result to find execution plan

## Lookup Algorithm
```
1. Load immutable snapshot atomically (no lock)
2. Start at root node in arena
3. For each node:
   a. Check if input starts with this node's prefix
   b. If match: advance input by prefix length
   c. Update longestMatch if node has apiId at boundary
   d. Find child matching next input character
   e. If no child: return longestMatch
4. Return final match or zero
```

## RouteNode Structure

The RouteNode is a 64-byte cache-line-aligned structure enabling branchless child lookup:

```go
type RouteNode struct {
    prefixOff   uint32      // Offset into the global string table
    prefixLen   uint16      // Length of the prefix
    maskLo      uint64      // Existence bits for child bytes 0–63
    maskHi      uint64      // Existence bits for child bytes 64–127
    childIdx    uint32      // Base offset in arena where this node's children start
    numChildren uint16      // Count of contiguous children
    apiId       uint32      // API identifier (non-zero if this is a route endpoint)
    _           [32]byte    // Padding to reach 64 bytes (one cache line)
}
```

**Branchless Child Lookup Pattern:**
To find the child for input byte `b`:
1. Select `maskLo` (if b < 64) or `maskHi` (if b >= 64) using bit manipulation
2. Check if bit `b % 64` is set in the selected mask (child exists)
3. Count set bits below position b via popcount to get offset into children array
4. If in high mask, add the count of all set bits in maskLo
5. Access child at `arena[childIdx + offset]`

This allows single-cycle child lookup without branching, maximizing CPU instruction throughput.

## Performance Notes
- **Lock-free reads**: Lookup takes zero locks
- **Atomic snapshot**: Updates are atomic - no torn reads
- **String table**: All prefixes stored in single byte array (cache-friendly)
- **Arena allocation**: All nodes in contiguous memory (predictable access patterns)
- **Branchless child lookup**: Uses bitmasks and popcount for zero-branch child selection
- **Cache-line aligned**: 64-byte nodes fit exactly one CPU cache line
- **Boundary detection**: Ensures /user/123 doesn't match /user/123-extra
- **Longest-match**: Supports overlapping routes (longest wins)

## Update Path
```
Add(path, apiId)
    ↓
Lock mutex, modify builder
    ↓
builder.BakeToArena() → creates immutable RouteNode arena + string table
    ↓
Atomic store new snapshot
    ↓
Unlock mutex
```

## Design Advantages
1. **No pointers in nodes**: Uses offsets, avoids GC scanning
2. **Immutable snapshots**: Readers see consistent tree state
3. **String pooling**: Shared prefix strings reduce memory
4. **Cache-friendly**: Arena layout keeps nodes together

## Example Route Structure
```
Request: /api/v1/users/123

Routes registered:
  /api           → apiId 1
  /api/v1        → apiId 2
  /api/v1/users  → apiId 3

Arena nodes:
[0] prefix="/api", apiId=1, childIdx=1
[1] prefix="/v1", apiId=2, childIdx=2
[2] prefix="/users", apiId=3, childIdx=0
```
