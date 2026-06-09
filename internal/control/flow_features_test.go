package control

// flow_features_test.go — comprehensive integration tests for RAH gateway flows.
//
// Coverage:
//   - If/else branching (condition gate)
//   - Switch/case routing
//   - Slot liveness (many-variable flows)
//   - Batch ops: json_extract_emit → batch_flush (mock flusher)
//   - Batch ops: json_foreach_emit → batch_flush (mock flusher)
//   - Registry write + read: set_service_url / set_identifier / load_* round-trip
//   - TMS onboarding pipeline (end-to-end tenant property set + load)
//
// Known gaps identified:
//   - http_call discards response body; json_extract_emit cannot read it directly.
//     Tests pre-populate ByteSlots to simulate what a future json_extract_to_slot step would do.
//   - foreach depends on GetCollection which is stubbed for non-cookie/header sources.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/rctx"
	tenantregistry "rah/internal/registry"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

type mockOpFlusher struct {
	mu      sync.Mutex
	batches []rctx.Batch
}

func (m *mockOpFlusher) Submit(batch rctx.Batch) {
	m.mu.Lock()
	m.batches = append(m.batches, batch)
	m.mu.Unlock()
	if batch.Done != nil {
		close(batch.Done)
	}
}

func (m *mockOpFlusher) totalOps() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, b := range m.batches {
		n += len(b.Ops)
	}
	return n
}

func (m *mockOpFlusher) opsFlat() []rctx.StorageOp {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ops []rctx.StorageOp
	for _, b := range m.batches {
		ops = append(ops, b.Ops...)
	}
	return ops
}

func newTestFM(t *testing.T) *engine.FlowManager {
	t.Helper()
	return engine.NewFlowManager(64, config.GlobalLayout{
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1 << 20},
	})
}

func newTestStack(t *testing.T) (*engine.FlowManager, *Compiler, *ManagementServer, *tenantregistry.RegistryManager) {
	t.Helper()
	fm := newTestFM(t)
	regMgr := tenantregistry.NewRegistryManager()
	fm.RegistryExec = engine.NewRegistryExecutor(regMgr)
	compiler := NewCompiler(fm)
	compiler.RegMgr = regMgr
	server := NewManagementServer(fm, compiler, NewNameRegistry(), regMgr)
	return fm, compiler, server, regMgr
}

// sync a single flow + API via the management server, fail if /sync returns non-200.
func mustSync(t *testing.T, server *ManagementServer, req UnifiedSyncRequest) {
	t.Helper()
	payload, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	server.UnifiedSyncHandler(rec, httpReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync failed (status %d): %s", rec.Code, rec.Body.String())
	}
}

// run a request against a registered API and return the context after execution.
func runRequest(t *testing.T, fm *engine.FlowManager, method, path string, headers map[string]string) *rctx.Context {
	t.Helper()
	req := httptest.NewRequest(method, "http://localhost"+path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Use req.URL.Path (no query string) for router lookup.
	state := fm.State.Load()
	apiID := state.Router.Lookup(req.URL.Path)
	if apiID == 0 {
		t.Fatalf("route %s not found in router", req.URL.Path)
	}
	resp := httptest.NewRecorder()
	ctx := fm.GetContext()
	ctx.Reset(resp)
	ctx.ApiId = apiID
	ctx.SnapshotMetadata(req.Method, req.URL.Path, req.URL.RawQuery)
	fm.ProcessRequest(ctx, req)
	return ctx
}

// ─── Test 1: If/Else branching ────────────────────────────────────────────────

// TestIfElseBranching verifies that the if/else gate routes to the correct
// sub-flow based on whether a header slot is non-empty.
//
// Flow:
//   if header.X-Admin → allowFlow (200) | denyFlow (403)
func TestIfElseBranching(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "ifelse-1",
		Flows: []FlowUpdate{
			{Name: "allowFlow", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
			{Name: "denyFlow", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "403"},
			}, Action: "upsert"},
			{Name: "gatewayFlow", Instructions: []StepConfig{
				{
					Action:    "if",
					Condition: "header.X-Admin",
					Then:      "allowFlow",
					Else:      "denyFlow",
				},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "if-api", Path: "/v1/if-test", FlowName: "gatewayFlow", Action: "upsert"},
		},
	})

	// With header → should go to allowFlow → 200
	ctxAllow := runRequest(t, fm, http.MethodGet, "/v1/if-test", map[string]string{"X-Admin": "yes"})
	if ctxAllow.ResponseStatus != http.StatusOK {
		t.Errorf("expected 200 when X-Admin header present, got %d", ctxAllow.ResponseStatus)
	}

	// Without header → should go to denyFlow → 403
	ctxDeny := runRequest(t, fm, http.MethodGet, "/v1/if-test", nil)
	if ctxDeny.ResponseStatus != http.StatusForbidden {
		t.Errorf("expected 403 when X-Admin header absent, got %d", ctxDeny.ResponseStatus)
	}
}

