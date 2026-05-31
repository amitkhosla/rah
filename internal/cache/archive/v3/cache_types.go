package v3

import "unsafe"

// ── xSlot val encoding ───────────────────────────────────────────────────────
//
// 64-bit val field — all types share a 16-bit fixed header in bits[63:48]:
//
//	[63:62] Gen      — 2b wrap generation (fast pre-check; EntryHeader.Gen authoritative)
//	[61:50] ExpTrunc — 12b truncated expiry: (unixSec >> 5) & 0xFFF
//	                   32-second granularity, ~36-hour range before wrap
//	[49:48] Type     — 00=SlabRAM  01=KeyIsValue  10=EmptyValue  11=reserved
//
// Type-dependent payload [47:0]:
//
//	SlabRAM    : SizeClass(2b) | TierID(2b) | Offset(44b)
//	KeyIsValue : Expiry_unix32(32b) | spare(16b)   — value is the key (in tag)
//	EmptyValue : Expiry_unix32(32b) | spare(16b)   — negative cache
const (
	xValGenShift  = 62
	xValExpShift  = 50
	xValTypeShift = 48

	xValTypeMask     = uint64(0x3) << xValTypeShift
	xValTypeSlabRAM  = uint64(0x0) << xValTypeShift
	xValTypeKeyIsVal = uint64(0x1) << xValTypeShift
	xValTypeEmptyVal = uint64(0x2) << xValTypeShift

	// SlabRAM payload bit positions within [47:0].
	xValClassShift = 46
	xValTierShift  = 44
	xValOffMask    = uint64((1 << 44) - 1)
)

// SmartPointer is the 64-bit val stored in an xSlot, encoding entry location
// and type. Use PackSlabVal / UnpackSlab to produce and decode it.
type SmartPointer = uint64

// PackSlabVal encodes a SlabRAM val for storage in xSlot.val.
func PackSlabVal(gen uint8, expTrunc uint16, classID, tierID uint8, offset uint64) SmartPointer {
	return (uint64(gen&0x3) << xValGenShift) |
		(uint64(expTrunc&0xFFF) << xValExpShift) |
		xValTypeSlabRAM |
		(uint64(classID&0x3) << xValClassShift) |
		(uint64(tierID&0x3) << xValTierShift) |
		(offset & xValOffMask)
}

// Unpack decodes the common 16-bit header fields present in every val type.
func Unpack(v SmartPointer) (gen uint8, expTrunc uint16, typ uint64) {
	return uint8(v >> xValGenShift),
		uint16((v >> xValExpShift) & 0xFFF),
		v & xValTypeMask
}

// UnpackSlab decodes the SlabRAM-specific payload from a val.
func UnpackSlab(v SmartPointer) (classID, tierID uint8, offset uint64) {
	return uint8((v >> xValClassShift) & 0x3),
		uint8((v >> xValTierShift) & 0x3),
		v & xValOffMask
}

// ExpTrunc converts a full unix-second timestamp to the 12-bit truncated form
// used as a fast pre-check in xSlot.val.
func ExpTrunc(unixSec uint32) uint16 { return uint16((unixSec >> 5) & 0xFFF) }

// ExpTruncExpired reports whether the truncated expiry suggests the entry has
// passed. Conservative: false negatives occur after ~36 h wrap; callers must
// also check EntryHeader.Expiry for authoritative confirmation.
func ExpTruncExpired(trunc uint16, nowSec uint32) bool {
	return trunc < uint16((nowSec>>5)&0xFFF)
}

