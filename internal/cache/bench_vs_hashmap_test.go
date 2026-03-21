package cache

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── HashMap baselines ─────────────────────────────────────────────────────────

// mutexMap is a map[string][]byte protected by a RWMutex — the classic baseline.
type mutexMap struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func newMutexMap() *mutexMap { return &mutexMap{m: make(map[string][]byte)} }

func (mm *mutexMap) Set(key string, val []byte) {
	mm.mu.Lock()
	mm.m[key] = val
	mm.mu.Unlock()
}

func (mm *mutexMap) Get(key string) ([]byte, bool) {
	mm.mu.RLock()
	v, ok := mm.m[key]
	mm.mu.RUnlock()
	return v, ok
}

// syncMap wraps sync.Map for comparable API.
type syncMap struct{ m sync.Map }

func (sm *syncMap) Set(key string, val []byte) { sm.m.Store(key, val) }
func (sm *syncMap) Get(key string) ([]byte, bool) {
	v, ok := sm.m.Load(key)
	if !ok {
		return nil, false
	}
	return v.([]byte), true
}

// ── helpers ───────────────────────────────────────────────────────────────────

// makeCacheManager returns a CacheManager sized for benchmarks.
// NoopBackend: no disk I/O, measures pure in-memory performance.
func makeBenchCM(b *testing.B) *CacheManager {
	b.Helper()
	// 512 MB total, 4 size classes, 2 TTL tiers → 8 regions × 64 MB each.
	cm, err := NewCacheManager(
		512<<20,
		[]uint32{64, 256, 1024, 4096},
		[]uint32{60, 3600},
		1_000_000,
		0,
		NoopBackend,
	)
	if err != nil {
		b.Fatalf("NewCacheManager: %v", err)
	}
	b.Cleanup(cm.Stop)
	return cm
}

// Pre-built key tables: allocated once at test init, reused across all calls.
// Eliminates the fmt.Sprintf + []byte(string) allocation that was polluting
// GC metrics and making the benchmark unfairly expensive for CacheManager.
// In production, keys arrive as []byte from the HTTP request — never allocated
// inside the hot path.
var (
	preShortKeys [numKeysPerTenant][]byte // 6 B each → tinyIdx lane
	preLongKeys  [numKeysPerTenant][]byte // 12 B each → hashIdx lane
)

func init() {
	for i := range preShortKeys {
		preShortKeys[i] = []byte(fmt.Sprintf("k%04d", i))
	}
	for i := range preLongKeys {
		preLongKeys[i] = []byte(fmt.Sprintf("key:%08d", i))
	}
}

func shortKey(i int) []byte { return preShortKeys[i%numKeysPerTenant] }
func longKey(i int) []byte  { return preLongKeys[i%numKeysPerTenant] }
func value32() []byte       { return make([]byte, 32) }
func value256() []byte      { return make([]byte, 256) }

// allocDelta runs fn and returns heap bytes and object counts allocated during it.
func allocDelta(fn func()) (allocBytes, allocObjs uint64) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc, after.Mallocs - before.Mallocs
}

// ── Sequential write benchmarks ───────────────────────────────────────────────

func BenchmarkPut_ShortKey_CacheManager(b *testing.B) {
	cm := makeBenchCM(b)
	val := value32()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.Put(1, shortKey(i), val, 60)
	}
}

func BenchmarkPut_ShortKey_MutexMap(b *testing.B) {
	mm := newMutexMap()
	val := value32()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mm.Set(string(shortKey(i)), val)
	}
}

func BenchmarkPut_ShortKey_SyncMap(b *testing.B) {
	sm := &syncMap{}
	val := value32()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.Set(string(shortKey(i)), val)
	}
}

func BenchmarkPut_LongKey_CacheManager(b *testing.B) {
	cm := makeBenchCM(b)
	val := value32()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.Put(1, longKey(i), val, 60)
	}
}

func BenchmarkPut_LongKey_MutexMap(b *testing.B) {
	mm := newMutexMap()
	val := value32()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mm.Set(string(longKey(i)), val)
	}
}

func BenchmarkPut_LongKey_SyncMap(b *testing.B) {
	sm := &syncMap{}
	val := value32()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.Set(string(longKey(i)), val)
	}
}

// ── Sequential read benchmarks ────────────────────────────────────────────────

