package cache

// TestVsLibraries benchmarks our CacheManager against three popular Go cache
// libraries AND the native map baselines under production-realistic conditions.
//
// Libraries:
//
//	bigcache v3   — zero-GC ring-buffer; global LifeWindow TTL; copies internally.
//	ristretto v2  — TinyLFU admission + LRU eviction; per-entry TTL; async Set.
//	go-cache      — sync.RWMutex + map[string]interface{}; per-entry TTL; simple.
//
// Baselines:
//
//	timedMutexMap — sync.RWMutex + map[string]htEntry; closest to "just use a map".
//	timedSyncMap  — sync.Map + htEntry; lock-free for read-heavy workloads.
//
// Settings (identical to TestHighTPSWithEviction):
//
//	10 tenants × 10 000 keys = 100 000 entries  |  256 B values  |  10 s TTL
//	32 MB limit applied to CacheManager and bigcache (both pre-allocate).
//	ristretto MaxCost=32MB (adaptive); go-cache and maps have no hard limit.
//
// Memory columns:
//
//	"own MB"   — HeapInuse delta from a fresh GC baseline taken right before
//	             populate(). Shows only this backend's memory contribution.
//	"total MB" — absolute HeapInuse after populate (process-wide snapshot).
//
// Multi-tenancy:
//
//	CacheManager — tenantID passed as uint16 (native, zero key overhead).
//	All others   — tenant embedded in key string "t{tid}:key:{idx}".
//
// Value copy semantics (all implementations store independent copies):
//
//	CacheManager — copies value into slab on Put.
//	bigcache     — copies into ring buffer internally.
//	ristretto, go-cache, maps — explicit copy inside Set wrapper.
//
// Run with:
//
//	go test -v -run TestVsLibraries -timeout 600s ./internal/cache/

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	bigcachev3 "github.com/allegro/bigcache/v3"
	ristretto "github.com/dgraph-io/ristretto/v2"
	gocache "github.com/patrickmn/go-cache"
)

const libHardMaxMB = 32 // MB cap applied to CacheManager and bigcache

