package control

import (
	"context"
	"encoding/json"
	"testing"

	"rah/internal/config"
	"rah/internal/engine"
)

// diskOnlyStoreConfig creates a minimal DataStoreConfig backed by a temp directory,
// binding only the domains needed for management-server persistence tests.
func diskOnlyStoreConfig(t *testing.T) config.DataStoreConfig {
	t.Helper()
	return config.DataStoreConfig{
		Stores: map[string]config.StoreConfig{
			"disk": {
				Name:    "disk",
				Kind:    config.StoreDisk,
				Enabled: true,
				Connection: config.StoreConnection{
					Path: t.TempDir(),
				},
			},
		},
		Bindings: map[config.DataDomain]string{
			config.DomainAPIDefinitions: "disk",
			config.DomainFlows:          "disk",
			config.DomainTenantRegistry: "disk",
			config.DomainCache:          "disk",
		},
	}
}

// newTestMS builds a minimal ManagementServer suitable for persistence tests.
func newTestMS(t *testing.T) *ManagementServer {
	t.Helper()
	fm := engine.NewFlowManager(32, config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		MaxBoolsSlots: 8,
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1024 * 1024},
	})
	return NewManagementServer(fm, NewCompiler(fm), NewNameRegistry(), nil)
}

// echoFlow returns a trivial flow that compiles to a single echo_request instruction.
func echoFlow() []StepConfig {
	return []StepConfig{{Action: "echo_request"}}
}

