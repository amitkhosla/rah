package control

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"rah/internal/engine"
	"rah/internal/rctx"
)

func TestCompileGraphQLCallStaticQuery(t *testing.T) {
	// Test: Compiling a graphql_call step with static query
	c := NewCompiler(nil)

	// Initialize slot mapping
	c.slotMap = map[string]int{
		"data_response": 0,
		"errors":        1,
	}
	c.nextSlot = 2

	step := StepConfig{
		Action: "graphql_call",
		Input: map[string]string{
			"url":   "https://api.example.com/graphql",
			"query": "query { user { id name } }",
		},
	}

	err := c.compileGraphQLCall(step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}

	if c.GlobalTable[0].Name != "GRAPHQL_CALL" {
		t.Fatalf("expected GRAPHQL_CALL, got %s", c.GlobalTable[0].Name)
	}
}

func TestCompileGraphQLCallWithVariables(t *testing.T) {
	// Test: Compiling graphql_call with variables_slot
	c := NewCompiler(nil)

	c.slotMap = map[string]int{
		"vars":          0,
		"data_response": 1,
	}
	c.nextSlot = 2

	step := StepConfig{
		Action: "graphql_call",
		Input: map[string]string{
			"url":           "https://api.example.com/graphql",
			"query":         "query getUser($id: ID!) { user(id: $id) { id } }",
			"data_slot":     "data_response",
			"variables_slot": "vars",
		},
	}

	err := c.compileGraphQLCall(step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}
}

func TestCompileGraphQLCallWithOperationName(t *testing.T) {
	// Test: Compiling graphql_call with operation_name (no-op field, just verifies no error)
	c := NewCompiler(nil)

	c.slotMap = map[string]int{
		"data_response": 0,
	}
	c.nextSlot = 1

	step := StepConfig{
		Action: "graphql_call",
		Input: map[string]string{
			"url":            "https://api.example.com/graphql",
			"query":          "query getUser { user { id } }",
			"data_slot":      "data_response",
			"operation_name": "getUser",
		},
	}

	err := c.compileGraphQLCall(step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}
}

func TestCompileGraphQLCallErrorsSlot(t *testing.T) {
	// Test: Compiling graphql_call with errors_slot
	c := NewCompiler(nil)

	c.slotMap = map[string]int{
		"data_response": 0,
		"errors":        1,
	}
	c.nextSlot = 2

	step := StepConfig{
		Action: "graphql_call",
		Input: map[string]string{
			"url":            "https://api.example.com/graphql",
			"query":          "query { user { id } }",
			"data_slot":      "data_response",
			"errors_slot":    "errors",
			"fail_on_errors": "true",
		},
	}

	err := c.compileGraphQLCall(step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}
}

func TestCompileGraphQLCallMissingURL(t *testing.T) {
	// Test: Compiling graphql_call with no url or url_slot should error
	c := NewCompiler(nil)

	c.slotMap = map[string]int{}
	c.nextSlot = 0

	step := StepConfig{
		Action: "graphql_call",
		Input: map[string]string{
			"query": "query { user { id } }",
			// no url or url_slot
		},
	}

	err := c.compileGraphQLCall(step)
	if err == nil {
		t.Fatalf("expected error for missing url, got nil")
	}
}

func TestCompileGraphQLGet(t *testing.T) {
	// Test: Compiling graphql_get step
	c := NewCompiler(nil)

	c.slotMap = map[string]int{
		"data":   0,
		"result": 1,
	}
	c.nextSlot = 2

	step := StepConfig{
		Action: "graphql_get",
		Source: "data",
		As:     "result",
		Input: map[string]string{
			"path": "user.name",
		},
	}

	err := c.compileGraphQLGet(step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}

	if c.GlobalTable[0].Name != "GRAPHQL_GET" {
		t.Fatalf("expected GRAPHQL_GET, got %s", c.GlobalTable[0].Name)
	}
}

