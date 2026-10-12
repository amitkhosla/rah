package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amitkhosla/rah/internal/appversion"
)

// ---- Tier 1: InMemoryAppVersionStore Tests ----

// TestInMemoryStore_VersionRoundTrip tests that we can upsert and retrieve a version.
func TestInMemoryStore_VersionRoundTrip(t *testing.T) {
	store := newInMemoryAppVersionStore()

	v := appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "draft",
	}

	if err := store.UpsertVersion(v); err != nil {
		t.Fatalf("UpsertVersion: %v", err)
	}

	retrieved, err := store.GetVersion("orders-v1")
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}

	if retrieved.AppName != v.AppName {
		t.Errorf("AppName mismatch: got %q, want %q", retrieved.AppName, v.AppName)
	}
	if retrieved.LogicalApp != v.LogicalApp {
		t.Errorf("LogicalApp mismatch: got %q, want %q", retrieved.LogicalApp, v.LogicalApp)
	}
	if retrieved.Version != v.Version {
		t.Errorf("Version mismatch: got %q, want %q", retrieved.Version, v.Version)
	}
	if retrieved.Status != v.Status {
		t.Errorf("Status mismatch: got %q, want %q", retrieved.Status, v.Status)
	}
	if retrieved.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
	if retrieved.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set")
	}
}

// TestInMemoryStore_RoutingRoundTrip tests upsert and list of routing rules.
func TestInMemoryStore_RoutingRoundTrip(t *testing.T) {
	store := newInMemoryAppVersionStore()

	tenantID := uint16(42)
	rule := appversion.RoutingRule{
		TenantID:    &tenantID,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	}

	if err := store.UpsertRoutingRule(rule); err != nil {
		t.Fatalf("UpsertRoutingRule: %v", err)
	}

	rules, err := store.ListRoutingRules("orders")
	if err != nil {
		t.Fatalf("ListRoutingRules: %v", err)
	}

	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	retrieved := rules[0]
	if retrieved.TenantID == nil || *retrieved.TenantID != tenantID {
		t.Errorf("TenantID mismatch: got %v, want %d", retrieved.TenantID, tenantID)
	}
	if retrieved.LogicalApp != rule.LogicalApp {
		t.Errorf("LogicalApp mismatch: got %q, want %q", retrieved.LogicalApp, rule.LogicalApp)
	}
	if retrieved.ActiveApp != rule.ActiveApp {
		t.Errorf("ActiveApp mismatch: got %q, want %q", retrieved.ActiveApp, rule.ActiveApp)
	}
	if retrieved.Environment != rule.Environment {
		t.Errorf("Environment mismatch: got %q, want %q", retrieved.Environment, rule.Environment)
	}
}

// TestInMemoryStore_Promote tests creating a default (nil-tenant) routing rule.
func TestInMemoryStore_Promote(t *testing.T) {
	store := newInMemoryAppVersionStore()

	// First, upsert a version
	v := appversion.AppVersion{
		AppName:    "orders-v2",
		LogicalApp: "orders",
		Version:    "v2",
		Status:     "draft",
	}
	if err := store.UpsertVersion(v); err != nil {
		t.Fatalf("UpsertVersion: %v", err)
	}

	// Promote by upserting a nil-tenant rule
	rule := appversion.RoutingRule{
		TenantID:    nil, // default rule
		LogicalApp:  "orders",
		ActiveApp:   "orders-v2",
		Environment: "production",
	}
	if err := store.UpsertRoutingRule(rule); err != nil {
		t.Fatalf("UpsertRoutingRule: %v", err)
	}

	rules, err := store.ListRoutingRules("orders")
	if err != nil {
		t.Fatalf("ListRoutingRules: %v", err)
	}

	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	if rules[0].TenantID != nil {
		t.Errorf("TenantID should be nil for default rule, got %v", rules[0].TenantID)
	}
	if rules[0].ActiveApp != "orders-v2" {
		t.Errorf("ActiveApp should be orders-v2, got %q", rules[0].ActiveApp)
	}
}

// ---- Tier 2: Handler Tests ----

