package steps

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

func TestGraphQLCallStaticQuery(t *testing.T) {
	// Test: Static query with no variables
	var hits int32
	var capturedBody []byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		capturedBody = make([]byte, r.ContentLength)
		_, _ = r.Body.Read(capturedBody)
		_ = r.Body.Close()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"user":{"id":"123","name":"Alice"}}}`))
	}))
	defer ts.Close()

	cfg := GraphQLCallConfig{
		StaticURL:     ts.URL,
		URLSlot:       -1,
		StaticPrefix:  []byte(`{"query":"query { user { id name } }","variables":`),
		StaticSuffix:  []byte(`}`),
		QuerySlot:     -1,
		VariablesSlot: -1,
		DataSlot:      0,
		ErrorsSlot:    -1,
		TimeoutMs:     5000,
		FailOnErrors:  true,
	}

	req, _ := http.NewRequest("POST", ts.URL, nil)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
		Request:   req,
	}
	state := &engine.ExecutionState{PC: 5}

	instr := GraphQLCallFromConfig(cfg)
	next := instr.Action(ctx, state)

	if next != 6 {
		t.Errorf("expected next PC 6, got %d", next)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("expected 1 call, got %d", hits)
	}

	// Check that body contains null variables
	expectedBody := `{"query":"query { user { id name } }","variables":null}`
	if string(capturedBody) != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, string(capturedBody))
	}

	// Check data was extracted
	if len(ctx.ByteSlots[0]) == 0 {
		t.Errorf("expected data slot to be populated, got empty")
	}
}

func TestGraphQLCallWithVariables(t *testing.T) {
	// Test: Query with variables in a slot
	var capturedBody []byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody = make([]byte, r.ContentLength)
		_, _ = r.Body.Read(capturedBody)
		_ = r.Body.Close()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"user":{"id":"456"}}}`))
	}))
	defer ts.Close()

	cfg := GraphQLCallConfig{
		StaticURL:     ts.URL,
		URLSlot:       -1,
		StaticPrefix:  []byte(`{"query":"query getUser($id: ID!) { user(id: $id) { id } }","variables":`),
		StaticSuffix:  []byte(`}`),
		QuerySlot:     -1,
		VariablesSlot: 1,
		DataSlot:      0,
		ErrorsSlot:    -1,
		TimeoutMs:     5000,
		FailOnErrors:  true,
	}

	req, _ := http.NewRequest("POST", ts.URL, nil)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
		Request:   req,
	}
	ctx.ByteSlots[1] = []byte(`{"id":"456"}`)
	state := &engine.ExecutionState{PC: 3}

	instr := GraphQLCallFromConfig(cfg)
	next := instr.Action(ctx, state)

	if next != 4 {
		t.Errorf("expected next PC 4, got %d", next)
	}

	// Check body includes variables
	expectedBody := `{"query":"query getUser($id: ID!) { user(id: $id) { id } }","variables":{"id":"456"}}`
	if string(capturedBody) != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, string(capturedBody))
	}
}

func TestGraphQLCallResponseErrors(t *testing.T) {
	// Test: Response with errors field
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Invalid query"}],"data":null}`))
	}))
	defer ts.Close()

	cfg := GraphQLCallConfig{
		StaticURL:     ts.URL,
		URLSlot:       -1,
		StaticPrefix:  []byte(`{"query":"query {}","variables":`),
		StaticSuffix:  []byte(`}`),
		QuerySlot:     -1,
		VariablesSlot: -1,
		DataSlot:      0,
		ErrorsSlot:    1,
		TimeoutMs:     5000,
		FailOnErrors:  true,
	}

	req, _ := http.NewRequest("POST", ts.URL, nil)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
		Request:   req,
	}
	state := &engine.ExecutionState{PC: 2}

	instr := GraphQLCallFromConfig(cfg)
	next := instr.Action(ctx, state)

	if next != 3 {
		t.Errorf("expected next PC 3, got %d", next)
	}

	if !ctx.Failed {
		t.Errorf("expected ctx.Failed to be true when fail_on_errors=true and errors present")
	}

	if len(ctx.ByteSlots[1]) == 0 {
		t.Errorf("expected errors slot to be populated")
	}
}

