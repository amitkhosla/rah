//go:build !race

package cache_test

// TestVsAll benchmarks cache.CacheManager (InlineIndex) against
// cache.CacheManager (LookupIndex) and popular Go cache libraries under
// identical workload conditions.
//
// Two workloads:
//
//  1. Small keys (3–6 bytes): routes through tinyIdx (lossless tag lane).
//  2. Big keys (16–32 bytes): routes through hashIdx (H2 fingerprint lane).
//
// Settings:
//
//	10 tenants × 10 000 keys = 100 000 entries  |  256 B values  |  10 s TTL
//	99% reads, 1% writes  |  GOMAXPROCS goroutines  |  5 s windows
//
// Run with:
//
//	go test -v -run TestVsAll -timeout 600s ./internal/icache/
import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bigcachev3 "github.com/allegro/bigcache/v3"
	ristretto "github.com/dgraph-io/ristretto/v2"
	gocache "github.com/patrickmn/go-cache"

	lookup_v1 "rah/internal/cache/archive/lookup_v1"
	"rah/internal/cache"
)

// ── workload constants ────────────────────────────────────────────────────────

const (
	bvNumTenants    = 10
	bvKeysPerTenant = 10_000
	bvTotalKeys     = bvNumTenants * bvKeysPerTenant
	bvValueSize     = 256
	bvTTL           = uint32(10)
	bvRunDuration   = 2 * time.Second  // reduced for CI; was 8s (7 backends × 2 sections × 8s ≈ 112s)
	bvSampleEvery   = 1 * time.Second  // 2 sample windows per run
	bvWriteEvery    = 100 // 1 write per 100 ops → ~1% writes
	bvHardMaxMB     = 32
)

// ── pre-computed key tables ───────────────────────────────────────────────────

var (
	// Small keys: 3-byte binary  {i&0xFF, i>>8, tid+1}
	bvSmallKeys [bvNumTenants][bvKeysPerTenant][]byte

	// Big keys: 20-byte string key "t{tid}:k{i}:longprefix"
	bvBigKeys [bvNumTenants][bvKeysPerTenant][]byte

	// String keys for map-backed caches (big key path)
	bvStrKeys [bvNumTenants][bvKeysPerTenant]string

	// Pre-computed fingerprints for cache.LookupIndex comparison
	bvSmallFPs [bvNumTenants][bvKeysPerTenant][16]byte
	bvBigFPs   [bvNumTenants][bvKeysPerTenant][16]byte
)

func init() {
	var buf [64]byte
	for tid := 0; tid < bvNumTenants; tid++ {
		for i := 0; i < bvKeysPerTenant; i++ {
			// Small key: 3-byte binary
			bvSmallKeys[tid][i] = []byte{byte(i & 0xFF), byte(i >> 8), byte(tid + 1)}

			// Big key: 20-byte binary (tenantID + index + filler)
			big := make([]byte, 20)
			binary.LittleEndian.PutUint16(big[0:2], uint16(tid+1))
			binary.LittleEndian.PutUint64(big[2:10], uint64(i))
			for j := 10; j < 20; j++ {
				big[j] = byte((i + j) & 0xFF)
			}
			bvBigKeys[tid][i] = big

			// String keys for map-based caches
			b := buf[:0]
			b = append(b, 't')
			b = strconv.AppendInt(b, int64(tid+1), 10)
			b = append(b, ':')
			b = append(b, 'k')
			b = strconv.AppendInt(b, int64(i), 10)
			bvStrKeys[tid][i] = string(b)

			// Fingerprints for LookupIndex tests
			bvSmallFPs[tid][i] = lookup_v1.Hash128(uint16(tid+1), bvSmallKeys[tid][i])
			bvBigFPs[tid][i] = lookup_v1.Hash128(uint16(tid+1), bvBigKeys[tid][i])
		}
	}
}

// ── result type ───────────────────────────────────────────────────────────────

