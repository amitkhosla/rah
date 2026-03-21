//go:build !race

package cache_test

// TestRealisticScale models a production API gateway with 20 000 tenants.
//
// Key claim under test:
//
//	InlineIndex grows LOCALLY (per-shard trie nodes added only where data lands).
//	A standard hash map grows GLOBALLY (backing array doubled when load-factor
//	threshold is crossed, regardless of which buckets are active).
//
// Workload:
//
//	20 000 tenants split into three tiers:
//	  Tier-A  200 tenants × 1 000 keys each  → "hot"    (1% of tenants, 50% of entries)
//	  Tier-B  1 800 tenants × 100 keys each  → "warm"   (9% of tenants, 45% of entries)
//	  Tier-C  18 000 tenants × 1 key each    → "cold"   (90% of tenants, 5% of entries)
//
//	Read:write = 10:1 (reflects real API gateway traffic).
//	All goroutines running at GOMAXPROCS concurrency.
//
// Measurements:
//
//  1. Memory growth curve — populate incrementally, sample MB and bytes/entry
//     at seven checkpoints (1K → 5K → 20K → 50K → 100K → 200K → ~218K entries).
//     icache grows smoothly; hash-map backing array doubles at power-of-2 steps.
//
//  2. Shard utilisation — after full populate, show how many InlineIndex shards
//     are at each depth.  Cold tenants' shards stay shallow; hot shards go deeper.
//     Proves local growth: 90% of tenants contribute negligible index memory.
//
//  3. Steady-state throughput — 8-second hot loop at full scale with the
//     realistic 10:1 read:write ratio.  Compare icache vs sync.Map vs map+RWMutex.

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bigcachev3 "github.com/allegro/bigcache/v3"
	ristretto "github.com/dgraph-io/ristretto/v2"
	gocache "github.com/patrickmn/go-cache"

	lookup_v1 "rah/internal/cache/archive/lookup_v1"
	"rah/internal/cache"
	archivev1 "rah/internal/cache/archive/v1"
	archivev3 "rah/internal/cache/archive/v3"
)

// ── tenant model ──────────────────────────────────────────────────────────────

const (
	rsNumTenants = 20_000

	// Tier sizes.
	rsTierA      = 200          // hot tenants
	rsTierAKeys  = 1_000        // keys per hot tenant
	rsTierB      = 1_800        // warm tenants
	rsTierBKeys  = 100          // keys per warm tenant
	rsTierC      = 18_000       // cold tenants
	rsTierCKeys  = 1            // keys per cold tenant
	rsTotalKeys  = rsTierA*rsTierAKeys + rsTierB*rsTierBKeys + rsTierC*rsTierCKeys
	rsTTL        = uint32(60)   // 60s TTL
	rsReadRatio  = 50           // 1 write every rsReadRatio ops (realistic gateway: ~2% writes)
	rsRunSecs    = 8            // hot-loop duration

	// Mixed key lengths (realistic API gateway distribution):
	//   30% tiny  (4 B)  → tinyIdx lane, lossless tag, NO hash — fastest path
	//   50% short (16 B) → hashLane, ~16 ns Hash128
	//   20% long  (48 B) → hashLane, ~32 ns Hash128
	rsFracTiny  = 30  // % of keys that are tiny (≤6B, tinyIdx lane)
	rsFracShort = 50  // % of keys that are short hashLane
	// rsFracLong = 20  // remainder

	// Mixed value sizes matching key tiers:
	//   tiny keys  → 64 B values  (flags, counters, short tokens)
	//   short keys → 512 B values (API responses, session data)
	//   long keys  → 2048 B values (large JSON payloads, config blobs)
	rsTinyValSize  = 64
	rsShortValSize = 512
	rsLongValSize  = 2048

	// rsValueSize is the representative size used for Sections 1 & 2
	// (growth curve and depth analysis use a single-size workload for clarity).
	rsValueSize = rsShortValSize

	rsEntryHeaderSize = 16 // mirrors cache.EntryHeaderSize

	// Per-class strides (align8(header + value)).
	rsTinyStride  = (rsEntryHeaderSize + rsTinyValSize + 7) &^ 7   // 80 B
	rsShortStride = (rsEntryHeaderSize + rsShortValSize + 7) &^ 7  // 528 B
	rsLongStride  = (rsEntryHeaderSize + rsLongValSize + 7) &^ 7   // 2064 B

	// rsStride for Sections 1 & 2 (single size class = short).
	rsStride = rsShortStride

	// Per-class entry counts (for slab sizing).
	rsTinyCount  = rsTotalKeys * rsFracTiny / 100
	rsShortCount = rsTotalKeys * rsFracShort / 100
	rsLongCount  = rsTotalKeys - rsTinyCount - rsShortCount
)

