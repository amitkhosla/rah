# Cache Package

## Purpose
Implements a **zero-GC slab cache** with deterministic memory usage, multi-tenant quotas, and a fully lock-free read path. Preallocates all memory at startup. Routes keys through two independent index lanes based on key length, uses a fixed-stride circular slab per (SizeClass × TTLTier) cell, and runs a single background cleaner goroutine that uses slab-embedded back-pointers to tombstone expired index entries without hash recomputation.

---

## Files

| File | Purpose |
|------|---------|
| `cache_manager.go` | `CacheManager`: dual-lane routing, Put/Get, eviction accounting, single cleaner goroutine |
| `cache_types.go` | `SmartPointer` val encoding, `EntryHeader`, `xSlotPtr` back-pointer type |
| `regions.go` | `Region`: fixed-stride circular slab, `Write` / `Read` / `UpdateXSlotPtr` |
| `hash.go` | `Hash128`: 128-bit fingerprint via two independent maphash seeds |
| `index.go` | `LookupIndex`: 8-way COW trie + shared slot pool, tag helpers, `TryTombstone` |
| `*_test.go` | Unit, concurrency, and benchmark tests |

---

## Two-Lane Architecture

Keys are routed to one of two independent `LookupIndex` instances:

```
key ≤ 6 bytes AND all bytes < 128  →  tinyIdx   (lossless tag, no hash)
otherwise                           →  hashIdx   (H2 tag, 128-bit fp)
```

**Why two lanes?** For short keys the tag itself can carry the full (tenantID, key) losslessly. This means:
- Read validation is exact at the index level — no slab fingerprint check needed for Lane 1.
- The 128-bit fingerprint is only computed for keys that need it (Lane 2).
- The cleaner can route to the right index by reading the `lane` bit in `xSlotPtr`.

### Lane 1 — tinyIdx (keys ≤ 6 bytes, all bytes < 128)

Tag encoding (64 bits):
```
≤5B:  bit63=0 | keyLen(3b)[62:60] | tenantID(16b)[59:44] | key_exact(40b)[43:4] | spare[3:0]
 6B:  bit63=1 | tenantID(16b)[62:47] | key_7bit(42b)[46:5]  | spare[4:0]
```
- Bytes stored LSB-first; 6-byte keys use 7-bit strip (valid because all bytes < 128 guaranteed).
- Sanitised against `xEmpty`/`xTombstone` sentinels by flipping spare bit 2.
- At read time the tag match in the index IS the full key+tenant identity check — no slab fingerprint needed.

### Lane 2 — hashIdx (keys > 6 bytes, or 6B with any byte ≥ 128)

```
fp = Hash128(tenantID, key)   →  [16]byte
H1 = fp[0:8]   — routing only (consumed by trie traversal, never stored)
H2 = fp[8:16]  — identity (stored as tag in hashIdx xSlot)
```
Tag = H2, sanitised against sentinels. `EntryHeader.KeyMid = key[len/2]` is an additional identity byte validated at read time.

---

## xSlot val Encoding (SmartPointer)

Every index xSlot stores a 64-bit `SmartPointer` (= `uint64`) in its `val` field:

```
[63:62] Gen      — 2-bit wrap generation (fast pre-check; EntryHeader.Gen authoritative)
[61:50] ExpTrunc — 12-bit truncated expiry: (unix_sec >> 5) & 0xFFF  (32s granularity, ~36h range)
[49:48] Type     — 00=SlabRAM  01=KeyIsValue  10=EmptyValue  11=reserved

SlabRAM payload [47:0]:
  [47:46] SizeClass (2b)
  [45:44] TierID    (2b)
  [43: 0] Offset    (44b) — byte offset in region (up to 16TB)

KeyIsValue / EmptyValue payload [47:0]:
  [47:16] Expiry_unix32 (32b) — full TTL; authoritative since there is no slab
  [15: 0] spare
```

