package rctx

import (
	"fmt"
	"sync"
)

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

// SlotOverflowStore persists slot values that exceed arena capacity, or slot
// indices that exceed the maximum in-memory count, to an external backing
// store (disk, GCS, Redis, etc.).
//
// It is engaged only when:
//   - All pool-borrowed arena blocks are exhausted (slot data > 20KB), or
//   - A single value is larger than one arena block (> 4KB), or
//   - A slot index exceeds ExtByteSlots (64).
//
// Keys are request-scoped (include ReqID) so concurrent requests never
// collide. All keys are deleted by FlowManager.ReturnContext at request end —
// storage is ephemeral, not durable.
//
// Implementations must be safe for concurrent use.
// Use engine.NewSlotOverflowAdapter to wrap an existing datastore.KeyValueStore.
type SlotOverflowStore interface {
	SlotPut(key string, value []byte) error
	SlotGet(key string) ([]byte, error)
	SlotDelete(key string) error
}

// slotOverflowSentinel is a two-byte prefix marking a ByteSlot as an
// external store reference rather than inline arena data.
// Chosen to be invalid UTF-8 and unreachable by HTTP header/query/path values.
var slotOverflowSentinel = [2]byte{0x00, 0xFF}

// IsSlotOverflowRef returns true if b is a store-reference sentinel.
func IsSlotOverflowRef(b []byte) bool {
	return len(b) >= 2 && b[0] == slotOverflowSentinel[0] && b[1] == slotOverflowSentinel[1]
}

// DecodeSlotOverflowKey extracts the store key from a sentinel byte slice.
func DecodeSlotOverflowKey(b []byte) string { return string(b[2:]) }

// BuildSlotDataKey returns a DataStore key for a value too large for the arena.
// Format: slot-ov/{reqID}/{seq} — unique within a request via the seq counter.
func BuildSlotDataKey(reqID uint64, seq uint32) string {
	return fmt.Sprintf("slot-ov/%d/%d", reqID, seq)
}

// BuildSlotIndexKey returns a DataStore key for a slot index beyond in-memory
// capacity. Format: slot-idx/{reqID}/{slotIdx} — deterministic from index.
func BuildSlotIndexKey(reqID uint64, slotIdx int) string {
	return fmt.Sprintf("slot-idx/%d/%d", reqID, slotIdx)
}

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