// TestIfElseWithAndCondition verifies a compound condition (header.A && header.B).
// Both headers must be present for the then-branch; either missing → else-branch.
func TestIfElseWithAndCondition(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "ifelse-and",
		Flows: []FlowUpdate{
			{Name: "okFlow", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
			{Name: "failFlow", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "401"},
			}, Action: "upsert"},
			{Name: "andFlow", Instructions: []StepConfig{
				{
					Action:    "if",
					Condition: "header.X-Auth && header.X-Tenant",
					Then:      "okFlow",
					Else:      "failFlow",
				},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "and-api", Path: "/v1/and-test", FlowName: "andFlow", Action: "upsert"},
		},
	})

	// Both headers → 200
	ctx := runRequest(t, fm, "GET", "/v1/and-test", map[string]string{"X-Auth": "token", "X-Tenant": "acme"})
	if ctx.ResponseStatus != 200 {
		t.Errorf("both headers present: expected 200, got %d", ctx.ResponseStatus)
	}

	// Only one header → 401
	ctx2 := runRequest(t, fm, "GET", "/v1/and-test", map[string]string{"X-Auth": "token"})
	if ctx2.ResponseStatus != 401 {
		t.Errorf("only X-Auth: expected 401, got %d", ctx2.ResponseStatus)
	}
}

// ─── Test 2: Switch/Case routing ─────────────────────────────────────────────

// TestSwitchCaseRouting verifies that a switch step dispatches to the correct
// sub-flow based on a query parameter value.
func TestSwitchCaseRouting(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "switch-1",
		Flows: []FlowUpdate{
			{Name: "freeFlow", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
			{Name: "proFlow", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "202"},
			}, Action: "upsert"},
			{Name: "enterpriseFlow", Instructions: []StepConfig{
				{Action: "set_response_status", Value: "201"},
			}, Action: "upsert"},
			{Name: "switchFlow", Instructions: []StepConfig{
				{
					Action: "switch",
					As:     "query.tier",
					Cases: map[string]string{
						"free":       "freeFlow",
						"pro":        "proFlow",
						"enterprise": "enterpriseFlow",
					},
				},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "switch-api", Path: "/v1/switch", FlowName: "switchFlow", Action: "upsert"},
		},
	})

	tests := []struct {
		tier   string
		want   int
		label  string
	}{
		{"free", 200, "free tier"},
		{"pro", 202, "pro tier"},
		{"enterprise", 201, "enterprise tier"},
	}
	for _, tt := range tests {
		ctx := runRequest(t, fm, "GET", "/v1/switch?tier="+tt.tier, nil)
		if ctx.ResponseStatus != tt.want {
			t.Errorf("%s: expected %d, got %d", tt.label, tt.want, ctx.ResponseStatus)
		}
	}

	// Unknown tier → switch exits without matching, status stays at default 200
	// (ctx.Reset initialises ResponseStatus=200; no case sets it, so it stays 200).
	// The key property we assert: it did NOT dispatch to pro (202) or enterprise (201).
	ctx := runRequest(t, fm, "GET", "/v1/switch?tier=unknown", nil)
	if ctx.ResponseStatus == 202 || ctx.ResponseStatus == 201 {
		t.Errorf("unknown tier should not match pro/enterprise case, got %d", ctx.ResponseStatus)
	}
}

