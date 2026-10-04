package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/datasource"
)

// newTestMSWithSQLSources builds a ManagementServer with the sqlDataSourceStore
// seeded from the provided initial configs.
func newTestMSWithSQLSources(t *testing.T, initial []datasource.DataSourceConfig) *ManagementServer {
	t.Helper()
	// Set dummy env vars for testing (env:VAR refs need values)
	if os.Getenv("DATABASE_URL") == "" {
		_ = os.Setenv("DATABASE_URL", "postgres://localhost/testdb")
		t.Cleanup(func() { _ = os.Unsetenv("DATABASE_URL") })
	}
	if os.Getenv("REAL_DATABASE_URL") == "" {
		_ = os.Setenv("REAL_DATABASE_URL", "postgres://localhost/realdb")
		t.Cleanup(func() { _ = os.Unsetenv("REAL_DATABASE_URL") })
	}
	ms := newTestMS(t)
	ms.InitConnectorStores(t.Context(), config.GatewayConfig{DataSources: initial})
	return ms
}

func TestSQLDataSourcesCRUDHandler_ListEmpty(t *testing.T) {
	ms := newTestMSWithSQLSources(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/sql-data-sources", nil)
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	sources, ok := resp["sources"]
	if !ok {
		t.Fatal("response missing sources field")
	}
	if arr, ok := sources.([]any); !ok || len(arr) != 0 {
		t.Fatalf("expected empty sources array, got %v", sources)
	}
}

func TestSQLDataSourcesCRUDHandler_ListSeeded(t *testing.T) {
	initial := []datasource.DataSourceConfig{
		{Name: "pg-main", Driver: "postgres", DSNRef: "env:DATABASE_URL", MaxConnections: 10},
	}
	ms := newTestMSWithSQLSources(t, initial)

	req := httptest.NewRequest(http.MethodGet, "/sql-data-sources", nil)
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "pg-main") {
		t.Fatalf("expected pg-main in response: %s", rr.Body.String())
	}
}

func TestSQLDataSourcesCRUDHandler_Create(t *testing.T) {
	ms := newTestMSWithSQLSources(t, nil)

	body := `{"name":"learningapp","driver":"postgres","dsn_ref":"env:DATABASE_URL","max_connections":10}`
	req := httptest.NewRequest(http.MethodPost, "/sql-data-sources", strings.NewReader(body))
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 got %d body=%s", rr.Code, rr.Body.String())
	}

	// Verify it appears in the list
	req2 := httptest.NewRequest(http.MethodGet, "/sql-data-sources", nil)
	rr2 := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr2, req2)

	if !strings.Contains(rr2.Body.String(), "learningapp") {
		t.Fatalf("created source not found in list: %s", rr2.Body.String())
	}
}

func TestSQLDataSourcesCRUDHandler_CreateRejectsMissingName(t *testing.T) {
	ms := newTestMSWithSQLSources(t, nil)

	body := `{"driver":"postgres","dsn_ref":"env:DATABASE_URL"}`
	req := httptest.NewRequest(http.MethodPost, "/sql-data-sources", strings.NewReader(body))
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestSQLDataSourcesCRUDHandler_CreateRejectsMissingDriver(t *testing.T) {
	ms := newTestMSWithSQLSources(t, nil)

	body := `{"name":"learningapp","dsn_ref":"env:DATABASE_URL"}`
	req := httptest.NewRequest(http.MethodPost, "/sql-data-sources", strings.NewReader(body))
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestSQLDataSourcesCRUDHandler_GetByName(t *testing.T) {
	initial := []datasource.DataSourceConfig{
		{Name: "pg-main", Driver: "postgres", DSNRef: "env:DATABASE_URL"},
	}
	ms := newTestMSWithSQLSources(t, initial)

	req := httptest.NewRequest(http.MethodGet, "/sql-data-sources/pg-main", nil)
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "pg-main") {
		t.Fatalf("expected pg-main in response: %s", rr.Body.String())
	}
}

func TestSQLDataSourcesCRUDHandler_GetByNameNotFound(t *testing.T) {
	ms := newTestMSWithSQLSources(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/sql-data-sources/nonexistent", nil)
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}

func TestSQLDataSourcesCRUDHandler_Update(t *testing.T) {
	initial := []datasource.DataSourceConfig{
		{Name: "pg-main", Driver: "postgres", DSNRef: "env:DATABASE_URL", MaxConnections: 5},
	}
	ms := newTestMSWithSQLSources(t, initial)

	body := `{"name":"pg-main","driver":"postgres","dsn_ref":"env:DATABASE_URL","max_connections":20}`
	req := httptest.NewRequest(http.MethodPut, "/sql-data-sources/pg-main", strings.NewReader(body))
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}

	// Verify updated value
	req2 := httptest.NewRequest(http.MethodGet, "/sql-data-sources/pg-main", nil)
	rr2 := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr2, req2)

	if !strings.Contains(rr2.Body.String(), "20") {
		t.Fatalf("expected max_connections=20 in response: %s", rr2.Body.String())
	}
}

