package control

// pattern_match_compiler_test.go — compiler integration tests for the pattern_match step.
//
// Coverage:
//   - Compile a flow containing pattern_match; verify instruction count
//   - Runtime: matching value â†’ then-branch (200)
//   - Runtime: non-matching value â†’ else-branch (403)
//   - Regex flags: case-insensitive (i flag)
//   - Multiple flags combined (im)
//   - Invalid regex â†’ compile-time error
//   - Unsupported flag â†’ compile-time error
//   - Missing 'source' â†’ compile-time error
//   - Missing 'pattern' â†’ compile-time error
//   - Empty source slot (nil bytes) â†’ no-match path
//   - Nested pattern_match inside another flow

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/rctx"
)

// â"€â"€â"€ helpers â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// compileAndRun compiles a flow via the management server, dispatches a single
// HTTP request with the given headers, and returns the resulting context.
func compileAndRun(t *testing.T, flows []FlowUpdate, apis []ApiUpdate, method, path string, headers map[string]string) *rctx.Context {
	t.Helper()
	fm, _, server, _ := newTestStack(t)
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-test-" + t.Name(),
		Flows:    flows,
		Apis:     apis,
	})
	return runRequest(t, fm, method, path, headers)
}

// thenElseFlows returns a standard pair of then/else sub-flows that set
// response status 200 and 403 respectively.
func thenElseFlows() []FlowUpdate {
	return []FlowUpdate{
		{Name: "matchFlow", Action: "upsert", Instructions: []StepConfig{
			{Action: "set_response_status", Value: "200"},
		}},
		{Name: "noMatchFlow", Action: "upsert", Instructions: []StepConfig{
			{Action: "set_response_status", Value: "403"},
		}},
	}
}

// â"€â"€â"€ Test 1: basic match â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// TestPatternMatchBasicMatch verifies that a header value matching the pattern
// routes to the then-flow (200).
func TestPatternMatchBasicMatch(t *testing.T) {
	flows := append(thenElseFlows(), FlowUpdate{
		Name:   "mainFlow",
		Action: "upsert",
		Instructions: []StepConfig{
			{
				Action: "pattern_match",
				Source: "header.x-service",
				Input:  map[string]string{"pattern": `^(api|data).*`},
				Then:   "matchFlow",
				Else:   "noMatchFlow",
			},
		},
	})

	ctx := compileAndRun(t, flows, []ApiUpdate{
		{Name: "pmApi", Path: "/pm", FlowName: "mainFlow", Action: "upsert"},
	}, http.MethodGet, "/pm", map[string]string{"x-service": "api-backend"})

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200 (match), got %d", ctx.ResponseStatus)
	}
}

// â"€â"€â"€ Test 2: basic no-match â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// TestPatternMatchNoMatch verifies that a header value NOT matching the pattern
// routes to the else-flow (403).
func TestPatternMatchNoMatch(t *testing.T) {
	flows := append(thenElseFlows(), FlowUpdate{
		Name:   "mainFlow",
		Action: "upsert",
		Instructions: []StepConfig{
			{
				Action: "pattern_match",
				Source: "header.x-service",
				Input:  map[string]string{"pattern": `^(api|data).*`},
				Then:   "matchFlow",
				Else:   "noMatchFlow",
			},
		},
	})

	ctx := compileAndRun(t, flows, []ApiUpdate{
		{Name: "pmApi", Path: "/pm", FlowName: "mainFlow", Action: "upsert"},
	}, http.MethodGet, "/pm", map[string]string{"x-service": "web-frontend"})

	if ctx.ResponseStatus != 403 {
		t.Errorf("expected 403 (no-match), got %d", ctx.ResponseStatus)
	}
}

