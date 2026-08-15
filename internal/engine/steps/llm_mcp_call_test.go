package steps

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// â"€â"€ helpers â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func newMCPCallTestContext() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 16)
	ctx.IntSlots = make([]int64, 8)
	ctx.BoolSlots = make([]bool, 8)
	return ctx
}

func runMCPCallInstruction(instr engine.Instruction, ctx *rctx.Context) int16 {
	state := &engine.ExecutionState{PC: 0}
	return instr.Action(ctx, state)
}

// writeMCPCallSuccessResp writes a JSON-RPC 2.0 tools/call success response.
func writeMCPCallSuccessResp(w http.ResponseWriter, content any) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"content": content,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// writeMCPCallErrorResp writes a JSON-RPC 2.0 error response.
func writeMCPCallErrorResp(w http.ResponseWriter, code int, message string) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// â"€â"€ TestMCPCallTool_Success â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPCallTool_Success(t *testing.T) {
	content := []map[string]any{
		{"type": "text", "text": "The weather in Paris is sunny, 22Â°C."},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request is a proper JSON-RPC tools/call.
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req["method"] != "tools/call" {
			http.Error(w, "wrong method", 400)
			return
		}
		writeMCPCallSuccessResp(w, content)
	}))
	defer srv.Close()

	ctx := newMCPCallTestContext()
	ctx.ByteSlots[0] = []byte("get_weather")
	ctx.ByteSlots[1] = []byte(`{"location":"Paris"}`)

	cfg := MCPCallToolConfig{
		ServerURL:    srv.URL,
		TimeoutMs:    2000,
		ToolNameSlot: 0,
		ArgsSlot:     1,
		ResultSlot:   2,
	}
	instr := MCPCallTool(cfg)
	next := runMCPCallInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d (failed=%v, errMsg=%s)", next, ctx.Failed, ctx.ErrorMsg)
	}
	if ctx.Failed {
		t.Fatalf("unexpected failure: %s", ctx.ErrorMsg)
	}

	var result []map[string]any
	if err := json.Unmarshal(ctx.ByteSlots[2], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result))
	}
	if result[0]["type"] != "text" {
		t.Errorf("unexpected content type: %v", result[0]["type"])
	}
}

// â"€â"€ TestMCPCallTool_EmptyToolName_Skips â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPCallTool_EmptyToolName_Skips(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		writeMCPCallSuccessResp(w, []any{})
	}))
	defer srv.Close()

	ctx := newMCPCallTestContext()
	// ToolNameSlot (0) is empty — no tool name set.

	cfg := MCPCallToolConfig{
		ServerURL:    srv.URL,
		TimeoutMs:    2000,
		ToolNameSlot: 0,
		ArgsSlot:     1,
		ResultSlot:   2,
	}
	instr := MCPCallTool(cfg)
	next := runMCPCallInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("expected PC+1=1 (skip), got %d", next)
	}
	if ctx.Failed {
		t.Errorf("expected no failure, got: %s", ctx.ErrorMsg)
	}
	if callCount != 0 {
		t.Errorf("server should not have been called, callCount=%d", callCount)
	}
	if ctx.ByteSlots[2] != nil {
		t.Error("result slot should remain nil when skipped")
	}
}

// â"€â"€ TestMCPCallTool_EmptyArgs_UsesEmptyObject â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPCallTool_EmptyArgs_UsesEmptyObject(t *testing.T) {
	var receivedArgs json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		params, _ := req["params"].(map[string]any)
		argsBytes, _ := json.Marshal(params["arguments"])
		receivedArgs = argsBytes
		writeMCPCallSuccessResp(w, []any{map[string]any{"type": "text", "text": "ok"}})
	}))
	defer srv.Close()

	ctx := newMCPCallTestContext()
	ctx.ByteSlots[0] = []byte("list_files")
	// ArgsSlot (1) is empty — should default to {}.

	cfg := MCPCallToolConfig{
		ServerURL:    srv.URL,
		TimeoutMs:    2000,
		ToolNameSlot: 0,
		ArgsSlot:     1,
		ResultSlot:   2,
	}
	instr := MCPCallTool(cfg)
	next := runMCPCallInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d (err: %s)", next, ctx.ErrorMsg)
	}

	// The args sent should be an empty object {}.
	var argsMap map[string]any
	if err := json.Unmarshal(receivedArgs, &argsMap); err != nil {
		t.Fatalf("args unmarshal: %v", err)
	}
	if len(argsMap) != 0 {
		t.Errorf("expected empty args object, got: %v", argsMap)
	}
}

