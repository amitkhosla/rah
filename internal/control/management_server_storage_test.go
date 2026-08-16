package control_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"unsafe"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/control"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/storage"
)

// newTestManagerWithProviders creates a config.Manager with the given storage providers.
// Uses unsafe to set the private gateway field (required for testing).
func newTestManagerWithProviders(providers []storage.StorageProviderConfig) *config.Manager {
	gwCfg := config.GatewayConfig{
		StorageProviders: providers,
	}
	mgr := &config.Manager{}
	// Use unsafe to set the private gateway field.
	// This is only acceptable in tests where we have no other way to construct the object.
	*(*config.GatewayConfig)(unsafe.Pointer(uintptr(unsafe.Pointer(mgr)) + unsafe.Sizeof(sync.RWMutex{}))) = gwCfg
	return mgr
}

// TestStorageConnectorsHandlerHappyPath tests that GET /storage-connectors
// returns a JSON response with correctly formatted connector names and types.
func TestStorageConnectorsHandlerHappyPath(t *testing.T) {
	// Create a minimal ManagementServer with a config manager.
	fm := engine.NewFlowManager(32, config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		MaxBoolsSlots: 8,
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1024 * 1024},
	})
	ms := control.NewManagementServer(fm, control.NewCompiler(fm), control.NewNameRegistry(), nil)

	// Create a config manager with two storage providers.
	providers := []storage.StorageProviderConfig{
		{Name: "s3-us-east", Type: "s3"},
		{Name: "gcs-primary", Type: "gcs"},
	}
	cfgMgr := newTestManagerWithProviders(providers)
	ms.SetCfgMgr(cfgMgr)

	// Make GET request to StorageConnectorsHandler.
	req := httptest.NewRequest("GET", "/storage-connectors", nil)
	w := httptest.NewRecorder()
	ms.StorageConnectorsHandler(w, req)

	// Verify HTTP response code.
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify Content-Type header.
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", got)
	}

	// Decode the response body.
	var resp map[string][]map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify the structure and contents.
	connectors, ok := resp["connectors"]
	if !ok {
		t.Fatal("response missing 'connectors' key")
	}

	if len(connectors) != 2 {
		t.Errorf("expected 2 connectors, got %d", len(connectors))
	}

	// Check first connector (s3).
	if len(connectors) > 0 {
		if connectors[0]["name"] != "s3-us-east" {
			t.Errorf("expected first connector name 's3-us-east', got %q", connectors[0]["name"])
		}
		if connectors[0]["type"] != "s3" {
			t.Errorf("expected first connector type 's3', got %q", connectors[0]["type"])
		}
	}

	// Check second connector (gcs).
	if len(connectors) > 1 {
		if connectors[1]["name"] != "gcs-primary" {
			t.Errorf("expected second connector name 'gcs-primary', got %q", connectors[1]["name"])
		}
		if connectors[1]["type"] != "gcs" {
			t.Errorf("expected second connector type 'gcs', got %q", connectors[1]["type"])
		}
	}
}

// TestStorageConnectorsHandlerMethodNotAllowed tests that POST requests
// return 405 Method Not Allowed.
func TestStorageConnectorsHandlerMethodNotAllowed(t *testing.T) {
	fm := engine.NewFlowManager(32, config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		MaxBoolsSlots: 8,
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1024 * 1024},
	})
	ms := control.NewManagementServer(fm, control.NewCompiler(fm), control.NewNameRegistry(), nil)

	// Try POST request.
	req := httptest.NewRequest("POST", "/storage-connectors", nil)
	w := httptest.NewRecorder()
	ms.StorageConnectorsHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

// TestStorageConnectorsHandlerNilConfigManager tests that GET /storage-connectors
// returns an empty connectors array when cfgMgr is nil, without panicking.
func TestStorageConnectorsHandlerNilConfigManager(t *testing.T) {
	fm := engine.NewFlowManager(32, config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		MaxBoolsSlots: 8,
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1024 * 1024},
	})
	ms := control.NewManagementServer(fm, control.NewCompiler(fm), control.NewNameRegistry(), nil)
	// Do NOT set cfgMgr, leaving it nil.

	req := httptest.NewRequest("GET", "/storage-connectors", nil)
	w := httptest.NewRecorder()
	ms.StorageConnectorsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string][]map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	connectors, ok := resp["connectors"]
	if !ok {
		t.Fatal("response missing 'connectors' key")
	}

	if len(connectors) != 0 {
		t.Errorf("expected 0 connectors with nil cfgMgr, got %d", len(connectors))
	}
}

// TestStorageConnectorsHandlerEmptyProviders tests that GET /storage-connectors
// returns an empty connectors array when no providers are configured.
func TestStorageConnectorsHandlerEmptyProviders(t *testing.T) {
	fm := engine.NewFlowManager(32, config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		MaxBoolsSlots: 8,
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1024 * 1024},
	})
	ms := control.NewManagementServer(fm, control.NewCompiler(fm), control.NewNameRegistry(), nil)

	cfgMgr := newTestManagerWithProviders([]storage.StorageProviderConfig{})
	ms.SetCfgMgr(cfgMgr)

	req := httptest.NewRequest("GET", "/storage-connectors", nil)
	w := httptest.NewRecorder()
	ms.StorageConnectorsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string][]map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	connectors, ok := resp["connectors"]
	if !ok {
		t.Fatal("response missing 'connectors' key")
	}

	if len(connectors) != 0 {
		t.Errorf("expected 0 connectors, got %d", len(connectors))
	}
}

// TestStorageConnectorsHandlerMultipleTypes tests that multiple provider types
// (s3, gcs, local) are correctly serialized in the response.
func TestStorageConnectorsHandlerMultipleTypes(t *testing.T) {
	fm := engine.NewFlowManager(32, config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		MaxBoolsSlots: 8,
		DefaultLimits: config.ResourceLimit{MaxBodySize: 1024 * 1024},
	})
	ms := control.NewManagementServer(fm, control.NewCompiler(fm), control.NewNameRegistry(), nil)

	providers := []storage.StorageProviderConfig{
		{Name: "s3-us-east", Type: "s3"},
		{Name: "gcs-primary", Type: "gcs"},
		{Name: "local-fs", Type: "local"},
	}
	cfgMgr := newTestManagerWithProviders(providers)
	ms.SetCfgMgr(cfgMgr)

	req := httptest.NewRequest("GET", "/storage-connectors", nil)
	w := httptest.NewRecorder()
	ms.StorageConnectorsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string][]map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	connectors, ok := resp["connectors"]
	if !ok {
		t.Fatal("response missing 'connectors' key")
	}

	if len(connectors) != 3 {
		t.Errorf("expected 3 connectors, got %d", len(connectors))
	}

	expectedTypes := map[string]string{
		"s3-us-east":   "s3",
		"gcs-primary":  "gcs",
		"local-fs":     "local",
	}

	for _, c := range connectors {
		name := c["name"]
		expectedType, exists := expectedTypes[name]
		if !exists {
			t.Errorf("unexpected connector name: %q", name)
			continue
		}
		if c["type"] != expectedType {
			t.Errorf("connector %q: expected type %q, got %q", name, expectedType, c["type"])
		}
	}
}