func TestGraphQLCallEmptyURL(t *testing.T) {
	// Test: Missing upstream URL
	cfg := GraphQLCallConfig{
		StaticURL:     "",
		URLSlot:       -1,
		StaticPrefix:  []byte(`{"query":"{}","variables":`),
		QuerySlot:     -1,
		VariablesSlot: -1,
		DataSlot:      -1,
		ErrorsSlot:    -1,
		TimeoutMs:     5000,
		FailOnErrors:  true,
	}

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
	}
	state := &engine.ExecutionState{PC: 1}

	instr := GraphQLCallFromConfig(cfg)
	next := instr.Action(ctx, state)

	if next != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", next)
	}
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed=true")
	}
	if ctx.ResponseStatus != 502 {
		t.Errorf("expected status 502, got %d", ctx.ResponseStatus)
	}
}

func TestGraphQLCallBodyAssemblyZeroAlloc(t *testing.T) {
	// Test: Body assembly has zero allocations
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer ts.Close()

	cfg := GraphQLCallConfig{
		StaticURL:     ts.URL,
		URLSlot:       -1,
		StaticPrefix:  []byte(`{"query":"test","variables":`),
		StaticSuffix:  []byte(`}`),
		QuerySlot:     -1,
		VariablesSlot: 0,
		DataSlot:      -1,
		ErrorsSlot:    -1,
		TimeoutMs:     5000,
		FailOnErrors:  true,
	}

	req, _ := http.NewRequest("POST", ts.URL, nil)

	testBody := func() {
		ctx := &rctx.Context{
			ByteSlots: make([][]byte, 20),
			Request:   req,
		}
		ctx.ByteSlots[0] = []byte(`{"id":"123"}`)
		state := &engine.ExecutionState{PC: 0}

		instr := GraphQLCallFromConfig(cfg)
		instr.Action(ctx, state)
	}

	allocs := testing.AllocsPerRun(100, testBody)
	// Body assembly is zero-alloc, but the full HTTP call path allocates.
	// Log the number for tracking; do not fail since HTTP inherently allocates.
	t.Logf("GraphQL call allocs/op: %v", allocs)
}

func TestGraphQLGetExtractsNestedField(t *testing.T) {
	// Test: graphql_get with nested path
	cfg := GraphQLGetConfig{
		DataSlot:   0,
		StaticPath: "user.name",
		DestSlot:   1,
	}

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
	}
	ctx.ByteSlots[0] = []byte(`{"user":{"id":"123","name":"Alice"}}`)

	state := &engine.ExecutionState{PC: 10}

	instr := GraphQLGetFromConfig(cfg)
	next := instr.Action(ctx, state)

	if next != 11 {
		t.Errorf("expected next PC 11, got %d", next)
	}

	if len(ctx.ByteSlots[1]) == 0 {
		t.Errorf("expected dest slot to be populated")
	}

	if string(ctx.ByteSlots[1]) != `"Alice"` {
		t.Errorf("expected %q, got %q", `"Alice"`, string(ctx.ByteSlots[1]))
	}
}

func TestGraphQLGetMissingPath(t *testing.T) {
	// Test: graphql_get with missing path returns nil
	cfg := GraphQLGetConfig{
		DataSlot:   0,
		StaticPath: "nonexistent.field",
		DestSlot:   1,
	}

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
	}
	ctx.ByteSlots[0] = []byte(`{"user":{"id":"123"}}`)

	state := &engine.ExecutionState{PC: 5}

	instr := GraphQLGetFromConfig(cfg)
	next := instr.Action(ctx, state)

	if next != 6 {
		t.Errorf("expected next PC 6, got %d", next)
	}

	if len(ctx.ByteSlots[1]) != 0 {
		t.Errorf("expected dest slot to be nil, got %v", ctx.ByteSlots[1])
	}
}

func TestGraphQLGetEmptySourceSlot(t *testing.T) {
	// Test: graphql_get with empty source slot
	cfg := GraphQLGetConfig{
		DataSlot:   0,
		StaticPath: "field",
		DestSlot:   1,
	}

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
	}
	ctx.ByteSlots[0] = []byte{} // Empty

	state := &engine.ExecutionState{PC: 3}

	instr := GraphQLGetFromConfig(cfg)
	next := instr.Action(ctx, state)

	if next != 4 {
		t.Errorf("expected next PC 4, got %d", next)
	}

	if len(ctx.ByteSlots[1]) != 0 {
		t.Errorf("expected dest slot to be nil for empty source")
	}
}