// â"€â"€ TestMCPCallTool_InvalidJSONArgs_Returns400 â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPCallTool_InvalidJSONArgs_Returns400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should never be called.
		http.Error(w, "should not be called", 500)
	}))
	defer srv.Close()

	ctx := newMCPCallTestContext()
	ctx.ByteSlots[0] = []byte("some_tool")
	ctx.ByteSlots[1] = []byte(`not valid json at all`)

	cfg := MCPCallToolConfig{
		ServerURL:    srv.URL,
		TimeoutMs:    2000,
		ToolNameSlot: 0,
		ArgsSlot:     1,
		ResultSlot:   2,
	}
	instr := MCPCallTool(cfg)
	next := runMCPCallInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("expected StopPlan, got %d", next)
	}
	if !ctx.Failed {
		t.Error("expected ctx.Failed=true")
	}
	if ctx.ResponseStatus != 400 {
		t.Errorf("expected status 400, got %d", ctx.ResponseStatus)
	}
}

// â"€â"€ TestMCPCallTool_JSONRPCError_Returns502 â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPCallTool_JSONRPCError_Returns502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMCPCallErrorResp(w, -32602, "invalid params: tool not found")
	}))
	defer srv.Close()

	ctx := newMCPCallTestContext()
	ctx.ByteSlots[0] = []byte("nonexistent_tool")
	ctx.ByteSlots[1] = []byte(`{}`)

	cfg := MCPCallToolConfig{
		ServerURL:    srv.URL,
		TimeoutMs:    2000,
		ToolNameSlot: 0,
		ArgsSlot:     1,
		ResultSlot:   2,
	}
	instr := MCPCallTool(cfg)
	next := runMCPCallInstruction(instr, ctx)

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

// â"€â"€ TestMCPCallTool_429Retry_ThenSuccess â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPCallTool_429Retry_ThenSuccess(t *testing.T) {
	var callCount atomic.Int32
	content := []map[string]any{{"type": "text", "text": "result after retry"}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			// First call: 429 rate limit.
			w.WriteHeader(429)
			return
		}
		writeMCPCallSuccessResp(w, content)
	}))
	defer srv.Close()

	ctx := newMCPCallTestContext()
	ctx.ByteSlots[0] = []byte("search")
	ctx.ByteSlots[1] = []byte(`{"query":"hello"}`)

	cfg := MCPCallToolConfig{
		ServerURL:    srv.URL,
		TimeoutMs:    2000,
		ToolNameSlot: 0,
		ArgsSlot:     1,
		ResultSlot:   2,
	}
	instr := MCPCallTool(cfg)
	next := runMCPCallInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("expected PC+1=1 after retry, got %d (err: %s)", next, ctx.ErrorMsg)
	}
	if ctx.Failed {
		t.Fatalf("unexpected failure: %s", ctx.ErrorMsg)
	}
	if callCount.Load() < 2 {
		t.Errorf("expected at least 2 calls (1 retry), got %d", callCount.Load())
	}

	var result []map[string]any
	if err := json.Unmarshal(ctx.ByteSlots[2], &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result) != 1 || result[0]["text"] != "result after retry" {
		t.Errorf("unexpected result: %v", result)
	}
}

// â"€â"€ TestMCPCallTool_503AllRetries_Returns502 â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestMCPCallTool_503AllRetries_Returns502(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	ctx := newMCPCallTestContext()
	ctx.ByteSlots[0] = []byte("some_tool")
	ctx.ByteSlots[1] = []byte(`{}`)

	cfg := MCPCallToolConfig{
		ServerURL:    srv.URL,
		TimeoutMs:    2000,
		ToolNameSlot: 0,
		ArgsSlot:     1,
		ResultSlot:   2,
	}
	instr := MCPCallTool(cfg)
	next := runMCPCallInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("expected StopPlan after all retries, got %d", next)
	}
	if !ctx.Failed {
		t.Error("expected ctx.Failed=true")
	}
	if ctx.ResponseStatus != 502 {
		t.Errorf("expected status 502, got %d", ctx.ResponseStatus)
	}
	if callCount.Load() != 3 {
		t.Errorf("expected 3 attempts (1+2 retries), got %d", callCount.Load())
	}
}
