# cache — Zero-GC Inline-Index Cache

## Overview

`cache` is RAH's purpose-built, zero-GC, multi-tenant in-memory cache (`internal/cache`). It was designed from scratch to serve as the hot-path cache layer for an API gateway with **20,000+ tenants**, **<5µs request latency**, and **zero GC pauses** at steady state.

This document covers the design journey — from the first prototype through three generations of the inline-index design — and benchmarks all four in-house implementations against leading third-party Go caches.

---

## The Problem We Were Solving

An API gateway cache must satisfy constraints that standard Go caches cannot:

| Constraint | Why it matters |
|---|---|
| **Zero GC pauses** | A GC stop-the-world of even 1ms blows the <5µs latency target |
| **Multi-tenant isolation** | 20,000 tenants; one tenant's write must not evict another's entry |
| **Bounded memory** | Pre-allocated slab; no surprise heap growth under traffic spikes |
| **Lock-free reads** | Hot-path Get must not contend with concurrent Puts |
| **Per-entry TTL** | API responses expire at different rates; global TTL is unusable |
| **Tenant quota enforcement** | Each tenant gets a capped share; atomic accounting only |

None of bigcache, ristretto, go-cache, sync.Map, or map+RWMutex satisfy all of these simultaneously.

---

## Architecture: Two-Layer Design

The cache uses a two-layer architecture shared across all generations:

```
┌─────────────────────────────────────────────────────┐
│  InlineIndex (trie)                                 │
│  Two lanes:                                         │
│    tinyIdx — lossless tag for keys ≤ 6 bytes        │
│    hashIdx — H1+H2 tag for keys > 6 bytes           │
│  256 independent shards (lock-free reads per shard) │
└────────────────────┬────────────────────────────────┘
                     │  SmartPointer (64-bit)
                     ▼
┌─────────────────────────────────────────────────────┐
│  Region Matrix [SizeClass][TTLTier]                 │
│  Pre-allocated circular slabs, stride-addressed     │
│  EntryHeader + value bytes, zero GC                 │
└─────────────────────────────────────────────────────┘
```

### InlineIndex — iNode Layout

Every trie node is exactly **128 bytes (2 cache lines)**:

```
Cache Line 1 (bytes 0–63):
  [slot0: tag(8B) + val(8B)]  [slot1: tag(8B) + val(8B)]
  [slot2: tag(8B) + val(8B)]  [slot3: tag(8B) + val(8B)]
  ↑ 4 inline entries stored DIRECTLY in the node (no pool)

Cache Line 2 (bytes 64–127):
  [children[0..7]: uint32 × 8 = 32B]  ← 8 child pointers (8-way branching)
  [freedAt:  int64  = 8B]             ← recycle timestamp
  [h2word:   uint64 = 8B]             ← Swiss-style H2 pre-filter (4 × uint16)
  [padding:  16B]                     ← unused in main; used in archive/v3 for klenWord+tenantWord
```

**Key property**: a cache hit at depth 0 costs exactly **2 cache line loads** — one for CL1 (slots) and one for CL2 (children + h2word) — with no pointer indirection to a separate entry pool.

### Slab Region — EntryHeader

Every slab slot starts with a fixed-size header, immediately followed by value bytes:

**cache (main)** (16-byte header):
```
Offset  Size  Field
     0     4  Expiry    — authoritative unix32 TTL
     4     1  Gen       — circular-buffer wrap generation
     5     1  KeyMid    — key[len/2] for identity pre-check
     6     2  TenantID  — for eviction accounting
     8     2  ValueLen  — actual bytes used in slot
    10     6  XSlotPtrB — back-pointer to owning index slot (for cleaner)
```

**archive/v3** (24-byte header, stronger verification):
```
Offset  Size  Field
     0     4  Expiry    — authoritative unix32 TTL
     4     2  TenantID  — explicit re-verification at read time
     6     2  ValueLen
     8     6  XSlotPtrB — back-pointer
    14     1  Gen
    15     1  (spare)
    16     3  KeyFP     — key[(n/2)+1%n], key[(n/4)+1%n], key[(3n/4)+1%n]
    19     5  (padding)
```

### SmartPointer — 64-bit slot value

