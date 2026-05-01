package steps

import (
	"encoding/json"
	"testing"
)

func TestAnthropicMarshalWithTools(t *testing.T) {
	a := &anthropicAdapter{}
	req := LLMRequest{
		Model:     "claude-sonnet-4-6",
		MaxTokens: 1024,
		Messages:  []CanonicalMessage{{Role: RoleUser, Content: "Read /tmp/test.txt"}},
		Tools: []ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
		ToolChoice: &ToolChoice{Type: ToolChoiceAuto},
	}
	b, err := a.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["tools"]; !ok {
		t.Error("tools field missing from marshalled request")
	}
	if _, ok := wire["tool_choice"]; !ok {
		t.Error("tool_choice field missing from marshalled request")
	}
}

func TestAnthropicMarshalWithThinking(t *testing.T) {
	a := &anthropicAdapter{}
	req := LLMRequest{
		Model:     "claude-sonnet-4-6",
		MaxTokens: 8000,
		Messages:  []CanonicalMessage{{Role: RoleUser, Content: "Think hard"}},
		Thinking:  &ThinkingConfig{Enabled: true, BudgetTokens: 5000},
	}
	b, err := a.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["thinking"]; !ok {
		t.Error("thinking field missing")
	}
}

func TestAnthropicMarshalToolHistory(t *testing.T) {
	a := &anthropicAdapter{}
	req := LLMRequest{
		Model:     "claude-sonnet-4-6",
		MaxTokens: 1024,
		Messages: []CanonicalMessage{
			{Role: RoleUser, Content: "Read /tmp/test.txt"},
			{Role: RoleAssistant, ContentBlocks: []ContentBlock{
				{Type: BlockToolUse, ToolUseID: "tu_1", ToolName: "read_file", ToolInput: json.RawMessage(`{"path":"/tmp/test.txt"}`)},
			}},
			{Role: RoleUser, ContentBlocks: []ContentBlock{
				{Type: BlockToolResult, ToolCallID: "tu_1", ToolResult: "file contents here"},
			}},
		},
	}
	b, err := a.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	// Verify messages array has 3 entries and second has tool_use content
	var wire struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(wire.Messages))
	}
	// Second message should have array content with tool_use block
	if wire.Messages[1].Content[0] != '[' {
		t.Error("assistant tool_use message content should be a JSON array")
	}
}

func TestAnthropicUnmarshalToolUseResponse(t *testing.T) {
	a := &anthropicAdapter{}
	body := []byte(`{
        "id":"msg_1","type":"message","role":"assistant",
        "content":[
            {"type":"text","text":"I will read the file"},
            {"type":"tool_use","id":"tu_1","name":"read_file","input":{"path":"/tmp/test.txt"}}
        ],
        "stop_reason":"tool_use",
        "usage":{"input_tokens":100,"output_tokens":50}
    }`)
	resp, err := a.Unmarshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("expected stop_reason tool_use, got %s", resp.StopReason)
	}
	if len(resp.ContentBlocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(resp.ContentBlocks))
	}
	if resp.ContentBlocks[1].Type != BlockToolUse {
		t.Errorf("second block should be tool_use, got %s", resp.ContentBlocks[1].Type)
	}
	if resp.ContentBlocks[1].ToolName != "read_file" {
		t.Errorf("tool name mismatch: %s", resp.ContentBlocks[1].ToolName)
	}
	if resp.Content != "I will read the file" {
		t.Errorf("Content backward-compat field mismatch: %s", resp.Content)
	}
}

func TestAnthropicUnmarshalMixedBlocks(t *testing.T) {
	a := &anthropicAdapter{}
	body := []byte(`{
        "id":"msg_2","type":"message","role":"assistant",
        "content":[
            {"type":"thinking","thinking":"Let me think..."},
            {"type":"text","text":"Here is my answer"}
        ],
        "stop_reason":"end_turn",
        "usage":{"input_tokens":200,"output_tokens":80,"thinking_tokens":500}
    }`)
	resp, err := a.Unmarshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ContentBlocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(resp.ContentBlocks))
	}
	if resp.ContentBlocks[0].Type != BlockThinking {
		t.Errorf("first block should be thinking, got %s", resp.ContentBlocks[0].Type)
	}
	if resp.Content != "Here is my answer" {
		t.Errorf("Content compat field: %s", resp.Content)
	}
	if resp.ThinkingTokens != 500 {
		t.Errorf("ThinkingTokens: %d", resp.ThinkingTokens)
	}
}
