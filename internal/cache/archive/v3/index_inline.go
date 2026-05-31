/*
Package icache3 implements a concurrent trie index with three-word pre-filter
metadata per node (h2word, klenWord, tenantWord) and a 24-byte EntryHeader
with a 3-byte KeyFP fingerprint in the slab.

Compared to icache2:
  - iNode gains klenWord (packed key lengths) and tenantWord (packed tenant IDs).
  - GetTag takes keyLen and tenantID for triple-mask candidate filtering.
  - SetTagGetPtr takes keyLen and tenantID to maintain klenWord/tenantWord.
  - EntryHeader is 24 bytes (KeyFP replaces single KeyMid byte).
  - Region.Write/Read use [3]byte keyFP instead of uint8 keyMid.

Triple-mask filtering reduces false positives on slot scans: a slot is only
visited when its H2, key length, AND tenant ID all match the query.
*/
package v3

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	iSlots    = 4
	iFanout   = 8
	iBits     = 3
	iMask     = uint64(iFanout - 1)
	iMaxDepth = 10

	iDefaultShardBits = 8
	iMaxShardBits     = 20

	iEmpty     = uint64(0)
	iTombstone = ^uint64(0)

	iNullChild = uint32(0)

	iChunkBits = 10
	iChunkSize = 1 << iChunkBits
	iChunkMask = uint32(iChunkSize - 1)

	iGracePeriodNs = int64(10_000_000)
)

// ── node layout ───────────────────────────────────────────────────────────────

// iSlot is one inline index entry.  16 bytes.
type iSlot struct {
	tag atomic.Uint64
	val atomic.Uint64
}

// iNode is a trie node with inline key-value slots AND child pointers.
//
// Memory layout (128 bytes = 2 × 64-byte cache lines):
//
//	Cache line 1 (bytes   0–63): 4 inline iSlots — tag+val pairs.
//	Cache line 2 (bytes  64–95): 8 child node indices (uint32 × 8 = 32 B).
//	             (bytes  96–103): freedAt — unix-ns recycle timestamp.
//	             (bytes 104–111): h2word — packed H2 uint16s per slot.
//	             (bytes 112–119): klenWord — packed KeyLen uint16s per slot.
//	             (bytes 120–127): tenantWord — packed TenantID uint16s per slot.
type iNode struct {
	// Cache line 1: inline key-value slots.
	slots [iSlots]iSlot // 4 × 16 B = 64 B

	// Cache line 2: child pointers + recycle timestamp + pre-filter metadata.
	children   [iFanout]uint32 // 8 × 4 B = 32 B  (offset  64)
	freedAt    int64           // 8 B              (offset  96)
	h2word     atomic.Uint64  // 8 B              (offset 104) — packed H2 uint16s
	klenWord   atomic.Uint64  // 8 B              (offset 112) — packed KeyLen uint16s
	tenantWord atomic.Uint64  // 8 B              (offset 120) — packed TenantID uint16s
}

// h2ForTag derives the H2 uint16 for a tag using the upper 16 bits (bits 48–63).
// Forced to ≥1 so that 0 remains an unambiguous "empty" sentinel in h2word.
func h2ForTag(tag uint64) uint16 {
	h := uint16(tag >> 48)
	if h == 0 {
		h = 1
	}
	return h
}

// h2Set returns a new h2word with the 16-bit group at position i (0–3) set to v.
func h2Set(word uint64, i int, v uint16) uint64 {
	shift := uint(i * 16)
	return (word &^ (0xFFFF << shift)) | (uint64(v) << shift)
}

// h2MatchMask returns a 4-bit mask: bit i set if slot i's stored H2 == queryH2.
func h2MatchMask(word uint64, queryH2 uint16) uint8 {
	x := word ^ (uint64(queryH2) * 0x0001000100010001)
	z := (x-0x0001000100010001) &^ x & 0x8000800080008000
	return uint8((z>>15)&1 | (z>>30)&2 | (z>>45)&4 | (z>>60)&8)
}

// klenSet returns klenWord with the uint16 at position i (0–3) set to v.
func klenSet(word uint64, i int, v uint16) uint64 {
	shift := uint(i * 16)
	return (word &^ (0xFFFF << shift)) | (uint64(v) << shift)
}