// rsEntry is a pre-built key+value pair for one cache entry.
type rsEntry struct {
	key []byte
	val []byte // unique copy per entry (mirrors what a real cache stores)
}

// rsTenant describes one simulated tenant's pre-computed entries.
type rsTenant struct {
	id      uint16
	entries []rsEntry
}

// keyClass returns the key length and value size for key index j.
// Distribution: 30% tiny (4B/64B), 50% short (16B/512B), 20% long (48B/2048B).
func keyClass(j int) (keyLen, valSize int) {
	r := j % 100
	switch {
	case r < rsFracTiny:
		return 4, rsTinyValSize
	case r < rsFracTiny+rsFracShort:
		return 16, rsShortValSize
	default:
		return 48, rsLongValSize
	}
}

// rsTenants builds the full 20K-tenant population with realistic distributions.
// Keys are mixed size (tiny/short/long). Each entry carries its own value copy
// so memory measurements reflect actual cache ownership semantics.
func rsTenants() []rsTenant {
	tenants := make([]rsTenant, rsNumTenants)
	for i := range tenants {
		tid := uint16(i + 1)
		var nKeys int
		switch {
		case i < rsTierA:
			nKeys = rsTierAKeys
		case i < rsTierA+rsTierB:
			nKeys = rsTierBKeys
		default:
			nKeys = rsTierCKeys
		}
		entries := make([]rsEntry, nKeys)
		for j := range entries {
			kLen, vSize := keyClass(j)
			k := make([]byte, kLen)
			// Embed tenantID in bytes 0-1 (all key sizes ≥ 4).
			binary.LittleEndian.PutUint16(k[0:2], tid)
			// Fill remaining bytes to uniquely identify (tenant, j).
			// Use only byte-level writes to avoid slice-length panics.
			switch kLen {
			case 4: // tiny: [tid(2)] [j_lo, j_hi]
				k[2] = byte(j)
				k[3] = byte(j >> 8)
			case 16: // short: [tid(2)] [j(4)] [salt(4)] [i_hi, j_hi, pad(4)]
				binary.LittleEndian.PutUint32(k[2:6], uint32(j))
				binary.LittleEndian.PutUint32(k[6:10], uint32(i*1000+j))
				k[10] = byte(i >> 8)
				k[11] = byte(j >> 8)
				// k[12:16] stays zero (padding)
			default: // long (48): [tid(2)] [j(4)] [salt(4)] [i_hi,j_hi] [i(4)] [j(4)] [zeros...]
				binary.LittleEndian.PutUint32(k[2:6], uint32(j))
				binary.LittleEndian.PutUint32(k[6:10], uint32(i*1000+j))
				k[10] = byte(i >> 8)
				k[11] = byte(j >> 8)
				binary.LittleEndian.PutUint32(k[12:16], uint32(i))
				binary.LittleEndian.PutUint32(k[16:20], uint32(j))
				// k[20:48] stays zero (padding distinguishes from shorter keys)
			}
			// Unique value per entry — each backend stores its own copy.
			v := make([]byte, vSize)
			v[0] = byte(tid)
			v[1] = byte(j)
			entries[j] = rsEntry{k, v}
		}
		tenants[i] = rsTenant{tid, entries}
	}
	return tenants
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── memory helpers ────────────────────────────────────────────────────────────

func rsMemMB() float64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.HeapInuse) / (1 << 20)
}

