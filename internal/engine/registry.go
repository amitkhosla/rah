package engine

import "github.com/amitkhosla/rah/internal/rctx"

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

// Call executes the named flow and returns 1 to move to the next parent step.
func (r *FlowRegistry) Call(name string, ctx *rctx.Context) int16 {
	if fn, ok := r.staticFlows[name]; ok {
		return fn(ctx)
	}
	if plan, ok := r.dynamicFlows[name]; ok {
		Execute(ctx, plan, 0)
	}
	return 1
}
