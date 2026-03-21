/*
Package icache implements a concurrent trie index where every trie node
carries inline key-value slots, replacing the LookupIndex from the cache
package with an InlineIndex that stores entries directly in nodes.

Design — "inline-slot trie":

	Every node holds BOTH:
	  • 4 inline slots (tag+val, 16 B each = 64 B = one cache line) that store
	    actual index entries directly, without a separate pool.
	  • 8 child node pointers (uint32 × 8 = 32 B, second cache line).

	Lookup visits the root of the query path, scans its 4 inline slots first,
	and only descends into a child branch when the tag is not found there.
	Because the inline slots are on the first 64-byte cache line, a hit at the
	root (or any shallow node) costs one cache miss and no pool indirection.

	Insert navigates to the shallowest node on the query path that has a free
	slot, and stores the entry there.  A new child is created only when the
	current node is full AND the depth limit has not been reached.

Swiss-style H2 filter (h2word — 16-bit groups):

	h2word packs four 16-bit H2 values (one per inline slot) into a single
	atomic uint64.  H2 = upper 16 bits of the slot's tag (bits 48–63), forced
	to ≥1 for live entries; 0 means empty/tombstone.

	Using 16-bit groups instead of 8-bit groups avoids the false-positive rate
	explosion that occurs when the upper 8 bits of different tags collide.
	For hashIdx tags, bits 48-63 carry real fingerprint bytes (see makeTagHashH2).

	On Get, h2word is loaded from CL2 (already fetched for children routing).
	If no 16-bit group matches the query H2, the slot scan (CL1) is skipped.
*/
package cache

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	iSlots    = 4              // inline slots per node (one cache line of tag+val)
	iFanout   = 8              // children per node (8-way branching)
	iBits     = 3              // tag bits consumed per trie level
	iMask     = uint64(iFanout - 1)
	iMaxDepth = 10             // maximum depth (10 × 3 = 30 routing bits used)

	iDefaultShardBits = 8 // default: 256 shards
	iMaxShardBits     = 20

	iEmpty     = uint64(0)  // sentinel: empty slot tag
	iTombstone = ^uint64(0) // sentinel: deleted slot tag

	iNullChild = uint32(0) // reserved: "no child" sentinel in node pool

	iChunkBits = 10                      // 1024 nodes per pool chunk
	iChunkSize = 1 << iChunkBits         // nodes per chunk
	iChunkMask = uint32(iChunkSize - 1)

	// iGracePeriodNs: minimum age a detached node must have before recycling.
	// 10 ms is conservative for Linux/macOS; safe for Windows where quantum
	// stretching to ~15 ms is possible.
	iGracePeriodNs = int64(10_000_000)
)

// ── node layout ───────────────────────────────────────────────────────────────

// iSlot is one inline index entry.  16 bytes.
// SC ordering: val stored before tag on write; tag read before val on read.
type iSlot struct {
	tag atomic.Uint64 // 8 B
	val atomic.Uint64 // 8 B
}

// iNode is a trie node with inline key-value slots AND child pointers.
//
// Memory layout (128 bytes = 2 × 64-byte cache lines):
//
//	Cache line 1 (bytes   0–63): 4 inline iSlots — tag+val pairs.
//	Cache line 2 (bytes  64–95): 8 child node indices (uint32 × 8 = 32 B).
//	             (bytes 96–103): freedAt — unix-ns recycle timestamp.
//	             (bytes 104–111): h2word — packed H2 metadata, two bytes per slot.
//	             (bytes 112–127): 16 bytes padding.
//
// Swiss-style H2 filter (h2word):
//
//	h2word packs four 16-bit H2 values (one per inline slot) into a single
//	atomic uint64.  H2 = upper 16 bits of the slot's tag (bits 48–63),
//	forced to ≥1 for live entries; 0 means empty/tombstone.
//
//	Using 16-bit groups provides better discrimination than 8-bit groups,
//	especially for hashIdx where bits 48–63 carry real fingerprint bytes.
type iNode struct {
	// Cache line 1: inline key-value slots.
	slots [iSlots]iSlot // 4 × 16 B = 64 B

	// Cache line 2: child pointers + recycle timestamp + H2 metadata + padding.
	children [iFanout]uint32 // 8 × 4 B = 32 B  (offset  64)
	freedAt  int64           // 8 B              (offset  96)
	h2word   atomic.Uint64   // 8 B              (offset 104) — packed H2 uint16s
	_        [16]byte        // pad to 128 B     (offset 112)
}