// TestHandlerListVersions tests GET /api/apps/{app}/versions returns all versions for the logical app.
func TestHandlerListVersions(t *testing.T) {
	store := newInMemoryAppVersionStore()
	srv := &Server{
		AppVersions: store,
	}

	// Upsert two versions for the same logical app
	v1 := appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "draft",
	}
	v2 := appversion.AppVersion{
		AppName:    "orders-v2",
		LogicalApp: "orders",
		Version:    "v2",
		Status:     "draft",
	}

	if err := store.UpsertVersion(v1); err != nil {
		t.Fatalf("UpsertVersion v1: %v", err)
	}
	if err := store.UpsertVersion(v2); err != nil {
		t.Fatalf("UpsertVersion v2: %v", err)
	}

	// Create a test request
	req := httptest.NewRequest("GET", "/api/apps/orders/versions", nil)
	w := httptest.NewRecorder()

	// Call handler directly
	srv.handleListVersions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var versions []appversion.AppVersion
	if err := json.Unmarshal(w.Body.Bytes(), &versions); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(versions))
	}
}

// TestHandlerCreateVersion tests POST /api/apps/{app}/versions creates a new draft version.
func TestHandlerCreateVersion(t *testing.T) {
	store := newInMemoryAppVersionStore()
	draftStore := &InMemoryDraftStore{drafts: make(map[string]AppDraft)}

	// Create minimal source draft so CloneAppVersion doesn't fail
	sourceDraft := AppDraft{
		AppName:   "orders-v1",
		UpdatedAt: time.Now(),
		APIs:      json.RawMessage("[]"),
		Flows:     json.RawMessage("[]"),
	}
	draftStore.drafts["orders-v1"] = sourceDraft

	srv := &Server{
		AppVersions: store,
		draftStore:  draftStore,
	}

	body := bytes.NewBufferString(`{"source_version":"v1","target_version":"v2","changelog":"initial"}`)
	req := httptest.NewRequest("POST", "/api/apps/orders/versions", body)
	w := httptest.NewRecorder()

	srv.handleCreateVersion(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var result appversion.AppVersion
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if result.AppName != "orders-v2" {
		t.Errorf("expected appName orders-v2, got %q", result.AppName)
	}
	if result.Status != "draft" {
		t.Errorf("expected status draft, got %q", result.Status)
	}

	// Verify it's in the store
	stored, err := store.GetVersion("orders-v2")
	if err != nil {
		t.Fatalf("version not stored: %v", err)
	}
	if stored.Status != "draft" {
		t.Errorf("stored version status should be draft, got %q", stored.Status)
	}
}

// TestHandlerUpsertRouting tests PUT /api/apps/{app}/routing upserts routing rules.
func TestHandlerUpsertRouting(t *testing.T) {
	store := newInMemoryAppVersionStore()
	srv := &Server{
		AppVersions: store,
	}

	// Create a routing rule request
	rule := appversion.RoutingRule{
		TenantID:    nil,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	}

	bodyData := map[string][]appversion.RoutingRule{
		"rules": {rule},
	}
	bodyBytes, _ := json.Marshal(bodyData)
	body := bytes.NewBuffer(bodyBytes)

	req := httptest.NewRequest("PUT", "/api/apps/orders/routing", body)
	w := httptest.NewRecorder()

	srv.handleUpsertRouting(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify rule was stored
	rules, err := store.ListRoutingRules("orders")
	if err != nil {
		t.Fatalf("ListRoutingRules: %v", err)
	}

	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	if rules[0].ActiveApp != "orders-v1" {
		t.Errorf("expected activeApp orders-v1, got %q", rules[0].ActiveApp)
	}
}

// TestHandlerPromote tests POST /api/apps/{app}/versions/{ver}/promote creates a default routing rule.
func TestHandlerPromote(t *testing.T) {
	store := newInMemoryAppVersionStore()
	srv := &Server{
		AppVersions: store,
	}

	// Create a version first
	v := appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "draft",
	}
	if err := store.UpsertVersion(v); err != nil {
		t.Fatalf("UpsertVersion: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/apps/orders/versions/v1/promote", nil)
	w := httptest.NewRecorder()

	srv.handlePromoteVersion(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify the default rule was created
	rules, err := store.ListRoutingRules("orders")
	if err != nil {
		t.Fatalf("ListRoutingRules: %v", err)
	}

	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	if rules[0].TenantID != nil {
		t.Errorf("TenantID should be nil, got %v", rules[0].TenantID)
	}
	if rules[0].ActiveApp != "orders-v1" {
		t.Errorf("expected activeApp orders-v1, got %q", rules[0].ActiveApp)
	}
}

// ---- Tier 2: Negative / Error Path Tests ----

// TestHandlerCreateVersion_DuplicateVersion tests creating the same version twice returns 409.
func TestHandlerCreateVersion_DuplicateVersion(t *testing.T) {
	store := newInMemoryAppVersionStore()
	draftStore := &InMemoryDraftStore{drafts: make(map[string]AppDraft)}

	// Create minimal source draft
	sourceDraft := AppDraft{
		AppName:   "orders-v1",
		UpdatedAt: time.Now(),
		APIs:      json.RawMessage("[]"),
		Flows:     json.RawMessage("[]"),
	}
	draftStore.drafts["orders-v1"] = sourceDraft

	srv := &Server{
		AppVersions: store,
		draftStore:  draftStore,
	}

	// Create the first version
	body1 := bytes.NewBufferString(`{"source_version":"v1","target_version":"v2","changelog":"first"}`)
	req1 := httptest.NewRequest("POST", "/api/apps/orders/versions", body1)
	w1 := httptest.NewRecorder()
	srv.handleCreateVersion(w1, req1)

	if w1.Code != http.StatusCreated {
		t.Fatalf("first create should succeed, got %d", w1.Code)
	}

	// Try to create the same version again
	body2 := bytes.NewBufferString(`{"source_version":"v1","target_version":"v2","changelog":"second"}`)
	req2 := httptest.NewRequest("POST", "/api/apps/orders/versions", body2)
	w2 := httptest.NewRecorder()
	srv.handleCreateVersion(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w2.Code)
	}

	if !bytes.Contains(w2.Body.Bytes(), []byte("already exists")) {
		t.Errorf("error message should contain 'already exists', got: %s", w2.Body.String())
	}
}

// TestHandlerCreateVersion_MissingFields tests POST with empty source_version returns 400.
func TestHandlerCreateVersion_MissingFields(t *testing.T) {
	store := newInMemoryAppVersionStore()
	srv := &Server{
		AppVersions: store,
	}

	body := bytes.NewBufferString(`{"source_version":"","target_version":"v2"}`)
	req := httptest.NewRequest("POST", "/api/apps/orders/versions", body)
	w := httptest.NewRecorder()

	srv.handleCreateVersion(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// TestHandlerRetireVersion_WithActiveRouting tests retiring a version with an active routing rule returns 409.
func TestHandlerRetireVersion_WithActiveRouting(t *testing.T) {
	store := newInMemoryAppVersionStore()
	srv := &Server{
		AppVersions: store,
	}

	// Create a version
	v := appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "draft",
	}
	if err := store.UpsertVersion(v); err != nil {
		t.Fatalf("UpsertVersion: %v", err)
	}

	// Create a routing rule pointing to it
	rule := appversion.RoutingRule{
		TenantID:    nil,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	}
	if err := store.UpsertRoutingRule(rule); err != nil {
		t.Fatalf("UpsertRoutingRule: %v", err)
	}

	// Try to retire the version
	retireBody := bytes.NewBufferString(`{"status":"retired"}`)
	req := httptest.NewRequest("PUT", "/api/apps/orders/versions/v1/status", retireBody)
	w := httptest.NewRecorder()

	srv.handleUpdateVersionStatus(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d; body: %s", w.Code, w.Body.String())
	}

	if !bytes.Contains(w.Body.Bytes(), []byte("routing rule")) {
		t.Errorf("error message should mention routing rule, got: %s", w.Body.String())
	}
}

// TestHandlerUpsertRouting_MissingActiveApp tests upserting a rule with empty active_app returns 400.
func TestHandlerUpsertRouting_MissingActiveApp(t *testing.T) {
	store := newInMemoryAppVersionStore()
	srv := &Server{
		AppVersions: store,
	}

	rule := appversion.RoutingRule{
		TenantID:    nil,
		LogicalApp:  "orders",
		ActiveApp:   "", // missing
		Environment: "production",
	}

	bodyData := map[string][]appversion.RoutingRule{
		"rules": {rule},
	}
	bodyBytes, _ := json.Marshal(bodyData)
	body := bytes.NewBuffer(bodyBytes)

	req := httptest.NewRequest("PUT", "/api/apps/orders/routing", body)
	w := httptest.NewRecorder()

	srv.handleUpsertRouting(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

// TestHandlerPromote_VersionNotFound tests promoting a non-existent version returns 404.
func TestHandlerPromote_VersionNotFound(t *testing.T) {
	store := newInMemoryAppVersionStore()
	srv := &Server{
		AppVersions: store,
	}

	req := httptest.NewRequest("POST", "/api/apps/orders/versions/v999/promote", nil)
	w := httptest.NewRecorder()

	srv.handlePromoteVersion(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d; body: %s", w.Code, w.Body.String())
	}
}

// ---- Tier 3: File Persistence & Idempotency Tests ----

// TestFileStore_PersistenceRoundTrip tests that data persists across store instances.
func TestFileStore_PersistenceRoundTrip(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Create first store and add data
	store1, err := newFileAppVersionStore(tmpDir)
	if err != nil {
		t.Fatalf("newFileAppVersionStore: %v", err)
	}

	v := appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "draft",
	}
	if err := store1.UpsertVersion(v); err != nil {
		t.Fatalf("UpsertVersion: %v", err)
	}

	rule := appversion.RoutingRule{
		TenantID:    nil,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	}
	if err := store1.UpsertRoutingRule(rule); err != nil {
		t.Fatalf("UpsertRoutingRule: %v", err)
	}

	// Create a new store instance pointing to same directory
	store2, err := newFileAppVersionStore(tmpDir)
	if err != nil {
		t.Fatalf("newFileAppVersionStore (second): %v", err)
	}

	// Verify version is still there
	retrieved, err := store2.GetVersion("orders-v1")
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if retrieved.AppName != "orders-v1" {
		t.Errorf("version not persisted; got %q", retrieved.AppName)
	}

	// Verify rule is still there
	rules, err := store2.ListRoutingRules("orders")
	if err != nil {
		t.Fatalf("ListRoutingRules: %v", err)
	}
	if len(rules) != 1 {
		t.Errorf("rule not persisted; got %d rules", len(rules))
	}
}

// TestCloneBlastRadius tests that cloning a version doesn't modify the source draft.
func TestCloneBlastRadius(t *testing.T) {
	draftStore := &InMemoryDraftStore{drafts: make(map[string]AppDraft)}

	srv := &Server{
		AppVersions: newInMemoryAppVersionStore(),
		draftStore:  draftStore,
	}

	// Create a source draft with some data
	sourceData := `[{"name":"GetUser","app_name":"orders-v1","path":"/user/{id}"}]`
	sourceDraft := AppDraft{
		AppName:   "orders-v1",
		UpdatedAt: time.Now(),
		APIs:      json.RawMessage(sourceData),
		Flows:     json.RawMessage("[]"),
	}
	draftStore.drafts["orders-v1"] = sourceDraft

	// Record original source draft state
	originalSourceData := sourceDraft.APIs

	// Clone to orders-v2
	if err := srv.CloneAppVersion(context.Background(), "orders-v1", "orders-v2"); err != nil {
		t.Fatalf("CloneAppVersion: %v", err)
	}

	// Verify source draft was NOT modified
	sourceAfter := draftStore.drafts["orders-v1"]
	if !bytes.Equal(sourceAfter.APIs, originalSourceData) {
		t.Errorf("source draft was modified! before: %s, after: %s", originalSourceData, sourceAfter.APIs)
	}

	// Verify target draft was created with rewritten references
	targetDraft, ok := draftStore.drafts["orders-v2"]
	if !ok {
		t.Fatalf("cloned draft not created")
	}

	// Verify app names are rewritten but source stays the same
	if !bytes.Contains(targetDraft.APIs, []byte("orders-v2")) {
		t.Error("target draft should have orders-v2 references")
	}
	// Source should still have v1 references
	if !bytes.Contains(sourceAfter.APIs, []byte("orders-v1")) {
		t.Error("source draft should still have orders-v1 references")
	}
}

// TestPromote_Idempotent tests that promoting twice creates only one default rule.
func TestPromote_Idempotent(t *testing.T) {
	store := newInMemoryAppVersionStore()
	srv := &Server{
		AppVersions: store,
	}

	// Create a version
	v := appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "draft",
	}
	if err := store.UpsertVersion(v); err != nil {
		t.Fatalf("UpsertVersion: %v", err)
	}

	// Promote once
	req1 := httptest.NewRequest("POST", "/api/apps/orders/versions/v1/promote", nil)
	w1 := httptest.NewRecorder()
	srv.handlePromoteVersion(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("first promote failed: %d", w1.Code)
	}

	// Promote again (idempotent)
	req2 := httptest.NewRequest("POST", "/api/apps/orders/versions/v1/promote", nil)
	w2 := httptest.NewRecorder()
	srv.handlePromoteVersion(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("second promote failed: %d", w2.Code)
	}

	// Verify there's still only one rule
	rules, err := store.ListRoutingRules("orders")
	if err != nil {
		t.Fatalf("ListRoutingRules: %v", err)
	}

	if len(rules) != 1 {
		t.Errorf("expected 1 rule after two promotes, got %d", len(rules))
	}
}

// ---- Additional edge case tests ----

// TestInMemoryStore_MultipleVersionsListed tests filtering by logical app.
func TestInMemoryStore_MultipleVersionsListed(t *testing.T) {
	store := newInMemoryAppVersionStore()

	// Add versions for two different apps
	v1 := appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "draft",
	}
	v2 := appversion.AppVersion{
		AppName:    "orders-v2",
		LogicalApp: "orders",
		Version:    "v2",
		Status:     "draft",
	}
	v3 := appversion.AppVersion{
		AppName:    "payments-v1",
		LogicalApp: "payments",
		Version:    "v1",
		Status:     "draft",
	}

	for _, v := range []appversion.AppVersion{v1, v2, v3} {
		if err := store.UpsertVersion(v); err != nil {
			t.Fatalf("UpsertVersion: %v", err)
		}
	}

	// List orders should return only v1 and v2
	ordersVersions, err := store.ListVersions("orders")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}

	if len(ordersVersions) != 2 {
		t.Errorf("expected 2 orders versions, got %d", len(ordersVersions))
	}

	// List payments should return only v1
	paymentsVersions, err := store.ListVersions("payments")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}

	if len(paymentsVersions) != 1 {
		t.Errorf("expected 1 payments version, got %d", len(paymentsVersions))
	}
}

