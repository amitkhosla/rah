package router

import (
	"sync"
	"sync/atomic"
)

type RahRouterWoutPAdding struct {
	mu      sync.Mutex              // Protects the Builder during 'Add' calls
	builder *BuilderNodeWoutPadding // The flexible tree for modifications
	arena   atomic.Value            // The ultra-fast static arena for 'Lookup' behind atomic value
}

func NewWoutPadding() *RahRouterWoutPAdding {
	router := &RahRouterWoutPAdding{
		builder: NewBuilderWoutPadding(), // Starts with an empty BuilderNode
	}
	router.arena.Store([]RouteNodeWoutPatdding{})
	return router
}

// Add inserts a new route. This is the "Writer" path.
func (r *RahRouterWoutPAdding) Add(path string, apiId uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Add to the flexible builder tree
	r.builder.Add(path, apiId)

	// 2. Re-bake the arena and swap it atomically
	r.arena.Store(r.builder.BakeToArena())
}

// Lookup finds the API name. This is the "Reader" path.
// It uses NO LOCKS and is incredibly fast.
func (r *RahRouterWoutPAdding) Lookup(path string) uint32 {
	// We capture the slice header locally to ensure we stay on
	// one version of the arena for the duration of the lookup.
	currentArena := r.arena.Load().([]RouteNodeWoutPatdding)

	if len(currentArena) == 0 {
		return 0
	}

	curr := &currentArena[0]
	var lastMatchedAPI uint32

	for {
		pLen := len(curr.prefix)
		if len(path) >= pLen && path[:pLen] == curr.prefix {
			if curr.apiId != 0 {
				if len(path) == pLen || path[pLen] == '/' {
					lastMatchedAPI = curr.apiId
				}
			}

			path = path[pLen:]
			if len(path) == 0 {
				return lastMatchedAPI
			}

			// Branchless Popcount Jump
			next := curr.findChildIdx(path[0], currentArena)
			if next == nil {
				return lastMatchedAPI
			}
			curr = next
			continue
		}
		return lastMatchedAPI
	}
}

// AddMany adds multiple routes and bakes the arena ONLY ONCE at the end.
func (r *RahRouterWoutPAdding) AddMany(routes map[string]uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for path, name := range routes {
		r.builder.Add(path, name)
	}

	// Single bake for all 2,000 routes
	r.arena.Store(r.builder.BakeToArena())
}
