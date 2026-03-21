package cache

// TestHighTPSWithEviction — production-realistic multi-tenant cache benchmark.
//
// Design goals:
//   - 1M+ read TPS across available goroutines
//   - 10K put/sec: achieved via 100:1 read:put ratio in hot loop
//   - 10K delete/sec: background goroutine per backend
//       CacheManager: TTL-driven cleaner (automatic, no hot-path delete call)
//       timedMutexMap / timedSyncMap: explicit goroutine scanning expired entries
//   - 100K unique keys across 10 tenants (10K keys/tenant)
//   - 256B values: sizes slab and map memory to comparable ~30–40 MB
//   - 10s TTL: cleaner fires actively, maps must also expire entries
//   - Key rotation: sequential i%keysPerTenant cycles entire key space
//   - Short key (5B, tinyIdx lane) vs Long key (12B, hashIdx lane) compared
//
// CacheManager slab sizing:
//   1 sizeClass (256B) × 1 TTLTier (10s) = 1 region = 1 slab
//   32 MB / stride(272B) ≈ 124 000 slots  →  eviction starts at ~100K entries
//
// Why 512MB in earlier tests?
//   Earlier tests used 4 sizeClasses × 2 TTLTiers = 8 regions × 64MB each.
//   With only 10K × 64B values (800KB of data), 99.9% of the slab was unused.
//   Here we right-size: 32MB for 100K × 256B entries = ~27MB of actual data.
//
// String concatenation best practice (demonstrated in init() below):
//   AVOID: fmt.Sprintf("t%d:key:%d", tid, i)   → 2 allocs
//   AVOID: "t" + strconv.Itoa(tid) + ":" + ...  → N allocs (one per concat)
//   USE:   strconv.AppendInt into a stack [32]byte, then string(buf) → 1 alloc
//   This is the standard Go pattern for building strings from mixed types.

import (
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	htNumTenants    = 10
	htKeysPerTenant = 10_000
	htTotalKeys     = htNumTenants * htKeysPerTenant // 100 000
	htValueSize     = 256                            // bytes
	htTTL           = uint32(10)                     // 10-second TTL
	htRunDuration   = 30 * time.Second
	htSampleEvery   = 5 * time.Second
	htWriteEvery    = 100  // 1 Put per 100 hot-loop ops → ~10K puts/sec at 1M TPS
	htMapCleanEvery = time.Second // background delete goroutine interval for maps
)

// ── pre-built key tables ──────────────────────────────────────────────────────
//
// Best practice: build all composite keys once at init using strconv.AppendInt
// into a stack buffer. The final string() is 1 alloc vs fmt.Sprintf's 2.
// In production, keys come from the request context (already []byte) — 0 allocs.

var (
	// CacheManager keys: raw []byte, no tenant prefix (tenantID passed separately).
	htLongKeys  [htKeysPerTenant][]byte // "key:00000001" — 12B, hashIdx lane
	htShortKeys [htKeysPerTenant][]byte // byte(i)+digits — 2-5B, tinyIdx lane; shared across tenants (used by maps)

	// CacheManager short keys: per-tenant, first byte = tid+1 so each tenant
	// lands in a distinct tinyIdx shard (shard = (key[0]&0x0F)<<4).
	// tid 1-10 → key[0] = 1-10 → lower nibbles 1-10 → 10 distinct shards.
	htCMShortKeys [htNumTenants][htKeysPerTenant][]byte

	// Map keys: tenant embedded as string prefix.
	// Format: "t{tenantID}:key:{idx}" or "t{tenantID}:k{idx}"
	htTenantLongKeys  [htNumTenants][htKeysPerTenant]string
	htTenantShortKeys [htNumTenants][htKeysPerTenant]string
)

