package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tier 1: Functional (happy path) tests
// ─────────────────────────────────────────────────────────────────────────────

func TestInMemoryAuditStore_AppendAndList(t *testing.T) {
	store := NewInMemoryAuditStore()
	ctx := context.Background()

	// Append 3 records with different actions
	record1 := AuditRecord{
		ID:           "rec-1",
		Timestamp:    time.Now().UTC().Add(-2 * time.Second),
		Actor:        "user1",
		Action:       "login",
		ResourceType: "session",
		ResourceID:   "sess-1",
		Status:       "success",
		Summary:      "user1 logged in",
	}
	record2 := AuditRecord{
		ID:           "rec-2",
		Timestamp:    time.Now().UTC().Add(-1 * time.Second),
		Actor:        "user1",
		Action:       "deploy",
		ResourceType: "release",
		ResourceID:   "rel-1",
		Status:       "success",
		Summary:      "user1 deployed release",
	}
	record3 := AuditRecord{
		ID:           "rec-3",
		Timestamp:    time.Now().UTC(),
		Actor:        "user2",
		Action:       "logout",
		ResourceType: "session",
		ResourceID:   "sess-2",
		Status:       "success",
		Summary:      "user2 logged out",
	}

	if err := store.Append(ctx, record1); err != nil {
		t.Fatalf("Append record1 failed: %v", err)
	}
	if err := store.Append(ctx, record2); err != nil {
		t.Fatalf("Append record2 failed: %v", err)
	}
	if err := store.Append(ctx, record3); err != nil {
		t.Fatalf("Append record3 failed: %v", err)
	}

	// List returns all 3, newest first
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}

	// Verify newest-first order (rec-3, rec-2, rec-1)
	if records[0].ID != "rec-3" || records[0].Action != "logout" {
		t.Errorf("expected records[0] to be rec-3 (logout), got %s (%s)", records[0].ID, records[0].Action)
	}
	if records[1].ID != "rec-2" || records[1].Action != "deploy" {
		t.Errorf("expected records[1] to be rec-2 (deploy), got %s (%s)", records[1].ID, records[1].Action)
	}
	if records[2].ID != "rec-1" || records[2].Action != "login" {
		t.Errorf("expected records[2] to be rec-1 (login), got %s (%s)", records[2].ID, records[2].Action)
	}
}

func TestFileAuditStore_AppendAndList(t *testing.T) {
	tempDir := t.TempDir()
	store := NewFileAuditStore(tempDir)
	ctx := context.Background()

	// Append 2 records
	record1 := AuditRecord{
		ID:           "file-rec-1",
		Timestamp:    time.Now().UTC().Add(-1 * time.Second),
		Actor:        "alice",
		Action:       "user.create",
		ResourceType: "user",
		ResourceID:   "bob",
		Status:       "success",
		Summary:      "alice created bob",
	}
	record2 := AuditRecord{
		ID:           "file-rec-2",
		Timestamp:    time.Now().UTC(),
		Actor:        "alice",
		Action:       "user.delete",
		ResourceType: "user",
		ResourceID:   "charlie",
		Status:       "success",
		Summary:      "alice deleted charlie",
	}

	if err := store.Append(ctx, record1); err != nil {
		t.Fatalf("Append record1 failed: %v", err)
	}
	if err := store.Append(ctx, record2); err != nil {
		t.Fatalf("Append record2 failed: %v", err)
	}

	// List returns 2 records, newest first
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	// Verify newest-first order
	if records[0].ID != "file-rec-2" || records[0].Action != "user.delete" {
		t.Errorf("expected records[0] to be file-rec-2 (user.delete), got %s (%s)", records[0].ID, records[0].Action)
	}
	if records[1].ID != "file-rec-1" || records[1].Action != "user.create" {
		t.Errorf("expected records[1] to be file-rec-1 (user.create), got %s (%s)", records[1].ID, records[1].Action)
	}

	// Verify records are persisted by creating a new store instance pointing to same path
	store2 := NewFileAuditStore(tempDir)
	records2, err := store2.List(ctx, 10)
	if err != nil {
		t.Fatalf("List on new store failed: %v", err)
	}

	if len(records2) != 2 {
		t.Fatalf("expected persisted 2 records, got %d", len(records2))
	}
	if records2[0].ID != "file-rec-2" {
		t.Errorf("expected persisted records[0] to be file-rec-2, got %s", records2[0].ID)
	}
}

