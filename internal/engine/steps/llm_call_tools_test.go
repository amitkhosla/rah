package steps

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// mockAnthropicToolResponse returns a minimal Anthropic tool_use response body.
func mockAnthropicToolResponse() []byte {
	return []byte(`{
        "id":"msg_1","type":"message","role":"assistant",
        "content":[
            {"type":"text","text":"I will read the file"},
            {"type":"tool_use","id":"tu_1","name":"read_file","input":{"path":"/tmp/test.txt"}}
        ],
        "stop_reason":"tool_use",
        "usage":{"input_tokens":100,"output_tokens":40}
    }`)
}

func TestLLMCallPassesToolsToAdapter(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf [65536]byte
		n, _ := r.Body.Read(buf[:])
		capturedBody = make([]byte, n)
		copy(capturedBody, buf[:n])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(mockAnthropicToolResponse()) //nolint:errcheck
	}))
	defer srv.Close()

	tools := []ToolDefinition{{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	toolsJSON, _ := json.Marshal(tools)

	ctx := &rctx.Context{}
	ctx.InitSlots()
	ctx.ByteSlots[0] = []byte("Read /tmp/test.txt") // prompt
	ctx.ByteSlots[2] = toolsJSON                    // tools slot

	cfg := LLMCallConfig{
		ModelConfig: config.LLMModelConfig{
			Alias:   "test-claude",
			Adapter: config.AdapterAnthropic,
			BaseURL: srv.URL,
		},
		APIKey:           "test-key",
		PromptSlot:       0,
		ResultSlot:       1,
		MessagesSlot:     -1,
		SystemSlot:       -1,
		ModelSlot:        -1,
		APIKeySlot:       -1,
		InputTokensSlot:  -1,
		OutputTokensSlot: -1,
		StopReasonSlot:   -1,
		ModelConfigSlot:  -1,
		ToolsSlot:        2,
		ToolChoiceSlot:   -1,
		ThinkingSlot:     -1,
		ToolUseSlot:      3,
		ThinkingOutSlot:  -1,
		MaxTokens:        1024,
		MaxRetries:       0,
		TimeoutMs:        5000,
	}

	instr := LLMCall(cfg)
	state := &engine.ExecutionState{PC: 0}
	instr.Action(ctx, state)

	// Verify tools were in the wire request
	if !json.Valid(capturedBody) {
		t.Fatalf("captured body not valid JSON: %s", capturedBody)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(capturedBody, &wire); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}
	if _, ok := wire["tools"]; !ok {
		t.Error("tools field missing from wire request sent to adapter")
	}
}

func TestLLMCallWritesToolUseToSlot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(mockAnthropicToolResponse()) //nolint:errcheck
	}))
	defer srv.Close()

	ctx := &rctx.Context{}
	ctx.InitSlots()
	ctx.ByteSlots[0] = []byte("Read /tmp/test.txt")

	cfg := LLMCallConfig{
		ModelConfig: config.LLMModelConfig{
			Alias:   "test-claude",
			Adapter: config.AdapterAnthropic,
			BaseURL: srv.URL,
		},
		APIKey:           "test-key",
		PromptSlot:       0,
		ResultSlot:       1,
		MessagesSlot:     -1,
		SystemSlot:       -1,
		ModelSlot:        -1,
		APIKeySlot:       -1,
		InputTokensSlot:  -1,
		OutputTokensSlot: -1,
		StopReasonSlot:   -1,
		ModelConfigSlot:  -1,
		ToolsSlot:        -1,
		ToolChoiceSlot:   -1,
		ThinkingSlot:     -1,
		ToolUseSlot:      2, // write tool use blocks here
		ThinkingOutSlot:  -1,
		MaxTokens:        1024,
		MaxRetries:       0,
		TimeoutMs:        5000,
	}

	instr := LLMCall(cfg)
	state := &engine.ExecutionState{PC: 0}
	instr.Action(ctx, state)

	if len(ctx.ByteSlots[2]) == 0 {
		t.Fatal("tool use slot should be populated after LLM call returns tool_use")
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(ctx.ByteSlots[2], &blocks); err != nil {
		t.Fatalf("tool use slot contains invalid JSON: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 tool use block, got %d", len(blocks))
	}
	if blocks[0].ToolName != "read_file" {
		t.Errorf("tool name: %s", blocks[0].ToolName)
	}
}