// upsertFlowAndAPI applies a single-flow, single-API upsert and fatals on error.
func upsertFlowAndAPI(t *testing.T, s *ManagementServer, flowName, apiName, path string) {
	t.Helper()
	if err := s.ApplyUnifiedSync(UnifiedSyncRequest{
		Flows: []FlowUpdate{{Name: flowName, Instructions: echoFlow(), Action: "upsert"}},
		Apis:  []ApiUpdate{{Name: apiName, Path: path, FlowName: flowName, Action: "upsert"}},
	}); err != nil {
		t.Fatalf("initial upsert failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Deletion safety
// ---------------------------------------------------------------------------

func TestFlowDeleteRejectedWhenReferencedByApi(t *testing.T) {
	s := newTestMS(t)
	upsertFlowAndAPI(t, s, "f1", "a1", "/v1/test")

	err := s.ApplyUnifiedSync(UnifiedSyncRequest{
		Flows: []FlowUpdate{{Name: "f1", Action: "delete"}},
	})
	if err == nil {
		t.Fatal("expected error when deleting flow still referenced by API, got nil")
	}
}

func TestFlowDeleteAllowedWhenApiDeletedInSameRequest(t *testing.T) {
	s := newTestMS(t)
	upsertFlowAndAPI(t, s, "f1", "a1", "/v1/test")

	// Deleting both API and flow in a single sync payload must succeed.
	if err := s.ApplyUnifiedSync(UnifiedSyncRequest{
		Apis:  []ApiUpdate{{Name: "a1", Action: "delete"}},
		Flows: []FlowUpdate{{Name: "f1", Action: "delete"}},
	}); err != nil {
		t.Fatalf("expected simultaneous API+flow delete to succeed, got: %v", err)
	}

	state := s.FlowManager.State.Load()
	if state.Router.Lookup("/v1/test") != 0 {
		t.Fatal("route still present after deletion")
	}
	if _, ok := state.FlowLibrary["f1"]; ok {
		t.Fatal("flow still in library after deletion")
	}
}

func TestFlowDeleteAllowedWhenUnreferenced(t *testing.T) {
	s := newTestMS(t)
	// Register the flow without any API pointing to it.
	if err := s.ApplyUnifiedSync(UnifiedSyncRequest{
		Flows: []FlowUpdate{{Name: "standalone", Instructions: echoFlow(), Action: "upsert"}},
	}); err != nil {
		t.Fatalf("flow upsert failed: %v", err)
	}

	if err := s.ApplyUnifiedSync(UnifiedSyncRequest{
		Flows: []FlowUpdate{{Name: "standalone", Action: "delete"}},
	}); err != nil {
		t.Fatalf("expected unreferenced flow delete to succeed, got: %v", err)
	}

	state := s.FlowManager.State.Load()
	if _, ok := state.FlowLibrary["standalone"]; ok {
		t.Fatal("flow still in library after deletion")
	}
}

// ---------------------------------------------------------------------------
// Write-through persistence
// ---------------------------------------------------------------------------

func TestPersistenceFlowAndApiUpsertedToDataStore(t *testing.T) {
	dsm, err := NewDataStoreManager(diskOnlyStoreConfig(t))
	if err != nil {
		t.Fatalf("build datastore manager: %v", err)
	}

	s := newTestMS(t)
	s.SetDataStore(dsm)
	upsertFlowAndAPI(t, s, "myFlow", "myApi", "/v1/persist")

	ctx := context.Background()

	flows, err := dsm.ReadFlowsSnapshot(ctx)
	if err != nil {
		t.Fatalf("read flows snapshot: %v", err)
	}
	if _, ok := flows["myFlow"]; !ok {
		t.Fatalf("flow not persisted; got keys: %v", keys(flows))
	}

	apis, err := dsm.ReadAPIDefinitionsSnapshot(ctx)
	if err != nil {
		t.Fatalf("read apis snapshot: %v", err)
	}
	if _, ok := apis["myApi"]; !ok {
		t.Fatalf("API not persisted; got keys: %v", keys(apis))
	}
}

func TestPersistenceFlowAndApiDeletedFromDataStore(t *testing.T) {
	dsm, err := NewDataStoreManager(diskOnlyStoreConfig(t))
	if err != nil {
		t.Fatalf("build datastore manager: %v", err)
	}

	s := newTestMS(t)
	s.SetDataStore(dsm)
	upsertFlowAndAPI(t, s, "myFlow", "myApi", "/v1/persist")

	// Delete in a single request so the flow-reference check passes.
	if err := s.ApplyUnifiedSync(UnifiedSyncRequest{
		Apis:  []ApiUpdate{{Name: "myApi", Action: "delete"}},
		Flows: []FlowUpdate{{Name: "myFlow", Action: "delete"}},
	}); err != nil {
		t.Fatalf("delete sync failed: %v", err)
	}

	ctx := context.Background()

	flows, err := dsm.ReadFlowsSnapshot(ctx)
	if err != nil {
		t.Fatalf("read flows snapshot: %v", err)
	}
	if _, ok := flows["myFlow"]; ok {
		t.Fatal("flow still present in datastore after deletion")
	}

	apis, err := dsm.ReadAPIDefinitionsSnapshot(ctx)
	if err != nil {
		t.Fatalf("read apis snapshot: %v", err)
	}
	if _, ok := apis["myApi"]; ok {
		t.Fatal("API still present in datastore after deletion")
	}
}

func TestPersistenceSkippedWhenDataStoreNotSet(t *testing.T) {
	s := newTestMS(t) // No SetDataStore call.
	// Should not panic or return an error.
	upsertFlowAndAPI(t, s, "f", "a", "/v1/nopersist")
	if err := s.ApplyUnifiedSync(UnifiedSyncRequest{
		Apis:  []ApiUpdate{{Name: "a", Action: "delete"}},
		Flows: []FlowUpdate{{Name: "f", Action: "delete"}},
	}); err != nil {
		t.Fatalf("delete without datastore: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Bootstrap
// ---------------------------------------------------------------------------

func TestBootstrapFromDataStore(t *testing.T) {
	dsm, err := NewDataStoreManager(diskOnlyStoreConfig(t))
	if err != nil {
		t.Fatalf("build datastore manager: %v", err)
	}
	ctx := context.Background()

	// Pre-populate the datastore as if a previous run had persisted these entries.
	flowSteps := echoFlow()
	flowJSON, _ := json.Marshal(flowSteps)
	if err := dsm.PutGlobal(ctx, config.DomainFlows, "bootFlow", flowJSON); err != nil {
		t.Fatalf("seed flow: %v", err)
	}

	apiCfg := ApiConfig{ApiID: "bootApi", Path: "/v1/boot", FlowName: "bootFlow"}
	apiJSON, _ := json.Marshal(apiCfg)
	if err := dsm.PutGlobal(ctx, config.DomainAPIDefinitions, "bootApi", apiJSON); err != nil {
		t.Fatalf("seed api: %v", err)
	}

	// Bootstrap — must be called before SetDataStore to avoid redundant re-writes.
	s := newTestMS(t)
	if err := s.Bootstrap(ctx, dsm); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	s.SetDataStore(dsm)

	state := s.FlowManager.State.Load()
	apiID := state.Router.Lookup("/v1/boot")
	if apiID == 0 {
		t.Fatal("route /v1/boot not registered after bootstrap")
	}
	if int(apiID) >= len(state.Definitions) || state.Definitions[apiID] == nil {
		t.Fatalf("API definition missing for id=%d", apiID)
	}
	if _, ok := state.FlowLibrary["bootFlow"]; !ok {
		t.Fatal("flow library missing bootFlow after bootstrap")
	}
}

func TestBootstrapFromEmptyDataStoreIsNoop(t *testing.T) {
	dsm, err := NewDataStoreManager(diskOnlyStoreConfig(t))
	if err != nil {
		t.Fatalf("build datastore manager: %v", err)
	}

	s := newTestMS(t)
	if err := s.Bootstrap(context.Background(), dsm); err != nil {
		t.Fatalf("bootstrap on empty store: %v", err)
	}
	// Router should be empty (no routes registered).
	state := s.FlowManager.State.Load()
	if state.Router.Lookup("/anything") != 0 {
		t.Fatal("expected empty router after noop bootstrap")
	}
}

// ---------------------------------------------------------------------------
// Incremental sync — datastore accumulates across partial calls
// ---------------------------------------------------------------------------

// TestIncrementalSyncAccumulates verifies the scenario:
//
//	call 1 → upsert 3 APIs  → datastore has 3
//	call 2 → upsert 2 more  → datastore has 5
//	call 3 → delete 1       → datastore has 4
//	restart → bootstrap     → gateway sees 4 APIs
func TestIncrementalSyncAccumulates(t *testing.T) {
	dsm, err := NewDataStoreManager(diskOnlyStoreConfig(t))
	if err != nil {
		t.Fatalf("build datastore manager: %v", err)
	}

	s := newTestMS(t)
	s.SetDataStore(dsm)

	// Call 1: 3 flows + 3 APIs
	if err := s.ApplyUnifiedSync(UnifiedSyncRequest{
		Flows: []FlowUpdate{
			{Name: "f1", Instructions: echoFlow(), Action: "upsert"},
			{Name: "f2", Instructions: echoFlow(), Action: "upsert"},
			{Name: "f3", Instructions: echoFlow(), Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "a1", Path: "/v1/one", FlowName: "f1", Action: "upsert"},
			{Name: "a2", Path: "/v1/two", FlowName: "f2", Action: "upsert"},
			{Name: "a3", Path: "/v1/three", FlowName: "f3", Action: "upsert"},
		},
	}); err != nil {
		t.Fatalf("call 1 failed: %v", err)
	}

	// Call 2: 2 more flows + 2 more APIs (partial — no mention of a1/a2/a3)
	if err := s.ApplyUnifiedSync(UnifiedSyncRequest{
		Flows: []FlowUpdate{
			{Name: "f4", Instructions: echoFlow(), Action: "upsert"},
			{Name: "f5", Instructions: echoFlow(), Action: "upsert"},
		},
		Apis: []ApiUpdate{
			{Name: "a4", Path: "/v1/four", FlowName: "f4", Action: "upsert"},
			{Name: "a5", Path: "/v1/five", FlowName: "f5", Action: "upsert"},
		},
	}); err != nil {
		t.Fatalf("call 2 failed: %v", err)
	}

	ctx := context.Background()

	apis, _ := dsm.ReadAPIDefinitionsSnapshot(ctx)
	if len(apis) != 5 {
		t.Fatalf("after 2 calls: expected 5 APIs in datastore, got %d: %v", len(apis), keys(apis))
	}

	// Call 3: delete a2 only
	if err := s.ApplyUnifiedSync(UnifiedSyncRequest{
		Apis:  []ApiUpdate{{Name: "a2", Action: "delete"}},
		Flows: []FlowUpdate{{Name: "f2", Action: "delete"}},
	}); err != nil {
		t.Fatalf("call 3 (delete) failed: %v", err)
	}

	apis, _ = dsm.ReadAPIDefinitionsSnapshot(ctx)
	if len(apis) != 4 {
		t.Fatalf("after delete: expected 4 APIs in datastore, got %d: %v", len(apis), keys(apis))
	}
	if _, ok := apis["a2"]; ok {
		t.Fatal("deleted API a2 still present in datastore")
	}

	// Simulate restart: new management server bootstraps from the same datastore.
	s2 := newTestMS(t)
	if err := s2.Bootstrap(ctx, dsm); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	state := s2.FlowManager.State.Load()
	for _, path := range []string{"/v1/one", "/v1/three", "/v1/four", "/v1/five"} {
		if state.Router.Lookup(path) == 0 {
			t.Errorf("route %q not registered after bootstrap", path)
		}
	}
	if state.Router.Lookup("/v1/two") != 0 {
		t.Error("deleted route /v1/two still registered after bootstrap")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
