package steps

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// ── helpers ────────────────────────────────────────────────────────────────────

func newServeA2ATestContext() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 16)
	ctx.IntSlots = make([]int64, 8)
	ctx.BoolSlots = make([]bool, 8)
	return ctx
}

func runServeA2AInstruction(instr engine.Instruction, ctx *rctx.Context) int16 {
	state := &engine.ExecutionState{PC: 0}
	return instr.Action(ctx, state)
}

// parseA2AResponse unmarshals a JSON-RPC response from body.
// Since resp.Result can be any type when JSON is unmarshaled, we preserve it as json.RawMessage
func parseA2AResponse(body []byte) map[string]any {
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	return resp
}

// ── TestServeA2A_TaskSend_ValidSkill ───────────────────────────────────────────

func TestServeA2A_TaskSend_ValidSkill(t *testing.T) {
	// Mock skill endpoint that returns a successful response
	skillSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back the request with a success wrapper
		body, _ := io.ReadAll(r.Body)
		resp := map[string]any{
			"success": true,
			"echo":    json.RawMessage(body),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer skillSrv.Close()

	// Prepare request body with skill and message
	reqBody := a2aServRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "tasks/send",
		Params: json.RawMessage(`{
			"id": "task-123",
			"message": {"skill": "my-skill", "data": "test"}
		}`),
	}
	reqBodyBytes, _ := json.Marshal(reqBody)

	// Create HTTP request
	httpReq := httptest.NewRequest("POST", "http://localhost/a2a", bytes.NewReader(reqBodyBytes))
	w := httptest.NewRecorder()

	ctx := newServeA2ATestContext()
	ctx.Request = httpReq
	ctx.Writer = w

	// Create instruction with skill route pointing to mock server
	cfg := ServeA2AConfig{
		SkillRoutes: map[string]string{
			"my-skill": "/skill-endpoint",
		},
		GatewayBase: skillSrv.URL,
		TimeoutMs:   5000,
	}

	instr := ServeA2A(cfg)
	result := runServeA2AInstruction(instr, ctx)

	// Verify it always returns StopPlan
	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}

	// Verify response is valid JSON-RPC 2.0
	respBody := w.Body.Bytes()
	resp := parseA2AResponse(respBody)

	if jsonrpc, ok := resp["jsonrpc"].(string); !ok || jsonrpc != "2.0" {
		t.Errorf("expected jsonrpc 2.0")
	}

	// Verify task was returned with completed status
	var task a2aServTask
	if result, ok := resp["result"]; ok {
		resultBytes, _ := json.Marshal(result)
		_ = json.Unmarshal(resultBytes, &task)
	}

	if task.ID != "task-123" {
		t.Errorf("expected task ID task-123, got %s", task.ID)
	}
	if task.Status.State != "completed" {
		t.Errorf("expected status completed, got %s", task.Status.State)
	}
	if len(task.Result) == 0 {
		t.Errorf("expected non-empty result from skill")
	}
	if task.Error != "" {
		t.Errorf("expected no error, got %s", task.Error)
	}
}

// ── TestServeA2A_TaskSend_SingleSkillFallback ──────────────────────────────────

func TestServeA2A_TaskSend_SingleSkillFallback(t *testing.T) {
	// Mock skill endpoint
	skillSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
	}))
	defer skillSrv.Close()

	// Request without skill field in message — should fall back to only configured skill
	reqBody := a2aServRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "tasks/send",
		Params: json.RawMessage(`{
			"id": "task-456",
			"message": {"data": "test"}
		}`),
	}
	reqBodyBytes, _ := json.Marshal(reqBody)

	httpReq := httptest.NewRequest("POST", "http://localhost/a2a", bytes.NewReader(reqBodyBytes))
	w := httptest.NewRecorder()

	ctx := newServeA2ATestContext()
	ctx.Request = httpReq
	ctx.Writer = w

	// Only one skill configured — should be used automatically
	cfg := ServeA2AConfig{
		SkillRoutes: map[string]string{
			"default-skill": "/default",
		},
		GatewayBase: skillSrv.URL,
		TimeoutMs:   5000,
	}

	instr := ServeA2A(cfg)
	result := runServeA2AInstruction(instr, ctx)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}

	respBody := w.Body.Bytes()
	resp := parseA2AResponse(respBody)

	if errVal, ok := resp["error"]; ok && errVal != nil {
		t.Errorf("expected no error, got %v", errVal)
	}

	// Verify task completed successfully
	var task a2aServTask
	if result, ok := resp["result"]; ok {
		resultBytes, _ := json.Marshal(result)
		_ = json.Unmarshal(resultBytes, &task)
	}

	if task.Status.State != "completed" {
		t.Errorf("expected completed, got %s", task.Status.State)
	}
}