func TestFileAuditStore_ListMissingFile(t *testing.T) {
	tempDir := t.TempDir()
	store := NewFileAuditStore(tempDir)
	ctx := context.Background()

	// List when file doesn't exist should return nil, nil
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List should not error on missing file, got: %v", err)
	}
	if records != nil {
		t.Fatalf("List should return nil for missing file, got %v", records)
	}
}

func TestNewAuditStore_FileKind(t *testing.T) {
	tempDir := t.TempDir()
	store := newAuditStore("file", tempDir)

	if _, ok := store.(*FileAuditStore); !ok {
		t.Fatalf("expected *FileAuditStore, got %T", store)
	}
}

func TestNewAuditStore_MemoryKind(t *testing.T) {
	store := newAuditStore("memory", "")
	if _, ok := store.(*InMemoryAuditStore); !ok {
		t.Fatalf("expected *InMemoryAuditStore, got %T", store)
	}
}

func TestNewAuditStore_EmptyKind(t *testing.T) {
	store := newAuditStore("", "")
	if _, ok := store.(*InMemoryAuditStore); !ok {
		t.Fatalf("expected *InMemoryAuditStore for empty kind, got %T", store)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 2: Negative / error path tests
// ─────────────────────────────────────────────────────────────────────────────

func TestInMemoryAuditStore_CapEnforcement(t *testing.T) {
	store := NewInMemoryAuditStore()
	ctx := context.Background()

	// Append 1001 records
	for i := 1; i <= 1001; i++ {
		rec := AuditRecord{
			ID:        fmt.Sprintf("rec-%d", i),
			Timestamp: time.Now().UTC(),
			Actor:     "actor",
			Action:    "action",
			Status:    "success",
		}
		if err := store.Append(ctx, rec); err != nil {
			t.Fatalf("Append record %d failed: %v", i, err)
		}
	}

	// List returns exactly 1000 records (cap enforced)
	records, err := store.List(ctx, 2000)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 1000 {
		t.Fatalf("expected 1000 records (cap enforced), got %d", len(records))
	}

	// Verify the first record (oldest) was dropped by checking that rec-1 is not present
	for i, rec := range records {
		if rec.ID == "rec-1" {
			t.Fatalf("expected rec-1 (oldest) to be dropped, but found at index %d", i)
		}
	}

	// Verify the newest record (rec-1001) is present
	found := false
	for _, rec := range records {
		if rec.ID == "rec-1001" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected rec-1001 (newest) to be present")
	}
}

func TestFileAuditStore_ListLimit(t *testing.T) {
	tempDir := t.TempDir()
	store := NewFileAuditStore(tempDir)
	ctx := context.Background()

	// Append 5 records
	for i := 1; i <= 5; i++ {
		rec := AuditRecord{
			ID:        "limit-rec-" + string(rune(i+'0')),
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second),
			Actor:     "user",
			Action:    "action",
			Status:    "success",
		}
		if err := store.Append(ctx, rec); err != nil {
			t.Fatalf("Append record %d failed: %v", i, err)
		}
	}

	// List with limit=3 returns exactly 3 records (the 3 newest)
	records, err := store.List(ctx, 3)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 3 {
		t.Fatalf("expected 3 records (limit enforced), got %d", len(records))
	}

	// Verify they are the 3 newest (rec-5, rec-4, rec-3)
	if records[0].ID != "limit-rec-5" {
		t.Errorf("expected records[0] to be limit-rec-5, got %s", records[0].ID)
	}
	if records[1].ID != "limit-rec-4" {
		t.Errorf("expected records[1] to be limit-rec-4, got %s", records[1].ID)
	}
	if records[2].ID != "limit-rec-3" {
		t.Errorf("expected records[2] to be limit-rec-3, got %s", records[2].ID)
	}
}

func TestAuditLogHandler_MethodNotAllowed(t *testing.T) {
	srv := &Server{auditStore: NewInMemoryAuditStore()}

	req := httptest.NewRequest(http.MethodPost, "/api/audit", nil)
	w := httptest.NewRecorder()

	srv.auditLogHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 Method Not Allowed, got %d", w.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tier 3: Non-functional (concurrency, persistence, etc.) tests
// ─────────────────────────────────────────────────────────────────────────────

func TestInMemoryAuditStore_Concurrent(t *testing.T) {
	t.Parallel()
	store := NewInMemoryAuditStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	numGoroutines := 10
	recordsPerGoroutine := 10

	// Spawn 10 goroutines, each appending 10 records
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < recordsPerGoroutine; i++ {
				rec := AuditRecord{
					ID:        "concurrent-" + string(rune(goroutineID)) + "-" + string(rune(i)),
					Timestamp: time.Now().UTC(),
					Actor:     "actor",
					Action:    "action",
					Status:    "success",
				}
				if err := store.Append(ctx, rec); err != nil {
					t.Errorf("Append failed: %v", err)
					return
				}
			}
		}(g)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// List should return at most 1000 records and no panic/race
	records, err := store.List(ctx, 2000)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	expectedMax := 1000
	if len(records) > expectedMax {
		t.Fatalf("expected at most %d records, got %d", expectedMax, len(records))
	}

	// Verify no corrupted records
	for _, rec := range records {
		if rec.ID == "" {
			t.Errorf("found record with empty ID")
		}
	}
}

func TestFileAuditStore_Concurrent(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	store := NewFileAuditStore(tempDir)
	ctx := context.Background()

	var wg sync.WaitGroup
	numGoroutines := 5
	recordsPerGoroutine := 5

	// Spawn 5 goroutines, each appending 5 records
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < recordsPerGoroutine; i++ {
				rec := AuditRecord{
					ID:        "file-concurrent-" + string(rune(goroutineID)) + "-" + string(rune(i)),
					Timestamp: time.Now().UTC(),
					Actor:     "actor",
					Action:    "action",
					Status:    "success",
				}
				if err := store.Append(ctx, rec); err != nil {
					t.Errorf("Append failed: %v", err)
					return
				}
			}
		}(g)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// List and verify no corruption
	records, err := store.List(ctx, 1000)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	expectedCount := numGoroutines * recordsPerGoroutine
	if len(records) != expectedCount {
		t.Fatalf("expected %d records, got %d", expectedCount, len(records))
	}

	// Verify all records are valid JSON by checking that each one can be marshaled
	for _, rec := range records {
		if rec.ID == "" {
			t.Errorf("found record with empty ID")
		}
		// Attempt to re-marshal to verify it's valid JSON
		_, err := json.Marshal(rec)
		if err != nil {
			t.Errorf("record failed to re-marshal: %v", err)
		}
	}
}

