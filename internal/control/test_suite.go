package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"rah/internal/cache"
	"rah/internal/config"
	"rah/internal/engine"
	registrypkg "rah/internal/registry"
	"rah/internal/rctx"
)

// ── Types ─────────────────────────────────────────────────────────────────────

// TestSuite is a collection of test cases run in sequence sharing one test tenant.
type TestSuite struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	EnvironmentID string   `json:"environment_id,omitempty"`
	CaseIDs       []string `json:"case_ids"`      // ordered list of TestCase IDs to run
	Mode          string   `json:"mode"`          // "draft" | "published"
	CreatedAt     int64    `json:"created_at"`
}

// SuiteRunResult is returned by POST /test/suites/{id}/run.
type SuiteRunResult struct {
	SuiteID         string            `json:"suite_id"`
	RunID           string            `json:"run_id"`
	StartedAt       int64             `json:"started_at"`
	DurationMs      float64           `json:"duration_ms"`
	Passed          bool              `json:"passed"`
	TotalCases      int               `json:"total_cases"`
	PassedCases     int               `json:"passed_cases"`
	Results         []SuiteCaseResult `json:"results"`
	TestTenantAlias string            `json:"test_tenant_alias,omitempty"`
}

// SuiteCaseResult is one test case's outcome within a suite run.
type SuiteCaseResult struct {
	CaseID     string            `json:"case_id"`
	CaseName   string            `json:"case_name"`
	Passed     bool              `json:"passed"`
	DurationMs float64           `json:"duration_ms"`
	Response   TestHTTPResponse  `json:"response"`
	Assertions []AssertionResult `json:"assertions"`
	Error      string            `json:"error,omitempty"`
}

// ── Suite Runner ──────────────────────────────────────────────────────────────

// runSuite executes all cases in the suite sequentially under a single isolated
// test tenant. The tenant is registered before the first case and cleaned up
// (via deferred calls) after the last case regardless of failures.
func (h *TestHandler) runSuite(ctx context.Context, suite TestSuite, regMgr *registrypkg.RegistryManager, cacheMgr *cache.CacheManager) (SuiteRunResult, error) {
	runID := fmt.Sprintf("suite-%d", time.Now().UnixNano())
	startedAt := time.Now()

	alias, testTenantID, err := regMgr.RegisterTestTenant(runID)
	if err != nil {
		return SuiteRunResult{}, fmt.Errorf("register test tenant: %w", err)
	}
	defer func() {
		_ = regMgr.DeleteTestTenant(runID)
		if cacheMgr != nil {
			_ = cacheMgr.DeleteTenant(testTenantID)
		}
	}()

	result := SuiteRunResult{
		SuiteID:         suite.ID,
		RunID:           runID,
		StartedAt:       startedAt.UnixNano(),
		Passed:          true,
		TotalCases:      len(suite.CaseIDs),
		Results:         make([]SuiteCaseResult, 0, len(suite.CaseIDs)),
		TestTenantAlias: alias,
	}

	for _, caseID := range suite.CaseIDs {
		// Load the TestCase from the datastore.
		if h.dsm == nil || !h.dsm.IsConfigured(config.DomainTestCases) {
			cr := SuiteCaseResult{
				CaseID: caseID,
				Passed: false,
				Error:  "test_cases domain not configured",
				Assertions: []AssertionResult{},
			}
			result.Results = append(result.Results, cr)
			result.Passed = false
			continue
		}

		data, ok, err := h.dsm.GetGlobal(ctx, config.DomainTestCases, caseID)
		if err != nil {
			cr := SuiteCaseResult{
				CaseID: caseID,
				Passed: false,
				Error:  fmt.Sprintf("failed to load test case: %v", err),
				Assertions: []AssertionResult{},
			}
			result.Results = append(result.Results, cr)
			result.Passed = false
			continue
		}
		if !ok {
			cr := SuiteCaseResult{
				CaseID: caseID,
				Passed: false,
				Error:  fmt.Sprintf("test case %q not found", caseID),
				Assertions: []AssertionResult{},
			}
			result.Results = append(result.Results, cr)
			result.Passed = false
			continue
		}

		var tc TestCase
		if err := json.Unmarshal(data, &tc); err != nil {
			cr := SuiteCaseResult{
				CaseID: caseID,
				Passed: false,
				Error:  fmt.Sprintf("failed to decode test case: %v", err),
				Assertions: []AssertionResult{},
			}
			result.Results = append(result.Results, cr)
			result.Passed = false
			continue
		}

		cr := h.executeOneCase(suite.Mode, tc, testTenantID)
		result.Results = append(result.Results, cr)
		if cr.Passed {
			result.PassedCases++
		} else {
			result.Passed = false
		}
	}

	result.DurationMs = time.Since(startedAt).Seconds() * 1000
	return result, nil
}

