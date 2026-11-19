package steps

import (
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// Regression: contexts from FlowManager.Pool must have a usable MutationLog.
// It was allocated with len 0, so set_request_header silently recorded nothing
// and no flow-set header ever reached an upstream http_call.
func TestSetRequestHeader_RecordsMutationOnPooledContext(t *testing.T) {
	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	ctx := fm.Pool.Get().(*rctx.Context)
	ctx.MutationCount = 0
	ctx.ByteSlots[0] = []byte("hello-from-flow")

	instr := SetRequestHeader("X-Test-Injected", 0)
	state := &engine.ExecutionState{PC: 0}
	instr.Action(ctx, state)

	if ctx.MutationCount != 1 {
		t.Fatalf("MutationCount = %d, want 1 (pooled MutationLog len=%d)", ctx.MutationCount, len(ctx.MutationLog))
	}
	m := ctx.MutationLog[0]
	if string(m.Key) != "X-Test-Injected" || string(m.Value) != "hello-from-flow" {
		t.Fatalf("mutation = %q:%q", m.Key, m.Value)
	}
}

// The fix must not add per-request allocations: the 16-slot log is allocated once
// per pooled context; each SetRequestHeader is an indexed write.
func TestSetRequestHeader_ZeroAllocsOnHotPath(t *testing.T) {
	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	ctx := fm.Pool.Get().(*rctx.Context)
	ctx.ByteSlots[0] = []byte("v")
	instr := SetRequestHeader("X-Test-Injected", 0)
	state := &engine.ExecutionState{PC: 0}

	allocs := testing.AllocsPerRun(1000, func() {
		ctx.MutationCount = 0
		instr.Action(ctx, state)
	})
	if allocs != 0 {
		t.Fatalf("SetRequestHeader allocs/op = %v, want 0", allocs)
	}
}

func BenchmarkSetRequestHeader(b *testing.B) {
	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 16, MaxBoolsSlots: 8})
	ctx := fm.Pool.Get().(*rctx.Context)
	ctx.ByteSlots[0] = []byte("v")
	instr := SetRequestHeader("X-Test-Injected", 0)
	state := &engine.ExecutionState{PC: 0}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx.MutationCount = 0
		instr.Action(ctx, state)
	}
}
