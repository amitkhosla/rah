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
)

// arenaBlock is a fixed-size memory region for slot data.
// buf contains raw bytes only — no Go pointers — so GC never scans contents.
// Alignment: buf[4096] + used[4] = 4100B, struct alignment = 4B (int32). ✓
type arenaBlock struct {
	buf  [ArenaBlockSize]byte
	used int32
}

var (
	// arenaPool holds pre-allocated 4KB blocks returned by requests that
	// exceeded their inline primary arena. GC does not scan block contents.
	arenaPool = sync.Pool{New: func() any { return new(arenaBlock) }}
)