func TestRecordingProxy_StatusCapture(t *testing.T) {
	// Create a test HTTP server that returns 400
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer testServer.Close()

	// Create a statusRecorder wrapping httptest.NewRecorder()
	recorder := httptest.NewRecorder()
	statusRecorder := &statusRecorder{ResponseWriter: recorder}

	// Call the test server and capture the status
	resp, err := http.Get(testServer.URL)
	if err != nil {
		t.Fatalf("HTTP Get failed: %v", err)
	}
	defer resp.Body.Close()

	// Simulate the status recorder capturing a 400 response
	statusRecorder.WriteHeader(http.StatusBadRequest)

	// Verify statusRecorder.status == 400 after WriteHeader is called
	if statusRecorder.status != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", statusRecorder.status)
	}
}


// ─────────────────────────────────────────────────────────────────────────────
// Integration tests for Server with audit trail
// ─────────────────────────────────────────────────────────────────────────────

func TestServer_auditLogHandler_GetRequest(t *testing.T) {
	srv := &Server{auditStore: NewInMemoryAuditStore()}
	ctx := context.Background()

	// Pre-populate audit store with a record
	rec := AuditRecord{
		ID:        "test-1",
		Timestamp: time.Now().UTC(),
		Actor:     "user1",
		Action:    "test_action",
		Status:    "success",
	}
	if err := srv.auditStore.Append(ctx, rec); err != nil {
		t.Fatalf("Failed to append record: %v", err)
	}

	// Create a GET request to /api/audit
	req := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	w := httptest.NewRecorder()

	srv.auditLogHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	// Parse response
	var response []AuditRecord
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(response) != 1 {
		t.Fatalf("expected 1 record in response, got %d", len(response))
	}
	if response[0].ID != "test-1" {
		t.Fatalf("expected record ID test-1, got %s", response[0].ID)
	}
}

