# Cache Architecture (Current Implementation)

This document describes the **current** cache behavior implemented under `internal/cache`.

---

## 1) High-level design

The cache is a bounded in-memory system with:

- **Two index lanes** (tinyIdx / hashIdx) routed by key length and byte range
- **Fixed-stride circular regions** split by `SizeClass × TTLTier`
- **Per-tenant usage accounting** + global accounting
- **Single cleaner goroutine** that expires stale index entries via slab back-pointers

All region memory is pre-allocated at startup; no allocation occurs during steady state.

---

## 2) Two-lane routing

```
key ≤ 6 bytes AND all bytes < 128  →  tinyIdx  (lossless tag, no hash)
otherwise                           →  hashIdx  (H2 tag from 128-bit fingerprint)
```

**tinyIdx** encodes `(tenantID, key)` losslessly in the tag — no hash, no slab fingerprint check needed at read time.

**hashIdx** uses `H2 = fp[8:16]` as the tag (sanitised against sentinels). An extra identity byte `KeyMid = key[len/2]` is stored in `EntryHeader` and validated at read time.

---

## 3) Core components

### `CacheManager`

- Routes writes to the correct region (`SizeClass`, `TTLTier` selection)
- Owns `tinyIdx` and `hashIdx`
- Enforces per-tenant quota via atomic counters
- Tracks per-tenant / global used bytes
- Starts a single background cleaner goroutine on init

### `Region`

A fixed-stride circular slab for one `SizeClass × TTLTier` cell.

```
stride = align8(EntryHeaderSize + SizeClass)   // e.g. SizeClass=64 → stride=80B
slot   = writeSlot % count                     // circular, unconditional overwrite
gen    = uint8(writeSlot / count)              // wraps at 256; stale-slot guard
```

Each slot layout:
```
EntryHeader (16 bytes) + value bytes (≤ SizeClass) + zero padding to stride
```

**Writes are serialised by `r.mu` (one mutex per region).**
**Reads are lock-free** — validated by gen, expiry, and KeyMid checks.

### `EntryHeader` (16 bytes)

```
Offset  Size  Field
     0     4  Expiry    — unix32, authoritative TTL
     4     1  Gen       — 8-bit wrap generation counter
     5     1  KeyMid    — key[len/2]; 0x00 for tinyIdx (identity in tag)
     6     2  TenantID  — for accounting on eviction
     8     2  ValueLen  — actual bytes written
    10     6  XSlotPtrB — 6-byte little-endian xSlotPtr back-pointer (cleaner)
```

### `LookupIndex`

- 256 shards, each with a per-shard `sync.Mutex` for writes
- Immutable COW trie snapshot published via `atomic.Pointer` per shard
- xSlots use `atomic.Uint64` for tag and val fields
- **Reads (`GetTag`) are fully lock-free** — snapshot load + 4-slot scan, zero mutex
- **Writes (`SetTag`, `DeleteTag`, `SetTagGetPtr`) acquire the per-shard mutex**

---

## 4) Val encoding (SmartPointer — 64-bit)

```
[63:62] Gen(2b)       — fast pre-check; authoritative in EntryHeader.Gen
[61:50] ExpTrunc(12b) — (unix_sec >> 5) & 0xFFF; 32s granularity, ~36h range
[49:48] Type(2b)      — 00=SlabRAM  01=KeyIsValue  10=EmptyValue
[47: 0] payload

SlabRAM payload:
  [47:46] SizeClass(2b)
  [45:44] TierID(2b)
  [43: 0] Offset(44b)  — byte offset in region buffer
```

`ExpTrunc` is a fast pre-check: if truncated expiry is in the past, skip the slab load entirely.

---

## 5) Write flow (`Put`)

