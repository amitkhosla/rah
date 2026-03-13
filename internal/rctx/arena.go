package rctx

import "sync"

// Arena sizing constants. Adjust via profiling — if ArenaOverflowed rate
// exceeds ~1% in metrics, increase ArenaBlockSize or MaxExtraArenas.
const (
	ArenaBlockSize = 4096 // bytes per arena block (matches OS page size)
	MaxExtraArenas = 4    // pool-borrowed extras: up to 4×4KB = 16KB overflow

	BaseByteSlots = 32 // inline byte-slot headers — covers most flows
	BaseIntSlots  = 16 // inline int-slot headers
	BaseBoolSlots = 8  // inline bool-slot headers

	ExtByteSlots = 64 // total byte slots when extension is borrowed (32 more)
	ExtIntSlots  = 32 // total int slots when extension is borrowed
	ExtBoolSlots = 16 // total bool slots when extension is borrowed
)

// arenaBlock is a fixed-size memory region for slot data.
// buf contains raw bytes only — no Go pointers — so GC never scans contents.
// Alignment: buf[4096] + used[4] = 4100B, struct alignment = 4B (int32). ✓
type arenaBlock struct {
	buf  [ArenaBlockSize]byte
	used int32
}

// slotExtBlock provides overflow slot headers when a flow needs more than the
// inline base (BaseByteSlots / BaseIntSlots / BaseBoolSlots).
// Borrowing one block extends capacity to ExtByteSlots / ExtIntSlots / ExtBoolSlots.
// Alignment: 64×24 + 32×8 + 16×1 = 1536+256+16 = 1808B, alignment = 8B. ✓
type slotExtBlock struct {
	slots [ExtByteSlots][]byte
	ints  [ExtIntSlots]int64
	bools [ExtBoolSlots]bool
}

var (
	// arenaPool holds pre-allocated 4KB blocks returned by requests that
	// exceeded their inline primary arena. GC does not scan block contents.
	arenaPool = sync.Pool{New: func() any { return new(arenaBlock) }}

	// slotExtPool holds pre-allocated slot-header extension blocks returned
	// by requests that exceeded the inline base slot counts.
	slotExtPool = sync.Pool{New: func() any { return new(slotExtBlock) }}
)