The index stores a 64-bit `SmartPointer` encoding:
```
[63:62] Gen      — 2-bit wrap generation (fast pre-check)
[61:50] ExpTrunc — 12-bit truncated expiry (32s granularity, ~36h range)
[49:48] Type     — 00=SlabRAM  01=KeyIsValue  10=EmptyValue
[47: 0] Payload  — SizeClass(2b) | TierID(2b) | SlabOffset(44b)
```

This encodes the full slab address in a single atomic word — no pointer dereference needed to locate the value.

---

## The Journey: Four Generations

### Generation 0 — archive/lookup (LookupIndex)

The first production cache used a **LookupIndex**: a separate trie with entries stored in a pool of `lNode` structs, connected via pointers. Every cache hit required:
1. Index lookup → pointer to pool node
2. Pool node dereference → SmartPointer
3. Slab read

This required an extra pointer indirection and a pool allocation on every Put, adding ~18 bytes/entry of pool overhead.

**Memory**: +45MB own index memory for 398K entries
**Throughput**: 8.8M/s
**Location**: `internal/cache/archive/lookup/`

---

### Generation 1 — archive/v1 (InlineIndex, baseline)

Replaced the separate entry pool with **inline slots directly in each trie node**. Each iNode stores 4 entries in its first cache line — no pool, no pointer indirection on hit.

**Swiss-style H2 filter** (`h2word`): a single `atomic.Uint64` in CL2 packs four 16-bit H2 values. On Get, one atomic load + a SWAR bit trick checks all 4 slots simultaneously:

```go
// SWAR: checks 4 × 16-bit groups in parallel for zero
x := word ^ (uint64(queryH2) * 0x0001000100010001)
return (x-0x0001000100010001)&^x&0x8000800080008000 != 0
```

If no H2 match → CL1 (the slot scan) is skipped entirely.

**H2 source**: derived from `Hash128` (two `maphash` calls) — upper 16 bits of the fingerprint hash. H1 and H2 are independent (different seeds), giving strong collision resistance but costing ~23ns for a 12-byte key.

**Memory**: +27MB own index memory for 398K entries (40% less than archive/lookup)
**Throughput**: 8.5M/s
**Location**: `internal/cache/archive/v1/`

---

### Generation 2 — cache (main, real-byte H2, single maphash)

Two improvements over archive/v1, now promoted to the canonical `internal/cache` package:

**1. `hashH1Only` — single maphash for routing**
H1 (routing hash) now uses a single `maphash` call from `routingPool` instead of `Hash128`'s two-call path. Saves ~9ns per hashLane Get/Put.

**2. `hashLaneBig16` — real-byte H2, fully independent from H1**
H2 is no longer derived from H1 via XOR-folding. Instead it samples 5 structural positions of the real key:

```go
func hashLaneBig16(tID uint16, key []byte) uint16 {
    // samples: key[0], key[n/4], key[n/2], key[3n/4], key[n-1]
    // mixes in tenantID so cross-tenant keys always differ
    // result packed into tag[63:48]
}
```

This provides:
- **Full independence** from H1 (different algorithm, not a derivation)
- **Real key entropy** — the 16 bits reflect actual key byte differences
- **Cross-tenant safety** — tenantID mixed in prevents collisions between tenants with identical keys
- **Zero extra maphash calls** — pure arithmetic, ~2ns

**Memory**: identical to archive/v1 (+27MB)
**Throughput**: **8.9M/s** (fastest of all four generations)
**Location**: `internal/cache/` ← **this is the active implementation**

---

### Generation 3 — archive/v3 (triple pre-filter + KeyFP)

Designed for maximum verification depth without persisting the full key. Uses the 16 bytes of iNode padding (bytes 112–127 in CL2) for two new pre-filter words:

```
CL2 layout (archive/v3):
  [children: 32B] [freedAt: 8B] [h2word: 8B] [klenWord: 8B] [tenantWord: 8B]
```

**`klenWord`** — packed key lengths, one `uint16` per slot. Filters out length mismatches before scanning CL1.

**`tenantWord`** — packed tenant IDs, one `uint16` per slot. Filters out cross-tenant false positives before CL1.

**Triple-mask filtering** on Get:
```go
h2mask     := h2MatchMask(h2word, queryH2)            // 4-bit mask
klenMask   := klenMatchMask(klenWord, queryLen)        // 4-bit mask
tenantMask := tenantMatchMask(tenantWord, queryTenant) // 4-bit mask
candidates := h2mask & klenMask & tenantMask           // only matching slots
```

