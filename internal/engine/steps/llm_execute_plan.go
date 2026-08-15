package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// MCPServerConfig is a type alias for config.MCPServerConfig for use in this package.
type MCPServerConfig = config.MCPServerConfig

// PlanStep mirrors one item in the LLM's plan JSON array.
type PlanStep struct {
	Step               int                    `json:"step"`
	ToolName           string                 `json:"tool_name"`
	Params             map[string]interface{} `json:"params"`
	DependsOn          interface{}            `json:"dependsOn"` // "None", null, or int/float64
	ExpectListResponse bool                   `json:"expectListResponse"`
	StopOnError        bool                   `json:"stop_on_error"`
	NewToolRequired    bool                   `json:"newToolRequired"`
	nextSteps          []int                  // populated by buildGraph, not from JSON
}

// ExecutionPlan is the top-level LLM response.
type ExecutionPlan struct {
	Summary string     `json:"summary"`
	Plan    []PlanStep `json:"plan"`
}

// ExecutePlanConfig is resolved at bake time.
type ExecutePlanConfig struct {
	// PlanSlot: ByteSlot index holding the JSON plan from the LLM
	PlanSlot int
	// ResultSlot: ByteSlot index to write execution results as JSON map {"1": <result>, "2": <result>}
	ResultSlot int
	// ErrorSlot: ByteSlot index to write error message if execution fails (-1 = skip)
	ErrorSlot int
	// MCPConfig: MCP server to dispatch tool calls to
	MCPConfig MCPServerConfig
	// MCPAPIKey: resolved at bake time
	MCPAPIKey string
	// MaxConcurrent: 0 = sequential (same as Python code), >0 = parallel where deps allow
	MaxConcurrent int
	// TimeoutMs: per-tool-call timeout
	TimeoutMs int
	// SkipNewToolRequired: if true, silently skip steps with newToolRequired=true
	// if false (default), stop plan with error
	SkipNewToolRequired bool
	// MessagesSlot: if >= 0, append the plan result as an assistant message to the
	// []CanonicalMessage JSON stored in this slot. Enables multi-turn agentic flows.
	// Set to -1 (default) to skip.
	MessagesSlot int
}

// executePlanRPCRequest is the JSON-RPC 2.0 request body for a tools/call.
type executePlanRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// executePlanRPCResponse is the JSON-RPC 2.0 response for a tools/call.
type executePlanRPCResponse struct {
	JSONRPC string                     `json:"jsonrpc"`
	ID      int                        `json:"id"`
	Result  *executePlanCallResult     `json:"result,omitempty"`
	Error   *mcpRPCError               `json:"error,omitempty"`
}

// executePlanCallResult holds the content array from a tools/call response.
type executePlanCallResult struct {
	Content json.RawMessage `json:"content"`
}