type bvResult struct {
	name        string
	ownMB       float64 // HeapInuse delta after populate
	totalMB     float64 // process-wide HeapInuse after populate
	heapObjs    uint64
	avgTPS      float64
	gcPerWin    []uint32
	pausePerWin []float64 // ms
}

// ── memory helpers ────────────────────────────────────────────────────────────

type bvMemSnap struct {
	inuse   float64
	objects uint64
}

func bvMemTake() bvMemSnap {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return bvMemSnap{float64(ms.HeapInuse) / (1 << 20), ms.HeapObjects}
}

type bvGCMark struct {
	cycles  uint32
	pauseNs uint64
}

func bvGCTake() bvGCMark {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return bvGCMark{ms.NumGC, ms.PauseTotalNs}
}

// ── hot-loop runner ───────────────────────────────────────────────────────────

// runBV runs one backend under the standard hot loop.
// getFn and putFn receive (tenantID 1-based, keyIndex 0-based).
func runBV(
	name string,
	populate func(),
	getFn func(tid int, i int),
	putFn func(tid int, i int),
) bvResult {
	base := bvMemTake()
	populate()
	after := bvMemTake()

	goroutines := runtime.GOMAXPROCS(0)
	counters := make([]atomic.Uint64, goroutines)
	stop := make(chan struct{})

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			i := g * 997 // prime offset so goroutines don't all hit the same key
			for {
				select {
				case <-stop:
					return
				default:
				}
				tid := i%bvNumTenants + 1 // 1-based
				key := i % bvKeysPerTenant
				if i%bvWriteEvery == 0 {
					putFn(tid, key)
				} else {
					getFn(tid, key)
				}
				counters[g].Add(1)
				i++
			}
		}()
	}

	windows := int(bvRunDuration / bvSampleEvery)
	gcPerWin := make([]uint32, windows)
	pausePerWin := make([]float64, windows)
	tpsPerWin := make([]float64, windows)

	for w := 0; w < windows; w++ {
		prev := make([]uint64, goroutines)
		for g := 0; g < goroutines; g++ {
			prev[g] = counters[g].Load()
		}
		prevGC := bvGCTake()
		time.Sleep(bvSampleEvery)
		var ops uint64
		for g := 0; g < goroutines; g++ {
			ops += counters[g].Load() - prev[g]
		}
		cur := bvGCTake()
		gcPerWin[w] = cur.cycles - prevGC.cycles
		pausePerWin[w] = float64(cur.pauseNs-prevGC.pauseNs) / 1e6
		tpsPerWin[w] = float64(ops) / bvSampleEvery.Seconds()
	}

	close(stop)
	wg.Wait()

	var totalTPS float64
	for _, t := range tpsPerWin {
		totalTPS += t
	}
	return bvResult{
		name:        name,
		ownMB:       after.inuse - base.inuse,
		totalMB:     after.inuse,
		heapObjs:    after.objects - base.objects,
		avgTPS:      totalTPS / float64(windows),
		gcPerWin:    gcPerWin,
		pausePerWin: pausePerWin,
	}
}

// ── TTL-aware map types ───────────────────────────────────────────────────────

type bvEntry struct {
	val       []byte
	expiresAt int64
}

type bvMutexMap struct {
	mu sync.RWMutex
	m  map[string]bvEntry
}

func newBVMutexMap(cap int) *bvMutexMap {
	return &bvMutexMap{m: make(map[string]bvEntry, cap)}
}

func (m *bvMutexMap) Set(key string, val []byte) {
	m.mu.Lock()
	m.m[key] = bvEntry{val, time.Now().Unix() + int64(bvTTL)}
	m.mu.Unlock()
}

func (m *bvMutexMap) Get(key string) ([]byte, bool) {
	m.mu.RLock()
	e, ok := m.m[key]
	m.mu.RUnlock()
	if !ok || time.Now().Unix() > e.expiresAt {
		return nil, false
	}
	return e.val, true
}