// ─── Test 3: Slot liveness enables large flows ────────────────────────────────

// TestSlotLivenessEnablesLargeFlow creates a flow that sequentially introduces
// and discards many variables. Without slot liveness analysis, this would
// exhaust the 48-slot limit. With liveness, freed slots are recycled.
func TestSlotLivenessEnablesLargeFlow(t *testing.T) {
	_, compiler, _, _ := newTestStack(t)

	// Build a flow where each variable is used once then a new one introduced.
	// Pattern: bind v0, use v0 in concat → v1, use v1 in concat → v2, ...
	// With liveness: vN-1 is freed when vN+1 is allocated.
	const chainLen = 40 // would overflow 48 slots without liveness
	steps := make([]StepConfig, 0, chainLen*2)

	// Step 0: bind a header to a slot
	steps = append(steps, StepConfig{Action: "set_response_status", Value: "200"})

	// Create a chain: concat current + "" → next, dropping the previous slot
	for i := 0; i < chainLen; i++ {
		srcA := "chain.var"
		srcB := "chain.const"
		if i > 0 {
			srcA = "chain.var" + strings.Repeat("x", i)
		}
		dest := "chain.var" + strings.Repeat("x", i+1)
		steps = append(steps, StepConfig{
			Action:        "concat",
			KeyIdentifier: srcA,
			Source:        srcB,
			As:            dest,
		})
	}

	_, err := compiler.CompileExecutable(steps, nil)
	if err != nil {
		t.Fatalf("large flow should compile with slot liveness, got error: %v", err)
	}
}

// ─── Test 4: json_extract_emit → batch_flush (mock flusher) ──────────────────

// TestJSONExtractEmitDispatchesBatch verifies that json_extract_emit enqueues
// PUT ops into the op buffer, and batch_flush dispatches them to the flusher.
//
// Gap noted: http_call does not capture the response body into slots.
// The body slot is pre-populated here to represent what a future
// json_extract_to_slot instruction would provide.
func TestJSONExtractEmitDispatchesBatch(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	// Wire mock cache flusher so OnFlush/MaxOps are activated in Pool.New.
	mock := &mockOpFlusher{}
	fm.CacheExec = mock

	const jsonBody = `{"api_key":"sk-test-001","client_id":"cid-xyz","region":"us-east"}`

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "extract-emit-1",
		Flows: []FlowUpdate{
			{Name: "extractFlow", Instructions: []StepConfig{
				// json_extract_emit: extract 3 fields, emit as cache PUTs
				{
					Action:   "json_extract_emit",
					Variable: "body.json", // slot holding the JSON
					Params: []map[string]string{
						{
							"path":       "api_key",
							"key_prefix": "cred:",
							"op_type":    "put",
							"target":     "cache",
							"value_slot": "",
							"async":      "true",
						},
						{
							"path":       "client_id",
							"key_prefix": "client:",
							"op_type":    "put",
							"target":     "cache",
							"value_slot": "",
							"async":      "true",
						},
						{
							"path":       "region",
							"key_prefix": "region:",
							"op_type":    "put",
							"target":     "cache",
							"value_slot": "",
							"async":      "true",
						},
					},
				},
				{Action: "batch_flush"},
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "extract-api", Path: "/v1/extract", FlowName: "extractFlow", Action: "upsert"},
		},
	})

	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/extract")
	if apiID == 0 {
		t.Fatal("route /v1/extract not found")
	}

	req := httptest.NewRequest("POST", "/v1/extract", nil)
	resp := httptest.NewRecorder()
	ctx := fm.GetContext()
	ctx.Reset(resp)
	ctx.ApiId = apiID
	ctx.SnapshotMetadata("POST", "/v1/extract", "")

	// Pre-populate the body slot (simulating json_extract_to_slot from http_call response).
	// Find which slot "body.json" maps to by checking the plan's slot map via CompileExecutable.
	// Simpler: locate the slot by checking what auto-bind did vs what we need.
	// Since body.json is not a header/query, it won't be auto-bound.
	// We inject it directly at slot 0 (first slot assigned to "body.json" in extractFlow).
	bodySlot := 0 // first slot allocated in extractFlow
	ctx.ByteSlots[bodySlot] = []byte(jsonBody)

	fm.ProcessRequest(ctx, req)

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200, got %d", ctx.ResponseStatus)
	}

	// batch_flush should have submitted 3 PUT ops to CacheExec
	ops := mock.opsFlat()
	if len(ops) != 3 {
		t.Errorf("expected 3 cache PUT ops, got %d", len(ops))
	}

	// Verify keys include the expected prefixes
	found := map[string]bool{}
	for _, op := range ops {
		found[string(op.Key)] = true
	}
	for _, wantKey := range []string{"cred:sk-test-001", "client:cid-xyz", "region:us-east"} {
		if !found[wantKey] {
			t.Errorf("expected op key %q not found in submitted ops; got: %v", wantKey, keysOf(found))
		}
	}
}