// executePlanContentItem is one entry in the MCP content array.
type executePlanContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ExecutePlan returns an engine.Instruction that reads a JSON execution plan
// from ctx.ByteSlots[cfg.PlanSlot], builds a DAG, executes all steps in
// dependency order (dispatching each tool via MCP), and writes the results
// map as JSON to ctx.ByteSlots[cfg.ResultSlot].
func ExecutePlan(cfg ExecutePlanConfig) engine.Instruction {
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 30_000
	}

	return engine.Instruction{
		Name: "execute_plan[" + cfg.MCPConfig.URL + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read plan JSON from slot — if empty, no-op.
			if cfg.PlanSlot < 0 || cfg.PlanSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}
			raw := ctx.ByteSlots[cfg.PlanSlot]
			if len(raw) == 0 {
				return state.PC + 1
			}

			// 2. Unmarshal into ExecutionPlan.
			var plan ExecutionPlan
			if err := json.Unmarshal(raw, &plan); err != nil {
				if cfg.ErrorSlot >= 0 && cfg.ErrorSlot < len(ctx.ByteSlots) {
					msg := "execute_plan: invalid plan JSON: " + err.Error()
					ctx.ByteSlots[cfg.ErrorSlot] = ctx.Alloc(len(msg))
					copy(ctx.ByteSlots[cfg.ErrorSlot], msg)
				}
				ctx.ResponseStatus = 400
				ctx.Failed = true
				ctx.ErrorCode = 400
				return engine.StopPlan
			}
			if len(plan.Plan) == 0 {
				// Empty plan — write empty object and continue.
				empty := []byte("{}")
				if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
					ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(empty))
					copy(ctx.ByteSlots[cfg.ResultSlot], empty)
				}
				return state.PC + 1
			}

			// 3. Build DAG.
			nodes, readyQueue, waitingFor := buildGraph(plan.Plan)

			// 4. Execute graph.
			results, err := executeGraph(ctx, cfg, timeoutMs, nodes, readyQueue, waitingFor)
			if err != nil {
				if cfg.ErrorSlot >= 0 && cfg.ErrorSlot < len(ctx.ByteSlots) {
					msg := err.Error()
					ctx.ByteSlots[cfg.ErrorSlot] = ctx.Alloc(len(msg))
					copy(ctx.ByteSlots[cfg.ErrorSlot], msg)
				}
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				return engine.StopPlan
			}

			// 5. Marshal results to JSON and write to ResultSlot.
			// Convert int keys to string keys for JSON.
			strResults := make(map[string]interface{}, len(results))
			for k, v := range results {
				strResults[strconv.Itoa(k)] = v
			}
			out, marshalErr := json.Marshal(strResults)
			if marshalErr != nil {
				out = []byte("{}")
			}
			if cfg.ResultSlot >= 0 && cfg.ResultSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[cfg.ResultSlot] = ctx.Alloc(len(out))
				copy(ctx.ByteSlots[cfg.ResultSlot], out)
			}

			// Append result as assistant message to conversation history if configured.
			if cfg.MessagesSlot >= 0 && cfg.MessagesSlot < len(ctx.ByteSlots) && cfg.ResultSlot >= 0 {
				result := ctx.ByteSlots[cfg.ResultSlot]
				var msgs []CanonicalMessage
				if existing := ctx.ByteSlots[cfg.MessagesSlot]; len(existing) > 0 {
					_ = json.Unmarshal(existing, &msgs)
				}
				msgs = append(msgs, CanonicalMessage{Role: RoleAssistant, Content: string(result)})
				if b, err := json.Marshal(msgs); err == nil {
					ctx.ByteSlots[cfg.MessagesSlot] = b
				}
			}

			return state.PC + 1
		},
	}
}

// buildGraph constructs the DAG from a slice of PlanSteps.
// Returns:
//   - nodes: map of step ID to *PlanStep (with nextSteps populated)
//   - readyQueue: initial set of step IDs with no dependencies
//   - waitingFor: map of step ID to number of unresolved dependencies
func buildGraph(steps []PlanStep) (nodes map[int]*PlanStep, readyQueue []int, waitingFor map[int]int) {
	nodes = make(map[int]*PlanStep, len(steps))
	waitingFor = make(map[int]int, len(steps))

	// Map step ID â†’ *PlanStep.
	for i := range steps {
		s := &steps[i]
		nodes[s.Step] = s
		s.nextSteps = nil
		waitingFor[s.Step] = 0
	}

	for stepID, task := range nodes {
		dep := parseDependsOn(task.DependsOn)
		if dep < 0 {
			readyQueue = append(readyQueue, stepID)
		} else if parent, ok := nodes[dep]; ok {
			parent.nextSteps = append(parent.nextSteps, stepID)
			waitingFor[stepID]++
		} else {
			// Invalid dep â†’ treat as ready.
			readyQueue = append(readyQueue, stepID)
		}
	}
	return
}