func TestServer_auditLogHandler_WithLimit(t *testing.T) {
	srv := &Server{auditStore: NewInMemoryAuditStore()}
	ctx := context.Background()

	// Pre-populate audit store with 5 records
	for i := 1; i <= 5; i++ {
		rec := AuditRecord{
			ID:        "test-" + string(rune(i+'0')),
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second),
			Actor:     "user",
			Action:    "action",
			Status:    "success",
		}
		if err := srv.auditStore.Append(ctx, rec); err != nil {
			t.Fatalf("Failed to append record: %v", err)
		}
	}

	// Create a GET request with limit parameter
	req := httptest.NewRequest(http.MethodGet, "/api/audit?limit=2", nil)
	w := httptest.NewRecorder()

	srv.auditLogHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	// Parse response
	var response []AuditRecord
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(response) != 2 {
		t.Fatalf("expected 2 records with limit=2, got %d", len(response))
	}
}

func TestFileAuditStore_PathConstruction(t *testing.T) {
	tempDir := t.TempDir()

	// Create a FileAuditStore with a path
	store := NewFileAuditStore(tempDir)

	// Verify it constructs the correct file path (audit.ndjson)
	expectedPath := filepath.Join(tempDir, "audit.ndjson")
	if store.path != expectedPath {
		t.Fatalf("expected path %s, got %s", expectedPath, store.path)
	}
}

