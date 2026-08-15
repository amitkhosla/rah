package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/datastore"
)

type mockWorkflowStore struct {
	data map[string]map[string][]byte // tenant -> key -> value
}

func newMockWorkflowStore() *mockWorkflowStore {
	return &mockWorkflowStore{data: make(map[string]map[string][]byte)}
}

func (m *mockWorkflowStore) IsConfigured(domain config.DataDomain) bool {
	return domain == config.DomainWorkflows
}

func (m *mockWorkflowStore) Get(_ context.Context, _ config.DataDomain, tenant datastore.Tenant, key string) ([]byte, bool, error) {
	td := m.data[string(tenant)]
	if td == nil {
		return nil, false, nil
	}
	v, ok := td[key]
	return v, ok, nil
}

func (m *mockWorkflowStore) Put(_ context.Context, _ config.DataDomain, tenant datastore.Tenant, key string, value []byte) error {
	tk := string(tenant)
	if m.data[tk] == nil {
		m.data[tk] = make(map[string][]byte)
	}
	m.data[tk][key] = value
	return nil
}

func (m *mockWorkflowStore) Delete(_ context.Context, _ config.DataDomain, tenant datastore.Tenant, key string) error {
	if td := m.data[string(tenant)]; td != nil {
		delete(td, key)
	}
	return nil
}

func newWorkflowMux(t *testing.T) (*http.ServeMux, *mockWorkflowStore) {
	t.Helper()
	store := newMockWorkflowStore()
	mux := http.NewServeMux()
	NewWorkflowHandler(store).RegisterHandlers(mux)
	return mux, store
}

func TestWorkflowHandler_ListEmpty(t *testing.T) {
	mux, _ := newWorkflowMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/workflows", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	workflows, _ := result["workflows"].([]interface{})
	if len(workflows) != 0 {
		t.Fatalf("expected empty list, got %d items", len(workflows))
	}
}

func TestWorkflowHandler_CreateValidWorkflow(t *testing.T) {
	mux, _ := newWorkflowMux(t)
	wf := config.WorkflowDefinition{
		Name: "pay-flow",
		Nodes: []config.WorkflowNode{
			{ID: "n1", FlowName: "validate"},
			{ID: "n2", FlowName: "charge"},
		},
		Edges: []config.WorkflowEdge{
			{ID: "e1", SourceNodeID: "n1", TargetNodeID: "n2", ListenerName: "validated"},
		},
	}
	body, _ := json.Marshal(wf)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/workflows", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result config.WorkflowDefinition
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Name != "pay-flow" {
		t.Fatalf("expected name 'pay-flow', got %q", result.Name)
	}
	if result.CreatedAt == 0 || result.UpdatedAt == 0 {
		t.Fatal("expected timestamps to be set")
	}
}

func TestWorkflowHandler_CreateWorkflowWithCycle(t *testing.T) {
	mux, _ := newWorkflowMux(t)
	wf := config.WorkflowDefinition{
		Name: "cycle-wf",
		Nodes: []config.WorkflowNode{
			{ID: "n1", FlowName: "f1"},
			{ID: "n2", FlowName: "f2"},
		},
		Edges: []config.WorkflowEdge{
			{ID: "e1", SourceNodeID: "n1", TargetNodeID: "n2", ListenerName: "a"},
			{ID: "e2", SourceNodeID: "n2", TargetNodeID: "n1", ListenerName: "b"},
		},
	}
	body, _ := json.Marshal(wf)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/workflows", bytes.NewReader(body)))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestWorkflowHandler_CreateWithInvalidEdgeReference(t *testing.T) {
	mux, _ := newWorkflowMux(t)
	wf := config.WorkflowDefinition{
		Name:  "bad-edges",
		Nodes: []config.WorkflowNode{{ID: "n1", FlowName: "f1"}},
		Edges: []config.WorkflowEdge{
			{ID: "e1", SourceNodeID: "n1", TargetNodeID: "nonexistent", ListenerName: "done"},
		},
	}
	body, _ := json.Marshal(wf)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/workflows", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestWorkflowHandler_CreateWithInvalidName(t *testing.T) {
	mux, _ := newWorkflowMux(t)
	wf := config.WorkflowDefinition{
		Name:  "bad name!",
		Nodes: []config.WorkflowNode{{ID: "n1", FlowName: "f1"}},
	}
	body, _ := json.Marshal(wf)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/workflows", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestWorkflowHandler_GetWorkflow(t *testing.T) {
	mux, _ := newWorkflowMux(t)
	wf := config.WorkflowDefinition{
		Name:  "my-wf",
		Nodes: []config.WorkflowNode{{ID: "n1", FlowName: "f1"}},
		Edges: []config.WorkflowEdge{},
	}
	body, _ := json.Marshal(wf)
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/workflows", bytes.NewReader(body)))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/workflows/my-wf", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result config.WorkflowDefinition
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Name != "my-wf" {
		t.Fatalf("expected 'my-wf', got %q", result.Name)
	}
}

func TestWorkflowHandler_GetNonexistent(t *testing.T) {
	mux, _ := newWorkflowMux(t)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/workflows/ghost", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestWorkflowHandler_DeleteWorkflow(t *testing.T) {
	mux, _ := newWorkflowMux(t)
	wf := config.WorkflowDefinition{
		Name:  "del-wf",
		Nodes: []config.WorkflowNode{{ID: "n1", FlowName: "f1"}},
		Edges: []config.WorkflowEdge{},
	}
	body, _ := json.Marshal(wf)
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/workflows", bytes.NewReader(body)))

	// Verify listed
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/workflows", nil))
	var list map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list["workflows"].([]interface{})) != 1 {
		t.Fatal("expected 1 workflow before delete")
	}

	// Delete
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("DELETE", "/workflows/del-wf", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	// Verify gone
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/workflows", nil))
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list["workflows"].([]interface{})) != 0 {
		t.Fatal("expected empty list after delete")
	}
}

func TestWorkflowHandler_StorageNotConfigured(t *testing.T) {
	mux := http.NewServeMux()
	NewWorkflowHandler(nil).RegisterHandlers(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/workflows", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}
