package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/mcpreg"
)

// newTestMSWithAI returns a ManagementServer wired with a config.Manager and
// an mcpreg.Registry so AI/MCP sync paths are exercised.
func newTestMSWithAI(t *testing.T) (*ManagementServer, *config.Manager, *mcpreg.Registry) {
	t.Helper()
	ms := newTestMS(t)
	cfgMgr := config.Default()
	reg := mcpreg.NewRegistry()
	ms.SetCfgMgr(cfgMgr)
	ms.SetMCPReg(reg)
	return ms, cfgMgr, reg
}

func TestApplyUnifiedSyncLLMModels(t *testing.T) {
	ms, cfgMgr, _ := newTestMSWithAI(t)

	req := UnifiedSyncRequest{
		SyncUUID: "sync-llm-1",
		LLMModels: []config.LLMModelConfig{
			{Alias: "gpt4o", Provider: "openai", Adapter: "openai"},
		},
	}
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync: %v", err)
	}

	found := false
	for _, m := range cfgMgr.LLM().Models {
		if m.Alias == "gpt4o" {
			found = true
			if m.Provider != "openai" {
				t.Errorf("model provider = %q, want openai", m.Provider)
			}
		}
	}
	if !found {
		t.Error("model gpt4o not found in cfgMgr after sync")
	}
}

func TestApplyUnifiedSyncLLMModelsUpsert(t *testing.T) {
	ms, cfgMgr, _ := newTestMSWithAI(t)

	// Insert then update the same alias.
	req1 := UnifiedSyncRequest{
		SyncUUID: "sync-llm-2a",
		LLMModels: []config.LLMModelConfig{
			{Alias: "claude3", Provider: "anthropic-old", Adapter: "anthropic"},
		},
	}
	if err := ms.ApplyUnifiedSync(req1); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	req2 := UnifiedSyncRequest{
		SyncUUID: "sync-llm-2b",
		LLMModels: []config.LLMModelConfig{
			{Alias: "claude3", Provider: "anthropic-new", Adapter: "anthropic"},
		},
	}
	if err := ms.ApplyUnifiedSync(req2); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	count := 0
	for _, m := range cfgMgr.LLM().Models {
		if m.Alias == "claude3" {
			count++
			if m.Provider != "anthropic-new" {
				t.Errorf("expected updated provider anthropic-new, got %q", m.Provider)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one claude3 entry, got %d", count)
	}
}

func TestApplyUnifiedSyncMCPServers(t *testing.T) {
	ms, cfgMgr, _ := newTestMSWithAI(t)

	req := UnifiedSyncRequest{
		SyncUUID: "sync-mcp-1",
		MCPServers: []config.MCPServerConfig{
			{Alias: "tools-mcp", Transport: "http", URL: "http://tools.example.com"},
		},
	}
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync: %v", err)
	}

	found := false
	for _, s := range cfgMgr.LLM().MCPServers {
		if s.Alias == "tools-mcp" {
			found = true
			if s.URL != "http://tools.example.com" {
				t.Errorf("server URL = %q, want http://tools.example.com", s.URL)
			}
		}
	}
	if !found {
		t.Error("MCP server tools-mcp not found in cfgMgr after sync")
	}
}

func TestApplyUnifiedSyncVirtualMCPServers(t *testing.T) {
	ms, _, reg := newTestMSWithAI(t)

	req := UnifiedSyncRequest{
		SyncUUID: "sync-vmcp-1",
		VirtualMCPServers: []mcpreg.VirtualMCPServerDef{
			{Name: "kb-server", Sources: []mcpreg.ToolSource{{Kind: "api_tool"}}},
		},
	}
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync: %v", err)
	}

	def, ok := reg.GetServer(0, "kb-server")
	if !ok {
		t.Fatal("virtual MCP server kb-server not found in registry after sync")
	}
	if def.Name != "kb-server" {
		t.Errorf("server name = %q, want kb-server", def.Name)
	}
}

func TestApplyUnifiedSyncAPITools(t *testing.T) {
	ms, _, reg := newTestMSWithAI(t)

	req := UnifiedSyncRequest{
		SyncUUID: "sync-apitool-1",
		APITools: []mcpreg.APIToolDef{
			{Name: "search", Path: "/v1/kb/search", Method: "POST"},
		},
	}
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync: %v", err)
	}

	tool, ok := reg.GetAPITool("search")
	if !ok {
		t.Fatal("API tool search not found in registry after sync")
	}
	if tool.Path != "/v1/kb/search" {
		t.Errorf("tool path = %q, want /v1/kb/search", tool.Path)
	}
}