// TestInMemoryStore_UpdateVersionPreservesCreatedAt tests that updating a version preserves CreatedAt.
func TestInMemoryStore_UpdateVersionPreservesCreatedAt(t *testing.T) {
	store := newInMemoryAppVersionStore()

	v := appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "draft",
	}

	// First insert
	if err := store.UpsertVersion(v); err != nil {
		t.Fatalf("UpsertVersion: %v", err)
	}

	first, _ := store.GetVersion("orders-v1")
	originalCreatedAt := first.CreatedAt

	// Sleep a tiny bit and update
	time.Sleep(10 * time.Millisecond)

	v.Status = "active"
	if err := store.UpsertVersion(v); err != nil {
		t.Fatalf("UpsertVersion (update): %v", err)
	}

	second, _ := store.GetVersion("orders-v1")

	if second.CreatedAt != originalCreatedAt {
		t.Errorf("CreatedAt should not change on update, was %v, became %v", originalCreatedAt, second.CreatedAt)
	}

	if second.UpdatedAt.Before(first.UpdatedAt) {
		t.Error("UpdatedAt should move forward on update")
	}
}

// TestInMemoryStore_RetireVersion tests the retire operation.
func TestInMemoryStore_RetireVersion(t *testing.T) {
	store := newInMemoryAppVersionStore()

	v := appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "draft",
	}

	if err := store.UpsertVersion(v); err != nil {
		t.Fatalf("UpsertVersion: %v", err)
	}

	if err := store.RetireVersion("orders-v1"); err != nil {
		t.Fatalf("RetireVersion: %v", err)
	}

	retrieved, err := store.GetVersion("orders-v1")
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}

	if retrieved.Status != "retired" {
		t.Errorf("expected status 'retired', got %q", retrieved.Status)
	}
}