**`KeyFP [3]byte`** in EntryHeader: three bytes from adjacent-to-H2 key positions (`key[(n/2)+1%n]`, `key[(n/4)+1%n]`, `key[(3n/4)+1%n]`). Combined with H1 (64b) + H2 (16b) + TenantID (16b) + KeyLen (16b) + KeyFP (24b), the false-positive probability is **< 1/2^136** — effectively zero for any gateway workload.

**Verification order** (deepest chain, cheapest first):
```
H2 → KeyLen → TenantID   (all from CL2, single cache line load)
→ H1                     (from CL1 slot tag, second cache line load)
→ KeyFP + Expiry + Gen   (from slab EntryHeader, third cache line load)
```

**Memory**: identical to main (+27MB) — klenWord + tenantWord fit in previously unused padding
**Throughput**: 8.8M/s (essentially identical — pre-filters add no measurable overhead)
**Location**: `internal/cache/archive/v3/`

---

## Benchmark Results

**Workload**: 20,000 tenants, 398,000 live entries, 50:1 read:write, 8-second sustained hot loop
Mixed key/value sizes: 30% tiny (4B key / 64B value), 50% short (16B / 512B), 20% long (48B / 2048B)
**Machine**: all goroutines at `GOMAXPROCS`

```
implementation                                     avg TPS    ownMB    totMB     heap objs  GC
────────────────────────────────────────────────  ────────  ───────  ───────  ────────────  ────
cache (InlineIndex + real-byte H2)                 8.9M/s      +27MB      640MB       965k objs  gc=0
cache/archive/v3 (klenWord + KeyFP)                8.8M/s      +27MB      640MB       965k objs  gc=0
cache/archive/lookup (LookupIndex)                 8.8M/s      +45MB      658MB      1007k objs  gc=0
cache/archive/v1 (InlineIndex, gen1 maphash H2)    8.5M/s      +27MB      640MB       964k objs  gc=0
sync.Map (string key, no TTL)                      7.8M/s      +62MB      342MB      2543k objs  gc=0
ristretto v2 (TinyLFU, async Set)                  6.0M/s      +61MB      357MB       947k objs  gc=1
bigcache v3 (ring-buffer, global TTL)              5.6M/s       +0MB     1094MB       931k objs  gc=5
go-cache (map+mutex, per-item TTL)                 1.5M/s      +24MB      321MB      1615k objs  gc=0
map+RWMutex (string key, no TTL)                   1.1M/s       +7MB      312MB      1218k objs  gc=0
```

### What the numbers show

**cache (main) is the fastest at 8.9M/s** — 14% faster than sync.Map, 48% faster than ristretto, 59% faster than bigcache, and 5× faster than go-cache. All with zero GC and 27MB of own index memory.

**All four in-house implementations beat all third-party caches** — even archive/v1 at 8.5M/s is 9% ahead of sync.Map, which has no TTL, no tenant isolation, and no bounded memory.

**Memory accuracy**: sync.Map appeared cheap in early measurements because it stored slice headers (24B) not value copies. Once adjusted to copy values fairly, our slab approach uses _less_ own memory (+27MB) than sync.Map (+62MB) while holding full value ownership.

**GC**: ristretto triggers 1 GC per run (async Set goroutines), bigcache triggers 5 (ring buffer map). All in-house variants: 0 GC. The slab design eliminates GC pressure by keeping all values inside pre-allocated byte arrays.

**LookupIndex overhead**: archive/lookup uses 45MB vs cache's 27MB — 67% more index memory for the same entries, because each entry also occupies a pool node with its own 8-byte pointer.

---

## Trie Growth: Local vs Global

A key architectural advantage of the InlineIndex is **local growth**: only the trie shards that receive data grow deeper. This matters enormously for multi-tenant workloads.

After populating 398K entries across 20K tenants (Tier-A: 200 hot tenants × 1000 keys; Tier-B: 1800 warm × 100 keys; Tier-C: 18000 cold × 1 key):

```
depth      nodes   entries     slots     fill%   node KB
──────  ────────  ────────  ────────  ────────  ────────
0            256      1024      1024    100.0%      32.0
1           2048      8192      8192    100.0%     256.0
2          16240     23808     64960     36.7%    2030.0
3           6000     18000     24000     75.0%     750.0
4          32000    128000    128000    100.0%    4000.0
5          61376    138176    245504     56.3%    7672.0
6          80800     80800    323200     25.0%   10100.0
──────  ────────  ────────  ────────  ────────  ────────
total     198720    398000    794880     50.1%   24840.0
```