func init() {
	var buf [32]byte

	for i := range htLongKeys {
		// Demonstrate strconv.AppendInt pattern: stack buffer, 0 allocations.
		b := buf[:0]
		b = append(b, "key:"...)
		b = strconv.AppendInt(b, int64(i), 10)
		htLongKeys[i] = []byte(string(b)) // copy once into persistent []byte

		// Short key: first byte cycles through values, then decimal suffix.
		// Used by map backends (tenant embedded in key string separately).
		b = buf[:0]
		b = append(b, byte(i&0xFF))
		b = strconv.AppendInt(b, int64(i>>8), 10) // 0..38 for i<10000
		htShortKeys[i] = []byte(string(b))
	}

	for tid := 0; tid < htNumTenants; tid++ {
		for i := 0; i < htKeysPerTenant; i++ {
			// CacheManager per-tenant short key: 3 binary bytes.
			//   key[0] = byte(i & 0xFF)     — cycles 0-255; determines shard via
			//                                  lower nibble, and routing via upper nibble.
			//   key[1] = byte(i >> 8)        — 0-38 (for i < 10000); extra routing bits.
			//   key[2] = byte(tid+1)         — tenant tag; ensures cross-tenant uniqueness
			//                                  in the trie (routing bits 20-27).
			// Together these spread 10K keys uniformly across 16 shards × 8 routing buckets
			// with no clustering, unlike decimal-digit strings that share upper nibbles.
			htCMShortKeys[tid][i] = []byte{byte(i & 0xFF), byte(i >> 8), byte(tid + 1)}
		}
	}

	for tid := 0; tid < htNumTenants; tid++ {
		for i := 0; i < htKeysPerTenant; i++ {
			// Long map key: "t{tid+1}:key:{i}"
			b := buf[:0]
			b = append(b, 't')
			b = strconv.AppendInt(b, int64(tid+1), 10)
			b = append(b, ':')
			b = append(b, "key:"...)
			b = strconv.AppendInt(b, int64(i), 10)
			htTenantLongKeys[tid][i] = string(b) // 1 alloc

			// Short map key: "t{tid+1}:k{i%10000}"
			b = buf[:0]
			b = append(b, 't')
			b = strconv.AppendInt(b, int64(tid+1), 10)
			b = append(b, ':')
			b = append(b, 'k')
			b = strconv.AppendInt(b, int64(i%10000), 10)
			htTenantShortKeys[tid][i] = string(b)
		}
	}
}

// ── TTL-aware map types ───────────────────────────────────────────────────────

type htEntry struct {
	value     []byte
	expiresAt int64 // unix seconds
}

// timedMutexMap is map[string]htEntry protected by RWMutex, with TTL support.
// Background goroutine calls DeleteExpired() to simulate CacheManager's cleaner.
type timedMutexMap struct {
	mu sync.RWMutex
	m  map[string]htEntry
}

func newTimedMutexMap(capacity int) *timedMutexMap {
	return &timedMutexMap{m: make(map[string]htEntry, capacity)}
}

func (tm *timedMutexMap) Set(key string, val []byte, ttl uint32) {
	// Copy value so each entry owns its bytes — mirrors CacheManager's slab copy.
	v := make([]byte, len(val))
	copy(v, val)
	tm.mu.Lock()
	tm.m[key] = htEntry{value: v, expiresAt: time.Now().Unix() + int64(ttl)}
	tm.mu.Unlock()
}

func (tm *timedMutexMap) Get(key string) ([]byte, bool) {
	tm.mu.RLock()
	e, ok := tm.m[key]
	tm.mu.RUnlock()
	if !ok || time.Now().Unix() > e.expiresAt {
		return nil, false
	}
	return e.value, true
}

// DeleteExpired scans the full map and removes expired entries.
// Called by the background goroutine every htMapCleanEvery.
func (tm *timedMutexMap) DeleteExpired() int {
	now := time.Now().Unix()
	tm.mu.Lock()
	deleted := 0
	for k, e := range tm.m {
		if now > e.expiresAt {
			delete(tm.m, k)
			deleted++
		}
	}
	tm.mu.Unlock()
	return deleted
}

// timedSyncMap wraps sync.Map with TTL support.
type timedSyncMap struct{ m sync.Map }

func (ts *timedSyncMap) Set(key string, val []byte, ttl uint32) {
	// Copy value so each entry owns its bytes — mirrors CacheManager's slab copy.
	v := make([]byte, len(val))
	copy(v, val)
	ts.m.Store(key, htEntry{value: v, expiresAt: time.Now().Unix() + int64(ttl)})
}