func BenchmarkGet_ShortKey_CacheManager(b *testing.B) {
	cm := makeBenchCM(b)
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		cm.Put(1, shortKey(i), val, 3600)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.Get(1, shortKey(i%n))
	}
}

func BenchmarkGet_ShortKey_MutexMap(b *testing.B) {
	mm := newMutexMap()
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		mm.Set(string(shortKey(i)), val)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mm.Get(string(shortKey(i % n)))
	}
}

func BenchmarkGet_ShortKey_SyncMap(b *testing.B) {
	sm := &syncMap{}
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		sm.Set(string(shortKey(i)), val)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.Get(string(shortKey(i % n)))
	}
}

func BenchmarkGet_LongKey_CacheManager(b *testing.B) {
	cm := makeBenchCM(b)
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		cm.Put(1, longKey(i), val, 3600)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cm.Get(1, longKey(i%n))
	}
}

func BenchmarkGet_LongKey_MutexMap(b *testing.B) {
	mm := newMutexMap()
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		mm.Set(string(longKey(i)), val)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mm.Get(string(longKey(i % n)))
	}
}

func BenchmarkGet_LongKey_SyncMap(b *testing.B) {
	sm := &syncMap{}
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		sm.Set(string(longKey(i)), val)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.Get(string(longKey(i % n)))
	}
}

// ── Large-N insertion: memory pressure comparison ─────────────────────────────
//
// Loads 500 000 entries into each structure and reports heap delta.
// Run with: go test -run TestLargeNInsert -v ./internal/cache/

func TestLargeNInsert(t *testing.T) {
	const n = 500_000
	val := value32()

	// CacheManager
	cm, _ := NewCacheManager(512<<20, []uint32{64, 256, 1024, 4096}, []uint32{60, 3600}, uint64(n), 0, NoopBackend)
	defer cm.Stop()
	cmBytes, cmObjs := allocDelta(func() {
		for i := 0; i < n; i++ {
			cm.Put(1, longKey(i), val, 3600)
		}
	})

	// MutexMap
	mm := newMutexMap()
	mmBytes, mmObjs := allocDelta(func() {
		for i := 0; i < n; i++ {
			mm.Set(string(longKey(i)), val)
		}
	})

	// sync.Map
	sm := &syncMap{}
	smBytes, smObjs := allocDelta(func() {
		for i := 0; i < n; i++ {
			sm.Set(string(longKey(i)), val)
		}
	})

	t.Logf("%-20s %12s %12s", "backend", "alloc bytes", "alloc objs")
	t.Logf("%-20s %12d %12d", "CacheManager", cmBytes, cmObjs)
	t.Logf("%-20s %12d %12d", "MutexMap", mmBytes, mmObjs)
	t.Logf("%-20s %12d %12d", "sync.Map", smBytes, smObjs)
}


// ── Concurrent mixed read/write ───────────────────────────────────────────────

func BenchmarkMixed_8Writers_CacheManager(b *testing.B) {
	cm := makeBenchCM(b)
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		cm.Put(1, longKey(i), val, 3600)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%8 == 0 {
				cm.Put(1, longKey(i%n), val, 3600)
			} else {
				cm.Get(1, longKey(i%n))
			}
			i++
		}
	})
}

func BenchmarkMixed_8Writers_MutexMap(b *testing.B) {
	mm := newMutexMap()
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		mm.Set(string(longKey(i)), val)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%8 == 0 {
				mm.Set(string(longKey(i%n)), val)
			} else {
				mm.Get(string(longKey(i % n)))
			}
			i++
		}
	})
}

func BenchmarkMixed_8Writers_SyncMap(b *testing.B) {
	sm := &syncMap{}
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		sm.Set(string(longKey(i)), val)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%8 == 0 {
				sm.Set(string(longKey(i%n)), val)
			} else {
				sm.Get(string(longKey(i % n)))
			}
			i++
		}
	})
}

// ── 100 Get : 1 Put (high read pressure) ─────────────────────────────────────
//
// Simulates a read-heavy cache workload: for every 100 reads, one goroutine
// writes. This is the realistic production ratio for response caches.

func BenchmarkReadHeavy100R1W_CacheManager(b *testing.B) {
	cm := makeBenchCM(b)
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		cm.Put(1, longKey(i), val, 3600)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%100 == 0 {
				cm.Put(1, longKey(i%n), val, 3600)
			} else {
				cm.Get(1, longKey(i%n))
			}
			i++
		}
	})
}

