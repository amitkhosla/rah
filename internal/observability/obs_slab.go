package observability

import (
	"context"
	"fmt"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// ── sizing constants ──────────────────────────────────────────────────────────

const (
	// obsSlabCount is the ring depth: active (writers) + draining + reset + spare.
	// 4 gives the drain goroutine up to 3 rotation windows (~30 ms) to complete a
	// store write before writers wrap around and find no free slab.
	obsSlabCount = 4

	// obsTpsPerCPU is the per-logical-CPU TPS ceiling used to size each slab.
	// Raise this constant if profiling shows slab-full drops under peak load.
	obsTpsPerCPU = 20_000

	// obsWindowMs is the slab rotation interval. The drain goroutine fires every
	// obsWindowMs and seals the active slab; the next slab becomes active.
	obsWindowMs = 10

	// obsMaxCPU caps the CPU count for sizing so the total static allocation
	// stays bounded on machines with more than 64 logical CPUs.
	obsMaxCPU = 64
)

// ── slot inline-storage limits (bytes, longer values are truncated) ───────────

const (
	obsAPINameMax   = 32
	obsTenantKeyMax = 24
	obsMethodMax    = 8  // "OPTIONS" is 7 bytes — longest standard HTTP method
	obsPathMax      = 128
	obsExtraMax     = 4
	obsExtraKeyMax  = 16
	obsExtraValMax  = 24
)

// ── obsSlot ───────────────────────────────────────────────────────────────────

// obsSlot is a single pre-allocated access log record stamped in by the
// request goroutine. The layout is fixed at 512 bytes (8 × 64-byte cache
// lines) so that adjacent slots written by different CPUs never share a
// cache line, eliminating false-sharing without any per-slot padding logic.
//
// Field layout (offsets):
//
//	  0 –  71   numeric: 9 × int64/float64
//	 72 –  87   ids / lengths: uint16×2, uint8×5, pad 7
//	 88 – 279   inline strings: ApiName 32, TenantKey 24, Method 8, Path 128
//	280 – 447   inline extras: lengths 8, keys 64, values 96
//	448 – 451   ready uint32 (atomic store/load)
//	452 – 511   padding to 512 bytes
type obsSlot struct {
	// --- numeric (72 bytes) ---
	TimestampNs int64
	TotalMs     float64
	GatewayMs   float64
	UpstreamMs  float64
	TTFBMs      float64
	ConnSetupMs float64
	TransferMs  float64
	ReqBytes    int64
	ResBytes    int64

	// --- ids / lengths (16 bytes) ---
	TenantID     uint16
	Status       uint16
	APINameLen   uint8
	TenantKeyLen uint8
	MethodLen    uint8
	PathLen      uint8
	ExtraCount   uint8
	_pad0        [7]byte

	// --- inline strings (192 bytes) ---
	APIName   [obsAPINameMax]byte
	TenantKey [obsTenantKeyMax]byte
	Method    [obsMethodMax]byte
	Path      [obsPathMax]byte

	// --- inline extras (168 bytes) ---
	ExtraKeyLen [obsExtraMax]uint8
	ExtraValLen [obsExtraMax]uint8
	ExtraKey    [obsExtraMax][obsExtraKeyMax]byte
	ExtraVal    [obsExtraMax][obsExtraValMax]byte

	// --- ready flag (64 bytes incl. padding) ---
	// Plain uint32; callers use atomic.StoreUint32 / atomic.LoadUint32.
	// Kept as a raw field so its size is always exactly 4 bytes regardless
	// of Go version changes to sync/atomic wrapper types.
	ready uint32
	_pad1 [60]byte
}

// obsSlotSizeCheck panics at startup if the struct layout drifts from the
// intended 512 bytes. A unit test (obs_slab_test.go) also asserts this.
func init() {
	if sz := unsafe.Sizeof(obsSlot{}); sz != 512 {
		panic("observability: obsSlot size changed — update _pad1 to keep it 512 bytes (current: " +
			fmt.Sprintf("%d", sz) + ")")
	}
}

// ── obsSlab ───────────────────────────────────────────────────────────────────

// obsSlab is a fixed-capacity array of obsSlots with an atomic write cursor.
// The cursor and sealed flag occupy the first 64 bytes (one cache line) so
// that writers accessing the cursor never share a cache line with slot data.
type obsSlab struct {
	// First cache line: hot writer state.
	cursor int64    // claimed slot count; writers use atomic.AddInt64
	sealed uint32   // 1 when drain is processing; writers must not claim
	_pad   [52]byte // pad cursor + sealed to exactly 64 bytes

	// Slot backing array; length = slabCap, allocated once at init.
	slots []obsSlot
}

func newObsSlab(cap int) *obsSlab {
	return &obsSlab{slots: make([]obsSlot, cap)}
}

// reset clears only the slots that were used in the previous rotation, then
// rearms the slab for the next write cycle. Called by the drain goroutine only.
func (s *obsSlab) reset(usedCount int64) {
	for i := int64(0); i < usedCount; i++ {
		atomic.StoreUint32(&s.slots[i].ready, 0)
	}
	atomic.StoreInt64(&s.cursor, 0)
	atomic.StoreUint32(&s.sealed, 0)
}

// ── obsSlabRing ───────────────────────────────────────────────────────────────

// obsSlabRing is a lock-free rotating ring of obsSlabs.
//
// Write path (hot):
//
//	load active index (atomic) → Add(1) on slab cursor (atomic) →
//	write all slot fields (plain stores) → StoreUint32(ready, 1)
//
// Drain path (background goroutine, every obsWindowMs):
//
//	seal active slab → advance active index → wait for in-flight writers →
//	reconstruct []AccessLogRecord → hand off to pending batch → maybe flush
type obsSlabRing struct {
	slabs      [obsSlabCount]*obsSlab
	active     int32 // current active slab index; use atomic.LoadInt32/StoreInt32
	slabCap    int
	store      ObsStore
	batchSize  int
	flushEvery time.Duration
	dropped    uint64 // atomic
	stop       chan struct{}
	done       sync.WaitGroup
}

func newObsSlabRing(store ObsStore, slabCap, batchSize int, flushEvery time.Duration) *obsSlabRing {
	r := &obsSlabRing{
		slabCap:    slabCap,
		store:      store,
		batchSize:  batchSize,
		flushEvery: flushEvery,
		stop:       make(chan struct{}),
	}
	for i := range r.slabs {
		r.slabs[i] = newObsSlab(slabCap)
	}
	return r
}

func (r *obsSlabRing) start() {
	r.done.Add(1)
	go r.drain()
}

func (r *obsSlabRing) stopAndWait() {
	close(r.stop)
	r.done.Wait()
}

// write stamps record into the current active slab. Hot path: two atomics
// (cursor Add + ready Store), one bounds check, and plain field assignments.
// Zero allocations.
func (r *obsSlabRing) write(record AccessLogRecord) {
	slabIdx := atomic.LoadInt32(&r.active)
	slab := r.slabs[slabIdx]

	// Claim a slot index.
	idx := atomic.AddInt64(&slab.cursor, 1) - 1

	// Slab full or already sealed: count the drop and return.
	if idx >= int64(r.slabCap) || atomic.LoadUint32(&slab.sealed) != 0 {
		atomic.AddUint64(&r.dropped, 1)
		return
	}

	slot := &slab.slots[idx]

	// Numeric fields — plain stores, no copy.
	slot.TimestampNs = record.TimestampNs
	slot.TotalMs = record.TotalMs
	slot.GatewayMs = record.GatewayMs
	slot.UpstreamMs = record.UpstreamMs
	slot.TTFBMs = record.TTFBMs
	slot.ConnSetupMs = record.ConnSetupMs
	slot.TransferMs = record.TransferMs
	slot.ReqBytes = record.ReqBytes
	slot.ResBytes = record.ResBytes
	slot.TenantID = record.TenantID
	slot.Status = uint16(record.Status)

	// Inline string copies — the only memcopy work on the hot path.
	slot.APINameLen = uint8(inlineCopy(slot.APIName[:], record.ApiName))
	slot.TenantKeyLen = uint8(inlineCopy(slot.TenantKey[:], record.TenantKey))
	slot.MethodLen = uint8(inlineCopy(slot.Method[:], record.Method))
	slot.PathLen = uint8(inlineCopy(slot.Path[:], record.Path))

	// Extra fields: iterate the map once, store up to obsExtraMax entries.
	ei := 0
	for k, v := range record.Extra {
		if ei >= obsExtraMax {
			break
		}
		slot.ExtraKeyLen[ei] = uint8(inlineCopy(slot.ExtraKey[ei][:], k))
		slot.ExtraValLen[ei] = uint8(inlineCopy(slot.ExtraVal[ei][:], v))
		ei++
	}
	slot.ExtraCount = uint8(ei)

	// Signal drain: all fields are written.
	atomic.StoreUint32(&slot.ready, 1)
}

// drain is the single consumer goroutine. It rotates slabs every obsWindowMs,
// accumulates records into a pending batch, and flushes to the store either
// when the batch reaches batchSize or when flushEvery elapses.
func (r *obsSlabRing) drain() {
	defer r.done.Done()

	rotateTick := time.NewTicker(obsWindowMs * time.Millisecond)
	flushTick := time.NewTicker(r.flushEvery)
	defer rotateTick.Stop()
	defer flushTick.Stop()

	// pending accumulates records across multiple rotation windows before
	// writing to the store, preserving the batchSize / flushEvery semantics.
	pending := make([]AccessLogRecord, 0, r.batchSize*2)

	flush := func() {
		if len(pending) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = r.store.WriteAccessLog(ctx, pending)
		cancel()
		pending = pending[:0]
	}

	for {
		select {
		case <-rotateTick.C:
			r.rotateSlab(&pending)
			if len(pending) >= r.batchSize {
				flush()
			}

		case <-flushTick.C:
			flush()

		case <-r.stop:
			r.rotateSlab(&pending)
			flush()
			return
		}
	}
}

// rotateSlab seals the active slab, advances the active pointer, waits for
// any in-flight writers to finish, then appends all ready slots to out.
func (r *obsSlabRing) rotateSlab(out *[]AccessLogRecord) {
	current := atomic.LoadInt32(&r.active)
	slab := r.slabs[current]

	// Seal: block new claims on this slab.
	atomic.StoreUint32(&slab.sealed, 1)

	// Advance active so writers immediately move to the next slab.
	next := (current + 1) % obsSlabCount
	atomic.StoreInt32(&r.active, next)

	// Snapshot the claimed count.
	count := atomic.LoadInt64(&slab.cursor)
	if count <= 0 {
		slab.reset(0)
		return
	}
	if count > int64(r.slabCap) {
		count = int64(r.slabCap)
	}

	// Straggler guard: a writer that claimed a slot just before the seal may
	// still be writing. At 10-20 K TPS the write window is ~100 ns so the
	// spin terminates almost immediately. The 1 ms deadline is a safety rail.
	deadline := time.Now().Add(time.Millisecond)
	for i := int64(0); i < count; i++ {
		slot := &slab.slots[i]
		for atomic.LoadUint32(&slot.ready) == 0 {
			if time.Now().After(deadline) {
				atomic.AddUint64(&r.dropped, 1)
				goto nextSlot
			}
			runtime.Gosched()
		}
		*out = append(*out, slotToRecord(slot))
	nextSlot:
	}

	// Reset slab for the next rotation cycle.
	slab.reset(count)
}

// slotToRecord reconstructs an AccessLogRecord from an obsSlot.
// Called in the drain goroutine — allocations here are acceptable.
func slotToRecord(s *obsSlot) AccessLogRecord {
	r := AccessLogRecord{
		TimestampNs: s.TimestampNs,
		ApiName:     string(s.APIName[:s.APINameLen]),
		TenantID:    s.TenantID,
		TenantKey:   string(s.TenantKey[:s.TenantKeyLen]),
		Method:      string(s.Method[:s.MethodLen]),
		Path:        string(s.Path[:s.PathLen]),
		Status:      int(s.Status),
		TotalMs:     s.TotalMs,
		GatewayMs:   s.GatewayMs,
		UpstreamMs:  s.UpstreamMs,
		TTFBMs:      s.TTFBMs,
		ConnSetupMs: s.ConnSetupMs,
		TransferMs:  s.TransferMs,
		ReqBytes:    s.ReqBytes,
		ResBytes:    s.ResBytes,
	}
	if s.ExtraCount > 0 {
		r.Extra = make(map[string]string, s.ExtraCount)
		for i := uint8(0); i < s.ExtraCount; i++ {
			k := string(s.ExtraKey[i][:s.ExtraKeyLen[i]])
			v := string(s.ExtraVal[i][:s.ExtraValLen[i]])
			r.Extra[k] = v
		}
	}
	return r
}

// ── helpers ───────────────────────────────────────────────────────────────────

// inlineCopy copies up to len(dst) bytes from src into dst and returns the
// number of bytes copied. No allocation; src is truncated if it exceeds dst.
func inlineCopy(dst []byte, src string) int {
	n := len(src)
	if n > len(dst) {
		n = len(dst)
	}
	copy(dst[:n], src)
	return n
}

// obsComputeSlabCap returns the slab slot capacity for the current process,
// sized to hold one full obsWindowMs window of writes at obsTpsPerCPU per CPU.
// The result is rounded up to the next power of two for cheap index masking.
//
// Capacity table (obsTpsPerCPU=20 000, obsWindowMs=10):
//
//	 2 CPUs →    400 → 512 slots   (~262 KB / slab)
//	 4 CPUs →    800 → 1 024 slots (~512 KB / slab)
//	 8 CPUs →  1 600 → 2 048 slots (~  1 MB / slab)
//	16 CPUs →  3 200 → 4 096 slots (~  2 MB / slab)
//	32 CPUs →  6 400 → 8 192 slots (~  4 MB / slab)
//	64 CPUs → 12 800 → 16 384 slots (~  8 MB / slab)
//
// Total static allocation (4 slabs): 1 MB – 32 MB across the supported range.
func obsComputeSlabCap() int {
	ncpu := runtime.GOMAXPROCS(0)
	if ncpu > obsMaxCPU {
		ncpu = obsMaxCPU
	}
	target := ncpu * obsTpsPerCPU * obsWindowMs / 1000
	if target < 256 {
		target = 256 // floor: never allocate fewer than 256 slots
	}
	return 1 << bits.Len(uint(target-1)) // next power of 2 ≥ target
}
