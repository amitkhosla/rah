package control

// template_pattern_compiler_test.go — compiler integration tests for
// validate_pattern and extract_pattern steps.
//
// Coverage:
//   - validate_pattern: matching input → result slot non-nil (truthy)
//   - validate_pattern: non-matching input → result slot nil
//   - extract_pattern: capture slots populated on match
//   - extract_pattern: capture slots nil on no-match
//   - validate_pattern: {undeclaredVar} in pattern → compile error
//   - extract_pattern: {undeclaredVar} in pattern → compile error
//   - extract_pattern: missing Source → compile error
//   - validate_pattern: missing As → compile error
//   - Step descriptors registered for both actions

import (
	"net/http"
	"testing"
)

// ─── validate_pattern: basic match ───────────────────────────────────────────

// TestValidatePatternBasicMatch verifies that a header value matching the pattern
// sets the result slot to a truthy (non-nil) value and the subsequent if-branch
// routes to the 200 flow.
func TestValidatePatternBasicMatch(t *testing.T) {
	flows := []FlowUpdate{
		{Name: "okFlow", Action: "upsert", Instructions: []StepConfig{
			{Action: "set_response_status", Value: "200"},
		}},
		{Name: "failFlow", Action: "upsert", Instructions: []StepConfig{
			{Action: "set_response_status", Value: "403"},
		}},
		{Name: "mainFlow", Action: "upsert", Instructions: []StepConfig{
			{
				Action: "validate_pattern",
				Source: "header.x-custom",
				Input:  map[string]string{"pattern": "internal_*"},
				As:     "isValid",
			},
			{
				Action:    "if",
				Condition: "isValid",
				Then:      "okFlow",
				Else:      "failFlow",
			},
		}},
	}

	ctx := compileAndRun(t, flows, []ApiUpdate{
		{Name: "vpApi", Path: "/vp", FlowName: "mainFlow", Action: "upsert"},
	}, http.MethodGet, "/vp", map[string]string{"x-custom": "internal_service"})

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200 (match), got %d", ctx.ResponseStatus)
	}
}

// ─── validate_pattern: no match ──────────────────────────────────────────────

// TestValidatePatternNoMatch verifies that a non-matching value sets the result
// slot to nil and the if-branch routes to the 403 flow.
func TestValidatePatternNoMatch(t *testing.T) {
	flows := []FlowUpdate{
		{Name: "okFlow", Action: "upsert", Instructions: []StepConfig{
			{Action: "set_response_status", Value: "200"},
		}},
		{Name: "failFlow", Action: "upsert", Instructions: []StepConfig{
			{Action: "set_response_status", Value: "403"},
		}},
		{Name: "mainFlow", Action: "upsert", Instructions: []StepConfig{
			{
				Action: "validate_pattern",
				Source: "header.x-custom",
				Input:  map[string]string{"pattern": "internal_*"},
				As:     "isValid",
			},
			{
				Action:    "if",
				Condition: "isValid",
				Then:      "okFlow",
				Else:      "failFlow",
			},
		}},
	}

	ctx := compileAndRun(t, flows, []ApiUpdate{
		{Name: "vpApi", Path: "/vp", FlowName: "mainFlow", Action: "upsert"},
	}, http.MethodGet, "/vp", map[string]string{"x-custom": "external_service"})

	if ctx.ResponseStatus != 403 {
		t.Errorf("expected 403 (no-match), got %d", ctx.ResponseStatus)
	}
}

// ─── extract_pattern: captures populated on match ────────────────────────────

// TestExtractPatternCaptures verifies that capture slots are populated when the
// source value matches the pattern.
func TestExtractPatternCaptures(t *testing.T) {
	flows := []FlowUpdate{
		{Name: "okFlow", Action: "upsert", Instructions: []StepConfig{
			{Action: "set_response_status", Value: "200"},
		}},
		{Name: "failFlow", Action: "upsert", Instructions: []StepConfig{
			{Action: "set_response_status", Value: "403"},
		}},
		{Name: "mainFlow", Action: "upsert", Instructions: []StepConfig{
			// Pattern: "svc_(service)" extracts the part after "svc_" into capture slot "service"
			{
				Action: "extract_pattern",
				Source: "header.x-id",
				Input:  map[string]string{"pattern": "svc_(service)"},
			},
			// "service" slot is non-nil (non-empty) after a match → truthy
			{
				Action:    "if",
				Condition: "service",
				Then:      "okFlow",
				Else:      "failFlow",
			},
		}},
	}

	ctx := compileAndRun(t, flows, []ApiUpdate{
		{Name: "epApi", Path: "/ep", FlowName: "mainFlow", Action: "upsert"},
	}, http.MethodGet, "/ep", map[string]string{"x-id": "svc_payments"})

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200 (capture present), got %d", ctx.ResponseStatus)
	}
}

// ─── extract_pattern: nil on mismatch ────────────────────────────────────────

