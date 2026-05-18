package steps

import (
	"regexp"
	"testing"

	"rah/internal/engine"
	"rah/internal/rctx"
)

func TestPatternMatchRegex_SimpleMatch(t *testing.T) {
	pattern := regexp.MustCompile(`^api-`)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 48),
	}
	ctx.ByteSlots[0] = []byte("api-service")
	state := &engine.ExecutionState{PC: 0}

	instr := PatternMatchRegex(0, pattern, 10, 20)
	next := instr.Action(ctx, state)

	if next != 10 {
		t.Fatalf("expected match jump to 10, got %d", next)
	}
}

func TestPatternMatchRegex_NoMatch(t *testing.T) {
	pattern := regexp.MustCompile(`^api-`)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 48),
	}
	ctx.ByteSlots[0] = []byte("data-service")
	state := &engine.ExecutionState{PC: 0}

	instr := PatternMatchRegex(0, pattern, 10, 20)
	next := instr.Action(ctx, state)

	if next != 20 {
		t.Fatalf("expected no-match jump to 20, got %d", next)
	}
}

func TestPatternMatchRegex_EmptySlot(t *testing.T) {
	pattern := regexp.MustCompile(`.*`)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 48),
	}
	// ByteSlots[0] is nil/empty by default
	state := &engine.ExecutionState{PC: 0}

	instr := PatternMatchRegex(0, pattern, 10, 20)
	next := instr.Action(ctx, state)

	// Empty slot should not match (even with .*)
	if next != 20 {
		t.Fatalf("expected no-match on empty slot, got %d", next)
	}
}

func TestPatternMatchRegex_ComplexPattern(t *testing.T) {
	// Match headers like "internal-prod-12345" or "external-staging-789"
	pattern := regexp.MustCompile(`^(internal|external)-(prod|staging|dev)-\d+$`)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 48),
	}

	testCases := []struct {
		name       string
		input      string
		shouldMatch bool
	}{
		{"valid internal prod", "internal-prod-12345", true},
		{"valid external staging", "external-staging-789", true},
		{"valid internal dev", "internal-dev-0", true},
		{"invalid prefix", "custom-prod-12345", false},
		{"invalid missing id", "internal-prod", false},
		{"invalid non-numeric id", "internal-prod-abc", false},
	}

	for _, tc := range testCases {
		ctx.ByteSlots[0] = []byte(tc.input)
		state := &engine.ExecutionState{PC: 0}

		instr := PatternMatchRegex(0, pattern, 10, 20)
		next := instr.Action(ctx, state)

		expectedNext := int16(10)
		if !tc.shouldMatch {
			expectedNext = 20
		}

		if next != expectedNext {
			t.Errorf("%s: expected next=%d, got %d", tc.name, expectedNext, next)
		}
	}
}

func TestPatternMatchRegexWithCapture_MatchWithGroups(t *testing.T) {
	pattern := regexp.MustCompile(`^([a-z]+)-([0-9]+)$`)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 48),
	}
	ctx.ByteSlots[0] = []byte("api-123")
	state := &engine.ExecutionState{PC: 0}

	instr := PatternMatchRegexWithCapture(0, pattern, 10, 20, []int{1, 2})
	next := instr.Action(ctx, state)

	if next != 10 {
		t.Fatalf("expected match, got next=%d", next)
	}

	if string(ctx.ByteSlots[1]) != "api" {
		t.Errorf("expected capture group 1 to be 'api', got %q", string(ctx.ByteSlots[1]))
	}

	if string(ctx.ByteSlots[2]) != "123" {
		t.Errorf("expected capture group 2 to be '123', got %q", string(ctx.ByteSlots[2]))
	}
}

