package steps

import (
	"encoding/json"
	"testing"
)

func TestOpenAIMarshalWithTools(t *testing.T) {
	o := &openAIAdapter{}
	req := LLMRequest{
		Model:     "gpt-4o",
		MaxTokens: 1024,
		Messages:  []CanonicalMessage{{Role: RoleUser, Content: "Read /tmp/test.txt"}},
		Tools: []ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
		ToolChoice: &ToolChoice{Type: ToolChoiceAuto},
	}
	b, err := o.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["tools"]; !ok {
		t.Error("tools missing")
	}
	if string(wire["tool_choice"]) != `"auto"` {
		t.Errorf("tool_choice wrong: %s", wire["tool_choice"])
	}
}

func TestOpenAIUnmarshalToolCalls(t *testing.T) {
	o := &openAIAdapter{}
	body := []byte(`{
        "choices":[{"message":{
            "role":"assistant","content":null,
            "tool_calls":[{"id":"tc_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"/tmp/test.txt\"}"}}]
        },"finish_reason":"tool_calls"}],
        "usage":{"prompt_tokens":100,"completion_tokens":30}
    }`)
	resp, err := o.Unmarshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "tool_calls" {
		t.Errorf("stop reason: %s", resp.StopReason)
	}
	if len(resp.ContentBlocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(resp.ContentBlocks))
	}
	if resp.ContentBlocks[0].Type != BlockToolUse {
		t.Errorf("block type: %s", resp.ContentBlocks[0].Type)
	}
	if resp.ContentBlocks[0].ToolName != "read_file" {
		t.Errorf("tool name: %s", resp.ContentBlocks[0].ToolName)
	}
}

func TestOpenAIUnmarshal_CachedTokens(t *testing.T) {
	adapter := &openAIAdapter{}
	body := []byte(`{
		"choices": [{"message": {"role": "assistant", "content": "hello"}, "finish_reason": "stop"}],
		"usage": {
			"prompt_tokens": 150,
			"completion_tokens": 30,
			"total_tokens": 180,
			"prompt_tokens_details": {
				"cached_tokens": 120
			}
		}
	}`)
	resp, err := adapter.Unmarshal(body)
	if err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if resp.InputTokens != 150 {
		t.Errorf("expected InputTokens=150, got %d", resp.InputTokens)
	}
	if resp.CacheReadTokens != 120 {
		t.Errorf("expected CacheReadTokens=120, got %d", resp.CacheReadTokens)
	}
}
