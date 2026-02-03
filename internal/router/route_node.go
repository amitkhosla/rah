package router

import (
	"math/bits"
)

type RouteNode struct {

	// Prefix metadata instead of string pointer
	prefixOff uint32 // Offset into the global PrefixTable
	prefixLen uint16 // Length of the prefix

	maskLo      uint64 // bits 0-63
	maskHi      uint64 // bits 64-127
	childIdx    uint32 // Offset into the global Arena slice
	numChildren uint16 // Number of contiguous children starting at childIdx
	apiId       uint32
	_           [32]byte
}

func (n *RouteNode) findChildIdx(b byte, arena []RouteNode) *RouteNode {
	// 1. Branchless Selection of the correct mask
	// If b < 64, isHi is 0. If b >= 64, isHi is 1.
	isHi := uint64(b >> 6)
	bitIdx := b & 63
	bit := uint64(1) << bitIdx

	// Select maskLo or maskHi without an 'if'
	// This allows the compiler to use CMOV (Conditional Move) instructions
	targetMask := (n.maskLo & ^(isHi * 0xFFFFFFFFFFFFFFFF)) | (n.maskHi & (isHi * 0xFFFFFFFFFFFFFFFF))

	if targetMask&bit == 0 {
		return nil
	}

	// 2. Calculate the jump index
	// If we are in the High mask, we must add all bits from the Low mask first
	loCount := bits.OnesCount64(n.maskLo)

	// If isHi is 0, offset is just the popcount of the low mask up to bit
	// If isHi is 1, offset is loCount + popcount of the high mask up to bit
	idx := (uint32(loCount) * uint32(isHi)) + uint32(bits.OnesCount64(targetMask&(bit-1)))

	return &arena[n.childIdx+idx]
}