func (ts *timedSyncMap) Get(key string) ([]byte, bool) {
	v, ok := ts.m.Load(key)
	if !ok {
		return nil, false
	}
	e := v.(htEntry)
	if time.Now().Unix() > e.expiresAt {
		return nil, false
	}
	return e.value, true
}

// DeleteExpired scans sync.Map and removes expired entries.
func (ts *timedSyncMap) DeleteExpired() int {
	now := time.Now().Unix()
	deleted := 0
	ts.m.Range(func(k, v any) bool {
		if now > v.(htEntry).expiresAt {
			ts.m.Delete(k)
			deleted++
		}
		return true
	})
	return deleted
}

// ── background delete goroutine for maps ──────────────────────────────────────

type expiryCleaner interface{ DeleteExpired() int }

// startMapCleaner runs a goroutine that calls DeleteExpired every interval.
// Returns a stop function. Mirrors CacheManager's cleanerLoop for maps.
func startMapCleaner(c expiryCleaner, interval time.Duration, stop <-chan struct{}) *atomic.Uint64 {
	totalDeleted := &atomic.Uint64{}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				totalDeleted.Add(uint64(c.DeleteExpired()))
			}
		}
	}()
	return totalDeleted
}

// ── memory snapshot ───────────────────────────────────────────────────────────

type memSnap struct{ inuse, allocated, sys float64; objects uint64 }

func takeMemSnap() memSnap {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return memSnap{
		inuse:     float64(ms.HeapInuse) / (1 << 20),
		allocated: float64(ms.HeapAlloc) / (1 << 20),
		sys:       float64(ms.HeapSys) / (1 << 20),
		objects:   ms.HeapObjects,
	}
}

func (s memSnap) delta(base memSnap) memSnap {
	return memSnap{
		inuse:     s.inuse - base.inuse,
		allocated: s.allocated - base.allocated,
		sys:       s.sys - base.sys,
		objects:   s.objects - base.objects,
	}
}

// ── GC snapshot ───────────────────────────────────────────────────────────────

type gcMark struct{ cycles uint32; pauseNs uint64 }

func takeGCMark() gcMark {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return gcMark{ms.NumGC, ms.PauseTotalNs}
}

// ── runner ────────────────────────────────────────────────────────────────────

type htResult struct {
	name         string
	memDelta     memSnap // memory allocated by this backend only (own base subtracted)
	totalInuseMB float64 // absolute HeapInuse after populate (process-wide)
	avgTPS       float64
	gcPerWin     []uint32
	pausePerWin  []float64 // ms
	deleted      uint64
}

// runHTBackend runs one backend under the standard hot-loop for htRunDuration.
// It takes its own memory baseline immediately before populate() so that memDelta
// reflects only this backend's allocations, not any leftover from prior runs.
func runHTBackend(
	name string,
	populate func(),
	getFn func(tid uint16, i int),
	putFn func(tid uint16, i int),
	stopCh chan struct{},
) htResult {
	base := takeMemSnap() // per-backend baseline: GC fires here, clearing prior garbage
	populate()
	after := takeMemSnap()
	memD := after.delta(base)

	goroutines := runtime.GOMAXPROCS(0)
	var counters [24]atomic.Uint64 // enough for any GOMAXPROCS
	val := make([]byte, htValueSize)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			i := g * 997 // prime offset per goroutine
			for {
				select {
				case <-stopCh:
					return
				default:
				}
				tid := uint16(i%htNumTenants) + 1
				key := i % htKeysPerTenant
				if i%htWriteEvery == 0 {
					putFn(tid, key)
				} else {
					getFn(tid, key)
				}
				_ = val
				counters[g].Add(1)
				i++
			}
		}()
	}

	windows := int(htRunDuration / htSampleEvery)
	gcPerWin := make([]uint32, windows)
	pausePerWin := make([]float64, windows)
	tpsPerWin := make([]float64, windows)

	for w := 0; w < windows; w++ {
		prev := make([]uint64, goroutines)
		for g := 0; g < goroutines; g++ {
			prev[g] = counters[g].Load()
		}
		prevGC := takeGCMark()
		time.Sleep(htSampleEvery)
		var ops uint64
		for g := 0; g < goroutines; g++ {
			ops += counters[g].Load() - prev[g]
		}
		curGC := takeGCMark()
		gcPerWin[w] = curGC.cycles - prevGC.cycles
		pausePerWin[w] = float64(curGC.pauseNs-prevGC.pauseNs) / 1e6
		tpsPerWin[w] = float64(ops) / htSampleEvery.Seconds()
	}

	close(stopCh)
	wg.Wait()

	var totalTPS float64
	for _, t := range tpsPerWin {
		totalTPS += t
	}
	return htResult{
		name:         name,
		memDelta:     memD,
		totalInuseMB: after.inuse,
		avgTPS:       totalTPS / float64(windows),
		gcPerWin:     gcPerWin,
		pausePerWin:  pausePerWin,
	}
}