**Pre-checks**: `Gen & 0x3` and `ExpTrunc` are checked before touching the slab — fast miss for recycled or expired entries with zero cache-line loads.

---

## EntryHeader (16 bytes)

Fixed prefix of every slab slot, immediately followed by value bytes zero-padded to SizeClass:

```
Offset  Size  Field
     0     4  Expiry    — unix32, authoritative TTL
     4     1  Gen       — 8-bit generation (circular buffer wrap counter)
     5     1  KeyMid    — key[len/2]; 0x00 for Lane 1 (identity already in tag)
     6     2  TenantID  — for eviction accounting when slot is overwritten
     8     2  ValueLen  — actual value bytes written (≤ SizeClass)
    10     6  XSlotPtrB — 6-byte little-endian xSlotPtr back-pointer (cleaner)
```

Total: **16 bytes**, naturally 4-byte aligned. `unsafe.Pointer` cast safe when stride is 8-byte aligned.

### Read Validation Sequence (lock-free)

| Step | Source | Check | Miss condition |
|------|--------|-------|----------------|
| 1 | val.ExpTrunc | Fast pre-check (no slab load) | `expTrunc < (now>>5)&0xFFF` |
| 2 | header.Gen & 0x3 | Slot recycled? | ≠ val.Gen |
| 3 | header.Expiry | Authoritative TTL | `< now` |
| 4 | header.KeyMid | Key identity (Lane 2 only) | ≠ `key[len/2]` |

Steps 2–4 require loading the header cache line. Step 1 avoids that load for obviously expired entries.

---

## xSlotPtr — Slab-to-Index Back-Pointer

A 6-byte (48-bit) value stored in every `EntryHeader.XSlotPtrB`. Encodes the exact trie position of the owning xSlot so the cleaner can navigate directly without recomputing hashes.

```
Bit layout (lower 48 bits):
  [47:40] shard    — 8b  which of ≤256 shards
  [39:36] depth    — 4b  trie depth at write time (0–10)
  [35: 6] path     — 30b 3b per trie level, MSB-first (level 0 in bits[35:33])
  [ 5: 4] slot     — 2b  xSlot index within the 4-slot partition
  [    3] lane     — 1b  0=tinyIdx, 1=hashIdx
  [ 2: 0] spare
```

**Staleness**: if a split or collapse moves the entry after the xSlotPtr was written, `TryTombstone` detects the stale path via `navigateToSlotStart` returning `(0, false)` and skips the slot. The entry will expire naturally on next read.

**Lifecycle**:
1. `region.Write(...)` writes value bytes and zeroes `XSlotPtrB`.
2. `idx.SetTagGetPtr(tag, val, lane)` returns the xSlotPtr encoding the exact slot used.
3. `region.UpdateXSlotPtr(physOff, sp)` stores the 6 bytes in the header.
4. Cleaner reads `XSlotPtrB`, calls `TryTombstone`.

---

## Region — Fixed-Stride Circular Slab

```
Region {
    buf       []byte         — contiguous pre-allocated buffer
    count     uint64         — total slots = len(buf) / stride
    stride    uint32         — align8(EntryHeaderSize + SizeClass), fixed per region
    ttlSeconds uint32
    writeSlot uint64         — monotonic slot counter (protected by mu)
    mu        sync.Mutex     — serialises writes; reads are lock-free
}
```

**Stride** = `align8(16 + SizeClass)` e.g. SizeClass=64 → stride=80B.

**Write path**: `writeSlot` advances monotonically. Physical slot = `writeSlot % count`. Generation = `uint8(writeSlot / count)`. Old header at that position is read before overwriting to return `overwrittenEntry` for accounting.

**Read path** (lock-free): validate gen2b, expTrunc, Expiry, KeyMid → return `buf[physOff+16 : physOff+16+header.ValueLen]`. Returned slice is a direct view into the region buffer; do not retain across writes.