// h2ForTag derives the H2 uint16 for a tag using the upper 16 bits (bits 48–63).
// Forced to ≥1 so that 0 remains an unambiguous "empty" sentinel in h2word.
// For hashIdx, the caller packs real fingerprint bytes into bits 48–63 via
// makeTagHashH2, so this function extracts them without extra mixing.
// For tinyIdx, the upper bits vary enough to serve as good discriminators.
func h2ForTag(tag uint64) uint16 {
	h := uint16(tag >> 48)
	if h == 0 {
		h = 1
	}
	return h
}

// h2Set returns a new h2word with the 16-bit group at position i (0–3) set to v,
// leaving the other groups unchanged.
func h2Set(word uint64, i int, v uint16) uint64 {
	shift := uint(i * 16)
	return (word &^ (0xFFFF << shift)) | (uint64(v) << shift)
}

// h2AnyMatch reports whether any 16-bit group in word equals queryH2 (which must be ≥1).
// Uses the "has-zero-word" bit trick extended to 16-bit groups.
func h2AnyMatch(word uint64, queryH2 uint16) bool {
	// XOR with broadcast of queryH2: matching 16-bit groups become 0x0000.
	x := word ^ (uint64(queryH2) * 0x0001000100010001)
	// A 16-bit group is zero iff (x - 0x0001000100010001) & ^x & 0x8000800080008000 != 0.
	return (x-0x0001000100010001)&^x&0x8000800080008000 != 0
}

// ── node pool (chunked, stable pointers) ──────────────────────────────────────

// iNodePool is a bump-allocator for iNode values using fixed-size chunks.
//
// Growing the pool appends a new chunk pointer without moving existing chunks,
// so held *iNode pointers remain valid across pool growth.
//
// Index 0 is reserved as the "no child" sentinel; real nodes start at 1.
//
// Recycling: nodes detached during compaction are enqueued in a FIFO ready
// list.  Each node carries its own freedAt timestamp.  alloc() checks the
// front of the queue; if freedAt + iGracePeriodNs ≤ now the node is safe
// to reuse, otherwise a fresh bump-allocation is made.
type iChunkSlice = []*[iChunkSize]iNode

type iNodePool struct {
	mu      sync.Mutex
	list    atomic.Pointer[iChunkSlice]
	next    atomic.Uint32
	ready   []uint32 // FIFO queue of detached node indices (oldest at readyHd)
	readyHd int      // index of oldest element; compacted when > len/2
}

// ensure guarantees chunk backing for all indices up to maxIdx.
// Must be called with p.mu held.
func (p *iNodePool) ensure(maxIdx uint32) {
	needed := int(maxIdx>>iChunkBits) + 1
	list := p.list.Load()
	if list != nil && len(*list) >= needed {
		return
	}
	cur := 0
	if list != nil {
		cur = len(*list)
	}
	newList := make(iChunkSlice, needed)
	if list != nil {
		copy(newList, *list)
	}
	for i := cur; i < needed; i++ {
		newList[i] = new([iChunkSize]iNode)
	}
	p.list.Store(&newList)
}

// alloc claims one node index, reusing a recycled node when its grace period
// has expired.  Never returns 0 (reserved sentinel).
func (p *iNodePool) alloc() uint32 {
	p.mu.Lock()
	if p.readyHd < len(p.ready) {
		idx := p.ready[p.readyHd]
		if time.Now().UnixNano()-p.node(idx).freedAt >= iGracePeriodNs {
			p.readyHd++
			p.mu.Unlock()
			resetNode(p.node(idx))
			return idx
		}
	}
	p.mu.Unlock()

	idx := p.next.Add(1) - 1
	if idx == iNullChild {
		// Skip the reserved sentinel.
		idx = p.next.Add(1) - 1
	}
	p.mu.Lock()
	p.ensure(idx)
	p.mu.Unlock()
	return idx
}

