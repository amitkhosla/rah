package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/mcpreg"
	"rah/internal/rctx"
)

// ── Type aliases from mcpreg ──────────────────────────────────────────────────
// These are direct references to mcpreg types; kept as aliases here so the
// compiler file and any callers can reference steps.VirtualMCPLookup etc.

// VirtualMCPLookup is the function the instruction calls to find a virtual MCP
// server definition. Injected at bake time from the compiler config.
type VirtualMCPLookup func(tenantID uint16, name string) (mcpreg.VirtualMCPServerDef, bool)

// MCPServerLookup is the function the instruction calls to resolve an external
// MCP server config (URL, auth) by alias. Injected at bake time.
type MCPServerLookup func(alias string) (config.MCPServerConfig, string, bool) // (cfg, resolvedAPIKey, found)

// ── Copied JSON-RPC helpers from internal/mcp/server.go ──────────────────────
// We copy rather than import to avoid a dependency on the internal/mcp package.

const mcpServProtocolVersion = "2024-11-05"

// mcpServRequest is a JSON-RPC 2.0 request envelope.
type mcpServRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// mcpServResponse is a JSON-RPC 2.0 response envelope.
type mcpServResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpServRPCErr  `json:"error,omitempty"`
}

// mcpServRPCErr is the JSON-RPC 2.0 error object.
type mcpServRPCErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpServToolsResult is the result shape of tools/list from an external MCP server.
type mcpServToolsResult struct {
	Tools []mcpServRawTool `json:"tools"`
}

// mcpServRawTool matches the wire shape of an external MCP tools/list entry.
type mcpServRawTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// mcpServToolCallParams is the expected shape of tools/call params.
type mcpServToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// mcpServExternalResp wraps a response from an external MCP server for tools/call.
type mcpServExternalResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpServRPCErr  `json:"error,omitempty"`
}

// mcpServToolCallResult is the MCP tools/call result shape used when extracting content.
type mcpServToolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content,omitempty"`
	IsError bool `json:"isError,omitempty"`
}

// mcpServWriteResult encodes a successful JSON-RPC 2.0 response to w.
func mcpServWriteResult(w http.ResponseWriter, id json.RawMessage, result any) {
	resp := mcpServResponse{JSONRPC: "2.0", ID: id, Result: result}
	_ = json.NewEncoder(w).Encode(resp)
}

// mcpServWriteError encodes a JSON-RPC 2.0 error response to w.
func mcpServWriteError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := mcpServResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &mcpServRPCErr{Code: code, Message: msg},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// mcpServWriteToolResult encodes a tools/call result envelope.
func mcpServWriteToolResult(w http.ResponseWriter, id json.RawMessage, text string, isError bool) {
	result := map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"isError": isError,
	}
	mcpServWriteResult(w, id, result)
}

// ── HTTP client pool for serve_mcp outbound calls ─────────────────────────────

var (
	mcpServeClientCache sync.Map // key: base URL → *http.Client
)