- 90% of tenants (Tier-C, 1 key each) contribute negligible depth — their shards stay at depth 0–1
- Hot tenants (Tier-A) push their shards to depth 4–6 only
- A standard hash map would double its entire backing array when any bucket crosses load factor 0.75 — paying for all 20,000 tenants' capacity even when 18,000 of them have 1 entry

---

## Collision Safety Analysis

For a 30-minute TTL gateway cache (worst case: stale entries persist longest), the false-positive probability per lookup across generations:

| Generation | Checks | False positive rate |
|---|---|---|
| archive/v1 (baseline) | H1(48b) + H2(16b) + KeyMid(8b) + ExpTrunc(12b) | ~1 / 2^72 |
| cache (main) | H1(48b) + H2_real(16b) + KeyMid(8b) + ExpTrunc(12b) | ~1 / 2^72 (stronger H2 entropy) |
| archive/v3 | H1(48b) + H2_real(16b) + KeyLen(16b) + TenantID(16b) + KeyFP(24b) + Expiry(32b) | ~1 / 2^136 |

All generations are correctness-safe for any gateway workload. archive/v3 provides defence-in-depth for long TTLs and high-value data where correctness is paramount.

---

## Choosing a Generation

| Use case | Recommended |
|---|---|
| Maximum throughput, typical API gateway TTLs (≤5 min) | **cache (main)** — `internal/cache` |
| Long TTLs (≥30 min), high-value correctness requirement | **archive/v3** — `internal/cache/archive/v3` |
| Compatibility / reference baseline | **archive/v1** — `internal/cache/archive/v1` |
| Debugging / LookupIndex comparison | **archive/lookup** — `internal/cache/archive/lookup` |

---

## Configuration

All parameters are fully configurable — either via the gateway YAML config or directly through the Go API.

### YAML config (`[cache]` section in gateway config)

```yaml
cache:
  disabled: false            # true = skip CacheManager entirely; cache_get/put become no-ops
  mem_budget_mb: 256         # total slab memory in MB (default: 256)
  tenant_limit_mb: 0         # per-tenant quota in MB; 0 = unlimited
  size_classes: [256, 1024, 4096, 16384]   # value size buckets in bytes (default)
  ttl_tiers:    [60, 300, 3600]            # TTL buckets in seconds (default)
  backend:
    kind: ""                 # "" or "disk" (default), "memory", "redis", "dragonfly"
    connection:
      path: "./icache2"      # disk backend root directory (disk only)
      address: "localhost:6379"   # Redis/Dragonfly address (redis/dragonfly only)
      topology: "single"         # single | sentinel | cluster (redis/dragonfly only)
      pool_size: 32
```

### Go API

```go
import "rah/internal/cache"

cm, err := cache.NewCacheManager(
    256 * 1024 * 1024,        // totalMemory: total slab budget in bytes
    []uint32{256, 1024, 4096, 16384}, // sizeClasses: value size buckets in bytes
    []uint32{60, 300, 3600},          // ttlTiers: TTL buckets in seconds
    0,                        // expectedEntries: hint for index sizing (0 = auto)
    0,                        // tenantLimit: per-tenant quota in bytes; 0 = unlimited
    cache.NoopBackend,        // backend: NoopBackend = pure in-memory; nil = disk default
)
```

### How Memory Is Divided Across Regions

The total slab budget is split **equally** across every `[SizeClass × TTLTier]` cell:

```
regionMemory = totalMemory / (len(sizeClasses) × len(ttlTiers))
```

With defaults (`mem_budget_mb: 256`, 4 size classes, 3 TTL tiers → 12 regions):

```
regionMemory = 256MB / 12 = ~21.3MB per region

Regions and their slot counts:

SizeClass   stride = align8(16+SC)   slots = regionMem / stride   slot capacity
─────────   ─────────────────────    ─────────────────────────    ─────────────
      256              272B            ~82,000 slots per TTL tier
    1,024            1,040B            ~21,400 slots per TTL tier
    4,096            4,112B             ~5,400 slots per TTL tier
   16,384           16,400B             ~1,360 slots per TTL tier
```

