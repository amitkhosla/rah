package steps

import (
	"encoding/json"
	"testing"
)

func TestShallowMergeJSON_AddsNewKeys(t *testing.T) {
	base := []byte(`{"a":1}`)
	patch := []byte(`{"b":2}`)
	result, err := shallowMergeJSON(base, patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if _, ok := m["a"]; !ok {
		t.Error("expected key 'a' to be present")
	}
	if _, ok := m["b"]; !ok {
		t.Error("expected key 'b' to be present")
	}
}

func TestShallowMergeJSON_OverwritesExistingKeys(t *testing.T) {
	base := []byte(`{"a":1,"b":2}`)
	patch := []byte(`{"b":99}`)
	result, err := shallowMergeJSON(base, patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if m["b"] != float64(99) {
		t.Errorf("expected b=99, got %v", m["b"])
	}
}

func TestShallowMergeJSON_PreservesUnmentionedKeys(t *testing.T) {
	base := []byte(`{"a":1,"c":"unchanged"}`)
	patch := []byte(`{"b":2}`)
	result, err := shallowMergeJSON(base, patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if m["c"] != "unchanged" {
		t.Errorf("expected c=unchanged, got %v", m["c"])
	}
	if m["a"] != float64(1) {
		t.Errorf("expected a=1, got %v", m["a"])
	}
	if m["b"] != float64(2) {
		t.Errorf("expected b=2, got %v", m["b"])
	}
}

func TestShallowMergeJSON_EmptyBase(t *testing.T) {
	patch := []byte(`{"x":42}`)
	result, err := shallowMergeJSON(nil, patch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if m["x"] != float64(42) {
		t.Errorf("expected x=42, got %v", m["x"])
	}
}
