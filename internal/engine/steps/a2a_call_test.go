package steps

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/a2a"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

func TestA2ACall_SuccessfulCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req a2a.JSONRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := a2a.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: &a2a.Task{
				ID:     "t1",
				Status: a2a.TaskStatus{State: "completed"},
				Result: "done",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 4),
	}
	ctx.ByteSlots[0] = []byte(`{"text":"hello"}`)
	state := &engine.ExecutionState{PC: 0}

	cfg := A2ACallConfig{
		URL:        srv.URL,
		InputSlot:  0,
		OutputSlot: 1,
		TimeoutSec: 5,
	}

	next := A2ACall(cfg).Action(ctx, state)
	if next != 1 {
		t.Fatalf("expected PC 1, got %d", next)
	}
	if len(ctx.ByteSlots[1]) == 0 {
		t.Fatal("expected result in output slot")
	}
	// result should not be "null"
	if string(ctx.ByteSlots[1]) == "null" {
		t.Fatal("expected non-null result")
	}
}

func TestA2ACall_RemoteError_SetsNull(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := a2a.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &a2a.JSONRPCError{Code: -32603, Message: "internal error"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 4),
	}
	ctx.ByteSlots[0] = []byte(`{"text":"hello"}`)
	state := &engine.ExecutionState{PC: 0}

	cfg := A2ACallConfig{
		URL:        srv.URL,
		InputSlot:  0,
		OutputSlot: 1,
		TimeoutSec: 5,
	}

	// Suppress error logging during test
	ctx.Obs = nil

	next := A2ACall(cfg).Action(ctx, state)
	if next != 1 {
		t.Fatalf("expected PC 1, got %d", next)
	}
	if string(ctx.ByteSlots[1]) != "null" {
		t.Errorf("expected null on remote error, got %s", ctx.ByteSlots[1])
	}
}

