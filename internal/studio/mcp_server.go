package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ── JSON-RPC 2.0 types ────────────────────────────────────────────────────────

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── Tool definitions ──────────────────────────────────────────────────────────

// mcpToolDef is the MCP tools/list wire shape for a single tool.
type mcpToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// mcpToolMeta holds routing info used by proxyToolCall.
type mcpToolMeta struct {
	HTTPMethod  string // GET, POST, DELETE
	Path        string // e.g. "/ai/llm/models" — may contain "{param}" placeholder
	BodyParam   string // argument key holding the JSON body, empty if none
	PathParam   string // argument key replacing the "{param}" placeholder, empty if none
	QuerySuffix string // literal query string appended (e.g. "cursor=0&limit=50"), empty if none
}

var studioMCPTools = []mcpToolDef{
	// ── LLM models ──────────────────────────────────────────────────────────────
	{
		Name:        "register_llm_model",
		Description: "Register or update an LLM model configuration on the gateway.",
		InputSchema: bodySchema("JSON body of the LLM model definition"),
	},
	{
		Name:        "list_llm_models",
		Description: "List all registered LLM models on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "delete_llm_model",
		Description: "Delete an LLM model by its alias.",
		InputSchema: paramSchema("alias", "Alias of the LLM model to delete"),
	},

	// ── MCP servers ─────────────────────────────────────────────────────────────
	{
		Name:        "register_mcp_server",
		Description: "Register or update an external MCP server configuration on the gateway.",
		InputSchema: bodySchema("JSON body of the MCPServerConfig definition"),
	},
	{
		Name:        "list_mcp_servers",
		Description: "List all registered external MCP servers on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "delete_mcp_server",
		Description: "Delete an external MCP server by its alias.",
		InputSchema: paramSchema("alias", "Alias of the MCP server to delete"),
	},

	// ── Virtual MCP servers ──────────────────────────────────────────────────────
	{
		Name:        "create_virtual_server",
		Description: "Create or update a virtual MCP server definition on the gateway.",
		InputSchema: bodySchema("JSON body of the VirtualMCPServerDef definition"),
	},
	{
		Name:        "list_virtual_servers",
		Description: "List all virtual MCP server definitions on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "delete_virtual_server",
		Description: "Delete a virtual MCP server by name.",
		InputSchema: paramSchema("name", "Name of the virtual MCP server to delete"),
	},

	// ── API tools ────────────────────────────────────────────────────────────────
	{
		Name:        "register_api_tool",
		Description: "Register or update an API tool definition on the gateway.",
		InputSchema: bodySchema("JSON body of the APIToolDef definition"),
	},
	{
		Name:        "list_api_tools",
		Description: "List all registered API tool definitions on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "delete_api_tool",
		Description: "Delete an API tool by name.",
		InputSchema: paramSchema("name", "Name of the API tool to delete"),
	},

	// ── Gateway management ───────────────────────────────────────────────────────
	{
		Name:        "sync_flow",
		Description: "Sync a flow/API definition to the gateway (POST /sync).",
		InputSchema: bodySchema("JSON sync payload containing flows and APIs"),
	},
	{
		Name:        "list_gateway_apis",
		Description: "List all APIs currently registered on the gateway.",
		InputSchema: emptySchema(),
	},

	// ── Tenant management ────────────────────────────────────────────────────────
	{
		Name:        "create_tenant",
		Description: "Create or update a tenant on the gateway.",
		InputSchema: bodySchema("JSON body of the tenant definition"),
	},
	{
		Name:        "list_tenants",
		Description: "List all tenants (first 50) on the gateway.",
		InputSchema: emptySchema(),
	},
	{
		Name:        "delete_tenant",
		Description: "Delete a tenant by alias.",
		InputSchema: paramSchema("alias", "Alias of the tenant to delete"),
	},
}

// toolMetaTable maps tool name → routing metadata.
var toolMetaTable = map[string]mcpToolMeta{
	"register_llm_model":   {HTTPMethod: http.MethodPost, Path: "/ai/llm/models", BodyParam: "body"},
	"list_llm_models":      {HTTPMethod: http.MethodGet, Path: "/ai/llm/models"},
	"delete_llm_model":     {HTTPMethod: http.MethodDelete, Path: "/ai/llm/models/{alias}", PathParam: "alias"},
	"register_mcp_server":  {HTTPMethod: http.MethodPost, Path: "/ai/mcp/servers", BodyParam: "body"},
	"list_mcp_servers":     {HTTPMethod: http.MethodGet, Path: "/ai/mcp/servers"},
	"delete_mcp_server":    {HTTPMethod: http.MethodDelete, Path: "/ai/mcp/servers/{alias}", PathParam: "alias"},
	"create_virtual_server": {HTTPMethod: http.MethodPost, Path: "/ai/mcp/virtual", BodyParam: "body"},
	"list_virtual_servers": {HTTPMethod: http.MethodGet, Path: "/ai/mcp/virtual"},
	"delete_virtual_server": {HTTPMethod: http.MethodDelete, Path: "/ai/mcp/virtual/{name}", PathParam: "name"},
	"register_api_tool":    {HTTPMethod: http.MethodPost, Path: "/ai/tools/apis", BodyParam: "body"},
	"list_api_tools":       {HTTPMethod: http.MethodGet, Path: "/ai/tools/apis"},
	"delete_api_tool":      {HTTPMethod: http.MethodDelete, Path: "/ai/tools/apis/{name}", PathParam: "name"},
	"sync_flow":            {HTTPMethod: http.MethodPost, Path: "/sync", BodyParam: "body"},
	"list_gateway_apis":    {HTTPMethod: http.MethodGet, Path: "/getAllApis"},
	"create_tenant":        {HTTPMethod: http.MethodPost, Path: "/tenants", BodyParam: "body"},
	"list_tenants":         {HTTPMethod: http.MethodGet, Path: "/tenants", QuerySuffix: "cursor=0&limit=50"},
	"delete_tenant":        {HTTPMethod: http.MethodDelete, Path: "/tenants/{alias}", PathParam: "alias"},
}

