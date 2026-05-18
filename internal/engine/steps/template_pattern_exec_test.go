package steps

import (
	"testing"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// newCtx returns an initialised Context with BaseByteSlots slots.
func newCtx() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.InitSlots()
	return ctx
}

// newState returns an ExecutionState at PC 3 so we can verify "PC+1" returns 4.
func newState(pc int16) *engine.ExecutionState {
	return &engine.ExecutionState{PC: pc}
}

// --- helpers to build CompiledTemplatePattern quickly in tests ----------------

func mustParse(t *testing.T, pattern string, slotMap map[string]int, allocSlot func(string) (int, error)) *CompiledTemplatePattern {
	t.Helper()
	ctp, err := ParseTemplatePattern(pattern, slotMap, allocSlot)
	if err != nil {
		t.Fatalf("ParseTemplatePattern(%q) error: %v", pattern, err)
	}
	return ctp
}

// ---------------------------------------------------------------------------
// ValidateTemplatePattern tests
// ---------------------------------------------------------------------------

func TestValidateTemplatePattern_ExactMatch(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp := mustParse(t, "exact_value", nil, alloc)

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("exact_value")
	srcSlot, resultSlot := 0, 1

	instr := ValidateTemplatePattern(srcSlot, ctp, resultSlot)
	next := instr.Action(ctx, newState(5))

	if next != 6 {
		t.Errorf("expected PC+1=6, got %d", next)
	}
	if len(ctx.ByteSlots[resultSlot]) == 0 {
		t.Error("expected truthy result ([]byte{1}), got nil/empty")
	}
}

func TestValidateTemplatePattern_ExactNoMatch(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp := mustParse(t, "exact_value", nil, alloc)

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("other_value")
	srcSlot, resultSlot := 0, 1

	instr := ValidateTemplatePattern(srcSlot, ctp, resultSlot)
	instr.Action(ctx, newState(0))

	if ctx.ByteSlots[resultSlot] != nil {
		t.Errorf("expected nil result on no-match, got %v", ctx.ByteSlots[resultSlot])
	}
}

func TestValidateTemplatePattern_PrefixMatch(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp := mustParse(t, "internal_*", nil, alloc)

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("internal_service_a")
	srcSlot, resultSlot := 0, 1

	instr := ValidateTemplatePattern(srcSlot, ctp, resultSlot)
	instr.Action(ctx, newState(0))

	if len(ctx.ByteSlots[resultSlot]) == 0 {
		t.Error("expected truthy result for prefix match")
	}
}

func TestValidateTemplatePattern_PrefixNoMatch(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp := mustParse(t, "internal_*", nil, alloc)

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("external_service")
	srcSlot, resultSlot := 0, 1

	instr := ValidateTemplatePattern(srcSlot, ctp, resultSlot)
	instr.Action(ctx, newState(0))

	if ctx.ByteSlots[resultSlot] != nil {
		t.Errorf("expected nil on prefix no-match, got %v", ctx.ByteSlots[resultSlot])
	}
}

func TestValidateTemplatePattern_SuffixMatch(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp := mustParse(t, "*_prod", nil, alloc)

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("service_a_prod")
	srcSlot, resultSlot := 0, 1

	instr := ValidateTemplatePattern(srcSlot, ctp, resultSlot)
	instr.Action(ctx, newState(0))

	if len(ctx.ByteSlots[resultSlot]) == 0 {
		t.Error("expected truthy result for suffix match")
	}
}

func TestValidateTemplatePattern_PrefixSuffixExtract_Match(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	// Pattern: literal_prefix + (capture) + literal_suffix → StrategyPrefixSuffixExtract
	ctp := mustParse(t, "pre_(cap)_suf", nil, alloc)

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("pre_hello_suf")
	srcSlot, resultSlot := 0, 2

	instr := ValidateTemplatePattern(srcSlot, ctp, resultSlot)
	instr.Action(ctx, newState(0))

	if len(ctx.ByteSlots[resultSlot]) == 0 {
		t.Error("expected truthy result for PrefixSuffixExtract match")
	}
}

func TestValidateTemplatePattern_PrefixSuffixExtract_NoMatch(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp := mustParse(t, "pre_(cap)_suf", nil, alloc)

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("pre_hello_WRONG")
	srcSlot, resultSlot := 0, 2

	instr := ValidateTemplatePattern(srcSlot, ctp, resultSlot)
	instr.Action(ctx, newState(0))

	if ctx.ByteSlots[resultSlot] != nil {
		t.Errorf("expected nil on no-match, got %v", ctx.ByteSlots[resultSlot])
	}
}

func TestValidateTemplatePattern_PrefixSlotRefExtract_Match(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	// Pre-declare "ref" slot at index 3 in slotMap
	slotMap := map[string]int{"ref": 3}
	ctp := mustParse(t, "pre_(cap)_{ref}", slotMap, alloc)

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("pre_hello_world") // srcSlot
	ctx.ByteSlots[3] = []byte("world")           // ref slot value at runtime
	srcSlot, resultSlot := 0, 4

	instr := ValidateTemplatePattern(srcSlot, ctp, resultSlot)
	instr.Action(ctx, newState(0))

	if len(ctx.ByteSlots[resultSlot]) == 0 {
		t.Error("expected truthy result for PrefixSlotRefExtract match")
	}
}

