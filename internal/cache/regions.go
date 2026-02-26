package cache

import (
	"rah/internal/clock"
	"sync"
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
) (offset uint64, generation uint32, overwritten *[16]byte, ok bool) {

	r.mu.Lock()
	defer r.mu.Unlock()

	headerSize := uint64(32)
	totalSize := headerSize + uint64(len(value))

	if totalSize > r.capacity {
		return 0, 0, nil, false
	}

	now := uint32(clock.CurrentClock.UnixCurTime)

	if r.writePos+totalSize > r.capacity {
		r.writePos = 0
		r.generation++
	}

	offset = r.writePos
	existing := (*EntryHeader)(unsafe.Pointer(&r.memory[offset]))

	if existing.Expiry > now {
		return 0, 0, nil, false
	}

	if existing.Expiry != 0 {
		overwritten = &existing.Fingerprint
	}

	// REPLACED BLOCK: Direct pointer assignment instead of copy()
	h := (*EntryHeader)(unsafe.Pointer(&r.memory[offset]))
	h.Expiry = now + r.ttlSeconds
	h.ValueLen = uint32(len(value))
	h.Generation = r.generation
	h.TenantID = tenantID
	h.Fingerprint = fingerprint

	copy(r.memory[offset+32:], value)

	r.writePos += totalSize
	return offset, r.generation, overwritten, true
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

	if header.Expiry < uint32(clock.CurrentClock.UnixCurTime) {
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
	return header.Expiry <= uint32(clock.CurrentClock.UnixCurTime)
}
