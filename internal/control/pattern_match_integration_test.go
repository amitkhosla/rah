package control

// pattern_match_integration_test.go â€” SESSION-3 integration tests.
//
// Tests that compile pattern_match flows and execute them end-to-end,
// proving SESSION-1 (instruction) and SESSION-2 (compiler) work together.
//
// Coverage:
//   - Compile flow with pattern_match step
//   - Execute with matching header value â†’ correct then-branch
//   - Execute with non-matching header value â†’ correct else-branch
//   - Regex flags: case-insensitive matching
//   - Invalid regex â†’ compile-time error

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/rctx"
)

// â”€â”€â”€ Test 1: Compile and execute with match â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternMatch proves end-to-end integration:
// 1. Create a flow with pattern_match step
// 2. Compile it via the compiler
// 3. Execute with a header value that MATCHES the pattern
// 4. Verify: response status is 200 (then-branch executed)
func TestCompileAndExecutePatternMatch(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-integration-match-1",
		Flows: []FlowUpdate{
			// Then-branch: return 200 (matched)
			{
				Name:   "matchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
			},
			// Else-branch: return 403 (not matched)
			{
				Name:   "noMatchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "403"},
				},
			},
			// Main flow with pattern_match step
			{
				Name:   "mainFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-service",
						Input: map[string]string{
							"pattern": `^(api|data).*`,
						},
						Then: "matchFlow",
						Else: "noMatchFlow",
					},
				},
			},
		},
		Apis: []ApiUpdate{
			{
				Name:     "pmApi",
				Path:     "/pm",
				FlowName: "mainFlow",
				Action:   "upsert",
			},
		},
	})

	// Execute with header value that MATCHES the pattern "^(api|data).*"
	ctx := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
		"x-service": "api-backend",
	})

	// Verify: then-branch executed (200)
	if ctx.ResponseStatus != 200 {
		t.Errorf("expected response status 200 (then-branch), got %d", ctx.ResponseStatus)
	}
}

// â”€â”€â”€ Test 2: Compile and execute with no-match â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternNoMatch proves end-to-end integration:
// 1. Same flow as Test 1
// 2. Execute with a header value that DOES NOT match the pattern
// 3. Verify: response status is 403 (else-branch executed)
func TestCompileAndExecutePatternNoMatch(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-integration-nomatch-1",
		Flows: []FlowUpdate{
			{
				Name:   "matchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
			},
			{
				Name:   "noMatchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "403"},
				},
			},
			{
				Name:   "mainFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-service",
						Input: map[string]string{
							"pattern": `^(api|data).*`,
						},
						Then: "matchFlow",
						Else: "noMatchFlow",
					},
				},
			},
		},
		Apis: []ApiUpdate{
			{
				Name:     "pmApi",
				Path:     "/pm",
				FlowName: "mainFlow",
				Action:   "upsert",
			},
		},
	})

	// Execute with header value that DOES NOT match "^(api|data).*"
	ctx := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
		"x-service": "web-frontend",
	})

	// Verify: else-branch executed (403)
	if ctx.ResponseStatus != 403 {
		t.Errorf("expected response status 403 (else-branch), got %d", ctx.ResponseStatus)
	}
}