// ── TestServeA2A_TaskSend_SkillIDOverride ─────────────────────────────────────

func TestServeA2A_TaskSend_SkillIDOverride(t *testing.T) {
	// Mock skill endpoint that reflects which skill was called
	skillSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"path": r.URL.Path,
		})
	}))
	defer skillSrv.Close()

	// Request with explicit skill in message
	reqBody := a2aServRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("3"),
		Method:  "tasks/send",
		Params: json.RawMessage(`{
			"id": "task-789",
			"message": {"skill": "premium-skill", "action": "analyze"}
		}`),
	}
	reqBodyBytes, _ := json.Marshal(reqBody)

	httpReq := httptest.NewRequest("POST", "http://localhost/a2a", bytes.NewReader(reqBodyBytes))
	w := httptest.NewRecorder()

	ctx := newServeA2ATestContext()
	ctx.Request = httpReq
	ctx.Writer = w

	cfg := ServeA2AConfig{
		SkillRoutes: map[string]string{
			"basic-skill":   "/basic",
			"premium-skill": "/premium",
		},
		GatewayBase: skillSrv.URL,
		TimeoutMs:   5000,
	}

	instr := ServeA2A(cfg)
	result := runServeA2AInstruction(instr, ctx)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}

	respBody := w.Body.Bytes()
	resp := parseA2AResponse(respBody)

	if errVal, ok := resp["error"]; ok && errVal != nil {
		t.Errorf("expected no error, got %v", errVal)
	}

	// Verify premium endpoint was called
	var task a2aServTask
	if result, ok := resp["result"]; ok {
		resultBytes, _ := json.Marshal(result)
		_ = json.Unmarshal(resultBytes, &task)
	}
	var resultData map[string]string
	_ = json.Unmarshal(task.Result, &resultData)

	if resultData["path"] != "/premium" {
		t.Errorf("expected /premium path, got %s", resultData["path"])
	}
}

// ── TestServeA2A_UnknownSkill ──────────────────────────────────────────────────

func TestServeA2A_UnknownSkill(t *testing.T) {
	// Request with skill not in config
	reqBody := a2aServRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("4"),
		Method:  "tasks/send",
		Params: json.RawMessage(`{
			"id": "task-unknown",
			"message": {"skill": "nonexistent-skill"}
		}`),
	}
	reqBodyBytes, _ := json.Marshal(reqBody)

	httpReq := httptest.NewRequest("POST", "http://localhost/a2a", bytes.NewReader(reqBodyBytes))
	w := httptest.NewRecorder()

	ctx := newServeA2ATestContext()
	ctx.Request = httpReq
	ctx.Writer = w

	cfg := ServeA2AConfig{
		SkillRoutes: map[string]string{
			"skill-a": "/a",
			"skill-b": "/b",
		},
		GatewayBase: "http://localhost",
		TimeoutMs:   5000,
	}

	instr := ServeA2A(cfg)
	result := runServeA2AInstruction(instr, ctx)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}

	respBody := w.Body.Bytes()
	resp := parseA2AResponse(respBody)

	// Verify JSON-RPC error -32602 (invalid params)
	if errVal, ok := resp["error"].(map[string]any); ok {
		code := int(errVal["code"].(float64))
		if code != -32602 {
			t.Errorf("expected error code -32602, got %d", code)
		}
	} else {
		t.Errorf("expected error, got nil")
	}
}

// ── TestServeA2A_MalformedJSON ────────────────────────────────────────────────

