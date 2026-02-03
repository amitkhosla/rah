package engine

import (
	"rah/internal/rctx"
	"sync"
	"sync/atomic"
)

type HeaderLookup struct {
	Map map[string]int
}

type HeaderRegistry struct {
	Current  atomic.Pointer[HeaderLookup]
	writerMu sync.Mutex
	nextSlot int
}

func NewHeaderRegistry() *HeaderRegistry {
	r := &HeaderRegistry{nextSlot: 5} // Start slots after internal reserve
	r.Current.Store(&HeaderLookup{Map: make(map[string]int)})
	return r
}

func (r *HeaderRegistry) RegisterHeader(name string) int {
	r.writerMu.Lock()
	defer r.writerMu.Unlock()

	curr := r.Current.Load()
	if idx, exists := curr.Map[name]; exists {
		return idx
	}

	newMap := make(map[string]int, len(curr.Map)+1)
	for k, v := range curr.Map {
		newMap[k] = v
	}

	idx := r.nextSlot
	newMap[name] = idx
	r.nextSlot++

	r.Current.Store(&HeaderLookup{Map: newMap})
	return idx
}

type FlowRegistry struct {
	// Native Go functions for maximum speed
	staticFlows map[string]func(ctx *rctx.Context) int16
	// Instruction arrays for runtime flexibility
	dynamicFlows map[string][]Instruction
}

func NewRegistry() *FlowRegistry {
	return &FlowRegistry{
		staticFlows:  make(map[string]func(*rctx.Context) int16),
		dynamicFlows: make(map[string][]Instruction),
	}
}

func (r *FlowRegistry) RegisterStatic(name string, fn func(*rctx.Context) int16) {
	r.staticFlows[name] = fn
}

func (r *FlowRegistry) RegisterDynamic(name string, plan []Instruction) {
	r.dynamicFlows[name] = plan
}

// Call executes the shared flow and returns 1 to move to the next parent step.
func (r *FlowRegistry) Call(name string, ctx *rctx.Context) int16 {
	if fn, ok := r.staticFlows[name]; ok {
		return fn(ctx)
	}
	if plan, ok := r.dynamicFlows[name]; ok {
		Run(plan, ctx)
	}
	return 1
}