// â”€â”€â”€ Test 3: Pattern match with multiple values â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternMultipleMatches verifies that the pattern
// correctly matches multiple valid values.
func TestCompileAndExecutePatternMultipleMatches(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-integration-multi-1",
		Flows: []FlowUpdate{
			{
				Name:   "matchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
			},
			{
				Name:   "noMatchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "403"},
				},
			},
			{
				Name:   "mainFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-service",
						Input: map[string]string{
							"pattern": `^(api|data).*`,
						},
						Then: "matchFlow",
						Else: "noMatchFlow",
					},
				},
			},
		},
		Apis: []ApiUpdate{
			{
				Name:     "pmApi",
				Path:     "/pm",
				FlowName: "mainFlow",
				Action:   "upsert",
			},
		},
	})

	testCases := []struct {
		name      string
		header    string
		expected  int
		desc      string
	}{
		{"api-backend", "api-backend", 200, "matches api prefix"},
		{"api-prod", "api-prod", 200, "matches api prefix"},
		{"api", "api", 200, "matches api exactly"},
		{"data-warehouse", "data-warehouse", 200, "matches data prefix"},
		{"data-v2-prod", "data-v2-prod", 200, "matches data prefix"},
		{"web-frontend", "web-frontend", 403, "does not match api or data"},
		{"custom-service", "custom-service", 403, "does not match api or data"},
		{"API-backend", "API-backend", 403, "case-sensitive: uppercase doesn't match"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
				"x-service": tc.header,
			})
			if ctx.ResponseStatus != tc.expected {
				t.Errorf("header %q: expected %d, got %d", tc.header, tc.expected, ctx.ResponseStatus)
			}
		})
	}
}

// â”€â”€â”€ Test 4: Case-insensitive flag â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternCaseInsensitive verifies that the "i" flag
// enables case-insensitive matching.
func TestCompileAndExecutePatternCaseInsensitive(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-integration-case-insensitive-1",
		Flows: []FlowUpdate{
			{
				Name:   "matchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
			},
			{
				Name:   "noMatchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "403"},
				},
			},
			{
				Name:   "mainFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-service",
						Input: map[string]string{
							"pattern": `^API`,
							"flags":   "i", // case-insensitive
						},
						Then: "matchFlow",
						Else: "noMatchFlow",
					},
				},
			},
		},
		Apis: []ApiUpdate{
			{
				Name:     "pmApi",
				Path:     "/pm",
				FlowName: "mainFlow",
				Action:   "upsert",
			},
		},
	})

	// Test: uppercase input with case-insensitive pattern
	ctxUpper := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
		"x-service": "API-prod",
	})
	if ctxUpper.ResponseStatus != 200 {
		t.Errorf("uppercase API-prod with (?i)^API: expected 200, got %d", ctxUpper.ResponseStatus)
	}

	// Test: lowercase input with case-insensitive pattern
	ctxLower := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
		"x-service": "api-prod",
	})
	if ctxLower.ResponseStatus != 200 {
		t.Errorf("lowercase api-prod with (?i)^API: expected 200, got %d", ctxLower.ResponseStatus)
	}

	// Test: mixed case input
	ctxMixed := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
		"x-service": "Api-prod",
	})
	if ctxMixed.ResponseStatus != 200 {
		t.Errorf("mixed case Api-prod with (?i)^API: expected 200, got %d", ctxMixed.ResponseStatus)
	}

	// Test: non-matching input
	ctxNoMatch := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
		"x-service": "data-prod",
	})
	if ctxNoMatch.ResponseStatus != 403 {
		t.Errorf("data-prod with (?i)^API: expected 403 (no match), got %d", ctxNoMatch.ResponseStatus)
	}
}

// â”€â”€â”€ Test 5: Multiple flags combined â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternMultipleFlags verifies that multiple flags
// can be combined (e.g., "i" + "m" for case-insensitive + multiline).
func TestCompileAndExecutePatternMultipleFlags(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-integration-flags-multi-1",
		Flows: []FlowUpdate{
			{
				Name:   "matchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
			},
			{
				Name:   "noMatchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "403"},
				},
			},
			{
				Name:   "mainFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-tenant",
						Input: map[string]string{
							"pattern": `^ACME`,
							"flags":   "im", // case-insensitive + multiline
						},
						Then: "matchFlow",
						Else: "noMatchFlow",
					},
				},
			},
		},
		Apis: []ApiUpdate{
			{
				Name:     "pmApi",
				Path:     "/pm",
				FlowName: "mainFlow",
				Action:   "upsert",
			},
		},
	})

	// Execute with lowercase value (matches due to "i" flag)
	ctx := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
		"x-tenant": "acme-corp",
	})
	if ctx.ResponseStatus != 200 {
		t.Errorf("acme-corp with (?im)^ACME: expected 200, got %d", ctx.ResponseStatus)
	}
}