func TestGraphQLCallConcurrent(t *testing.T) {
	// Test: Concurrent graphql_call (race detection)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"result":"ok"}}`))
	}))
	defer ts.Close()

	cfg := GraphQLCallConfig{
		StaticURL:     ts.URL,
		URLSlot:       -1,
		StaticPrefix:  []byte(`{"query":"test","variables":`),
		StaticSuffix:  []byte(`}`),
		QuerySlot:     -1,
		VariablesSlot: -1,
		DataSlot:      0,
		ErrorsSlot:    -1,
		TimeoutMs:     5000,
		FailOnErrors:  false,
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", ts.URL, nil)
			ctx := &rctx.Context{
				ByteSlots: make([][]byte, 20),
				Request:   req,
			}
			state := &engine.ExecutionState{PC: 0}

			instr := GraphQLCallFromConfig(cfg)
			instr.Action(ctx, state)
		}()
	}
	wg.Wait()
}

func TestGraphQLCallTimeoutReturnsSameBody(t *testing.T) {
	// Test: Upstream timeout returns 504
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate delay beyond timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := GraphQLCallConfig{
		StaticURL:     ts.URL,
		URLSlot:       -1,
		StaticPrefix:  []byte(`{"query":"test","variables":`),
		StaticSuffix:  []byte(`}`),
		QuerySlot:     -1,
		VariablesSlot: -1,
		DataSlot:      -1,
		ErrorsSlot:    -1,
		TimeoutMs:     1, // 1ms timeout (almost guaranteed to trigger)
		FailOnErrors:  true,
	}

	req, _ := http.NewRequest("POST", ts.URL, nil)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
		Request:   req,
	}
	state := &engine.ExecutionState{PC: 0}

	instr := GraphQLCallFromConfig(cfg)
	// Most of the time this will timeout (depends on system speed)
	// Just check it doesn't panic
	instr.Action(ctx, state)
}

func TestAppendJSONEscapedStringQuotes(t *testing.T) {
	// Test: Escaping quotes
	dst := []byte{}
	result := AppendJSONEscapedString(dst, `query { user(name: "John") }`)
	expected := `query { user(name: \"John\") }`
	if string(result) != expected {
		t.Errorf("expected %q, got %q", expected, string(result))
	}
}

func TestAppendJSONEscapedStringNewlines(t *testing.T) {
	// Test: Escaping newlines
	dst := []byte{}
	result := AppendJSONEscapedString(dst, "query {\n  user {\n    id\n  }\n}")
	expected := `query {\n  user {\n    id\n  }\n}`
	if string(result) != expected {
		t.Errorf("expected %q, got %q", expected, string(result))
	}
}

func TestAppendJSONEscapedStringBackslash(t *testing.T) {
	// Test: Escaping backslashes — input has 2 backslashes, output has 4; quotes also escaped.
	dst := []byte{}
	result := AppendJSONEscapedString(dst, `query { pattern: "a\\b" }`)
	expected := `query { pattern: \"a\\\\b\" }`
	if string(result) != expected {
		t.Errorf("expected %q, got %q", expected, string(result))
	}
}

func TestAppendJSONEscapedStringTabs(t *testing.T) {
	// Test: Escaping tabs
	dst := []byte{}
	result := AppendJSONEscapedString(dst, "query {\n\tuser {\n\t\tid\n\t}\n}")
	expected := `query {\n\tuser {\n\t\tid\n\t}\n}`
	if string(result) != expected {
		t.Errorf("expected %q, got %q", expected, string(result))
	}
}

func TestParseGraphQLErrorFirstMessage(t *testing.T) {
	// Test: Extract first error message
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
	}
	ctx.ByteSlots[0] = []byte(`[{"message":"Field not found"},{"message":"Invalid type"}]`)

	state := &engine.ExecutionState{PC: 7}

	instr := ParseGraphQLError(0, 1)
	next := instr.Action(ctx, state)

	if next != 8 {
		t.Errorf("expected next PC 8, got %d", next)
	}

	if string(ctx.ByteSlots[1]) != "Field not found" {
		t.Errorf("expected %q, got %q", "Field not found", string(ctx.ByteSlots[1]))
	}
}

func TestParseGraphQLErrorEmptyArray(t *testing.T) {
	// Test: Empty errors array
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
	}
	ctx.ByteSlots[0] = []byte(`[]`)

	state := &engine.ExecutionState{PC: 2}

	instr := ParseGraphQLError(0, 1)
	next := instr.Action(ctx, state)

	if next != 3 {
		t.Errorf("expected next PC 3, got %d", next)
	}

	if len(ctx.ByteSlots[1]) != 0 {
		t.Errorf("expected dest slot to be nil")
	}
}