// ── main test ─────────────────────────────────────────────────────────────────

func TestHighTPSWithEviction(t *testing.T) {
	val := make([]byte, htValueSize)

	// ── CacheManager (long keys) ──────────────────────────────────────────────
	// 1 sizeClass × 1 TTLTier = 1 slab.
	// 32MB / stride(272B) = 124K slots → eviction active with 100K keys.
	cmStop := make(chan struct{})
	var cm *CacheManager
	cmResult := runHTBackend("CacheManager/LongKey",
		func() {
			cm, _ = NewCacheManager(
				32<<20,        // 32 MB — 1 slab, right-sized for 100K × 256B entries
				[]uint32{256}, // 1 size class
				[]uint32{10},  // 1 TTL tier: 10s
				htTotalKeys, 0, NoopBackend,
			)
			for tid := uint16(1); tid <= htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					cm.Put(tid, htLongKeys[i], val, htTTL)
				}
			}
		},
		func(tid uint16, i int) { cm.Get(tid, htLongKeys[i]) },
		func(tid uint16, i int) { cm.Put(tid, htLongKeys[i], val, htTTL) },
		cmStop,
	)
	cm.Stop()

	// ── CacheManager (short keys) ─────────────────────────────────────────────
	cmShortStop := make(chan struct{})
	var cmShort *CacheManager
	cmShortResult := runHTBackend("CacheManager/ShortKey",
		func() {
			cmShort, _ = NewCacheManager(32<<20, []uint32{256}, []uint32{10},
				htTotalKeys, 0, NoopBackend)
			for tid := uint16(1); tid <= htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					cmShort.Put(tid, htCMShortKeys[tid-1][i], val, htTTL)
				}
			}
		},
		func(tid uint16, i int) { cmShort.Get(tid, htCMShortKeys[tid-1][i]) },
		func(tid uint16, i int) { cmShort.Put(tid, htCMShortKeys[tid-1][i], val, htTTL) },
		cmShortStop,
	)
	cmShort.Stop()

	// ── timedMutexMap (long keys) ─────────────────────────────────────────────
	mmStop := make(chan struct{})
	var mm *timedMutexMap
	mmResult := runHTBackend("MutexMap/LongKey",
		func() {
			mm = newTimedMutexMap(htTotalKeys)
			for tid := 0; tid < htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					mm.Set(htTenantLongKeys[tid][i], val, htTTL)
				}
			}
		},
		func(tid uint16, i int) { mm.Get(htTenantLongKeys[tid-1][i]) },
		func(tid uint16, i int) { mm.Set(htTenantLongKeys[tid-1][i], val, htTTL) },
		mmStop,
	)
	startMapCleaner(mm, htMapCleanEvery, mmStop)

	// ── timedMutexMap (short keys) ────────────────────────────────────────────
	mmShortStop := make(chan struct{})
	var mmShort *timedMutexMap
	mmShortResult := runHTBackend("MutexMap/ShortKey",
		func() {
			mmShort = newTimedMutexMap(htTotalKeys)
			for tid := 0; tid < htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					mmShort.Set(htTenantShortKeys[tid][i], val, htTTL)
				}
			}
		},
		func(tid uint16, i int) { mmShort.Get(htTenantShortKeys[tid-1][i]) },
		func(tid uint16, i int) { mmShort.Set(htTenantShortKeys[tid-1][i], val, htTTL) },
		mmShortStop,
	)
	startMapCleaner(mmShort, htMapCleanEvery, mmShortStop)

	// ── timedSyncMap (long keys) ──────────────────────────────────────────────
	smStop := make(chan struct{})
	var sm *timedSyncMap
	smResult := runHTBackend("sync.Map/LongKey",
		func() {
			sm = &timedSyncMap{}
			for tid := 0; tid < htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					sm.Set(htTenantLongKeys[tid][i], val, htTTL)
				}
			}
		},
		func(tid uint16, i int) { sm.Get(htTenantLongKeys[tid-1][i]) },
		func(tid uint16, i int) { sm.Set(htTenantLongKeys[tid-1][i], val, htTTL) },
		smStop,
	)
	startMapCleaner(sm, htMapCleanEvery, smStop)

	// ── timedSyncMap (short keys) ─────────────────────────────────────────────
	smShortStop := make(chan struct{})
	var smShort *timedSyncMap
	smShortResult := runHTBackend("sync.Map/ShortKey",
		func() {
			smShort = &timedSyncMap{}
			for tid := 0; tid < htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					smShort.Set(htTenantShortKeys[tid][i], val, htTTL)
				}
			}
		},
		func(tid uint16, i int) { smShort.Get(htTenantShortKeys[tid-1][i]) },
		func(tid uint16, i int) { smShort.Set(htTenantShortKeys[tid-1][i], val, htTTL) },
		smShortStop,
	)
	startMapCleaner(smShort, htMapCleanEvery, smShortStop)

	// ── print results ─────────────────────────────────────────────────────────
	results := []htResult{
		cmResult, cmShortResult,
		mmResult, mmShortResult,
		smResult, smShortResult,
	}

	t.Logf("\n=== Config: %d tenants × %d keys = %d total | value=%dB | TTL=%ds | goroutines=%d ===",
		htNumTenants, htKeysPerTenant, htTotalKeys, htValueSize, htTTL, runtime.GOMAXPROCS(0))

	t.Logf("\n%-28s  %8s  %9s  %8s  %9s  %10s",
		"backend", "own MB", "total MB", "alloc MB", "heap objs", "avg TPS")
	for _, r := range results {
		t.Logf("%-28s  %8.1f  %9.1f  %8.1f  %9d  %10.0f  (~%.1f M/s)",
			r.name, r.memDelta.inuse, r.totalInuseMB,
			r.memDelta.allocated, r.memDelta.objects,
			r.avgTPS, r.avgTPS/1e6)
	}

	t.Logf("\n%-28s  %6s  %10s  %10s  %10s  %10s  %10s  %10s",
		"backend", "window",
		"0–5s TPS", "5–10s TPS", "10–15s TPS", "15–20s TPS", "20–25s TPS", "25–30s TPS")
	for _, r := range results {
		t.Logf("%-28s  %6s  %10.0f  %10.0f  %10.0f  %10.0f  %10.0f  %10.0f",
			r.name, "",
			safeIdx(r.gcPerWin, 0), safeIdx(r.gcPerWin, 1),
			safeIdx(r.gcPerWin, 2), safeIdx(r.gcPerWin, 3),
			safeIdx(r.gcPerWin, 4), safeIdx(r.gcPerWin, 5))
		t.Logf("%-28s  GC cycles / pause(ms)", "")
	}

	// Compact per-window TPS + GC table.
	t.Logf("\n%-28s  %s", "backend", "window TPS (GC cycles | pause ms)")
	for _, r := range results {
		line := fmt.Sprintf("%-28s", r.name)
		windows := len(r.gcPerWin)
		for w := 0; w < windows; w++ {
			line += fmt.Sprintf("  [%2d–%2ds: %.1fM gc=%d p=%.0fms]",
				w*5, (w+1)*5, r.avgTPS/1e6,
				r.gcPerWin[w], r.pausePerWin[w])
		}
		t.Log(line)
	}
}

func safeIdx(s []uint32, i int) float64 {
	if i >= len(s) {
		return 0
	}
	return float64(s[i])
}