// ── xSlotPtr ─────────────────────────────────────────────────────────────────
//
// 6-byte (48-bit) tag reference: slab entry → owning xSlot in the trie index.
// Stored in EntryHeader so the cleaner can locate and CAS-tombstone the index
// entry via a fresh trie traversal (immune to split/collapse path staleness).
//
// Bit layout (lower 48 bits of uint64, little-endian in EntryHeader.XSlotPtrB):
//
//	[47]    lane     — 1b  0=tinyIdx, 1=hashIdx
//	[46:39] shard    — 8b  pre-computed shard index (= shardOf(tag))
//	[38: 0] tagBits  — 39b lower 39 bits of the tag (routing bits for trieFind)
//
// The stored shard is pre-computed (rather than re-derived) so that hashed
// shard selection (tinyIdx hashShards=true) works correctly at tombstone time.
// The 39 routing bits cover shardBits(8) + maxDepth×xTrieBits(30) = 38 bits,
// leaving one spare bit.
type xSlotPtr uint64

const (
	xPtrLaneBit  = 47
	xPtrShardBit = 39

	xPtrLaneMask  = uint64(1) << xPtrLaneBit
	xPtrShardMask = uint64(0xFF) << xPtrShardBit
	xPtrTagMask   = (uint64(1) << xPtrShardBit) - 1

	xPtrLaneTiny = uint64(0)                // lane bit = 0 for tinyIdx
	xPtrLaneHash = uint64(1) << xPtrLaneBit // lane bit = 1 for hashIdx
)

func (p xSlotPtr) laneBit() uint64 { return uint64(p) >> xPtrLaneBit & 0x1 }
func (p xSlotPtr) tagBits() uint64 { return uint64(p) & xPtrTagMask }

// xSlotPtrTo6 serialises p into 6 little-endian bytes for EntryHeader.XSlotPtrB.
func xSlotPtrTo6(p xSlotPtr) [6]byte {
	v := uint64(p)
	return [6]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24), byte(v >> 32), byte(v >> 40)}
}

// xSlotPtrFrom6 deserialises 6 little-endian bytes back to an xSlotPtr.
func xSlotPtrFrom6(b [6]byte) xSlotPtr {
	return xSlotPtr(uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 |
		uint64(b[3])<<24 | uint64(b[4])<<32 | uint64(b[5])<<40)
}

// packXTagRef constructs an xSlotPtr from lane (pre-shifted: xPtrLaneTiny or
// xPtrLaneHash), the pre-computed shard index, and the lower 39 bits of the tag.
func packXTagRef(laneBit47 uint64, shard uint8, tagLower39 uint64) xSlotPtr {
	return xSlotPtr(
		(laneBit47 & xPtrLaneMask) |
			(uint64(shard) << xPtrShardBit) |
			(tagLower39 & xPtrTagMask),
	)
}

// ── EntryHeader ───────────────────────────────────────────────────────────────
//
// Fixed 24-byte prefix of every slab slot.
//
//	Offset  Size  Field
//	     0     4  Expiry    — unix32, authoritative TTL
//	     4     2  TenantID  — for accounting and explicit re-verification
//	     6     2  ValueLen  — actual bytes used within the slot (≤ SizeClass)
//	     8     6  XSlotPtrB — little-endian xSlotPtr back-pointer (cleaner)
//	    14     1  Gen       — 8-bit region generation (circular-buffer wrap counter)
//	    15     1  _         — spare
//	    16     3  KeyFP     — key[(n/2)+1%n], key[(n/4)+1%n], key[(3n/4)+1%n]
//	    17     5  _         — padding to 24 bytes
//
// Total: 24 bytes, 8-byte naturally aligned.

const EntryHeaderSize = 24

type EntryHeader struct {
	Expiry    uint32
	TenantID  uint16
	ValueLen  uint16
	XSlotPtrB [6]byte
	Gen       uint8
	_spare    uint8
	KeyFP     [3]byte
	_pad      [5]byte
}

// headerAt casts buf[physOff] to *EntryHeader via unsafe.
// Safe when buf is Go-heap-allocated (guaranteed ≥8-byte aligned) and
// physOff is a multiple of 8 (guaranteed by stride = align8(24 + SizeClass)).
func headerAt(buf []byte, physOff uint64) *EntryHeader {
	return (*EntryHeader)(unsafe.Pointer(&buf[physOff]))
}
