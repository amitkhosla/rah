package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/datastore"
)

func testStoreConfig(t *testing.T) config.DataStoreConfig {
	t.Helper()
	dataPath := t.TempDir()
	return config.DataStoreConfig{
		Stores: map[string]config.StoreConfig{
			"disk": {Name: "disk", Kind: config.StoreDisk, Enabled: true, Connection: config.StoreConnection{Path: dataPath}},
		},
		Bindings: map[config.DataDomain]string{
			config.DomainAPIDefinitions: "disk",
			config.DomainFlows:          "disk",
			config.DomainTenantRegistry: "disk",
			config.DomainCache:          "disk",
			config.DomainCustomerData:   "disk",
			config.DomainInstances:      "disk",
		},
	}
}

func TestDataStoreManagerDataStoreConfigHandlerGet(t *testing.T) {
	mgr, err := NewDataStoreManager(context.Background(), testStoreConfig(t), nil)
	if err != nil {
		t.Fatalf("failed to build manager: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/config/datastores", nil)
	rr := httptest.NewRecorder()
	mgr.DataStoreConfigHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "supported_kinds") {
		t.Fatalf("response missing supported_kinds field")
	}
}

func TestDataStoreManagerDataStoreConfigHandlerPost(t *testing.T) {
	mgr, err := NewDataStoreManager(context.Background(), testStoreConfig(t), nil)
	if err != nil {
		t.Fatalf("failed to build manager: %v", err)
	}

	body := `{
		"stores": {
			"mongo": {"name":"mongo", "kind":"mongodb", "enabled":true}
		},
		"bindings": {
			"api_definitions": "mongo",
			"flows": "mongo",
			"tenant_data": "mongo",
			"cache": "mongo",
			"customer_data": "mongo"
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/config/datastores", strings.NewReader(body))
	rr := httptest.NewRecorder()
	mgr.DataStoreConfigHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDataStoreManagerPutGetTenantScoped(t *testing.T) {
	mgr, err := NewDataStoreManager(context.Background(), testStoreConfig(t), nil)
	if err != nil {
		t.Fatalf("failed to build manager: %v", err)
	}

	ctx := context.Background()
	if err := mgr.Put(ctx, config.DomainCache, datastore.Tenant("tenant-a"), "session", []byte("value-a")); err != nil {
		t.Fatalf("put tenant-a failed: %v", err)
	}
	if err := mgr.Put(ctx, config.DomainCache, datastore.Tenant("tenant-b"), "session", []byte("value-b")); err != nil {
		t.Fatalf("put tenant-b failed: %v", err)
	}

	gotA, ok, err := mgr.Get(ctx, config.DomainCache, datastore.Tenant("tenant-a"), "session")
	if err != nil || !ok || string(gotA) != "value-a" {
		t.Fatalf("tenant-a mismatch ok=%v err=%v val=%q", ok, err, string(gotA))
	}

	gotB, ok, err := mgr.Get(ctx, config.DomainCache, datastore.Tenant("tenant-b"), "session")
	if err != nil || !ok || string(gotB) != "value-b" {
		t.Fatalf("tenant-b mismatch ok=%v err=%v val=%q", ok, err, string(gotB))
	}
}

func TestDataStoreManagerPoolStatsPresent(t *testing.T) {
	mgr, err := NewDataStoreManager(context.Background(), testStoreConfig(t), nil)
	if err != nil {
		t.Fatalf("failed to build manager: %v", err)
	}

	stats := mgr.PoolStats()
	if len(stats) == 0 {
		t.Fatalf("expected pool stats for bound domains")
	}
	if _, ok := stats[config.DomainCache]; !ok {
		t.Fatalf("expected cache domain pool stats")
	}
}

func TestDataStoreManagerRegistryStoreBootstrapRead(t *testing.T) {
	mgr, err := NewDataStoreManager(context.Background(), testStoreConfig(t), nil)
	if err != nil {
		t.Fatalf("failed to build manager: %v", err)
	}
	ctx := context.Background()
	tenant := datastore.Tenant("bootstrap")
	if err := mgr.Put(ctx, config.DomainTenantRegistry, tenant, "a", []byte("1")); err != nil {
		t.Fatalf("put failed: %v", err)
	}
	if err := mgr.Put(ctx, config.DomainTenantRegistry, tenant, "b", []byte("2")); err != nil {
		t.Fatalf("put failed: %v", err)
	}

	snap, err := mgr.ReadRegistryStoreSnapshot(ctx, tenant)
	if err != nil {
		t.Fatalf("snapshot read failed: %v", err)
	}
	if len(snap) != 2 {
		t.Fatalf("expected 2 registry entries, got %d", len(snap))
	}
}

func TestDataStoreManagerInstanceRegistry(t *testing.T) {
	mgr, err := NewDataStoreManager(context.Background(), testStoreConfig(t), nil)
	if err != nil {
		t.Fatalf("failed to build manager: %v", err)
	}
	ctx := context.Background()
	if err := mgr.RegisterInstance(ctx, "i-1", []byte("online")); err != nil {
		t.Fatalf("register instance failed: %v", err)
	}
	instances, err := mgr.ListInstances(ctx)
	if err != nil {
		t.Fatalf("list instances failed: %v", err)
	}
	if len(instances) != 1 || instances[0] != "i-1" {
		t.Fatalf("unexpected instances: %#v", instances)
	}
}

func TestDataStoreManagerGlobalSnapshotsForApisAndFlows(t *testing.T) {
	mgr, err := NewDataStoreManager(context.Background(), testStoreConfig(t), nil)
	if err != nil {
		t.Fatalf("failed to build manager: %v", err)
	}
	ctx := context.Background()

	if err := mgr.PutGlobal(ctx, config.DomainAPIDefinitions, "api:/v1/hello", []byte("def")); err != nil {
		t.Fatalf("put global api failed: %v", err)
	}
	if err := mgr.PutGlobal(ctx, config.DomainFlows, "flow:hello", []byte("instructions")); err != nil {
		t.Fatalf("put global flow failed: %v", err)
	}

	apis, err := mgr.ReadAPIDefinitionsSnapshot(ctx)
	if err != nil {
		t.Fatalf("read api snapshot failed: %v", err)
	}
	flows, err := mgr.ReadFlowsSnapshot(ctx)
	if err != nil {
		t.Fatalf("read flow snapshot failed: %v", err)
	}
	if len(apis) != 1 || string(apis["api:/v1/hello"]) != "def" {
		t.Fatalf("unexpected apis snapshot: %#v", apis)
	}
	if len(flows) != 1 || string(flows["flow:hello"]) != "instructions" {
		t.Fatalf("unexpected flows snapshot: %#v", flows)
	}
}

func TestDataStoreManagerUpdateRejectsStartupDomainChange(t *testing.T) {
	mgr, err := NewDataStoreManager(context.Background(), testStoreConfig(t), nil)
	if err != nil {
		t.Fatalf("failed to build manager: %v", err)
	}

	cfg := testStoreConfig(t)
	cfg.Stores["alt"] = config.StoreConfig{Name: "alt", Kind: config.StoreMongoDB, Enabled: true}
	cfg.Bindings[config.DomainAPIDefinitions] = "alt"

	if err := mgr.Update(context.Background(), cfg); err == nil {
		t.Fatalf("expected startup domain update rejection")
	}
}

func TestDataStoreManagerUpdateAllowsCacheChange(t *testing.T) {
	mgr, err := NewDataStoreManager(context.Background(), testStoreConfig(t), nil)
	if err != nil {
		t.Fatalf("failed to build manager: %v", err)
	}

	cfg := testStoreConfig(t)
	cfg.Stores["alt"] = config.StoreConfig{Name: "alt", Kind: config.StoreMongoDB, Enabled: true}
	cfg.Bindings[config.DomainCache] = "alt"

	if err := mgr.Update(context.Background(), cfg); err != nil {
		t.Fatalf("expected cache update to succeed: %v", err)
	}
}

func TestDataStoreManagerDataStoreConfigHandlerPostMutableDomain(t *testing.T) {
	mgr, err := NewDataStoreManager(context.Background(), testStoreConfig(t), nil)
	if err != nil {
		t.Fatalf("failed to build manager: %v", err)
	}

	body := `{
		"stores": {
			"disk": {"name":"disk", "kind":"disk", "enabled":true},
			"mongo": {"name":"mongo", "kind":"mongodb", "enabled":true}
		},
		"bindings": {
			"api_definitions": "disk",
			"flows": "disk",
			"tenant_data": "disk",
			"cache": "mongo",
			"customer_data": "mongo"
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/config/datastores", strings.NewReader(body))
	rr := httptest.NewRecorder()
	mgr.DataStoreConfigHandler(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 got %d body=%s", rr.Code, rr.Body.String())
	}

	cfg := mgr.Snapshot()
	if cfg.Bindings[config.DomainCache] != "mongo" {
		t.Fatalf("expected cache binding to update")
	}
}