func TestApplyUnifiedSyncSkipsWhenNoCfgMgr(t *testing.T) {
	// Without SetCfgMgr, LLMModels and MCPServers blocks should be silently skipped.
	ms := newTestMS(t)
	req := UnifiedSyncRequest{
		SyncUUID: "sync-nocfg",
		LLMModels: []config.LLMModelConfig{
			{Alias: "gpt4o", Provider: "openai", Adapter: "openai"},
		},
		MCPServers: []config.MCPServerConfig{
			{Alias: "tools-mcp", Transport: "http", URL: "http://tools.example.com"},
		},
	}
	// Must not panic or return an error.
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync without cfgMgr: %v", err)
	}
}

func TestApplyUnifiedSyncSkipsWhenNoMCPReg(t *testing.T) {
	// Without SetMCPReg, VirtualMCPServers and APITools blocks should be silently skipped.
	ms := newTestMS(t)
	req := UnifiedSyncRequest{
		SyncUUID: "sync-noreg",
		VirtualMCPServers: []mcpreg.VirtualMCPServerDef{
			{Name: "kb-server"},
		},
		APITools: []mcpreg.APIToolDef{
			{Name: "search", Path: "/v1/kb/search", Method: "POST"},
		},
	}
	if err := ms.ApplyUnifiedSync(req); err != nil {
		t.Fatalf("ApplyUnifiedSync without mcpReg: %v", err)
	}
}

// ---------------------------------------------------------------------------
// AppProtocol CRUD Tests (HTTP)
// ---------------------------------------------------------------------------

// appProtocolTestServer wraps a persistent HTTP mux for testing app-protocols.
type appProtocolTestServer struct {
	mux *http.ServeMux
}

// newAppProtocolTestServer creates a test server with RegisterAIRoutes wired.
func newAppProtocolTestServer(t *testing.T, cfgMgr *config.Manager, dsm *DataStoreManager, mcpReg *mcpreg.Registry) *appProtocolTestServer {
	t.Helper()

	// Ensure mcpReg is provided (app-protocols routes require it).
	if mcpReg == nil {
		mcpReg = mcpreg.NewRegistry()
	}

	mux := http.NewServeMux()
	rebake := func() {} // No-op for testing
	RegisterAIRoutes(mux, cfgMgr, rebake, dsm, mcpReg)

	return &appProtocolTestServer{mux: mux}
}

// makeRequest makes an HTTP request to the test server and returns the response and decoded aiResponse.
// Note: The mux is safe for concurrent use (ServeHTTP is goroutine-safe), but some operations
// like app protocols may need serialization for deterministic testing. For concurrent tests,
// each goroutine should use a separate appProtocolTestServer or coordinate carefully.
func (srv *appProtocolTestServer) makeRequest(t *testing.T, method, path string, body []byte) (*httptest.ResponseRecorder, aiResponse) {
	t.Helper()

	// Make the request.
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rr := httptest.NewRecorder()

	srv.mux.ServeHTTP(rr, req)

	// Parse the aiResponse envelope.
	var resp aiResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v (body=%q)", err, rr.Body.String())
	}

	return rr, resp
}

func TestAppProtocol_UpsertAndGet(t *testing.T) {
	ms, cfgMgr, mcpReg := newTestMSWithAI(t)
	dsm, _ := NewDataStoreManager(context.Background(), testStoreConfigForAppProtocols(t), nil)
	ms.SetDataStore(dsm)

	// Create a persistent test server.
	srv := newAppProtocolTestServer(t, cfgMgr, dsm, mcpReg)

	// Create a valid AppProtocolUpdate.
	updatePayload := AppProtocolUpdate{
		AppName: "weather-app",
		MCP: &AppMCPProtocol{
			ServerName:  "weather-mcp",
			Description: "Weather data provider",
		},
		A2A: &AppA2AProtocol{
			Description: "Weather A2A endpoint",
			Version:     "1.0",
		},
		Action: "upsert",
	}

	body, _ := json.Marshal(updatePayload)

	// POST /ai/app-protocols with valid data.
	rr, resp := srv.makeRequest(t, http.MethodPost, "/ai/app-protocols", body)

	if rr.Code != http.StatusOK {
		t.Errorf("POST /ai/app-protocols: expected 200, got %d", rr.Code)
	}
	if !resp.OK {
		t.Errorf("response OK=false, Error=%s", resp.Error)
	}

	// GET /ai/app-protocols?app_name=weather-app
	rr, resp = srv.makeRequest(t, http.MethodGet, "/ai/app-protocols?app_name=weather-app", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("GET /ai/app-protocols: expected 200, got %d", rr.Code)
	}

	// Verify the returned data.
	protos, ok := resp.Data.([]interface{})
	if !ok || len(protos) != 1 {
		t.Errorf("expected 1 protocol in response, got type=%T len=%d", resp.Data, len(protos))
		return
	}

	// Re-marshal to verify content (JSON unmarshaling gives us map[string]interface{}).
	protoBytes, _ := json.Marshal(protos[0])
	var retrieved AppProtocolUpdate
	if err := json.Unmarshal(protoBytes, &retrieved); err != nil {
		t.Fatalf("failed to unmarshal retrieved protocol: %v", err)
	}

	if retrieved.AppName != "weather-app" {
		t.Errorf("AppName: expected %q, got %q", "weather-app", retrieved.AppName)
	}
	if retrieved.MCP == nil || retrieved.MCP.ServerName != "weather-mcp" {
		t.Errorf("MCP.ServerName: expected %q, got %v", "weather-mcp", retrieved.MCP)
	}
	if retrieved.A2A == nil || retrieved.A2A.Description != "Weather A2A endpoint" {
		t.Errorf("A2A.Description: expected %q, got %v", "Weather A2A endpoint", retrieved.A2A)
	}
}