// klenMatchMask returns a 4-bit mask: bit i set if slot i's stored KeyLen == queryLen.
func klenMatchMask(word uint64, queryLen uint16) uint8 {
	x := word ^ (uint64(queryLen) * 0x0001000100010001)
	z := (x-0x0001000100010001) &^ x & 0x8000800080008000
	return uint8((z>>15)&1 | (z>>30)&2 | (z>>45)&4 | (z>>60)&8)
}

// tenantSet returns tenantWord with the uint16 at position i (0–3) set to v.
func tenantSet(word uint64, i int, v uint16) uint64 {
	shift := uint(i * 16)
	return (word &^ (0xFFFF << shift)) | (uint64(v) << shift)
}

// tenantMatchMask returns a 4-bit mask: bit i set if slot i's stored TenantID == queryTenant.
func tenantMatchMask(word uint64, queryTenant uint16) uint8 {
	x := word ^ (uint64(queryTenant) * 0x0001000100010001)
	z := (x-0x0001000100010001) &^ x & 0x8000800080008000
	return uint8((z>>15)&1 | (z>>30)&2 | (z>>45)&4 | (z>>60)&8)
}

// ── node pool (chunked, stable pointers) ──────────────────────────────────────

type iChunkSlice = []*[iChunkSize]iNode

type iNodePool struct {
	mu      sync.Mutex
	list    atomic.Pointer[iChunkSlice]
	next    atomic.Uint32
	ready   []uint32
	readyHd int
}

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
		idx = p.next.Add(1) - 1
	}
	p.mu.Lock()
	p.ensure(idx)
	p.mu.Unlock()
	return idx
}

func (p *iNodePool) node(idx uint32) *iNode {
	list := p.list.Load()
	return &(*list)[idx>>iChunkBits][idx&iChunkMask]
}

func (p *iNodePool) freeLater(indices []uint32) {
	now := time.Now().UnixNano()
	for _, idx := range indices {
		p.node(idx).freedAt = now
	}
	p.mu.Lock()
	p.ready = append(p.ready, indices...)
	if p.readyHd > len(p.ready)/2 {
		p.ready = append(p.ready[:0], p.ready[p.readyHd:]...)
		p.readyHd = 0
	}
	p.mu.Unlock()
}

// resetNode zeroes all slots, children, h2word, klenWord, and tenantWord.
func resetNode(n *iNode) {
	for i := range n.slots {
		n.slots[i].val.Store(0)
		n.slots[i].tag.Store(iEmpty)
	}
	for b := range n.children {
		atomic.StoreUint32(&n.children[b], iNullChild)
	}
	n.h2word.Store(0)
	n.klenWord.Store(0)
	n.tenantWord.Store(0)
}

// ── shard ─────────────────────────────────────────────────────────────────────

type iShard struct {
	mu   sync.Mutex
	root uint32
	_    [56]byte
}

// ── InlineIndex ───────────────────────────────────────────────────────────────

// InlineIndex is a concurrent, multi-shard trie index with inline slots.
// It uses three pre-filter words per node: h2word, klenWord, tenantWord.
//
// Reads are fully lock-free.
// Writes hold the per-shard mutex.
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
			"icache3: NewInlineIndex: shardBits %d exceeds maximum %d",
			shardBits, iMaxShardBits,
		))
	}
	numShards := 1 << shardBits

	idx := &InlineIndex{
		shards:    make([]iShard, numShards),
		shardMask: uint64(numShards - 1),
		shardBits: shardBits,
	}

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