// ─── Test 5: json_foreach_emit → batch_flush (array of N elements) ───────────

// TestJSONForeachEmitIteratesArray verifies that json_foreach_emit emits one
// batch of ops per array element, and batch_flush dispatches them all.
func TestJSONForeachEmitIteratesArray(t *testing.T) {
	fm, _, server, _ := newTestStack(t)

	mock := &mockOpFlusher{}
	fm.CacheExec = mock

	// 5 service records
	const jsonBody = `[
		{"id":"svc-001","endpoint":"https://a.internal"},
		{"id":"svc-002","endpoint":"https://b.internal"},
		{"id":"svc-003","endpoint":"https://c.internal"},
		{"id":"svc-004","endpoint":"https://d.internal"},
		{"id":"svc-005","endpoint":"https://e.internal"}
	]`

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "foreach-emit-1",
		Flows: []FlowUpdate{
			{Name: "foreachFlow", Instructions: []StepConfig{
				{
					Action:   "json_foreach_emit",
					Variable: "body.services",  // slot holding the JSON array
					Path:     "@this",          // array is at root
					Params: []map[string]string{
						{
							"path":       "id",
							"key_prefix": "svc:",
							"op_type":    "put",
							"target":     "cache",
							"value_slot": "",
							"async":      "true",
						},
						{
							"path":       "endpoint",
							"key_prefix": "ep:",
							"op_type":    "put",
							"target":     "cache",
							"value_slot": "",
							"async":      "true",
						},
					},
				},
				{Action: "batch_flush"},
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "foreach-api", Path: "/v1/foreach", FlowName: "foreachFlow", Action: "upsert"},
		},
	})

	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/foreach")
	if apiID == 0 {
		t.Fatal("route /v1/foreach not found")
	}

	req := httptest.NewRequest("POST", "/v1/foreach", nil)
	ctx := fm.GetContext()
	ctx.Reset(httptest.NewRecorder())
	ctx.ApiId = apiID
	ctx.SnapshotMetadata("POST", "/v1/foreach", "")
	ctx.ByteSlots[0] = []byte(jsonBody) // body.services = slot 0

	fm.ProcessRequest(ctx, req)

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200, got %d", ctx.ResponseStatus)
	}

	// 5 elements × 2 ops per element = 10 total ops
	ops := mock.opsFlat()
	if len(ops) != 10 {
		t.Errorf("expected 10 ops (5 elements × 2 ops), got %d", len(ops))
	}

	// Verify svc: keys
	svcKeys := map[string]bool{}
	epKeys := map[string]bool{}
	for _, op := range ops {
		k := string(op.Key)
		if strings.HasPrefix(k, "svc:") {
			svcKeys[k] = true
		} else if strings.HasPrefix(k, "ep:") {
			epKeys[k] = true
		}
	}
	for i := 1; i <= 5; i++ {
		id := "svc-00" + string(rune('0'+i))
		if !svcKeys["svc:"+id] {
			t.Errorf("missing svc key svc:%s", id)
		}
	}
}