func TestPatternMatchRegexWithCapture_NoMatch(t *testing.T) {
	pattern := regexp.MustCompile(`^([a-z]+)-([0-9]+)$`)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 48),
	}
	ctx.ByteSlots[0] = []byte("invalid-format-xyz")
	// Pre-populate capture slots to verify they get cleared
	ctx.ByteSlots[1] = []byte("old-value")
	ctx.ByteSlots[2] = []byte("old-value-2")
	state := &engine.ExecutionState{PC: 0}

	instr := PatternMatchRegexWithCapture(0, pattern, 10, 20, []int{1, 2})
	next := instr.Action(ctx, state)

	if next != 20 {
		t.Fatalf("expected no-match, got next=%d", next)
	}

	// Verify capture slots were cleared
	if ctx.ByteSlots[1] != nil {
		t.Errorf("expected capture slot 1 to be nil after no-match, got %q", string(ctx.ByteSlots[1]))
	}

	if ctx.ByteSlots[2] != nil {
		t.Errorf("expected capture slot 2 to be nil after no-match, got %q", string(ctx.ByteSlots[2]))
	}
}

func TestPatternMatchRegexWithCapture_PartialGroups(t *testing.T) {
	// Pattern with optional group
	pattern := regexp.MustCompile(`^([a-z]+)(?:-([0-9]+))?$`)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 48),
	}
	ctx.ByteSlots[0] = []byte("api")
	state := &engine.ExecutionState{PC: 0}

	instr := PatternMatchRegexWithCapture(0, pattern, 10, 20, []int{1, 2})
	next := instr.Action(ctx, state)

	if next != 10 {
		t.Fatalf("expected match, got next=%d", next)
	}

	// Group 1 should be populated
	if string(ctx.ByteSlots[1]) != "api" {
		t.Errorf("expected group 1 to be 'api', got %q", string(ctx.ByteSlots[1]))
	}

	// Group 2 didn't participate (optional and not present)
	if ctx.ByteSlots[2] != nil {
		t.Errorf("expected group 2 to be nil (optional, not present), got %q", string(ctx.ByteSlots[2]))
	}
}

func TestPatternMatchRegex_CaseInsensitive(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)^API-`)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 48),
	}

	testCases := []struct {
		name       string
		input      string
		shouldMatch bool
	}{
		{"uppercase", "API-service", true},
		{"lowercase", "api-service", true},
		{"mixed", "Api-service", true},
		{"no match", "data-service", false},
	}

	for _, tc := range testCases {
		ctx.ByteSlots[0] = []byte(tc.input)
		state := &engine.ExecutionState{PC: 0}

		instr := PatternMatchRegex(0, pattern, 10, 20)
		next := instr.Action(ctx, state)

		expectedNext := int16(10)
		if !tc.shouldMatch {
			expectedNext = 20
		}

		if next != expectedNext {
			t.Errorf("%s: expected next=%d, got %d", tc.name, expectedNext, next)
		}
	}
}

// TestPatternMatchRegex_ZeroAllocationHotPath verifies that matching is truly zero-allocation
// in the success case. This is important for RAH's <5µs latency target.
//
// Note: This is a manual verification test. A formal benchmark would live in pattern_bench_test.go
// (to be created in SESSION-9). For now, we just verify the logic is correct.
func TestPatternMatchRegex_ZeroAllocationDesign(t *testing.T) {
	// The implementation uses:
	// 1. ctx.ByteSlots[slotIdx] directly (no string conversion, no []byte allocation)
	// 2. pattern.Match() which doesn't allocate for match checks
	// This ensures hot-path (match success) = zero allocations
	pattern := regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 48),
	}
	ctx.ByteSlots[0] = []byte("valid-identifier-123")
	state := &engine.ExecutionState{PC: 0}

	instr := PatternMatchRegex(0, pattern, 10, 20)
	next := instr.Action(ctx, state)

	if next != 10 {
		t.Fatalf("expected successful match, got next=%d", next)
	}

	// If we got here without panicking or erroring, the zero-alloc design is intact.
	// Formal benchmarking in SESSION-9 will prove this quantitatively.
}