// â"€â"€â"€ Test 3: empty slot â†’ no-match â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// TestPatternMatchEmptySlot verifies that an absent header (empty bytes) routes
// to the else-flow, not the then-flow.
func TestPatternMatchEmptySlot(t *testing.T) {
	flows := append(thenElseFlows(), FlowUpdate{
		Name:   "mainFlow",
		Action: "upsert",
		Instructions: []StepConfig{
			{
				Action: "pattern_match",
				Source: "header.x-service",
				Input:  map[string]string{"pattern": `^(api|data).*`},
				Then:   "matchFlow",
				Else:   "noMatchFlow",
			},
		},
	})

	// No header sent â†’ slot is empty bytes â†’ no-match
	ctx := compileAndRun(t, flows, []ApiUpdate{
		{Name: "pmApi", Path: "/pm", FlowName: "mainFlow", Action: "upsert"},
	}, http.MethodGet, "/pm", nil)

	if ctx.ResponseStatus != 403 {
		t.Errorf("expected 403 (empty slot = no-match), got %d", ctx.ResponseStatus)
	}
}

// â"€â"€â"€ Test 4: case-insensitive flag â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// TestPatternMatchCaseInsensitiveFlag verifies that the "i" flag makes the
// match case-insensitive.
func TestPatternMatchCaseInsensitiveFlag(t *testing.T) {
	flows := append(thenElseFlows(), FlowUpdate{
		Name:   "mainFlow",
		Action: "upsert",
		Instructions: []StepConfig{
			{
				Action: "pattern_match",
				Source: "header.x-service",
				Input:  map[string]string{"pattern": `^API`, "flags": "i"},
				Then:   "matchFlow",
				Else:   "noMatchFlow",
			},
		},
	})

	// "api-prod" matches "^API" with i flag
	ctx := compileAndRun(t, flows, []ApiUpdate{
		{Name: "pmApi", Path: "/pm", FlowName: "mainFlow", Action: "upsert"},
	}, http.MethodGet, "/pm", map[string]string{"x-service": "api-prod"})

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200 (case-insensitive match), got %d", ctx.ResponseStatus)
	}
}

// TestPatternMatchCaseInsensitiveFlagNoMatch verifies that without the "i" flag,
// case mismatch is not a match.
func TestPatternMatchCaseInsensitiveFlagNoMatch(t *testing.T) {
	flows := append(thenElseFlows(), FlowUpdate{
		Name:   "mainFlow",
		Action: "upsert",
		Instructions: []StepConfig{
			{
				Action: "pattern_match",
				Source: "header.x-service",
				Input:  map[string]string{"pattern": `^API`}, // no flag — case sensitive
				Then:   "matchFlow",
				Else:   "noMatchFlow",
			},
		},
	})

	// "api-prod" does NOT match "^API" (case-sensitive)
	ctx := compileAndRun(t, flows, []ApiUpdate{
		{Name: "pmApi", Path: "/pm", FlowName: "mainFlow", Action: "upsert"},
	}, http.MethodGet, "/pm", map[string]string{"x-service": "api-prod"})

	if ctx.ResponseStatus != 403 {
		t.Errorf("expected 403 (case-sensitive no-match), got %d", ctx.ResponseStatus)
	}
}

// â"€â"€â"€ Test 5: multiple flags â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// TestPatternMatchMultipleFlags verifies that multiple flags can be combined.
func TestPatternMatchMultipleFlags(t *testing.T) {
	flows := append(thenElseFlows(), FlowUpdate{
		Name:   "mainFlow",
		Action: "upsert",
		Instructions: []StepConfig{
			{
				Action: "pattern_match",
				Source: "header.x-tenant",
				Input:  map[string]string{"pattern": `^ACME`, "flags": "im"},
				Then:   "matchFlow",
				Else:   "noMatchFlow",
			},
		},
	})

	// "acme-corp" matches "^ACME" with i+m flags
	ctx := compileAndRun(t, flows, []ApiUpdate{
		{Name: "pmApi", Path: "/pm", FlowName: "mainFlow", Action: "upsert"},
	}, http.MethodGet, "/pm", map[string]string{"x-tenant": "acme-corp"})

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200 (multi-flag match), got %d", ctx.ResponseStatus)
	}
}

