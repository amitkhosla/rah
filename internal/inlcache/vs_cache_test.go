package inlcache

// TestVsModels benchmarks the inline-slot trie (InlineIndex) against
// the cache package's separate-pool trie (LookupIndex) and popular
// Go cache libraries under identical workload conditions.
//
// Two comparison sections:
//
//  1. Pure index lookup (no value bytes stored):
//     InlineIndex  — tag (uint64) → uint64,   inline slots per node
//     LookupIndex  — fp ([16]byte) → uint64,  slots in separate pool
//     sync.Map     — uint64 → uint64,          baseline
//     map+RWMutex  — uint64 → uint64,          baseline
//
//  2. Full cache (key → []byte, TTL, eviction):
//     cache_v1.CacheManager — slab-alloc, zero-GC, per-entry TTL
//     bigcache v3        — ring-buffer, global TTL
//     ristretto v2       — TinyLFU, async Set
//     go-cache           — heap map, per-entry TTL
//
// Settings:
//
//	10 tenants × 10 000 keys = 100 000 entries  |  256 B values  |  10 s TTL
//	99% reads, 1% writes  |  GOMAXPROCS goroutines  |  30 s run / 5 s windows
//
// Run with:
//
//	go test -v -run TestVsModels -timeout 600s ./internal/inlcache/
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

	cache_v1 "rah/internal/cache/archive/lookup_v1"
)

// ── workload constants ────────────────────────────────────────────────────────

const (
	vcNumTenants    = 10
	vcKeysPerTenant = 10_000
	vcTotalKeys     = vcNumTenants * vcKeysPerTenant
	vcValueSize     = 256
	vcTTL           = uint32(10)
	vcRunDuration   = 30 * time.Second
	vcSampleEvery   = 5 * time.Second
	vcWriteEvery    = 100 // 1 write per 100 ops → ~1% writes
	vcHardMaxMB     = 32
)

// ── pre-computed key tables ───────────────────────────────────────────────────

var (
	// InlineIndex keys: 64-bit tag computed from (tenant, i).
	vcTags [vcNumTenants][vcKeysPerTenant]uint64

	// LookupIndex keys: [16]byte fingerprint built from tenantID + key index.
	vcFPs [vcNumTenants][vcKeysPerTenant][16]byte

	// CacheManager keys: raw []byte, tenantID passed separately.
	// 3-byte binary: same spread logic as htCMShortKeys in bench_eviction_test.
	vcCMKeys [vcNumTenants][vcKeysPerTenant][]byte

	// String-keyed cache keys: "t{tid}:k{i}" embedded tenant prefix.
	vcStrKeys [vcNumTenants][vcKeysPerTenant]string
)

func init() {
	var buf [32]byte
	for tid := 0; tid < vcNumTenants; tid++ {
		for i := 0; i < vcKeysPerTenant; i++ {
			// InlineIndex tag: same hash as index_test.go makeTag.
			h := uint64(tid+1)*0x9e3779b97f4a7c15 ^ uint64(i)*0x517cc1b727220a95
			vcTags[tid][i] = h | (1 << 63)

			// LookupIndex fingerprint: tenantID in bytes 0-1, index in bytes 2-9.
			binary.LittleEndian.PutUint16(vcFPs[tid][i][0:], uint16(tid+1))
			binary.LittleEndian.PutUint64(vcFPs[tid][i][2:], uint64(i))

			// CacheManager key: 3 binary bytes spread across shards.
			vcCMKeys[tid][i] = []byte{byte(i & 0xFF), byte(i >> 8), byte(tid + 1)}

			// String key for map-backed caches.
			b := buf[:0]
			b = append(b, 't')
			b = strconv.AppendInt(b, int64(tid+1), 10)
			b = append(b, ':')
			b = append(b, 'k')
			b = strconv.AppendInt(b, int64(i), 10)
			vcStrKeys[tid][i] = string(b)
		}
	}
}

// ── result type ───────────────────────────────────────────────────────────────

type vcResult struct {
	name         string
	ownMB        float64 // HeapInuse delta: this backend only
	totalMB      float64 // process-wide HeapInuse after populate
	heapObjs     uint64
	avgTPS       float64
	gcPerWin     []uint32
	pausePerWin  []float64 // ms
}

// ── memory helpers ────────────────────────────────────────────────────────────

type vcMemSnap struct{ inuse float64; objects uint64 }

func vcMemTake() vcMemSnap {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return vcMemSnap{float64(ms.HeapInuse) / (1 << 20), ms.HeapObjects}
}

type vcGCMark struct{ cycles uint32; pauseNs uint64 }