// TestInMemoryStore_RetireNonExistent tests retiring a non-existent version fails.
func TestInMemoryStore_RetireNonExistent(t *testing.T) {
	store := newInMemoryAppVersionStore()

	err := store.RetireVersion("nonexistent-v1")
	if err == nil {
		t.Fatal("RetireVersion should fail for non-existent version")
	}
}

// TestInMemoryStore_DeleteRoutingRule tests deletion of routing rules.
func TestInMemoryStore_DeleteRoutingRule(t *testing.T) {
	store := newInMemoryAppVersionStore()

	tenantID := uint16(42)
	rule := appversion.RoutingRule{
		TenantID:    &tenantID,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	}

	if err := store.UpsertRoutingRule(rule); err != nil {
		t.Fatalf("UpsertRoutingRule: %v", err)
	}

	if err := store.DeleteRoutingRule(&tenantID, "orders", "production"); err != nil {
		t.Fatalf("DeleteRoutingRule: %v", err)
	}

	rules, err := store.ListRoutingRules("orders")
	if err != nil {
		t.Fatalf("ListRoutingRules: %v", err)
	}

	if len(rules) != 0 {
		t.Errorf("expected 0 rules after delete, got %d", len(rules))
	}
}

// TestInMemoryStore_UpdateRoutingRuleIdempotent tests that upserting the same rule twice results in one rule.
func TestInMemoryStore_UpdateRoutingRuleIdempotent(t *testing.T) {
	store := newInMemoryAppVersionStore()

	rule := appversion.RoutingRule{
		TenantID:    nil,
		LogicalApp:  "orders",
		ActiveApp:   "orders-v1",
		Environment: "production",
	}

	if err := store.UpsertRoutingRule(rule); err != nil {
		t.Fatalf("UpsertRoutingRule (first): %v", err)
	}

	// Update to point to v2
	rule.ActiveApp = "orders-v2"
	if err := store.UpsertRoutingRule(rule); err != nil {
		t.Fatalf("UpsertRoutingRule (second): %v", err)
	}

	rules, err := store.ListRoutingRules("orders")
	if err != nil {
		t.Fatalf("ListRoutingRules: %v", err)
	}

	if len(rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(rules))
	}

	if rules[0].ActiveApp != "orders-v2" {
		t.Errorf("expected activeApp to be orders-v2, got %q", rules[0].ActiveApp)
	}
}

