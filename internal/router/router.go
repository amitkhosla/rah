package router

import (
	"bytes"
	"sync"
	"sync/atomic"
	"unsafe"
)

type RahRouter struct {
	mu          sync.Mutex   // Protects the Builder during 'Add' calls
	builder     *BuilderNode // The flexible tree for modifications
	arena       atomic.Value // Stores []RouteNode
	prefixTable atomic.Value // Stores []byte
}

func New() *RahRouter {
	router := &RahRouter{
		builder: NewBuilder(), // Starts with an empty BuilderNode
	}
	router.arena.Store([]RouteNode{})
	return router
}

// Add inserts a new route. This is the "Writer" path.
func (r *RahRouter) Add(path string, apiId uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.builder.Add(path, apiId)

	// Bake both parts
	nodes, table := r.builder.BakeToArena()

	// Atomic store both
	r.arena.Store(nodes)
	r.prefixTable.Store(table)
}

// Lookup finds the API name. This is the "Reader" path.
// It uses NO LOCKS and is incredibly fast.
func (r *RahRouter) Lookup(path string) uint32 {
	currentArena := r.arena.Load().([]RouteNode)
	table := r.prefixTable.Load().([]byte)
	if len(currentArena) == 0 {
		return 0
	}

	input := unsafe.Slice(unsafe.StringData(path), len(path))
	curr := &currentArena[0]
	var longestMatch uint32

	for {
		prefix := table[curr.prefixOff : curr.prefixOff+uint32(curr.prefixLen)]

		// 1. Structural Match: Does the input start with this fragment?
		if len(input) < len(prefix) || !bytes.Equal(input[:len(prefix)], prefix) {
			return longestMatch
		}

		// Advance the pointer
		input = input[len(prefix):]

		// 2. Boundary Check: Only update longestMatch if this node is an API
		// AND we are at a segment boundary (next char is '/' or end of string).
		if curr.apiId != 0 {
			if len(input) == 0 || input[0] == '/' {
				longestMatch = curr.apiId
			}
		}

		// 3. Exact match exit
		if len(input) == 0 {
			return curr.apiId
		}

		// 4. Try to find a child for the next character
		next := curr.findChildIdx(input[0], currentArena)
		if next == nil {
			return longestMatch
		}
		curr = next
	}
}

// AddMany adds multiple routes and bakes the arena ONLY ONCE at the end.
func (r *RahRouter) AddMany(routes map[string]uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for path, name := range routes {
		r.builder.Add(path, name)
	}

	nodes, table := r.builder.BakeToArena()

	// Store both atomically
	r.arena.Store(nodes)
	r.prefixTable.Store(table)
}