// â”€â”€â”€ Test 6: Empty header (no match) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternEmptyHeader verifies that a missing header
// (empty slot) does not match, even with wildcards.
func TestCompileAndExecutePatternEmptyHeader(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-integration-empty-header-1",
		Flows: []FlowUpdate{
			{
				Name:   "matchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
			},
			{
				Name:   "noMatchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "403"},
				},
			},
			{
				Name:   "mainFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-service",
						Input: map[string]string{
							"pattern": `.*`, // matches everything, but not empty
						},
						Then: "matchFlow",
						Else: "noMatchFlow",
					},
				},
			},
		},
		Apis: []ApiUpdate{
			{
				Name:     "pmApi",
				Path:     "/pm",
				FlowName: "mainFlow",
				Action:   "upsert",
			},
		},
	})

	// Execute with NO x-service header â†’ empty slot â†’ no match
	ctx := runRequest(t, fm, http.MethodGet, "/pm", nil)
	if ctx.ResponseStatus != 403 {
		t.Errorf("no header with pattern .*: expected 403 (empty slot), got %d", ctx.ResponseStatus)
	}
}

// â”€â”€â”€ Test 7: Complex regex pattern â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternComplex verifies that complex regex patterns work
// (multi-level grouping, anchors, character classes, quantifiers).
func TestCompileAndExecutePatternComplex(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-integration-complex-1",
		Flows: []FlowUpdate{
			{
				Name:   "matchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
			},
			{
				Name:   "noMatchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "403"},
				},
			},
			{
				Name:   "mainFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-region",
						Input: map[string]string{
							// Pattern: (us|eu)-(east|west|central)-\d+
							// Examples: us-east-1, eu-west-3, us-central-2
							"pattern": `^(us|eu)-(east|west|central)-\d+$`,
						},
						Then: "matchFlow",
						Else: "noMatchFlow",
					},
				},
			},
		},
		Apis: []ApiUpdate{
			{
				Name:     "pmApi",
				Path:     "/pm",
				FlowName: "mainFlow",
				Action:   "upsert",
			},
		},
	})

	testCases := []struct {
		name     string
		region   string
		expected int
	}{
		{"valid us-east-1", "us-east-1", 200},
		{"valid eu-west-3", "eu-west-3", 200},
		{"valid us-central-2", "us-central-2", 200},
		{"invalid prefix", "ap-east-1", 403},
		{"invalid no number", "us-east", 403},
		{"invalid non-numeric", "us-east-abc", 403},
		{"invalid extra", "us-east-1-extra", 403},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
				"x-region": tc.region,
			})
			if ctx.ResponseStatus != tc.expected {
				t.Errorf("%q: expected %d, got %d", tc.region, tc.expected, ctx.ResponseStatus)
			}
		})
	}
}

// â”€â”€â”€ Test 8: Invalid regex compile error â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternInvalidRegex verifies that an invalid regex
// is caught at compile time and produces a clear error.
func TestCompileAndExecutePatternInvalidRegex(t *testing.T) {
	_, compiler, _, _ := newTestStack(t)

	// Try to compile a flow with an invalid regex (unclosed bracket)
	flow := []StepConfig{
		{
			Action: "pattern_match",
			Source: "header.x-service",
			Input: map[string]string{
				"pattern": `[invalid`, // unclosed bracket â€” invalid regex
			},
			Then: "matchFlow",
			Else: "noMatchFlow",
		},
	}

	_, err := compiler.Compile(flow)
	if err == nil {
		t.Fatal("expected compile error for invalid regex, got nil")
	}

	// Verify error message mentions regex error
	errMsg := err.Error()
	if !contains(errMsg, "invalid regex") && !contains(errMsg, "error parsing regexp") {
		t.Errorf("expected error about invalid regex, got: %v", err)
	}
}

