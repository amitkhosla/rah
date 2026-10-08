package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// ServeA2AConfig configures a serve_a2a instruction.
// SkillRoutes maps skill ID to the registered API path of the skill flow.
// Gateway dispatches tasks/send to the skill's HTTP endpoint — same pattern
// as serve_mcp's API tool calls.
type ServeA2AConfig struct {
	SkillRoutes     map[string]string
	GatewayBase     string       // static base URL; empty means use GatewayBaseFunc
	GatewayBaseFunc func() string // called at request time when GatewayBase is empty
	TimeoutMs       int
}

// ── JSON-RPC 2.0 types ────────────────────────────────────────────────────────

// a2aServRequest is a JSON-RPC 2.0 request envelope for A2A protocol.
type a2aServRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// a2aServResponse is a JSON-RPC 2.0 response envelope for A2A protocol.
type a2aServResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *a2aServRPCErr  `json:"error,omitempty"`
}

// a2aServRPCErr is the JSON-RPC 2.0 error object for A2A protocol.
type a2aServRPCErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// a2aServTaskSendParams is the expected shape of tasks/send params.
type a2aServTaskSendParams struct {
	ID      string          `json:"id"`
	Message json.RawMessage `json:"message"`
}

// a2aServTask is the result shape of a dispatched task.
type a2aServTask struct {
	ID     string          `json:"id"`
	Status a2aServStatus   `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// a2aServStatus indicates the execution state of a task.
type a2aServStatus struct {
	State string `json:"state"`
}

// ── HTTP client pool ──────────────────────────────────────────────────────────

// a2aServClientKey is the composite cache key for the HTTP client pool.
type a2aServClientKey struct {
	baseURL   string
	timeoutMs int
}

var a2aServeClientCache sync.Map // key: a2aServClientKey → *http.Client

func getA2AServeClient(baseURL string, timeoutMs int) *http.Client {
	key := a2aServClientKey{baseURL: baseURL, timeoutMs: timeoutMs}
	if c, ok := a2aServeClientCache.Load(key); ok {
		return c.(*http.Client)
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	actual, _ := a2aServeClientCache.LoadOrStore(key, client)
	return actual.(*http.Client)
}

// ── a2aServWriteResult and a2aServWriteError helpers ──────────────────────────

// a2aServWriteResult encodes a successful JSON-RPC 2.0 response to w.
func a2aServWriteResult(w http.ResponseWriter, id json.RawMessage, result any) {
	resp := a2aServResponse{JSONRPC: "2.0", ID: id, Result: result}
	_ = json.NewEncoder(w).Encode(resp)
}

// a2aServWriteError encodes a JSON-RPC 2.0 error response to w.
func a2aServWriteError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := a2aServResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &a2aServRPCErr{Code: code, Message: msg},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ── ServeA2A instruction ──────────────────────────────────────────────────────

// ServeA2A returns an engine.Instruction that handles A2A JSON-RPC 2.0 protocol
// for the configured skill routes. Skill flows are dispatched via HTTP to the
// gateway's data port — same pattern as serve_mcp API tool calls.
// Always returns engine.StopPlan.
func ServeA2A(cfg ServeA2AConfig) engine.Instruction {
	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 30_000
	}

	return engine.Instruction{
		Name: "a2a_serve",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			w := ctx.GetWriter()
			if w == nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}
			w.Header().Set("Content-Type", "application/json")
			ctx.ResponseStatus = 200

			// Read request body.
			var rawBody []byte
			if ctx.Request != nil {
				limit := ctx.MaxBodySize
				if limit <= 0 {
					limit = 4 * 1024 * 1024
				}
				if b, err := io.ReadAll(io.LimitReader(ctx.Request.Body, limit)); err == nil {
					rawBody = b
				}
			}
			if len(rawBody) == 0 {
				a2aServWriteError(w, nil, -32700, "parse error: empty body")
				return engine.StopPlan
			}

			var req a2aServRequest
			if err := json.Unmarshal(rawBody, &req); err != nil {
				a2aServWriteError(w, nil, -32700, "parse error: "+err.Error())
				return engine.StopPlan
			}
			if req.JSONRPC != "2.0" {
				a2aServWriteError(w, req.ID, -32600, "invalid request: jsonrpc must be 2.0")
				return engine.StopPlan
			}

			switch req.Method {
			case "tasks/send":
				a2aServHandleTaskSend(w, req, cfg, timeoutMs, ctx)
			case "tasks/get":
				a2aServWriteError(w, req.ID, -32601, "tasks/get: async tasks not supported in this version")
			default:
				a2aServWriteError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
			}

			return engine.StopPlan
		},
	}
}

func a2aServHandleTaskSend(
	w http.ResponseWriter,
	req a2aServRequest,
	cfg ServeA2AConfig,
	timeoutMs int,
	rctxCtx *rctx.Context,
) {
	var params a2aServTaskSendParams
	if len(req.Params) == 0 {
		a2aServWriteError(w, req.ID, -32602, "invalid params: tasks/send requires params")
		return
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		a2aServWriteError(w, req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	// Extract skill ID from message. The A2A message may carry a "skill" hint field.
	skillID := ""
	if len(params.Message) > 0 {
		var msg struct {
			Skill string `json:"skill"`
		}
		if err := json.Unmarshal(params.Message, &msg); err == nil {
			skillID = msg.Skill
		}
	}
	// Fall back to the only skill if exactly one is configured.
	if skillID == "" && len(cfg.SkillRoutes) == 1 {
		for k := range cfg.SkillRoutes {
			skillID = k
		}
	}

	path, ok := cfg.SkillRoutes[skillID]
	if !ok {
		a2aServWriteError(w, req.ID, -32602, fmt.Sprintf("unknown skill: %q", skillID))
		return
	}

	// Dispatch task message to skill flow via HTTP.
	bodyBytes := params.Message
	if len(bodyBytes) == 0 {
		bodyBytes = []byte("{}")
	}

	baseCtx := context.Background()
	if rctxCtx != nil && rctxCtx.Request != nil {
		baseCtx = rctxCtx.Request.Context()
	}
	reqCtx, cancel := context.WithTimeout(baseCtx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	base := cfg.GatewayBase
	if base == "" && cfg.GatewayBaseFunc != nil {
		base = cfg.GatewayBaseFunc()
	}
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+path, bytes.NewReader(bodyBytes))
	if err != nil {
		a2aServWriteError(w, req.ID, -32603, "dispatch error: "+err.Error())
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Propagate inbound headers (auth tokens, trace context) to the skill call.
	if rctxCtx != nil && rctxCtx.Request != nil {
		for key, vals := range rctxCtx.Request.Header {
			if httpReq.Header.Get(key) == "" {
				for _, v := range vals {
					httpReq.Header.Add(key, v)
				}
			}
		}
	}

	client := getA2AServeClient(base, timeoutMs)
	resp, err := client.Do(httpReq)
	if err != nil {
		a2aServWriteError(w, req.ID, -32603, "skill dispatch failed: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	resultBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		a2aServWriteError(w, req.ID, -32603, "read response failed: "+err.Error())
		return
	}

	taskID := params.ID
	if taskID == "" {
		taskID = fmt.Sprintf("task-%d", time.Now().UnixNano())
	}

	task := a2aServTask{ID: taskID}
	if resp.StatusCode >= 300 {
		task.Status = a2aServStatus{State: "failed"}
		task.Error = string(resultBody)
	} else {
		task.Status = a2aServStatus{State: "completed"}
		task.Result = json.RawMessage(resultBody)
	}

	a2aServWriteResult(w, req.ID, task)
}
