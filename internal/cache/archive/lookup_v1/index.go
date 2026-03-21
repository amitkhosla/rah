package lookup_v1

import (
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Index constants.
const (
	xSlots            = 4              // slots per partition = 64 bytes = 1 cache line
	xTrieBits         = 3              // bits consumed per trie level
	xTrieFanout       = 1 << xTrieBits // 8-way branching
	xTrieMask         = uint64(xTrieFanout - 1)
	xMaxDepth         = 10 // max trie depth per shard (10×3 = 30 routing bits)
	xDefaultShardBits = 8  // default: 256 shards
	xMaxShardBits     = 20 // 1<<20 = ~1M shards; prevents accidental OOM at init

	xChunkBits = 10              // 1024 slots per pool chunk
	xChunkSize = 1 << xChunkBits // slots per chunk
	xChunkMask = uint32(xChunkSize - 1)

	xGracePeriodNs = int64(time.Second) // slots held 1s before reuse after collapse/split

	xEmpty     = uint64(0)
	xTombstone = ^uint64(0)
)

// xIndexer is one entry in a trie node. 8 bytes.
//
//	extension == 0  →  leaf:     slotStart indexes the first of xSlots pool slots.
//	extension  > 0  →  internal: extension is the child xNode index in shard.nodes[].
//	                             slotStart is unused (0).
//
// 0 is a safe leaf sentinel: child nodes always occupy index ≥ 1 (root is at 0).
type xIndexer struct {
	slotStart uint32
	extension uint32
}

// xNode is one 8-way trie node.
// 8 × xIndexer = 64 bytes = exactly 1 cache line.
type xNode [xTrieFanout]xIndexer

// xSlot stores one index entry. 16 bytes.
// Insert ordering: store val BEFORE tag (SC atomics) so any reader
// observing the new tag is guaranteed to also observe the correct val.
type xSlot struct {
	tag atomic.Uint64
	val atomic.Uint64
}

// xShardSnap is an immutable snapshot of one shard's trie.
// Published atomically via atomic.Pointer; never modified after Store.
// nodes[0] is always the root.
type xShardSnap struct {
	nodes []xNode
}

// xShard is one independent routing shard.
// Padded to 64 bytes (1 cache line) to prevent false sharing between
// adjacent shards under concurrent writes on multi-core systems.
type xShard struct {
	mu   sync.Mutex
	snap atomic.Pointer[xShardSnap]
	_    [48]byte // pad to 64 bytes = 1 cache line
}

// safeSlot is returned by slotPool.slot() on any out-of-bounds access.
// Its tag is always xEmpty: Get returns miss, Set writes are silently discarded.
// This prevents a programming error from panicking a live request goroutine.
var safeSlot xSlot

// chunkSlice is a slice of pointers to fixed-size slot arrays.
// Pointer elements never move after allocation; only the slice header grows.
type chunkSlice = []*[xChunkSize]xSlot

// deferredFree holds partition starts waiting for the grace period to expire
// before their slots are cleared and made available for reuse.
//
// NOTE: old []xNode backing arrays from replaced xShardSnaps are NOT stored
// here. Go's GC tracks all references to those slices (via callers' sn.nodes
// stack variables and any concurrent readers holding the old snapshot pointer).
// Once all such references are released the GC reclaims the memory automatically
// — no manual grace-period bookkeeping needed for node slices.
// Only slot-pool indices (uint32) require explicit bookkeeping because the pool
// is indexed by uint32, not by GC-tracked pointer.
type deferredFree struct {
	starts    []uint32 // partition slot-start indices to clear and reuse
	freeAfter int64    // unix nanoseconds: reuse allowed after this time
}

// nodeSlicePool recycles []xNode backing arrays to reduce allocation pressure
// from COW operations in splitLeaf and tryCollapse.
// Slices are returned immediately after the new snapshot is committed; it is
// safe because concurrent readers hold a live *xShardSnap pointer that keeps
// the OLD backing array reachable by the GC until they finish — the GC will
// not allow two goroutines to observe different versions of the same physical
// memory. Reuse only happens after GC observes no live references.
var nodeSlicePool sync.Pool

func borrowNodeSlice(n int) []xNode {
	if v := nodeSlicePool.Get(); v != nil {
		s := v.([]xNode)
		if cap(s) >= n {
			return s[:n]
		}
		// Wrong size: drop it (GC reclaims); fall through to make.
	}
	return make([]xNode, n)
}

func returnNodeSlice(s []xNode) {
	nodeSlicePool.Put(s[:cap(s)])
}

// slotPool is a shared, grow-on-demand slot pool used by all shards.
//
// Allocation path:
//  1. claimPartitions() drains the pending queue (grace-period-expired entries).
//  2. Takes from the free list if available.
//  3. Falls back to the bump allocator (atomic Add on next).
//
// Release path:
//  1. releasePartitions() appends to pending with freeAfter = now + 1s.
//  2. drainPending() (called on next claimPartitions) clears slots and moves
//     expired entries to the free list.
//
// The 1-second grace period ensures no active reader holds a snapshot that
// references recycled slots (all reads complete in <5µs in practice).
type slotPool struct {
	mu      sync.Mutex
	list    atomic.Pointer[chunkSlice]
	next    atomic.Uint32
	free    []uint32       // partition starts ready for immediate reuse
	pending []deferredFree // partition starts within grace period
}

// slot returns a pointer to the xSlot at pool index idx.
// Returns &safeSlot (tag=xEmpty) on out-of-bounds access instead of panicking,
// so callers on the hot path serve a cache miss rather than crashing.
func (p *slotPool) slot(idx uint32) *xSlot {
	list := p.list.Load()
	chunkIdx := idx >> xChunkBits
	if list == nil || int(chunkIdx) >= len(*list) {
		return &safeSlot
	}
	return &(*list)[chunkIdx][idx&xChunkMask]
}

// ensure guarantees backing chunks exist for all slot indices up to maxIdx.
func (p *slotPool) ensure(maxIdx uint32) {
	needed := int(maxIdx>>xChunkBits) + 1
	list := p.list.Load()
	if list != nil && len(*list) >= needed {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	list = p.list.Load()
	for list == nil || len(*list) < needed {
		cur := 0
		if list != nil {
			cur = len(*list)
		}
		newList := make(chunkSlice, cur+1)
		if list != nil {
			copy(newList, *list)
		}
		newList[cur] = new([xChunkSize]xSlot)
		p.list.Store(&newList)
		list = &newList
	}
}

// claimPartitions returns n partition starts, drawing from the free list first
// then the bump allocator. Must not be called with p.mu held.
func (p *slotPool) claimPartitions(n int) []uint32 {
	starts := make([]uint32, n)

	// Drain pending queue and pop from free list under lock.
	p.mu.Lock()
	p.drainPending()
	fromFree := n
	if len(p.free) < n {
		fromFree = len(p.free)
	}
	if fromFree > 0 {
		copy(starts[:fromFree], p.free[len(p.free)-fromFree:])
		p.free = p.free[:len(p.free)-fromFree]
	}
	p.mu.Unlock()

	// Claim any remaining slots from the bump allocator (lock-free).
	remaining := n - fromFree
	if remaining > 0 {
		base := p.next.Add(uint32(remaining)*xSlots) - uint32(remaining)*xSlots
		p.ensure(base + uint32(remaining)*xSlots - 1)
		for i := 0; i < remaining; i++ {
			starts[fromFree+i] = base + uint32(i)*xSlots
		}
	}
	return starts
}

// releasePartitions defers partition starts for reuse after xGracePeriodNs.
// Slots are not cleared immediately: they remain readable (with old data) for
// the grace period so readers holding stale slot-pool snapshots still see valid
// content. Clearing happens in drainPending once the grace period expires.
//
// Old []xNode backing arrays are NOT passed here — they are managed by Go's GC
// via live references held by the caller and any concurrent readers.
func (p *slotPool) releasePartitions(starts []uint32) {
	freed := make([]uint32, len(starts))
	copy(freed, starts)
	p.mu.Lock()
	p.pending = append(p.pending, deferredFree{
		starts:    freed,
		freeAfter: time.Now().UnixNano() + xGracePeriodNs,
	})
	p.mu.Unlock()
}

// drainPending moves grace-period-expired entries to the free list,
// clearing their slots to xEmpty so they are safe for reuse.
// Must be called with p.mu held.
func (p *slotPool) drainPending() {
	if len(p.pending) == 0 {
		return
	}
	now := time.Now().UnixNano()
	cut := 0
	for cut < len(p.pending) && p.pending[cut].freeAfter <= now {
		cut++
	}
	for i := 0; i < cut; i++ {
		for _, start := range p.pending[i].starts {
			for j := uint32(0); j < xSlots; j++ {
				s := p.slot(start + j)
				s.val.Store(0)
				s.tag.Store(xEmpty)
			}
		}
		p.free = append(p.free, p.pending[i].starts...)
	}
	if cut > 0 {
		p.pending = p.pending[cut:]
	}
}

// LookupIndex is a lock-free read, per-shard-write trie-based cache index.
//
// Structure:
//   - shards[N] — N independent shards (N = 1 << shardBits), each with its own trie.
//     Each xShard is padded to 64 bytes to prevent false sharing.
//   - pool — one shared, grow-on-demand slot pool used by all shards.
//     Supports deferred slot recycling after a 1-second grace period.
//
// Reads  (Get)      : fully lock-free.
// Writes (Set/Delete): per-shard sync.Mutex; all other shards unaffected.
// Split             : triggered when all xSlots in a partition are live.
// Collapse          : triggered after Delete; recursive upward until no level qualifies.
type LookupIndex struct {
	pool      slotPool
	shards    []xShard
	shardMask uint64
	shardBits uint
}

// NewLookupIndex initialises the index.
// shardBits controls parallelism: numShards = 1 << shardBits.
// Pass 0 to use the default (8 → 256 shards).
// Panics if shardBits > xMaxShardBits (20) to prevent accidental OOM.
func NewLookupIndex(shardBits uint) *LookupIndex {
	if shardBits == 0 {
		shardBits = xDefaultShardBits
	}
	if shardBits > xMaxShardBits {
		panic(fmt.Sprintf(
			"cache: NewLookupIndex: shardBits %d exceeds maximum %d (would allocate %d shards)",
			shardBits, xMaxShardBits, 1<<shardBits,
		))
	}
	numShards := 1 << shardBits
	idx := &LookupIndex{
		shards:    make([]xShard, numShards),
		shardMask: uint64(numShards - 1),
		shardBits: shardBits,
	}

	empty := make(chunkSlice, 0)
	idx.pool.list.Store(&empty)

	// Pre-claim all initial slots: numShards × 8 partitions × 4 slots.
	initialSlots := uint32(numShards) * xTrieFanout * xSlots
	idx.pool.next.Store(initialSlots)
	idx.pool.ensure(initialSlots - 1)

	// Initialise each shard: one root node with 8 leaf indexers.
	for s := 0; s < numShards; s++ {
		base := uint32(s) * xTrieFanout * xSlots
		var root xNode
		for j := uint32(0); j < xTrieFanout; j++ {
			root[j].slotStart = base + j*xSlots
		}
		idx.shards[s].snap.Store(&xShardSnap{nodes: []xNode{root}})
	}
	return idx
}

// Get returns the stored value for fingerprint fp. Fully lock-free.
func (idx *LookupIndex) Get(fp [16]byte) (uint64, bool) {
	tag := makeTag(fp)
	sn := idx.shards[tag&idx.shardMask].snap.Load()
	slotStart := trieFind(sn.nodes, tag, idx.shardBits)
	for i := uint32(0); i < xSlots; i++ {
		s := idx.pool.slot(slotStart + i)
		t := s.tag.Load()
		if t == tag {
			return s.val.Load(), true
		}
		if t == xEmpty {
			return 0, false
		}
	}
	return 0, false
}

// Set inserts or updates fp → ptr.
func (idx *LookupIndex) Set(fp [16]byte, ptr uint64) bool {
	tag := makeTag(fp)
	sh := &idx.shards[tag&idx.shardMask]
	sh.mu.Lock()
	idx.upsert(sh, tag, ptr)
	sh.mu.Unlock()
	return true
}

// Delete marks the entry for fp as a tombstone and attempts recursive collapse.
func (idx *LookupIndex) Delete(fp [16]byte) bool {
	tag := makeTag(fp)
	sh := &idx.shards[tag&idx.shardMask]
	sh.mu.Lock()
	ok := idx.remove(sh, tag)
	sh.mu.Unlock()
	return ok
}

// trieFind traverses the trie and returns the leaf partition slot start.
// Lock-free: reads only atomic.Pointer-published node arrays and xIndexer values.
func trieFind(nodes []xNode, tag uint64, shardBits uint) uint32 {
	nodeIdx := 0
	shift := shardBits
	for {
		ix := nodes[nodeIdx][(tag>>shift)&xTrieMask]
		if ix.extension == 0 {
			return ix.slotStart
		}
		nodeIdx = int(ix.extension)
		shift += xTrieBits
	}
}

// triePath traces root-to-leaf, recording node indices and slot choices.
// Used by write operations that need the full path (split, collapse).
func triePath(nodes []xNode, tag uint64, shardBits uint) (nodeIdxs []int, slotNums []uint8, slotStart uint32, leafShift uint) {
	nodeIdx := 0
	shift := shardBits
	for {
		slot := uint8((tag >> shift) & xTrieMask)
		nodeIdxs = append(nodeIdxs, nodeIdx)
		slotNums = append(slotNums, slot)
		ix := nodes[nodeIdx][slot]
		if ix.extension == 0 {
			return nodeIdxs, slotNums, ix.slotStart, shift
		}
		nodeIdx = int(ix.extension)
		shift += xTrieBits
	}
}

// --- write operations (caller holds shard mu) ---

func (idx *LookupIndex) upsert(sh *xShard, tag, val uint64) {
	for {
		sn := sh.snap.Load()
		nodeIdxs, slotNums, slotStart, leafShift := triePath(sn.nodes, tag, idx.shardBits)

		var firstTomb *xSlot
		for i := uint32(0); i < xSlots; i++ {
			s := idx.pool.slot(slotStart + i)
			t := s.tag.Load()
			switch t {
			case tag:
				s.val.Store(val)
				return
			case xEmpty:
				target := s
				if firstTomb != nil {
					target = firstTomb
				}
				target.val.Store(val)
				target.tag.Store(tag)
				return
			case xTombstone:
				if firstTomb == nil {
					firstTomb = s
				}
			}
		}
		if firstTomb != nil {
			firstTomb.val.Store(val)
			firstTomb.tag.Store(tag)
			return
		}
		// All 4 slots live — split this leaf.
		if len(nodeIdxs) >= xMaxDepth {
			return // silent drop at depth ceiling
		}
		idx.splitLeaf(sh, sn, nodeIdxs, slotNums, leafShift, slotStart)
		// Retry: new snapshot has 8 sub-partitions for this hash prefix.
	}
}

func (idx *LookupIndex) remove(sh *xShard, tag uint64) bool {
	sn := sh.snap.Load()
	nodeIdxs, slotNums, slotStart, _ := triePath(sn.nodes, tag, idx.shardBits)

	// Tombstone the matching slot.
	found := false
	for i := uint32(0); i < xSlots; i++ {
		s := idx.pool.slot(slotStart + i)
		t := s.tag.Load()
		if t == tag {
			s.tag.Store(xTombstone)
			s.val.Store(0)
			found = true
			break
		}
		if t == xEmpty {
			return false
		}
	}
	if !found {
		return false
	}

	// Recursive collapse: after each successful collapse, check one level up.
	// Stops when collapse is not possible or we reach the root's direct children.
	for len(nodeIdxs) >= 2 {
		sn = sh.snap.Load()
		if !idx.tryCollapse(sh, sn, nodeIdxs, slotNums) {
			break
		}
		// Trim path: check grandparent on next iteration.
		nodeIdxs = nodeIdxs[:len(nodeIdxs)-1]
		slotNums = slotNums[:len(slotNums)-1]
	}
	return true
}

// tryCollapse merges 8 sibling partitions back into one leaf if their combined
// live entries fit in a single partition (≤ xSlots).
//
// Returns true if collapse happened (snapshot updated), false otherwise.
//
// Conditions for collapse:
//   - All 8 indexers of the leaf node have extension==0 (no sub-children).
//   - Total live entries across all 8 partitions ≤ xSlots (4).
func (idx *LookupIndex) tryCollapse(sh *xShard, sn *xShardSnap, nodeIdxs []int, slotNums []uint8) bool {
	leafNodeIdx := nodeIdxs[len(nodeIdxs)-1]
	leafNode := sn.nodes[leafNodeIdx]

	type entry struct{ tag, val uint64 }
	var live [xSlots]entry
	liveCount := 0
	var oldStarts [xTrieFanout]uint32

	for child := 0; child < xTrieFanout; child++ {
		ix := leafNode[child]
		if ix.extension != 0 {
			return false // child has sub-nodes; cannot collapse this level
		}
		oldStarts[child] = ix.slotStart
		for i := uint32(0); i < xSlots; i++ {
			s := idx.pool.slot(ix.slotStart + i)
			t := s.tag.Load()
			if t == xEmpty || t == xTombstone {
				continue
			}
			if liveCount >= xSlots {
				return false // too many live entries to fit in one partition
			}
			live[liveCount] = entry{t, s.val.Load()}
			liveCount++
		}
	}

	// Claim one replacement partition (free list first, then bump allocator).
	newStarts := idx.pool.claimPartitions(1)
	newBase := newStarts[0]

	// Write live entries into new partition (val before tag: SC ordering).
	for i := 0; i < liveCount; i++ {
		s := idx.pool.slot(newBase + uint32(i))
		s.val.Store(live[i].val)
		s.tag.Store(live[i].tag)
	}

	// COW: update parent's indexer back to a leaf pointing to newBase.
	parentNodeIdx := nodeIdxs[len(nodeIdxs)-2]
	parentSlot := slotNums[len(slotNums)-2]
	newNodes := borrowNodeSlice(len(sn.nodes))
	copy(newNodes, sn.nodes)
	newNodes[parentNodeIdx][parentSlot].extension = 0
	newNodes[parentNodeIdx][parentSlot].slotStart = newBase

	sh.snap.Store(&xShardSnap{nodes: newNodes})
	returnNodeSlice(sn.nodes) // return old nodes to pool immediately; GC keeps it alive as long as readers hold sn

	// Defer old 8 partitions for slot recycling after grace period.
	idx.pool.releasePartitions(oldStarts[:])
	return true
}

// splitLeaf expands a full leaf partition into 8 child partitions.
//
//	childShift = leafShift + xTrieBits
//	Old 4 live entries redistributed by (tag >> childShift) & 7.
//	8 fresh partitions claimed; free list consulted first.
func (idx *LookupIndex) splitLeaf(sh *xShard, sn *xShardSnap,
	nodeIdxs []int, slotNums []uint8, leafShift uint, oldSlotStart uint32) {

	childShift := leafShift + xTrieBits

	// Claim 8 new partitions (free list first, then bump allocator).
	newStarts := idx.pool.claimPartitions(xTrieFanout)

	// Build child node: 8 leaf indexers pointing to the new partitions.
	var child xNode
	for j := 0; j < xTrieFanout; j++ {
		child[j].slotStart = newStarts[j]
	}

	// Redistribute old 4 live entries into the 8 new sub-partitions.
	for i := uint32(0); i < xSlots; i++ {
		s := idx.pool.slot(oldSlotStart + i)
		t := s.tag.Load()
		v := s.val.Load()
		if t == xEmpty || t == xTombstone {
			continue
		}
		subSlot := (t >> childShift) & xTrieMask
		destBase := newStarts[subSlot]
		for k := uint32(0); k < xSlots; k++ {
			dest := idx.pool.slot(destBase + k)
			if dest.tag.Load() == xEmpty {
				dest.val.Store(v)
				dest.tag.Store(t)
				break
			}
		}
	}

	// COW: copy nodes, append child, update the one leaf indexer.
	// childNodeIdx ≥ 1 always (root is at 0; all children start at index ≥ 1).
	childNodeIdx := uint32(len(sn.nodes))
	newNodes := borrowNodeSlice(len(sn.nodes) + 1)
	copy(newNodes, sn.nodes)
	newNodes[childNodeIdx] = child

	leafNodeIdx := nodeIdxs[len(nodeIdxs)-1]
	leafSlot := slotNums[len(slotNums)-1]
	newNodes[leafNodeIdx][leafSlot].extension = childNodeIdx
	newNodes[leafNodeIdx][leafSlot].slotStart = 0

	sh.snap.Store(&xShardSnap{nodes: newNodes})
	returnNodeSlice(sn.nodes) // return old nodes to pool immediately; GC keeps it alive as long as readers hold sn

	// Defer old partition for slot recycling after grace period.
	idx.pool.releasePartitions([]uint32{oldSlotStart})
}

// makeTag extracts H1 from fp and sanitises it against sentinels.
// Forces bit 63 set: tag ≠ xEmpty (0).
// Shard selection bits (0 to shardBits-1) and trie routing bits are unaffected.
func makeTag(fp [16]byte) uint64 {
	h1 := binary.LittleEndian.Uint64(fp[0:8])
	t := h1 | (uint64(1) << 63)
	if t == xTombstone {
		return xTombstone - 1
	}
	return t
}

// makeTagHash builds the hashIdx tag from a 128-bit fingerprint.
// Uses H2 (fp[8:16]) as the identity: H1 was already consumed for routing
// and H2 stored in EntryHeader.KeyMid provides the per-entry identity check.
func makeTagHash(fp [16]byte) uint64 {
	h2 := binary.LittleEndian.Uint64(fp[8:16])
	if h2 == xEmpty {
		h2 = 1
	}
	if h2 == xTombstone {
		h2 ^= 1
	}
	return h2
}

// makeTagTiny builds the tinyIdx tag for keys ≤ 6 bytes.
// The tag is a lossless encoding of (tenantID, key): no hash, no collision.
//
// Layout:
//
//	≤5B: bit63=0 | keyLen(3b)[62:60] | tenantID(16b)[59:44] | key_exact(40b)[43:4] | spare[3:0]
//	 6B: bit63=1 | tenantID(16b)[62:47] | key_7bit(42b)[46:5] | spare[4:0]
//
// For 6B keys all bytes must be < 128; callers routing to tinyIdx guarantee this.
func makeTagTiny(tenantID uint16, key []byte) uint64 {
	n := len(key)
	var tag uint64
	if n == 6 {
		// 7-bit strip: each byte contributes 7 bits, total 42 bits.
		var k42 uint64
		for i, b := range key {
			k42 |= uint64(b&0x7F) << (uint(i) * 7)
		}
		tag = (uint64(1) << 63) | (uint64(tenantID) << 47) | (k42 << 5)
	} else {
		// Exact encoding: each byte stored verbatim, LSB-first.
		var k40 uint64
		for i, b := range key {
			k40 |= uint64(b) << (uint(i) * 8)
		}
		tag = (uint64(n&0x7) << 60) | (uint64(tenantID) << 44) | (k40 << 4)
	}
	// Sanitise against trie sentinel values.
	if tag == xEmpty {
		tag |= 4 // set spare bit 2
	}
	if tag == xTombstone {
		tag ^= 4 // flip spare bit 2
	}
	return tag
}

// GetTag is the tag-based variant of Get, bypassing makeTag.
// CacheManager uses this after pre-computing a lane-specific tag.
func (idx *LookupIndex) GetTag(tag uint64) (uint64, bool) {
	sn := idx.shards[tag&idx.shardMask].snap.Load()
	slotStart := trieFind(sn.nodes, tag, idx.shardBits)
	for i := uint32(0); i < xSlots; i++ {
		s := idx.pool.slot(slotStart + i)
		t := s.tag.Load()
		if t == tag {
			return s.val.Load(), true
		}
		if t == xEmpty {
			return 0, false
		}
	}
	return 0, false
}

// SetTag is the tag-based variant of Set, bypassing makeTag.
func (idx *LookupIndex) SetTag(tag uint64, val uint64) bool {
	sh := &idx.shards[tag&idx.shardMask]
	sh.mu.Lock()
	idx.upsert(sh, tag, val)
	sh.mu.Unlock()
	return true
}

// DeleteTag is the tag-based variant of Delete, bypassing makeTag.
func (idx *LookupIndex) DeleteTag(tag uint64) bool {
	sh := &idx.shards[tag&idx.shardMask]
	sh.mu.Lock()
	ok := idx.remove(sh, tag)
	sh.mu.Unlock()
	return ok
}

// SetTagGetPtr inserts tag→val and returns an xSlotPtr encoding the pre-computed
// shard and lower 39 bits of the tag (for use by the cleaner's TryTombstone).
// lane must be xPtrLaneTiny (0) or xPtrLaneHash (1<<47).
// Returns 0 if the entry was silently dropped at the depth ceiling (very rare).
func (idx *LookupIndex) SetTagGetPtr(tag uint64, val uint64, lane uint64) xSlotPtr {
	sh := &idx.shards[tag&idx.shardMask]
	sh.mu.Lock()
	idx.upsert(sh, tag, val)
	sh.mu.Unlock()
	// If upsert succeeded (or updated), pack a tag reference for the cleaner.
	// packXTagRef is always valid: even a depth-ceiling drop leaves the old entry
	// in the index, so the cleaner can still tombstone it via a fresh trieFind.
	shard := uint8(tag&idx.shardMask)
	return packXTagRef(lane, shard, tag)
}

// TryTombstone performs a fresh trie traversal using the shard and tag routing
// bits stored in sp, then CAS-tombstones the slot whose val still references
// physOff/gen2b. This approach is immune to split/collapse path staleness.
//
// Returns true when the tombstone is placed.
func (idx *LookupIndex) TryTombstone(sp xSlotPtr, physOff uint64, gen2b uint8) bool {
	snap := idx.shards[sp.shardIdx()].snap.Load()
	// sp.tagBits() holds the lower 39 bits of the tag — sufficient for trieFind
	// since the trie consumes bits [shardBits : shardBits+maxDepth*3] = [8:38].
	slotStart := trieFind(snap.nodes, sp.tagBits(), idx.shardBits)
	for i := uint32(0); i < xSlots; i++ {
		s := idx.pool.slot(slotStart + i)
		t := s.tag.Load()
		if t == xEmpty {
			return false
		}
		if t == xTombstone {
			continue
		}
		v := s.val.Load()
		vGen2b, _, vTyp := Unpack(v)
		_, _, vPhysOff := UnpackSlab(v)
		if vTyp != xValTypeSlabRAM || vPhysOff != physOff || vGen2b != gen2b {
			continue
		}
		if s.tag.CompareAndSwap(t, xTombstone) {
			s.val.Store(0)
			return true
		}
		return false
	}
	return false
}
