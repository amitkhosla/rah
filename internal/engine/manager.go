package engine

import (
	"net/http"
	"rah/internal/api"
	"rah/internal/config"
	"rah/internal/rctx"
	"sync"
)

type FlowManager struct {
	Definitions    []*api.ApiDefinition // Resolved via ApiId
	HeaderRegistry *HeaderRegistry
	Pool           sync.Pool
	Config         config.GlobalLayout
}

func NewFlowManager(maxAPIs int, cfg config.GlobalLayout) *FlowManager {
	fm := &FlowManager{
		HeaderRegistry: NewHeaderRegistry(),
		Config:         cfg,
		Definitions:    make([]*api.ApiDefinition, maxAPIs),
	}

	fm.Pool.New = func() any {
		return &rctx.Context{
			ByteSlots:   make([][]byte, cfg.MaxBytes),
			IntSlots:    make([]int64, cfg.MaxInts),
			BoolSlots:   make([]bool, 8), // Default small set
			MutationLog: make([]rctx.HeaderMutation, 0, 16),
		}
	}
	return fm
}

// ProcessRequest is the Hot-Path orchestrator
func (fm *FlowManager) ProcessRequest(ctx *rctx.Context, req *http.Request) {
	// 1. Snapshot Metadata & Headers
	fm.Extract(ctx, req)

	// 2. Resolve Definition
	if int(ctx.ApiId) >= len(fm.Definitions) {
		ctx.ResponseStatus = 404
		return
	}
	def := fm.Definitions[ctx.ApiId]
	if def == nil {
		ctx.ResponseStatus = 404
		return
	}

	// 3. Zero-Alloc Path Parameter Extraction
	// Uses the static offsets baked during startup
	for _, loc := range def.ParamPositions {
		if loc.StaticPrefixLen >= len(ctx.Path) {
			continue
		}
		val := ctx.Path[loc.StaticPrefixLen:]
		end := 0
		for end < len(val) && val[end] != '/' {
			end++
		}
		ctx.ByteSlots[loc.SlotIdx] = val[:end]
	}

	// 4. Instruction Loop
	if plan, ok := def.Plan.(*Plan); ok {
		for _, instr := range plan.Instructions {
			// 99 is the signal to break (Auth fail, Proxy done, etc)
			if instr.Action(ctx) == 99 {
				break
			}
		}
	}
}

func (fm *FlowManager) Extract(ctx *rctx.Context, req *http.Request) {
	ctx.SnapshotMetadata(req.Method, req.URL.Path, req.URL.RawQuery)

	// Map Headers to Slots using the Atomic Registry
	lookup := fm.HeaderRegistry.Current.Load().Map
	for key, values := range req.Header {
		if slotIdx, ok := lookup[key]; ok {
			ctx.ByteSlots[slotIdx] = []byte(values[0])
		}
	}

	ctx.RequestBody = req.Body
	ctx.MaxBodySize = fm.Config.DefaultLimits.MaxBodySize
}

// Registry helpers to prevent nil pointers
type RegistryMap struct{ Map map[string]int }