// node returns a stable *iNode pointer for idx.  Lock-free.
func (p *iNodePool) node(idx uint32) *iNode {
	list := p.list.Load()
	return &(*list)[idx>>iChunkBits][idx&iChunkMask]
}

// freeLater timestamps each detached node and appends them to the FIFO ready
// queue.  Must be called AFTER releasing the shard lock.
func (p *iNodePool) freeLater(indices []uint32) {
	now := time.Now().UnixNano()
	for _, idx := range indices {
		p.node(idx).freedAt = now
	}
	p.mu.Lock()
	p.ready = append(p.ready, indices...)
	// Compact the queue backing array when the dead head exceeds half capacity.
	if p.readyHd > len(p.ready)/2 {
		p.ready = append(p.ready[:0], p.ready[p.readyHd:]...)
		p.readyHd = 0
	}
	p.mu.Unlock()
}

// resetNode zeroes all slots, children, and h2word so a recycled node looks
// freshly allocated to the next writer.
func resetNode(n *iNode) {
	for i := range n.slots {
		n.slots[i].val.Store(0)
		n.slots[i].tag.Store(iEmpty)
	}
	for b := range n.children {
		atomic.StoreUint32(&n.children[b], iNullChild)
	}
	n.h2word.Store(0) // clear all four H2 uint16 groups
}

// ── shard ─────────────────────────────────────────────────────────────────────

// iShard is one independent trie shard.
// Padded to 64 bytes to avoid false sharing between adjacent shards.
type iShard struct {
	mu   sync.Mutex
	root uint32   // index of root node in the shared pool
	_    [56]byte // pad to 64 B
}

// ── InlineIndex ───────────────────────────────────────────────────────────────

// InlineIndex is a concurrent, multi-shard trie index with inline slots.
//
// Reads are fully lock-free.
// Writes (Set, Delete, Compact, DeepCompact) hold the per-shard mutex.
type InlineIndex struct {
	pool      iNodePool
	shards    []iShard
	shardMask uint64
	shardBits uint
}

// NewInlineIndex initialises the index.
// shardBits controls parallelism: numShards = 1 << shardBits.
// Pass 0 to use the default (8 → 256 shards).
func NewInlineIndex(shardBits uint) *InlineIndex {
	if shardBits == 0 {
		shardBits = iDefaultShardBits
	}
	if shardBits > iMaxShardBits {
		panic(fmt.Sprintf(
			"cache: NewInlineIndex: shardBits %d exceeds maximum %d",
			shardBits, iMaxShardBits,
		))
	}
	numShards := 1 << shardBits

	idx := &InlineIndex{
		shards:    make([]iShard, numShards),
		shardMask: uint64(numShards - 1),
		shardBits: shardBits,
	}

	// Reserve index 0 and pre-allocate root nodes.
	idx.pool.next.Store(1)
	idx.pool.mu.Lock()
	idx.pool.ensure(uint32(numShards) + 1)
	idx.pool.mu.Unlock()

	for s := range idx.shards {
		idx.shards[s].root = idx.pool.alloc()
	}
	return idx
}

// ── lock-free read ────────────────────────────────────────────────────────────

// Get returns the stored value for tag.  Fully lock-free.
//
// Swiss-style H2 fast path: at each node the h2word (on CL2, already fetched
// for child routing) is checked first.  If no 16-bit group matches the
// query's H2, the CL1 slot scan is skipped entirely — saving one cache miss
// per level for tags that merely pass through a node without matching any
// inline slot.
func (idx *InlineIndex) Get(tag uint64) (uint64, bool) {
	sh := &idx.shards[tag&idx.shardMask]
	nodeIdx := atomic.LoadUint32(&sh.root)
	shift := idx.shardBits
	queryH2 := h2ForTag(tag)

	for depth := 0; depth <= iMaxDepth; depth++ {
		if nodeIdx == iNullChild {
			return 0, false
		}
		node := idx.pool.node(nodeIdx)

		// H2 filter: if no packed uint16 group equals queryH2, skip slot scan.
		if h2AnyMatch(node.h2word.Load(), queryH2) {
			for i := range node.slots {
				t := node.slots[i].tag.Load()
				if t == tag {
					return node.slots[i].val.Load(), true
				}
			}
		}

		// Not found in inline slots; follow child for the next 3 tag bits.
		branch := uint32((tag >> shift) & iMask)
		nodeIdx = atomic.LoadUint32(&node.children[branch])
		shift += iBits
	}
	return 0, false
}

