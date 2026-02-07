package engine

import "math/bits"

// SubRouteNode fits in 64 bytes (1 cache line)
type SubRouteNode struct {
	MaskLo      uint64
	MaskHi      uint64
	ChildIdx    uint32
	NumChildren uint16
	PrefixLen   uint16

	// Special Markers for Path Parameters (repurposed padding)
	HasParamChild bool
	ParamChildIdx uint32
	ParamSlot     uint8

	// Terminal logic
	IsTerminal  bool
	IsStrict    bool
	EndpointIdx uint32
}

func (n *SubRouteNode) FindChildIdx(b byte, arena []SubRouteNode) uint32 {
	isHi := uint64(b >> 6)
	bitIdx := b & 63
	bit := uint64(1) << bitIdx

	targetMask := (n.MaskLo & ^(isHi * 0xFFFFFFFFFFFFFFFF)) | (n.MaskHi & (isHi * 0xFFFFFFFFFFFFFFFF))

	if targetMask&bit != 0 {
		// Succinct rank calculation using bit manipulation
		var rank int
		if isHi == 0 {
			rank = bits.OnesCount64(n.MaskLo & (bit - 1))
		} else {
			rank = bits.OnesCount64(n.MaskLo) + bits.OnesCount64(n.MaskHi&(bit-1))
		}
		return n.ChildIdx + uint32(rank)
	}
	return 0
}