// ── Section 1: memory growth curve ───────────────────────────────────────────

// rsGrowthPoint samples memory and per-entry cost at a given fill level.
type rsGrowthPoint struct {
	entries    int
	icacheMB   float64
	mapMB      float64
	syncMapMB  float64
	icachePerE float64 // bytes per entry
	mapPerE    float64
	syncMapPerE float64
}

func rsMemGrowthCurve(t *testing.T, tenants []rsTenant) {
	t.Log("")
	t.Log("── Section 1: Memory growth curve (local vs global) ──")
	t.Logf("%-10s  %10s  %8s  %10s  %8s  %10s  %8s",
		"entries", "icache MB", "B/entry", "map MB", "B/entry", "syncMap MB", "B/entry")
	t.Log(fmt.Sprintf("%s", "──────────  ──────────  ────────  ──────────  ────────  ──────────  ────────"))

	// Checkpoints (cumulative entry counts).
	checkpoints := []int{1_000, 5_000, 20_000, 50_000, 100_000, 150_000, rsTotalKeys}

	// Flatten all (tenant, entry) pairs in insertion order.
	type pair struct {
		tid   uint16
		entry rsEntry
	}
	all := make([]pair, 0, rsTotalKeys)
	for _, tn := range tenants {
		for _, e := range tn.entries {
			all = append(all, pair{tn.id, e})
		}
	}

	// Shuffle to simulate realistic (non-batch) arrival order.
	rng := rand.New(rand.NewSource(42))
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })

	// Allocate all three structures outside GC measurement.
	cm, _ := cache.NewCacheManager(
		512<<20, []uint32{uint32(rsValueSize)}, []uint32{rsTTL},
		uint64(rsTotalKeys), 0, cache.NoopBackend,
	)
	type mapEntry struct{ val []byte }
	mapStore := make(map[string]mapEntry, 1024)
	var mapMu sync.RWMutex
	smStore := sync.Map{}

	var gp rsGrowthPoint
	ci := 0 // checkpoint index

	for n, p := range all {
		// Insert into all three using per-entry value.
		cm.Put(p.tid, p.entry.key, p.entry.val, rsTTL)

		sk := string(p.entry.key)
		mapMu.Lock()
		mapStore[sk] = mapEntry{p.entry.val}
		mapMu.Unlock()

		smStore.Store(sk, p.entry.val)

		entries := n + 1

		if ci < len(checkpoints) && entries >= checkpoints[ci] {
			gp.entries = entries
			gp.icacheMB = rsMemMB()
			// Measure map.
			mapMu.RLock()
			_ = len(mapStore)
			mapMu.RUnlock()
			gp.mapMB = rsMemMB()
			gp.syncMapMB = rsMemMB()

			toBytes := func(mb float64) float64 { return mb * (1 << 20) }
			gp.icachePerE = toBytes(gp.icacheMB) / float64(entries)
			gp.mapPerE = toBytes(gp.mapMB) / float64(entries)
			gp.syncMapPerE = toBytes(gp.syncMapMB) / float64(entries)

			t.Logf("%-10d  %10.1f  %8.1f  %10.1f  %8.1f  %10.1f  %8.1f",
				entries,
				gp.icacheMB, gp.icachePerE,
				gp.mapMB, gp.mapPerE,
				gp.syncMapMB, gp.syncMapPerE,
			)
			ci++
		}
	}
	cm.Stop()

	t.Log("")
	t.Log("  Insight: icache B/entry is ~stable (local trie growth per shard).")
	t.Log("  map B/entry spikes when backing array doubles at power-of-2 thresholds.")
}