// GetTag is an alias for Get (tag-based interface for CacheManager).
func (idx *InlineIndex) GetTag(tag uint64) (uint64, bool) {
	return idx.Get(tag)
}

// ── write helpers (caller holds shard mu) ─────────────────────────────────────

// insertAt tries to write (tag, val) into node's inline slots.
// Returns true if successful (found empty or matching slot), false if all
// slots are occupied by different live entries.
//
// Write ordering for H2:
//
//	h2word updated BEFORE val+tag so any reader that observes the new tag
//	already observes the matching H2 group.
func insertAt(node *iNode, tag, val uint64) bool {
	h2 := h2ForTag(tag)
	var tomb *iSlot
	tombI := -1
	for i := range node.slots {
		s := &node.slots[i]
		t := s.tag.Load()
		switch t {
		case tag:
			// Update existing entry in-place.
			s.val.Store(val)
			return true
		case iEmpty:
			node.h2word.Store(h2Set(node.h2word.Load(), i, h2))
			s.val.Store(val)
			s.tag.Store(tag)
			return true
		case iTombstone:
			if tomb == nil {
				tomb = s
				tombI = i
			}
		}
	}
	if tomb != nil {
		node.h2word.Store(h2Set(node.h2word.Load(), tombI, h2))
		tomb.val.Store(val)
		tomb.tag.Store(tag)
		return true
	}
	return false
}

// upsert inserts or updates (tag, val) in the shard rooted at sh.root.
// Descends the trie, storing the entry at the shallowest available node.
// Creates child nodes as needed; silently drops at max depth.
func (idx *InlineIndex) upsert(sh *iShard, tag, val uint64) {
	nodeIdx := sh.root
	shift := idx.shardBits

	for depth := 0; depth <= iMaxDepth; depth++ {
		node := idx.pool.node(nodeIdx)

		if insertAt(node, tag, val) {
			return
		}

		// Node is full: navigate (or create) the child for the next 3 bits.
		branch := uint32((tag >> shift) & iMask)
		childIdx := atomic.LoadUint32(&node.children[branch])
		if childIdx == iNullChild {
			if depth == iMaxDepth {
				return // depth ceiling: silently drop
			}
			// Write the entry into the child node BEFORE publishing its index
			// to the parent.  A lock-free reader that sees the child index must
			// already find the entry there.
			childIdx = idx.pool.alloc()
			child := idx.pool.node(childIdx)
			insertAt(child, tag, val) // guaranteed to succeed: child is empty
			atomic.StoreUint32(&node.children[branch], childIdx)
			return
		}
		nodeIdx = childIdx
		shift += iBits
	}
}

// Set inserts or updates tag → val.
func (idx *InlineIndex) Set(tag, val uint64) {
	sh := &idx.shards[tag&idx.shardMask]
	sh.mu.Lock()
	idx.upsert(sh, tag, val)
	sh.mu.Unlock()
}

// SetTagGetPtr inserts tag→val and returns an xSlotPtr encoding the pre-computed
// shard and lower 39 bits of the tag (for use by the cleaner's TryTombstone).
// lane must be xPtrLaneTiny (0) or xPtrLaneHash (1<<47).
func (idx *InlineIndex) SetTagGetPtr(tag uint64, val uint64, lane uint64) xSlotPtr {
	sh := &idx.shards[tag&idx.shardMask]
	sh.mu.Lock()
	idx.upsert(sh, tag, val)
	sh.mu.Unlock()
	shard := uint8(tag & idx.shardMask)
	return packXTagRef(lane, shard, tag)
}

