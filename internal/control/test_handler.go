package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/cache"
	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// â”€â”€ Types â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestAssertion is one check applied to the execution result.
type TestAssertion struct {
	Type     string `json:"type"`          // "status" | "body_contains" | "latency_ms" | "step_executed" | "side_effect"
	Expected any    `json:"expected"`      // value depends on Type
	Path     string `json:"path,omitempty"` // JSONPath for body_contains (e.g. "$.user.id")
	Max      int    `json:"max,omitempty"` // for latency_ms: max allowed ms
	Step     string `json:"step,omitempty"` // for step_executed: step name
	Key      string `json:"key,omitempty"` // for side_effect: "cache_writes" | "registry_writes_suppressed"
}

// TestCase is a customer-defined API test case stored in the DB.
type TestCase struct {
	ID            string          `json:"id"`
	EnvironmentID string          `json:"environment_id,omitempty"`
	APIName       string          `json:"api_name"`
	Name          string          `json:"name"`
	FlowName      string          `json:"flow_name"`  // which flow to execute
	CallMode      string          `json:"call_mode"`  // "mock" | "real" | "schema_only"
	Input         TestInput       `json:"input"`
	Mocks         []StepMock      `json:"mocks,omitempty"`
	Assertions    []TestAssertion `json:"assertions"`
	CreatedAt     int64           `json:"created_at"`
}

// TestInput is the synthetic HTTP request passed to the flow.
type TestInput struct {
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
	TenantKey string            `json:"tenant_key,omitempty"`
}

// StepMock records a canned response for one named step (used when CallMode="mock").
type StepMock struct {
	StepName string `json:"step_name"`
	Status   int    `json:"status,omitempty"`
	Body     string `json:"body,omitempty"`
}

// StepTrace is the per-step timing entry returned to the caller.
type StepTrace struct {
	Name       string `json:"name"`
	Type       string `json:"type"`                 // "internal" | "external"
	DurationNs int64  `json:"duration_ns"`
	Suppressed bool   `json:"suppressed,omitempty"` // true if write was suppressed
}

// SideEffects records what the flow did (or would have done) to shared state.
type SideEffects struct {
	CacheWrites               int `json:"cache_writes"`
	CacheReads                int `json:"cache_reads"`
	RegistryWritesSuppressed  int `json:"registry_writes_suppressed"`
	DatastoreWritesSuppressed int `json:"datastore_writes_suppressed"`
}