func BenchmarkReadHeavy100R1W_MutexMap(b *testing.B) {
	mm := newMutexMap()
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		mm.Set(string(longKey(i)), val)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%100 == 0 {
				mm.Set(string(longKey(i%n)), val)
			} else {
				mm.Get(string(longKey(i % n)))
			}
			i++
		}
	})
}

func BenchmarkReadHeavy100R1W_SyncMap(b *testing.B) {
	sm := &syncMap{}
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		sm.Set(string(longKey(i)), val)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%100 == 0 {
				sm.Set(string(longKey(i%n)), val)
			} else {
				sm.Get(string(longKey(i % n)))
			}
			i++
		}
	})
}

// ── 1000 Get : 1 Put : 1 Delete ──────────────────────────────────────────────
//
// Simulates a cache with TTL-driven evictions: the vast majority of operations
// are reads; writes are infrequent; deletes (e.g. invalidation) are rare.
// Pattern per goroutine: ops 0..998 = Get, op 999 = Put, op 1000 = Delete.

func BenchmarkWithDeletes1000R1W1D_CacheManager(b *testing.B) {
	cm := makeBenchCM(b)
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		cm.Put(1, longKey(i), val, 3600)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 1001 {
			case 999:
				cm.Put(1, longKey(i%n), val, 3600)
			case 1000:
				cm.deleteKey(1, longKey(i%n))
			default:
				cm.Get(1, longKey(i%n))
			}
			i++
		}
	})
}

func BenchmarkWithDeletes1000R1W1D_MutexMap(b *testing.B) {
	mm := newMutexMap()
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		mm.Set(string(longKey(i)), val)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 1001 {
			case 999:
				mm.Set(string(longKey(i%n)), val)
			case 1000:
				mm.mu.Lock()
				delete(mm.m, string(longKey(i%n)))
				mm.mu.Unlock()
			default:
				mm.Get(string(longKey(i % n)))
			}
			i++
		}
	})
}

func BenchmarkWithDeletes1000R1W1D_SyncMap(b *testing.B) {
	sm := &syncMap{}
	val := value32()
	const n = 10_000
	for i := 0; i < n; i++ {
		sm.Set(string(longKey(i)), val)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 1001 {
			case 999:
				sm.Set(string(longKey(i%n)), val)
			case 1000:
				sm.m.Delete(string(longKey(i % n)))
			default:
				sm.Get(string(longKey(i % n)))
			}
			i++
		}
	})
}

// ── High-TPS multi-tenant throughput test ─────────────────────────────────────
//
// A realistic multi-tenant workload:
//   - 5 tenants, each with numKeys/5 unique keys
//   - HashMap baselines must concatenate "tenantID:key" into a string on every op
//   - CacheManager handles tenancy natively via uint16 tenantID — no string alloc
//
// Reports: TPS, total keys loaded, and live heap after population.
//
// Run with: go test -run TestThroughput -v ./internal/cache/

const (
	throughputDuration = 3 * time.Second
	gcStressDuration   = 30 * time.Second // long enough for GC to kick in multiple times
	numTenants         = 5
	numKeysPerTenant   = 10_000 // 50 000 total keys across all tenants
	writeEvery         = 100    // 1 Put per 100 ops
)

// tenantKey builds the scoped string key used by HashMap baselines.
// Simulates what any multi-tenant HashMap must do: encode identity in the key.
func tenantKey(tenantID uint16, i int) string {
	return fmt.Sprintf("t%d:key:%08d", tenantID, i)
}

type heapSnap struct {
	inUseMB  float64 // memory actually holding live objects
	allocMB  float64 // total allocated (live + not yet GC'd)
	sysMB    float64 // memory obtained from OS
	objects  uint64  // live heap objects
}

func snapHeap() heapSnap {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return heapSnap{
		inUseMB: float64(ms.HeapInuse) / (1024 * 1024),
		allocMB: float64(ms.HeapAlloc) / (1024 * 1024),
		sysMB:   float64(ms.HeapSys) / (1024 * 1024),
		objects: ms.HeapObjects,
	}
}

func heapInUseMB() float64 { return snapHeap().inUseMB }

