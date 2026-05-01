package steps

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGeminiMarshalWithTools(t *testing.T) {
	g := &geminiAdapter{}
	req := LLMRequest{
		Model:     "gemini-2.0-flash",
		MaxTokens: 1024,
		Messages:  []CanonicalMessage{{Role: RoleUser, Content: "Read a file"}},
		Tools: []ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
	}
	b, err := g.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["tools"]; !ok {
		t.Error("tools missing from gemini request")
	}
}

func TestGeminiMarshalSchemaSimplification(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","$schema":"http://json-schema.org/draft-07/schema","$defs":{},"additionalProperties":false,"properties":{"path":{"type":"string"}}}`)
	result := sanitizeSchemaForGemini(schema)
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["$schema"]; ok {
		t.Error("$schema should be removed")
	}
	if _, ok := m["$defs"]; ok {
		t.Error("$defs should be removed")
	}
	if _, ok := m["additionalProperties"]; ok {
		t.Error("additionalProperties should be removed")
	}
	if _, ok := m["properties"]; !ok {
		t.Error("properties should be preserved")
	}
}

func TestGeminiMarshalStripsThinkingFromHistory(t *testing.T) {
	g := &geminiAdapter{}
	req := LLMRequest{
		Model: "gemini-2.0-flash",
		Messages: []CanonicalMessage{
			{Role: RoleUser, Content: "Hello"},
			{Role: RoleAssistant, ContentBlocks: []ContentBlock{
				{Type: BlockThinking, Text: "I should say hi"},
				{Type: BlockText, Text: "Hi there!"},
			}},
			{Role: RoleUser, Content: "How are you?"},
		},
	}
	b, err := g.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	// Thinking blocks should not appear in the wire format
	if strings.Contains(string(b), "thinking") {
		t.Error("thinking blocks should be stripped from Gemini request history")
	}
}

func TestGeminiMarshalIDSanitization(t *testing.T) {
	g := &geminiAdapter{}
	// Tool use IDs with colons/dots should be sanitized
	req := LLMRequest{
		Model: "gemini-2.0-flash",
		Messages: []CanonicalMessage{
			{Role: RoleUser, Content: "Run bash"},
			{Role: RoleAssistant, ContentBlocks: []ContentBlock{
				{Type: BlockToolUse, ToolUseID: "toolu_01:abc.def", ToolName: "bash", ToolInput: json.RawMessage(`{"cmd":"ls"}`)},
			}},
			{Role: RoleUser, ContentBlocks: []ContentBlock{
				{Type: BlockToolResult, ToolCallID: "toolu_01:abc.def", ToolResult: "file.go"},
			}},
		},
	}
	b, err := g.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	// Colons and dots in IDs should be replaced
	if strings.Contains(string(b), "toolu_01:abc.def") {
		t.Error("tool use IDs with colons/dots should be sanitized for Gemini")
	}
}

func TestGeminiUnmarshalFunctionCall(t *testing.T) {
	g := &geminiAdapter{}
	body := []byte(`{
        "candidates":[{"content":{"parts":[
            {"functionCall":{"name":"read_file","args":{"path":"/tmp/test.txt"}}}
        ],"role":"model"},"finishReason":"STOP"}],
        "usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":30}
    }`)
	resp, err := g.Unmarshal(body)
	if err != nil {
		t.Fatal(err)
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