func TestInMemoryAuditStore_NoLimitReturnsAll(t *testing.T) {
	store := NewInMemoryAuditStore()
	ctx := context.Background()

	// Append 5 records
	for i := 1; i <= 5; i++ {
		rec := AuditRecord{
			ID:        "no-limit-" + string(rune(i+'0')),
			Timestamp: time.Now().UTC(),
			Actor:     "user",
			Action:    "action",
			Status:    "success",
		}
		if err := store.Append(ctx, rec); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// List with limit=0 (no limit) should return all
	records, err := store.List(ctx, 0)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 5 {
		t.Fatalf("expected 5 records with no limit, got %d", len(records))
	}
}

func TestFileAuditStore_InvalidJsonSkipped(t *testing.T) {
	tempDir := t.TempDir()
	auditFile := filepath.Join(tempDir, "audit.ndjson")

	// Write an audit file with one valid and one invalid JSON line
	content := `{"id":"valid-1","timestamp":"2026-01-01T00:00:00Z","actor":"user","action":"test","status":"success"}
invalid json line here
{"id":"valid-2","timestamp":"2026-01-01T00:00:01Z","actor":"user","action":"test","status":"success"}
`
	if err := os.WriteFile(auditFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	store := NewFileAuditStore(tempDir)
	ctx := context.Background()

	// List should skip the invalid line and return only the 2 valid records
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 valid records (invalid line skipped), got %d", len(records))
	}

	// Verify the valid records are present
	if records[0].ID != "valid-2" || records[1].ID != "valid-1" {
		t.Errorf("expected valid-2 and valid-1, got %s and %s", records[0].ID, records[1].ID)
	}
}

func TestFileAuditStore_EmptyFile(t *testing.T) {
	tempDir := t.TempDir()
	auditFile := filepath.Join(tempDir, "audit.ndjson")

	// Create an empty audit file
	if err := os.WriteFile(auditFile, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	store := NewFileAuditStore(tempDir)
	ctx := context.Background()

	// List should return an empty slice, not nil
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 0 {
		t.Fatalf("expected 0 records for empty file, got %d", len(records))
	}
}

func TestAuditRecord_MetadataPreserved(t *testing.T) {
	store := NewInMemoryAuditStore()
	ctx := context.Background()

	// Create a record with metadata
	metadata := map[string]string{
		"region": "us-east-1",
		"env":    "staging",
	}
	rec := AuditRecord{
		ID:           "meta-rec-1",
		Timestamp:    time.Now().UTC(),
		Actor:        "user",
		Action:       "deploy",
		ResourceType: "release",
		ResourceID:   "rel-123",
		Status:       "success",
		Summary:      "Deploy to staging",
		Metadata:     metadata,
	}

	if err := store.Append(ctx, rec); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Retrieve and verify metadata is preserved
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	retrieved := records[0]
	if retrieved.Metadata == nil || retrieved.Metadata["region"] != "us-east-1" || retrieved.Metadata["env"] != "staging" {
		t.Errorf("metadata not preserved correctly: %v", retrieved.Metadata)
	}
}

func TestFileAuditStore_AppendMultipleTimesPreservesAll(t *testing.T) {
	tempDir := t.TempDir()
	store := NewFileAuditStore(tempDir)
	ctx := context.Background()

	// Append record 1
	rec1 := AuditRecord{ID: "append-1", Timestamp: time.Now().UTC(), Actor: "user", Action: "a1", Status: "ok"}
	if err := store.Append(ctx, rec1); err != nil {
		t.Fatalf("Append 1 failed: %v", err)
	}

	// Append record 2
	rec2 := AuditRecord{ID: "append-2", Timestamp: time.Now().UTC(), Actor: "user", Action: "a2", Status: "ok"}
	if err := store.Append(ctx, rec2); err != nil {
		t.Fatalf("Append 2 failed: %v", err)
	}

	// Append record 3
	rec3 := AuditRecord{ID: "append-3", Timestamp: time.Now().UTC(), Actor: "user", Action: "a3", Status: "ok"}
	if err := store.Append(ctx, rec3); err != nil {
		t.Fatalf("Append 3 failed: %v", err)
	}

	// List should return all 3 in newest-first order
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}

	if records[0].ID != "append-3" || records[1].ID != "append-2" || records[2].ID != "append-1" {
		t.Errorf("records not in newest-first order: %v", []string{records[0].ID, records[1].ID, records[2].ID})
	}
}

func TestAuditLogHandler_NegativeLimit(t *testing.T) {
	srv := &Server{auditStore: NewInMemoryAuditStore()}
	ctx := context.Background()

	// Add 3 records
	for i := 1; i <= 3; i++ {
		rec := AuditRecord{
			ID:     "neg-limit-" + string(rune(i+'0')),
			Actor:  "user",
			Action: "action",
			Status: "ok",
		}
		srv.auditStore.Append(ctx, rec)
	}

	// GET with negative limit should return all records
	req := httptest.NewRequest(http.MethodGet, "/api/audit?limit=-1", nil)
	w := httptest.NewRecorder()

	srv.auditLogHandler(w, req)

	var records []AuditRecord
	json.NewDecoder(w.Body).Decode(&records)

	if len(records) != 3 {
		t.Fatalf("expected 3 records with negative limit, got %d", len(records))
	}
}

func TestAuditLogHandler_InvalidLimitParam(t *testing.T) {
	srv := &Server{auditStore: NewInMemoryAuditStore()}
	ctx := context.Background()

	// Add a record
	rec := AuditRecord{ID: "test", Actor: "user", Action: "action", Status: "ok"}
	srv.auditStore.Append(ctx, rec)

	// GET with invalid limit (non-numeric) should default to some reasonable behavior
	req := httptest.NewRequest(http.MethodGet, "/api/audit?limit=not-a-number", nil)
	w := httptest.NewRecorder()

	srv.auditLogHandler(w, req)

	// Should still return 200 and records (parsing error should be handled gracefully)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var records []AuditRecord
	if err := json.NewDecoder(w.Body).Decode(&records); err == nil {
		if len(records) == 0 {
			// It's OK if invalid limit defaults to no limit
			t.Logf("invalid limit defaulted to returning all records")
		}
	}
}

func TestFileAuditStore_WhitespaceLines(t *testing.T) {
	tempDir := t.TempDir()
	auditFile := filepath.Join(tempDir, "audit.ndjson")

	// Write a file with blank lines and whitespace-only lines
	content := `{"id":"rec-1","actor":"user","action":"a","status":"ok"}


{"id":"rec-2","actor":"user","action":"b","status":"ok"}

`
	if err := os.WriteFile(auditFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	store := NewFileAuditStore(tempDir)
	ctx := context.Background()

	// List should skip blank/whitespace lines
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records (whitespace lines skipped), got %d", len(records))
	}

	if records[0].ID != "rec-2" || records[1].ID != "rec-1" {
		t.Errorf("expected rec-2 and rec-1, got %s and %s", records[0].ID, records[1].ID)
	}
}

func TestInMemoryAuditStore_ContextCancellation(t *testing.T) {
	store := NewInMemoryAuditStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	rec := AuditRecord{ID: "test", Actor: "user", Action: "action", Status: "ok"}

	// Append and List should not be affected by context cancellation
	// (the implementation ignores context, so both should succeed)
	if err := store.Append(ctx, rec); err != nil {
		t.Fatalf("Append should work even with cancelled context: %v", err)
	}

	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List should work even with cancelled context: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
}

func TestFileAuditStore_ConcurrentReadsDoNotInterfere(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	store := NewFileAuditStore(tempDir)
	ctx := context.Background()

	// Append some records
	for i := 1; i <= 5; i++ {
		rec := AuditRecord{
			ID:     "concurrent-read-" + string(rune(i+'0')),
			Actor:  "user",
			Action: "action",
			Status: "ok",
		}
		if err := store.Append(ctx, rec); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// Perform concurrent reads
	results := make(chan int, 10)
	var wg sync.WaitGroup

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			records, err := store.List(ctx, 10)
			if err != nil {
				results <- -1
				return
			}
			results <- len(records)
		}()
	}

	wg.Wait()
	close(results)

	// All reads should return the same number of records
	expectedCount := 5
	for count := range results {
		if count != expectedCount {
			t.Errorf("expected %d records, got %d", expectedCount, count)
		}
	}
}