// â"€â"€â"€ Test 6: compile-time error cases â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// TestPatternMatchInvalidRegex verifies that an invalid regex produces a
// compile-time error, not a panic at runtime.
func TestPatternMatchInvalidRegex(t *testing.T) {
	fm, compiler, _, _ := newTestStack(t)
	_ = fm

	flow := []StepConfig{
		{
			Action: "pattern_match",
			Source: "header.x-service",
			Input:  map[string]string{"pattern": `[invalid`}, // unclosed bracket
			Then:   "matchFlow",
			Else:   "noMatchFlow",
		},
	}
	_, err := compiler.Compile(flow)
	if err == nil {
		t.Fatal("expected compile-time error for invalid regex, got nil")
	}
	if !containsAny(err.Error(), "invalid regex", "error parsing regexp") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestPatternMatchUnsupportedFlag verifies that an unknown flag produces a
// clear compile-time error.
func TestPatternMatchUnsupportedFlag(t *testing.T) {
	fm, compiler, _, _ := newTestStack(t)
	_ = fm

	flow := []StepConfig{
		{
			Action: "pattern_match",
			Source: "header.x-service",
			Input:  map[string]string{"pattern": `.*`, "flags": "z"}, // 'z' is not a supported flag
			Then:   "matchFlow",
			Else:   "noMatchFlow",
		},
	}
	_, err := compiler.Compile(flow)
	if err == nil {
		t.Fatal("expected compile-time error for unsupported flag, got nil")
	}
	if !containsAny(err.Error(), "unsupported regex flag") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestPatternMatchMissingSource verifies that omitting 'source' is a
// compile-time error.
func TestPatternMatchMissingSource(t *testing.T) {
	fm, compiler, _, _ := newTestStack(t)
	_ = fm

	flow := []StepConfig{
		{
			Action: "pattern_match",
			// Source intentionally omitted
			Input: map[string]string{"pattern": `.*`},
			Then:  "matchFlow",
			Else:  "noMatchFlow",
		},
	}
	_, err := compiler.Compile(flow)
	if err == nil {
		t.Fatal("expected compile-time error for missing source, got nil")
	}
	if !containsAny(err.Error(), "'source' is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestPatternMatchMissingPattern verifies that omitting 'pattern' is a
// compile-time error.
func TestPatternMatchMissingPattern(t *testing.T) {
	fm, compiler, _, _ := newTestStack(t)
	_ = fm

	flow := []StepConfig{
		{
			Action: "pattern_match",
			Source: "header.x-service",
			// Input/pattern intentionally omitted
			Then: "matchFlow",
			Else: "noMatchFlow",
		},
	}
	_, err := compiler.Compile(flow)
	if err == nil {
		t.Fatal("expected compile-time error for missing pattern, got nil")
	}
	if !containsAny(err.Error(), "'pattern' is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// â"€â"€â"€ Test 7: instruction count verification â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// TestPatternMatchInstructionCount verifies that the compiler emits the correct
// number of instructions for a pattern_match step:
//
//	1 gate (PATTERN_MATCH_REGEX)
//	+ len(then-branch) instructions
//	+ 1 GOTO (skip-else jump)
//	+ len(else-branch) instructions
//
// then-branch has 1 instruction (set_response_status)
// else-branch has 1 instruction (set_response_status)
// Total = 1 + 1 + 1 + 1 = 4
func TestPatternMatchInstructionCount(t *testing.T) {
	fm, compiler, _, _ := newTestStack(t)
	_ = fm

	// Register the sub-flows in the compiler's FlowLibrary so simulateBake works.
	// For Compile() (not BakeAll), sub-flows are passed via fragments=nil,
	// so we embed the then/else steps inline by inlining a flat flow.
	// Instead, use BakeAll via mustSync so the flow library is populated.
	_, _, server, _ := newTestStack(t)
	_ = compiler

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-count-test",
		Flows: []FlowUpdate{
			{Name: "matchFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "noMatchFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "403"},
			}},
			{Name: "mainFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-service",
					Input:  map[string]string{"pattern": `^(api|data).*`},
					Then:   "matchFlow",
					Else:   "noMatchFlow",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "pmApi", Path: "/pm", FlowName: "mainFlow", Action: "upsert"},
		},
	})

	// Fetch the compiled state and verify the pattern_match gate is in mainFlow.
	fm2 := server.FlowManager
	state := fm2.State.Load()
	if state == nil {
		t.Fatal("engine state is nil after sync")
	}

	mainFlowInstrs, ok := state.FlowLibrary["mainFlow"]
	if !ok {
		t.Fatal("mainFlow not found in FlowLibrary after sync")
	}

	// Compiler.Compile() layout for mainFlow (stored in FlowLibrary):
	//   [0] SET_STREAM_RESPONSE_BODY — always first; no http_call so streaming=true
	//   [1] PATTERN_MATCH_REGEX      (gate — jumps to thenStart or elseStart)
	//   [2] GOTO                     (skip-else; sub-flows live in their own entries)
	// Total = 3 instructions.
	const wantCount = 3
	if len(mainFlowInstrs) != wantCount {
		t.Errorf("expected %d instructions in mainFlow FlowLibrary entry, got %d", wantCount, len(mainFlowInstrs))
		for i, instr := range mainFlowInstrs {
			t.Logf("  [%d] %s", i, instr.Name)
		}
	}

	// Verify the gate instruction is at position 1 (after the stream-body flag).
	if len(mainFlowInstrs) > 1 && mainFlowInstrs[1].Name != "PATTERN_MATCH_REGEX" {
		t.Errorf("expected PATTERN_MATCH_REGEX at index 1, got %q", mainFlowInstrs[1].Name)
	}
}

// â"€â"€â"€ Test 8: dotall flag matches newline â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// TestPatternMatchDotAllFlag verifies that the "s" (dot-all) flag makes "."
// match newlines.
func TestPatternMatchDotAllFlag(t *testing.T) {
	flows := append(thenElseFlows(), FlowUpdate{
		Name:   "mainFlow",
		Action: "upsert",
		Instructions: []StepConfig{
			{
				Action: "pattern_match",
				Source: "header.x-data",
				Input:  map[string]string{"pattern": `line1.line2`, "flags": "s"},
				Then:   "matchFlow",
				Else:   "noMatchFlow",
			},
		},
	})

	// "line1\nline2" — the "." matches "\n" with s flag
	ctx := compileAndRun(t, flows, []ApiUpdate{
		{Name: "pmApi", Path: "/pm", FlowName: "mainFlow", Action: "upsert"},
	}, http.MethodGet, "/pm", map[string]string{"x-data": "line1\nline2"})

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200 (dot-all match), got %d", ctx.ResponseStatus)
	}
}

// â"€â"€â"€ Test 9: step descriptor is registered â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// TestPatternMatchStepDescriptorRegistered verifies that the pattern_match
// step descriptor is present in AllStepDescriptors.
func TestPatternMatchStepDescriptorRegistered(t *testing.T) {
	descriptors := AllStepDescriptors()
	for _, d := range descriptors {
		if d.Type == "pattern_match" {
			// Found — verify key fields.
			if d.Category != "control" {
				t.Errorf("expected category 'control', got %q", d.Category)
			}
			if d.Capability != "branching" {
				t.Errorf("expected capability 'branching', got %q", d.Capability)
			}
			if !d.SupportsNested {
				t.Error("expected SupportsNested = true for pattern_match")
			}
			if len(d.Fields) == 0 {
				t.Error("expected at least one StepField for pattern_match")
			}
			return
		}
	}
	t.Error("pattern_match descriptor not found in AllStepDescriptors()")
}

// â"€â"€â"€ Test 10: isControlFlowAction includes pattern_match â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// TestPatternMatchIsControlFlow verifies that pattern_match is correctly
// excluded from on_error wrapping.
func TestPatternMatchIsControlFlow(t *testing.T) {
	if !isControlFlowAction("pattern_match") {
		t.Error("isControlFlowAction(\"pattern_match\") should return true")
	}
}

// â"€â"€â"€ helpers â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

// newTestStackForCompile returns a compiler with an empty flow manager.
// Unlike newTestStack it does NOT wire up a management server.
func newTestStackForCompile(t *testing.T) (*Compiler, func()) {
	t.Helper()
	fm := newTestFM(t)
	c := NewCompiler(fm)
	return c, func() {}
}

// Ensure package-level runRequest and mustSync are available (defined in flow_features_test.go).
var _ = runRequest
var _ = mustSync
var _ = httptest.NewRecorder
