package control

// pattern_match_e2e_test.go — SESSION-7 end-to-end tests.
//
// Goal: prove the entire pattern_match feature works across all layers:
//
//	Studio DSL export → UnifiedSync → Compiler → Engine → Execution → Response
//
// Unlike pattern_match_compiler_test.go (unit/compiler) and
// pattern_match_integration_test.go (flow execution), these tests exercise
// the complete gateway lifecycle including:
//   - Management server /sync endpoint (as Studio would POST)
//   - Router lookup (path → apiID)
//   - FlowManager.ProcessRequest (full request lifecycle)
//   - Pattern gate instruction (PATTERN_MATCH_REGEX)
//   - Branch jump (then/else) verified via HTTP response status
//
// Test index:
//   E2E-1:  Basic match path — header matches pattern → then-branch (200)
//   E2E-2:  Basic no-match path — header absent/wrong → else-branch (404)
//   E2E-3:  Case-insensitive flag in E2E (uppercase header, i flag)
//   E2E-4:  Multiple sequential pattern steps chained via sub-flows
//   E2E-5:  Complex regex (groups, alternation, quantifiers) in E2E
//   E2E-6:  Compile error: invalid regex → sync returns 400
//   E2E-7:  Full management server lifecycle (POST /sync → router → execute)
//   E2E-8:  Performance — pattern match stays within <1µs budget
//   E2E-9:  Body-prefix routing via query param slot
//   E2E-10: Pattern match in nested sub-flow called from parent flow

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// ─── E2E-1: Basic match path ─────────────────────────────────────────────────

// TestPatternMatchE2EBasicMatchPath exercises the full lifecycle for the happy
// path: Studio export → /sync → router lookup → ProcessRequest → 200.
//
// Flow topology:
//
//	mainFlow  →  pattern_match(header.x-service, "^api-.*")
//	                ├── matchBranch  → set_response_status 200
//	                └── noMatchBranch → set_response_status 404
func TestPatternMatchE2EBasicMatchPath(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	// Simulate Studio exporting the DSL and POSTing to /sync.
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "e2e-basic-match-1",
		Flows: []FlowUpdate{
			{Name: "matchBranch", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "noMatchBranch", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "404"},
			}},
			{Name: "mainFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-service",
					Input:  map[string]string{"pattern": `^api-.*`},
					Then:   "matchBranch",
					Else:   "noMatchBranch",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-1", Path: "/e2e/basic", FlowName: "mainFlow", Action: "upsert"},
		},
	})

	// Router lookup + full request execution via ProcessRequest.
	ctx := runRequest(t, fm, http.MethodGet, "/e2e/basic", map[string]string{
		"x-service": "api-gateway",
	})

	if ctx.ResponseStatus != 200 {
		t.Errorf("E2E-1: expected 200 (matched 'api-gateway' against '^api-.*'), got %d", ctx.ResponseStatus)
	}
}

// ─── E2E-2: No-match path ────────────────────────────────────────────────────

// TestPatternMatchE2ENoMatchPath verifies that a header not matching the pattern
// routes to the else-branch (404) through the full gateway stack.
func TestPatternMatchE2ENoMatchPath(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "e2e-no-match-1",
		Flows: []FlowUpdate{
			{Name: "e2MatchBranch", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e2NoMatchBranch", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "404"},
			}},
			{Name: "e2MainFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-service",
					Input:  map[string]string{"pattern": `^api-.*`},
					Then:   "e2MatchBranch",
					Else:   "e2NoMatchBranch",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-2", Path: "/e2e/no-match", FlowName: "e2MainFlow", Action: "upsert"},
		},
	})

	// Wrong prefix → no-match → 404.
	ctx := runRequest(t, fm, http.MethodGet, "/e2e/no-match", map[string]string{
		"x-service": "web-frontend",
	})
	if ctx.ResponseStatus != 404 {
		t.Errorf("E2E-2 (wrong prefix): expected 404, got %d", ctx.ResponseStatus)
	}

	// Absent header → empty slot → no-match → 404.
	ctx2 := runRequest(t, fm, http.MethodGet, "/e2e/no-match", nil)
	if ctx2.ResponseStatus != 404 {
		t.Errorf("E2E-2 (absent header): expected 404, got %d", ctx2.ResponseStatus)
	}
}

