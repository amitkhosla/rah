package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// MCPCallToolConfig is resolved at bake time and captured in the instruction closure.
type MCPCallToolConfig struct {
	ServerURL    string // MCP server HTTP URL
	APIKey       string // auth token (empty = no auth)
	TimeoutMs    int    // default 30000
	ToolNameSlot int    // ByteSlots index: name of the tool to call
	ArgsSlot     int    // ByteSlots index: JSON-encoded arguments object
	ResultSlot   int    // ByteSlots index: write JSON result here
}

// mcpCallResult is the wire type for the tools/call result content array.
type mcpCallResult struct {
	Content json.RawMessage `json:"content"`
}

// mcpCallRPCResponse is the JSON-RPC 2.0 response for a tools/call request.
type mcpCallRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  *mcpCallResult  `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

// MCPCallTool returns an engine.Instruction that calls a specific MCP tool on
// an MCP server and writes the result content array (as JSON) to cfg.ResultSlot.
//
// At runtime:
//  1. Reads tool name from ctx.ByteSlots[cfg.ToolNameSlot] — skips if empty.
//  2. Reads args from ctx.ByteSlots[cfg.ArgsSlot] — uses {} if empty.
//  3. Validates args is valid JSON object — returns 400 + StopPlan if invalid.
//  4. Posts JSON-RPC 2.0 tools/call to cfg.ServerURL with retry on 429/5xx.
//  5. On JSON-RPC error â†’ returns 502 + StopPlan.
//  6. On success â†’ writes result.content JSON array to cfg.ResultSlot.
func MCPCallTool(cfg MCPCallToolConfig) engine.Instruction {
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 30_000
	}

	return engine.Instruction{
		Name: "mcp_call_tool[" + cfg.ServerURL + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read tool name — skip if empty.
			if cfg.ToolNameSlot < 0 || cfg.ToolNameSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}
			toolName := ctx.ByteSlots[cfg.ToolNameSlot]
			if len(toolName) == 0 {
				return state.PC + 1
			}

			// 2. Read args — default to {}.
			var argsRaw json.RawMessage
			if cfg.ArgsSlot >= 0 && cfg.ArgsSlot < len(ctx.ByteSlots) && len(ctx.ByteSlots[cfg.ArgsSlot]) > 0 {
				argsRaw = json.RawMessage(ctx.ByteSlots[cfg.ArgsSlot])
			} else {
				argsRaw = json.RawMessage("{}")
			}

			// 3. Validate args is a valid JSON object.
			var argsCheck map[string]json.RawMessage
			if err := json.Unmarshal(argsRaw, &argsCheck); err != nil {
				ctx.ResponseStatus = 400
				ctx.Failed = true
				ctx.ErrorCode = 400
				msg := "mcp_call_tool: args is not a valid JSON object"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 4. Build JSON-RPC 2.0 request body.
			rpcReq := map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  "tools/call",
				"params": map[string]any{
					"name":      string(toolName),
					"arguments": argsRaw,
				},
			}
			body, err := json.Marshal(rpcReq)
			if err != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "mcp_call_tool: request marshal failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			client := getMCPClient(cfg.ServerURL, timeoutMs)
			maxAttempts := 3 // 1 initial + 2 retries

			var lastStatus int
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				start := time.Now()

				reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
				httpReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.ServerURL, bytes.NewReader(body))
				if reqErr != nil {
					cancel()
					ctx.ResponseStatus = 500
					ctx.Failed = true
					ctx.ErrorCode = 500
					msg := "mcp_call_tool: request build failed"
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
					if attempt < maxAttempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "mcp_call_tool: upstream error"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				respBody, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()
				lastStatus = resp.StatusCode

				if readErr != nil {
					if attempt < maxAttempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "mcp_call_tool: response read failed"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				// Retry on rate-limit or transient server errors.
				if resp.StatusCode == 429 || resp.StatusCode >= 500 {
					if attempt < maxAttempts {
						time.Sleep(llmBackoff(attempt))
						continue
					}
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "mcp_call_tool: server error"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				// Parse JSON-RPC response.
				var rpcResp mcpCallRPCResponse
				if err := json.Unmarshal(respBody, &rpcResp); err != nil {
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "mcp_call_tool: response parse failed"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				// JSON-RPC application-level error.
				if rpcResp.Error != nil {
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "mcp_call_tool: server error: " + rpcResp.Error.Message
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				if rpcResp.Result == nil {
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					msg := "mcp_call_tool: empty result"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}

				// Write result.content to ResultSlot.
				if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
					out := []byte(rpcResp.Result.Content)
					ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(out))
					copy(ctx.ByteSlots[cfg.ResultSlot], out)
				}

				return state.PC + 1
			}

			// All retries exhausted (shouldn't reach here normally).
			ctx.ResponseStatus = lastStatus
			if ctx.ResponseStatus == 0 {
				ctx.ResponseStatus = 502
			}
			ctx.Failed = true
			ctx.ErrorCode = int16(ctx.ResponseStatus)
			msg := "mcp_call_tool: all retries exhausted"
			ctx.ErrorMsg = ctx.Alloc(len(msg))
			copy(ctx.ErrorMsg, msg)
			return engine.StopPlan
		},
	}
}