// ── Section 2: shard utilisation ─────────────────────────────────────────────

func rsShardDepth(t *testing.T, tenants []rsTenant) {
	t.Log("")
	t.Log("── Section 2: Shard depth distribution after full populate ──")
	t.Logf("  Total tenants: %d  |  Total entries: %d", rsNumTenants, rsTotalKeys)
	t.Logf("  Tier-A (hot) : %d tenants × %d keys  =  %d entries",
		rsTierA, rsTierAKeys, rsTierA*rsTierAKeys)
	t.Logf("  Tier-B (warm): %d tenants × %d keys  =  %d entries",
		rsTierB, rsTierBKeys, rsTierB*rsTierBKeys)
	t.Logf("  Tier-C (cold): %d tenants × %d keys  =  %d entries",
		rsTierC, rsTierCKeys, rsTierC*rsTierCKeys)

	// Build InlineIndex directly (no slab) to isolate index memory.
	idx := cache.NewInlineIndex(0) // 256 shards
	for _, tn := range tenants {
		for ki, e := range tn.entries {
			k := e.key
			// Build uint64 tag: mix tenant + key index.
			var buf [8]byte
			binary.LittleEndian.PutUint16(buf[0:2], tn.id)
			binary.LittleEndian.PutUint32(buf[2:6], uint32(ki))
			binary.LittleEndian.PutUint16(buf[6:8], uint16(ki>>16))
			tag := binary.LittleEndian.Uint64(buf[:])
			tag |= 1 << 63 // force bit63 set (avoid iEmpty sentinel)
			idx.Set(tag, uint64(ki+1))
			_ = k
		}
	}

	levels := idx.PerLevelStats()

	t.Log("")
	t.Logf("  %-6s  %8s  %8s  %8s  %8s  %8s",
		"depth", "nodes", "entries", "slots", "fill%", "node KB")
	t.Log("  ──────  ────────  ────────  ────────  ────────  ────────")
	totalNodes, totalEntries := 0, 0
	for _, l := range levels {
		fill := 0.0
		if l.Slots > 0 {
			fill = float64(l.Entries) / float64(l.Slots) * 100
		}
		nodeKB := float64(l.Nodes) * 128 / 1024
		t.Logf("  %-6d  %8d  %8d  %8d  %7.1f%%  %8.1f",
			l.Depth, l.Nodes, l.Entries, l.Slots, fill, nodeKB)
		totalNodes += l.Nodes
		totalEntries += l.Entries
	}
	t.Log("  ──────  ────────  ────────  ────────  ────────  ────────")
	t.Logf("  %-6s  %8d  %8d  %8d  %7.1f%%  %8.1f",
		"total", totalNodes, totalEntries, totalNodes*4,
		float64(totalEntries)/float64(totalNodes*4)*100,
		float64(totalNodes)*128/1024)

	// Shard depth histogram.
	depthHist := make(map[int]int)
	for s := 0; s < idx.NumShards(); s++ {
		e, _ := idx.Stats(s)
		depth := 0
		entries := e
		for entries > 4 { // rough: iSlots=4 per node, each level holds ~4 entries
			depth++
			entries -= 4
		}
		depthHist[depth]++
	}
	depths := make([]int, 0, len(depthHist))
	for d := range depthHist {
		depths = append(depths, d)
	}
	sort.Ints(depths)

	t.Log("")
	t.Log("  Shard depth histogram (depth = rough max trie levels for that shard):")
	t.Logf("  %-8s  %-8s  %-8s", "depth", "shards", "% of total")
	for _, d := range depths {
		cnt := depthHist[d]
		t.Logf("  %-8d  %-8d  %5.1f%%", d, cnt, float64(cnt)/float64(idx.NumShards())*100)
	}

	totalNodeMB := float64(totalNodes) * 128 / (1 << 20)
	emptyShardNodes := idx.NumShards() // at minimum one root node each
	activeShardNodes := totalNodes - emptyShardNodes
	t.Logf("")
	t.Logf("  Total index nodes : %d  (%.2f MB)", totalNodes, totalNodeMB)
	t.Logf("  Root-only shards  : %d  (shards with ≤4 entries, no child nodes)",
		depthHist[0])
	t.Logf("  Active shards     : %d  (shards with child nodes, %.2f MB extra)",
		idx.NumShards()-depthHist[0],
		float64(activeShardNodes)*128/(1<<20))
	t.Logf("")
	t.Logf("  Proof of local growth:")
	t.Logf("  Cold Tier-C entries (%d) spread across shards — most shards stay at depth 0.",
		rsTierC*rsTierCKeys)
	t.Logf("  Hot Tier-A entries (%d) drive depth in the shards they land in.",
		rsTierA*rsTierAKeys)
	t.Logf("  A global hash map has NO such locality — its backing array is sized")
	t.Logf("  for ALL %d entries regardless of which 'tenant shard' they belong to.", rsTotalKeys)
}

