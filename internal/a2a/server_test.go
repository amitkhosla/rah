package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestA2AServer_TaskSend(t *testing.T) {
	srv := &A2AServer{
		Send: func(ctx context.Context, params TaskSendParams) (*Task, error) {
			return &Task{
				ID:     params.ID,
				Status: TaskStatus{State: "completed"},
				Result: "ok",
			}, nil
		},
	}

	reqBody, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tasks/send",
		Params:  json.RawMessage(`{"id":"t1","message":{"text":"hello"}}`),
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(reqBody))
	r.Header.Set("Content-Type", "application/json")

	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp JSONRPCResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected result, got nil")
	}
}

func TestA2AServer_UnknownMethod(t *testing.T) {
	srv := &A2AServer{}

	reqBody, _ := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tasks/unknown",
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(reqBody))
	r.Header.Set("Content-Type", "application/json")

	srv.ServeHTTP(w, r)

	var resp JSONRPCResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected error code -32601, got %d", resp.Error.Code)
	}
}