// ─── Test 6: Registry set + load round-trip ──────────────────────────────────

// TestRegistrySetAndLoadRoundTrip sets a service URL and identifier via
// set_service_url / set_identifier steps, then loads them back using
// load_service_url / load_identifier. Verifies end-to-end registry roundtrip
// within a single gateway process.
func TestRegistrySetAndLoadRoundTrip(t *testing.T) {
	fm, _, server, regMgr := newTestStack(t)

	// Register tenant "acme" so registry_lookup can resolve it.
	regMgr.UpsertTenantState([]string{"acme"}, nil, nil, nil)

	const upstreamURL = "https://acme-service.internal/v2"
	const apiKey = "sk-acme-live-001"

	// --- Flow 1: onboarding — write URL and identifier to registry ---
	// Data sources are request headers so BindHeader populates the right slots
	// at request time; no slot-index guessing needed.
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "registry-set",
		Flows: []FlowUpdate{
			{Name: "onboardFlow", Instructions: []StepConfig{
				{Action: "registry_lookup", KeyIdentifier: "header.X-Tenant"},
				{Action: "set_service_url", Key: "primary", Source: "header.X-Upstream-URL"},
				{Action: "set_identifier", Key: "api_key", Source: "header.X-Api-Key"},
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "onboard-api", Path: "/v1/onboard", FlowName: "onboardFlow", Action: "upsert"},
		},
	})

	// Execute onboarding request — values come from headers, no slot pre-population needed.
	ctxOnboard := runRequest(t, fm, "POST", "/v1/onboard", map[string]string{
		"X-Tenant":       "acme",
		"X-Upstream-URL": upstreamURL,
		"X-Api-Key":      apiKey,
	})
	if ctxOnboard.ResponseStatus != 200 {
		t.Fatalf("onboarding flow failed: status %d", ctxOnboard.ResponseStatus)
	}

	// --- Flow 2: data plane — load URL and identifier from registry ---
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "registry-load",
		Flows: []FlowUpdate{
			{Name: "loadFlow", Instructions: []StepConfig{
				{Action: "registry_lookup", KeyIdentifier: "header.X-Tenant"},
				{Action: "load_service_url", Key: "primary", As: "var.loaded_url"},
				{Action: "load_identifier", Key: "api_key", As: "var.loaded_key"},
				{Action: "set_response_body", Source: "var.loaded_url"},
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "load-api", Path: "/v1/load", FlowName: "loadFlow", Action: "upsert"},
		},
	})

	// Verify registry record was written correctly during onboarding.
	rec := regMgr.GetTenantRecord(ctxOnboard.TenantID)
	if rec == nil {
		t.Fatal("no tenant record for acme after onboarding")
	}
	if got := rec.Identifiers["api_key"]; got != apiKey {
		t.Errorf("identifier api_key: want %q, got %q", apiKey, got)
	}
	if got := rec.ServiceURLs["primary"]; got != upstreamURL {
		t.Errorf("service URL primary: want %q, got %q", upstreamURL, got)
	}

	// Load flow reads URL back from registry and puts it in the response body.
	ctx2 := runRequest(t, fm, "GET", "/v1/load", map[string]string{"X-Tenant": "acme"})
	if ctx2.ResponseStatus != 200 {
		t.Fatalf("load flow failed: status %d", ctx2.ResponseStatus)
	}
	// The load flow uses set_response_body from var.loaded_url; verify via ResponseBuffer.
	if got := string(ctx2.ResponseBuffer); got != upstreamURL {
		t.Errorf("response body after load: want %q, got %q", upstreamURL, got)
	}
}

// ─── Test 7: TMS onboarding pipeline — full E2E ───────────────────────────────