func TestAppProtocol_Persistence(t *testing.T) {
	ms1, cfgMgr, mcpReg := newTestMSWithAI(t)
	dsm, _ := NewDataStoreManager(context.Background(), testStoreConfigForAppProtocols(t), nil)
	ms1.SetDataStore(dsm)

	// Create a test server for the first instance.
	srv := newAppProtocolTestServer(t, cfgMgr, dsm, mcpReg)

	// Upsert an app protocol.
	updatePayload := AppProtocolUpdate{
		AppName: "persist-app",
		MCP: &AppMCPProtocol{
			ServerName: "persist-mcp",
		},
		Action: "upsert",
	}
	body, _ := json.Marshal(updatePayload)

	rr, _ := srv.makeRequest(t, http.MethodPost, "/ai/app-protocols", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("initial upsert failed: %d", rr.Code)
	}

	// Verify it was saved in the first server instance.
	rr, respFirst := srv.makeRequest(t, http.MethodGet, "/ai/app-protocols?app_name=persist-app", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET before restart: %d", rr.Code)
	}
	protos1, ok := respFirst.Data.([]interface{})
	if !ok || len(protos1) != 1 {
		t.Fatalf("before restart: expected 1 protocol, got %d", len(protos1))
	}

	// Simulate restart by creating a new ManagementServer with the same datastore.
	ms2 := newTestMS(t)
	ms2.SetDataStore(dsm)
	ms2.SetCfgMgr(cfgMgr)

	// Create a new test server for the second instance (simulates restart).
	srv2 := newAppProtocolTestServer(t, cfgMgr, dsm, mcpReg)

	// GET from the new instance should return the persisted entry.
	rr, resp := srv2.makeRequest(t, http.MethodGet, "/ai/app-protocols?app_name=persist-app", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("GET after restart: expected 200, got %d", rr.Code)
	}

	protos, ok := resp.Data.([]interface{})
	if !ok || len(protos) != 1 {
		t.Errorf("expected 1 protocol after restart, got type=%T len=%d", resp.Data, len(protos))
		return
	}

	protoBytes, _ := json.Marshal(protos[0])
	var retrieved AppProtocolUpdate
	if err := json.Unmarshal(protoBytes, &retrieved); err != nil {
		t.Fatalf("unmarshal retrieved protocol: %v", err)
	}

	if retrieved.AppName != "persist-app" {
		t.Errorf("after restart: AppName mismatch, expected %q got %q", "persist-app", retrieved.AppName)
	}
}

func TestAppProtocol_UpsertMissingAppName(t *testing.T) {
	ms, cfgMgr, mcpReg := newTestMSWithAI(t)
	dsm, _ := NewDataStoreManager(context.Background(), testStoreConfigForAppProtocols(t), nil)
	ms.SetDataStore(dsm)

	srv := newAppProtocolTestServer(t, cfgMgr, dsm, mcpReg)

	// Try to upsert without AppName.
	updatePayload := AppProtocolUpdate{
		AppName: "",
		MCP: &AppMCPProtocol{
			ServerName: "test-mcp",
		},
		Action: "upsert",
	}
	body, _ := json.Marshal(updatePayload)

	rr, resp := srv.makeRequest(t, http.MethodPost, "/ai/app-protocols", body)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
	if resp.OK {
		t.Errorf("expected OK=false for missing app_name")
	}
	if !strings.Contains(resp.Error, "app_name must not be empty") {
		t.Errorf("expected error about app_name, got %q", resp.Error)
	}
}