// TestExtractPatternNilOnMismatch verifies that capture slots are nil when the
// source value does NOT match the pattern.
func TestExtractPatternNilOnMismatch(t *testing.T) {
	flows := []FlowUpdate{
		{Name: "okFlow", Action: "upsert", Instructions: []StepConfig{
			{Action: "set_response_status", Value: "200"},
		}},
		{Name: "failFlow", Action: "upsert", Instructions: []StepConfig{
			{Action: "set_response_status", Value: "403"},
		}},
		{Name: "mainFlow", Action: "upsert", Instructions: []StepConfig{
			// Pattern requires "svc_" prefix — "api_payments" won't match
			{
				Action: "extract_pattern",
				Source: "header.x-id",
				Input:  map[string]string{"pattern": "svc_(service)"},
			},
			// "service" slot is nil after no-match: len(ByteSlots[service]) == 0 → false
			{
				Action:    "if",
				Condition: "service",
				Then:      "okFlow",
				Else:      "failFlow",
			},
		}},
	}

	ctx := compileAndRun(t, flows, []ApiUpdate{
		{Name: "epApi", Path: "/ep", FlowName: "mainFlow", Action: "upsert"},
	}, http.MethodGet, "/ep", map[string]string{"x-id": "api_payments"})

	if ctx.ResponseStatus != 403 {
		t.Errorf("expected 403 (nil capture on mismatch), got %d", ctx.ResponseStatus)
	}
}

// ─── validate_pattern: unknown slot ref → compile error ──────────────────────

// TestValidatePatternUnknownSlotRef verifies that referencing an undeclared slot
// via {name} in the pattern produces a compile-time error.
func TestValidatePatternUnknownSlotRef(t *testing.T) {
	fm, compiler, _, _ := newTestStack(t)
	_ = fm

	flow := []StepConfig{
		{
			Action: "validate_pattern",
			Source: "header.x-custom",
			Input:  map[string]string{"pattern": "prefix_{undeclaredVar}"},
			As:     "isValid",
		},
	}
	_, err := compiler.Compile(flow)
	if err == nil {
		t.Fatal("expected compile-time error for undeclared slot ref, got nil")
	}
	if !containsAny(err.Error(), "not declared", "undeclaredVar") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ─── extract_pattern: unknown slot ref → compile error ───────────────────────

// TestExtractPatternUnknownSlotRef verifies that referencing an undeclared slot
// via {name} in the pattern produces a compile-time error.
func TestExtractPatternUnknownSlotRef(t *testing.T) {
	fm, compiler, _, _ := newTestStack(t)
	_ = fm

	flow := []StepConfig{
		{
			Action: "extract_pattern",
			Source: "header.x-id",
			Input:  map[string]string{"pattern": "svc_(service){undeclaredRef}"},
		},
	}
	_, err := compiler.Compile(flow)
	if err == nil {
		t.Fatal("expected compile-time error for undeclared slot ref, got nil")
	}
	if !containsAny(err.Error(), "not declared", "undeclaredRef") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ─── extract_pattern: missing source → compile error ─────────────────────────

// TestExtractPatternMissingSource verifies that omitting 'source' produces a
// compile-time error.
func TestExtractPatternMissingSource(t *testing.T) {
	fm, compiler, _, _ := newTestStack(t)
	_ = fm

	flow := []StepConfig{
		{
			Action: "extract_pattern",
			// Source intentionally omitted
			Input: map[string]string{"pattern": "svc_(service)"},
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

// ─── validate_pattern: missing as → compile error ────────────────────────────

// TestValidatePatternMissingAs verifies that omitting 'as' produces a
// compile-time error.
func TestValidatePatternMissingAs(t *testing.T) {
	fm, compiler, _, _ := newTestStack(t)
	_ = fm

	flow := []StepConfig{
		{
			Action: "validate_pattern",
			Source: "header.x-custom",
			Input:  map[string]string{"pattern": "internal_*"},
			// As intentionally omitted
		},
	}
	_, err := compiler.Compile(flow)
	if err == nil {
		t.Fatal("expected compile-time error for missing as, got nil")
	}
	if !containsAny(err.Error(), "'as' is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ─── step descriptor: extract_pattern registered ─────────────────────────────

// TestExtractPatternStepDescriptorRegistered verifies that the extract_pattern
// step descriptor is present in AllStepDescriptors.
func TestExtractPatternStepDescriptorRegistered(t *testing.T) {
	descriptors := AllStepDescriptors()
	for _, d := range descriptors {
		if d.Type == "extract_pattern" {
			if d.Category != "string" {
				t.Errorf("expected category 'string', got %q", d.Category)
			}
			if d.Capability != "extract" {
				t.Errorf("expected capability 'extract', got %q", d.Capability)
			}
			return
		}
	}
	t.Error("extract_pattern descriptor not found in AllStepDescriptors()")
}

// ─── step descriptor: validate_pattern registered ────────────────────────────

// TestValidatePatternStepDescriptorRegistered verifies that the validate_pattern
// step descriptor is present in AllStepDescriptors.
func TestValidatePatternStepDescriptorRegistered(t *testing.T) {
	descriptors := AllStepDescriptors()
	for _, d := range descriptors {
		if d.Type == "validate_pattern" {
			if d.Category != "string" {
				t.Errorf("expected category 'string', got %q", d.Category)
			}
			if d.Capability != "match" {
				t.Errorf("expected capability 'match', got %q", d.Capability)
			}
			return
		}
	}
	t.Error("validate_pattern descriptor not found in AllStepDescriptors()")
}