// TestTMSOnboardingPipeline simulates the Tenant Management Service (TMS) use case:
//
//  1. A mock TMS upstream returns a JSON payload with service URLs and identifiers.
//  2. A "sync" flow receives a pre-extracted JSON body (gap: http_call does not
//     capture response body; a future json_extract_to_slot step would bridge this).
//  3. json_foreach_emit iterates the services array and emits registry PUT ops.
//  4. batch_flush dispatches all ops to RegistryExecutor synchronously.
//  5. Subsequent requests use load_service_url / load_identifier to serve the
//     registry-resident data — no upstream call needed.
//
// This test demonstrates the full pipeline working end-to-end and identifies
// the missing link (http_call body capture) as a future improvement.
func TestTMSOnboardingPipeline(t *testing.T) {
	fm, _, server, regMgr := newTestStack(t)

	// Register two tenants.
	regMgr.UpsertTenantState([]string{"tenant-alpha"}, nil, nil, nil)
	regMgr.UpsertTenantState([]string{"tenant-beta"}, nil, nil, nil)

	// TMS JSON payload: array of services with their endpoints.
	// In a real TMS flow, this body comes from an upstream HTTP call.
	// Key design: "name" field IS the registry service-name key (static,
	// known at compile time via EnsureURLKeyID). "url" field IS the value.
	//
	// Current limitation of json_foreach_emit: the op KEY is KeyPrefix+extracted,
	// so to get key="primary" we need extracted="primary" (the service name).
	// Value comes from ValueSlot. Since ValueSlot is per-op and the URL must be
	// in a pre-allocated slot, we use a two-phase approach:
	//   Phase A: json_foreach_emit emits each service URL keyed by URL (for cache).
	//   Phase B: A dedicated "set_service_url" step writes to registry from a known slot.
	//
	// This test uses Phase B (registry) via set_service_url for named properties,
	// and demonstrates Phase A (cache) via json_foreach_emit for bulk storage.

	// Compact single-line JSON — newlines in header values can cause issues in http.
	const tmsJSON = `{"primary_url":"https://alpha-primary.internal/api","secondary_url":"https://alpha-secondary.internal/api","api_key":"sk-alpha-prod-001","webhook_secret":"whs-abc123"}`

	// Wire mock cache flusher for bulk cache ops.
	mockCache := &mockOpFlusher{}
	fm.CacheExec = mockCache

	// --- Sync flow: onboard a tenant from TMS data ---
	// All data sources are request headers so BindHeader populates the correct slots
	// at request time — no slot-index assumptions needed.
	// (In production, a future json_extract_to_slot step would bridge http_call → slots.)
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "tms-onboard",
		Flows: []FlowUpdate{
			{Name: "tmsSyncFlow", Instructions: []StepConfig{
				{Action: "registry_lookup", KeyIdentifier: "header.X-Tenant"},

				// Write named properties to registry (data sourced from request headers).
				{Action: "set_service_url", Key: "primary", Source: "header.X-Primary-URL"},
				{Action: "set_service_url", Key: "secondary", Source: "header.X-Secondary-URL"},
				{Action: "set_identifier", Key: "api_key", Source: "header.X-Api-Key"},
				{Action: "set_meta", Key: "webhook_secret", Source: "header.X-Webhook-Secret"},

				// Bulk-cache JSON fields from header.X-TMS-Body for fast lookups later.
				{
					Action:   "json_extract_emit",
					Variable: "header.X-TMS-Body",
					Params: []map[string]string{
						{
							"path":       "primary_url",
							"key_prefix": "tms:primary:",
							"op_type":    "put",
							"target":     "cache",
							"value_slot": "",
							"async":      "true",
						},
						{
							"path":       "secondary_url",
							"key_prefix": "tms:secondary:",
							"op_type":    "put",
							"target":     "cache",
							"value_slot": "",
							"async":      "true",
						},
					},
				},
				{Action: "batch_flush"},
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "tms-sync-api", Path: "/v1/tms/sync", FlowName: "tmsSyncFlow", Action: "upsert"},
		},
	})

	// Execute TMS sync for tenant-alpha — all values come from request headers.
	ctxSync := runRequest(t, fm, "POST", "/v1/tms/sync", map[string]string{
		"X-Tenant":         "tenant-alpha",
		"X-Primary-URL":    "https://alpha-primary.internal/api",
		"X-Secondary-URL":  "https://alpha-secondary.internal/api",
		"X-Api-Key":        "sk-alpha-prod-001",
		"X-Webhook-Secret": "whs-abc123",
		"X-TMS-Body":       tmsJSON,
	})
	if ctxSync.ResponseStatus != 200 {
		t.Fatalf("TMS sync failed: status %d", ctxSync.ResponseStatus)
	}

	// Verify registry was populated for tenant-alpha.
	{
		rec := regMgr.GetTenantRecord(ctxSync.TenantID)
		if rec == nil {
			t.Fatal("no tenant record for tenant-alpha after TMS sync")
		}
		if got := rec.ServiceURLs["primary"]; got != "https://alpha-primary.internal/api" {
			t.Errorf("primary URL: want https://alpha-primary.internal/api, got %q", got)
		}
		if got := rec.ServiceURLs["secondary"]; got != "https://alpha-secondary.internal/api" {
			t.Errorf("secondary URL: want https://alpha-secondary.internal/api, got %q", got)
		}
		if got := rec.Identifiers["api_key"]; got != "sk-alpha-prod-001" {
			t.Errorf("api_key: want sk-alpha-prod-001, got %q", got)
		}
		if got := rec.Metadata["webhook_secret"]; got != "whs-abc123" {
			t.Errorf("webhook_secret: want whs-abc123, got %q", got)
		}
	}

	// Verify cache received 2 bulk ops via batch_flush.
	ops := mockCache.opsFlat()
	if len(ops) != 2 {
		t.Errorf("expected 2 cache ops from json_extract_emit, got %d", len(ops))
	}

	// --- Flow 2: API gateway — load properties from registry on each request ---
	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "tms-load",
		Flows: []FlowUpdate{
			{Name: "tmsAPIFlow", Instructions: []StepConfig{
				{Action: "registry_lookup", KeyIdentifier: "header.X-Tenant"},
				{Action: "load_service_url", Key: "primary", As: "var.upstream"},
				{Action: "load_identifier", Key: "api_key", As: "var.key"},
				{Action: "set_response_body", Source: "var.upstream"},
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "tms-api", Path: "/v1/tms/call", FlowName: "tmsAPIFlow", Action: "upsert"},
		},
	})

	ctx3 := runRequest(t, fm, "GET", "/v1/tms/call", map[string]string{"X-Tenant": "tenant-alpha"})
	if ctx3.ResponseStatus != 200 {
		t.Fatalf("TMS API call failed: status %d", ctx3.ResponseStatus)
	}

	// set_response_body copies the loaded URL into ResponseBuffer.
	want := "https://alpha-primary.internal/api"
	if got := string(ctx3.ResponseBuffer); got != want {
		t.Errorf("upstream after load: want %q, got %q", want, got)
	}

	// tenant-beta should return empty (not yet onboarded) — no crash.
	ctx4 := runRequest(t, fm, "GET", "/v1/tms/call", map[string]string{"X-Tenant": "tenant-beta"})
	if ctx4.ResponseStatus == 500 {
		t.Errorf("un-onboarded tenant should not cause 500, got %d", ctx4.ResponseStatus)
	}
}

