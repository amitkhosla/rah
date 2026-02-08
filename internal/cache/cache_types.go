package cache

// SmartPointer is now a 64-bit (8-byte) address.
// We moved to 64-bit to support Gigabytes of data while keeping a "Fail-Proof" safety margin.
//
// Bit Layout [64 bits total]:
// [63-62] Tag (2b): 00=Standard, 01=Tiny
// [61-56] SlabID (6b): Up to 64 distinct TTL/Size buckets.
// [55-52] Version (4b): 16 cycles of "Steamroller" protection.
// [51-28] DataLen (24b): Supports individual items up to 16MB.
// [27-0]  Offset (28b): Direct byte addressing for 256MB per segment.
type SmartPointer uint64

const (
	TagSlabRAM   = 0x0 // Standard variable-length items
	TagTiny      = 0x1 // Fixed 24B slots (10B Header + 14B Data)
	TagDirectInt = 0x2 // Direct value storage (Future)
	TagExternal  = 0x3 // Overflow to Redis/Disk

	MagicByte = 0xAA // Sentinel to detect memory corruption
)

func PackPointer(tag, slabID, ver, segID uint8, dLen, offset uint32) SmartPointer {
	return SmartPointer((uint64(tag&0x3) << 62) |
		(uint64(slabID&0x3F) << 56) |
		(uint64(ver&0xF) << 52) |
		(uint64(segID&0xF) << 48) |
		(uint64(dLen&0xFFFFFF) << 24) |
		(uint64(offset & 0xFFFFFF)))
}

func Unpack(ptr SmartPointer) (tag, slabID, ver, segID uint8, dLen, offset uint32) {
	val := uint64(ptr)
	tag = uint8(val >> 62)
	slabID = uint8((val >> 56) & 0x3F)
	ver = uint8((val >> 52) & 0xF)
	segID = uint8((val >> 48) & 0xF)
	dLen = uint32((val >> 24) & 0xFFFFFF)
	offset = uint32(val & 0xFFFFFF)
	return
}

func GetSlabID(ptr SmartPointer) uint8 {
	return uint8((uint64(ptr) >> 56) & 0x3F)
}
