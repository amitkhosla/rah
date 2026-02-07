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

func (s *Slab) Push(tenantID uint32, data []byte) (uint8, uint8, uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	seg := s.Segments[s.CurrentSegID]
	offset := s.Cursor
	dataLen := uint32(len(data))
	expiry := uint32(time.Now().Unix()) + s.TTLSeconds

	// --- 12-BYTE FAIL-PROOF HEADER ---
	// [0]: Magic, [1]: Ver(4b), [2]: Checksum, [3]: Flags
	// [4-7]: TenantID, [8-11]: Expiry
	seg.Data[offset] = MagicByte
	seg.Data[offset+1] = seg.Version & 0xF
	seg.Data[offset+2] = MagicByte ^ (seg.Version & 0xF) ^ uint8(tenantID)
	seg.Data[offset+3] = 0

	binary.LittleEndian.PutUint32(seg.Data[offset+4:], tenantID)
	binary.LittleEndian.PutUint32(seg.Data[offset+8:], expiry)

	if s.IsTiny {
		// TINY PATH: Fixed 24B slot. 10B Header used, 14B for Data.
		copy(seg.Data[offset+10:], data)
		s.Cursor += 24
	} else {
		// STANDARD PATH: Header(12) + Data
		copy(seg.Data[offset+12:], data)

		// 8-byte alignment for 64-bit CPU speed
		total := 12 + dataLen
		padding := (8 - (total % 8)) % 8
		s.Cursor += (total + padding)
	}
	return s.CurrentSegID, seg.Version, offset
}