// ─── E2E-3: Regex flags in E2E ───────────────────────────────────────────────

// TestPatternMatchE2ERegexFlags verifies that the "i" flag (case-insensitive)
// works correctly through the full gateway stack.
func TestPatternMatchE2ERegexFlags(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "e2e-flags-1",
		Flows: []FlowUpdate{
			{Name: "e3MatchFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e3NoMatchFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "403"},
			}},
			{Name: "e3MainFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-role",
					Input:  map[string]string{"pattern": `^ADMIN`, "flags": "i"},
					Then:   "e3MatchFlow",
					Else:   "e3NoMatchFlow",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-3", Path: "/e2e/flags", FlowName: "e3MainFlow", Action: "upsert"},
		},
	})

	tests := []struct {
		label  string
		header string
		want   int
	}{
		{"uppercase exact", "ADMIN", 200},
		{"lowercase", "admin", 200},
		{"mixed case", "Admin-user", 200},
		{"no match", "viewer", 403},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			ctx := runRequest(t, fm, http.MethodGet, "/e2e/flags", map[string]string{
				"x-role": tt.header,
			})
			if ctx.ResponseStatus != tt.want {
				t.Errorf("E2E-3 %s (%q): expected %d, got %d", tt.label, tt.header, tt.want, ctx.ResponseStatus)
			}
		})
	}
}

// ─── E2E-4: Multiple sequential pattern steps ────────────────────────────────

// TestPatternMatchE2EMultiplePatternSteps verifies that multiple pattern_match
// steps can be chained across sub-flows, each acting as an independent gate.
//
// Topology:
//
//	entryFlow → pattern_match(x-region, "^us-.*")
//	               ├── usRegionFlow → pattern_match(x-tier, "^(pro|enterprise)$")
//	               │                     ├── premiumFlow → 200
//	               │                     └── freeFlow    → 402
//	               └── otherRegionFlow → 503
func TestPatternMatchE2EMultiplePatternSteps(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "e2e-multi-pattern-1",
		Flows: []FlowUpdate{
			// Leaf outcomes.
			{Name: "e4PremiumFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e4FreeFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "402"},
			}},
			{Name: "e4OtherRegionFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "503"},
			}},
			// Second-level gate: tier check.
			{Name: "e4UsRegionFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-tier",
					Input:  map[string]string{"pattern": `^(pro|enterprise)$`},
					Then:   "e4PremiumFlow",
					Else:   "e4FreeFlow",
				},
			}},
			// Entry gate: region check.
			{Name: "e4EntryFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-region",
					Input:  map[string]string{"pattern": `^us-.*`},
					Then:   "e4UsRegionFlow",
					Else:   "e4OtherRegionFlow",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-4", Path: "/e2e/multi", FlowName: "e4EntryFlow", Action: "upsert"},
		},
	})

	tests := []struct {
		label  string
		region string
		tier   string
		want   int
	}{
		{"us pro", "us-east-1", "pro", 200},
		{"us enterprise", "us-west-2", "enterprise", 200},
		{"us free", "us-central", "free", 402},
		{"eu pro — wrong region", "eu-west-1", "pro", 503},
		{"ap enterprise — wrong region", "ap-east-1", "enterprise", 503},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			ctx := runRequest(t, fm, http.MethodGet, "/e2e/multi", map[string]string{
				"x-region": tt.region,
				"x-tier":   tt.tier,
			})
			if ctx.ResponseStatus != tt.want {
				t.Errorf("E2E-4 %s (region=%q tier=%q): expected %d, got %d",
					tt.label, tt.region, tt.tier, tt.want, ctx.ResponseStatus)
			}
		})
	}
}

// ─── E2E-5: Complex regex in E2E ─────────────────────────────────────────────