func vcGCTake() vcGCMark {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return vcGCMark{ms.NumGC, ms.PauseTotalNs}
}

// ── hot-loop runner ───────────────────────────────────────────────────────────

// runVC runs one backend under the standard hot loop.
// getFn and putFn receive (tenantID 1-based, keyIndex 0-based).
func runVC(
	name string,
	populate func(),
	getFn func(tid int, i int),
	putFn func(tid int, i int),
) vcResult {
	base := vcMemTake()
	populate()
	after := vcMemTake()

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
				tid := i%vcNumTenants + 1 // 1-based
				key := i % vcKeysPerTenant
				if i%vcWriteEvery == 0 {
					putFn(tid, key)
				} else {
					getFn(tid, key)
				}
				counters[g].Add(1)
				i++
			}
		}()
	}

	windows := int(vcRunDuration / vcSampleEvery)
	gcPerWin := make([]uint32, windows)
	pausePerWin := make([]float64, windows)
	tpsPerWin := make([]float64, windows)

	for w := 0; w < windows; w++ {
		prev := make([]uint64, goroutines)
		for g := 0; g < goroutines; g++ {
			prev[g] = counters[g].Load()
		}
		prevGC := vcGCTake()
		time.Sleep(vcSampleEvery)
		var ops uint64
		for g := 0; g < goroutines; g++ {
			ops += counters[g].Load() - prev[g]
		}
		cur := vcGCTake()
		gcPerWin[w] = cur.cycles - prevGC.cycles
		pausePerWin[w] = float64(cur.pauseNs-prevGC.pauseNs) / 1e6
		tpsPerWin[w] = float64(ops) / vcSampleEvery.Seconds()
	}

	close(stop)
	wg.Wait()

	var totalTPS float64
	for _, t := range tpsPerWin {
		totalTPS += t
	}
	return vcResult{
		name:        name,
		ownMB:       after.inuse - base.inuse,
		totalMB:     after.inuse,
		heapObjs:    after.objects - base.objects,
		avgTPS:      totalTPS / float64(windows),
		gcPerWin:    gcPerWin,
		pausePerWin: pausePerWin,
	}
}

// ── TTL-aware map types (mirrors bench_eviction_test.go) ─────────────────────

type vcEntry struct {
	val       uint64
	expiresAt int64
}

type vcMutexMap struct {
	mu sync.RWMutex
	m  map[uint64]vcEntry
}

func newVCMutexMap(cap int) *vcMutexMap {
	return &vcMutexMap{m: make(map[uint64]vcEntry, cap)}
}

func (m *vcMutexMap) Set(tag uint64, val uint64) {
	m.mu.Lock()
	m.m[tag] = vcEntry{val, time.Now().Unix() + int64(vcTTL)}
	m.mu.Unlock()
}

func (m *vcMutexMap) Get(tag uint64) (uint64, bool) {
	m.mu.RLock()
	e, ok := m.m[tag]
	m.mu.RUnlock()
	if !ok || time.Now().Unix() > e.expiresAt {
		return 0, false
	}
	return e.val, true
}

type vcSyncMap struct{ m sync.Map }

func (s *vcSyncMap) Set(tag uint64, val uint64) {
	s.m.Store(tag, vcEntry{val, time.Now().Unix() + int64(vcTTL)})
}

func (s *vcSyncMap) Get(tag uint64) (uint64, bool) {
	v, ok := s.m.Load(tag)
	if !ok {
		return 0, false
	}
	e := v.(vcEntry)
	if time.Now().Unix() > e.expiresAt {
		return 0, false
	}
	return e.val, true
}

// ── main comparison test ──────────────────────────────────────────────────────