// ── Schema helpers ────────────────────────────────────────────────────────────

func bodySchema(desc string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"body": map[string]any{
				"type":        "string",
				"description": desc,
			},
		},
		"required": []string{"body"},
	}
}

func paramSchema(name, desc string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			name: map[string]any{
				"type":        "string",
				"description": desc,
			},
		},
		"required": []string{name},
	}
}

func emptySchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

// ── JSON-RPC helpers ──────────────────────────────────────────────────────────

func mcpWriteResult(w http.ResponseWriter, id json.RawMessage, result any) {
	resp := mcpResponse{JSONRPC: "2.0", ID: id, Result: result}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func mcpWriteError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &mcpRPCError{Code: code, Message: msg},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func mcpWriteToolResult(w http.ResponseWriter, id json.RawMessage, text string, isError bool) {
	result := map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"isError": isError,
	}
	mcpWriteResult(w, id, result)
}

// ── MCPHandler ────────────────────────────────────────────────────────────────

// MCPHandler handles POST /mcp — a JSON-RPC 2.0 MCP endpoint that exposes
// gateway management operations as MCP tools.
func (s *Server) MCPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var req mcpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		mcpWriteError(w, nil, -32700, "parse error: "+err.Error())
		return
	}

	switch req.Method {
	case "initialize":
		s.mcpHandleInitialize(w, req)
	case "tools/list":
		s.mcpHandleToolsList(w, req)
	case "tools/call":
		s.mcpHandleToolsCall(w, r.Context(), req)
	default:
		mcpWriteError(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Server) mcpHandleInitialize(w http.ResponseWriter, req mcpRequest) {
	result := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "rah-studio",
			"version": "1.0",
		},
	}
	mcpWriteResult(w, req.ID, result)
}

func (s *Server) mcpHandleToolsList(w http.ResponseWriter, req mcpRequest) {
	result := map[string]any{
		"tools": studioMCPTools,
	}
	mcpWriteResult(w, req.ID, result)
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) mcpHandleToolsCall(w http.ResponseWriter, ctx context.Context, req mcpRequest) {
	var params mcpToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		mcpWriteError(w, req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	text, isError := s.proxyToolCall(ctx, params.Name, params.Arguments)
	mcpWriteToolResult(w, req.ID, text, isError)
}

// proxyToolCall resolves the tool name to a management server request, executes
// it, and returns the response body (or error message) as a string.
func (s *Server) proxyToolCall(ctx context.Context, toolName string, arguments json.RawMessage) (text string, isError bool) {
	meta, ok := toolMetaTable[toolName]
	if !ok {
		return fmt.Sprintf("unknown tool: %s", toolName), true
	}

	// Resolve management base URL.
	base := ""
	if s.managementBaseURL != nil {
		base = s.managementBaseURL.String()
	} else if len(s.targets) > 0 && len(s.targets[0].URLs) > 0 {
		base = s.targets[0].URLs[0]
	}
	if base == "" {
		return "no management endpoint configured", true
	}

	// Decode arguments map (may be empty for no-param tools).
	var args map[string]string
	if len(arguments) > 0 && string(arguments) != "null" && string(arguments) != "{}" {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return "invalid arguments: " + err.Error(), true
		}
	}

	// Build the path — substitute path param placeholder if needed.
	path := meta.Path
	if meta.PathParam != "" {
		paramVal, ok := args[meta.PathParam]
		if !ok || strings.TrimSpace(paramVal) == "" {
			return fmt.Sprintf("missing required argument: %s", meta.PathParam), true
		}
		path = strings.ReplaceAll(path, "{"+meta.PathParam+"}", paramVal)
	}

	// Build request body if needed.
	var bodyReader io.Reader
	if meta.BodyParam != "" {
		bodyStr, ok := args[meta.BodyParam]
		if !ok || strings.TrimSpace(bodyStr) == "" {
			return fmt.Sprintf("missing required argument: %s", meta.BodyParam), true
		}
		bodyReader = bytes.NewBufferString(bodyStr)
	}

	// Build target URL.
	targetURL, err := buildTargetURL(base, path, meta.QuerySuffix)
	if err != nil {
		return "invalid management endpoint: " + err.Error(), true
	}

	// Build and send the HTTP request.
	httpReq, err := http.NewRequestWithContext(ctx, meta.HTTPMethod, targetURL, bodyReader)
	if err != nil {
		return "failed to build request: " + err.Error(), true
	}
	if meta.BodyParam != "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return "management API unreachable: " + err.Error(), true
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "failed to read management response: " + err.Error(), true
	}

	responseText := strings.TrimSpace(string(respBody))
	if responseText == "" {
		responseText = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	// Treat 4xx/5xx as tool errors so the LLM knows something went wrong.
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("HTTP %d: %s", resp.StatusCode, responseText), true
	}

	return responseText, false
}