// Delete tombstones the entry for tag and attempts a shallow compaction upward.
// Returns true if the entry was found and tombstoned.
func (idx *InlineIndex) Delete(tag uint64) bool {
	sh := &idx.shards[tag&idx.shardMask]
	sh.mu.Lock()
	found, path := idx.findPath(sh, tag)
	var freed []uint32
	if found {
		// Compact upward: try to collapse the deepest parent first.
		for len(path) >= 2 {
			parentIdx := path[len(path)-2]
			ok, detached := idx.tryCollapse(parentIdx)
			if !ok {
				break
			}
			freed = append(freed, detached...)
			path = path[:len(path)-1]
		}
	}
	sh.mu.Unlock()
	// Schedule recycling AFTER lock release so grace period starts once the
	// detach is visible to all readers.
	if len(freed) > 0 {
		idx.pool.freeLater(freed)
	}
	return found
}

// DeleteTag is an alias for Delete (tag-based interface).
func (idx *InlineIndex) DeleteTag(tag uint64) bool {
	return idx.Delete(tag)
}

// TryTombstone performs a fresh trie traversal using the shard and tag routing
// bits stored in the tag parameter, then tombstones the slot whose val still
// references physOff/gen2b.
//
// Returns true when the tombstone is placed.
func (idx *InlineIndex) TryTombstone(tag uint64, physOff uint64, gen2b uint8) bool {
	sh := &idx.shards[tag&idx.shardMask]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	nodeIdx := sh.root
	shift := idx.shardBits
	for depth := 0; depth <= iMaxDepth; depth++ {
		if nodeIdx == iNullChild {
			return false
		}
		node := idx.pool.node(nodeIdx)
		for i := range node.slots {
			s := &node.slots[i]
			t := s.tag.Load()
			if t != tag {
				continue
			}
			v := s.val.Load()
			vGen2b, _, vTyp := Unpack(v)
			_, _, vPhysOff := UnpackSlab(v)
			if vTyp != xValTypeSlabRAM || vPhysOff != physOff || vGen2b != gen2b {
				continue
			}
			node.h2word.Store(h2Set(node.h2word.Load(), i, 0))
			s.val.Store(0)
			s.tag.Store(iTombstone)
			return true
		}
		branch := uint32((tag >> shift) & iMask)
		nodeIdx = atomic.LoadUint32(&node.children[branch])
		shift += iBits
	}
	return false
}

// findPath scans the trie for tag, tombstones it, and returns the node path.
func (idx *InlineIndex) findPath(sh *iShard, tag uint64) (bool, []uint32) {
	var path []uint32
	nodeIdx := sh.root
	shift := idx.shardBits

	for depth := 0; depth <= iMaxDepth; depth++ {
		if nodeIdx == iNullChild {
			return false, nil
		}
		path = append(path, nodeIdx)
		node := idx.pool.node(nodeIdx)

		for i := range node.slots {
			s := &node.slots[i]
			if s.tag.Load() == tag {
				// Clear H2 group BEFORE writing the tombstone.
				node.h2word.Store(h2Set(node.h2word.Load(), i, 0))
				s.val.Store(0)
				s.tag.Store(iTombstone)
				return true, path
			}
		}

		branch := uint32((tag >> shift) & iMask)
		nodeIdx = atomic.LoadUint32(&node.children[branch])
		shift += iBits
	}
	return false, nil
}