func TestVsLibraries(t *testing.T) {
	val := make([]byte, htValueSize) // source value; each store makes its own copy

	// ── 1. CacheManager (ours) — LongKey ─────────────────────────────────────
	cmStop := make(chan struct{})
	var cm *CacheManager
	cmRes := runHTBackend("CacheManager(ours)/LongKey",
		func() {
			cm, _ = NewCacheManager(
				libHardMaxMB<<20, []uint32{256}, []uint32{10},
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

	// ── 2. CacheManager (ours) — ShortKey ────────────────────────────────────
	cmShortStop := make(chan struct{})
	var cmShort *CacheManager
	cmShortRes := runHTBackend("CacheManager(ours)/ShortKey",
		func() {
			cmShort, _ = NewCacheManager(
				libHardMaxMB<<20, []uint32{256}, []uint32{10},
				htTotalKeys, 0, NoopBackend,
			)
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

	// ── 3. bigcache v3 — LongKey ──────────────────────────────────────────────
	bcStop := make(chan struct{})
	var bc *bigcachev3.BigCache
	bcRes := runHTBackend("bigcache v3/LongKey",
		func() {
			bc, _ = bigcachev3.New(context.Background(), bigcachev3.Config{
				Shards:             256,
				LifeWindow:         10 * time.Second,
				CleanWindow:        1 * time.Second,
				MaxEntriesInWindow: htTotalKeys,
				MaxEntrySize:       300,
				HardMaxCacheSize:   libHardMaxMB,
				Verbose:            false,
			})
			for tid := 0; tid < htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					_ = bc.Set(htTenantLongKeys[tid][i], val)
				}
			}
		},
		func(tid uint16, i int) { _, _ = bc.Get(htTenantLongKeys[tid-1][i]) },
		func(tid uint16, i int) { _ = bc.Set(htTenantLongKeys[tid-1][i], val) },
		bcStop,
	)
	_ = bc.Close()

	// ── 4. bigcache v3 — ShortKey ─────────────────────────────────────────────
	bcShortStop := make(chan struct{})
	var bcShort *bigcachev3.BigCache
	bcShortRes := runHTBackend("bigcache v3/ShortKey",
		func() {
			bcShort, _ = bigcachev3.New(context.Background(), bigcachev3.Config{
				Shards:             256,
				LifeWindow:         10 * time.Second,
				CleanWindow:        1 * time.Second,
				MaxEntriesInWindow: htTotalKeys,
				MaxEntrySize:       270,
				HardMaxCacheSize:   libHardMaxMB,
				Verbose:            false,
			})
			for tid := 0; tid < htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					_ = bcShort.Set(htTenantShortKeys[tid][i], val)
				}
			}
		},
		func(tid uint16, i int) { _, _ = bcShort.Get(htTenantShortKeys[tid-1][i]) },
		func(tid uint16, i int) { _ = bcShort.Set(htTenantShortKeys[tid-1][i], val) },
		bcShortStop,
	)
	_ = bcShort.Close()

	// ── 5. ristretto v2 — LongKey ─────────────────────────────────────────────
	// Note: Set is async (TinyLFU admission buffer). Not all items are immediately
	// visible. rc.Wait() is called after populate to flush the buffer.
	rcStop := make(chan struct{})
	var rc *ristretto.Cache[string, []byte]
	rcRes := runHTBackend("ristretto v2/LongKey",
		func() {
			rc, _ = ristretto.NewCache(&ristretto.Config[string, []byte]{
				NumCounters: int64(htTotalKeys * 10),
				MaxCost:     libHardMaxMB << 20,
				BufferItems: 64,
				Cost:        func(v []byte) int64 { return int64(len(v)) },
			})
			for tid := 0; tid < htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					v := make([]byte, htValueSize)
					copy(v, val)
					rc.SetWithTTL(htTenantLongKeys[tid][i], v, int64(htValueSize), 10*time.Second)
				}
			}
			rc.Wait()
		},
		func(tid uint16, i int) { _, _ = rc.Get(htTenantLongKeys[tid-1][i]) },
		func(tid uint16, i int) {
			v := make([]byte, htValueSize)
			copy(v, val)
			rc.SetWithTTL(htTenantLongKeys[tid-1][i], v, int64(htValueSize), 10*time.Second)
		},
		rcStop,
	)
	rc.Close()

	// ── 6. ristretto v2 — ShortKey ────────────────────────────────────────────
	rcShortStop := make(chan struct{})
	var rcShort *ristretto.Cache[string, []byte]
	rcShortRes := runHTBackend("ristretto v2/ShortKey",
		func() {
			rcShort, _ = ristretto.NewCache(&ristretto.Config[string, []byte]{
				NumCounters: int64(htTotalKeys * 10),
				MaxCost:     libHardMaxMB << 20,
				BufferItems: 64,
				Cost:        func(v []byte) int64 { return int64(len(v)) },
			})
			for tid := 0; tid < htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					v := make([]byte, htValueSize)
					copy(v, val)
					rcShort.SetWithTTL(htTenantShortKeys[tid][i], v, int64(htValueSize), 10*time.Second)
				}
			}
			rcShort.Wait()
		},
		func(tid uint16, i int) { _, _ = rcShort.Get(htTenantShortKeys[tid-1][i]) },
		func(tid uint16, i int) {
			v := make([]byte, htValueSize)
			copy(v, val)
			rcShort.SetWithTTL(htTenantShortKeys[tid-1][i], v, int64(htValueSize), 10*time.Second)
		},
		rcShortStop,
	)
	rcShort.Close()

	// ── 7. go-cache — LongKey ─────────────────────────────────────────────────
	gcStop := make(chan struct{})
	var gc *gocache.Cache
	gcRes := runHTBackend("go-cache/LongKey",
		func() {
			gc = gocache.New(10*time.Second, 1*time.Second)
			for tid := 0; tid < htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					v := make([]byte, htValueSize)
					copy(v, val)
					gc.SetDefault(htTenantLongKeys[tid][i], v)
				}
			}
		},
		func(tid uint16, i int) { gc.Get(htTenantLongKeys[tid-1][i]) },
		func(tid uint16, i int) {
			v := make([]byte, htValueSize)
			copy(v, val)
			gc.Set(htTenantLongKeys[tid-1][i], v, 10*time.Second)
		},
		gcStop,
	)

	// ── 8. go-cache — ShortKey ────────────────────────────────────────────────
	gcShortStop := make(chan struct{})
	var gcShort *gocache.Cache
	gcShortRes := runHTBackend("go-cache/ShortKey",
		func() {
			gcShort = gocache.New(10*time.Second, 1*time.Second)
			for tid := 0; tid < htNumTenants; tid++ {
				for i := 0; i < htKeysPerTenant; i++ {
					v := make([]byte, htValueSize)
					copy(v, val)
					gcShort.SetDefault(htTenantShortKeys[tid][i], v)
				}
			}
		},
		func(tid uint16, i int) { gcShort.Get(htTenantShortKeys[tid-1][i]) },
		func(tid uint16, i int) {
			v := make([]byte, htValueSize)
			copy(v, val)
			gcShort.Set(htTenantShortKeys[tid-1][i], v, 10*time.Second)
		},
		gcShortStop,
	)

	// ── 9. timedMutexMap — LongKey ────────────────────────────────────────────
	mmStop := make(chan struct{})
	var mm *timedMutexMap
	mmRes := runHTBackend("map+RWMutex/LongKey",
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

	// ── 10. timedMutexMap — ShortKey ──────────────────────────────────────────
	mmShortStop := make(chan struct{})
	var mmShort *timedMutexMap
	mmShortRes := runHTBackend("map+RWMutex/ShortKey",
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

	// ── 11. timedSyncMap — LongKey ────────────────────────────────────────────
	smStop := make(chan struct{})
	var sm *timedSyncMap
	smRes := runHTBackend("sync.Map/LongKey",
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

	// ── 12. timedSyncMap — ShortKey ───────────────────────────────────────────
	smShortStop := make(chan struct{})
	var smShort *timedSyncMap
	smShortRes := runHTBackend("sync.Map/ShortKey",
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

	// ── print results ─────────────────────────────────────────────────────────
	results := []htResult{
		cmRes, cmShortRes,
		bcRes, bcShortRes,
		rcRes, rcShortRes,
		gcRes, gcShortRes,
		mmRes, mmShortRes,
		smRes, smShortRes,
	}

	goroutines := 0
	for _, r := range results {
		if goroutines == 0 && len(r.gcPerWin) > 0 {
			goroutines = runtime.GOMAXPROCS(0)
		}
	}

	t.Logf("\n=== Library Comparison: %d tenants × %dk keys = %dk entries | %dB values | %ds TTL | %d goroutines ===",
		htNumTenants, htKeysPerTenant/1000, htTotalKeys/1000, htValueSize, htTTL, goroutines)
	t.Logf("Memory cap: %dMB (CacheManager=slab pre-alloc, bigcache=ring-buf pre-alloc,", libHardMaxMB)
	t.Logf("           ristretto=MaxCost adaptive, go-cache/maps=unlimited heap).")
	t.Logf("'own MB' = HeapInuse delta from per-backend GC baseline (this backend only).")
	t.Logf("'total MB' = absolute HeapInuse after populate (process-wide).\n")

	t.Logf("%-30s  %8s  %9s  %9s  %8s  %10s",
		"implementation", "own MB", "total MB", "heap objs", "GC/win", "avg TPS")
	t.Logf("%-30s  %8s  %9s  %9s  %8s  %10s",
		"------------------------------",
		"--------", "---------", "---------", "--------", "----------")

	for _, r := range results {
		avgGC := 0.0
		for _, g := range r.gcPerWin {
			avgGC += float64(g)
		}
		if len(r.gcPerWin) > 0 {
			avgGC /= float64(len(r.gcPerWin))
		}
		t.Logf("%-30s  %8.1f  %9.1f  %9d  %8.1f  %10.0f  (~%.1fM/s)",
			r.name,
			r.memDelta.inuse, r.totalInuseMB,
			r.memDelta.objects,
			avgGC,
			r.avgTPS, r.avgTPS/1e6)
	}

	// Per-window TPS + GC breakdown.
	t.Logf("\n%-30s  %s", "implementation", "per-5s window: [GC cycles | pause ms]")
	for _, r := range results {
		line := fmt.Sprintf("%-30s", r.name)
		for w := range r.gcPerWin {
			line += fmt.Sprintf("  [%2d–%2ds gc=%d p=%.0fms]",
				w*5, (w+1)*5, r.gcPerWin[w], r.pausePerWin[w])
		}
		t.Log(line)
	}

	// Feature comparison matrix.
	t.Logf(`
=== Feature Matrix ===
Feature               CacheManager  bigcache v3  ristretto v2  go-cache  map+RWMutex  sync.Map
──────────────────────────────────────────────────────────────────────────────────────────────
Multi-tenant native   ✓ tenantID     ✗ key pfx    ✗ key pfx     ✗ key pfx  ✗ key pfx   ✗ key pfx
Zero-GC reads         ✓             ✓             ✓              ✗          ✗           ✓ (r/o)
Pre-allocated memory  ✓ slab        ✓ ring buf    ✗ adaptive     ✗ heap     ✗ heap      ✗ heap
Per-entry TTL         ✓             ✗ global      ✓              ✓          ✓           ✓
Eviction policy       FIFO/TTL      FIFO          TinyLFU        TTL scan   TTL scan    TTL scan
Backend persistence   ✓ disk/Redis  ✗             ✗              ✗          ✗           ✗
Size-class tiering    ✓             ✗             ✗              ✗          ✗           ✗
Lock-free reads       ✓             ✗ shard lock  ✓              ✗ RWMu     ✗ RWMu      ✓
Memory cap            ✓             ✓             ✓ (MaxCost)    ✗          ✗           ✗
`)
}
