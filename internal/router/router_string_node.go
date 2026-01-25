package router

import (
	"math/bits"
)

type RouteStringNode struct {
	prefix      string
	maskLo      uint64 // bits 0-63
	maskHi      uint64 // bits 64-127
	childIdx    uint32 // Offset into the global Arena slice
	numChildren uint16 // Number of contiguous children starting at childIdx
	apiName     string
}

func (n *RouteStringNode) findChildIdx(b byte, arena []RouteStringNode) *RouteStringNode {
	var bit uint64
	var mask uint64
	var idx uint32

	if b < 64 {
		bit = 1 << b
		mask = n.maskLo
		if mask&bit == 0 {
			return nil
		}
		idx = uint32(bits.OnesCount64(mask & (bit - 1)))
	} else {
		bit = 1 << (b - 64)
		mask = n.maskHi
		if mask&bit == 0 {
			return nil
		}
		// We add all set bits from the Low mask to skip over those children
		idx = uint32(bits.OnesCount64(n.maskLo) + bits.OnesCount64(mask&(bit-1)))
	}

	// Direct slice access via the Arena.
	// This is a single memory fetch because of contiguous allocation.
	return &arena[n.childIdx+idx]
}
