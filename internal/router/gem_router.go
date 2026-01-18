package router

import (
	"math/bits"
	"slices"
)

type GemRouter struct {
	root *Node
}

func New() *GemRouter {
	// Starting with an empty prefix node simplifies everything
	return &GemRouter{root: &Node{}}
}

// Lookup finds the API Name for the given path.
// It returns the name of the most specific matching basepath.
func (r *GemRouter) Lookup(path string) string {
	curr := r.root
	var lastMatchedAPI string

	for {
		// 1. Refresh prefix info for the CURRENT node
		prefix := curr.prefix
		prefixLen := len(prefix)
		pathLen := len(path)
		// 2. Check if this node matches the start of our path
		if pathLen >= prefixLen && path[:prefixLen] == prefix {

			// If this node is an API gateway entry, remember it
			if curr.apiName != "" {
				if pathLen == prefixLen || path[prefixLen] == '/' {
					lastMatchedAPI = curr.apiName
				}
			}

			// Consume characters
			path = path[prefixLen:]

			// Path fully matched!
			if len(path) == 0 {
				return lastMatchedAPI
			}

			// 3. Try to jump to a child
			next := curr.findChild(path[0])
			if next == nil {
				// No more specific children, return the best basepath we found
				return lastMatchedAPI
			}

			// Move to child and restart loop
			curr = next
			continue
		}

		// Prefix mismatch
		return lastMatchedAPI
	}
}

// Add inserts a new API basepath and its associated name.
func (r *GemRouter) Add(path string, apiName string) {
	// No more nil check needed
	r.root.addRoute(path, apiName)
}

func (n *Node) addRoute(path string, apiName string) {
	i := 0
	maxLen := minimum(len(path), len(n.prefix))
	for i < maxLen && path[i] == n.prefix[i] {
		i++
	}

	// Split node if common prefix is shorter than current node prefix
	if i < len(n.prefix) {
		child := &Node{
			prefix:   n.prefix[i:],
			mask:     n.mask,
			children: n.children,
			apiName:  n.apiName,
		}
		n.prefix = n.prefix[:i]
		n.apiName = "" // Junction node gets no name unless it was an exact match
		n.mask = charToBit(child.prefix[0])
		n.children = []*Node{child}
	}

	if i < len(path) {
		path = path[i:]
		bit := charToBit(path[0])

		if target := n.findChild(path[0]); target != nil {
			target.addRoute(path, apiName)
		} else {
			newNode := &Node{prefix: path, apiName: apiName}
			if bit != 0 {
				n.mask |= bit
				idx := bits.OnesCount64(n.mask & (bit - 1))
				n.children = slices.Insert(n.children, idx, newNode)
			} else {
				n.children = append(n.children, newNode)
			}
		}
	} else {
		// Perfect match for this node
		n.apiName = apiName
	}
}

func minimum(a, b int) int {
	if a < b {
		return a
	}
	return b
}