// parseDependsOn normalises the DependsOn field (which may be nil, "None", a
// numeric string, a float64, or an int) to an integer dependency ID.
// Returns -1 if there is no dependency.
func parseDependsOn(v interface{}) int {
	if v == nil {
		return -1
	}
	switch val := v.(type) {
	case string:
		s := strings.TrimSpace(val)
		if s == "" || strings.EqualFold(s, "none") {
			return -1
		}
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
		return -1
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	default:
		return -1
	}
}

// executeGraph runs the DAG sequentially (FIFO queue).
// Returns a map of stepID â†’ result (interface{}) and any fatal error.
func executeGraph(
	ctx *rctx.Context,
	cfg ExecutePlanConfig,
	timeoutMs int,
	nodes map[int]*PlanStep,
	queue []int,
	waitingFor map[int]int,
) (map[int]interface{}, error) {
	results := make(map[int]interface{}, len(nodes))

	for len(queue) > 0 {
		// Pop front (FIFO).
		currentID := queue[0]
		queue = queue[1:]

		task, ok := nodes[currentID]
		if !ok {
			continue
		}

		// Handle steps requiring a new (unavailable) tool.
		if task.NewToolRequired {
			if !cfg.SkipNewToolRequired {
				return nil, fmt.Errorf("step %d requires new tool %q which is not available", task.Step, task.ToolName)
			}
			// Skip — record nil result and unlock children.
			results[currentID] = nil
			for _, childID := range task.nextSteps {
				waitingFor[childID]--
				if waitingFor[childID] == 0 {
					queue = append(queue, childID)
				}
			}
			continue
		}

		// Determine if this is a fan-out step.
		parentID := parseDependsOn(task.DependsOn)
		isFanOut := false
		if parentID >= 0 {
			if parentNode, pok := nodes[parentID]; pok && parentNode.ExpectListResponse {
				isFanOut = true
			}
		}

		if isFanOut {
			parentResult := results[parentID]
			list, ok := parentResult.([]interface{})
			if !ok {
				if parentResult != nil {
					list = []interface{}{parentResult}
				} else {
					list = []interface{}{}
				}
			}

			stepResults := make([]interface{}, 0, len(list))
			for _, item := range list {
				injectedParams := injectValue(task.Params, item)
				result, err := dispatchTool(ctx, cfg, timeoutMs, task.ToolName, injectedParams)
				if err != nil && task.StopOnError {
					return nil, fmt.Errorf("step %d tool %q failed: %w", task.Step, task.ToolName, err)
				}
				stepResults = append(stepResults, result)
			}
			results[currentID] = stepResults
		} else {
			// Single execution.
			var parentData interface{}
			if parentID >= 0 {
				parentData = results[parentID]
			}

			finalParams := injectValue(task.Params, parentData)
			result, err := dispatchTool(ctx, cfg, timeoutMs, task.ToolName, finalParams)
			if err != nil && task.StopOnError {
				return nil, fmt.Errorf("step %d tool %q failed: %w", task.Step, task.ToolName, err)
			}
			results[currentID] = result
		}

		// Unlock children.
		for _, childID := range task.nextSteps {
			waitingFor[childID]--
			if waitingFor[childID] == 0 {
				queue = append(queue, childID)
			}
		}
	}

	return results, nil
}

// injectValue replaces placeholder strings in params with the given value.
// It marshals the params to JSON, performs string replacement, then unmarshals
// back — the same approach used in the Python orchestrator.
func injectValue(params map[string]interface{}, value interface{}) map[string]interface{} {
	if value == nil {
		return params
	}
	if len(params) == 0 {
		return params
	}

	raw, err := json.Marshal(params)
	if err != nil {
		return params
	}
	s := string(raw)

	// Convert value to its string representation.
	var valStr string
	switch v := value.(type) {
	case string:
		valStr = v
	case float64:
		valStr = strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		valStr = strconv.Itoa(v)
	case int64:
		valStr = strconv.FormatInt(v, 10)
	default:
		if b, merr := json.Marshal(v); merr == nil {
			valStr = string(b)
		}
	}

	// Replace each placeholder in both quoted and unquoted forms.
	for _, placeholder := range []string{"{item}", "{result}", "{stock}", "{data}"} {
		// Replace "\"<placeholder>\"" (JSON-quoted string value) with "\"<valStr>\"".
		s = strings.ReplaceAll(s, `"`+placeholder+`"`, `"`+valStr+`"`)
		// Replace bare placeholder (e.g. inside a string that had the placeholder embedded).
		s = strings.ReplaceAll(s, placeholder, valStr)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return params
	}
	return result
}

