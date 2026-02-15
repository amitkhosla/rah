package engine

import "math/bits"

/*
SubRouteNode represents a single node inside the SubArena radix tree.

Design Principles:

1. Flat slice storage for cache locality.
2. Children indexed via 2x uint64 bitmasks (MaskLo / MaskHi) for O(1) branching.
3. Static segments take precedence over dynamic param segments.
4. Method-level routing stored via bitmask.
5. Terminal detection separated from path traversal.

Method Index Mapping:
0 → GET
1 → POST
2 → PUT
3 → DELETE
4 → OTHERS
*/

type SubRouteNode struct {
	// Radix branching masks (ASCII 0–127)
	MaskLo      uint64
	MaskHi      uint64
	ChildIdx    uint32
	NumChildren uint16
	PrefixLen   uint16

	// Dynamic Path Parameter Handling
	HasParamChild bool
	ParamChildIdx uint32
	ParamSlot     uint8

	// Method handling via bitmask
	// Each bit corresponds to method index.
	AllowedMethods uint8
	StrictMethods  uint8

	// Endpoint index per method
	EndpointIdx [5]uint32
}

/*
FindChildIdx performs O(1) lookup using bitmask rank calculation.

Returns:
- Index of child node
- 0 if not found
*/
func (n *SubRouteNode) FindChildIdx(
	char byte,
	arena []SubRouteNode,
) uint32 {

	var mask uint64
	var base uint32

	if char < 64 {
		mask = n.MaskLo
		base = n.ChildIdx
		if mask&(1<<char) == 0 {
			return 0
		}
		offset := bitsCount(mask & ((1 << char) - 1))
		return base + uint32(offset)
	}

	mask = n.MaskHi
	base = n.ChildIdx
	char -= 64
	if mask&(1<<char) == 0 {
		return 0
	}
	offset := bitsCount(mask & ((1 << char) - 1))
	return base + uint32(offset)
}

/*
bitsCount returns number of set bits.
Uses builtin for performance.
*/
func bitsCount(x uint64) int {
	return bits.OnesCount64(x)
}