Each TTL tier (60s, 300s, 3600s) gets the **same slot count** within a size class — there is no weighting between tiers. If your workload skews heavily toward one TTL range, consider removing unused tiers to concentrate memory.

### Fixed-Size Slots — Why Not Variable-Length?

Every slot in a region occupies exactly `stride = align8(16 + SizeClass)` bytes, regardless of the actual value size. A 512-byte value stored in the 1024-class occupies 1040 bytes (528 bytes of data, 512 bytes of zero padding).

This is a deliberate trade-off. Variable-length slots are not viable for four interconnected reasons:

**1. The SmartPointer encodes a bare byte offset.**
The 44-bit `Offset` field in SmartPointer is the literal byte offset of the slot in the slab buffer. The lock-free read path is:
```
physOff = SmartPointer.Offset   // direct; no table lookup
value   = buf[physOff+16 : physOff+16+header.ValueLen]
```
With variable-length entries, a single integer offset is not enough to locate a slot — you would need an auxiliary offset table. That table would either be GC-visible (heap allocation) or need a lock, destroying the lock-free guarantee.

**2. The circular buffer is pure arithmetic.**
```
physSlot = writeSlot % count        // which slot to claim
physOff  = physSlot × stride        // byte offset — one multiply
```
Variable-length makes `count` meaningless (slots have different sizes) and makes "advance to next slot" a linked-list walk instead of an increment.

**3. The Gen counter depends on uniform slot sizes.**
```
gen = uint8(writeSlot / count)      // wrap detection
```
`count` is the number of identically-sized slots. With mixed sizes, `writeSlot / count` produces no useful generation signal.

**4. Deletion creates permanent fragmentation in variable-length buffers.**
With fixed stride, a deleted (tombstoned) slot is reclaimed the next time the circular buffer wraps to that position — no hole, no wasted space, no compaction pass needed. With variable-length, deleting a 300-byte entry and writing a 500-byte entry at the same position leaves 200 bytes permanently dead until compaction. Over time the buffer becomes sparse without a stop-and-compact pass, which requires either a global lock or a complex concurrent algorithm.

### Sizing the Slab for Your Workload

The worst-case internal fragmentation is one entry just over a class boundary (e.g., 257 bytes → 1024-class wastes 767 bytes per slot). Choose size classes that match your actual value distribution:

```
Example: API gateway responses (typical distribution)
  30% flags/tokens  →  ~64B    → use SizeClass 64 or 128
  50% JSON bodies   →  ~512B   → use SizeClass 512 or 1024
  20% large blobs   →  ~2KB    → use SizeClass 2048 or 4096

Config: size_classes: [128, 1024, 4096]   # 3 classes × 3 TTL tiers = 9 regions
        mem_budget_mb: 192                 # 192/9 = ~21MB per region
```

**Capacity formula per region:**
```
stride   = align8(16 + SizeClass)              e.g. align8(16 + 1024) = 1040 B
slots    = floor(regionMemory / stride)        e.g. floor(21MB / 1040) ≈ 21,200 slots
capacity = slots × (number of TTL tiers)       total entries this class can hold
```

A value that exceeds the largest configured SizeClass is **silently dropped** (Put returns false). Size your largest class to cover the 99th-percentile value in your workload.

---

## Package Structure

```
internal/cache/                  ← MAIN (InlineIndex + real-byte H2, hashLaneBig16)
  cache_manager.go               ← Put / Get / Stop, lane routing, tenant quota
  index_inline.go                ← InlineIndex, iNode, h2word SWAR, trie traversal
  regions.go                     ← Slab region, Write / Read, circular buffer, TTL
  cache_types.go                 ← EntryHeader (16B), SmartPointer, xSlotPtr encoding
  hash.go                        ← hashH1Only + hashLaneBig16 (real-byte H2)
  backend.go / disk_backend.go   ← CacheBackend interface + bbolt persistence

  archive/v1/                    ← Generation 1: InlineIndex + maphash H2
  archive/v3/                    ← Generation 3: InlineIndex + klenWord + tenantWord + KeyFP
  archive/lookup/                ← Generation 0: LookupIndex (separate pool nodes)
  archive/lookup_v1/             ← LookupIndex variant (benchmark reference)
```

---

*Last updated: 2026-05-16*