func TestCompileGraphQLGetMissingSource(t *testing.T) {
	// Test: graphql_get requires a source slot
	c := NewCompiler(nil)

	c.slotMap = map[string]int{
		"result": 0,
	}
	c.nextSlot = 1

	step := StepConfig{
		Action: "graphql_get",
		// Source is missing
		As: "result",
		Input: map[string]string{
			"path": "user.name",
		},
	}

	err := c.compileGraphQLGet(step)
	if err == nil {
		t.Fatalf("expected error for missing source, got nil")
	}
}

func TestCompileParseGraphQLError(t *testing.T) {
	// Test: Compiling parse_graphql_error step
	c := NewCompiler(nil)

	c.slotMap = map[string]int{
		"errors":  0,
		"message": 1,
	}
	c.nextSlot = 2

	step := StepConfig{
		Action: "parse_graphql_error",
		Source: "errors",
		As:     "message",
	}

	err := c.compileParseGraphQLError(step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(c.GlobalTable) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(c.GlobalTable))
	}

	if c.GlobalTable[0].Name != "PARSE_GRAPHQL_ERROR" {
		t.Fatalf("expected PARSE_GRAPHQL_ERROR, got %s", c.GlobalTable[0].Name)
	}
}

func TestGraphQLFlowEndToEnd(t *testing.T) {
	// Test: Full flow with graphql_call + graphql_get
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{"user":{"id":"123","name":"Alice","email":"alice@example.com"}}}`))
	}))
	defer ts.Close()

	// Create a simple flow with graphql_call and graphql_get
	c := NewCompiler(nil)
	c.slotMap = map[string]int{
		"response_data": 0,
		"user_name":     1,
		"user_email":    2,
	}
	c.nextSlot = 3

	// Compile graphql_call
	callStep := StepConfig{
		Action: "graphql_call",
		Input: map[string]string{
			"url":       ts.URL,
			"query":     "query { user { id name email } }",
			"data_slot": "response_data",
		},
	}

	err := c.compileGraphQLCall(callStep)
	if err != nil {
		t.Fatalf("compileGraphQLCall error: %v", err)
	}

	// Compile graphql_get for user.name
	getNameStep := StepConfig{
		Action: "graphql_get",
		Source: "response_data",
		As:     "user_name",
		Input: map[string]string{
			"path": "user.name",
		},
	}

	err = c.compileGraphQLGet(getNameStep)
	if err != nil {
		t.Fatalf("compileGraphQLGet error: %v", err)
	}

	// Compile graphql_get for user.email
	getEmailStep := StepConfig{
		Action: "graphql_get",
		Source: "response_data",
		As:     "user_email",
		Input: map[string]string{
			"path": "user.email",
		},
	}

	err = c.compileGraphQLGet(getEmailStep)
	if err != nil {
		t.Fatalf("compileGraphQLGet error: %v", err)
	}

	if len(c.GlobalTable) != 3 {
		t.Fatalf("expected 3 instructions, got %d", len(c.GlobalTable))
	}

	// Now execute the flow
	req, _ := http.NewRequest("GET", ts.URL, nil)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
		Request:   req,
	}

	state := &engine.ExecutionState{PC: 0}

	// Execute graphql_call
	pc := c.GlobalTable[0].Action(ctx, state)
	state.PC = pc

	if pc != 1 {
		t.Errorf("graphql_call: expected PC 1, got %d", pc)
	}
	if len(ctx.ByteSlots[0]) == 0 {
		t.Errorf("graphql_call: expected response_data to be populated")
	}

	// Execute graphql_get for user.name
	pc = c.GlobalTable[1].Action(ctx, state)
	state.PC = pc
	if pc != 2 {
		t.Errorf("graphql_get name: expected PC 2, got %d", pc)
	}
	if string(ctx.ByteSlots[1]) != `"Alice"` {
		t.Errorf("graphql_get name: expected %q, got %q", `"Alice"`, string(ctx.ByteSlots[1]))
	}

	// Execute graphql_get for user.email
	pc = c.GlobalTable[2].Action(ctx, state)
	state.PC = pc
	if pc != 3 {
		t.Errorf("graphql_get email: expected PC 3, got %d", pc)
	}
	if string(ctx.ByteSlots[2]) != `"alice@example.com"` {
		t.Errorf("graphql_get email: expected %q, got %q", `"alice@example.com"`, string(ctx.ByteSlots[2]))
	}
}
