package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer returns a Server wired to a fresh Registry plus a fake gateway
// (backed by fakeGateway).  It returns the server and the fake gateway's URL.
func newTestServer(t *testing.T, fakeGateway *httptest.Server) *Server {
	t.Helper()
	reg := NewRegistry()
	baseURL := ""
	if fakeGateway != nil {
		baseURL = fakeGateway.URL
	}
	return NewServer(reg, baseURL)
}

// rpc issues a JSON-RPC request to the server and returns the parsed Response.
func rpc(t *testing.T, srv *Server, method string, params any) Response {
	t.Helper()

	reqObj := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		reqObj["params"] = json.RawMessage(raw)
	}

	body, _ := json.Marshal(reqObj)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	var resp Response
	if err := json.NewDecoder(w.Result().Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// TestMCPInitialize verifies that the initialize method returns MCP capabilities.
func TestMCPInitialize(t *testing.T) {
	srv := newTestServer(t, nil)
	resp := rpc(t, srv, "initialize", nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object")
	}
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, want %s", result["protocolVersion"], protocolVersion)
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok || caps["tools"] == nil {
		t.Errorf("capabilities.tools missing")
	}
}

// TestMCPToolsList verifies that registered tools appear in the tools/list response.
func TestMCPToolsList(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolEntry{
		Name:        "greet",
		Description: "Say hello",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
		GatewayURL:  "/api/greet",
		Method:      "POST",
	})
	srv := NewServer(reg, "http://localhost:8080")

	resp := rpc(t, srv, "tools/list", nil)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "greet" {
		t.Errorf("tool name = %v, want greet", tool["name"])
	}
}

// TestMCPToolsCall verifies a successful tool call proxies to the gateway and
// returns a text content item.
func TestMCPToolsCall(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"greeting":"hello"}`))
	}))
	defer fake.Close()

	reg := NewRegistry()
	reg.Register(ToolEntry{
		Name:       "greet",
		GatewayURL: fake.URL + "/api/greet",
		Method:     "POST",
	})
	srv := NewServer(reg, fake.URL)

	resp := rpc(t, srv, "tools/call", map[string]any{
		"name":      "greet",
		"arguments": map[string]any{"name": "World"},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	if result["isError"] != false {
		t.Errorf("isError should be false for 200 response")
	}
	content := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("content is empty")
	}
	item := content[0].(map[string]any)
	if item["type"] != "text" {
		t.Errorf("content type = %v, want text", item["type"])
	}
	if item["text"] != `{"greeting":"hello"}` {
		t.Errorf("content text = %v", item["text"])
	}
}

// TestMCPToolsCallGatewayError verifies that a 5xx gateway response sets isError:true.
func TestMCPToolsCallGatewayError(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer fake.Close()

	reg := NewRegistry()
	reg.Register(ToolEntry{
		Name:       "broken",
		GatewayURL: fake.URL + "/api/broken",
		Method:     "POST",
	})
	srv := NewServer(reg, fake.URL)

	resp := rpc(t, srv, "tools/call", map[string]any{
		"name":      "broken",
		"arguments": map[string]any{},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	if result["isError"] != true {
		t.Errorf("isError should be true for 500 response, got %v", result["isError"])
	}
}

// TestMCPUnknownMethod verifies that unrecognised methods return -32601.
func TestMCPUnknownMethod(t *testing.T) {
	srv := newTestServer(t, nil)
	resp := rpc(t, srv, "bogus/method", nil)
	if resp.Error == nil {
		t.Fatal("expected error, got none")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("code = %d, want -32601", resp.Error.Code)
	}
}

// TestMCPInvalidJSON verifies that a malformed request body returns -32700.
func TestMCPInvalidJSON(t *testing.T) {
	srv := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString("{not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	var resp Response
	if err := json.NewDecoder(w.Result().Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error, got none")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("code = %d, want -32700", resp.Error.Code)
	}
}

// TestMCPToolsCallNotFound verifies that calling an unregistered tool returns -32602.
func TestMCPToolsCallNotFound(t *testing.T) {
	srv := newTestServer(t, nil)
	resp := rpc(t, srv, "tools/call", map[string]any{
		"name":      "no_such_tool",
		"arguments": map[string]any{},
	})
	if resp.Error == nil {
		t.Fatal("expected error, got none")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("code = %d, want -32602", resp.Error.Code)
	}
}
