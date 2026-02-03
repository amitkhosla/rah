package router

import (
	"sort"
)

// BuilderNode is the "flexible" node used for Add/Split logic
type BuilderNode struct {
	prefix   string
	apiId    uint32
	children map[byte]*BuilderNode
}

func NewBuilder() *BuilderNode {
	return &BuilderNode{children: make(map[byte]*BuilderNode)}
}

// BakeToArena transforms the Builder tree into the high-performance slice.
func (bn *BuilderNode) BakeToArena() ([]RouteNode, []byte) {
	var nodes []RouteNode
	var table []byte // The new PrefixTable

	type task struct {
		temp *BuilderNode
		idx  int
	}

	// Root node setup
	rootPrefix := []byte(bn.prefix)
	table = append(table, rootPrefix...)
	nodes = append(nodes, RouteNode{
		prefixOff: 0,
		prefixLen: uint16(len(rootPrefix)),
		apiId:     bn.apiId,
	})

	queue := []task{{bn, 0}}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if len(curr.temp.children) == 0 {
			continue
		}

		nodes[curr.idx].childIdx = uint32(len(nodes))
		nodes[curr.idx].numChildren = uint16(len(curr.temp.children))
		keys := make([]byte, 0, len(curr.temp.children))
		for key := range curr.temp.children {
			keys = append(keys, key)
		}

		// Sort keys... (existing logic)
		sort.Slice(keys, func(k1, k2 int) bool { return keys[k1] < keys[k2] })

		for _, b := range keys {
			// 1. Set the bit in the parent's mask FIRST
			if b < 64 {
				nodes[curr.idx].maskLo |= (1 << b)
			} else {
				nodes[curr.idx].maskHi |= (1 << (b - 64))
			}

			// 2. Add the child to the Arena and Table
			child := curr.temp.children[b]
			off := uint32(len(table))
			pBytes := []byte(child.prefix)
			table = append(table, pBytes...)

			nodes = append(nodes, RouteNode{
				prefixOff: off,
				prefixLen: uint16(len(pBytes)),
				apiId:     child.apiId,
			})

			// 3. Queue the child for BFS processing
			queue = append(queue, task{child, len(nodes) - 1})
		}
	}
	return nodes, table
}

func (bn *BuilderNode) Add(path string, apiName uint32) {
	// If the current node is empty (root initialization)
	if bn.prefix == "" && len(bn.children) == 0 {
		bn.prefix = path
		bn.apiId = apiName
		return
	}

	i := 0
	maxLen := min(len(path), len(bn.prefix))
	for i < maxLen && path[i] == bn.prefix[i] {
		i++
	}

	// Case 1: The prefix needs to be split
	if i < len(bn.prefix) {
		// Create a new child containing the old suffix
		oldSuffix := &BuilderNode{
			prefix:   bn.prefix[i:],
			apiId:    bn.apiId,
			children: bn.children,
		}
		// Reset current node to the common prefix
		bn.prefix = bn.prefix[:i]
		bn.apiId = 0
		bn.children = make(map[byte]*BuilderNode)
		bn.children[oldSuffix.prefix[0]] = oldSuffix
	}

	// Case 2: There is more path to add
	if i < len(path) {
		remainingPath := path[i:]
		if child, exists := bn.children[remainingPath[0]]; exists {
			child.Add(remainingPath, apiName)
		} else {
			bn.children[remainingPath[0]] = &BuilderNode{
				prefix:   remainingPath,
				apiId:    apiName,
				children: make(map[byte]*BuilderNode),
			}
		}
	} else {
		// Case 3: Perfect match
		bn.apiId = apiName
	}
}