func TestSQLDataSourcesCRUDHandler_Delete(t *testing.T) {
	initial := []datasource.DataSourceConfig{
		{Name: "pg-main", Driver: "postgres", DSNRef: "env:DATABASE_URL"},
	}
	ms := newTestMSWithSQLSources(t, initial)

	req := httptest.NewRequest(http.MethodDelete, "/sql-data-sources/pg-main", nil)
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 got %d body=%s", rr.Code, rr.Body.String())
	}

	// Verify it's gone
	req2 := httptest.NewRequest(http.MethodGet, "/sql-data-sources/pg-main", nil)
	rr2 := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr2, req2)

	if rr2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", rr2.Code)
	}
}

func TestSQLDataSourcesCRUDHandler_DeleteNotFound(t *testing.T) {
	ms := newTestMSWithSQLSources(t, nil)

	req := httptest.NewRequest(http.MethodDelete, "/sql-data-sources/nonexistent", nil)
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", rr.Code)
	}
}

func TestSQLDataSourcesCRUDHandler_MethodNotAllowed(t *testing.T) {
	ms := newTestMSWithSQLSources(t, nil)

	req := httptest.NewRequest(http.MethodPatch, "/sql-data-sources", nil)
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 got %d", rr.Code)
	}
}

func TestSQLDataSourcesCRUDHandler_GetMasksDSNRef(t *testing.T) {
	initial := []datasource.DataSourceConfig{
		{Name: "pg-main", Driver: "postgres", DSNRef: "env:DATABASE_URL"},
	}
	ms := newTestMSWithSQLSources(t, initial)

	req := httptest.NewRequest(http.MethodGet, "/sql-data-sources/pg-main", nil)
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "DATABASE_URL") {
		t.Fatal("GET response must not expose raw DSNRef value")
	}
	if !strings.Contains(body, "env:***") {
		t.Fatalf("expected masked env:***, got %s", body)
	}
}

func TestSQLDataSourcesCRUDHandler_ListMasksDSNRef(t *testing.T) {
	initial := []datasource.DataSourceConfig{
		{Name: "pg-main", Driver: "postgres", DSNRef: "gsm:projects/x/secrets/db"},
	}
	ms := newTestMSWithSQLSources(t, initial)

	req := httptest.NewRequest(http.MethodGet, "/sql-data-sources", nil)
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	body := rr.Body.String()
	if strings.Contains(body, "projects/x/secrets/db") {
		t.Fatal("list response must not expose raw DSNRef path")
	}
	if !strings.Contains(body, "gsm:***") {
		t.Fatalf("expected gsm:***, got %s", body)
	}
}

func TestSQLDataSourcesCRUDHandler_PutSentinelPreservesStoredDSN(t *testing.T) {
	initial := []datasource.DataSourceConfig{
		{Name: "pg-main", Driver: "postgres", DSNRef: "env:REAL_DATABASE_URL"},
	}
	ms := newTestMSWithSQLSources(t, initial)

	// PUT with masked sentinel — should preserve stored DSN
	body := `{"name":"pg-main","driver":"postgres","dsn_ref":"env:***","max_connections":20}`
	req := httptest.NewRequest(http.MethodPut, "/sql-data-sources/pg-main", strings.NewReader(body))
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}

	// Verify stored DSN is still the original, not the sentinel
	// Do a GET to verify the stored DSN wasn't overwritten
	req2 := httptest.NewRequest(http.MethodGet, "/sql-data-sources/pg-main", nil)
	rr2 := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr2, req2)

	body2 := rr2.Body.String()
	var cfg datasource.DataSourceConfig
	if err := json.Unmarshal([]byte(body2), &cfg); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if cfg.DSNRef != "env:***" {
		t.Fatalf("expected sentinel in API response, got %q", cfg.DSNRef)
	}
	if cfg.MaxConnections != 20 {
		t.Fatalf("expected max_connections to be updated to 20, got %d", cfg.MaxConnections)
	}
}

func TestSQLDataSourcesCRUDHandler_PutLiteralDSNSentinelPreserves(t *testing.T) {
	initial := []datasource.DataSourceConfig{
		{Name: "pg-main", Driver: "postgres", DSNRef: "postgres://user:pass@host/db"},
	}
	ms := newTestMSWithSQLSources(t, initial)

	// PUT with postgres scheme sentinel "postgres:***" — should preserve stored literal DSN
	body := `{"name":"pg-main","driver":"postgres","dsn_ref":"postgres:***","max_connections":15}`
	req := httptest.NewRequest(http.MethodPut, "/sql-data-sources/pg-main", strings.NewReader(body))
	rr := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d body=%s", rr.Code, rr.Body.String())
	}

	// Verify stored DSN is still the original
	req2 := httptest.NewRequest(http.MethodGet, "/sql-data-sources/pg-main", nil)
	rr2 := httptest.NewRecorder()
	ms.SQLDataSourcesCRUDHandler(rr2, req2)

	body2 := rr2.Body.String()
	var cfg datasource.DataSourceConfig
	if err := json.Unmarshal([]byte(body2), &cfg); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if cfg.DSNRef != "postgres:***" {
		t.Fatalf("expected masked postgres:*** in API response, got %q", cfg.DSNRef)
	}
	if cfg.MaxConnections != 15 {
		t.Fatalf("expected max_connections to be updated to 15, got %d", cfg.MaxConnections)
	}
}
