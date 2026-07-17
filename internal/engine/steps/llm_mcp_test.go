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

// â”€â”€ helpers â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func mcpServerCfg(url string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Alias:     "test_server",
		Transport: config.MCPTransportHTTP,
		URL:       url,
	}
}

func newMCPTestContext() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 16)
	ctx.IntSlots = make([]int64, 8)
	ctx.BoolSlots = make([]bool, 8)
	return ctx
}

func runMCPInstruction(instr engine.Instruction, ctx *rctx.Context) int16 {
	state := &engine.ExecutionState{PC: 0}
	return instr.Action(ctx, state)
}

// writeMCPSuccessResp writes a JSON-RPC 2.0 tools/list success response.
func writeMCPSuccessResp(w http.ResponseWriter, tools []map[string]any) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"tools": tools,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// â”€â”€ TestMCPListTools_NamesMode â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestMCPListTools_NamesMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMCPSuccessResp(w, []map[string]any{
			{"name": "tool1", "description": "First tool"},
			{"name": "tool2", "description": "Second tool"},
		})
	}))
	defer srv.Close()

	ctx := newMCPTestContext()
	cfg := MCPListToolsConfig{
		ServerConfig: mcpServerCfg(srv.URL),
		Mode:         MCPLoadNames,
		ResultSlot:   0,
		TimeoutMs:    2000,
	}
	instr := MCPListTools(cfg)
	next := runMCPInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d (failed=%v, errMsg=%s)", next, ctx.Failed, ctx.ErrorMsg)
	}
	if ctx.Failed {
		t.Fatalf("unexpected failure: %s", ctx.ErrorMsg)
	}

	var names []string
	if err := json.Unmarshal(ctx.ByteSlots[0], &names); err != nil {
		t.Fatalf("unmarshal names: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	if names[0] != "tool1" || names[1] != "tool2" {
		t.Errorf("unexpected names: %v", names)
	}
}

// â”€â”€ TestMCPListTools_BriefMode â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestMCPListTools_BriefMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMCPSuccessResp(w, []map[string]any{
			{"name": "tool1", "description": "Does something useful"},
		})
	}))
	defer srv.Close()

	ctx := newMCPTestContext()
	cfg := MCPListToolsConfig{
		ServerConfig: mcpServerCfg(srv.URL),
		Mode:         MCPLoadBrief,
		ResultSlot:   1,
		TimeoutMs:    2000,
	}
	instr := MCPListTools(cfg)
	next := runMCPInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}

	type briefTool struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	var tools []briefTool
	if err := json.Unmarshal(ctx.ByteSlots[1], &tools); err != nil {
		t.Fatalf("unmarshal brief: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "tool1" {
		t.Errorf("unexpected name: %s", tools[0].Name)
	}
	if tools[0].Description != "Does something useful" {
		t.Errorf("unexpected description: %s", tools[0].Description)
	}
}

// â”€â”€ TestMCPListTools_FullMode â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestMCPListTools_FullMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMCPSuccessResp(w, []map[string]any{
			{
				"name":        "search",
				"description": "Search the web",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	ctx := newMCPTestContext()
	cfg := MCPListToolsConfig{
		ServerConfig: mcpServerCfg(srv.URL),
		Mode:         MCPLoadFull,
		ResultSlot:   2,
		TimeoutMs:    2000,
	}
	instr := MCPListTools(cfg)
	next := runMCPInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d (err: %s)", next, ctx.ErrorMsg)
	}

	var tools []ToolDefinition
	if err := json.Unmarshal(ctx.ByteSlots[2], &tools); err != nil {
		t.Fatalf("unmarshal full: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "search" {
		t.Errorf("unexpected name: %s", tools[0].Name)
	}
	if tools[0].InputSchema == nil {
		t.Error("expected non-nil inputSchema")
	}
}

// â”€â”€ TestMCPListTools_ServerError_Returns502 â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestMCPListTools_ServerError_Returns502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]any{
				"code":    -32601,
				"message": "method not found",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ctx := newMCPTestContext()
	cfg := MCPListToolsConfig{
		ServerConfig: mcpServerCfg(srv.URL),
		Mode:         MCPLoadFull,
		ResultSlot:   0,
		TimeoutMs:    2000,
	}
	instr := MCPListTools(cfg)
	next := runMCPInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("expected StopPlan, got %d", next)
	}
	if !ctx.Failed {
		t.Error("expected ctx.Failed=true")
	}
	if ctx.ResponseStatus != 502 {
		t.Errorf("expected status 502, got %d", ctx.ResponseStatus)
	}
}

// â”€â”€ TestMCPListTools_UnsupportedTransport_Returns501 â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestMCPListTools_UnsupportedTransport_Returns501(t *testing.T) {
	ctx := newMCPTestContext()
	cfg := MCPListToolsConfig{
		ServerConfig: config.MCPServerConfig{
			Alias:     "stdio_server",
			Transport: config.MCPTransportStdio,
			Command:   []string{"python", "server.py"},
		},
		Mode:       MCPLoadFull,
		ResultSlot: 0,
		TimeoutMs:  2000,
	}
	instr := MCPListTools(cfg)
	next := runMCPInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("expected StopPlan, got %d", next)
	}
	if !ctx.Failed {
		t.Error("expected ctx.Failed=true")
	}
	if ctx.ResponseStatus != 501 {
		t.Errorf("expected status 501, got %d", ctx.ResponseStatus)
	}
}

// â”€â”€ TestMCPFetchSchemas_FiltersToSelected â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestMCPFetchSchemas_FiltersToSelected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMCPSuccessResp(w, []map[string]any{
			{"name": "search", "description": "Search"},
			{"name": "calculator", "description": "Calculate"},
			{"name": "weather", "description": "Get weather"},
		})
	}))
	defer srv.Close()

	ctx := newMCPTestContext()

	// Put selected tool names in slot 0
	selected, _ := json.Marshal([]string{"search", "weather"})
	ctx.ByteSlots[0] = selected

	cfg := MCPFetchSchemasConfig{
		ServerConfig: mcpServerCfg(srv.URL),
		SelectedSlot: 0,
		ResultSlot:   1,
		TimeoutMs:    2000,
	}
	instr := MCPFetchSchemas(cfg)
	next := runMCPInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d (err: %s)", next, ctx.ErrorMsg)
	}

	var tools []ToolDefinition
	if err := json.Unmarshal(ctx.ByteSlots[1], &tools); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools after filter, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["search"] || !names["weather"] {
		t.Errorf("unexpected tools: %v", names)
	}
	if names["calculator"] {
		t.Error("calculator should have been filtered out")
	}
}

// â”€â”€ TestMCPFetchSchemas_EmptySelected_Skips â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestMCPFetchSchemas_EmptySelected_Skips(t *testing.T) {
	// Server should never be called when selected slot is empty.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		writeMCPSuccessResp(w, []map[string]any{
			{"name": "tool1"},
		})
	}))
	defer srv.Close()

	ctx := newMCPTestContext()
	// slot 0 is empty (zero value []byte)

	cfg := MCPFetchSchemasConfig{
		ServerConfig: mcpServerCfg(srv.URL),
		SelectedSlot: 0,
		ResultSlot:   1,
		TimeoutMs:    2000,
	}
	instr := MCPFetchSchemas(cfg)
	next := runMCPInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}
	if ctx.Failed {
		t.Errorf("expected no failure, got: %s", ctx.ErrorMsg)
	}
	if callCount != 0 {
		t.Errorf("server should not have been called, callCount=%d", callCount)
	}
	if ctx.ByteSlots[1] != nil {
		t.Error("result slot should remain nil when skipped")
	}
}