func TestServeA2A_MalformedJSON(t *testing.T) {
	// Send non-JSON body
	httpReq := httptest.NewRequest("POST", "http://localhost/a2a", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()

	ctx := newServeA2ATestContext()
	ctx.Request = httpReq
	ctx.Writer = w

	cfg := ServeA2AConfig{
		SkillRoutes: map[string]string{
			"skill-a": "/a",
		},
		GatewayBase: "http://localhost",
		TimeoutMs:   5000,
	}

	instr := ServeA2A(cfg)
	result := runServeA2AInstruction(instr, ctx)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}

	respBody := w.Body.Bytes()
	resp := parseA2AResponse(respBody)

	// Verify JSON-RPC error -32700 (parse error)
	if errVal, ok := resp["error"].(map[string]any); ok {
		code := int(errVal["code"].(float64))
		if code != -32700 {
			t.Errorf("expected error code -32700, got %d", code)
		}
	} else {
		t.Errorf("expected error, got nil")
	}
}

// ── TestServeA2A_WrongJSONRPCVersion ───────────────────────────────────────────

func TestServeA2A_WrongJSONRPCVersion(t *testing.T) {
	// Request with jsonrpc: "1.0" instead of "2.0"
	reqBody := a2aServRequest{
		JSONRPC: "1.0",
		ID:      json.RawMessage("5"),
		Method:  "tasks/send",
		Params: json.RawMessage(`{
			"id": "task-version",
			"message": {"skill": "skill-a"}
		}`),
	}
	reqBodyBytes, _ := json.Marshal(reqBody)

	httpReq := httptest.NewRequest("POST", "http://localhost/a2a", bytes.NewReader(reqBodyBytes))
	w := httptest.NewRecorder()

	ctx := newServeA2ATestContext()
	ctx.Request = httpReq
	ctx.Writer = w

	cfg := ServeA2AConfig{
		SkillRoutes: map[string]string{
			"skill-a": "/a",
		},
		GatewayBase: "http://localhost",
		TimeoutMs:   5000,
	}

	instr := ServeA2A(cfg)
	result := runServeA2AInstruction(instr, ctx)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}

	respBody := w.Body.Bytes()
	resp := parseA2AResponse(respBody)

	// Verify JSON-RPC error -32600 (invalid request)
	if errVal, ok := resp["error"].(map[string]any); ok {
		code := int(errVal["code"].(float64))
		if code != -32600 {
			t.Errorf("expected error code -32600, got %d", code)
		}
	} else {
		t.Errorf("expected error, got nil")
	}
}

// ── TestServeA2A_UnknownMethod ─────────────────────────────────────────────────

func TestServeA2A_UnknownMethod(t *testing.T) {
	// Request with unsupported method "tasks/cancel"
	reqBody := a2aServRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("6"),
		Method:  "tasks/cancel",
		Params: json.RawMessage(`{
			"id": "task-to-cancel"
		}`),
	}
	reqBodyBytes, _ := json.Marshal(reqBody)

	httpReq := httptest.NewRequest("POST", "http://localhost/a2a", bytes.NewReader(reqBodyBytes))
	w := httptest.NewRecorder()

	ctx := newServeA2ATestContext()
	ctx.Request = httpReq
	ctx.Writer = w

	cfg := ServeA2AConfig{
		SkillRoutes: map[string]string{
			"skill-a": "/a",
		},
		GatewayBase: "http://localhost",
		TimeoutMs:   5000,
	}

	instr := ServeA2A(cfg)
	result := runServeA2AInstruction(instr, ctx)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}

	respBody := w.Body.Bytes()
	resp := parseA2AResponse(respBody)

	// Verify JSON-RPC error -32601 (method not found)
	if errVal, ok := resp["error"].(map[string]any); ok {
		code := int(errVal["code"].(float64))
		if code != -32601 {
			t.Errorf("expected error code -32601, got %d", code)
		}
	} else {
		t.Errorf("expected error, got nil")
	}
}

// ── TestServeA2A_SkillHTTP400 ──────────────────────────────────────────────────

