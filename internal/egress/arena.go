package egress

import "sync"

const egressArenaBlockSize = 32 * 1024 // 32 KB per pool block

var egressArenaPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, egressArenaBlockSize)
		return &b
	},
}

// arenaPool is a pool-backed append-only byte store.
// Strings are written as [uint16_hi][uint16_lo][bytes...].
// All writes are serialised by the caller (egressCache holds the lock).
type arenaPool struct {
	blocks []*[]byte // borrowed from egressArenaPool (for release)
	data   []byte    // flat view of all written bytes
}

// Write appends s to the arena in length-prefixed format.
// Returns the starting offset of the length header and the string length.
// Panics if len(s) > 65535.
func (a *arenaPool) Write(s string) (off uint32, length uint16) {
	if len(s) > 65535 {
		panic("egress arena: string too long")
	}
	if a.data == nil {
		b := egressArenaPool.Get().(*[]byte)
		a.blocks = append(a.blocks, b)
		a.data = (*b)[:0]
	}
	off = uint32(len(a.data))
	length = uint16(len(s))
	a.data = append(a.data, byte(length>>8), byte(length))
	a.data = append(a.data, s...)
	return off, length
}

// ReadAt returns a zero-copy string view of the string stored at off+2
// (past the 2-byte length header). length must be the value returned by Write.
func (a *arenaPool) ReadAt(off uint32, length uint16) string {
	start := off + 2 // skip 2-byte length header
	return string(a.data[start : start+uint32(length)])
}

// Release returns borrowed pool blocks and resets the arena.
func (a *arenaPool) Release() {
	for _, b := range a.blocks {
		*b = (*b)[:0]
		egressArenaPool.Put(b)
	}
	a.blocks = a.blocks[:0]
	a.data = nil
}