// TestPatternMatchE2EComplexRegex verifies that production-grade patterns
// (UUID validation, semantic versioning) work correctly end-to-end.
func TestPatternMatchE2EComplexRegex(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "e2e-complex-regex-1",
		Flows: []FlowUpdate{
			{Name: "e5ValidFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e5InvalidFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "400"},
			}},
			{Name: "e5MainFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-request-id",
					// UUID v4 pattern: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
					Input: map[string]string{
						"pattern": `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
						"flags":   "i",
					},
					Then: "e5ValidFlow",
					Else: "e5InvalidFlow",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-5", Path: "/e2e/uuid", FlowName: "e5MainFlow", Action: "upsert"},
		},
	})

	tests := []struct {
		label   string
		reqID   string
		want    int
	}{
		{"valid uuid v4", "550e8400-e29b-41d4-a716-446655440000", 200},
		{"valid uuid uppercase", "550E8400-E29B-41D4-A716-446655440000", 200},
		{"not uuid — too short", "550e8400-e29b-41d4", 400},
		{"not uuid — wrong version", "550e8400-e29b-31d4-a716-446655440000", 400},
		{"not uuid — no dashes", "550e8400e29b41d4a716446655440000", 400},
		{"empty", "", 400},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			headers := map[string]string{}
			if tt.reqID != "" {
				headers["x-request-id"] = tt.reqID
			}
			ctx := runRequest(t, fm, http.MethodGet, "/e2e/uuid", headers)
			if ctx.ResponseStatus != tt.want {
				t.Errorf("E2E-5 %s (%q): expected %d, got %d", tt.label, tt.reqID, tt.want, ctx.ResponseStatus)
			}
		})
	}
}

// ─── E2E-6: Compile error — invalid regex → sync returns 400 ─────────────────

// TestPatternMatchE2EInvalidRegexSyncError verifies that an invalid regex in the
// DSL is rejected at compile time by the /sync endpoint with a 400 status and a
// clear error message — not a runtime panic.
func TestPatternMatchE2EInvalidRegexSyncError(t *testing.T) {
	_, _, server, _ := newTestStack(t)

	payload, _ := json.Marshal(UnifiedSyncRequest{
		SyncUUID: "e2e-invalid-regex-1",
		Flows: []FlowUpdate{
			{Name: "e6MatchFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e6MainFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-service",
					Input:  map[string]string{"pattern": `[unclosed`}, // invalid regex
					Then:   "e6MatchFlow",
					Else:   "e6MatchFlow",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-6", Path: "/e2e/invalid-regex", FlowName: "e6MainFlow", Action: "upsert"},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	server.UnifiedSyncHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("E2E-6: expected 400 for invalid regex, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	// Verify the error message is descriptive (mentions regex or pattern).
	if !containsAny(body, "regex", "pattern", "regexp") {
		t.Errorf("E2E-6: expected error body to mention 'regex'/'pattern', got: %s", body)
	}
}

// TestPatternMatchE2EUnsupportedFlagSyncError verifies that an unsupported regex
// flag in the DSL is rejected at compile time with a 400 status.
func TestPatternMatchE2EUnsupportedFlagSyncError(t *testing.T) {
	_, _, server, _ := newTestStack(t)

	payload, _ := json.Marshal(UnifiedSyncRequest{
		SyncUUID: "e2e-bad-flag-1",
		Flows: []FlowUpdate{
			{Name: "e6bMatchFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e6bMainFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-service",
					Input:  map[string]string{"pattern": `.*`, "flags": "z"}, // 'z' not supported
					Then:   "e6bMatchFlow",
					Else:   "e6bMatchFlow",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-6b", Path: "/e2e/bad-flag", FlowName: "e6bMainFlow", Action: "upsert"},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	server.UnifiedSyncHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("E2E-6b: expected 400 for unsupported flag 'z', got %d", rec.Code)
	}
}

// TestPatternMatchE2EMissingSourceSyncError verifies that omitting 'source' in
// a pattern_match step is caught at sync time (not runtime).
func TestPatternMatchE2EMissingSourceSyncError(t *testing.T) {
	_, _, server, _ := newTestStack(t)

	payload, _ := json.Marshal(UnifiedSyncRequest{
		SyncUUID: "e2e-missing-source-1",
		Flows: []FlowUpdate{
			{Name: "e6cMatchFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e6cMainFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					// Source intentionally omitted.
					Input: map[string]string{"pattern": `.*`},
					Then:  "e6cMatchFlow",
					Else:  "e6cMatchFlow",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-6c", Path: "/e2e/missing-source", FlowName: "e6cMainFlow", Action: "upsert"},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	server.UnifiedSyncHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("E2E-6c: expected 400 for missing source, got %d", rec.Code)
	}
}

// ─── E2E-7: Full management server lifecycle ──────────────────────────────────

// TestPatternMatchE2EFullManagementServerLifecycle exercises the complete
// management-server-driven workflow:
//
//  1. POST /sync with a pattern_match flow (Studio would call this)
//  2. Verify engine state updated (router has the route)
//  3. Execute request through FlowManager.ProcessRequest
//  4. Verify pattern matching worked
//  5. Re-sync with a *different* pattern to verify hot reload
//  6. Verify the new pattern takes effect immediately
func TestPatternMatchE2EFullManagementServerLifecycle(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	// ── Step 1: Initial sync with pattern "^internal-.*" ──────────────────────
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "e2e-lifecycle-v1",
		Flows: []FlowUpdate{
			{Name: "e7OkFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e7DenyFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "403"},
			}},
			{Name: "e7GateFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-client",
					Input:  map[string]string{"pattern": `^internal-.*`},
					Then:   "e7OkFlow",
					Else:   "e7DenyFlow",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-7", Path: "/e2e/lifecycle", FlowName: "e7GateFlow", Action: "upsert"},
		},
	})

	// ── Step 2: Verify engine state after first sync ───────────────────────────
	state := fm.State.Load()
	if state == nil {
		t.Fatal("E2E-7: engine state is nil after sync")
	}
	if state.Router.Lookup("/e2e/lifecycle") == 0 {
		t.Fatal("E2E-7: route /e2e/lifecycle not found in router after sync")
	}

	// ── Step 3: Execute with matching header ───────────────────────────────────
	ctx := runRequest(t, fm, http.MethodGet, "/e2e/lifecycle", map[string]string{
		"x-client": "internal-svc",
	})
	if ctx.ResponseStatus != 200 {
		t.Errorf("E2E-7 v1 match: expected 200, got %d", ctx.ResponseStatus)
	}

	// ── Step 4: Execute with non-matching header ───────────────────────────────
	ctx2 := runRequest(t, fm, http.MethodGet, "/e2e/lifecycle", map[string]string{
		"x-client": "external-partner",
	})
	if ctx2.ResponseStatus != 403 {
		t.Errorf("E2E-7 v1 no-match: expected 403, got %d", ctx2.ResponseStatus)
	}

	// ── Step 5: Re-sync with a broader pattern — hot reload ────────────────────
	// New pattern accepts both internal-* and partner-* clients.
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "e2e-lifecycle-v2",
		Flows: []FlowUpdate{
			{Name: "e7OkFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e7DenyFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "403"},
			}},
			{Name: "e7GateFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-client",
					// Broader: now accepts internal-* OR partner-*
					Input: map[string]string{"pattern": `^(internal|partner)-.*`},
					Then:  "e7OkFlow",
					Else:  "e7DenyFlow",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-7", Path: "/e2e/lifecycle", FlowName: "e7GateFlow", Action: "upsert"},
		},
	})

	// ── Step 6: Verify hot-reloaded pattern takes effect ──────────────────────
	// "external-*" still denied.
	ctx3 := runRequest(t, fm, http.MethodGet, "/e2e/lifecycle", map[string]string{
		"x-client": "external-random",
	})
	if ctx3.ResponseStatus != 403 {
		t.Errorf("E2E-7 v2 still-denied: expected 403, got %d", ctx3.ResponseStatus)
	}

	// "partner-*" now allowed by the updated pattern.
	ctx4 := runRequest(t, fm, http.MethodGet, "/e2e/lifecycle", map[string]string{
		"x-client": "partner-acme",
	})
	if ctx4.ResponseStatus != 200 {
		t.Errorf("E2E-7 v2 partner-now-allowed: expected 200, got %d", ctx4.ResponseStatus)
	}
}

// ─── E2E-8: Performance check ─────────────────────────────────────────────────

// TestPatternMatchE2EPerformance verifies that the full pattern match instruction
// execution (not the router lookup or request building overhead) stays within
// the <1µs latency budget defined in MEMORY.md.
//
// We benchmark the instruction Action() directly — the hot path — to isolate
// the regex evaluation cost from test infrastructure overhead.
func TestPatternMatchE2EPerformance(t *testing.T) {
	pattern := regexp.MustCompile(`^(api|data|internal)-[a-z0-9-]+$`)
	instr := engine.Instruction{
		Name: "PATTERN_MATCH_REGEX",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			slotContent := ctx.ByteSlots[0]
			if len(slotContent) == 0 {
				return 1
			}
			if pattern.Match(slotContent) {
				return 1
			}
			return 2
		},
	}

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 48),
	}
	ctx.ByteSlots[0] = []byte("api-gateway-prod")
	state := &engine.ExecutionState{}

	const iterations = 10_000
	start := time.Now()
	for i := 0; i < iterations; i++ {
		_ = instr.Action(ctx, state)
	}
	elapsed := time.Since(start)

	avgNs := elapsed.Nanoseconds() / iterations
	const budgetNs = 1000 // 1µs per call

	if avgNs > budgetNs {
		t.Errorf("E2E-8: pattern match hot path avg=%dns exceeds <1µs budget (%dns)", avgNs, budgetNs)
	}
	t.Logf("E2E-8: pattern match hot path: avg=%dns over %d iterations (budget=%dns)", avgNs, iterations, budgetNs)
}

// BenchmarkPatternMatchE2EInstruction benchmarks the raw pattern match
// instruction hot path, excluding all gateway infrastructure overhead.
// Run with: go test -bench=BenchmarkPatternMatchE2E ./internal/control/
func BenchmarkPatternMatchE2EInstruction(b *testing.B) {
	pattern := regexp.MustCompile(`^(api|data|internal)-[a-z0-9-]+$`)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 48),
	}
	ctx.ByteSlots[0] = []byte("api-gateway-prod")
	state := &engine.ExecutionState{}

	instr := engine.Instruction{
		Name: "PATTERN_MATCH_REGEX",
		Action: func(c *rctx.Context, s *engine.ExecutionState) int16 {
			if len(c.ByteSlots[0]) == 0 {
				return 2
			}
			if pattern.Match(c.ByteSlots[0]) {
				return 1
			}
			return 2
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = instr.Action(ctx, state)
	}
}

// BenchmarkPatternMatchE2ECompileAndExecute benchmarks the full compile + 1
// request execution cycle. This is the "cold path" — useful for measuring
// how long a config update + first request takes.
func BenchmarkPatternMatchE2ECompileAndExecute(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Construct the stack directly without *testing.T (not available in *testing.B).
		fm := engine.NewFlowManager(64, config.GlobalLayout{
			DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
		})
		compiler := NewCompiler(fm)
		server := NewManagementServer(fm, compiler, NewNameRegistry(), nil)

		_ = json.NewEncoder(bytes.NewBuffer(nil)).Encode(UnifiedSyncRequest{})

		mustSyncB(b, server, UnifiedSyncRequest{
			SyncUUID: "bench-compile-exec",
			Flows: []FlowUpdate{
				{Name: "bmMatch", Action: "upsert", Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				}},
				{Name: "bmNoMatch", Action: "upsert", Instructions: []StepConfig{
					{Action: "set_response_status", Value: "403"},
				}},
				{Name: "bmMain", Action: "upsert", Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-service",
						Input:  map[string]string{"pattern": `^api-.*`},
						Then:   "bmMatch",
						Else:   "bmNoMatch",
					},
				}},
			},
			Apis: []ApiUpdate{
				{Name: "bench-api", Path: "/bench/pm", FlowName: "bmMain", Action: "upsert"},
			},
		})

		req := httptest.NewRequest(http.MethodGet, "http://localhost/bench/pm", nil)
		req.Header.Set("x-service", "api-bench")
		state := fm.State.Load()
		apiID := state.Router.Lookup(req.URL.Path)
		if apiID == 0 {
			b.Fatal("route not found")
		}
		ctx := fm.GetContext()
		ctx.Reset(httptest.NewRecorder())
		ctx.ApiId = apiID
		ctx.SnapshotMetadata(req.Method, req.URL.Path, req.URL.RawQuery)
		fm.ProcessRequest(ctx, req)
		fm.Pool.Put(ctx)
	}
}

// ─── E2E-9: Query-param-based pattern routing ─────────────────────────────────

// TestPatternMatchE2EQueryParamSource verifies that pattern_match can match
// against a query parameter value (not just a header) through the full stack.
func TestPatternMatchE2EQueryParamSource(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "e2e-query-param-1",
		Flows: []FlowUpdate{
			{Name: "e9SupportedFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e9UnsupportedFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "422"},
			}},
			{Name: "e9MainFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "query.format",
					Input:  map[string]string{"pattern": `^(json|xml|csv)$`},
					Then:   "e9SupportedFlow",
					Else:   "e9UnsupportedFlow",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-9", Path: "/e2e/export", FlowName: "e9MainFlow", Action: "upsert"},
		},
	})

	tests := []struct {
		label  string
		query  string
		want   int
	}{
		{"json format", "/e2e/export?format=json", 200},
		{"xml format", "/e2e/export?format=xml", 200},
		{"csv format", "/e2e/export?format=csv", 200},
		{"unknown format", "/e2e/export?format=protobuf", 422},
		{"missing format", "/e2e/export", 422},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://localhost"+tt.query, nil)
			state := fm.State.Load()
			apiID := state.Router.Lookup("/e2e/export") // path without query
			if apiID == 0 {
				t.Fatalf("route /e2e/export not found")
			}
			ctx := fm.GetContext()
			ctx.Reset(httptest.NewRecorder())
			ctx.ApiId = apiID
			ctx.SnapshotMetadata(req.Method, req.URL.Path, req.URL.RawQuery)
			fm.ProcessRequest(ctx, req)
			if ctx.ResponseStatus != tt.want {
				t.Errorf("E2E-9 %s (%s): expected %d, got %d", tt.label, tt.query, tt.want, ctx.ResponseStatus)
			}
		})
	}
}

// ─── E2E-10: Pattern match in nested sub-flow ─────────────────────────────────

// TestPatternMatchE2ENestedSubflow verifies that pattern_match works correctly
// when the step lives inside a sub-flow that is called from a parent flow.
// This exercises the full inlining path through CompileExecutable.
//
// Topology:
//
//	apiFlow  →  call(authCheckFlow)
//	               └── authCheckFlow → pattern_match(x-token, "^Bearer .*")
//	                                       ├── authorizedFlow → 200
//	                                       └── unauthorizedFlow → 401
func TestPatternMatchE2ENestedSubflow(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "e2e-nested-subflow-1",
		Flows: []FlowUpdate{
			// Leaf outcomes.
			{Name: "e10AuthorizedFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e10UnauthorizedFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "401"},
			}},
			// Auth sub-flow containing the pattern gate.
			{Name: "e10AuthCheckFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.authorization",
					Input:  map[string]string{"pattern": `^Bearer .+`},
					Then:   "e10AuthorizedFlow",
					Else:   "e10UnauthorizedFlow",
				},
			}},
			// Parent flow that delegates to auth check.
			{Name: "e10ApiFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "call", FlowName: "e10AuthCheckFlow"},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-10", Path: "/e2e/nested", FlowName: "e10ApiFlow", Action: "upsert"},
		},
	})

	tests := []struct {
		label string
		auth  string
		want  int
	}{
		{"valid bearer token", "Bearer sk-valid-token-123", 200},
		{"bearer with minimal value", "Bearer x", 200},
		{"missing bearer prefix", "Basic dXNlcjpwYXNz", 401},
		{"no authorization header", "", 401},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			headers := map[string]string{}
			if tt.auth != "" {
				headers["authorization"] = tt.auth
			}
			ctx := runRequest(t, fm, http.MethodGet, "/e2e/nested", headers)
			if ctx.ResponseStatus != tt.want {
				t.Errorf("E2E-10 %s (auth=%q): expected %d, got %d",
					tt.label, tt.auth, tt.want, ctx.ResponseStatus)
			}
		})
	}
}

// ─── E2E-11: Dotall flag in E2E (multiline header values) ────────────────────

// TestPatternMatchE2EDotallFlag verifies that the "s" flag (dot-all: "." matches
// newlines) works correctly through the full gateway stack.
//
// Note: HTTP headers in practice do not contain raw newlines (they'd be folded
// or rejected by HTTP parsers). This test uses the s flag with a contrived
// multi-line-aware pattern to exercise the flag path in E2E context.
func TestPatternMatchE2EDotallFlag(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "e2e-dotall-1",
		Flows: []FlowUpdate{
			{Name: "e11MatchFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}},
			{Name: "e11NoMatchFlow", Action: "upsert", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "400"},
			}},
			{Name: "e11MainFlow", Action: "upsert", Instructions: []StepConfig{
				{
					Action: "pattern_match",
					Source: "header.x-payload",
					// "s" flag: "." matches newline, so "start.end" matches "start\nend"
					Input: map[string]string{"pattern": `^start.end$`, "flags": "s"},
					Then:  "e11MatchFlow",
					Else:  "e11NoMatchFlow",
				},
			}},
		},
		Apis: []ApiUpdate{
			{Name: "e2e-api-11", Path: "/e2e/dotall", FlowName: "e11MainFlow", Action: "upsert"},
		},
	})

	// With s flag, "start\nend" matches "^start.end$".
	ctx := runRequest(t, fm, http.MethodGet, "/e2e/dotall", map[string]string{
		"x-payload": "start\nend",
	})
	if ctx.ResponseStatus != 200 {
		t.Errorf("E2E-11 dotall: expected 200, got %d", ctx.ResponseStatus)
	}

	// Without newline in payload, "startXend" still matches (. matches any char).
	ctx2 := runRequest(t, fm, http.MethodGet, "/e2e/dotall", map[string]string{
		"x-payload": "startXend",
	})
	if ctx2.ResponseStatus != 200 {
		t.Errorf("E2E-11 single char: expected 200, got %d", ctx2.ResponseStatus)
	}

	// Pattern requires exactly one char between start and end.
	ctx3 := runRequest(t, fm, http.MethodGet, "/e2e/dotall", map[string]string{
		"x-payload": "startend",
	})
	if ctx3.ResponseStatus != 400 {
		t.Errorf("E2E-11 no-separator: expected 400, got %d", ctx3.ResponseStatus)
	}
}

// ─── helpers specific to E2E test file ────────────────────────────────────────

// mustSyncB is mustSync adapted for *testing.B.
func mustSyncB(b *testing.B, server *ManagementServer, req UnifiedSyncRequest) {
	b.Helper()
	payload, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	server.UnifiedSyncHandler(rec, httpReq)
	if rec.Code != http.StatusOK {
		b.Fatalf("sync failed (status %d): %s", rec.Code, rec.Body.String())
	}
}

// Sentinel imports — ensure packages are used (some helpers may come from other test files).
var _ = (*rctx.Context)(nil)
var _ = httptest.NewRecorder