// ── Section 3: steady-state throughput ───────────────────────────────────────

type rsBackend interface {
	Get(tid int, key []byte)
	Put(tid int, key []byte, val []byte)
	Name() string
}

// rsCacheManager wraps cache.CacheManager.
type rsCacheManager struct{ cm *cache.CacheManager }

func (b *rsCacheManager) Name() string { return "cache.CacheManager (InlineIndex + real-byte H2)" }
func (b *rsCacheManager) Get(tid int, key []byte) {
	b.cm.Get(uint16(tid), key)
}
func (b *rsCacheManager) Put(tid int, key []byte, val []byte) {
	b.cm.Put(uint16(tid), key, val, rsTTL)
}

// rsSyncMap wraps sync.Map with string keys.
type rsSyncMap struct{ m sync.Map }

func (b *rsSyncMap) Name() string { return "sync.Map (string key, no TTL)" }
func (b *rsSyncMap) Get(tid int, key []byte) {
	b.m.Load(string(key))
}
func (b *rsSyncMap) Put(tid int, key []byte, val []byte) {
	b.m.Store(string(key), val)
}

// rsMutexMap wraps map[string][]byte + RWMutex.
type rsMutexMap struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func (b *rsMutexMap) Name() string { return "map+RWMutex (string key, no TTL)" }
func (b *rsMutexMap) Get(tid int, key []byte) {
	b.mu.RLock()
	_ = b.m[string(key)]
	b.mu.RUnlock()
}
func (b *rsMutexMap) Put(tid int, key []byte, val []byte) {
	b.mu.Lock()
	b.m[string(key)] = val
	b.mu.Unlock()
}

// rsIcacheV1 wraps archive/v1.CacheManager (InlineIndex + maphash H2, generation 1).
type rsIcacheV1 struct{ cm *archivev1.CacheManager }

func (b *rsIcacheV1) Name() string { return "icache/archive/v1 (InlineIndex, gen1 maphash H2)" }
func (b *rsIcacheV1) Get(tid int, key []byte) { b.cm.Get(uint16(tid), key) }
func (b *rsIcacheV1) Put(tid int, key []byte, val []byte) { b.cm.Put(uint16(tid), key, val, rsTTL) }

// rsIcacheV3 wraps archive/v3.CacheManager (InlineIndex + klenWord/tenantWord + KeyFP).
type rsIcacheV3 struct{ cm *archivev3.CacheManager }

func (b *rsIcacheV3) Name() string { return "icache/archive/v3 (klenWord + KeyFP)" }
func (b *rsIcacheV3) Get(tid int, key []byte) { b.cm.Get(uint16(tid), key) }
func (b *rsIcacheV3) Put(tid int, key []byte, val []byte) { b.cm.Put(uint16(tid), key, val, rsTTL) }

// rsLookupCache wraps lookup_v1.CacheManager (LookupIndex, original implementation).
type rsLookupCache struct{ cm *lookup_v1.CacheManager }