// tryCollapse checks whether ALL children of parentIdx together have ≤ iSlots
// live entries.  If so, pulls those entries into the parent's inline slots,
// detaches all children, and returns (true, detachedIndices).
func (idx *InlineIndex) tryCollapse(parentIdx uint32) (bool, []uint32) {
	parent := idx.pool.node(parentIdx)

	// Collect live entries from all children.
	type entry struct{ tag, val uint64 }
	var childLive [iSlots]entry
	childLiveN := 0

	for b := range parent.children {
		childIdx := atomic.LoadUint32(&parent.children[b])
		if childIdx == iNullChild {
			continue
		}
		child := idx.pool.node(childIdx)
		// If the child itself has grandchildren, refuse to collapse.
		for b2 := range child.children {
			if atomic.LoadUint32(&child.children[b2]) != iNullChild {
				return false, nil
			}
		}
		for i := range child.slots {
			t := child.slots[i].tag.Load()
			if t == iEmpty || t == iTombstone {
				continue
			}
			if childLiveN >= iSlots {
				return false, nil // too many entries
			}
			childLive[childLiveN] = entry{t, child.slots[i].val.Load()}
			childLiveN++
		}
	}

	// Count free slots in parent.
	freeSlots := 0
	for i := range parent.slots {
		t := parent.slots[i].tag.Load()
		if t == iEmpty || t == iTombstone {
			freeSlots++
		}
	}
	if freeSlots < childLiveN {
		return false, nil
	}

	// Write child entries into parent's free slots.
	wi := 0
	for i := range parent.slots {
		if wi >= childLiveN {
			break
		}
		s := &parent.slots[i]
		t := s.tag.Load()
		if t == iEmpty || t == iTombstone {
			h2 := h2ForTag(childLive[wi].tag)
			parent.h2word.Store(h2Set(parent.h2word.Load(), i, h2))
			s.val.Store(childLive[wi].val)
			s.tag.Store(childLive[wi].tag)
			wi++
		}
	}

	// Detach all children atomically; collect their indices for recycling.
	var detached []uint32
	for b := range parent.children {
		childIdx := atomic.LoadUint32(&parent.children[b])
		if childIdx != iNullChild {
			detached = append(detached, childIdx)
			atomic.StoreUint32(&parent.children[b], iNullChild)
		}
	}
	return true, detached
}

// ── compaction ────────────────────────────────────────────────────────────────

// CompactAll performs a shallow collapse on every shard.
func (idx *InlineIndex) CompactAll() {
	for s := range idx.shards {
		sh := &idx.shards[s]
		sh.mu.Lock()
		freed := idx.compactNode(sh.root)
		sh.mu.Unlock()
		if len(freed) > 0 {
			idx.pool.freeLater(freed)
		}
	}
}

// DeepCompactAll recursively compacts every shard from leaves to root.
func (idx *InlineIndex) DeepCompactAll() {
	for s := range idx.shards {
		sh := &idx.shards[s]
		sh.mu.Lock()
		freed := idx.deepCompactNode(sh.root)
		sh.mu.Unlock()
		if len(freed) > 0 {
			idx.pool.freeLater(freed)
		}
	}
}

func (idx *InlineIndex) compactNode(nodeIdx uint32) []uint32 {
	if nodeIdx == iNullChild {
		return nil
	}
	_, freed := idx.tryCollapse(nodeIdx)
	return freed
}

// deepCompactNode post-order traversal: compact children before parent.
func (idx *InlineIndex) deepCompactNode(nodeIdx uint32) []uint32 {
	if nodeIdx == iNullChild {
		return nil
	}
	node := idx.pool.node(nodeIdx)
	var freed []uint32
	for b := range node.children {
		childIdx := atomic.LoadUint32(&node.children[b])
		if childIdx != iNullChild {
			freed = append(freed, idx.deepCompactNode(childIdx)...)
		}
	}
	_, detached := idx.tryCollapse(nodeIdx)
	return append(freed, detached...)
}

// ── stats ─────────────────────────────────────────────────────────────────────

// Stats returns live-entry count and node count for the given shard index.
func (idx *InlineIndex) Stats(shardIdx int) (entries, nodes int) {
	sh := &idx.shards[shardIdx]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	idx.statsNode(sh.root, &entries, &nodes)
	return
}

func (idx *InlineIndex) statsNode(nodeIdx uint32, entries, nodes *int) {
	if nodeIdx == iNullChild {
		return
	}
	*nodes++
	node := idx.pool.node(nodeIdx)
	for i := range node.slots {
		t := node.slots[i].tag.Load()
		if t != iEmpty && t != iTombstone {
			*entries++
		}
	}
	for b := range node.children {
		idx.statsNode(atomic.LoadUint32(&node.children[b]), entries, nodes)
	}
}

// TotalNodes returns the number of live nodes in the pool.
func (idx *InlineIndex) TotalNodes() int {
	bumped := int(idx.pool.next.Load()) - 1 // subtract reserved index-0
	idx.pool.mu.Lock()
	inQueue := len(idx.pool.ready) - idx.pool.readyHd
	idx.pool.mu.Unlock()
	n := bumped - inQueue
	if n < 0 {
		return 0
	}
	return n
}

// NumShards returns the number of shards.
func (idx *InlineIndex) NumShards() int { return len(idx.shards) }