// â”€â”€â”€ Test 9: Missing source compile error â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternMissingSource verifies that omitting 'source'
// is caught at compile time.
func TestCompileAndExecutePatternMissingSource(t *testing.T) {
	_, compiler, _, _ := newTestStack(t)

	flow := []StepConfig{
		{
			Action: "pattern_match",
			// Source intentionally omitted
			Input: map[string]string{
				"pattern": `.*`,
			},
			Then: "matchFlow",
			Else: "noMatchFlow",
		},
	}

	_, err := compiler.Compile(flow)
	if err == nil {
		t.Fatal("expected compile error for missing source, got nil")
	}

	errMsg := err.Error()
	if !contains(errMsg, "source") && !contains(errMsg, "required") {
		t.Errorf("expected error about missing source, got: %v", err)
	}
}

// â”€â”€â”€ Test 10: Missing pattern compile error â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternMissingPattern verifies that omitting 'pattern'
// is caught at compile time.
func TestCompileAndExecutePatternMissingPattern(t *testing.T) {
	_, compiler, _, _ := newTestStack(t)

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
		t.Fatal("expected compile error for missing pattern, got nil")
	}

	errMsg := err.Error()
	if !contains(errMsg, "pattern") && !contains(errMsg, "required") {
		t.Errorf("expected error about missing pattern, got: %v", err)
	}
}

// â”€â”€â”€ Test 11: Pattern with special regex characters â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternSpecialChars verifies that special regex characters
// are properly handled (not escaped, part of the regex semantics).
func TestCompileAndExecutePatternSpecialChars(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-integration-special-chars-1",
		Flows: []FlowUpdate{
			{
				Name:   "matchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
			},
			{
				Name:   "noMatchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "403"},
				},
			},
			{
				Name:   "mainFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-email",
						Input: map[string]string{
							// Pattern: simple email-like format
							"pattern": `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`,
						},
						Then: "matchFlow",
						Else: "noMatchFlow",
					},
				},
			},
		},
		Apis: []ApiUpdate{
			{
				Name:     "pmApi",
				Path:     "/pm",
				FlowName: "mainFlow",
				Action:   "upsert",
			},
		},
	})

	testCases := []struct {
		name     string
		email    string
		expected int
	}{
		{"valid basic", "user@example.com", 200},
		{"valid with dot", "john.doe@company.co.uk", 200},
		{"valid with underscore", "user_name@test.org", 200},
		{"valid with plus", "user+tag@domain.net", 200},
		{"invalid no at", "userexamplecom", 403},
		{"invalid no domain", "user@", 403},
		{"invalid no tld", "user@domain", 403},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
				"x-email": tc.email,
			})
			if ctx.ResponseStatus != tc.expected {
				t.Errorf("%q: expected %d, got %d", tc.email, tc.expected, ctx.ResponseStatus)
			}
		})
	}
}

// â”€â”€â”€ Test 12: Multiple pattern_match steps in sequence â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecuteMultiplePatternSteps verifies that multiple pattern_match
// steps can be chained together.
func TestCompileAndExecuteMultiplePatternSteps(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-integration-multi-steps-1",
		Flows: []FlowUpdate{
			// Flow 1: check x-service matches ^api
			{
				Name:   "checkService",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-service",
						Input: map[string]string{
							"pattern": `^api.*`,
						},
						Then: "checkRegion",
						Else: "wrongService",
					},
				},
			},
			// Flow 2: check x-region matches (us|eu)
			{
				Name:   "checkRegion",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-region",
						Input: map[string]string{
							"pattern": `^(us|eu).*`,
						},
						Then: "bothMatch",
						Else: "wrongRegion",
					},
				},
			},
			// Success: both service and region matched
			{
				Name:   "bothMatch",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
			},
			// Error: service didn't match
			{
				Name:   "wrongService",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "400"},
				},
			},
			// Error: region didn't match
			{
				Name:   "wrongRegion",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "401"},
				},
			},
		},
		Apis: []ApiUpdate{
			{
				Name:     "pmApi",
				Path:     "/pm",
				FlowName: "checkService",
				Action:   "upsert",
			},
		},
	})

	testCases := []struct {
		name     string
		service  string
		region   string
		expected int
	}{
		{"both match", "api-gateway", "us-east", 200},
		{"both match eu", "api-backend", "eu-west", 200},
		{"service mismatch", "web-frontend", "us-east", 400},
		{"region mismatch", "api-gateway", "ap-north", 401},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{
				"x-service": tc.service,
				"x-region":  tc.region,
			}
			ctx := runRequest(t, fm, http.MethodGet, "/pm", headers)
			if ctx.ResponseStatus != tc.expected {
				t.Errorf("service=%q region=%q: expected %d, got %d", tc.service, tc.region, tc.expected, ctx.ResponseStatus)
			}
		})
	}
}