**Eviction is unconditional**: the write always claims the next slot regardless of whether the existing entry has expired. The `hasOld` flag is set when the displaced slot was previously written (Expiry > 0).

---

## CacheManager

```go
cm, _ := NewCacheManager(
    totalMemory,    // bytes; divided evenly across all regions
    sizeClasses,    // []uint32 e.g. [64, 256, 1024, 4096]
    ttlTiers,       // []uint32 seconds e.g. [60, 300, 3600, 86400]
    expectedEntries,
    tenantLimit,    // max bytes per tenant; 0 = unlimited
)
defer cm.Stop()    // shuts down the cleaner goroutine
```

### Put Flow

```
1. Tenant quota check (counter.used + entrySize ≤ tenantLimit)
2. isHashLane(key) → choose tinyIdx or hashIdx
3. region.Write(tenantID, keyMid, value, ttl)
   └─ returns physOff, gen, old (evicted entry), hasOld
4. if hasOld: subUsage(old.tenantID, 16+old.valueLen)
5. PackSlabVal(gen&0x3, ExpTrunc(expiry), classID, tierID, physOff)
6. idx.SetTagGetPtr(tag, ptr, lane) → xSlotPtr
7. region.UpdateXSlotPtr(physOff, xSlotPtr)
8. counter.used.Add(entrySize); globalUsed.Add(entrySize)
```

### Get Flow

```
1. isHashLane(key) → tinyIdx.GetTag(makeTagTiny(...))
                  or hashIdx.GetTag(makeTagHash(fp))
2. Unpack(val) → gen2b, expTrunc, type
3. if type ≠ SlabRAM: miss (KeyIsValue/EmptyValue reserved)
4. UnpackSlab(val) → classID, tierID, physOff
5. region.Read(physOff, gen2b, expTrunc, keyMid) → []byte
```

---

## Single Cleaner Goroutine

One background goroutine started by `NewCacheManager`, stopped by `Stop()`.

**Strategy**: round-robin across all `[SizeClass][TTLTier]` regions, scanning `cleanerBatchSize=256` slots per region per pass. Sleeps 50ms when a full round finds nothing to clean.

**Per-slot action**:
```
if header.Expiry > 0 AND header.Expiry < now:
    sp = xSlotPtrFrom6(header.XSlotPtrB)
    idx = tinyIdx or hashIdx  (sp.lane bit)
    idx.TryTombstone(sp, physOff, header.Gen&0x3)
```

**Accounting**: the cleaner does **not** modify tenant/global counters. Accounting is owned exclusively by the overwrite path (`hasOld` in `region.Write`) to prevent double-decrement races.

**`TryTombstone` safety**: before CAS-tombstoning, verifies `xSlot.val` still encodes the same `physOff` and `gen2b` — prevents false deletions if a different entry has since claimed the same xSlot.

---

## LookupIndex — Design

### Structure

```
LookupIndex
├── shards    []xShard           — N = 1<<shardBits (default 256)
│   ├── mu    sync.Mutex         — per-shard write lock
│   ├── snap  atomic.Pointer     — immutable xShardSnap (COW on every split/collapse)
│   │   └── nodes []xNode        — trie node array; nodes[0] = root
│   └── _     [48]byte           — padding to 64B (1 cache line, no false sharing)
└── pool      slotPool           — shared across all shards
    ├── list  atomic.Pointer     — *[]*[1024]xSlot (grows on demand)
    ├── next  atomic.Uint32      — bump allocator
    ├── free  []uint32           — partition starts ready for reuse
    └── pending []deferredFree   — partitions within 1-second grace period
```

### Tag-Based API

The public `Get(fp [16]byte)` / `Set` / `Delete` methods call `makeTag(fp)` internally (uses H1, forces bit63). These are used by the existing index tests.

`CacheManager` uses the tag-based variants that accept a pre-computed lane-specific tag:

| Method | Purpose |
|--------|---------|
| `GetTag(tag)` | Lock-free lookup by pre-computed tag |
| `SetTag(tag, val)` | Insert/update by tag (does not return xSlotPtr) |
| `SetTagGetPtr(tag, val, lane)` | Insert/update + returns xSlotPtr encoding the exact xSlot used |
| `DeleteTag(tag)` | Tombstone by tag |
| `TryTombstone(sp, physOff, gen2b)` | CAS-tombstone if val still matches; used by cleaner |

### Trie Routing

```
tag & shardMask              → shard index
(tag >> shardBits) & 7       → L0 choice  (3 bits)
(tag >> shardBits+3) & 7     → L1 choice  (if L0 was internal)
...
leaf slotStart → scan 4 xSlots for tag match
```

Each `xNode` = 8 × `xIndexer` = 64 bytes = exactly 1 cache line. `extension == 0` means leaf (use `slotStart`), `> 0` means internal (follow to `nodes[extension]`).

### Split and Collapse

**Split** (all 4 leaf slots live): claim 8 new partitions, redistribute 4 entries by next 3 bits of tag, COW-publish new snapshot. Cost: +576 bytes (+1 node +8 partitions), old partition deferred 1s.

**Collapse** (8 sibling leaves combined ≤ 4 live entries): merge into 1 new partition, COW-publish. Recursive upward. Old 8 partitions deferred 1s.

**Grace period** (1 second): released slots remain readable with stale data until `drainPending` clears them. Covers all in-flight lock-free readers (<5µs per read → 200,000× safety margin).

### navigateToSlotStart

Used by `TryTombstone` to replay an encoded path:
```
for d in 0..depth-1:
    slot = (pathBits >> (27 - d*3)) & 7
    ix = nodes[nodeIdx][slot]
    if d == depth-1 and ix.extension == 0: return ix.slotStart ✓
    if split/collapse detected:            return (0, false)
    nodeIdx = ix.extension
```

---

## Memory Layout

```
TotalMemory / (len(SizeClasses) × len(TTLTiers))  bytes per region

Example: 512MB total, SizeClasses=[64,256,1024,4096], TTLTiers=[60,3600]
 = 8 regions × 64MB each

Region[0][0]: stride=80B,   count=838,860  slots (64B vals,  60s TTL)
Region[0][1]: stride=80B,   count=838,860  slots (64B vals,  1h  TTL)
Region[1][0]: stride=272B,  count=247,099  slots (256B vals, 60s TTL)
...
```

---

## Performance Notes

- **Lock-free reads**: trie snap load + 4-slot scan + header validation, 0 mutex, 0 allocs
- **Shard-level writes**: 256 shards → low contention even under high write throughput
- **Pre-check layering**: ExpTrunc filter avoids slab cache-line load for expired entries
- **Fixed stride**: predictable slot size removes runtime alignment calculations
- **Single cleaner**: minimal background overhead; 50ms sleep at idle
- **xSlotPtr back-pointer**: O(trie depth) index navigation in cleaner vs O(N) scan

### Benchmark Reference (amd64, warm L1)

| Operation | Throughput |
|-----------|-----------|
| `GetTag` (index only) | ~4.5 ns/op |
| `Set` | ~77 ns/op |
| `Get` cold (500K entries, DRAM) | ~30 ns/op |
| Delete + re-insert cycle | ~2 µs/op |

All hot-path operations: **0 allocs/op**.

---

## Hash Function

```go
Hash128(tenantID uint16, key []byte) → [16]byte
  fp[0:8]  = maphash64(routingSeed,     tenantID, key)  ← H1: shard+trie routing
  fp[8:16] = maphash64(fingerprintSeed, tenantID, key)  ← H2: hashIdx tag + KeyMid source
```

TenantID is mixed into both halves → identical keys for different tenants always produce distinct fingerprints. Cross-tenant index collision is impossible.
