package steps

import (
	"testing"
)

// helper: allocSlot that tracks allocated names → indices
func newTestAllocSlot() (func(name string) (int, error), map[string]int) {
	allocated := make(map[string]int)
	next := 0
	alloc := func(name string) (int, error) {
		if idx, ok := allocated[name]; ok {
			return idx, nil
		}
		idx := next
		next++
		allocated[name] = idx
		return idx, nil
	}
	return alloc, allocated
}

func TestParseTemplatePattern_PureLiteral(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp, err := ParseTemplatePattern("exact_value", nil, alloc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctp.Strategy != StrategyExact {
		t.Errorf("expected StrategyExact, got %v", ctp.Strategy)
	}
	if len(ctp.Segments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(ctp.Segments))
	}
	if ctp.Segments[0].Kind != SegLiteral {
		t.Errorf("expected SegLiteral, got %v", ctp.Segments[0].Kind)
	}
	if string(ctp.Segments[0].Literal) != "exact_value" {
		t.Errorf("unexpected literal: %q", ctp.Segments[0].Literal)
	}
}

func TestParseTemplatePattern_PrefixWildcard(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp, err := ParseTemplatePattern("prefix_*", nil, alloc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctp.Strategy != StrategyPrefix {
		t.Errorf("expected StrategyPrefix, got %v", ctp.Strategy)
	}
}

func TestParseTemplatePattern_WildcardSuffix(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp, err := ParseTemplatePattern("*_suffix", nil, alloc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctp.Strategy != StrategySuffix {
		t.Errorf("expected StrategySuffix, got %v", ctp.Strategy)
	}
}

func TestParseTemplatePattern_PrefixCaptureStaticSuffix(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp, err := ParseTemplatePattern("pre_(cap)_suf", nil, alloc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctp.Strategy != StrategyPrefixSuffixExtract {
		t.Errorf("expected StrategyPrefixSuffixExtract, got %v", ctp.Strategy)
	}
	if len(ctp.CaptureSlots) != 1 {
		t.Errorf("expected 1 capture slot, got %d", len(ctp.CaptureSlots))
	}
}

func TestParseTemplatePattern_PrefixCaptureSlotRef(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	slotMap := map[string]int{"ref": 5}
	ctp, err := ParseTemplatePattern("pre_(cap)_{ref}", slotMap, alloc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctp.Strategy != StrategyPrefixSlotRefExtract {
		t.Errorf("expected StrategyPrefixSlotRefExtract, got %v", ctp.Strategy)
	}
	if len(ctp.CaptureSlots) != 1 {
		t.Errorf("expected 1 capture slot, got %d", len(ctp.CaptureSlots))
	}
	if len(ctp.SlotRefSlots) != 1 || ctp.SlotRefSlots[0] != 5 {
		t.Errorf("expected SlotRefSlots=[5], got %v", ctp.SlotRefSlots)
	}
}

func TestParseTemplatePattern_TwoCaptures(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp, err := ParseTemplatePattern("(a)_(b)", nil, alloc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctp.Strategy != StrategySequential {
		t.Errorf("expected StrategySequential, got %v", ctp.Strategy)
	}
	if len(ctp.CaptureSlots) != 2 {
		t.Errorf("expected 2 capture slots, got %d", len(ctp.CaptureSlots))
	}
}

func TestParseTemplatePattern_UnknownSlotRef(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	_, err := ParseTemplatePattern("{undeclared}", map[string]int{}, alloc)
	if err == nil {
		t.Fatal("expected error for undeclared slot reference, got nil")
	}
}

func TestParseTemplatePattern_EmptyPattern(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	_, err := ParseTemplatePattern("", nil, alloc)
	if err == nil {
		t.Fatal("expected error for empty pattern, got nil")
	}
}

func TestParseTemplatePattern_NestedParens(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	_, err := ParseTemplatePattern("((nested))", nil, alloc)
	if err == nil {
		t.Fatal("expected error for nested parentheses, got nil")
	}
}

func TestParseTemplatePattern_MidWildcard(t *testing.T) {
	alloc, _ := newTestAllocSlot()
	ctp, err := ParseTemplatePattern("pre_*_suf", nil, alloc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctp.Strategy != StrategySequential {
		t.Errorf("expected StrategySequential for mid-wildcard, got %v", ctp.Strategy)
	}
}