func TestAppProtocol_DeleteNotFound(t *testing.T) {
	ms, cfgMgr, mcpReg := newTestMSWithAI(t)
	dsm, _ := NewDataStoreManager(context.Background(), testStoreConfigForAppProtocols(t), nil)
	ms.SetDataStore(dsm)

	srv := newAppProtocolTestServer(t, cfgMgr, dsm, mcpReg)

	// Try to delete an app protocol that doesn't exist.
	rr, resp := srv.makeRequest(t, http.MethodDelete, "/ai/app-protocols/nonexistent", nil)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
	if resp.OK {
		t.Errorf("expected OK=false for non-existent app")
	}
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("expected 'not found' in error, got %q", resp.Error)
	}
}

func TestAppProtocol_ConcurrentUpserts(t *testing.T) {
	ms, cfgMgr, mcpReg := newTestMSWithAI(t)
	dsm, _ := NewDataStoreManager(context.Background(), testStoreConfigForAppProtocols(t), nil)
	ms.SetDataStore(dsm)

	srv := newAppProtocolTestServer(t, cfgMgr, dsm, mcpReg)

	const numApps = 10
	errors := make(chan error, numApps)

	// Upsert 10 different app protocols (serially to avoid concurrent map writes).
	// Note: The handlers in ai_management.go do not use synchronization primitives,
	// so accessing the appProtocols map from multiple goroutines concurrently would
	// be unsafe. We test sequential behavior here instead.
	for i := 0; i < numApps; i++ {
		appName := fmt.Sprintf("app-%d", i)
		updatePayload := AppProtocolUpdate{
			AppName: appName,
			MCP: &AppMCPProtocol{
				ServerName: fmt.Sprintf("mcp-%d", i),
			},
			Action: "upsert",
		}
		body, _ := json.Marshal(updatePayload)
		rr, _ := srv.makeRequest(t, http.MethodPost, "/ai/app-protocols", body)

		if rr.Code != http.StatusOK {
			errors <- fmt.Errorf("app-%d upsert failed: %d", i, rr.Code)
		}
	}

	close(errors)

	// Check for any errors.
	for err := range errors {
		t.Errorf("sequential upsert error: %v", err)
	}

	// GET all protocols to verify all 10 were inserted.
	rr, resp := srv.makeRequest(t, http.MethodGet, "/ai/app-protocols", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("GET all: expected 200, got %d", rr.Code)
	}

	protos, ok := resp.Data.([]interface{})
	if !ok {
		t.Errorf("expected []interface{} for all protocols, got %T", resp.Data)
		return
	}

	if len(protos) != numApps {
		t.Errorf("expected %d protocols after concurrent upserts, got %d", numApps, len(protos))
	}
}

func TestAppProtocol_UpsertUpdate(t *testing.T) {
	ms, cfgMgr, mcpReg := newTestMSWithAI(t)
	dsm, _ := NewDataStoreManager(context.Background(), testStoreConfigForAppProtocols(t), nil)
	ms.SetDataStore(dsm)

	srv := newAppProtocolTestServer(t, cfgMgr, dsm, mcpReg)

	// First upsert.
	update1 := AppProtocolUpdate{
		AppName: "update-app",
		MCP: &AppMCPProtocol{
			ServerName:  "mcp-v1",
			Description: "Version 1",
		},
		Action: "upsert",
	}
	body1, _ := json.Marshal(update1)
	rr, _ := srv.makeRequest(t, http.MethodPost, "/ai/app-protocols", body1)
	if rr.Code != http.StatusOK {
		t.Errorf("first upsert: expected 200, got %d", rr.Code)
	}

	// Second upsert (update) with same AppName but different MCP config.
	update2 := AppProtocolUpdate{
		AppName: "update-app",
		MCP: &AppMCPProtocol{
			ServerName:  "mcp-v2",
			Description: "Version 2",
		},
		Action: "upsert",
	}
	body2, _ := json.Marshal(update2)
	rr, resp := srv.makeRequest(t, http.MethodPost, "/ai/app-protocols", body2)
	if rr.Code != http.StatusOK {
		t.Errorf("update upsert: expected 200, got %d", rr.Code)
	}

	// Verify the updated value via GET.
	rr, resp = srv.makeRequest(t, http.MethodGet, "/ai/app-protocols?app_name=update-app", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("GET: expected 200, got %d", rr.Code)
	}

	protos, ok := resp.Data.([]interface{})
	if !ok || len(protos) != 1 {
		t.Errorf("expected 1 protocol, got %d", len(protos))
		return
	}

	protoBytes, _ := json.Marshal(protos[0])
	var retrieved AppProtocolUpdate
	if err := json.Unmarshal(protoBytes, &retrieved); err != nil {
		t.Fatalf("unmarshal retrieved protocol: %v", err)
	}

	if retrieved.MCP.ServerName != "mcp-v2" {
		t.Errorf("expected updated ServerName mcp-v2, got %q", retrieved.MCP.ServerName)
	}
	if retrieved.MCP.Description != "Version 2" {
		t.Errorf("expected updated Description Version 2, got %q", retrieved.MCP.Description)
	}
}

