package cache

// SmartPointer encodes location inside slab memory.
//
// Layout (64 bits):
//
// [63-62] Tag (2 bits)
// [61-56] SizeClassID (6 bits)
// [55-52] TierID (4 bits)
// [51-44] Generation (8 bits)
// [43-0 ] Offset (44 bits)
//
// Offset supports up to 16TB per region (more than enough).
type SmartPointer uint64

const (
	TagSlabRAM   = 0x0 // Standard variable-length items
	TagTiny      = 0x3 // Fixed 24B slots (10B Header + 14B Data)
	TagDirectInt = 0x2 // Direct value storage (Future)
	TagExternal  = 0x1 // Overflow to Redis/Disk

	MagicByte = 0xAA // Sentinel to detect memory corruption
)

func PackPointer(tag, classID, tierID uint8, generation uint32, offset uint64) SmartPointer {
	return SmartPointer(
		(uint64(tag&0x3) << 62) |
			(uint64(classID&0x3F) << 56) |
			(uint64(tierID&0xF) << 52) |
			(uint64(generation&0xFF) << 44) |
			(offset & 0xFFFFFFFFFFF),
	)
}

// Unpack decodes a SmartPointer into its individual components.
// [63-62] Tag (2 bits)
// [61-56] SizeClassID (6 bits)
// [55-52] TierID (4 bits)
// [51-44] Generation (8 bits)
// [43-0 ] Offset (44 bits)
//
// Returns:
//
//	tag         - entry type (RAM / external / etc)
//	classID     - size class index
//	tierID      - TTL tier index
//	generation  - region generation (wrap protection)
//	offset      - byte offset inside region
func Unpack(ptr SmartPointer) (
	tag uint8,
	classID uint8,
	tierID uint8,
	generation uint32,
	offset uint64,
) {
	val := uint64(ptr)

	tag = uint8(val >> 62)
	classID = uint8((val >> 56) & 0x3F)
	tierID = uint8((val >> 52) & 0x0F)
	generation = uint32((val >> 44) & 0xFF)
	offset = val & 0xFFFFFFFFFFF

	return
}

func GetSlabID(ptr SmartPointer) uint8 {
	return uint8((uint64(ptr) >> 56) & 0x3F)
}

// EntryHeader is stored at the beginning of every record inside a slab.
//
// Layout (32 bytes, cache-line friendly):
//
//	0  -  3  : Expiry (absolute unix seconds)
//	4  -  7  : ValueLen
//	8  - 11  : Generation (region wrap protection)
//
// 12  - 13  : TenantID
// 14         : Flags
// 15         : padding (alignment)
// 16  - 31  : 128-bit fingerprint
//
// Total: 32 bytes
type EntryHeader struct {
	Expiry      uint32
	ValueLen    uint32
	Generation  uint32
	TenantID    uint16
	Flags       uint8
	_pad        uint8
	Fingerprint [16]byte
}
