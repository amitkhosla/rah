package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
)

func TestUnifiedSyncRegistersApiAndRuntimeConsumesCompiledFlow(t *testing.T) {
	fm := engine.NewFlowManager(32, config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		MaxBoolsSlots: 8,
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1024 * 1024},
	})

	compiler := NewCompiler(fm)
	registry := NewNameRegistry()
	server := NewManagementServer(fm, compiler, registry, nil)

	syncReq := UnifiedSyncRequest{
		SyncUUID: "sync-1",
		Flows: []FlowUpdate{
			{
				Name: "usersFlow",
				Instructions: []StepConfig{
					{Action: "set_response_status", Value: "200"},
				},
				Action: "upsert",
			},
		},
		Apis: []ApiUpdate{
			{Name: "users-api", Path: "/v1/users", FlowName: "usersFlow", Action: "upsert"},
		},
	}

	payload, err := json.Marshal(syncReq)
	if err != nil {
		t.Fatalf("marshal sync request: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(payload))
	httpResp := httptest.NewRecorder()
	server.UnifiedSyncHandler(httpResp, httpReq)

	if httpResp.Code != http.StatusOK {
		t.Fatalf("expected status 200 from /sync, got %d body=%s", httpResp.Code, httpResp.Body.String())
	}

	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/users")
	if apiID == 0 {
		t.Fatalf("expected /v1/users to be present in router")
	}
	if int(apiID) >= len(state.Definitions) || state.Definitions[apiID] == nil {
		t.Fatalf("expected api definition for id=%d", apiID)
	}

	runtimeReq := httptest.NewRequest(http.MethodGet, "http://localhost/v1/users", nil)
	runtimeResp := httptest.NewRecorder()

	ctx := fm.GetContext()
	defer fm.Pool.Put(ctx)
	ctx.Reset(runtimeResp)
	ctx.ApiId = apiID
	ctx.SnapshotMetadata(runtimeReq.Method, runtimeReq.URL.Path, runtimeReq.URL.RawQuery)

	fm.ProcessRequest(ctx, runtimeReq)

	if ctx.ResponseStatus != http.StatusOK {
		t.Fatalf("expected runtime request to resolve and execute with 200, got %d", ctx.ResponseStatus)
	}
}

func TestUnifiedSyncBuildsApiPlanWithInputBindingsAndSubflowCalls(t *testing.T) {
	fm := engine.NewFlowManager(32, config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		MaxBoolsSlots: 8,
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1024 * 1024},
	})

	compiler := NewCompiler(fm)
	registry := NewNameRegistry()
	server := NewManagementServer(fm, compiler, registry, nil)

	syncReq := UnifiedSyncRequest{
		SyncUUID: "sync-2",
		Flows: []FlowUpdate{
			{
				Name: "lookupUser",
				Instructions: []StepConfig{
					{Action: "registry_lookup", KeyIdentifier: "query.user", As: "userMeta", Scope: "tenant"},
				},
				Action: "upsert",
			},
			{
				Name: "entryFlow",
				Instructions: []StepConfig{
					{Action: "call", FlowName: "lookupUser"},
				},
				Action: "upsert",
			},
		},
		Apis: []ApiUpdate{
			{Name: "lookup-api", Path: "/v1/users", FlowName: "entryFlow", Action: "upsert"},
		},
	}

	payload, err := json.Marshal(syncReq)
	if err != nil {
		t.Fatalf("marshal sync request: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(payload))
	httpResp := httptest.NewRecorder()
	server.UnifiedSyncHandler(httpResp, httpReq)

	if httpResp.Code != http.StatusOK {
		t.Fatalf("expected status 200 from /sync, got %d body=%s", httpResp.Code, httpResp.Body.String())
	}

	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/users")
	if apiID == 0 {
		t.Fatalf("expected /v1/users to be present in router")
	}

	def := state.Definitions[apiID]
	if def == nil || len(def.Endpoints) == 0 || len(def.Endpoints[0].Plan) < 3 {
		t.Fatalf("expected endpoint plan to be generated")
	}

	// [0] SET_STREAM_RESPONSE_BODY â€” always first; flow has no http_call so streaming=true
	// [1] BIND_QUERY â€” auto-bind preamble for query.user dependency
	// [2] REG_LOOKUP â€” from the called lookupUser subflow
	if got := def.Endpoints[0].Plan[1].Name; got != "BIND_QUERY" {
		t.Fatalf("expected Plan[1] to be BIND_QUERY, got %s", got)
	}
	if got := def.Endpoints[0].Plan[2].Name; got != "REG_LOOKUP" {
		t.Fatalf("expected Plan[2] to be REG_LOOKUP from called subflow, got %s", got)
	}
}

func TestUnifiedSyncConfiguredTextResponseViaCompilerAPI(t *testing.T) {
	fm := engine.NewFlowManager(32, config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		MaxBoolsSlots: 8,
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1024 * 1024},
	})

	compiler := NewCompiler(fm)
	registry := NewNameRegistry()
	server := NewManagementServer(fm, compiler, registry, nil)

	const expectedBody = "hello from configured flow"
	syncReq := UnifiedSyncRequest{
		SyncUUID: "text-response-1",
		Flows: []FlowUpdate{{
			Name: "textFlow",
			Instructions: []StepConfig{
				{Action: "return", Status: 200, Body: expectedBody},
			},
			Action: "upsert",
		}},
		Apis: []ApiUpdate{{Name: "text-api", Path: "/v1/text", FlowName: "textFlow", Action: "upsert"}},
	}

	payload, err := json.Marshal(syncReq)
	if err != nil {
		t.Fatalf("marshal sync request: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(payload))
	httpResp := httptest.NewRecorder()
	server.UnifiedSyncHandler(httpResp, httpReq)

	if httpResp.Code != http.StatusOK {
		t.Fatalf("expected status 200 from /sync, got %d body=%s", httpResp.Code, httpResp.Body.String())
	}

	state := fm.State.Load()
	apiID := state.Router.Lookup("/v1/text")
	if apiID == 0 {
		t.Fatalf("expected /v1/text to be present in router")
	}

	runtimeReq := httptest.NewRequest(http.MethodGet, "http://localhost/v1/text", nil)
	runtimeResp := httptest.NewRecorder()

	ctx := fm.GetContext()
	defer fm.Pool.Put(ctx)
	ctx.Reset(runtimeResp)
	ctx.ApiId = apiID
	ctx.SnapshotMetadata(runtimeReq.Method, runtimeReq.URL.Path, runtimeReq.URL.RawQuery)

	fm.ProcessRequest(ctx, runtimeReq)

	if ctx.ResponseStatus != http.StatusOK {
		t.Fatalf("expected 200, got %d", ctx.ResponseStatus)
	}
	if got := string(ctx.ResponseBuffer); got != expectedBody {
		t.Fatalf("expected response body %q, got %q", expectedBody, got)
	}
}
