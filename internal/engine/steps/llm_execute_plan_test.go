package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// newTestContext creates a minimal rctx.Context suitable for unit tests.
func newTestContext() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.InitSlots()
	ctx.ResponseStatus = 200
	return ctx
}

// newTestExecutionState creates a minimal ExecutionState for tests.
func newTestExecutionState() *engine.ExecutionState {
	return &engine.ExecutionState{}
}

// writeSlotStr writes a string value into the given ByteSlot index.
func writeSlotStr(ctx *rctx.Context, slot int, val string) {
	ctx.ByteSlots[slot] = []byte(val)
}

// readSlotStr reads the ByteSlot at index as a string.
func readSlotStr(ctx *rctx.Context, slot int) string {
	if slot < 0 || slot >= len(ctx.ByteSlots) {
		return ""
	}
	return string(ctx.ByteSlots[slot])
}

// buildMCPServer creates an httptest.Server that serves JSON-RPC tools/call
// responses. The handler function receives the tool name and arguments and
// returns (responseBody string, statusCode int).
func buildMCPServer(t *testing.T, handler func(toolName string, args map[string]interface{}) (interface{}, int)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name      string                 `json:"name"`
				Arguments map[string]interface{} `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}

		result, status := handler(req.Params.Name, req.Params.Arguments)
		if status != 200 {
			w.WriteHeader(status)
			return
		}

		// Encode result as MCP text content.
		var textContent string
		if result == nil {
			textContent = "null"
		} else {
			b, _ := json.Marshal(result)
			textContent = string(b)
		}

		content := []map[string]string{{"type": "text", "text": textContent}}
		contentBytes, _ := json.Marshal(content)

		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]interface{}{"content": json.RawMessage(contentBytes)},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// makePlanJSON marshals a plan array into the top-level ExecutionPlan JSON.
func makePlanJSON(t *testing.T, steps []PlanStep) string {
	t.Helper()
	plan := ExecutionPlan{Summary: "test", Plan: steps}
	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("makePlanJSON marshal: %v", err)
	}
	return string(b)
}

// â"€â"€â"€ Tests â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestExecutePlan_EmptyPlanSlot_Noop(t *testing.T) {
	ctx := newTestContext()
	state := newTestExecutionState()

	cfg := ExecutePlanConfig{
		PlanSlot:   0,
		ResultSlot: 1,
		ErrorSlot:  -1,
	}
	instr := ExecutePlan(cfg)

	// ByteSlots[0] is empty — should be a no-op.
	next := instr.Action(ctx, state)
	if next != state.PC+1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	if len(ctx.ByteSlots[1]) != 0 {
		t.Fatalf("expected empty ResultSlot, got %q", ctx.ByteSlots[1])
	}
	if ctx.Failed {
		t.Fatal("expected Failed=false")
	}
}

func TestExecutePlan_LinearPlan_ExecutesInOrder(t *testing.T) {
	var callOrder []string

	srv := buildMCPServer(t, func(toolName string, args map[string]interface{}) (interface{}, int) {
		callOrder = append(callOrder, toolName)
		switch toolName {
		case "step_one":
			return "result_one", 200
		case "step_two":
			return "result_two", 200
		}
		return nil, 200
	})

	ctx := newTestContext()
	state := newTestExecutionState()

	plan := makePlanJSON(t, []PlanStep{
		{Step: 1, ToolName: "step_one", Params: map[string]interface{}{}, DependsOn: nil, StopOnError: true},
		{Step: 2, ToolName: "step_two", Params: map[string]interface{}{}, DependsOn: float64(1), StopOnError: true},
	})
	writeSlotStr(ctx, 0, plan)

	cfg := ExecutePlanConfig{
		PlanSlot:   0,
		ResultSlot: 1,
		ErrorSlot:  2,
		MCPConfig:  config.MCPServerConfig{URL: srv.URL, Transport: "http"},
		TimeoutMs:  5000,
	}
	instr := ExecutePlan(cfg)
	next := instr.Action(ctx, state)

	if next != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (error: %s)", next, readSlotStr(ctx, 2))
	}
	if ctx.Failed {
		t.Fatalf("expected Failed=false, error: %s", readSlotStr(ctx, 2))
	}

	// Verify both tools were called in order.
	if len(callOrder) != 2 {
		t.Fatalf("expected 2 calls, got %d: %v", len(callOrder), callOrder)
	}
	if callOrder[0] != "step_one" {
		t.Fatalf("expected step_one first, got %s", callOrder[0])
	}
	if callOrder[1] != "step_two" {
		t.Fatalf("expected step_two second, got %s", callOrder[1])
	}

	// Verify results JSON has both steps.
	resultJSON := readSlotStr(ctx, 1)
	var results map[string]interface{}
	if err := json.Unmarshal([]byte(resultJSON), &results); err != nil {
		t.Fatalf("result JSON parse: %v", err)
	}
	if _, ok := results["1"]; !ok {
		t.Fatalf("missing step 1 in results: %s", resultJSON)
	}
	if _, ok := results["2"]; !ok {
		t.Fatalf("missing step 2 in results: %s", resultJSON)
	}
}

func TestExecutePlan_IndependentSteps_BothExecuted(t *testing.T) {
	var called []string

	srv := buildMCPServer(t, func(toolName string, args map[string]interface{}) (interface{}, int) {
		called = append(called, toolName)
		return toolName + "_result", 200
	})

	ctx := newTestContext()
	state := newTestExecutionState()

	plan := makePlanJSON(t, []PlanStep{
		{Step: 1, ToolName: "tool_a", Params: map[string]interface{}{}, DependsOn: nil},
		{Step: 2, ToolName: "tool_b", Params: map[string]interface{}{}, DependsOn: nil},
	})
	writeSlotStr(ctx, 0, plan)

	cfg := ExecutePlanConfig{
		PlanSlot:   0,
		ResultSlot: 1,
		ErrorSlot:  2,
		MCPConfig:  config.MCPServerConfig{URL: srv.URL, Transport: "http"},
		TimeoutMs:  5000,
	}
	instr := ExecutePlan(cfg)
	next := instr.Action(ctx, state)

	if next != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (error: %s)", next, readSlotStr(ctx, 2))
	}
	if len(called) != 2 {
		t.Fatalf("expected 2 tool calls, got %d: %v", len(called), called)
	}

	var results map[string]interface{}
	if err := json.Unmarshal([]byte(readSlotStr(ctx, 1)), &results); err != nil {
		t.Fatalf("result parse: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(results), results)
	}
}

func TestExecutePlan_FanOut_LoopsOverParentList(t *testing.T) {
	callArgs := make([]map[string]interface{}, 0)

	srv := buildMCPServer(t, func(toolName string, args map[string]interface{}) (interface{}, int) {
		switch toolName {
		case "get_list":
			// Return a list for fan-out.
			return []interface{}{"A", "B", "C"}, 200
		case "process_item":
			callArgs = append(callArgs, copyArgs(args))
			itemStr := fmt.Sprintf("%v", args["item"])
			return "processed_" + itemStr, 200
		}
		return nil, 200
	})

	ctx := newTestContext()
	state := newTestExecutionState()

	plan := makePlanJSON(t, []PlanStep{
		{Step: 1, ToolName: "get_list", Params: map[string]interface{}{}, DependsOn: nil, ExpectListResponse: true},
		{Step: 2, ToolName: "process_item", Params: map[string]interface{}{"item": "{item}"}, DependsOn: float64(1)},
	})
	writeSlotStr(ctx, 0, plan)

	cfg := ExecutePlanConfig{
		PlanSlot:   0,
		ResultSlot: 1,
		ErrorSlot:  2,
		MCPConfig:  config.MCPServerConfig{URL: srv.URL, Transport: "http"},
		TimeoutMs:  5000,
	}
	instr := ExecutePlan(cfg)
	next := instr.Action(ctx, state)

	if next != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (error: %s)", next, readSlotStr(ctx, 2))
	}

	// process_item should have been called 3 times — once per list element.
	if len(callArgs) != 3 {
		t.Fatalf("expected 3 fan-out calls, got %d", len(callArgs))
	}

	// Verify injected item values.
	seen := map[string]bool{}
	for _, a := range callArgs {
		if item, ok := a["item"].(string); ok {
			seen[item] = true
		}
	}
	for _, want := range []string{"A", "B", "C"} {
		if !seen[want] {
			t.Fatalf("expected item %q to be injected, got args: %v", want, callArgs)
		}
	}
}

func TestExecutePlan_StopOnError_HaltsExecution(t *testing.T) {
	srv := buildMCPServer(t, func(toolName string, args map[string]interface{}) (interface{}, int) {
		return nil, 500
	})

	ctx := newTestContext()
	state := newTestExecutionState()

	plan := makePlanJSON(t, []PlanStep{
		{Step: 1, ToolName: "failing_tool", Params: map[string]interface{}{}, DependsOn: nil, StopOnError: true},
	})
	writeSlotStr(ctx, 0, plan)

	cfg := ExecutePlanConfig{
		PlanSlot:   0,
		ResultSlot: 1,
		ErrorSlot:  2,
		MCPConfig:  config.MCPServerConfig{URL: srv.URL, Transport: "http"},
		TimeoutMs:  5000,
	}
	instr := ExecutePlan(cfg)
	next := instr.Action(ctx, state)

	if next != engine.StopPlan {
		t.Fatalf("expected StopPlan, got %d", next)
	}
	if !ctx.Failed {
		t.Fatal("expected Failed=true")
	}
	if ctx.ResponseStatus != 500 {
		t.Fatalf("expected ResponseStatus=500, got %d", ctx.ResponseStatus)
	}
	errMsg := readSlotStr(ctx, 2)
	if errMsg == "" {
		t.Fatal("expected error message in ErrorSlot, got empty")
	}
}

func TestExecutePlan_ContinueOnError_NoHalt(t *testing.T) {
	srv := buildMCPServer(t, func(toolName string, args map[string]interface{}) (interface{}, int) {
		if toolName == "failing_tool" {
			return nil, 500
		}
		return "ok", 200
	})

	ctx := newTestContext()
	state := newTestExecutionState()

	plan := makePlanJSON(t, []PlanStep{
		{Step: 1, ToolName: "failing_tool", Params: map[string]interface{}{}, DependsOn: nil, StopOnError: false},
		{Step: 2, ToolName: "ok_tool", Params: map[string]interface{}{}, DependsOn: nil, StopOnError: false},
	})
	writeSlotStr(ctx, 0, plan)

	cfg := ExecutePlanConfig{
		PlanSlot:   0,
		ResultSlot: 1,
		ErrorSlot:  2,
		MCPConfig:  config.MCPServerConfig{URL: srv.URL, Transport: "http"},
		TimeoutMs:  5000,
	}
	instr := ExecutePlan(cfg)
	next := instr.Action(ctx, state)

	// Should NOT halt — stop_on_error is false.
	if next != state.PC+1 {
		t.Fatalf("expected PC+1 (continue on error), got %d", next)
	}
	if ctx.Failed {
		t.Fatal("expected Failed=false")
	}

	// Result slot should be populated.
	var results map[string]interface{}
	if err := json.Unmarshal([]byte(readSlotStr(ctx, 1)), &results); err != nil {
		t.Fatalf("result parse: %v", err)
	}
	// Step 1 result should be nil (error occurred, not stopped).
	if _, ok := results["1"]; !ok {
		t.Fatalf("missing step 1 in results: %v", results)
	}
}

func TestExecutePlan_NewToolRequired_SkipNewToolTrue(t *testing.T) {
	var called []string

	srv := buildMCPServer(t, func(toolName string, args map[string]interface{}) (interface{}, int) {
		called = append(called, toolName)
		return "ok", 200
	})

	ctx := newTestContext()
	state := newTestExecutionState()

	plan := makePlanJSON(t, []PlanStep{
		{Step: 1, ToolName: "unavailable_tool", Params: map[string]interface{}{}, DependsOn: nil, NewToolRequired: true},
		{Step: 2, ToolName: "normal_tool", Params: map[string]interface{}{}, DependsOn: nil},
	})
	writeSlotStr(ctx, 0, plan)

	cfg := ExecutePlanConfig{
		PlanSlot:            0,
		ResultSlot:          1,
		ErrorSlot:           2,
		MCPConfig:           config.MCPServerConfig{URL: srv.URL, Transport: "http"},
		TimeoutMs:           5000,
		SkipNewToolRequired: true,
	}
	instr := ExecutePlan(cfg)
	next := instr.Action(ctx, state)

	if next != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (error: %s)", next, readSlotStr(ctx, 2))
	}
	if ctx.Failed {
		t.Fatal("expected Failed=false")
	}

	// unavailable_tool should NOT have been called.
	for _, c := range called {
		if c == "unavailable_tool" {
			t.Fatal("unavailable_tool should not have been called")
		}
	}
}

func TestExecutePlan_NewToolRequired_SkipNewToolFalse(t *testing.T) {
	ctx := newTestContext()
	state := newTestExecutionState()

	plan := makePlanJSON(t, []PlanStep{
		{Step: 1, ToolName: "unavailable_tool", Params: map[string]interface{}{}, DependsOn: nil, NewToolRequired: true},
	})
	writeSlotStr(ctx, 0, plan)

	cfg := ExecutePlanConfig{
		PlanSlot:            0,
		ResultSlot:          1,
		ErrorSlot:           2,
		MCPConfig:           config.MCPServerConfig{URL: "http://localhost:9999", Transport: "http"},
		TimeoutMs:           5000,
		SkipNewToolRequired: false,
	}
	instr := ExecutePlan(cfg)
	next := instr.Action(ctx, state)

	if next != engine.StopPlan {
		t.Fatalf("expected StopPlan, got %d", next)
	}
	if !ctx.Failed {
		t.Fatal("expected Failed=true")
	}
	errMsg := readSlotStr(ctx, 2)
	if errMsg == "" {
		t.Fatal("expected error in ErrorSlot")
	}
}

func TestExecutePlan_InjectValue_ReplacesPlaceholder(t *testing.T) {
	var capturedArgs map[string]interface{}

	srv := buildMCPServer(t, func(toolName string, args map[string]interface{}) (interface{}, int) {
		if toolName == "step_one" {
			return []interface{}{"AAPL"}, 200
		}
		capturedArgs = args
		return "done", 200
	})

	ctx := newTestContext()
	state := newTestExecutionState()

	plan := makePlanJSON(t, []PlanStep{
		{Step: 1, ToolName: "step_one", Params: map[string]interface{}{}, DependsOn: nil, ExpectListResponse: true},
		{
			Step:      2,
			ToolName:  "process_stock",
			Params:    map[string]interface{}{"symbol": "{item}", "extra": "static"},
			DependsOn: float64(1),
		},
	})
	writeSlotStr(ctx, 0, plan)

	cfg := ExecutePlanConfig{
		PlanSlot:   0,
		ResultSlot: 1,
		ErrorSlot:  2,
		MCPConfig:  config.MCPServerConfig{URL: srv.URL, Transport: "http"},
		TimeoutMs:  5000,
	}
	instr := ExecutePlan(cfg)
	next := instr.Action(ctx, state)

	if next != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (error: %s)", next, readSlotStr(ctx, 2))
	}

	if capturedArgs == nil {
		t.Fatal("expected process_stock to be called")
	}
	if sym, ok := capturedArgs["symbol"].(string); !ok || sym != "AAPL" {
		t.Fatalf("expected symbol=AAPL, got %v", capturedArgs["symbol"])
	}
	if extra, ok := capturedArgs["extra"].(string); !ok || extra != "static" {
		t.Fatalf("expected extra=static, got %v", capturedArgs["extra"])
	}
}

func TestExecutePlan_ResultSlot_HasAllStepResults(t *testing.T) {
	srv := buildMCPServer(t, func(toolName string, args map[string]interface{}) (interface{}, int) {
		return toolName + "_output", 200
	})

	ctx := newTestContext()
	state := newTestExecutionState()

	plan := makePlanJSON(t, []PlanStep{
		{Step: 1, ToolName: "fetch_a", Params: map[string]interface{}{}, DependsOn: nil},
		{Step: 2, ToolName: "fetch_b", Params: map[string]interface{}{}, DependsOn: nil},
		{Step: 3, ToolName: "fetch_c", Params: map[string]interface{}{}, DependsOn: float64(1)},
	})
	writeSlotStr(ctx, 0, plan)

	cfg := ExecutePlanConfig{
		PlanSlot:   0,
		ResultSlot: 1,
		ErrorSlot:  2,
		MCPConfig:  config.MCPServerConfig{URL: srv.URL, Transport: "http"},
		TimeoutMs:  5000,
	}
	instr := ExecutePlan(cfg)
	next := instr.Action(ctx, state)

	if next != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (error: %s)", next, readSlotStr(ctx, 2))
	}

	var results map[string]interface{}
	if err := json.Unmarshal([]byte(readSlotStr(ctx, 1)), &results); err != nil {
		t.Fatalf("result parse: %v", err)
	}

	for _, key := range []string{"1", "2", "3"} {
		if _, ok := results[key]; !ok {
			t.Fatalf("missing step %s in results: %v", key, results)
		}
	}
}

// â"€â"€â"€ Helpers â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// copyArgs deep-copies a map[string]interface{} for later inspection.
func copyArgs(args map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}