func TestVsModels(t *testing.T) {
	val256 := make([]byte, vcValueSize)

	// ─────────────────────────────────────────────────────────────────────────
	// SECTION 1 — Pure index (tag → uint64, no value bytes stored)
	// ─────────────────────────────────────────────────────────────────────────

	// 1a. InlineIndex — inline slots per node (this package)
	var inl *InlineIndex
	inlRes := runVC("InlineIndex (inline slots)",
		func() {
			inl = NewInlineIndex(0)
			for tid := 0; tid < vcNumTenants; tid++ {
				for i := 0; i < vcKeysPerTenant; i++ {
					inl.Set(vcTags[tid][i], uint64(i+1))
				}
			}
		},
		func(tid, i int) { inl.Get(vcTags[tid-1][i]) },
		func(tid, i int) { inl.Set(vcTags[tid-1][i], uint64(i+1)) },
	)

	// 1b. LookupIndex — separate slot pool (cache package, original model)
	var li *cache_v1.LookupIndex
	liRes := runVC("LookupIndex (separate pool)",
		func() {
			li = cache_v1.NewLookupIndex(0)
			for tid := 0; tid < vcNumTenants; tid++ {
				for i := 0; i < vcKeysPerTenant; i++ {
					li.Set(vcFPs[tid][i], uint64(i+1))
				}
			}
		},
		func(tid, i int) { li.Get(vcFPs[tid-1][i]) },
		func(tid, i int) { li.Set(vcFPs[tid-1][i], uint64(i+1)) },
	)

	// 1c. map+RWMutex (uint64 → uint64, no indirection, baseline)
	var mm *vcMutexMap
	mmRes := runVC("map+RWMutex (uint64 key)",
		func() {
			mm = newVCMutexMap(vcTotalKeys)
			for tid := 0; tid < vcNumTenants; tid++ {
				for i := 0; i < vcKeysPerTenant; i++ {
					mm.Set(vcTags[tid][i], uint64(i+1))
				}
			}
		},
		func(tid, i int) { mm.Get(vcTags[tid-1][i]) },
		func(tid, i int) { mm.Set(vcTags[tid-1][i], uint64(i+1)) },
	)

	// 1d. sync.Map (uint64 → uint64, lock-free reads, baseline)
	var sm vcSyncMap
	smRes := runVC("sync.Map (uint64 key)",
		func() {
			for tid := 0; tid < vcNumTenants; tid++ {
				for i := 0; i < vcKeysPerTenant; i++ {
					sm.Set(vcTags[tid][i], uint64(i+1))
				}
			}
		},
		func(tid, i int) { sm.Get(vcTags[tid-1][i]) },
		func(tid, i int) { sm.Set(vcTags[tid-1][i], uint64(i+1)) },
	)

	// ─────────────────────────────────────────────────────────────────────────
	// SECTION 2 — Full cache (key → []byte, TTL, eviction)
	// ─────────────────────────────────────────────────────────────────────────

	// 2a. CacheManager — slab-alloc, zero-GC slab reads, per-entry TTL
	var cm *cache_v1.CacheManager
	cmRes := runVC("CacheManager (slab+trie)",
		func() {
			cm, _ = cache_v1.NewCacheManager(
				vcHardMaxMB<<20, []uint32{256}, []uint32{10},
				vcTotalKeys, 0, cache_v1.NoopBackend,
			)
			for tid := 0; tid < vcNumTenants; tid++ {
				for i := 0; i < vcKeysPerTenant; i++ {
					cm.Put(uint16(tid+1), vcCMKeys[tid][i], val256, vcTTL)
				}
			}
		},
		func(tid, i int) { cm.Get(uint16(tid), vcCMKeys[tid-1][i]) },
		func(tid, i int) { cm.Put(uint16(tid), vcCMKeys[tid-1][i], val256, vcTTL) },
	)
	cm.Stop()

	// 2b. bigcache v3 — ring-buffer, global TTL, zero-GC
	var bc *bigcachev3.BigCache
	bcRes := runVC("bigcache v3",
		func() {
			bc, _ = bigcachev3.New(context.Background(), bigcachev3.Config{
				Shards:             256,
				LifeWindow:         10 * time.Second,
				CleanWindow:        1 * time.Second,
				MaxEntriesInWindow: vcTotalKeys,
				MaxEntrySize:       300,
				HardMaxCacheSize:   vcHardMaxMB,
				Verbose:            false,
			})
			for tid := 0; tid < vcNumTenants; tid++ {
				for i := 0; i < vcKeysPerTenant; i++ {
					_ = bc.Set(vcStrKeys[tid][i], val256)
				}
			}
		},
		func(tid, i int) { _, _ = bc.Get(vcStrKeys[tid-1][i]) },
		func(tid, i int) { _ = bc.Set(vcStrKeys[tid-1][i], val256) },
	)
	_ = bc.Close()

	// 2c. ristretto v2 — TinyLFU admission, async Set
	var rc *ristretto.Cache[string, []byte]
	rcRes := runVC("ristretto v2",
		func() {
			rc, _ = ristretto.NewCache(&ristretto.Config[string, []byte]{
				NumCounters: int64(vcTotalKeys * 10),
				MaxCost:     vcHardMaxMB << 20,
				BufferItems: 64,
				Cost:        func(v []byte) int64 { return int64(len(v)) },
			})
			for tid := 0; tid < vcNumTenants; tid++ {
				for i := 0; i < vcKeysPerTenant; i++ {
					v := make([]byte, vcValueSize)
					copy(v, val256)
					rc.SetWithTTL(vcStrKeys[tid][i], v, int64(vcValueSize), 10*time.Second)
				}
			}
			rc.Wait()
		},
		func(tid, i int) { _, _ = rc.Get(vcStrKeys[tid-1][i]) },
		func(tid, i int) {
			v := make([]byte, vcValueSize)
			copy(v, val256)
			rc.SetWithTTL(vcStrKeys[tid-1][i], v, int64(vcValueSize), 10*time.Second)
		},
	)
	rc.Close()

	// 2d. go-cache — heap map, per-entry TTL
	var gc *gocache.Cache
	gcRes := runVC("go-cache",
		func() {
			gc = gocache.New(10*time.Second, 1*time.Second)
			for tid := 0; tid < vcNumTenants; tid++ {
				for i := 0; i < vcKeysPerTenant; i++ {
					v := make([]byte, vcValueSize)
					copy(v, val256)
					gc.SetDefault(vcStrKeys[tid][i], v)
				}
			}
		},
		func(tid, i int) { gc.Get(vcStrKeys[tid-1][i]) },
		func(tid, i int) {
			v := make([]byte, vcValueSize)
			copy(v, val256)
			gc.Set(vcStrKeys[tid-1][i], v, 10*time.Second)
		},
	)

	// ─────────────────────────────────────────────────────────────────────────
	// Print results
	// ─────────────────────────────────────────────────────────────────────────

	t.Logf("\n=== Model Comparison: %d tenants × %dk keys = %dk entries | %dB values | %ds TTL | %d goroutines ===\n",
		vcNumTenants, vcKeysPerTenant/1000, vcTotalKeys/1000, vcValueSize, vcTTL,
		runtime.GOMAXPROCS(0))

	section1 := []vcResult{inlRes, liRes, mmRes, smRes}
	section2 := []vcResult{cmRes, bcRes, rcRes, gcRes}

	printSection := func(title string, results []vcResult) {
		t.Logf("── %s ──", title)
		t.Logf("%-32s  %8s  %9s  %9s  %8s  %12s",
			"implementation", "own MB", "total MB", "heap objs", "GC/win", "avg TPS")
		t.Logf("%-32s  %8s  %9s  %9s  %8s  %12s",
			"--------------------------------", "--------", "---------",
			"---------", "--------", "------------")
		for _, r := range results {
			avgGC := 0.0
			for _, g := range r.gcPerWin {
				avgGC += float64(g)
			}
			if len(r.gcPerWin) > 0 {
				avgGC /= float64(len(r.gcPerWin))
			}
			t.Logf("%-32s  %8.1f  %9.1f  %9d  %8.2f  %12.0f  (~%.1fM/s)",
				r.name, r.ownMB, r.totalMB, r.heapObjs,
				avgGC, r.avgTPS, r.avgTPS/1e6)
		}
		t.Log("")
	}

	printSection("Section 1 — Pure index  (tag/fp → uint64, no value bytes)", section1)
	printSection("Section 2 — Full cache  (key → []byte, TTL, eviction)", section2)

	// Per-window breakdown.
	t.Logf("── Per-5s window breakdown ──")
	for _, r := range append(section1, section2...) {
		line := fmt.Sprintf("%-32s", r.name)
		for w := range r.gcPerWin {
			line += fmt.Sprintf("  [%2d–%2ds gc=%d p=%.0fms tps=%.0fM]",
				w*5, (w+1)*5, r.gcPerWin[w], r.pausePerWin[w], r.avgTPS/1e6)
		}
		t.Log(line)
	}

	// Node/slot stats for the two trie implementations.
	t.Logf("\n── Trie structure after populate ──")
	t.Logf("InlineIndex : %d nodes × 128 B/node = %.1f MB node memory",
		inl.TotalNodes(), float64(inl.TotalNodes())*128/(1<<20))
	t.Logf("  shards=%d  (4 inline slots per node, 8-way branching)",
		inl.NumShards())

	// Feature comparison.
	t.Logf(`
── Design comparison: inline-slot trie vs separate-pool trie ──
Feature                    InlineIndex          LookupIndex
──────────────────────────────────────────────────────────────
Slots location             inline in node       separate pool
Node size                  128 B (2 cache lines) 64 B (1 cache line)
Slot indirection on read   0 (in same node)     1 (pool lookup)
Hot-path cache lines       1 (slots on CL1)     2 (node CL + slot CL)
Node pool growth           stable *iNode ptrs   COW slice + grace period
Inline slots per node      4                    0 (slots are external)
Memory per 100K entries    see "own MB" above   see "own MB" above
`)

	_ = gcRes // used above
}
