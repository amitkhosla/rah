package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// MCPLoadMode controls how much tool detail is written to the output slot.
type MCPLoadMode string

const (
	// MCPLoadNames writes a JSON array of tool name strings only.
	MCPLoadNames MCPLoadMode = "names"
	// MCPLoadBrief writes a JSON array of {name, description} objects.
	MCPLoadBrief MCPLoadMode = "brief"
	// MCPLoadFull writes a JSON array of full ToolDefinition objects.
	MCPLoadFull MCPLoadMode = "full"
)

// MCPListToolsConfig is resolved once at bake time and captured in the instruction closure.
type MCPListToolsConfig struct {
	ServerConfig config.MCPServerConfig
	APIKey       string      // resolved literal key
	Mode         MCPLoadMode // output verbosity; default MCPLoadFull
	ResultSlot   int         // ByteSlots index for output
	TimeoutMs    int         // per-request timeout; default 5000
}

// MCPFetchSchemasConfig is resolved once at bake time and captured in the instruction closure.
type MCPFetchSchemasConfig struct {
	ServerConfig config.MCPServerConfig
	APIKey       string
	SelectedSlot int // ByteSlots index with JSON array of selected tool name strings
	ResultSlot   int // ByteSlots index for output: JSON array of full ToolDefinition
	TimeoutMs    int
}

// â"€â"€ JSON-RPC wire types â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

type mcpRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpToolsResult struct {
	Tools []mcpRawTool `json:"tools"`
}

type mcpRawTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  *mcpToolsResult `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

// â"€â"€ HTTP client cache â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

var (
	mcpClientCache sync.Map // key: server URL string â†’ *http.Client
	mcpClientCount atomic.Int64
)

func getMCPClient(serverURL string, timeoutMs int) *http.Client {
	if c, ok := mcpClientCache.Load(serverURL); ok {
		return c.(*http.Client)
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
		},
		Timeout: timeout,
	}
	if mcpClientCount.Load() < 256 {
		actual, loaded := mcpClientCache.LoadOrStore(serverURL, client)
		if loaded {
			return actual.(*http.Client)
		}
		mcpClientCount.Add(1)
	}
	return client
}

// â"€â"€ shared fetch helper â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// mcpFetchTools issues a JSON-RPC tools/list call to the MCP server and returns
// the raw tool list. On error it populates ctx error fields and returns nil.
func mcpFetchTools(ctx *rctx.Context, serverURL string, apiKey string, timeoutMs int) []mcpRawTool {
	rpcReq := mcpRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
		Params:  map[string]any{},
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		ctx.ResponseStatus = 500
		ctx.Failed = true
		ctx.ErrorCode = 500
		msg := "mcp: request marshal failed"
		ctx.ErrorMsg = ctx.Alloc(len(msg))
		copy(ctx.ErrorMsg, msg)
		return nil
	}

	client := getMCPClient(serverURL, timeoutMs)
	reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, serverURL, bytes.NewReader(body))
	if err != nil {
		ctx.ResponseStatus = 500
		ctx.Failed = true
		ctx.ErrorCode = 500
		msg := "mcp: request build failed"
		ctx.ErrorMsg = ctx.Alloc(len(msg))
		copy(ctx.ErrorMsg, msg)
		return nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		ctx.ResponseStatus = 502
		ctx.Failed = true
		ctx.ErrorCode = 502
		msg := "mcp: upstream error"
		ctx.ErrorMsg = ctx.Alloc(len(msg))
		copy(ctx.ErrorMsg, msg)
		return nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		ctx.ResponseStatus = 502
		ctx.Failed = true
		ctx.ErrorCode = 502
		msg := "mcp: response read failed"
		ctx.ErrorMsg = ctx.Alloc(len(msg))
		copy(ctx.ErrorMsg, msg)
		return nil
	}

	var rpcResp mcpRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		ctx.ResponseStatus = 502
		ctx.Failed = true
		ctx.ErrorCode = 502
		msg := "mcp: response parse failed"
		ctx.ErrorMsg = ctx.Alloc(len(msg))
		copy(ctx.ErrorMsg, msg)
		return nil
	}

	if rpcResp.Error != nil {
		ctx.ResponseStatus = 502
		ctx.Failed = true
		ctx.ErrorCode = 502
		msg := "mcp: server error: " + rpcResp.Error.Message
		ctx.ErrorMsg = ctx.Alloc(len(msg))
		copy(ctx.ErrorMsg, msg)
		return nil
	}

	if rpcResp.Result == nil {
		ctx.ResponseStatus = 502
		ctx.Failed = true
		ctx.ErrorCode = 502
		msg := "mcp: empty result"
		ctx.ErrorMsg = ctx.Alloc(len(msg))
		copy(ctx.ErrorMsg, msg)
		return nil
	}

	return rpcResp.Result.Tools
}

// â"€â"€ MCPListTools â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// MCPListTools returns an engine.Instruction that fetches the tool list from an
// MCP server and writes the result (in the requested mode) to cfg.ResultSlot.
func MCPListTools(cfg MCPListToolsConfig) engine.Instruction {
	mode := cfg.Mode
	if mode == "" {
		mode = MCPLoadFull
	}
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}

	return engine.Instruction{
		Name: "mcp_list_tools[" + cfg.ServerConfig.Alias + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Only HTTP and SSE (treated as HTTP) transports are supported.
			t := cfg.ServerConfig.Transport
			if t != config.MCPTransportHTTP && t != config.MCPTransportSSE {
				ctx.ResponseStatus = 501
				ctx.Failed = true
				ctx.ErrorCode = 501
				msg := "mcp: unsupported transport: " + string(t)
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			tools := mcpFetchTools(ctx, cfg.ServerConfig.URL, cfg.APIKey, timeoutMs)
			if tools == nil {
				// ctx already populated by mcpFetchTools
				return engine.StopPlan
			}

			var out []byte
			var marshalErr error

			switch mode {
			case MCPLoadNames:
				names := make([]string, len(tools))
				for i, t := range tools {
					names[i] = t.Name
				}
				out, marshalErr = json.Marshal(names)

			case MCPLoadBrief:
				type briefTool struct {
					Name        string `json:"name"`
					Description string `json:"description,omitempty"`
				}
				brief := make([]briefTool, len(tools))
				for i, t := range tools {
					brief[i] = briefTool{Name: t.Name, Description: t.Description}
				}
				out, marshalErr = json.Marshal(brief)

			default: // MCPLoadFull
				full := make([]ToolDefinition, len(tools))
				for i, t := range tools {
					var schemaRaw json.RawMessage
					if t.InputSchema != nil {
						schemaRaw, _ = json.Marshal(t.InputSchema)
					}
					full[i] = ToolDefinition{
						Name:        t.Name,
						Description: t.Description,
						InputSchema: schemaRaw,
					}
				}
				out, marshalErr = json.Marshal(full)
			}

			if marshalErr != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "mcp: output marshal failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(out))
				copy(ctx.ByteSlots[cfg.ResultSlot], out)
			}

			return state.PC + 1
		},
	}
}

// â"€â"€ MCPFetchSchemas â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// MCPFetchSchemas returns an engine.Instruction that fetches full schemas for a
// selected subset of tools from an MCP server, filtering by the tool names
// stored as a JSON array in cfg.SelectedSlot.
func MCPFetchSchemas(cfg MCPFetchSchemasConfig) engine.Instruction {
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}

	return engine.Instruction{
		Name: "mcp_fetch_schemas[" + cfg.ServerConfig.Alias + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Read selected tool names from slot.
			if cfg.SelectedSlot < 0 || cfg.SelectedSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}
			raw := ctx.ByteSlots[cfg.SelectedSlot]
			if len(raw) == 0 {
				return state.PC + 1
			}

			var selectedNames []string
			if err := json.Unmarshal(raw, &selectedNames); err != nil || len(selectedNames) == 0 {
				return state.PC + 1
			}

			// Build a fast lookup set.
			want := make(map[string]struct{}, len(selectedNames))
			for _, n := range selectedNames {
				want[n] = struct{}{}
			}

			// Only HTTP and SSE transports are supported.
			t := cfg.ServerConfig.Transport
			if t != config.MCPTransportHTTP && t != config.MCPTransportSSE {
				ctx.ResponseStatus = 501
				ctx.Failed = true
				ctx.ErrorCode = 501
				msg := "mcp: unsupported transport: " + string(t)
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			allTools := mcpFetchTools(ctx, cfg.ServerConfig.URL, cfg.APIKey, timeoutMs)
			if allTools == nil {
				return engine.StopPlan
			}

			// Filter to selected tools only.
			filtered := make([]ToolDefinition, 0, len(selectedNames))
			for _, t := range allTools {
				if _, ok := want[t.Name]; ok {
					var schemaRaw json.RawMessage
					if t.InputSchema != nil {
						schemaRaw, _ = json.Marshal(t.InputSchema)
					}
					filtered = append(filtered, ToolDefinition{
						Name:        t.Name,
						Description: t.Description,
						InputSchema: schemaRaw,
					})
				}
			}

			out, marshalErr := json.Marshal(filtered)
			if marshalErr != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "mcp: output marshal failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(out))
				copy(ctx.ByteSlots[cfg.ResultSlot], out)
			}

			return state.PC + 1
		},
	}
}
