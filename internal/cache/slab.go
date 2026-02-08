package cache

import (
	"encoding/binary"
	"sync"
	"time"
)

type SlabSegment struct {
	Data    []byte
	Version uint8 // Incremented on wrap-around (Steamroller)
}

type Slab struct {
	mu           sync.RWMutex
	ID           uint8
	Segments     []*SlabSegment
	CurrentSegID uint8
	Cursor       uint32
	TTLSeconds   uint32
	IsTiny       bool // Fixed the compilation issue: Added field to struct
}

func NewSlab(id uint8, initialSize uint32, ttl uint32, isTiny bool) *Slab {
	firstSeg := &SlabSegment{Data: make([]byte, initialSize), Version: 1}
	return &Slab{
		ID:           id,
		Segments:     []*SlabSegment{firstSeg},
		TTLSeconds:   ttl,
		CurrentSegID: 0,
		IsTiny:       isTiny,
	}
}

// IsSpaceAvailable checks if the data plus the 12-byte header can fit.
func (s *Slab) IsSpaceAvailable(dataLen uint32) bool {
	header := uint32(12)
	if s.IsTiny {
		return s.Cursor+24 <= uint32(len(s.Segments[s.CurrentSegID].Data))
	}
	return s.Cursor+(header+dataLen) <= uint32(len(s.Segments[s.CurrentSegID].Data))
}

// IsOldestExpired checks if the item at the start of the current segment is dead.
func (s *Slab) IsOldestExpired() bool {
	seg := s.Segments[s.CurrentSegID]
	if s.Cursor == 0 || len(seg.Data) < 12 {
		return false
	}
	// Expiry is at Bytes 8-11 in our 12-byte header
	expiry := binary.LittleEndian.Uint32(seg.Data[8:12])
	return uint32(time.Now().Unix()) > expiry && expiry != 0
}

func (s *Slab) ResetToZero() {
	s.Cursor = 0
	seg := s.Segments[s.CurrentSegID]
	seg.Version++
	if seg.Version > 15 { // Wraps at 4 bits (0-15)
		seg.Version = 1
	}
}

func (s *Slab) Grow() {
	lastSize := uint32(len(s.Segments[s.CurrentSegID].Data))
	newSize := lastSize * 2
	newSeg := &SlabSegment{Data: make([]byte, newSize), Version: 1}
	s.Segments = append(s.Segments, newSeg)
	s.CurrentSegID = uint8(len(s.Segments) - 1)
	s.Cursor = 0
}

// Push now stores the key for collision validation [cite: 2, 5]
func (s *Slab) Push(tenantID uint16, key string, data []byte) (uint8, uint8, uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	seg := s.Segments[s.CurrentSegID]
	offset := s.Cursor
	keyLen := uint16(len(key))
	dataLen := uint32(len(data))
	expiry := uint32(time.Now().Unix()) + s.TTLSeconds

	// HEADER: [Magic:1][Ver:1][KeyLen:2][Tenant:2][Expiry:4][DataLen:4] = 14 bytes
	seg.Data[offset] = MagicByte
	seg.Data[offset+1] = seg.Version & 0xF
	binary.LittleEndian.PutUint16(seg.Data[offset+2:], keyLen)
	binary.LittleEndian.PutUint16(seg.Data[offset+4:], tenantID)
	binary.LittleEndian.PutUint32(seg.Data[offset+6:], expiry)
	binary.LittleEndian.PutUint32(seg.Data[offset+10:], dataLen)

	// Write Key (for collision check) and Data
	copy(seg.Data[offset+14:], []byte(key))
	copy(seg.Data[offset+14+uint32(keyLen):], data)

	s.Cursor += (14 + uint32(keyLen) + dataLen)
	return s.CurrentSegID, seg.Version, offset
}

// Get performs the "Triple Validation": Version, Tenant, and Actual Key [cite: 346, 347, 348]
func (s *Slab) Get(ptr SmartPointer, key string, expectedTenant uint16) ([]byte, bool) {
	_, _, ver, segID, _, offset := Unpack(ptr)
	seg := s.Segments[segID]

	// 1. Version & Magic Check
	if seg.Data[offset] != MagicByte || (seg.Data[offset+1]&0xF) != ver {
		return nil, false
	}

	// 2. Metadata & Expiry Check
	keyLen := binary.LittleEndian.Uint16(seg.Data[offset+2:])
	storedTenant := binary.LittleEndian.Uint16(seg.Data[offset+4:])
	expiry := binary.LittleEndian.Uint32(seg.Data[offset+6:])
	dataLen := binary.LittleEndian.Uint32(seg.Data[offset+10:])

	if storedTenant != expectedTenant || uint32(time.Now().Unix()) > expiry {
		return nil, false
	}

	// 3. Key Match (Collision Protection)
	storedKey := string(seg.Data[offset+14 : offset+14+uint32(keyLen)])
	if storedKey != key {
		return nil, false
	}

	return seg.Data[offset+14+uint32(keyLen) : offset+14+uint32(keyLen)+dataLen], true
}