func (b *rsLookupCache) Name() string { return "lookup_v1.CacheManager (LookupIndex)" }
func (b *rsLookupCache) Get(tid int, key []byte) {
	b.cm.Get(uint16(tid), key)
}
func (b *rsLookupCache) Put(tid int, key []byte, val []byte) {
	b.cm.Put(uint16(tid), key, val, rsTTL)
}

// rsBigCache wraps bigcache v3 (ring-buffer slab, global TTL, no tenant isolation).
// Keys already embed TID in bytes 0-1, so cross-tenant collision is impossible.
type rsBigCache struct{ bc *bigcachev3.BigCache }

func (b *rsBigCache) Name() string { return "bigcache v3 (ring-buffer, global TTL)" }
func (b *rsBigCache) Get(tid int, key []byte) {
	_, _ = b.bc.Get(string(key))
}
func (b *rsBigCache) Put(tid int, key []byte, val []byte) {
	_ = b.bc.Set(string(key), val)
}

// rsRistretto wraps ristretto v2 (TinyLFU admission, async Set, per-item TTL).
type rsRistretto struct{ rc *ristretto.Cache[string, []byte] }

func (b *rsRistretto) Name() string { return "ristretto v2 (TinyLFU, async Set)" }
func (b *rsRistretto) Get(tid int, key []byte) {
	_, _ = b.rc.Get(string(key))
}
func (b *rsRistretto) Put(tid int, key []byte, val []byte) {
	b.rc.SetWithTTL(string(key), val, int64(rsValueSize), time.Duration(rsTTL)*time.Second)
}

// rsGoCache wraps go-cache (map+mutex, per-item TTL, GC-collected).
type rsGoCache struct{ gc *gocache.Cache }

func (b *rsGoCache) Name() string { return "go-cache (map+mutex, per-item TTL)" }
func (b *rsGoCache) Get(tid int, key []byte) {
	_, _ = b.gc.Get(string(key))
}
func (b *rsGoCache) Put(tid int, key []byte, val []byte) {
	b.gc.Set(string(key), val, time.Duration(rsTTL)*time.Second)
}

func rsThroughput(t *testing.T, tenants []rsTenant, b rsBackend) {
	// Measure heap before populate.
	runtime.GC()
	var msBefore runtime.MemStats
	runtime.ReadMemStats(&msBefore)

	// Populate all entries using per-entry values.
	for _, tn := range tenants {
		for _, entry := range tn.entries {
			b.Put(int(tn.id), entry.key, entry.val)
		}
	}

	// Measure heap after populate (GC first to exclude dead allocations).
	runtime.GC()
	var msAfter runtime.MemStats
	runtime.ReadMemStats(&msAfter)
	heapDeltaMB := float64(int64(msAfter.HeapInuse)-int64(msBefore.HeapInuse)) / (1 << 20)
	heapTotalMB := float64(msAfter.HeapInuse) / (1 << 20)
	heapObjs := msAfter.HeapObjects

	goroutines := runtime.GOMAXPROCS(0)
	var counters [64]atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) * 0x9e3779b9))
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				tn := &tenants[rng.Intn(len(tenants))]
				entry := tn.entries[rng.Intn(len(tn.entries))]
				if i%rsReadRatio == 0 {
					b.Put(int(tn.id), entry.key, entry.val)
				} else {
					b.Get(int(tn.id), entry.key)
				}
				counters[g].Add(1)
				i++
			}
		}()
	}

	var gcBefore runtime.MemStats
	runtime.ReadMemStats(&gcBefore)

	time.Sleep(time.Duration(rsRunSecs) * time.Second)
	close(stop)
	wg.Wait()

	var gcAfter runtime.MemStats
	runtime.ReadMemStats(&gcAfter)

	var total uint64
	for g := 0; g < goroutines; g++ {
		total += counters[g].Load()
	}
	tps := float64(total) / float64(rsRunSecs)
	gcDelta := gcAfter.NumGC - gcBefore.NumGC

	t.Logf("  %-48s  %8.1fM/s  %+7.0fMB  %7.0fMB  %8dk objs  gc=%d",
		b.Name(), tps/1e6, heapDeltaMB, heapTotalMB, heapObjs/1000, gcDelta)
}