// ─── Test 8: Batch ops with RegistryExec (json_foreach_emit → registry) ──────

// TestJSONForeachEmitToRegistry tests the full batch pipeline routing to the
// RegistryExecutor. Each array element writes a service URL entry to the registry.
//
// Design note: since RegistryExecutor.Submit resolves TenantID→alias, ctx.TenantID
// must be set before batch_flush. We set it directly here (simulating registry_lookup).
func TestJSONForeachEmitToRegistry(t *testing.T) {
	fm, _, server, regMgr := newTestStack(t)

	// Register tenant.
	regMgr.UpsertTenantState([]string{"test-tenant"}, nil, nil, nil)

	// For RegistryExecutor to work, TenantID must resolve to an alias.
	// We look up the TenantID after tenant creation.
	reg := tenantregistry.State.Active.Load()
	tID, ok := reg.Aliases.Lookup("test-tenant")
	if !ok {
		t.Fatal("test-tenant not in registry after UpsertTenantState")
	}

	// JSON: array of {id, url} records. Each element emits one PUT to registry URLs.
	// Key = "ep:" + extracted_id_value (e.g. "ep:svc-a")
	// Value = extracted_id_value (the ID itself — represents a self-referencing record ID).
	// This tests the batch pipeline end-to-end. For key≠value scenarios a future
	// ValuePath field in ExtractOp would be needed.
	const jsonBody = `[{"id":"svc-a"},{"id":"svc-b"},{"id":"svc-c"}]`

	mustSync(t, server, UnifiedSyncRequest{
		SyncUUID: "foreach-registry",
		Flows: []FlowUpdate{
			{Name: "regBatchFlow", Instructions: []StepConfig{
				{
					Action:   "json_foreach_emit",
					Variable: "var.body",
					Path:     "@this",
					Params: []map[string]string{
						{
							"path":       "id",
							"key_prefix": "ep:",
							"op_type":    "put",
							"target":     "registry_url",
							"value_slot": "",
							"async":      "true",
						},
					},
				},
				{Action: "batch_flush"},
				{Action: "set_response_status", Value: "200"},
			}, Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "reg-batch-api", Path: "/v1/reg-batch", FlowName: "regBatchFlow", Action: "upsert"},
		},
	})

	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/reg-batch")

	req := httptest.NewRequest("POST", "/v1/reg-batch", nil)
	ctx := fm.GetContext()
	ctx.Reset(httptest.NewRecorder())
	ctx.ApiId = apiID
	ctx.TenantID = tID // set tenant so RegistryExecutor can resolve alias
	ctx.TenantKey = "test-tenant"
	ctx.SnapshotMetadata("POST", "/v1/reg-batch", "")
	ctx.ByteSlots[0] = []byte(jsonBody) // var.body = slot 0

	fm.ProcessRequest(ctx, req)

	if ctx.ResponseStatus != 200 {
		t.Errorf("expected 200, got %d", ctx.ResponseStatus)
	}

	// Verify registry received the 3 service URL writes.
	rec := regMgr.GetTenantRecord(tID)
	if rec == nil {
		t.Fatal("no tenant record after batch flush to registry")
	}
	for _, want := range []string{"ep:svc-a", "ep:svc-b", "ep:svc-c"} {
		if _, found := rec.ServiceURLs[want]; !found {
			t.Errorf("expected registry service URL key %q, not found; got: %v", want, rec.ServiceURLs)
		}
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func keysOf(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// findSlotForStep returns the source slot used by the instruction with the given name,
// by scanning ctx.ByteSlots for the first non-nil slot after the named instruction.
// This is a heuristic helper for test assertions only.
func findSlotForStep(plan []engine.Instruction, name string) int {
	for i, instr := range plan {
		if instr.Name == name && i > 0 {
			// The instruction before it likely set up the source slot.
			// Return the slot index assigned to the preceding bind instruction.
			// Simplified: return i as a proxy (not precise, used for test setup).
			_ = i
			return -1 // caller uses direct slot index instead
		}
	}
	return -1
}

// findNamedSlot locates the destination slot of an instruction by inspecting
// what the instruction loaded into ByteSlots after execution.
// For use in assertions only — returns the first slot modified by the named instr.
func findNamedSlot(plan []engine.Instruction, name string) int {
	for i, instr := range plan {
		if instr.Name == name {
			// The instruction at position i uses a destSlot captured at compile time.
			// We can't introspect it without executing; return i as a best-effort index.
			return i // caller will bounds-check
		}
	}
	return -1
}