func TestThroughput(t *testing.T) {
	goroutines := runtime.GOMAXPROCS(0)
	val := value32()

	// ── memory: measure each backend independently ────────────────────────────
	measureMem := func(name string, populate func()) {
		before := snapHeap()
		populate()
		after := snapHeap()
		t.Logf("%-20s  %6d keys  inuse=%6.1f MB  alloc=%6.1f MB  sys=%6.1f MB  objects=%d",
			name,
			numTenants*numKeysPerTenant,
			after.inUseMB-before.inUseMB,
			after.allocMB-before.allocMB,
			after.sysMB-before.sysMB,
			after.objects-before.objects,
		)
	}

	t.Log("\n=== Memory after loading 50 000 keys (5 tenants × 10 000) ===")

	var cm *CacheManager
	measureMem("CacheManager", func() {
		cm, _ = NewCacheManager(512<<20, []uint32{64, 256, 1024, 4096}, []uint32{60, 3600},
			uint64(numTenants*numKeysPerTenant), 0, NoopBackend)
		for tid := uint16(1); tid <= numTenants; tid++ {
			for i := 0; i < numKeysPerTenant; i++ {
				cm.Put(tid, longKey(i), val, 3600)
			}
		}
	})
	defer cm.Stop()

	var mm *mutexMap
	measureMem("MutexMap (t:key str)", func() {
		mm = newMutexMap()
		for tid := uint16(1); tid <= numTenants; tid++ {
			for i := 0; i < numKeysPerTenant; i++ {
				mm.Set(tenantKey(tid, i), val)
			}
		}
	})

	sm := &syncMap{}
	measureMem("sync.Map (t:key str)", func() {
		for tid := uint16(1); tid <= numTenants; tid++ {
			for i := 0; i < numKeysPerTenant; i++ {
				sm.Set(tenantKey(tid, i), val)
			}
		}
	})

	// ── throughput runner ─────────────────────────────────────────────────────
	run := func(name string, getFn func(tid uint16, i int), setFn func(tid uint16, i int)) {
		var total atomic.Uint64
		var wg sync.WaitGroup
		stop := make(chan struct{})

		for g := 0; g < goroutines; g++ {
			g := g
			wg.Add(1)
			go func() {
				defer wg.Done()
				i := g * 997 // prime offset — spreads goroutines across key space
				var local uint64
				for {
					select {
					case <-stop:
						total.Add(local)
						return
					default:
					}
					tid := uint16(i%numTenants) + 1
					key := i % numKeysPerTenant
					if i%writeEvery == 0 {
						setFn(tid, key)
					} else {
						getFn(tid, key)
					}
					local++
					i++
				}
			}()
		}

		time.Sleep(throughputDuration)
		close(stop)
		wg.Wait()

		ops := total.Load()
		tps := float64(ops) / throughputDuration.Seconds()
		t.Logf("%-20s  goroutines=%2d  ops=%10d  TPS=%10.0f  (~%.1f M/s)",
			name, goroutines, ops, tps, tps/1_000_000)
	}

	t.Logf("\n=== 100 Get : 1 Put — %d tenants, %d keys each (%.0fs) ===",
		numTenants, numKeysPerTenant, throughputDuration.Seconds())
	run("CacheManager",
		func(tid uint16, i int) { cm.Get(tid, longKey(i)) },
		func(tid uint16, i int) { cm.Put(tid, longKey(i), val, 3600) },
	)
	run("MutexMap",
		func(tid uint16, i int) { mm.Get(tenantKey(tid, i)) },
		func(tid uint16, i int) { mm.Set(tenantKey(tid, i), val) },
	)
	run("sync.Map",
		func(tid uint16, i int) { sm.Get(tenantKey(tid, i)) },
		func(tid uint16, i int) { sm.Set(tenantKey(tid, i), val) },
	)
}

// TestThroughputGCStress runs the same workload for 30 seconds so that Go's GC
// fires multiple times. The HashMap baselines allocate a new string key on every
// op (tenantKey builds "t1:key:00001234"), creating sustained GC pressure.
// CacheManager takes uint16 + []byte — the key []byte from longKey() is the
// only allocation and is never retained, so it is short-lived and cheap to collect.
//
// Reports: TPS sampled every 5 seconds to show GC-induced throughput dips.
//
// Run with: go test -run TestThroughputGCStress -v ./internal/cache/