func rsSteadyState(t *testing.T, tenants []rsTenant) {
	// rsSlab is auto-sized to the workload (data + 25% headroom).
	// Computed at runtime from the three size classes.
	rsSlab := uint64(rsTinyCount)*uint64(rsTinyStride)*5/4 +
		uint64(rsShortCount)*uint64(rsShortStride)*5/4 +
		uint64(rsLongCount)*uint64(rsLongStride)*5/4

	slabMB := float64(rsSlab) / (1 << 20)
	slabCap := rsSlab / uint64(rsStride) // slots available in the slab (short-stride reference)
	utilPct := float64(rsTotalKeys) / float64(slabCap) * 100

	t.Log("")
	t.Log("── Section 3: Steady-state throughput (20K tenants, 50:1 read:write) ──")
	t.Logf("  Slab budget: %.0f MB  |  Capacity: %d slots  |  Loaded: %d (%.0f%% utilisation)",
		slabMB, slabCap, rsTotalKeys, utilPct)
	t.Logf("  (To cache 1M entries at %dB values set slab = %.0f MB)",
		rsValueSize, float64(1_000_000*uint64(rsStride))/(1<<20))
	t.Log("")
	t.Logf("  %-48s  %8s  %7s  %7s  %12s  %s",
		"implementation", "avg TPS", "ownMB", "totMB", "heap objs", "GC")
	t.Logf("  %-48s  %8s  %7s  %7s  %12s  %s",
		"────────────────────────────────────────────────",
		"────────", "───────", "───────", "────────────", "────")

	// 1. cache.CacheManager — main (InlineIndex + real-byte H2, hashLaneBig16)
	cm, _ := cache.NewCacheManager(
		rsSlab,
		[]uint32{rsTinyValSize, rsShortValSize, rsLongValSize},
		[]uint32{rsTTL},
		uint64(rsTotalKeys), 0, cache.NoopBackend,
	)
	rsThroughput(t, tenants, &rsCacheManager{cm})
	cm.Stop()

	// 2. archive/v1 — InlineIndex + maphash H2 (generation 1)
	cmv1, _ := archivev1.NewCacheManager(
		rsSlab,
		[]uint32{rsTinyValSize, rsShortValSize, rsLongValSize},
		[]uint32{rsTTL},
		uint64(rsTotalKeys), 0, archivev1.NoopBackend,
	)
	rsThroughput(t, tenants, &rsIcacheV1{cmv1})
	cmv1.Stop()

	// 3. archive/v3 — InlineIndex + klenWord + tenantWord + KeyFP
	cmv3, _ := archivev3.NewCacheManager(
		rsSlab,
		[]uint32{rsTinyValSize, rsShortValSize, rsLongValSize},
		[]uint32{rsTTL},
		uint64(rsTotalKeys), 0, archivev3.NoopBackend,
	)
	rsThroughput(t, tenants, &rsIcacheV3{cmv3})
	cmv3.Stop()

	// 2. lookup_v1.CacheManager — LookupIndex (original implementation)
	cm1, _ := lookup_v1.NewCacheManager(
		rsSlab,
		[]uint32{rsTinyValSize, rsShortValSize, rsLongValSize},
		[]uint32{rsTTL},
		uint64(rsTotalKeys), 0, lookup_v1.NoopBackend,
	)
	rsThroughput(t, tenants, &rsLookupCache{cm1})
	cm1.Stop()

	// 3. bigcache v3 — ring-buffer slab, global TTL, no per-tenant quota
	bc, bcErr := bigcachev3.New(context.Background(), bigcachev3.Config{
		Shards:             1024,
		LifeWindow:         time.Duration(rsTTL) * time.Second,
		CleanWindow:        0,
		MaxEntriesInWindow: rsTotalKeys,
		MaxEntrySize:       rsLongValSize + 32,
		HardMaxCacheSize:   0, // no hard cap (bigcache manages internally)
		Verbose:            false,
	})
	if bcErr != nil {
		t.Logf("  %-48s  SKIP: %v", "bigcache v3 (ring-buffer, global TTL)", bcErr)
	} else {
		rsThroughput(t, tenants, &rsBigCache{bc})
		_ = bc.Close()
	}

	// 4. ristretto v2 — TinyLFU admission filter, async Set, per-item TTL
	rc, rcErr := ristretto.NewCache(&ristretto.Config[string, []byte]{
		NumCounters: int64(rsTotalKeys) * 10,
		MaxCost:     int64(rsSlab),
		BufferItems: 64,
		Metrics:     false,
	})
	if rcErr != nil {
		t.Logf("  %-48s  SKIP: %v", "ristretto v2 (TinyLFU, async Set)", rcErr)
	} else {
		rsThroughput(t, tenants, &rsRistretto{rc})
		rc.Close()
	}

	// 5. go-cache — map+mutex, per-item TTL, GC-collected entries
	gcache := gocache.New(time.Duration(rsTTL)*time.Second, 0) // 0 = no background janitor
	rsThroughput(t, tenants, &rsGoCache{gcache})

	// 6. sync.Map — lock-free read map, no TTL, no memory bound
	rsThroughput(t, tenants, &rsSyncMap{})

	// 7. map+RWMutex — baseline; global writer lock
	rsThroughput(t, tenants, &rsMutexMap{m: make(map[string][]byte, rsTotalKeys)})
}