```
1. Tenant quota check (counter.used + entrySize ≤ tenantLimit)
2. isHashLane(key) → choose tinyIdx or hashIdx
3. region.Write(tenantID, keyMid, value, ttl)        [acquires r.mu]
   └─ returns physOff, gen, old (evicted entry), hasOld
4. if hasOld: subUsage(old.tenantID, 16+old.valueLen)
5. PackSlabVal(gen&0x3, ExpTrunc(expiry), classID, tierID, physOff)
6. idx.SetTagGetPtr(tag, ptr, lane)                  [acquires sh.mu]
   └─ returns xSlotPtr encoding exact trie position
7. region.UpdateXSlotPtr(physOff, xSlotPtr)          [lock-free, single writer]
8. counter.used.Add(entrySize); globalUsed.Add(entrySize)
```

Eviction is **unconditional** — the write always claims the next slot regardless of whether the existing entry has expired.

---

## 6) Read flow (`Get`)

```
1. isHashLane(key) → compute tag (tiny or H2)
2. idx.GetTag(tag) → val                             [lock-free]
3. Unpack(val) → gen2b, expTrunc, type
4. ExpTrunc pre-check → miss if obviously expired    [no slab load]
5. UnpackSlab(val) → classID, tierID, physOff
6. region.Read(physOff, gen2b, expTrunc, keyMid)     [lock-free]
   ├─ check gen&0x3 == gen2b
   ├─ check Expiry (authoritative)
   └─ check KeyMid (hashIdx only)
7. Return value slice (direct view into region buffer)
```

**Fully lock-free read path** — no mutex acquired anywhere.

---

## 7) Cleaner goroutine

One background goroutine started by `NewCacheManager`, stopped by `Stop()`.

- Round-robins all `[SizeClass][TTLTier]` regions
- Scans 256 slots per region per pass
- Sleeps 50ms when a full round finds nothing to clean

Per expired slot:
```
sp = xSlotPtrFrom6(header.XSlotPtrB)     // read back-pointer
idx = tinyIdx or hashIdx  (sp.lane bit)
idx.TryTombstone(sp, physOff, header.Gen&0x3)
```

`TryTombstone` validates that the xSlot's val still encodes the same `physOff` and `gen2b` before CAS-tombstoning. Stale paths (after trie split/collapse) are detected and skipped.

**Accounting (tenant/global counters) is NOT touched by the cleaner.** It is owned exclusively by the overwrite path (`hasOld` in `region.Write`) to prevent double-decrement races.

---

## 8) xSlotPtr — slab-to-index back-pointer

A 6-byte (48-bit) value stored in every `EntryHeader.XSlotPtrB`:

```
[47:40] shard  — 8b   which of ≤256 shards
[39:36] depth  — 4b   trie depth at write time
[35: 6] path   — 30b  3b per trie level, MSB-first
[ 5: 4] slot   — 2b   xSlot index within 4-slot partition
[    3] lane   — 1b   0=tinyIdx, 1=hashIdx
[ 2: 0] spare
```

Written at `Put` time after `SetTagGetPtr` returns the exact slot position.

---

## 9) Concurrency summary

| Operation | Lock held |
|-----------|-----------|
| `Get` (index lookup) | None — lock-free atomic loads |
| `Get` (region read) | None — lock-free validation |
| `Put` (region write) | `r.mu` (one per region, e.g. 16 regions for 4×4 config) |
| `Put` (index insert) | `sh.mu` (one per shard, 256 shards) |
| Cleaner `TryTombstone` | `sh.mu` (same per-shard mutex) |

---

## 10) Data-flow diagram

```
Put(tenantID, key, value, ttl)
  │
  ├─ Quota check (atomic)
  ├─ isHashLane? → tinyIdx or hashIdx
  ├─ region.Write [r.mu] → physOff, gen, old
  │   └─ hasOld → subUsage(old.tenantID)
  ├─ PackSlabVal → SmartPointer
  ├─ idx.SetTagGetPtr [sh.mu] → xSlotPtr
  ├─ region.UpdateXSlotPtr [no lock]
  └─ addUsage(tenantID)

Get(tenantID, key)
  │
  ├─ isHashLane? → compute tag
  ├─ idx.GetTag [lock-free] → val
  ├─ ExpTrunc pre-check [no slab]
  └─ region.Read [lock-free] → []byte
```