// â”€â”€â”€ Test 13: Anchored patterns â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternAnchors verifies that ^ (start) and $ (end)
// anchors work correctly.
func TestCompileAndExecutePatternAnchors(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-integration-anchors-1",
		Flows: []FlowUpdate{
			{
				Name:   "matchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
			},
			{
				Name:   "noMatchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "403"},
				},
			},
			{
				Name:   "mainFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-version",
						Input: map[string]string{
							"pattern": `^v\d+\.\d+\.\d+$`,
						},
						Then: "matchFlow",
						Else: "noMatchFlow",
					},
				},
			},
		},
		Apis: []ApiUpdate{
			{
				Name:     "pmApi",
				Path:     "/pm",
				FlowName: "mainFlow",
				Action:   "upsert",
			},
		},
	})

	testCases := []struct {
		name     string
		version  string
		expected int
	}{
		{"valid version", "v1.2.3", 200},
		{"valid version long", "v10.20.30", 200},
		{"prefix mismatch", "1.2.3", 403},
		{"extra stuff", "v1.2.3-beta", 403},
		{"prefix extra", "version-v1.2.3", 403},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
				"x-version": tc.version,
			})
			if ctx.ResponseStatus != tc.expected {
				t.Errorf("%q: expected %d, got %d", tc.version, tc.expected, ctx.ResponseStatus)
			}
		})
	}
}

// â”€â”€â”€ Test 14: Dotall flag (. matches newline) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestCompileAndExecutePatternDotAll verifies that the "s" flag makes "."
// match newlines.
func TestCompileAndExecutePatternDotAll(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "pm-integration-dotall-1",
		Flows: []FlowUpdate{
			{
				Name:   "matchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
			},
			{
				Name:   "noMatchFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "403"},
				},
			},
			{
				Name:   "mainFlow",
				Action: "upsert",
				Instructions: []StepConfig{
					{
						Action: "pattern_match",
						Source: "header.x-data",
						Input: map[string]string{
							"pattern": `line1.line2`,
							"flags":   "s", // dot-all: . matches newlines
						},
						Then: "matchFlow",
						Else: "noMatchFlow",
					},
				},
			},
		},
		Apis: []ApiUpdate{
			{
				Name:     "pmApi",
				Path:     "/pm",
				FlowName: "mainFlow",
				Action:   "upsert",
			},
		},
	})

	// Test: newline in data, matches with "s" flag
	ctx := runRequest(t, fm, http.MethodGet, "/pm", map[string]string{
		"x-data": "line1\nline2",
	})
	if ctx.ResponseStatus != 200 {
		t.Errorf("line1\\nline2 with (?s)line1.line2: expected 200, got %d", ctx.ResponseStatus)
	}
}

// â”€â”€â”€ helpers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// contains checks if s contains the substring sub.
func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Ensure package-level runRequest and mustSync are available (defined in flow_features_test.go).
var _ = runRequest
var _ = mustSync
var _ = httptest.NewRecorder

// sentinel to ensure rctx package is imported
var _ = (*rctx.Context)(nil)