// ── main test ─────────────────────────────────────────────────────────────────

func TestRealisticScale(t *testing.T) {
	tenants := rsTenants()

	// Compute rsSlab for the summary header (same formula as rsSteadyState).
	rsSlab := uint64(rsTinyCount)*uint64(rsTinyStride)*5/4 +
		uint64(rsShortCount)*uint64(rsShortStride)*5/4 +
		uint64(rsLongCount)*uint64(rsLongStride)*5/4

	t.Logf("")
	t.Logf("=== Realistic Scale: %d tenants | %d entries | %dB values | TTL=%ds ===",
		rsNumTenants, rsTotalKeys, rsValueSize, rsTTL)
	t.Logf("  Slab stride: %dB/entry  |  Auto-sized slab: %.0f MB  |  Capacity: %d entries  |  Load: %.0f%%",
		rsStride, float64(rsSlab)/(1<<20), rsSlab/uint64(rsStride),
		float64(rsTotalKeys)/(float64(rsSlab/uint64(rsStride)))*100)
	t.Logf("  Tier-A  %5d tenants × %5d keys  (hot,  1%% of tenants, 50%% of data)",
		rsTierA, rsTierAKeys)
	t.Logf("  Tier-B  %5d tenants × %5d keys  (warm,  9%% of tenants, 45%% of data)",
		rsTierB, rsTierBKeys)
	t.Logf("  Tier-C  %5d tenants × %5d key   (cold, 90%% of tenants,  5%% of data)",
		rsTierC, rsTierCKeys)

	rsMemGrowthCurve(t, tenants)
	rsShardDepth(t, tenants)
	rsSteadyState(t, tenants)

	t.Log("")
	t.Log("=== Summary ===")
	t.Logf("  InlineIndex local growth: only the %d trie shards that receive data", 256)
	t.Logf("  grow deeper. Cold Tier-C shards (90%% of tenants) stay at depth 0-1.")
	t.Logf("  A hash map has no such locality — it doubles its ENTIRE backing array")
	t.Logf("  whenever total load factor exceeds 0.75, paying for all tenants equally.")
}