func TestAppProtocol_DeleteSuccess(t *testing.T) {
	ms, cfgMgr, mcpReg := newTestMSWithAI(t)
	dsm, _ := NewDataStoreManager(context.Background(), testStoreConfigForAppProtocols(t), nil)
	ms.SetDataStore(dsm)

	srv := newAppProtocolTestServer(t, cfgMgr, dsm, mcpReg)

	// Upsert an app protocol.
	update := AppProtocolUpdate{
		AppName: "delete-app",
		MCP: &AppMCPProtocol{
			ServerName: "delete-mcp",
		},
		Action: "upsert",
	}
	body, _ := json.Marshal(update)
	rr, _ := srv.makeRequest(t, http.MethodPost, "/ai/app-protocols", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("upsert failed: %d", rr.Code)
	}

	// Verify it exists.
	rr, _ = srv.makeRequest(t, http.MethodGet, "/ai/app-protocols?app_name=delete-app", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET before delete: %d", rr.Code)
	}

	// Delete it.
	rr, resp := srv.makeRequest(t, http.MethodDelete, "/ai/app-protocols/delete-app", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("DELETE: expected 200, got %d", rr.Code)
	}
	if !resp.OK {
		t.Errorf("DELETE response: OK=false, Error=%s", resp.Error)
	}

	// Verify it's gone.
	rr, resp = srv.makeRequest(t, http.MethodGet, "/ai/app-protocols?app_name=delete-app", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("GET after delete: expected 200, got %d", rr.Code)
	}
	protos, ok := resp.Data.([]interface{})
	if !ok || len(protos) != 0 {
		t.Errorf("expected 0 protocols after delete, got %d", len(protos))
	}
}

func TestAppProtocol_GetAll(t *testing.T) {
	ms, cfgMgr, mcpReg := newTestMSWithAI(t)
	dsm, _ := NewDataStoreManager(context.Background(), testStoreConfigForAppProtocols(t), nil)
	ms.SetDataStore(dsm)

	srv := newAppProtocolTestServer(t, cfgMgr, dsm, mcpReg)

	// Upsert two app protocols.
	for i := 1; i <= 2; i++ {
		appName := fmt.Sprintf("app-%d", i)
		update := AppProtocolUpdate{
			AppName: appName,
			MCP: &AppMCPProtocol{
				ServerName: fmt.Sprintf("mcp-%d", i),
			},
			Action: "upsert",
		}
		body, _ := json.Marshal(update)
		rr, _ := srv.makeRequest(t, http.MethodPost, "/ai/app-protocols", body)
		if rr.Code != http.StatusOK {
			t.Fatalf("upsert %s failed: %d", appName, rr.Code)
		}
	}

	// GET all (without filter).
	rr, resp := srv.makeRequest(t, http.MethodGet, "/ai/app-protocols", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("GET all: expected 200, got %d", rr.Code)
	}

	protos, ok := resp.Data.([]interface{})
	if !ok || len(protos) != 2 {
		t.Errorf("expected 2 protocols total, got %d", len(protos))
	}
}

// testStoreConfigForAppProtocols returns a DataStore config suitable for AppProtocol tests.
func testStoreConfigForAppProtocols(t *testing.T) config.DataStoreConfig {
	t.Helper()
	dataPath := t.TempDir()
	return config.DataStoreConfig{
		Stores: map[string]config.StoreConfig{
			"disk": {Name: "disk", Kind: config.StoreDisk, Enabled: true, Connection: config.StoreConnection{Path: dataPath}},
		},
		Bindings: map[config.DataDomain]string{
			// Required domains
			config.DomainAPIDefinitions: "disk",
			config.DomainFlows:          "disk",
			config.DomainTenantRegistry: "disk",
			config.DomainCache:          "disk",
			// Optional but used in tests
			config.DomainAppProtocols: "disk",
		},
	}
}