func TestServeA2A_SkillHTTP400(t *testing.T) {
	// Mock skill endpoint that returns HTTP 400
	skillSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid request to skill"))
	}))
	defer skillSrv.Close()

	reqBody := a2aServRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("7"),
		Method:  "tasks/send",
		Params: json.RawMessage(`{
			"id": "task-bad-request",
			"message": {"skill": "skill-a", "invalid": "data"}
		}`),
	}
	reqBodyBytes, _ := json.Marshal(reqBody)

	httpReq := httptest.NewRequest("POST", "http://localhost/a2a", bytes.NewReader(reqBodyBytes))
	w := httptest.NewRecorder()

	ctx := newServeA2ATestContext()
	ctx.Request = httpReq
	ctx.Writer = w

	cfg := ServeA2AConfig{
		SkillRoutes: map[string]string{
			"skill-a": "/a",
		},
		GatewayBase: skillSrv.URL,
		TimeoutMs:   5000,
	}

	instr := ServeA2A(cfg)
	result := runServeA2AInstruction(instr, ctx)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}

	respBody := w.Body.Bytes()
	resp := parseA2AResponse(respBody)

	if errVal, ok := resp["error"]; ok && errVal != nil {
		t.Errorf("expected no RPC error, got %v", errVal)
	}

	// Verify task has failed status with error text
	var task a2aServTask
	if result, ok := resp["result"]; ok {
		resultBytes, _ := json.Marshal(result)
		_ = json.Unmarshal(resultBytes, &task)
	}

	if task.Status.State != "failed" {
		t.Errorf("expected status failed, got %s", task.Status.State)
	}
	if task.Error == "" {
		t.Errorf("expected error text, got empty")
	}
	if task.Error != "Invalid request to skill" {
		t.Errorf("expected error 'Invalid request to skill', got %s", task.Error)
	}
}

// ── TestServeA2A_AlwaysStopPlan ────────────────────────────────────────────────

func TestServeA2A_AlwaysStopPlan(t *testing.T) {
	// Test that ServeA2A returns StopPlan on all code paths

	testCases := []struct {
		name    string
		reqBody []byte
		setup   func(*rctx.Context)
	}{
		{
			name:    "valid request",
			reqBody: []byte(`{"jsonrpc":"2.0","id":1,"method":"tasks/send","params":{"id":"t1","message":{"skill":"s1"}}}`),
			setup: func(ctx *rctx.Context) {
				// Valid setup
			},
		},
		{
			name:    "malformed json",
			reqBody: []byte("bad json"),
			setup: func(ctx *rctx.Context) {
				// Still valid setup, bad request body
			},
		},
		{
			name:    "wrong jsonrpc version",
			reqBody: []byte(`{"jsonrpc":"1.0","id":1,"method":"tasks/send"}`),
			setup: func(ctx *rctx.Context) {
				// Valid request object, but wrong version
			},
		},
		{
			name:    "unknown method",
			reqBody: []byte(`{"jsonrpc":"2.0","id":1,"method":"unknown"}`),
			setup: func(ctx *rctx.Context) {
				// Valid JSON-RPC, but method doesn't exist
			},
		},
		{
			name:    "empty body",
			reqBody: []byte(""),
			setup: func(ctx *rctx.Context) {
				// Empty request body
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			httpReq := httptest.NewRequest("POST", "http://localhost/a2a", bytes.NewReader(tc.reqBody))
			w := httptest.NewRecorder()

			ctx := newServeA2ATestContext()
			ctx.Request = httpReq
			ctx.Writer = w
			tc.setup(ctx)

			cfg := ServeA2AConfig{
				SkillRoutes: map[string]string{
					"s1": "/s1",
				},
				GatewayBase: "http://localhost",
				TimeoutMs:   5000,
			}

			instr := ServeA2A(cfg)
			result := runServeA2AInstruction(instr, ctx)

			if result != engine.StopPlan {
				t.Errorf("expected StopPlan, got %d", result)
			}
		})
	}
}

// ── TestServeA2A_NilWriter ────────────────────────────────────────────────────

func TestServeA2A_NilWriter(t *testing.T) {
	// When ctx.GetWriter() returns nil, should return StopPlan without panic

	httpReq := httptest.NewRequest("POST", "http://localhost/a2a", bytes.NewReader(
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tasks/send"}`),
	))

	ctx := newServeA2ATestContext()
	ctx.Request = httpReq
	// Don't set ResponseWriter — GetWriter will return nil

	cfg := ServeA2AConfig{
		SkillRoutes: map[string]string{
			"s1": "/s1",
		},
		GatewayBase: "http://localhost",
		TimeoutMs:   5000,
	}

	instr := ServeA2A(cfg)

	// Should not panic and should return StopPlan
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("unexpected panic: %v", r)
		}
	}()

	result := runServeA2AInstruction(instr, ctx)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}

	// Verify Failed flag was set
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed to be true")
	}

	if ctx.ResponseStatus != 500 {
		t.Errorf("expected status 500, got %d", ctx.ResponseStatus)
	}
}