func TestLLMCallNoToolsWhenSlotEmpty(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf [65536]byte
		n, _ := r.Body.Read(buf[:])
		capturedBody = make([]byte, n)
		copy(capturedBody, buf[:n])
		w.Header().Set("Content-Type", "application/json")
		// Return a simple text response
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`)) //nolint:errcheck
	}))
	defer srv.Close()

	ctx := &rctx.Context{}
	ctx.InitSlots()
	ctx.ByteSlots[0] = []byte("Hello")

	cfg := LLMCallConfig{
		ModelConfig: config.LLMModelConfig{
			Alias:   "test-claude",
			Adapter: config.AdapterAnthropic,
			BaseURL: srv.URL,
		},
		APIKey:           "test-key",
		PromptSlot:       0,
		ResultSlot:       1,
		MessagesSlot:     -1,
		SystemSlot:       -1,
		ModelSlot:        -1,
		APIKeySlot:       -1,
		InputTokensSlot:  -1,
		OutputTokensSlot: -1,
		StopReasonSlot:   -1,
		ModelConfigSlot:  -1,
		ToolsSlot:        2, // slot 2 is empty
		ToolChoiceSlot:   -1,
		ThinkingSlot:     -1,
		ToolUseSlot:      -1,
		ThinkingOutSlot:  -1,
		MaxTokens:        1024,
		MaxRetries:       0,
		TimeoutMs:        5000,
	}

	instr := LLMCall(cfg)
	state := &engine.ExecutionState{PC: 0}
	instr.Action(ctx, state)

	var wire map[string]json.RawMessage
	if err := json.Unmarshal(capturedBody, &wire); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}
	if _, ok := wire["tools"]; ok {
		t.Error("tools field should NOT be in wire request when tools slot is empty")
	}
}

func TestLLMCallFallbackChainPassesTools(t *testing.T) {
	// Test that when primary model fails, fallback chain gets the same tools
	var primaryBody, fallbackBody []byte

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf [65536]byte
		n, _ := r.Body.Read(buf[:])
		primaryBody = make([]byte, n)
		copy(primaryBody, buf[:n])
		// Primary fails
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`)) //nolint:errcheck
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf [65536]byte
		n, _ := r.Body.Read(buf[:])
		fallbackBody = make([]byte, n)
		copy(fallbackBody, buf[:n])
		// Fallback succeeds
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(mockAnthropicToolResponse()) //nolint:errcheck
	}))
	defer fallback.Close()

	tools := []ToolDefinition{{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	toolsJSON, _ := json.Marshal(tools)

	ctx := &rctx.Context{}
	ctx.InitSlots()
	ctx.ByteSlots[0] = []byte("Read /tmp/test.txt") // prompt
	ctx.ByteSlots[2] = toolsJSON                    // tools slot

	cfg := LLMCallConfig{
		ModelConfig: config.LLMModelConfig{
			Alias:   "primary-model",
			Adapter: config.AdapterAnthropic,
			BaseURL: primary.URL,
		},
		APIKey:           "test-key",
		PromptSlot:       0,
		ResultSlot:       1,
		ToolsSlot:        2,
		ToolUseSlot:      3,
		MaxTokens:        1024,
		MaxRetries:       0,
		TimeoutMs:        5000,
		FallbackChain: []FallbackEntry{{
			ModelConfig: config.LLMModelConfig{
				Alias:   "fallback-model",
				Adapter: config.AdapterAnthropic,
				BaseURL: fallback.URL,
			},
			APIKey: "test-key",
		}},
	}

	instr := LLMCall(cfg)
	state := &engine.ExecutionState{PC: 0}
	instr.Action(ctx, state)

	// Verify fallback was called with tools
	if !json.Valid(fallbackBody) {
		t.Fatalf("fallback body not valid JSON: %s", fallbackBody)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(fallbackBody, &wire); err != nil {
		t.Fatalf("failed to parse fallback request: %v", err)
	}
	if _, ok := wire["tools"]; !ok {
		t.Error("tools field missing from fallback chain request - fix not working!")
	}
	if len(ctx.ByteSlots[3]) == 0 {
		t.Error("tool use slot should be populated after fallback succeeds")
	}
}