func getMCPServeClient(baseURL string, timeoutMs int) *http.Client {
	if c, ok := mcpServeClientCache.Load(baseURL); ok {
		return c.(*http.Client)
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	actual, _ := mcpServeClientCache.LoadOrStore(baseURL, client)
	return actual.(*http.Client)
}

// ── tools/list cache for mcp_all sources ─────────────────────────────────────

type mcpServCachedTools struct {
	tools     []mcpServRawTool
	expiresAt time.Time
}

var mcpServeToolsCache sync.Map // key: serverAlias → *mcpServCachedTools

// fetchExternalTools calls tools/list on an external MCP server, with 60s caching.
func fetchExternalTools(alias, serverURL, apiKey string, timeoutMs int) ([]mcpServRawTool, error) {
	now := time.Now()
	if v, ok := mcpServeToolsCache.Load(alias); ok {
		c := v.(*mcpServCachedTools)
		if now.Before(c.expiresAt) {
			return c.tools, nil
		}
	}

	rpcReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{},
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, err
	}

	client := getMCPServeClient(serverURL, timeoutMs)
	reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, serverURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}

	var rpcResp struct {
		Result *mcpServToolsResult `json:"result,omitempty"`
		Error  *mcpServRPCErr      `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, err
	}
	if rpcResp.Error != nil {
		return nil, nil // server returned error, treat as empty
	}
	var tools []mcpServRawTool
	if rpcResp.Result != nil {
		tools = rpcResp.Result.Tools
	}

	// Cache for 60 seconds.
	mcpServeToolsCache.Store(alias, &mcpServCachedTools{
		tools:     tools,
		expiresAt: now.Add(60 * time.Second),
	})
	return tools, nil
}

// ── ServeMCPConfig ────────────────────────────────────────────────────────────

// ServeMCPConfig configures a serve_mcp instruction.
type ServeMCPConfig struct {
	// NameSlot is the ByteSlots index holding the virtual MCP server name.
	// Set to -1 if NameLiteral is used instead.
	NameSlot    int
	NameLiteral string

	// BodySlot is the ByteSlots index holding the raw JSON-RPC request body.
	BodySlot int

	// LookupServer finds a VirtualMCPServerDef by tenant + name.
	LookupServer VirtualMCPLookup

	// LookupMCPServer finds an external MCP server config by alias.
	LookupMCPServer MCPServerLookup

	// GatewayBase is the base URL for api_tool calls (e.g. "http://localhost:8080").
	GatewayBase string

	// TimeoutMs for outbound calls (default 30000).
	TimeoutMs int
}

// ── ServeMCP instruction ──────────────────────────────────────────────────────

// ServeMCP returns an engine.Instruction that implements the full MCP JSON-RPC
// 2.0 server protocol for a named virtual MCP server.
//
// It always returns engine.StopPlan — the response is written directly to the
// response writer and no further instructions should run.
func ServeMCP(cfg ServeMCPConfig) engine.Instruction {
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 30_000
	}

	return engine.Instruction{
		Name: "serve_mcp",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			w := ctx.GetWriter()
			if w == nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}
			w.Header().Set("Content-Type", "application/json")

			// 1. Resolve virtual MCP server name.
			var name string
			if cfg.NameSlot >= 0 && cfg.NameSlot < len(ctx.ByteSlots) {
				name = string(ctx.ByteSlots[cfg.NameSlot])
			} else {
				name = cfg.NameLiteral
			}
			if name == "" {
				ctx.ResponseStatus = 400
				mcpServWriteError(w, nil, -32602, "serve_mcp: server name is empty")
				return engine.StopPlan
			}

			// 2. Read JSON-RPC body.
			var rawBody []byte
			if cfg.BodySlot >= 0 && cfg.BodySlot < len(ctx.ByteSlots) {
				rawBody = ctx.ByteSlots[cfg.BodySlot]
			}
			if len(rawBody) == 0 && ctx.Request != nil {
				// Fall back to raw request body if slot is empty.
				if b, err := io.ReadAll(io.LimitReader(ctx.Request.Body, 4*1024*1024)); err == nil {
					rawBody = b
				}
			}
			if len(rawBody) == 0 {
				ctx.ResponseStatus = 200
				mcpServWriteError(w, nil, -32700, "parse error: empty request body")
				return engine.StopPlan
			}

			// 3. Parse JSON-RPC request envelope.
			var req mcpServRequest
			if err := json.Unmarshal(rawBody, &req); err != nil {
				ctx.ResponseStatus = 200
				mcpServWriteError(w, nil, -32700, "parse error: "+err.Error())
				return engine.StopPlan
			}

			// 4. Look up VirtualMCPServerDef.
			var def mcpreg.VirtualMCPServerDef
			var found bool
			if cfg.LookupServer != nil {
				def, found = cfg.LookupServer(ctx.TenantID, name)
			}
			if !found {
				ctx.ResponseStatus = 200
				mcpServWriteError(w, req.ID, -32602, "virtual MCP server not found: "+name)
				return engine.StopPlan
			}

			// 5. Dispatch on method.
			switch req.Method {
			case "initialize":
				mcpServHandleInitialize(w, req, def)
			case "notifications/initialized":
				mcpServWriteResult(w, req.ID, map[string]any{})
			case "tools/list":
				mcpServHandleToolsList(w, req, def, cfg, timeoutMs)
			case "tools/call":
				mcpServHandleToolsCall(w, req, def, cfg, timeoutMs, ctx)
			default:
				mcpServWriteError(w, req.ID, -32601, "method not found: "+req.Method)
			}

			// 6. Set response status and stop.
			ctx.ResponseStatus = 200
			return engine.StopPlan
		},
	}
}

// ── Handler functions ─────────────────────────────────────────────────────────

func mcpServHandleInitialize(w http.ResponseWriter, req mcpServRequest, def mcpreg.VirtualMCPServerDef) {
	serverName := def.Name
	if serverName == "" {
		serverName = "rah-virtual-mcp"
	}
	result := map[string]any{
		"protocolVersion": mcpServProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": "1.0",
		},
	}
	mcpServWriteResult(w, req.ID, result)
}

func mcpServHandleToolsList(
	w http.ResponseWriter,
	req mcpServRequest,
	def mcpreg.VirtualMCPServerDef,
	cfg ServeMCPConfig,
	timeoutMs int,
) {
	type toolEntry struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	}

	defaultSchema := json.RawMessage(`{"type":"object"}`)
	tools := make([]toolEntry, 0, len(def.Sources))

	for _, src := range def.Sources {
		switch src.Kind {
		case mcpreg.ToolSourceAPI:
			if src.APITool == nil {
				continue
			}
			schema := src.APITool.InputSchema
			if len(schema) == 0 {
				schema = defaultSchema
			}
			tools = append(tools, toolEntry{
				Name:        src.APITool.Name,
				Description: src.APITool.Description,
				InputSchema: schema,
			})

		case mcpreg.ToolSourceMCPTool:
			tools = append(tools, toolEntry{
				Name:        src.ToolName,
				Description: "(from " + src.ServerAlias + ")",
				InputSchema: defaultSchema,
			})

		case mcpreg.ToolSourceMCPAll:
			if cfg.LookupMCPServer == nil {
				continue
			}
			serverCfg, apiKey, ok := cfg.LookupMCPServer(src.ServerAlias)
			if !ok {
				continue
			}
			extTools, err := fetchExternalTools(src.ServerAlias, serverCfg.URL, apiKey, timeoutMs)
			if err != nil || len(extTools) == 0 {
				continue
			}
			for _, t := range extTools {
				schema := t.InputSchema
				if len(schema) == 0 {
					schema = defaultSchema
				}
				tools = append(tools, toolEntry{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: schema,
				})
			}
		}
	}

	mcpServWriteResult(w, req.ID, map[string]any{"tools": tools})
}

func mcpServHandleToolsCall(
	w http.ResponseWriter,
	req mcpServRequest,
	def mcpreg.VirtualMCPServerDef,
	cfg ServeMCPConfig,
	timeoutMs int,
	ctx *rctx.Context,
) {
	var params mcpServToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		mcpServWriteError(w, req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	// Find the source that owns this tool name.
	for _, src := range def.Sources {
		switch src.Kind {
		case mcpreg.ToolSourceAPI:
			if src.APITool == nil || src.APITool.Name != params.Name {
				continue
			}
			mcpServCallAPITool(w, req, params, src.APITool, cfg, timeoutMs, ctx)
			return

		case mcpreg.ToolSourceMCPTool:
			if src.ToolName != params.Name {
				continue
			}
			mcpServForwardToMCP(w, req, params, src.ServerAlias, src.ToolName, cfg, timeoutMs)
			return

		case mcpreg.ToolSourceMCPAll:
			// Forward to the external MCP server; it will return an error if the
			// tool doesn't exist there, which is the natural behaviour.
			mcpServForwardToMCP(w, req, params, src.ServerAlias, params.Name, cfg, timeoutMs)
			return
		}
	}

	// No source matched.
	mcpServWriteError(w, req.ID, -32602, "unknown tool: "+params.Name)
}

// mcpServCallAPITool calls a RAH API endpoint as an MCP tool.
func mcpServCallAPITool(
	w http.ResponseWriter,
	req mcpServRequest,
	params mcpServToolCallParams,
	tool *mcpreg.APIToolDef,
	cfg ServeMCPConfig,
	timeoutMs int,
	ctx *rctx.Context,
) {
	url := cfg.GatewayBase + tool.Path
	method := tool.Method
	if method == "" {
		method = http.MethodPost
	}

	body := params.Arguments
	if len(body) == 0 {
		body = []byte("{}")
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, method, url, bytes.NewReader(body))
	if err != nil {
		mcpServWriteToolResult(w, req.ID, "failed to build request: "+err.Error(), true)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Apply tool-level auth.
	switch tool.AuthKind {
	case "bearer":
		if tool.AuthKeyRef != "" {
			httpReq.Header.Set("Authorization", "Bearer "+tool.AuthKeyRef)
		}
	case "header":
		if tool.AuthHeader != "" && tool.AuthKeyRef != "" {
			httpReq.Header.Set(tool.AuthHeader, tool.AuthKeyRef)
		}
	}

	// Copy original request headers so tenant auth propagates.
	if ctx != nil && ctx.Request != nil {
		for key, vals := range ctx.Request.Header {
			// Don't overwrite headers already set above.
			if httpReq.Header.Get(key) == "" {
				for _, v := range vals {
					httpReq.Header.Add(key, v)
				}
			}
		}
	}

	client := getMCPServeClient(cfg.GatewayBase, timeoutMs)
	resp, err := client.Do(httpReq)
	if err != nil {
		mcpServWriteToolResult(w, req.ID, "gateway request failed: "+err.Error(), true)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		mcpServWriteToolResult(w, req.ID, "failed to read gateway response: "+err.Error(), true)
		return
	}

	isError := resp.StatusCode >= 400
	mcpServWriteToolResult(w, req.ID, string(respBody), isError)
}

// mcpServForwardToMCP forwards a tools/call to an external MCP server.
func mcpServForwardToMCP(
	w http.ResponseWriter,
	req mcpServRequest,
	params mcpServToolCallParams,
	serverAlias string,
	toolName string,
	cfg ServeMCPConfig,
	timeoutMs int,
) {
	if cfg.LookupMCPServer == nil {
		mcpServWriteToolResult(w, req.ID, "MCP server lookup not configured", true)
		return
	}
	serverCfg, apiKey, ok := cfg.LookupMCPServer(serverAlias)
	if !ok {
		mcpServWriteToolResult(w, req.ID, "MCP server not found: "+serverAlias, true)
		return
	}

	args := params.Arguments
	if len(args) == 0 {
		args = []byte("{}")
	}

	rpcBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": json.RawMessage(args),
		},
	}
	bodyBytes, err := json.Marshal(rpcBody)
	if err != nil {
		mcpServWriteToolResult(w, req.ID, "failed to marshal request: "+err.Error(), true)
		return
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, serverCfg.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		mcpServWriteToolResult(w, req.ID, "failed to build request: "+err.Error(), true)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := getMCPServeClient(serverCfg.URL, timeoutMs)
	resp, err := client.Do(httpReq)
	if err != nil {
		mcpServWriteToolResult(w, req.ID, "MCP server request failed: "+err.Error(), true)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		mcpServWriteToolResult(w, req.ID, "failed to read MCP response: "+err.Error(), true)
		return
	}

	// Try to parse as a proper MCP tools/call response and extract text content.
	var extResp mcpServExternalResp
	if err := json.Unmarshal(respBody, &extResp); err == nil {
		if extResp.Error != nil {
			mcpServWriteToolResult(w, req.ID, extResp.Error.Message, true)
			return
		}
		if len(extResp.Result) > 0 {
			var callResult mcpServToolCallResult
			if err := json.Unmarshal(extResp.Result, &callResult); err == nil {
				// Concatenate all text content entries.
				var combined string
				for _, c := range callResult.Content {
					if c.Type == "text" {
						if combined != "" {
							combined += "\n"
						}
						combined += c.Text
					}
				}
				mcpServWriteToolResult(w, req.ID, combined, callResult.IsError)
				return
			}
			// Result didn't match expected shape; pass through raw.
			mcpServWriteToolResult(w, req.ID, string(extResp.Result), false)
			return
		}
	}

	// Fallback: pass through raw body.
	isError := resp.StatusCode >= 400
	mcpServWriteToolResult(w, req.ID, string(respBody), isError)
}
