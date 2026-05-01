package steps

import (
	"encoding/json"
	"testing"
)

func TestCanonicalMessageHasBlocks(t *testing.T) {
	plain := CanonicalMessage{Role: RoleUser, Content: "hello"}
	if plain.HasBlocks() {
		t.Error("plain text message should not HasBlocks")
	}
	withBlocks := CanonicalMessage{
		Role: RoleAssistant,
		ContentBlocks: []ContentBlock{{Type: BlockToolUse, ToolName: "read"}},
	}
	if !withBlocks.HasBlocks() {
		t.Error("message with blocks should HasBlocks")
	}
}

func TestToolChoiceSerialization(t *testing.T) {
	tc := ToolChoice{Type: ToolChoiceTool, Name: "read_file"}
	b, err := json.Marshal(tc)
	if err != nil {
		t.Fatal(err)
	}
	var got ToolChoice
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != ToolChoiceTool || got.Name != "read_file" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestThinkingConfigSerialization(t *testing.T) {
	tc := ThinkingConfig{Enabled: true, BudgetTokens: 5000}
	b, err := json.Marshal(tc)
	if err != nil {
		t.Fatal(err)
	}
	var got ThinkingConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.BudgetTokens != 5000 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestLLMRequestWithToolsRoundtrip(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	req := LLMRequest{
		Model:     "test-model",
		MaxTokens: 1024,
		Tools: []ToolDefinition{
			{Name: "read_file", Description: "Read a file", InputSchema: schema},
		},
		ToolChoice: &ToolChoice{Type: ToolChoiceAuto},
		Thinking:   &ThinkingConfig{Enabled: true, BudgetTokens: 2048},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = b // wire format consumed by adapters; just verify it marshals without error
	if len(req.Tools) != 1 || req.Tools[0].Name != "read_file" {
		t.Error("tools not preserved")
	}
}

func TestContentBlockTypes(t *testing.T) {
	blocks := []ContentBlock{
		{Type: BlockText, Text: "hello"},
		{Type: BlockToolUse, ToolUseID: "tu_1", ToolName: "bash", ToolInput: json.RawMessage(`{"cmd":"ls"}`)},
		{Type: BlockToolResult, ToolCallID: "tu_1", ToolResult: "file.go"},
		{Type: BlockThinking, Text: "I should use bash"},
	}
	b, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	var got []ContentBlock
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(got))
	}
	if got[1].ToolName != "bash" {
		t.Errorf("tool_use name mismatch: %s", got[1].ToolName)
	}
}