// dispatchTool sends a JSON-RPC 2.0 tools/call request to the MCP server
// configured in cfg, parses the result content, and returns it as interface{}.
func dispatchTool(
	ctx *rctx.Context,
	cfg ExecutePlanConfig,
	timeoutMs int,
	toolName string,
	params map[string]interface{},
) (interface{}, error) {
	if cfg.MCPConfig.URL == "" {
		return nil, fmt.Errorf("no tool dispatcher configured")
	}

	// Build JSON-RPC 2.0 request.
	rpcReq := executePlanRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      toolName,
			"arguments": params,
		},
	}
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("request marshal failed: %w", err)
	}

	client := getMCPClient(cfg.MCPConfig.URL, timeoutMs)
	maxAttempts := 3

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		start := time.Now()

		reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
		httpReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.MCPConfig.URL, bytes.NewReader(body))
		if reqErr != nil {
			cancel()
			return nil, fmt.Errorf("request build failed: %w", reqErr)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		apiKey := cfg.MCPAPIKey
		if apiKey == "" {
			apiKey = cfg.MCPConfig.APIKeyRef
		}
		if apiKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
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
			return nil, fmt.Errorf("upstream error: %w", doErr)
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr != nil {
			if attempt < maxAttempts {
				time.Sleep(llmBackoff(attempt))
				continue
			}
			return nil, fmt.Errorf("response read failed: %w", readErr)
		}

		// Retry on rate-limit or transient server errors.
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			if attempt < maxAttempts {
				time.Sleep(llmBackoff(attempt))
				continue
			}
			return nil, fmt.Errorf("server error: HTTP %d", resp.StatusCode)
		}

		// Parse JSON-RPC response.
		var rpcResp executePlanRPCResponse
		if parseErr := json.Unmarshal(respBody, &rpcResp); parseErr != nil {
			return nil, fmt.Errorf("response parse failed: %w", parseErr)
		}

		// JSON-RPC application-level error.
		if rpcResp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
		}

		if rpcResp.Result == nil {
			return nil, fmt.Errorf("empty result")
		}

		// Extract text from content array.
		return extractPlanResultContent(rpcResp.Result.Content), nil
	}

	return nil, fmt.Errorf("all retries exhausted")
}

// extractPlanResultContent parses the MCP content array and returns the
// most useful representation:
//   - If the text parses as JSON, return the parsed value (preserving arrays/objects).
//   - Otherwise return the raw text string.
func extractPlanResultContent(content json.RawMessage) interface{} {
	if len(content) == 0 {
		return nil
	}

	// content is a JSON array: [{"type":"text","text":"..."}]
	var items []executePlanContentItem
	if err := json.Unmarshal(content, &items); err != nil {
		// Fall back: try to parse content itself as a value.
		var v interface{}
		if err2 := json.Unmarshal(content, &v); err2 == nil {
			return v
		}
		return string(content)
	}

	// Collect all text blocks.
	var parts []string
	for _, item := range items {
		if item.Type == "text" && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}

	if len(parts) == 0 {
		return nil
	}

	// Combine text parts.
	text := strings.Join(parts, "")

	// Try to parse the combined text as JSON for structured results.
	var parsed interface{}
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			return parsed
		}
	}

	return text
}
