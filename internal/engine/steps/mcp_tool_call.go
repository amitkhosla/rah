package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// MCPCallConfig configures a call_mcp_tool instruction (static tool name variant).
type MCPCallConfig struct {
	// Server is the resolved MCP server config (injected at bake time).
	Server     config.MCPServerConfig
	APIKey     string  // resolved from APIKeyRef at bake time
	ToolName   string  // MCP tool name to invoke (baked at compile time)
	InputSlot  int     // ByteSlots index for JSON arguments (map[string]any); -1 if not used
	ResultSlot int     // ByteSlots index to write tool result JSON
	TimeoutMs  int     // default 10000
}

// mcpToolCallResponse is the JSON-RPC 2.0 response for tools/call.
type mcpToolCallResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result,omitempty"`
	Error *mcpRPCError `json:"error,omitempty"`
}

// MCPToolCall returns an engine.Instruction that calls a specific MCP tool with
// a static tool name (baked at compile time) and writes the concatenated text
// response to cfg.ResultSlot.
//
// At runtime:
//  1. Reads arguments from ctx.ByteSlots[cfg.InputSlot] (default {} if empty or -1)
//  2. Validates arguments as a valid JSON object
//  3. Posts JSON-RPC 2.0 tools/call to cfg.Server.URL
//  4. On JSON-RPC error or isError:true → sets 502 + StopPlan
//  5. On success → concatenates all text-type content blocks and writes to ResultSlot
func MCPToolCall(cfg MCPCallConfig) engine.Instruction {
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 10_000
	}

	return engine.Instruction{
		Name: "call_mcp_tool[" + cfg.ToolName + "@" + cfg.Server.Alias + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read input arguments
			var argsRaw json.RawMessage
			if cfg.InputSlot >= 0 && cfg.InputSlot < len(ctx.ByteSlots) && len(ctx.ByteSlots[cfg.InputSlot]) > 0 {
				argsRaw = json.RawMessage(ctx.ByteSlots[cfg.InputSlot])
			} else {
				argsRaw = json.RawMessage("{}")
			}

			// 2. Validate arguments
			var argsCheck map[string]json.RawMessage
			if err := json.Unmarshal(argsRaw, &argsCheck); err != nil {
				ctx.ResponseStatus = 400
				ctx.Failed = true
				ctx.ErrorCode = 400
				msg := "call_mcp_tool: input slot is not a valid JSON object"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 3. Build JSON-RPC 2.0 request
			rpcReq := map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  "tools/call",
				"params": map[string]any{
					"name":      cfg.ToolName,
					"arguments": argsRaw,
				},
			}
			body, err := json.Marshal(rpcReq)
			if err != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "call_mcp_tool: request marshal failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 4. Get HTTP client and make request
			client := getMCPClient(cfg.Server.URL, timeoutMs)

			start := time.Now()
			reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
			httpReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.Server.URL, bytes.NewReader(body))
			if reqErr != nil {
				cancel()
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "call_mcp_tool: request build failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			httpReq.Header.Set("Content-Type", "application/json")
			if cfg.APIKey != "" {
				httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			}

			resp, doErr := client.Do(httpReq)
			cancel()
			elapsed := time.Since(start)

			atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, elapsed.Nanoseconds())
			atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)

			if doErr != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "call_mcp_tool: upstream error"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			respBody, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()

			if readErr != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "call_mcp_tool: response read failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// HTTP error status codes
			if resp.StatusCode != http.StatusOK {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "call_mcp_tool: server error"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 5. Parse JSON-RPC response
			var rpcResp mcpToolCallResponse
			if err := json.Unmarshal(respBody, &rpcResp); err != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "call_mcp_tool: response parse failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// JSON-RPC application-level error
			if rpcResp.Error != nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "call_mcp_tool: server error: " + rpcResp.Error.Message
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			if rpcResp.Result == nil {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "call_mcp_tool: empty result"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// Check isError flag
			if rpcResp.Result.IsError {
				ctx.ResponseStatus = 502
				ctx.Failed = true
				ctx.ErrorCode = 502
				msg := "call_mcp_tool: tool execution error"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 6. Concatenate all text content blocks
			var textParts []string
			for _, content := range rpcResp.Result.Content {
				if content.Type == "text" {
					textParts = append(textParts, content.Text)
				}
				// Skip non-text content types (image, resource, etc.)
			}

			concatenated := strings.Join(textParts, "")
			out := []byte(concatenated)

			// 7. Write result to slot
			if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(out))
				copy(ctx.ByteSlots[cfg.ResultSlot], out)
			}

			return state.PC + 1
		},
	}
}
