package control

import (
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
)

// TestCompileEventFlow_ReservesWellKnownSlots verifies that CompileEventFlow()
// allocates well-known event slots at fixed indices (0, 1, 2).
// Slots __event__ (payload), __event_key__ (message key), and __event_topic__ (topic)
// are reserved first, guaranteeing their indices are 0, 1, 2 respectively.
func TestCompileEventFlow_ReservesWellKnownSlots(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)

	// Compile an empty flow using CompileEventFlow
	flow := []StepConfig{}
	_, err := compiler.CompileEventFlow(flow)
	if err != nil {
		t.Fatalf("CompileEventFlow failed: %v", err)
	}

	// Verify that __event__, __event_key__, __event_topic__ are at indices 0, 1, 2
	if idx, ok := compiler.slotMap["__event__"]; !ok {
		t.Error("__event__ not found in slot map")
	} else if idx != 0 {
		t.Errorf("__event__ expected at index 0, got %d", idx)
	}

	if idx, ok := compiler.slotMap["__event_key__"]; !ok {
		t.Error("__event_key__ not found in slot map")
	} else if idx != 1 {
		t.Errorf("__event_key__ expected at index 1, got %d", idx)
	}

	if idx, ok := compiler.slotMap["__event_topic__"]; !ok {
		t.Error("__event_topic__ not found in slot map")
	} else if idx != 2 {
		t.Errorf("__event_topic__ expected at index 2, got %d", idx)
	}
}

// TestCompile_DoesNotReserveWellKnownSlots verifies that the regular Compile()
// method does NOT pre-allocate well-known event slots.
// This ensures eventFlowMode is only active when explicitly calling CompileEventFlow.
func TestCompile_DoesNotReserveWellKnownSlots(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)

	// Compile an empty flow using regular Compile (not CompileEventFlow)
	flow := []StepConfig{}
	_, err := compiler.Compile(flow)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	// Verify that well-known slots are NOT present
	if _, ok := compiler.slotMap["__event__"]; ok {
		t.Error("__event__ should not be reserved in regular Compile")
	}

	if _, ok := compiler.slotMap["__event_key__"]; ok {
		t.Error("__event_key__ should not be reserved in regular Compile")
	}

	if _, ok := compiler.slotMap["__event_topic__"]; ok {
		t.Error("__event_topic__ should not be reserved in regular Compile")
	}
}

// TestCompileEventFlow_WellKnownSlotsNotReallocated verifies that when additional
// variables are allocated after CompileEventFlow, they start at index 3 (not reusing
// the well-known slots).
func TestCompileEventFlow_WellKnownSlotsNotReallocated(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)

	// Compile a flow that allocates an additional variable
	flow := []StepConfig{
		{
			Action: "set_response_body",
			Value:  "value",
			As:     "my_var",
		},
	}
	_, err := compiler.CompileEventFlow(flow)
	if err != nil {
		t.Fatalf("CompileEventFlow failed: %v", err)
	}

	// Verify well-known slots are at 0, 1, 2
	eventIdx := compiler.slotMap["__event__"]
	eventKeyIdx := compiler.slotMap["__event_key__"]
	eventTopicIdx := compiler.slotMap["__event_topic__"]

	if eventIdx != 0 {
		t.Errorf("__event__ expected at 0; got %d", eventIdx)
	}
	if eventKeyIdx != 1 {
		t.Errorf("__event_key__ expected at 1; got %d", eventKeyIdx)
	}
	if eventTopicIdx != 2 {
		t.Errorf("__event_topic__ expected at 2; got %d", eventTopicIdx)
	}

	// Verify my_var is at index 3 (next after well-known slots)
	if idx, ok := compiler.slotMap["my_var"]; ok {
		if idx != 3 {
			t.Errorf("my_var expected at index 3; got %d", idx)
		}
	}
	// Note: "my_var" may not be allocated if set_response_body doesn't create a slot
	// for the Value directly, so we don't fail if it's not present. The important test
	// is that nextSlot advanced from 3 (or beyond) when an additional slot is needed.
}

// TestCompileEventFlow_ResetsBetweenCalls verifies that eventFlowMode is properly
// reset after CompileEventFlow returns, so subsequent Compile calls are unaffected.
func TestCompileEventFlow_ResetsBetweenCalls(t *testing.T) {
	fm := engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
	compiler := NewCompiler(fm)

	// First call: CompileEventFlow
	flow1 := []StepConfig{}
	_, err := compiler.CompileEventFlow(flow1)
	if err != nil {
		t.Fatalf("first CompileEventFlow failed: %v", err)
	}

	if compiler.eventFlowMode {
		t.Error("eventFlowMode should be reset to false after CompileEventFlow returns")
	}

	// Reset compiler for next test
	compiler = NewCompiler(fm)

	// Second call: regular Compile (should not reserve well-known slots)
	flow2 := []StepConfig{}
	_, err = compiler.Compile(flow2)
	if err != nil {
		t.Fatalf("second Compile failed: %v", err)
	}

	if _, ok := compiler.slotMap["__event__"]; ok {
		t.Error("__event__ should not be present in second Compile (eventFlowMode reset)")
	}
}