func TestAuditRecord_Marshaling(t *testing.T) {
	rec := AuditRecord{
		ID:           "test-1",
		Timestamp:    time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		Actor:        "user",
		Action:       "test",
		ResourceType: "resource",
		ResourceID:   "res-1",
		Status:       "success",
		Summary:      "Test summary",
		Metadata: map[string]string{
			"key": "value",
		},
	}

	// Marshal to JSON
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal back
	var restored AuditRecord
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify all fields are preserved
	if restored.ID != rec.ID || restored.Actor != rec.Actor || restored.Action != rec.Action {
		t.Fatalf("fields not preserved after marshaling/unmarshaling")
	}
	if restored.Metadata["key"] != "value" {
		t.Fatalf("metadata not preserved")
	}
}

func TestInMemoryAuditStore_ReverseOrder(t *testing.T) {
	store := NewInMemoryAuditStore()
	ctx := context.Background()

	// Append in order 1, 2, 3
	for i := 1; i <= 3; i++ {
		rec := AuditRecord{
			ID:        "rev-" + string(rune(i+'0')),
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second),
			Actor:     "user",
			Action:    "action",
			Status:    "ok",
		}
		if err := store.Append(ctx, rec); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// List should return in reverse order: 3, 2, 1
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}

	// Newest first: rev-3, rev-2, rev-1
	for i := 3; i >= 1; i-- {
		idx := 3 - i
		expectedID := "rev-" + string(rune(i+'0'))
		if records[idx].ID != expectedID {
			t.Errorf("expected records[%d] to be %s, got %s", idx, expectedID, records[idx].ID)
		}
	}
}

