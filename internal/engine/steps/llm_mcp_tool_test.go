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

// â"€â"€ helpers â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func makeMCPToolCallCtx() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 48)
	ctx.IntSlots = make([]int64, 16)
	ctx.BoolSlots = make([]bool, 8)
	return ctx
}

func runMCPToolCallInstruction(instr engine.Instruction, ctx *rctx.Context) int16 {
	state := &engine.ExecutionState{PC: 0}
	return instr.Action(ctx, state)
}

// â"€â"€ TestMCPToolCallSuccess â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPToolCallSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "Hello from MCP"},
				},
				"isError": false,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := MCPCallConfig{
		Server: mockMCPServerConfig(server.URL),
		ToolName: "test_tool",
		InputSlot: -1,  // use default {}
		ResultSlot: 0,
		TimeoutMs: 5000,
	}

	ctx := makeMCPToolCallCtx()
	instr := MCPToolCall(cfg)
	result := runMCPToolCallInstruction(instr, ctx)

	if result != 1 {
		t.Errorf("Expected PC + 1, got %d", result)
	}

	if len(ctx.ByteSlots[0]) == 0 {
		t.Error("Result slot should have content")
	}

	if string(ctx.ByteSlots[0]) != "Hello from MCP" {
		t.Errorf("Expected 'Hello from MCP', got %s", string(ctx.ByteSlots[0]))
	}
}

// â"€â"€ TestMCPToolCallMultiContent â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPToolCallMultiContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "First "},
					{"type": "text", "text": "Second"},
				},
				"isError": false,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := MCPCallConfig{
		Server: mockMCPServerConfig(server.URL),
		ToolName: "test_tool",
		InputSlot: -1,
		ResultSlot: 0,
		TimeoutMs: 5000,
	}

	ctx := makeMCPToolCallCtx()
	instr := MCPToolCall(cfg)
	result := runMCPToolCallInstruction(instr, ctx)

	if result != 1 {
		t.Errorf("Expected PC + 1, got %d", result)
	}

	if string(ctx.ByteSlots[0]) != "First Second" {
		t.Errorf("Expected 'First Second', got %s", string(ctx.ByteSlots[0]))
	}
}

// â"€â"€ TestMCPToolCallIsError â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPToolCallIsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"content": []map[string]any{},
				"isError": true,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := MCPCallConfig{
		Server: mockMCPServerConfig(server.URL),
		ToolName: "test_tool",
		InputSlot: -1,
		ResultSlot: 0,
		TimeoutMs: 5000,
	}

	ctx := makeMCPToolCallCtx()
	instr := MCPToolCall(cfg)
	result := runMCPToolCallInstruction(instr, ctx)

	if result != engine.StopPlan {
		t.Errorf("Expected StopPlan, got %d", result)
	}

	if ctx.ResponseStatus != 502 {
		t.Errorf("Expected status 502, got %d", ctx.ResponseStatus)
	}
}

// â"€â"€ TestMCPToolCallRPCError â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPToolCallRPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]any{
				"code":    -32601,
				"message": "Method not found",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := MCPCallConfig{
		Server: mockMCPServerConfig(server.URL),
		ToolName: "test_tool",
		InputSlot: -1,
		ResultSlot: 0,
		TimeoutMs: 5000,
	}

	ctx := makeMCPToolCallCtx()
	instr := MCPToolCall(cfg)
	result := runMCPToolCallInstruction(instr, ctx)

	if result != engine.StopPlan {
		t.Errorf("Expected StopPlan, got %d", result)
	}

	if ctx.ResponseStatus != 502 {
		t.Errorf("Expected status 502, got %d", ctx.ResponseStatus)
	}
}

// â"€â"€ TestMCPToolCallHTTP500 â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPToolCallHTTP500(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := MCPCallConfig{
		Server: mockMCPServerConfig(server.URL),
		ToolName: "test_tool",
		InputSlot: -1,
		ResultSlot: 0,
		TimeoutMs: 5000,
	}

	ctx := makeMCPToolCallCtx()
	instr := MCPToolCall(cfg)
	result := runMCPToolCallInstruction(instr, ctx)

	if result != engine.StopPlan {
		t.Errorf("Expected StopPlan, got %d", result)
	}

	if ctx.ResponseStatus != 502 {
		t.Errorf("Expected status 502, got %d", ctx.ResponseStatus)
	}
}

// â"€â"€ TestMCPToolCallEmptyInput â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPToolCallEmptyInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// When no input slot is provided, empty {} is used as default
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "Success"},
				},
				"isError": false,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := MCPCallConfig{
		Server: mockMCPServerConfig(server.URL),
		ToolName: "test_tool",
		InputSlot: -1,  // No input slot, should use {}
		ResultSlot: 0,
		TimeoutMs: 5000,
	}

	ctx := makeMCPToolCallCtx()
	instr := MCPToolCall(cfg)
	result := runMCPToolCallInstruction(instr, ctx)

	if result != 1 {
		t.Errorf("Expected PC + 1, got %d", result)
	}
}

// â"€â"€ TestMCPToolCallNoAuth â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPToolCallNoAuth(t *testing.T) {
	hasAuthHeader := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			hasAuthHeader = true
		}
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "OK"},
				},
				"isError": false,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := MCPCallConfig{
		Server: mockMCPServerConfig(server.URL),
		APIKey: "",  // No API key
		ToolName: "test_tool",
		InputSlot: -1,
		ResultSlot: 0,
		TimeoutMs: 5000,
	}

	ctx := makeMCPToolCallCtx()
	instr := MCPToolCall(cfg)
	result := runMCPToolCallInstruction(instr, ctx)

	if result != 1 {
		t.Errorf("Expected PC + 1, got %d", result)
	}

	if hasAuthHeader {
		t.Error("Authorization header should not be sent when APIKey is empty")
	}
}

// â"€â"€ TestMCPToolCallWithAuth â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPToolCallWithAuth(t *testing.T) {
	authHeaderValue := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaderValue = r.Header.Get("Authorization")
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "OK"},
				},
				"isError": false,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := MCPCallConfig{
		Server: mockMCPServerConfig(server.URL),
		APIKey: "secret-key-123",
		ToolName: "test_tool",
		InputSlot: -1,
		ResultSlot: 0,
		TimeoutMs: 5000,
	}

	ctx := makeMCPToolCallCtx()
	instr := MCPToolCall(cfg)
	result := runMCPToolCallInstruction(instr, ctx)

	if result != 1 {
		t.Errorf("Expected PC + 1, got %d", result)
	}

	if authHeaderValue != "Bearer secret-key-123" {
		t.Errorf("Expected 'Bearer secret-key-123', got %s", authHeaderValue)
	}
}

// â"€â"€ helper to create mock server config â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func mockMCPServerConfig(url string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Alias:     "test_server",
		Transport: config.MCPTransportHTTP,
		URL:       url,
	}
}