// GetTag returns the stored value for tag with triple-mask pre-filtering.
// keyLen and tenantID are used to filter candidate slots before comparing tags.
// For tinyIdx pass keyLen=0 and tenantID=0 (lossless tag — no pre-filter needed).
func (idx *InlineIndex) GetTag(tag uint64, keyLen uint16, tenantID uint16) (uint64, bool) {
	sh := &idx.shards[tag&idx.shardMask]
	nodeIdx := atomic.LoadUint32(&sh.root)
	shift := idx.shardBits
	queryH2 := h2ForTag(tag)

	for depth := 0; depth <= iMaxDepth; depth++ {
		if nodeIdx == iNullChild {
			return 0, false
		}
		node := idx.pool.node(nodeIdx)

		// Triple-mask pre-filter: load all three words from CL2.
		h2mask := h2MatchMask(node.h2word.Load(), queryH2)
		var candidates uint8
		if keyLen == 0 && tenantID == 0 {
			// tinyIdx path: no klen/tenant filter (lossless tag, any match is valid).
			candidates = h2mask
		} else {
			klenMask := klenMatchMask(node.klenWord.Load(), keyLen)
			tenantMask := tenantMatchMask(node.tenantWord.Load(), tenantID)
			candidates = h2mask & klenMask & tenantMask
		}

		if candidates != 0 {
			// Scan only candidate slots for H1 match.
			for i := 0; i < iSlots; i++ {
				if candidates&(1<<i) == 0 {
					continue
				}
				if node.slots[i].tag.Load() == tag {
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

// ── write helpers (caller holds shard mu) ─────────────────────────────────────

// insertAt tries to write (tag, val, keyLen, tenantID) into node's inline slots.
// Returns true if successful.
func insertAt(node *iNode, tag, val uint64, keyLen uint16, tenantID uint16) bool {
	h2 := h2ForTag(tag)
	var tomb *iSlot
	tombI := -1
	for i := range node.slots {
		s := &node.slots[i]
		t := s.tag.Load()
		switch t {
		case tag:
			// Update existing entry in-place (val only; metadata unchanged).
			s.val.Store(val)
			return true
		case iEmpty:
			node.h2word.Store(h2Set(node.h2word.Load(), i, h2))
			node.klenWord.Store(klenSet(node.klenWord.Load(), i, keyLen))
			node.tenantWord.Store(tenantSet(node.tenantWord.Load(), i, tenantID))
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
		node.klenWord.Store(klenSet(node.klenWord.Load(), tombI, keyLen))
		node.tenantWord.Store(tenantSet(node.tenantWord.Load(), tombI, tenantID))
		tomb.val.Store(val)
		tomb.tag.Store(tag)
		return true
	}
	return false
}

// upsert inserts or updates (tag, val) in the shard rooted at sh.root.
func (idx *InlineIndex) upsert(sh *iShard, tag, val uint64, keyLen uint16, tenantID uint16) {
	nodeIdx := sh.root
	shift := idx.shardBits

	for depth := 0; depth <= iMaxDepth; depth++ {
		node := idx.pool.node(nodeIdx)

		if insertAt(node, tag, val, keyLen, tenantID) {
			return
		}

		branch := uint32((tag >> shift) & iMask)
		childIdx := atomic.LoadUint32(&node.children[branch])
		if childIdx == iNullChild {
			if depth == iMaxDepth {
				return
			}
			childIdx = idx.pool.alloc()
			child := idx.pool.node(childIdx)
			insertAt(child, tag, val, keyLen, tenantID)
			atomic.StoreUint32(&node.children[branch], childIdx)
			return
		}
		nodeIdx = childIdx
		shift += iBits
	}
}

// Set inserts or updates tag → val (no keyLen/tenantID metadata; use for tinyIdx).
func (idx *InlineIndex) Set(tag, val uint64) {
	sh := &idx.shards[tag&idx.shardMask]
	sh.mu.Lock()
	idx.upsert(sh, tag, val, 0, 0)
	sh.mu.Unlock()
}

// SetTagGetPtr inserts tag→val with keyLen/tenantID metadata and returns xSlotPtr.
// lane must be xPtrLaneTiny (0) or xPtrLaneHash (1<<47).
// For tinyIdx pass keyLen=0, tenantID=0.
func (idx *InlineIndex) SetTagGetPtr(tag uint64, val uint64, lane uint64, keyLen uint16, tenantID uint16) xSlotPtr {
	sh := &idx.shards[tag&idx.shardMask]
	sh.mu.Lock()
	idx.upsert(sh, tag, val, keyLen, tenantID)
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
	if len(freed) > 0 {
		idx.pool.freeLater(freed)
	}
	return found
}

// DeleteTag is an alias for Delete (tag-based interface).
func (idx *InlineIndex) DeleteTag(tag uint64) bool {
	return idx.Delete(tag)
}

// TryTombstone performs a fresh trie traversal and tombstones the slot whose
// val still references physOff/gen2b.
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
			node.klenWord.Store(klenSet(node.klenWord.Load(), i, 0))
			node.tenantWord.Store(tenantSet(node.tenantWord.Load(), i, 0))
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
				node.h2word.Store(h2Set(node.h2word.Load(), i, 0))
				node.klenWord.Store(klenSet(node.klenWord.Load(), i, 0))
				node.tenantWord.Store(tenantSet(node.tenantWord.Load(), i, 0))
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
// live entries. If so, pulls them into the parent's inline slots.
func (idx *InlineIndex) tryCollapse(parentIdx uint32) (bool, []uint32) {
	parent := idx.pool.node(parentIdx)

	type entry struct {
		tag      uint64
		val      uint64
		keyLen   uint16
		tenantID uint16
	}
	var childLive [iSlots]entry
	childLiveN := 0

	for b := range parent.children {
		childIdx := atomic.LoadUint32(&parent.children[b])
		if childIdx == iNullChild {
			continue
		}
		child := idx.pool.node(childIdx)
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
				return false, nil
			}
			// Read keyLen and tenantID from child's metadata words.
			kw := child.klenWord.Load()
			tw := child.tenantWord.Load()
			kLen := uint16((kw >> uint(i*16)) & 0xFFFF)
			tID := uint16((tw >> uint(i*16)) & 0xFFFF)
			childLive[childLiveN] = entry{t, child.slots[i].val.Load(), kLen, tID}
			childLiveN++
		}
	}

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
			parent.klenWord.Store(klenSet(parent.klenWord.Load(), i, childLive[wi].keyLen))
			parent.tenantWord.Store(tenantSet(parent.tenantWord.Load(), i, childLive[wi].tenantID))
			s.val.Store(childLive[wi].val)
			s.tag.Store(childLive[wi].tag)
			wi++
		}
	}

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

func (idx *InlineIndex) TotalNodes() int {
	bumped := int(idx.pool.next.Load()) - 1
	idx.pool.mu.Lock()
	inQueue := len(idx.pool.ready) - idx.pool.readyHd
	idx.pool.mu.Unlock()
	n := bumped - inQueue
	if n < 0 {
		return 0
	}
	return n
}

func (idx *InlineIndex) NumShards() int { return len(idx.shards) }

// LevelStats holds per-depth statistics for PerLevelStats.
type LevelStats struct {
	Depth   int
	Nodes   int
	Entries int
	Slots   int
}

func (idx *InlineIndex) PerLevelStats() []LevelStats {
	byDepth := make(map[int]*LevelStats)
	for s := range idx.shards {
		sh := &idx.shards[s]
		sh.mu.Lock()
		idx.perLevelNode(sh.root, 0, byDepth)
		sh.mu.Unlock()
	}
	maxDepth := 0
	for d := range byDepth {
		if d > maxDepth {
			maxDepth = d
		}
	}
	out := make([]LevelStats, 0, maxDepth+1)
	for d := 0; d <= maxDepth; d++ {
		if l, ok := byDepth[d]; ok {
			out = append(out, *l)
		}
	}
	return out
}

func (idx *InlineIndex) perLevelNode(nodeIdx uint32, depth int, acc map[int]*LevelStats) {
	if nodeIdx == iNullChild {
		return
	}
	l := acc[depth]
	if l == nil {
		l = &LevelStats{Depth: depth}
		acc[depth] = l
	}
	l.Nodes++
	l.Slots += iSlots
	node := idx.pool.node(nodeIdx)
	for i := range node.slots {
		t := node.slots[i].tag.Load()
		if t != iEmpty && t != iTombstone {
			l.Entries++
		}
	}
	for b := range node.children {
		idx.perLevelNode(atomic.LoadUint32(&node.children[b]), depth+1, acc)
	}
}
