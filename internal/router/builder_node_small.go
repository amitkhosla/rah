package router

import (
	"sort"
)

// BuilderNode is the "flexible" node used for Add/Split logic
type BuilderNodeSmall struct {
	prefix   string
	apiId    uint16
	children map[byte]*BuilderNodeSmall
}

func NewBuilderSmall() *BuilderNodeSmall {
	return &BuilderNodeSmall{children: make(map[byte]*BuilderNodeSmall)}
}

// BakeToArena transforms the Builder tree into the high-performance slice.
func (bn *BuilderNodeSmall) BakeToArena() []RouteNodeSmall {
	var nodes []RouteNodeSmall

	// Use BFS to keep siblings together for Cache Locality
	type task struct {
		temp *BuilderNodeSmall
		idx  int
	}

	queue := []task{{bn, 0}}
	nodes = append(nodes, RouteNodeSmall{prefix: bn.prefix, apiId: bn.apiId})

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if len(curr.temp.children) == 0 {
			continue
		}

		// 1. Group all children
		childStartIdx := uint16(len(nodes))
		nodes[curr.idx].childIdx = childStartIdx
		nodes[curr.idx].numChildren = uint8(len(curr.temp.children))

		// 2. Sort keys to ensure Popcount order matches Slice order
		keys := make([]byte, 0, len(curr.temp.children))
		for b := range curr.temp.children {
			keys = append(keys, b)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

		// 3. Populate BakedNode metadata
		for _, b := range keys {
			// Set Bitmask
			if b < 64 {
				nodes[curr.idx].maskLo |= (1 << b)
			} else {
				nodes[curr.idx].maskHi |= (1 << (b - 64))
			}

			// Add child to Arena
			childBuilder := curr.temp.children[b]
			childArenaIdx := len(nodes)
			nodes = append(nodes, RouteNodeSmall{
				prefix: childBuilder.prefix,
				apiId:  childBuilder.apiId,
			})

			// Queue child for its own children processing
			queue = append(queue, task{childBuilder, childArenaIdx})
		}
	}
	return nodes
}

func (bn *BuilderNodeSmall) Add(path string, apiName uint16) {
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
		oldSuffix := &BuilderNodeSmall{
			prefix:   bn.prefix[i:],
			apiId:    bn.apiId,
			children: bn.children,
		}
		// Reset current node to the common prefix
		bn.prefix = bn.prefix[:i]
		bn.apiId = 0
		bn.children = make(map[byte]*BuilderNodeSmall)
		bn.children[oldSuffix.prefix[0]] = oldSuffix
	}

	// Case 2: There is more path to add
	if i < len(path) {
		remainingPath := path[i:]
		if child, exists := bn.children[remainingPath[0]]; exists {
			child.Add(remainingPath, apiName)
		} else {
			bn.children[remainingPath[0]] = &BuilderNodeSmall{
				prefix:   remainingPath,
				apiId:    apiName,
				children: make(map[byte]*BuilderNodeSmall),
			}
		}
	} else {
		// Case 3: Perfect match
		bn.apiId = apiName
	}
}
