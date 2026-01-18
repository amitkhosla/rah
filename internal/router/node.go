package router

import (
	"math/bits"
)

type Node struct {
	prefix   string
	mask     uint64  // Alphanumeric bitmask for O(1) jump
	children []*Node // Compressed slice (no nil gaps)
	apiName  string  // Store the API Name instead of a handler function
}

// charToBit maps '0-9', 'a-z', 'A-Z', and '/', '-' to a 64-bit space.
func charToBit(b byte) uint64 {
	switch {
	case b >= '0' && b <= '9':
		return 1 << (b - '0')
	case b >= 'a' && b <= 'z':
		return 1 << (b - 'a' + 10)
	case b >= 'A' && b <= 'Z':
		return 1 << (b - 'A' + 36)
	case b == '/':
		return 1 << 62
	case b == '-':
		return 1 << 63
	default:
		return 0
	}
}

// findChild performs a constant-time lookup using bitwise math.
func (n *Node) findChild(b byte) *Node {
	bit := charToBit(b)

	if bit == 0 || n.mask&bit == 0 {
		// Fallback for symbols like underscores or dots
		for _, child := range n.children {
			if len(child.prefix) > 0 && child.prefix[0] == b {
				return child
			}
		}
		return nil
	}

	idx := bits.OnesCount64(n.mask & (bit - 1))
	return n.children[idx]
}