// TestFileStore_MultipleOperations tests file persistence with multiple operations.
func TestFileStore_MultipleOperations(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := newFileAppVersionStore(tmpDir)
	if err != nil {
		t.Fatalf("newFileAppVersionStore: %v", err)
	}

	// Add a version
	v1 := appversion.AppVersion{
		AppName:    "orders-v1",
		LogicalApp: "orders",
		Version:    "v1",
		Status:     "draft",
	}
	if err := store.UpsertVersion(v1); err != nil {
		t.Fatalf("UpsertVersion: %v", err)
	}

	// Retire it
	if err := store.RetireVersion("orders-v1"); err != nil {
		t.Fatalf("RetireVersion: %v", err)
	}

	// Add a new version
	v2 := appversion.AppVersion{
		AppName:    "orders-v2",
		LogicalApp: "orders",
		Version:    "v2",
		Status:     "draft",
	}
	if err := store.UpsertVersion(v2); err != nil {
		t.Fatalf("UpsertVersion: %v", err)
	}

	// Verify file exists
	filePath := filepath.Join(tmpDir, "app_versions.json")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("app_versions.json not created: %v", err)
	}

	// Load fresh store and verify
	store2, err := newFileAppVersionStore(tmpDir)
	if err != nil {
		t.Fatalf("newFileAppVersionStore (second): %v", err)
	}

	v1Retrieved, err := store2.GetVersion("orders-v1")
	if err != nil {
		t.Fatalf("GetVersion orders-v1: %v", err)
	}
	if v1Retrieved.Status != "retired" {
		t.Errorf("expected retired, got %q", v1Retrieved.Status)
	}

	v2Retrieved, err := store2.GetVersion("orders-v2")
	if err != nil {
		t.Fatalf("GetVersion orders-v2: %v", err)
	}
	if v2Retrieved.Status != "draft" {
		t.Errorf("expected draft, got %q", v2Retrieved.Status)
	}
}