type bvSyncMap struct{ m sync.Map }

func (s *bvSyncMap) Set(key string, val []byte) {
	s.m.Store(key, bvEntry{val, time.Now().Unix() + int64(bvTTL)})
}

func (s *bvSyncMap) Get(key string) ([]byte, bool) {
	v, ok := s.m.Load(key)
	if !ok {
		return nil, false
	}
	e := v.(bvEntry)
	if time.Now().Unix() > e.expiresAt {
		return nil, false
	}
	return e.val, true
}

// ── main comparison test ──────────────────────────────────────────────────────

func TestVsAll(t *testing.T) {
	val256 := make([]byte, bvValueSize)

	printSection := func(title string, results []bvResult) {
		t.Logf("── %s ──", title)
		t.Logf("%-38s  %8s  %9s  %9s  %8s  %12s",
			"implementation", "own MB", "total MB", "heap objs", "GC/win", "avg TPS")
		t.Logf("%-38s  %8s  %9s  %9s  %8s  %12s",
			"--------------------------------------", "--------", "---------",
			"---------", "--------", "------------")
		for _, r := range results {
			avgGC := 0.0
			for _, g := range r.gcPerWin {
				avgGC += float64(g)
			}
			if len(r.gcPerWin) > 0 {
				avgGC /= float64(len(r.gcPerWin))
			}
			t.Logf("%-38s  %8.1f  %9.1f  %9d  %8.2f  %12.0f  (~%.1fM/s)",
				r.name, r.ownMB, r.totalMB, r.heapObjs,
				avgGC, r.avgTPS, r.avgTPS/1e6)
		}
		t.Log("")
	}

	t.Logf("\n=== CacheManager Comparison: %d tenants × %dk keys = %dk entries | %dB values | %ds TTL | %d goroutines ===\n",
		bvNumTenants, bvKeysPerTenant/1000, bvTotalKeys/1000, bvValueSize, bvTTL,
		runtime.GOMAXPROCS(0))

	// ─────────────────────────────────────────────────────────────────────────
	// SECTION 1 — Small keys (3 bytes, tinyIdx lane)
	// ─────────────────────────────────────────────────────────────────────────

	t.Log("Preparing Section 1: small keys (3-byte binary, tinyIdx lane)...")

	// 1a. cache.CacheManager with InlineIndex
	var icm1 *cache.CacheManager
	icm1Res := runBV("cache.CacheManager (InlineIndex, small keys)",
		func() {
			icm1, _ = cache.NewCacheManager(
				bvHardMaxMB<<20, []uint32{256}, []uint32{10},
				bvTotalKeys, 0, cache.NoopBackend,
			)
			for tid := 0; tid < bvNumTenants; tid++ {
				for i := 0; i < bvKeysPerTenant; i++ {
					icm1.Put(uint16(tid+1), bvSmallKeys[tid][i], val256, bvTTL)
				}
			}
		},
		func(tid, i int) { icm1.Get(uint16(tid), bvSmallKeys[tid-1][i]) },
		func(tid, i int) { icm1.Put(uint16(tid), bvSmallKeys[tid-1][i], val256, bvTTL) },
	)
	icm1.Stop()

	// 1b. lookup_v1.CacheManager with LookupIndex
	var cm1 *lookup_v1.CacheManager
	cm1Res := runBV("lookup_v1.CacheManager (LookupIndex, small keys)",
		func() {
			cm1, _ = lookup_v1.NewCacheManager(
				bvHardMaxMB<<20, []uint32{256}, []uint32{10},
				bvTotalKeys, 0, lookup_v1.NoopBackend,
			)
			for tid := 0; tid < bvNumTenants; tid++ {
				for i := 0; i < bvKeysPerTenant; i++ {
					cm1.Put(uint16(tid+1), bvSmallKeys[tid][i], val256, bvTTL)
				}
			}
		},
		func(tid, i int) { cm1.Get(uint16(tid), bvSmallKeys[tid-1][i]) },
		func(tid, i int) { cm1.Put(uint16(tid), bvSmallKeys[tid-1][i], val256, bvTTL) },
	)
	cm1.Stop()

	// 1c. map+RWMutex (string keys, baseline)
	var mm1 *bvMutexMap
	mm1Res := runBV("map+RWMutex (string key, small keys)",
		func() {
			mm1 = newBVMutexMap(bvTotalKeys)
			for tid := 0; tid < bvNumTenants; tid++ {
				for i := 0; i < bvKeysPerTenant; i++ {
					v := make([]byte, bvValueSize)
					copy(v, val256)
					mm1.Set(bvStrKeys[tid][i], v)
				}
			}
		},
		func(tid, i int) { mm1.Get(bvStrKeys[tid-1][i]) },
		func(tid, i int) {
			v := make([]byte, bvValueSize)
			copy(v, val256)
			mm1.Set(bvStrKeys[tid-1][i], v)
		},
	)

	// 1d. sync.Map (string keys, baseline)
	var sm1 bvSyncMap
	sm1Res := runBV("sync.Map (string key, small keys)",
		func() {
			for tid := 0; tid < bvNumTenants; tid++ {
				for i := 0; i < bvKeysPerTenant; i++ {
					v := make([]byte, bvValueSize)
					copy(v, val256)
					sm1.Set(bvStrKeys[tid][i], v)
				}
			}
		},
		func(tid, i int) { sm1.Get(bvStrKeys[tid-1][i]) },
		func(tid, i int) {
			v := make([]byte, bvValueSize)
			copy(v, val256)
			sm1.Set(bvStrKeys[tid-1][i], v)
		},
	)

	section1 := []bvResult{icm1Res, cm1Res, mm1Res, sm1Res}
	printSection("Section 1 — Small keys (3B binary, tinyIdx lane)", section1)

	// ─────────────────────────────────────────────────────────────────────────
	// SECTION 2 — Big keys (20 bytes, hashIdx lane)
	// ─────────────────────────────────────────────────────────────────────────

	t.Log("Preparing Section 2: big keys (20-byte binary, hashIdx lane)...")

	// 2a. cache.CacheManager with InlineIndex
	var icm2 *cache.CacheManager
	icm2Res := runBV("cache.CacheManager (InlineIndex, big keys)",
		func() {
			icm2, _ = cache.NewCacheManager(
				bvHardMaxMB<<20, []uint32{256}, []uint32{10},
				bvTotalKeys, 0, cache.NoopBackend,
			)
			for tid := 0; tid < bvNumTenants; tid++ {
				for i := 0; i < bvKeysPerTenant; i++ {
					icm2.Put(uint16(tid+1), bvBigKeys[tid][i], val256, bvTTL)
				}
			}
		},
		func(tid, i int) { icm2.Get(uint16(tid), bvBigKeys[tid-1][i]) },
		func(tid, i int) { icm2.Put(uint16(tid), bvBigKeys[tid-1][i], val256, bvTTL) },
	)
	icm2.Stop()

	// 2b. lookup_v1.CacheManager with LookupIndex
	var cm2 *lookup_v1.CacheManager
	cm2Res := runBV("lookup_v1.CacheManager (LookupIndex, big keys)",
		func() {
			cm2, _ = lookup_v1.NewCacheManager(
				bvHardMaxMB<<20, []uint32{256}, []uint32{10},
				bvTotalKeys, 0, lookup_v1.NoopBackend,
			)
			for tid := 0; tid < bvNumTenants; tid++ {
				for i := 0; i < bvKeysPerTenant; i++ {
					cm2.Put(uint16(tid+1), bvBigKeys[tid][i], val256, bvTTL)
				}
			}
		},
		func(tid, i int) { cm2.Get(uint16(tid), bvBigKeys[tid-1][i]) },
		func(tid, i int) { cm2.Put(uint16(tid), bvBigKeys[tid-1][i], val256, bvTTL) },
	)
	cm2.Stop()

	// 2c. bigcache v3 — ring-buffer, global TTL
	var bc *bigcachev3.BigCache
	bcRes := runBV("bigcache v3 (big keys)",
		func() {
			bc, _ = bigcachev3.New(context.Background(), bigcachev3.Config{
				Shards:             256,
				LifeWindow:         10 * time.Second,
				CleanWindow:        1 * time.Second,
				MaxEntriesInWindow: bvTotalKeys,
				MaxEntrySize:       300,
				HardMaxCacheSize:   bvHardMaxMB,
				Verbose:            false,
			})
			for tid := 0; tid < bvNumTenants; tid++ {
				for i := 0; i < bvKeysPerTenant; i++ {
					_ = bc.Set(bvStrKeys[tid][i], val256)
				}
			}
		},
		func(tid, i int) { _, _ = bc.Get(bvStrKeys[tid-1][i]) },
		func(tid, i int) { _ = bc.Set(bvStrKeys[tid-1][i], val256) },
	)
	_ = bc.Close()

	// 2d. ristretto v2 — TinyLFU admission, async Set
	var rc *ristretto.Cache[string, []byte]
	rcRes := runBV("ristretto v2 (big keys)",
		func() {
			rc, _ = ristretto.NewCache(&ristretto.Config[string, []byte]{
				NumCounters: int64(bvTotalKeys * 10),
				MaxCost:     bvHardMaxMB << 20,
				BufferItems: 64,
				Cost:        func(v []byte) int64 { return int64(len(v)) },
			})
			for tid := 0; tid < bvNumTenants; tid++ {
				for i := 0; i < bvKeysPerTenant; i++ {
					v := make([]byte, bvValueSize)
					copy(v, val256)
					rc.SetWithTTL(bvStrKeys[tid][i], v, int64(bvValueSize), 10*time.Second)
				}
			}
			rc.Wait()
		},
		func(tid, i int) { _, _ = rc.Get(bvStrKeys[tid-1][i]) },
		func(tid, i int) {
			v := make([]byte, bvValueSize)
			copy(v, val256)
			rc.SetWithTTL(bvStrKeys[tid-1][i], v, int64(bvValueSize), 10*time.Second)
		},
	)
	rc.Close()

	// 2e. go-cache — heap map, per-entry TTL
	var gc *gocache.Cache
	gcRes := runBV("go-cache (big keys)",
		func() {
			gc = gocache.New(10*time.Second, 1*time.Second)
			for tid := 0; tid < bvNumTenants; tid++ {
				for i := 0; i < bvKeysPerTenant; i++ {
					v := make([]byte, bvValueSize)
					copy(v, val256)
					gc.SetDefault(bvStrKeys[tid][i], v)
				}
			}
		},
		func(tid, i int) { gc.Get(bvStrKeys[tid-1][i]) },
		func(tid, i int) {
			v := make([]byte, bvValueSize)
			copy(v, val256)
			gc.Set(bvStrKeys[tid-1][i], v, 10*time.Second)
		},
	)

	// 2f. map+RWMutex (string keys, baseline)
	var mm2 *bvMutexMap
	mm2Res := runBV("map+RWMutex (string key, big keys)",
		func() {
			mm2 = newBVMutexMap(bvTotalKeys)
			for tid := 0; tid < bvNumTenants; tid++ {
				for i := 0; i < bvKeysPerTenant; i++ {
					v := make([]byte, bvValueSize)
					copy(v, val256)
					mm2.Set(bvStrKeys[tid][i], v)
				}
			}
		},
		func(tid, i int) { mm2.Get(bvStrKeys[tid-1][i]) },
		func(tid, i int) {
			v := make([]byte, bvValueSize)
			copy(v, val256)
			mm2.Set(bvStrKeys[tid-1][i], v)
		},
	)

	// 2g. sync.Map (string keys, baseline)
	var sm2 bvSyncMap
	sm2Res := runBV("sync.Map (string key, big keys)",
		func() {
			for tid := 0; tid < bvNumTenants; tid++ {
				for i := 0; i < bvKeysPerTenant; i++ {
					v := make([]byte, bvValueSize)
					copy(v, val256)
					sm2.Set(bvStrKeys[tid][i], v)
				}
			}
		},
		func(tid, i int) { sm2.Get(bvStrKeys[tid-1][i]) },
		func(tid, i int) {
			v := make([]byte, bvValueSize)
			copy(v, val256)
			sm2.Set(bvStrKeys[tid-1][i], v)
		},
	)

	section2 := []bvResult{icm2Res, cm2Res, bcRes, rcRes, gcRes, mm2Res, sm2Res}
	printSection("Section 2 — Big keys (20B binary, hashIdx lane)", section2)

	// ─────────────────────────────────────────────────────────────────────────
	// Per-window breakdown
	// ─────────────────────────────────────────────────────────────────────────

	t.Logf("── Per-5s window breakdown ──")
	for _, r := range append(section1, section2...) {
		line := fmt.Sprintf("%-42s", r.name)
		for w := range r.gcPerWin {
			line += fmt.Sprintf("  [%2d–%2ds gc=%d p=%.0fms tps=%.0fM]",
				w*5, (w+1)*5, r.gcPerWin[w], r.pausePerWin[w], r.avgTPS/1e6)
		}
		t.Log(line)
	}

	// ─────────────────────────────────────────────────────────────────────────
	// InlineIndex structural stats
	// ─────────────────────────────────────────────────────────────────────────

	t.Logf("\n── InlineIndex structure (small-key instance after %dk entries) ──",
		bvTotalKeys/1000)

	// Re-populate a fresh InlineIndex to measure node count
	probe := cache.NewInlineIndex(0)
	for tid := 0; tid < bvNumTenants; tid++ {
		for i := 0; i < bvKeysPerTenant; i++ {
			// Use raw small-key tags (routing bits only — tinyIdx path)
			probe.Set(uint64(tid+1)*0x9e3779b97f4a7c15^uint64(i)*0x517cc1b727220a95|(1<<63),
				uint64(i+1))
		}
	}
	t.Logf("InlineIndex : %d nodes × 128 B/node = %.1f MB node memory",
		probe.TotalNodes(), float64(probe.TotalNodes())*128/(1<<20))
	t.Logf("  shards=%d  (4 inline slots per node, 8-way branching, 16-bit H2 groups)",
		probe.NumShards())

	// ─────────────────────────────────────────────────────────────────────────
	// Design comparison table
	// ─────────────────────────────────────────────────────────────────────────

	t.Logf(`
── Design comparison: icache (InlineIndex) vs cache (LookupIndex) ──
Feature                        icache (InlineIndex)      cache (LookupIndex)
─────────────────────────────────────────────────────────────────────────────
Index implementation           inline-slot trie          separate-pool trie
Slots location                 inline in each node       separate slot pool
Node size                      128 B (2 cache lines)     64 B (1 cache line)
Slot indirection on read       0 (slots in same node)    1 (pool lookup)
Hot-path cache lines           1 (CL1 = 4 slots)         2 (node CL + slot CL)
H2 filter word                 uint64 (4×16-bit groups)  N/A (separate-pool)
H2 discriminator bits          16 bits per slot          8 bits per slot
hashIdx tag (bits 48-63)       real fingerprint bytes    routing hash bits
Node pool growth               stable *iNode pointers    COW slice + grace period
Write memory cost              O(1)                      O(n) COW node copies
Compaction                     in-place (no COW)         COW + deferred free
Background goroutines          0 (compaction on delete)  2 (cleaner + sweeper)
`)

	_ = gcRes   // used above
	_ = mm2Res  // used above
	_ = sm2Res  // used above
}
