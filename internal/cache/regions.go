package cache

import (
	"sync"
	"time"
	"unsafe"
)

// Region represents one circular memory arena for a
// specific SizeClass × TTLTier combination.
type Region struct {
	memory     []byte
	writePos   uint64
	capacity   uint64
	generation uint32
	ttlSeconds uint32

	mu sync.Mutex
}

type overwrittenEntry struct {
	fingerprint [16]byte
	tenantID    uint16
	valueLen    uint32
}

func NewRegion(size uint64, ttl uint32) *Region {
	return &Region{
		memory:     make([]byte, size),
		capacity:   size,
		ttlSeconds: ttl,
	}
}

func (r *Region) Write(
	tenantID uint16,
	fingerprint [16]byte,
	value []byte,
) (
	offset uint64,
	generation uint32,
	overwritten overwrittenEntry,
	hasOverwritten bool,
	ok bool,
) {

	r.mu.Lock()
	defer r.mu.Unlock()

	headerSize := uint64(32)
	totalSize := headerSize + uint64(len(value))

	if totalSize > r.capacity {
		return 0, 0, overwritten, false, false
	}

	now := uint32(time.Now().Unix())

	// Wrap if needed
	if r.writePos+totalSize > r.capacity {
		r.writePos = 0
		r.generation++
	}

	offset = r.writePos

	existing := (*EntryHeader)(unsafe.Pointer(&r.memory[offset]))

	// If slot is occupied and not expired → cannot overwrite
	if existing.Expiry > now {
		return 0, 0, overwritten, false, false
	}

	// Non-zero expiry means this slot had a previous entry; at this point it is
	// already expired (existing.Expiry <= now), so return old metadata for
	// index/accounting cleanup in CacheManager.
	if existing.Expiry != 0 {
		overwritten = overwrittenEntry{
			fingerprint: existing.Fingerprint,
			tenantID:    existing.TenantID,
			valueLen:    existing.ValueLen,
		}
		hasOverwritten = true
	}

	header := EntryHeader{
		Expiry:      now + r.ttlSeconds,
		ValueLen:    uint32(len(value)),
		Generation:  r.generation,
		TenantID:    tenantID,
		Fingerprint: fingerprint,
	}

	copy(r.memory[offset:], unsafe.Slice((*byte)(unsafe.Pointer(&header)), 32))
	copy(r.memory[offset+32:], value)

	r.writePos += totalSize

	return offset, r.generation, overwritten, hasOverwritten, true
}

func (r *Region) Read(
	offset uint64,
	expectedGen uint32,
	expectedTenant uint16,
	expectedFingerprint [16]byte,
) ([]byte, bool) {

	header := (*EntryHeader)(unsafe.Pointer(&r.memory[offset]))

	if header.Generation != expectedGen {
		return nil, false
	}

	if header.TenantID != expectedTenant {
		return nil, false
	}

	if header.Expiry < uint32(time.Now().Unix()) {
		return nil, false
	}

	if header.Fingerprint != expectedFingerprint {
		return nil, false
	}

	start := offset + 32
	end := start + uint64(header.ValueLen)

	return r.memory[start:end], true
}
func (r *Region) canOverwrite(offset uint64) bool {
	header := (*EntryHeader)(unsafe.Pointer(&r.memory[offset]))
	return header.Expiry <= uint32(time.Now().Unix())
}