func TestValidateTemplatePattern_Sequential_TwoCaptures(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp := mustParse(t, "(a)_(b)", nil, alloc)

	// Verify it selected StrategySequential
	if ctp.Strategy != StrategySequential {
		t.Fatalf("expected StrategySequential, got %v", ctp.Strategy)
	}

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("foo_bar")
	srcSlot, resultSlot := 0, 5

	instr := ValidateTemplatePattern(srcSlot, ctp, resultSlot)
	instr.Action(ctx, newState(0))

	if len(ctx.ByteSlots[resultSlot]) == 0 {
		t.Error("expected truthy result for sequential two-capture match")
	}
}

// ---------------------------------------------------------------------------
// ExtractTemplatePattern tests
// ---------------------------------------------------------------------------

func TestExtractTemplatePattern_CapturesNilOnMismatch(t *testing.T) {
	alloc, sm := newTestAllocSlot()
	ctp := mustParse(t, "pre_(cap)_suf", nil, alloc)
	capSlot := sm["cap"]

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("pre_hello_WRONG") // no match

	instr := ExtractTemplatePattern(0, ctp)
	instr.Action(ctx, newState(0))

	if ctx.ByteSlots[capSlot] != nil {
		t.Errorf("expected capture slot nil on mismatch, got %v", ctx.ByteSlots[capSlot])
	}
}

func TestExtractTemplatePattern_EmptySrcSlot(t *testing.T) {
	alloc, sm := newTestAllocSlot()
	ctp := mustParse(t, "pre_(cap)_suf", nil, alloc)
	capSlot := sm["cap"]

	ctx := newCtx()
	ctx.ByteSlots[0] = nil // empty/nil srcSlot

	instr := ExtractTemplatePattern(0, ctp)
	instr.Action(ctx, newState(0))

	if ctx.ByteSlots[capSlot] != nil {
		t.Errorf("expected capture slot nil for empty srcSlot, got %v", ctx.ByteSlots[capSlot])
	}
}

func TestExtractTemplatePattern_PartialMatchNoCapture(t *testing.T) {
	alloc, sm := newTestAllocSlot()
	ctp := mustParse(t, "pre_(cap)_suf", nil, alloc)
	capSlot := sm["cap"]

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("pre_hello") // prefix matches but no suffix

	instr := ExtractTemplatePattern(0, ctp)
	instr.Action(ctx, newState(0))

	if ctx.ByteSlots[capSlot] != nil {
		t.Errorf("expected nil capture on partial match, got %v", ctx.ByteSlots[capSlot])
	}
}

func TestExtractTemplatePattern_PrefixSuffixExtract_Capture(t *testing.T) {
	alloc, sm := newTestAllocSlot()
	ctp := mustParse(t, "pre_(cap)_suf", nil, alloc)
	capSlot := sm["cap"]

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("pre_hello_suf")

	instr := ExtractTemplatePattern(0, ctp)
	instr.Action(ctx, newState(0))

	if string(ctx.ByteSlots[capSlot]) != "hello" {
		t.Errorf("expected capture 'hello', got %q", ctx.ByteSlots[capSlot])
	}
}

func TestExtractTemplatePattern_Sequential_TwoCaptures(t *testing.T) {
	alloc, sm := newTestAllocSlot()
	ctp := mustParse(t, "(service)_(env)", nil, alloc)

	serviceSlot := sm["service"]
	envSlot := sm["env"]

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("payments_prod")

	instr := ExtractTemplatePattern(0, ctp)
	instr.Action(ctx, newState(0))

	if string(ctx.ByteSlots[serviceSlot]) != "payments" {
		t.Errorf("expected service='payments', got %q", ctx.ByteSlots[serviceSlot])
	}
	if string(ctx.ByteSlots[envSlot]) != "prod" {
		t.Errorf("expected env='prod', got %q", ctx.ByteSlots[envSlot])
	}
}

func TestExtractTemplatePattern_PrefixSlotRef_Capture(t *testing.T) {
	alloc, sm := newTestAllocSlot()
	slotMap := map[string]int{"tenantId": 3}
	ctp := mustParse(t, "internal_(service)_{tenantId}", slotMap, alloc)
	serviceSlot := sm["service"]

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("internal_payments_tenant42")
	ctx.ByteSlots[3] = []byte("tenant42") // tenantId runtime value

	instr := ExtractTemplatePattern(0, ctp)
	instr.Action(ctx, newState(0))

	if string(ctx.ByteSlots[serviceSlot]) != "payments" {
		t.Errorf("expected service='payments', got %q", ctx.ByteSlots[serviceSlot])
	}
}

// ---------------------------------------------------------------------------
// Zero-alloc test for StrategyPrefixSuffixExtract hot path
// ---------------------------------------------------------------------------

func TestValidateTemplatePattern_ZeroAllocs(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp := mustParse(t, "pre_(cap)_suf", nil, alloc)

	if ctp.Strategy != StrategyPrefixSuffixExtract {
		t.Fatalf("expected StrategyPrefixSuffixExtract, got %v", ctp.Strategy)
	}

	ctx := newCtx()
	ctx.ByteSlots[0] = []byte("pre_hello_suf")
	srcSlot, resultSlot := 0, 5

	instr := ValidateTemplatePattern(srcSlot, ctp, resultSlot)
	state := newState(0)

	allocs := testing.AllocsPerRun(100, func() {
		instr.Action(ctx, state)
	})

	if allocs != 0 {
		t.Errorf("expected 0 allocations for PrefixSuffixExtract hot path, got %v", allocs)
	}
}
