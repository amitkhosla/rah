package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/rctx"
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
	server := NewManagementServer(fm, compiler, registry)

	syncReq := UnifiedSyncRequest{
		SyncUUID: "sync-1",
		Flows: []FlowUpdate{
			{
				Name: "usersFlow",
				Instructions: []StepConfig{
					{Action: "registry_lookup", KeyIdentifier: "header.X-User", As: "userMeta", Scope: "tenant"},
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

	ctx := fm.Pool.Get().(*rctx.Context)
	defer fm.Pool.Put(ctx)
	ctx.Reset(runtimeResp)
	ctx.ApiId = apiID
	ctx.SnapshotMetadata(runtimeReq.Method, runtimeReq.URL.Path, runtimeReq.URL.RawQuery)

	fm.ProcessRequest(ctx, runtimeReq)

	if ctx.ResponseStatus != http.StatusOK {
		t.Fatalf("expected runtime request to resolve and execute with 200, got %d", ctx.ResponseStatus)
	}
}