// AssertionResult is the outcome of one assertion.
type AssertionResult struct {
	Type     string `json:"type"`
	Passed   bool   `json:"passed"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Message  string `json:"message,omitempty"`
}

// TestExecuteRequest is the body of POST /test/execute.
type TestExecuteRequest struct {
	FlowName   string          `json:"flow_name"`
	Mode       string          `json:"mode"`      // "draft" | "published"
	CallMode   string          `json:"call_mode"` // "mock" | "real" | "schema_only"
	Input      TestInput       `json:"input"`
	Mocks      []StepMock      `json:"mocks,omitempty"`
	Assertions []TestAssertion `json:"assertions,omitempty"`
}

// TestExecuteResponse is returned by POST /test/execute.
type TestExecuteResponse struct {
	Passed        bool              `json:"passed"`
	DurationMs    float64           `json:"duration_ms"`
	RahOverheadMs float64           `json:"rah_overhead_ms"`
	ExternalMs    float64           `json:"external_ms"`
	Response      TestHTTPResponse  `json:"response"`
	Steps         []StepTrace       `json:"steps"`
	Assertions    []AssertionResult `json:"assertions"`
	SideEffects   SideEffects       `json:"side_effects"`
}

// TestHTTPResponse captures what the flow produced.
type TestHTTPResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// TestRun is a stored execution result.
type TestRun struct {
	ID          string              `json:"id"`
	TestCaseID  string              `json:"test_case_id,omitempty"`
	TriggeredBy string              `json:"triggered_by"` // "manual" | "pre_deploy" | "post_deploy"
	StartedAt   int64               `json:"started_at"`
	DurationMs  float64             `json:"duration_ms"`
	Passed      bool                `json:"passed"`
	Result      TestExecuteResponse `json:"result"`
}

// â”€â”€ Mock ResponseWriter â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// mockResponseWriter satisfies rctx.ResponseWriter without touching the network.
type mockResponseWriter struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func newMockResponseWriter() *mockResponseWriter {
	return &mockResponseWriter{
		header: make(http.Header),
		status: http.StatusOK,
	}
}

func (m *mockResponseWriter) Header() http.Header {
	return m.header
}

func (m *mockResponseWriter) WriteHeader(statusCode int) {
	m.status = statusCode
}

func (m *mockResponseWriter) Write(p []byte) (int, error) {
	return m.buf.Write(p)
}

// â”€â”€ TestHandler â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// TestHandler provides HTTP endpoints for flow test execution, test case
// management, and test run history.
type TestHandler struct {
	dsm      *DataStoreManager
	ms       *ManagementServer
	fm       *engine.FlowManager
	cacheMgr *cache.CacheManager
}

// NewTestHandler creates a TestHandler backed by the given managers.
// cacheMgr may be nil when the cache is not configured; test tenant cache data
// will not be purged after a single-execute run in that case.
func NewTestHandler(dsm *DataStoreManager, ms *ManagementServer, fm *engine.FlowManager, cacheMgr *cache.CacheManager) *TestHandler {
	return &TestHandler{dsm: dsm, ms: ms, fm: fm, cacheMgr: cacheMgr}
}

// RegisterTestRoutes registers all /test/* endpoints onto mux.
// cacheMgr may be nil when the cache is not configured; suite runs will still
// work but tenant cache data will not be purged after the run.
func RegisterTestRoutes(mux *http.ServeMux, dsm *DataStoreManager, ms *ManagementServer, fm *engine.FlowManager, cacheMgr *cache.CacheManager) {
	h := NewTestHandler(dsm, ms, fm, cacheMgr)
	mux.HandleFunc("/test/execute", h.ExecuteHandler)
	mux.HandleFunc("/test/load/run", h.LoadTestHandler)
	mux.HandleFunc("/test/cases", h.CasesHandler)
	mux.HandleFunc("/test/cases/", h.CaseHandler) // handles /test/cases/{id}
	mux.HandleFunc("/test/runs", h.RunsHandler)

	// Suite endpoints â€” require RegistryManager for test tenant isolation.
	regMgr := ms.RegMgr
	mux.HandleFunc("/test/suites", h.SuitesHandler)
	mux.HandleFunc("/test/suites/", func(w http.ResponseWriter, r *http.Request) {
		h.SuiteHandler(w, r, regMgr, cacheMgr)
	})
}

// â”€â”€ ExecuteHandler â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// ExecuteHandler handles POST /test/execute.
// It runs a named flow against a synthetic request and returns the execution
// result together with assertion outcomes.
func (h *TestHandler) ExecuteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req TestExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.FlowName == "" {
		writeError(w, http.StatusBadRequest, "flow_name is required")
		return
	}

	// Resolve instruction table.
	table, err := h.resolveTable(req.Mode, req.FlowName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if table == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("flow %q not found", req.FlowName))
		return
	}

	// Allocate an ephemeral test tenant for isolation.
	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	var testTenantID uint16
	if h.ms != nil && h.ms.RegMgr != nil {
		alias, tID, err := h.ms.RegMgr.RegisterTestTenant(runID)
		if err == nil {
			testTenantID = tID
			_ = alias
			defer func() {
				_ = h.ms.RegMgr.DeleteTestTenant(runID)
				if h.cacheMgr != nil {
					_ = h.cacheMgr.DeleteTenant(testTenantID)
				}
			}()
		}
	}

	// Build a synthetic context â€” not from the pool; test contexts are short-lived.
	ctx := &rctx.Context{}
	ctx.InitSlots()
	ctx.TestMode = true
	ctx.TestRunID = runID
	if testTenantID != 0 {
		ctx.TenantID = testTenantID
	}

	method := req.Input.Method
	if method == "" {
		method = http.MethodGet
	}
	ctx.Method = []byte(method)
	ctx.Path = []byte(req.Input.Path)
	ctx.TenantKey = req.Input.TenantKey

	// Attach a mock ResponseWriter so the flow can write a response.
	mock := newMockResponseWriter()
	ctx.Writer = mock

	// Populate the request body if provided.
	if req.Input.Body != "" {
		ctx.RequestBuffer = []byte(req.Input.Body)
		ctx.IsBuffered = true
	}

	// Execute the flow and measure wall-clock time.
	startTime := time.Now()
	engine.Execute(ctx, table, 0)
	durationMs := time.Since(startTime).Seconds() * 1000

	// Capture the response body: prefer the context ResponseBuffer (if the
	// flow used buffered mode), otherwise fall back to what the mock captured.
	responseBody := ""
	if len(ctx.ResponseBuffer) > 0 {
		responseBody = string(ctx.ResponseBuffer)
	} else if mock.buf.Len() > 0 {
		responseBody = mock.buf.String()
	}

	// Determine the effective HTTP status.
	responseStatus := ctx.ResponseStatus
	if responseStatus == 0 {
		if mock.status != 0 {
			responseStatus = mock.status
		} else {
			responseStatus = http.StatusOK
		}
	}

	// Evaluate assertions.
	assertResults := evaluateAssertions(req.Assertions, responseStatus, responseBody, durationMs)

	// Compute overall pass/fail.
	passed := true
	for _, ar := range assertResults {
		if !ar.Passed {
			passed = false
			break
		}
	}

	resp := TestExecuteResponse{
		Passed:      passed,
		DurationMs:  durationMs,
		RahOverheadMs: durationMs, // no upstream breakdown without per-step timing
		ExternalMs:  0,
		Response: TestHTTPResponse{
			Status: responseStatus,
			Body:   responseBody,
		},
		Steps:       []StepTrace{},
		Assertions:  assertResults,
		SideEffects: SideEffects{},
	}

	// Optionally persist the run.
	if h.dsm != nil && h.dsm.IsConfigured(config.DomainTestRuns) {
		run := TestRun{
			ID:          fmt.Sprintf("run-%d", time.Now().UnixNano()),
			TriggeredBy: "manual",
			StartedAt:   time.Now().UnixNano(),
			DurationMs:  durationMs,
			Passed:      passed,
			Result:      resp,
		}
		if data, err := json.Marshal(run); err == nil {
			_ = h.dsm.PutGlobal(context.Background(), config.DomainTestRuns, run.ID, data)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// resolveTable returns the instruction table for the given mode and flow name.
// Returns (nil, nil) when the flow is not found (caller should 404).
// Returns (nil, error) when there is a configuration problem (caller should 400).
func (h *TestHandler) resolveTable(mode, flowName string) ([]engine.Instruction, error) {
	var state *engine.EngineState

	if mode == "draft" {
		state = h.fm.DraftState.Load()
		if state == nil {
			return nil, fmt.Errorf("no draft loaded")
		}
	} else {
		// "published" or empty â†’ use live state.
		state = h.fm.State.Load()
		if state == nil {
			return nil, fmt.Errorf("no published state loaded")
		}
	}

	table, ok := state.FlowLibrary[flowName]
	if !ok {
		return nil, nil
	}
	return table, nil
}

// evaluateAssertions runs each assertion against the execution result.
func evaluateAssertions(assertions []TestAssertion, status int, body string, durationMs float64) []AssertionResult {
	results := make([]AssertionResult, 0, len(assertions))
	for _, a := range assertions {
		results = append(results, evaluateAssertion(a, status, body, durationMs))
	}
	return results
}

func evaluateAssertion(a TestAssertion, status int, body string, durationMs float64) AssertionResult {
	ar := AssertionResult{
		Type:     a.Type,
		Expected: a.Expected,
	}

	switch a.Type {
	case "status":
		var expected int
		switch v := a.Expected.(type) {
		case float64:
			expected = int(v)
		case int:
			expected = v
		}
		ar.Actual = status
		ar.Passed = status == expected

	case "body_contains":
		needle, _ := a.Expected.(string)
		ar.Actual = fmt.Sprintf("body(%d bytes)", len(body))
		ar.Passed = strings.Contains(body, needle)
		if !ar.Passed {
			ar.Message = fmt.Sprintf("body does not contain %q", needle)
		}

	case "latency_ms":
		ar.Actual = durationMs
		ar.Passed = durationMs <= float64(a.Max)
		if !ar.Passed {
			ar.Message = fmt.Sprintf("%.2f ms exceeds max %d ms", durationMs, a.Max)
		}

	case "step_executed":
		// Step-level tracing requires per-instruction observability wiring.
		// Marked as not evaluated until that integration is complete.
		ar.Passed = true
		ar.Message = "not_evaluated: step tracing requires observability wiring"

	case "side_effect":
		// Side-effect counters are populated only when steps explicitly
		// report them; no infrastructure for this yet.
		ar.Passed = true
		ar.Message = "not_evaluated: side-effect tracking not yet wired"

	default:
		ar.Passed = false
		ar.Message = fmt.Sprintf("unknown assertion type %q", a.Type)
	}

	return ar
}

// â”€â”€ CasesHandler â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// CasesHandler handles GET /test/cases and POST /test/cases.
func (h *TestHandler) CasesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listCases(w, r)
	case http.MethodPost:
		h.createCase(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *TestHandler) listCases(w http.ResponseWriter, r *http.Request) {
	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainTestCases) {
		writeJSON(w, http.StatusOK, []TestCase{})
		return
	}

	keys, err := h.dsm.ListGlobalKeys(r.Context(), config.DomainTestCases, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list test cases: "+err.Error())
		return
	}

	cases := make([]TestCase, 0, len(keys))
	for _, k := range keys {
		data, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainTestCases, k)
		if err != nil || !ok {
			continue
		}
		var tc TestCase
		if err := json.Unmarshal(data, &tc); err == nil {
			cases = append(cases, tc)
		}
	}

	writeJSON(w, http.StatusOK, cases)
}

func (h *TestHandler) createCase(w http.ResponseWriter, r *http.Request) {
	var tc TestCase
	if err := json.NewDecoder(r.Body).Decode(&tc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	tc.ID = fmt.Sprintf("tc-%d", time.Now().UnixNano())
	tc.CreatedAt = time.Now().Unix()

	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainTestCases) {
		writeError(w, http.StatusServiceUnavailable, "test_cases domain not configured")
		return
	}

	data, err := json.Marshal(tc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to marshal test case")
		return
	}

	if err := h.dsm.PutGlobal(context.Background(), config.DomainTestCases, tc.ID, data); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store test case: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, tc)
}

// â”€â”€ CaseHandler â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// CaseHandler handles GET /test/cases/{id} and DELETE /test/cases/{id}.
func (h *TestHandler) CaseHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/test/cases/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing test case id")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getCase(w, r, id)
	case http.MethodDelete:
		h.deleteCase(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *TestHandler) getCase(w http.ResponseWriter, r *http.Request, id string) {
	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainTestCases) {
		writeError(w, http.StatusServiceUnavailable, "test_cases domain not configured")
		return
	}

	data, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainTestCases, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage error: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "test case not found")
		return
	}

	var tc TestCase
	if err := json.Unmarshal(data, &tc); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decode test case")
		return
	}

	writeJSON(w, http.StatusOK, tc)
}

func (h *TestHandler) deleteCase(w http.ResponseWriter, r *http.Request, id string) {
	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainTestCases) {
		writeError(w, http.StatusServiceUnavailable, "test_cases domain not configured")
		return
	}

	if err := h.dsm.DeleteGlobal(r.Context(), config.DomainTestCases, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete test case: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// â”€â”€ RunsHandler â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// RunsHandler handles GET /test/runs.
func (h *TestHandler) RunsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainTestRuns) {
		writeJSON(w, http.StatusOK, []TestRun{})
		return
	}

	keys, err := h.dsm.ListGlobalKeys(r.Context(), config.DomainTestRuns, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list test runs: "+err.Error())
		return
	}

	// Return at most 50 most-recent runs.
	const maxRuns = 50
	if len(keys) > maxRuns {
		keys = keys[len(keys)-maxRuns:]
	}

	runs := make([]TestRun, 0, len(keys))
	for _, k := range keys {
		data, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainTestRuns, k)
		if err != nil || !ok {
			continue
		}
		var run TestRun
		if err := json.Unmarshal(data, &run); err == nil {
			runs = append(runs, run)
		}
	}

	writeJSON(w, http.StatusOK, runs)
}