// executeOneCase runs a single TestCase under the given testTenantID and
// returns the outcome. It mirrors the logic in ExecuteHandler but uses the
// isolated test tenant rather than the request's tenant key.
func (h *TestHandler) executeOneCase(mode string, tc TestCase, testTenantID uint16) SuiteCaseResult {
	cr := SuiteCaseResult{
		CaseID:     tc.ID,
		CaseName:   tc.Name,
		Assertions: []AssertionResult{},
	}

	// Determine which flow to run: prefer tc.FlowName, fall back to tc.APIName.
	flowName := tc.FlowName
	if flowName == "" {
		flowName = tc.APIName
	}
	if flowName == "" {
		cr.Error = "test case has no flow_name"
		return cr
	}

	table, err := h.resolveTable(mode, flowName)
	if err != nil {
		cr.Error = err.Error()
		return cr
	}
	if table == nil {
		cr.Error = fmt.Sprintf("flow %q not found", flowName)
		return cr
	}

	// Build a synthetic context using the isolated test tenant.
	rctxCtx := &rctx.Context{}
	rctxCtx.InitSlots()
	rctxCtx.TestMode = true
	rctxCtx.TestRunID = fmt.Sprintf("%d", time.Now().UnixNano())
	rctxCtx.TenantID = testTenantID
	rctxCtx.TenantKey = tc.Input.TenantKey

	method := tc.Input.Method
	if method == "" {
		method = http.MethodGet
	}
	rctxCtx.Method = []byte(method)
	rctxCtx.Path = []byte(tc.Input.Path)

	// Attach a mock ResponseWriter so the flow can write a response.
	mock := newMockResponseWriter()
	rctxCtx.Writer = mock

	// Populate the request body if provided.
	if tc.Input.Body != "" {
		rctxCtx.RequestBuffer = []byte(tc.Input.Body)
		rctxCtx.IsBuffered = true
	}

	// Execute the flow and measure wall-clock time.
	startTime := time.Now()
	engine.Execute(rctxCtx, table, 0)
	durationMs := time.Since(startTime).Seconds() * 1000

	// Capture the response body.
	responseBody := ""
	if len(rctxCtx.ResponseBuffer) > 0 {
		responseBody = string(rctxCtx.ResponseBuffer)
	} else if mock.buf.Len() > 0 {
		responseBody = mock.buf.String()
	}

	// Determine the effective HTTP status.
	responseStatus := rctxCtx.ResponseStatus
	if responseStatus == 0 {
		if mock.status != 0 {
			responseStatus = mock.status
		} else {
			responseStatus = http.StatusOK
		}
	}

	assertResults := evaluateAssertions(tc.Assertions, responseStatus, responseBody, durationMs)

	passed := true
	for _, ar := range assertResults {
		if !ar.Passed {
			passed = false
			break
		}
	}

	cr.Passed = passed
	cr.DurationMs = durationMs
	cr.Response = TestHTTPResponse{Status: responseStatus, Body: responseBody}
	cr.Assertions = assertResults
	return cr
}

// ── HTTP Handlers ─────────────────────────────────────────────────────────────

// SuitesHandler handles GET /test/suites and POST /test/suites.
func (h *TestHandler) SuitesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listSuites(w, r)
	case http.MethodPost:
		h.createSuite(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *TestHandler) listSuites(w http.ResponseWriter, r *http.Request) {
	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainTestCases) {
		writeJSON(w, http.StatusOK, []TestSuite{})
		return
	}

	keys, err := h.dsm.ListGlobalKeys(r.Context(), config.DomainTestCases, "suite:")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list suites: "+err.Error())
		return
	}

	suites := make([]TestSuite, 0, len(keys))
	for _, k := range keys {
		data, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainTestCases, k)
		if err != nil || !ok {
			continue
		}
		var s TestSuite
		if err := json.Unmarshal(data, &s); err == nil {
			suites = append(suites, s)
		}
	}

	writeJSON(w, http.StatusOK, suites)
}

func (h *TestHandler) createSuite(w http.ResponseWriter, r *http.Request) {
	var s TestSuite
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	s.ID = fmt.Sprintf("suite-%d", time.Now().UnixNano())
	s.CreatedAt = time.Now().Unix()

	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainTestCases) {
		writeError(w, http.StatusServiceUnavailable, "test_cases domain not configured")
		return
	}

	data, err := json.Marshal(s)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to marshal suite")
		return
	}

	key := "suite:" + s.ID
	if err := h.dsm.PutGlobal(context.Background(), config.DomainTestCases, key, data); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store suite: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, s)
}

// SuiteHandler handles GET /test/suites/{id} and POST /test/suites/{id}/run.
func (h *TestHandler) SuiteHandler(w http.ResponseWriter, r *http.Request, regMgr *registrypkg.RegistryManager, cacheMgr *cache.CacheManager) {
	// Path is either /test/suites/{id} or /test/suites/{id}/run
	tail := strings.TrimPrefix(r.URL.Path, "/test/suites/")
	if tail == "" {
		writeError(w, http.StatusBadRequest, "missing suite id")
		return
	}

	if strings.HasSuffix(tail, "/run") {
		id := strings.TrimSuffix(tail, "/run")
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.runSuiteHandler(w, r, id, regMgr, cacheMgr)
		return
	}

	// Plain /test/suites/{id}
	id := tail
	switch r.Method {
	case http.MethodGet:
		h.getSuite(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *TestHandler) getSuite(w http.ResponseWriter, r *http.Request, id string) {
	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainTestCases) {
		writeError(w, http.StatusServiceUnavailable, "test_cases domain not configured")
		return
	}

	key := "suite:" + id
	data, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainTestCases, key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage error: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "suite not found")
		return
	}

	var s TestSuite
	if err := json.Unmarshal(data, &s); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decode suite")
		return
	}

	writeJSON(w, http.StatusOK, s)
}

func (h *TestHandler) runSuiteHandler(w http.ResponseWriter, r *http.Request, id string, regMgr *registrypkg.RegistryManager, cacheMgr *cache.CacheManager) {
	if h.dsm == nil || !h.dsm.IsConfigured(config.DomainTestCases) {
		writeError(w, http.StatusServiceUnavailable, "test_cases domain not configured")
		return
	}

	key := "suite:" + id
	data, ok, err := h.dsm.GetGlobal(r.Context(), config.DomainTestCases, key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage error: "+err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "suite not found")
		return
	}

	var suite TestSuite
	if err := json.Unmarshal(data, &suite); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decode suite")
		return
	}

	if regMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "registry manager not available")
		return
	}

	result, err := h.runSuite(r.Context(), suite, regMgr, cacheMgr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "suite run failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}
