package router

import (
	"sync"
	"sync/atomic"
)

type RahStringRouter struct {
	mu      sync.Mutex         // Protects the Builder during 'Add' calls
	builder *BuilderStringNode // The flexible tree for modifications
	arena   atomic.Value       // The ultra-fast static arena for 'Lookup'
}

func NewStringRouter() *RahStringRouter {
	router := &RahStringRouter{
		builder: NewStringBuilder(), // Starts with an empty BuilderNode
	}
	router.arena.Store([]RouteStringNode{})
	return router
}

// Add inserts a new route. This is the "Writer" path.
func (r *RahStringRouter) Add(path string, apiName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Add to the flexible builder tree
	r.builder.Add(path, apiName)

	// 2. Re-bake the arena and swap it atomically
	// This happens in ~2ms for 2,000+ APIs
	r.arena.Store(r.builder.BakeToArena())
}

// Lookup finds the API name. This is the "Reader" path.
// It uses NO LOCKS and is incredibly fast.
func (r *RahStringRouter) Lookup(path string) string {
	// We capture the slice header locally to ensure we stay on
	// one version of the arena for the duration of the lookup.
	currentArena := r.arena.Load().([]RouteStringNode)

	if len(currentArena) == 0 {
		return ""
	}

	curr := &currentArena[0]
	var lastMatchedAPI string

	for {
		pLen := len(curr.prefix)
		if len(path) >= pLen && path[:pLen] == curr.prefix {
			if curr.apiName != "" {
				if len(path) == pLen || path[pLen] == '/' {
					lastMatchedAPI = curr.apiName
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
func (r *RahStringRouter) AddMany(routes map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for path, name := range routes {
		r.builder.Add(path, name)
	}

	// Single bake for all 2,000 routes
	r.arena.Store(r.builder.BakeToArena())
}
