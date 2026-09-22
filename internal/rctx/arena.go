package rctx

import "sync"

// Arena sizing constants. Adjust via profiling — if ArenaOverflowed rate
// exceeds ~1% in metrics, increase ArenaInlineSize or ArenaBlockSize.
const (
	ArenaBlockSize     = 4096 // bytes per pool-borrowed ext block (4KB)
	ArenaInlineSize    = 1024 // bytes always-inline in Context struct (no pool)
	SlotValueThreshold = 256  // values ≤ this go into arena; larger go to heap

	BaseByteSlots = 48 // inline byte-slot count — covers realistic flows
	BaseIntSlots  = 16 // inline int-slot count
	BaseBoolSlots = 8  // inline bool-slot count

	// ExtIntSlots is the overflow int-slot count, pool-borrowed when the inline
	// base fills. Total available = BaseIntSlots + ExtIntSlots = 32.
	// Sized at 2 cache lines (16 × 8 = 128 bytes) — stays warm after first access.
	ExtIntSlots = 16
)

// arenaBlock is a fixed-size memory region for slot data.
// buf contains raw bytes only — no Go pointers — so GC never scans contents.
// Alignment: buf[4096] + used[4] = 4100B, struct alignment = 4B (int32). ✓
type arenaBlock struct {
	buf  [ArenaBlockSize]byte
	used int32
}

// intSlotBlock is a pool-borrowed overflow block for flows that need more than
// BaseIntSlots integer counters (e.g. complex flows with many loop variables).
// Zeroed on borrow; returned to pool by ReleaseOverflow before ctx.Pool.Put.
type intSlotBlock struct {
	slots [ExtIntSlots]int64
}

var (
	// arenaPool holds pre-allocated 4KB blocks returned by requests that
	// exceeded their inline primary arena. GC does not scan block contents.
	arenaPool = sync.Pool{New: func() any { return new(arenaBlock) }}

	// intSlotExtPool holds overflow int-slot blocks for complex flows.
	// Nil intSlotExt on Context means the common path — no overhead.
	intSlotExtPool = sync.Pool{New: func() any { return new(intSlotBlock) }}
)