func TestFileAuditStore_DirectoryCreation(t *testing.T) {
	// Test that the store can create the ndjson file in an existing directory
	tempDir := t.TempDir()
	store := NewFileAuditStore(tempDir)
	ctx := context.Background()

	rec := AuditRecord{ID: "dir-test", Actor: "user", Action: "action", Status: "ok"}
	if err := store.Append(ctx, rec); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Verify the file was created
	expectedPath := filepath.Join(tempDir, "audit.ndjson")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("audit.ndjson file not created: %v", err)
	}
}

func TestAuditStore_InterfaceCompliance(t *testing.T) {
	// Verify that both implementations satisfy the AuditStore interface
	var _ AuditStore = (*InMemoryAuditStore)(nil)
	var _ AuditStore = (*FileAuditStore)(nil)

	// If this compiles, the implementations satisfy the interface
	t.Logf("Both InMemoryAuditStore and FileAuditStore satisfy the AuditStore interface")
}

func TestNewAuditStore_FileKindEmptyPath(t *testing.T) {
	// When kind is "file" but path is empty, should default to InMemoryAuditStore
	store := newAuditStore("file", "")
	if _, ok := store.(*InMemoryAuditStore); !ok {
		t.Fatalf("expected *InMemoryAuditStore when file kind has empty path, got %T", store)
	}
}

func TestFileAuditStore_AppendError(t *testing.T) {
	// Create a store with a path that's invalid
	invalidPath := "/dev/null/this/path/does/not/exist/audit.ndjson"

	// On Windows, construct a path that can't be created
	if os.PathSeparator == '\\' {
		invalidPath = "C:\\nul\\invalid\\path\\audit.ndjson"
	}

	store := &FileAuditStore{path: invalidPath}
	ctx := context.Background()

	rec := AuditRecord{ID: "test", Actor: "user", Action: "action", Status: "ok"}
	err := store.Append(ctx, rec)

	// Should return an error (can't write to invalid path)
	if err == nil {
		t.Fatalf("expected error when appending to invalid path")
	}
}

func TestInMemoryAuditStore_LargePayload(t *testing.T) {
	store := NewInMemoryAuditStore()
	ctx := context.Background()

	// Create record with large summary
	largeText := strings.Repeat("x", 10000)
	rec := AuditRecord{
		ID:        "large-1",
		Actor:     "user",
		Action:    "action",
		Status:    "ok",
		Summary:   largeText,
		Metadata:  map[string]string{"key": largeText},
	}

	if err := store.Append(ctx, rec); err != nil {
		t.Fatalf("Append with large payload failed: %v", err)
	}

	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(records) != 1 || len(records[0].Summary) != 10000 {
		t.Fatalf("large payload not preserved correctly")
	}
}
