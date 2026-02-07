package engine

import (
	"sync/atomic"
)

// Endpoint holds the execution plan for a matched route.
type Endpoint struct {
	Id            uint32
	Plan          []Instruction
	BreachCounter uint64 // For tracking Flavor 2 "Additional Path" usage
}

// ApiDefinition is the top-level container for a single API's routing logic.
type ApiDefinition struct {
	Id          uint32
	BaseRawPath string

	// MethodRoots maps HTTP Methods to SubArena entry points.
	// 0:GET, 1:POST, 2:PUT, 3:DELETE, 4:OTHERS
	// If the API supports "ANY", all slots point to the same root index.
	MethodRoots [5]uint32
	SubArena    []SubRouteNode
	Endpoints   []Endpoint
}

// MethodToIdx converts standard HTTP verbs to our internal array index.
// MethodToIdx handles []byte without converting to string.
func MethodToIdx(m []byte) int {
	if len(m) == 0 {
		return 4
	} // Default/Others

	// Optimized switch based on the first character and length
	switch m[0] {
	case 'G': // GET
		return 0
	case 'P':
		if len(m) > 1 && m[1] == 'O' {
			return 1
		} // POST
		if len(m) > 1 && m[1] == 'U' {
			return 2
		} // PUT
		return 4 // PATCH or others
	case 'D': // DELETE
		return 3
	default:
		return 4 // OTHERS
	}
}

// IncrementBreach is a thread-safe way to track Flavor 2 usage.
func (e *Endpoint) IncrementBreach() {
	atomic.AddUint64(&e.BreachCounter, 1)
}

// Add this to internal/engine/api_definition.go
func BakeDefinition(id uint32, path string) *ApiDefinition {
	return &ApiDefinition{
		Id:          id,
		BaseRawPath: path,
		SubArena:    make([]SubRouteNode, 0, 8),
		Endpoints:   make([]Endpoint, 0, 4),
	}
}