func TestThroughputGCStress(t *testing.T) {
	goroutines := runtime.GOMAXPROCS(0)
	val := value32()

	// Pre-populate.
	cm, _ := NewCacheManager(512<<20, []uint32{64, 256, 1024, 4096}, []uint32{60, 3600},
		uint64(numTenants*numKeysPerTenant), 0, NoopBackend)
	defer cm.Stop()
	mm := newMutexMap()
	sm := &syncMap{}
	for tid := uint16(1); tid <= numTenants; tid++ {
		for i := 0; i < numKeysPerTenant; i++ {
			k := longKey(i)
			cm.Put(tid, k, val, 3600)
			mm.Set(tenantKey(tid, i), val)
			sm.Set(tenantKey(tid, i), val)
		}
	}

	runGCStress := func(name string, getFn func(tid uint16, i int), setFn func(tid uint16, i int)) {
		var wg sync.WaitGroup
		stop := make(chan struct{})

		// Per-goroutine op counter, sampled every 5s.
		counters := make([]atomic.Uint64, goroutines)

		for g := 0; g < goroutines; g++ {
			g := g
			wg.Add(1)
			go func() {
				defer wg.Done()
				i := g * 997
				for {
					select {
					case <-stop:
						return
					default:
					}
					tid := uint16(i%numTenants) + 1
					key := i % numKeysPerTenant
					if i%writeEvery == 0 {
						setFn(tid, key)
					} else {
						getFn(tid, key)
					}
					counters[g].Add(1)
					i++
				}
			}()
		}

		// Sample every 5 seconds.
		var gcStats [6]struct{ tps float64; gcCycles uint32; gcPause uint64 }
		interval := 5 * time.Second
		for s := 0; s < 6; s++ {
			prev := make([]uint64, goroutines)
			for g := range counters {
				prev[g] = counters[g].Load()
			}
			prevGC := gcSnapshot()
			time.Sleep(interval)

			var ops uint64
			for g := range counters {
				ops += counters[g].Load() - prev[g]
			}
			curGC := gcSnapshot()
			gcStats[s] = struct{ tps float64; gcCycles uint32; gcPause uint64 }{
				tps:      float64(ops) / interval.Seconds(),
				gcCycles: curGC.cycles - prevGC.cycles,
				gcPause:  curGC.pauseNs - prevGC.pauseNs,
			}
		}

		close(stop)
		wg.Wait()

		t.Logf("\n--- %s (30s GC stress) ---", name)
		t.Logf("  %6s  %12s  %10s  %12s", "window", "TPS", "GC cycles", "GC pause (ms)")
		var totalTPS float64
		for s, gs := range gcStats {
			t.Logf("  %3d–%3ds  %12.0f  %10d  %12.1f",
				s*5, (s+1)*5, gs.tps, gs.gcCycles, float64(gs.gcPause)/1e6)
			totalTPS += gs.tps
		}
		t.Logf("  %-8s  %12.0f  (average)", "avg", totalTPS/6)
	}

	runGCStress("CacheManager",
		func(tid uint16, i int) { cm.Get(tid, longKey(i)) },
		func(tid uint16, i int) { cm.Put(tid, longKey(i), val, 3600) },
	)
	runGCStress("MutexMap",
		func(tid uint16, i int) { mm.Get(tenantKey(tid, i)) },
		func(tid uint16, i int) { mm.Set(tenantKey(tid, i), val) },
	)
	runGCStress("sync.Map",
		func(tid uint16, i int) { sm.Get(tenantKey(tid, i)) },
		func(tid uint16, i int) { sm.Set(tenantKey(tid, i), val) },
	)
}

type gcSnap struct {
	cycles  uint32
	pauseNs uint64
}

func gcSnapshot() gcSnap {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return gcSnap{cycles: ms.NumGC, pauseNs: ms.PauseTotalNs}
}

// ── Value size sweep ──────────────────────────────────────────────────────────

func BenchmarkPutValueSize_CacheManager(b *testing.B) {
	for _, size := range []int{8, 64, 256, 1024, 4096} {
		size := size
		b.Run(fmt.Sprintf("val=%dB", size), func(b *testing.B) {
			cm := makeBenchCM(b)
			val := make([]byte, size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cm.Put(1, longKey(i), val, 60)
			}
		})
	}
}

func BenchmarkPutValueSize_MutexMap(b *testing.B) {
	for _, size := range []int{8, 64, 256, 1024, 4096} {
		size := size
		b.Run(fmt.Sprintf("val=%dB", size), func(b *testing.B) {
			mm := newMutexMap()
			val := make([]byte, size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				mm.Set(string(longKey(i)), val)
			}
		})
	}
}
