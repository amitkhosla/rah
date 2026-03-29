package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const protocolVersion = "2024-11-05"

// Server is the virtual MCP HTTP server.
// It speaks MCP JSON-RPC 2.0 over HTTP POST.
// Mount at /mcp on any http.ServeMux.
type Server struct {
	registry   *Registry
	gatewayURL string // base URL of the gateway, e.g. "http://localhost:8080"
	httpClient *http.Client
}

// NewServer creates a new MCP server backed by the given tool registry.
// gatewayBaseURL is the base URL used when a tool's GatewayURL is relative
// (i.e. starts with "/").
func NewServer(registry *Registry, gatewayBaseURL string) *Server {
	return &Server{
		registry:   registry,
		gatewayURL: gatewayBaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// ServeHTTP handles MCP JSON-RPC 2.0 requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Only POST is valid for JSON-RPC over HTTP.
	if r.Method != http.MethodPost {
		writeError(w, nil, -32700, "only POST is supported")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
	if err != nil {
		writeError(w, nil, -32700, "failed to read request body")
		return
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, nil, -32700, "parse error: "+err.Error())
		return
	}

	switch req.Method {
	case "initialize":
		s.handleInitialize(w, req)
	case "notifications/initialized":
		// No-op notification — return empty success.
		writeResult(w, req.ID, map[string]any{})
	case "tools/list":
		s.handleToolsList(w, req)
	case "tools/call":
		s.handleToolsCall(w, req)
	default:
		writeError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// handleInitialize responds to the MCP initialize handshake.
func (s *Server) handleInitialize(w http.ResponseWriter, req Request) {
	result := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "rah-gateway",
			"version": "1.0",
		},
	}
	writeResult(w, req.ID, result)
}

// handleToolsList responds with the current set of registered tools.
func (s *Server) handleToolsList(w http.ResponseWriter, req Request) {
	entries := s.registry.List()
	tools := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		t := map[string]any{
			"name":        e.Name,
			"description": e.Description,
		}
		if len(e.InputSchema) > 0 {
			t["inputSchema"] = json.RawMessage(e.InputSchema)
		} else {
			t["inputSchema"] = map[string]any{"type": "object"}
		}
		tools = append(tools, t)
	}
	writeResult(w, req.ID, map[string]any{"tools": tools})
}

// toolsCallParams is the expected shape of tools/call params.
type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// handleToolsCall dispatches a tool call to the gateway.
func (s *Server) handleToolsCall(w http.ResponseWriter, req Request) {
	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(w, req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	tool, ok := s.registry.Get(params.Name)
	if !ok {
		writeError(w, req.ID, -32602, fmt.Sprintf("unknown tool: %s", params.Name))
		return
	}

	// Resolve full URL: if GatewayURL starts with "/" treat it as a path on
	// the configured gateway base URL.
	targetURL := tool.GatewayURL
	if len(targetURL) > 0 && targetURL[0] == '/' {
		targetURL = s.gatewayURL + targetURL
	}

	method := tool.Method
	if method == "" {
		method = http.MethodPost
	}

	// Build body — use the raw arguments JSON so the gateway receives the
	// exact object the caller provided.
	body := params.Arguments
	if len(body) == 0 {
		body = []byte("{}")
	}

	httpReq, err := http.NewRequest(method, targetURL, bytes.NewReader(body))
	if err != nil {
		writeToolResult(w, req.ID, fmt.Sprintf("failed to build request: %v", err), true)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		writeToolResult(w, req.ID, fmt.Sprintf("gateway request failed: %v", err), true)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		writeToolResult(w, req.ID, fmt.Sprintf("failed to read gateway response: %v", err), true)
		return
	}

	isError := resp.StatusCode >= 400
	writeToolResult(w, req.ID, string(respBody), isError)
}

// writeResult encodes a successful JSON-RPC 2.0 response.
func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// writeError encodes a JSON-RPC 2.0 error response.
func writeError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// writeToolResult encodes a tools/call result envelope.
func writeToolResult(w http.ResponseWriter, id json.RawMessage, text string, isError bool) {
	result := map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"isError": isError,
	}
	writeResult(w, id, result)
}
