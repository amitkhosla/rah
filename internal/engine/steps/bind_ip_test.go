package steps

import (
	"net/http/httptest"
	"testing"

	"rah/internal/engine"
	"rah/internal/rctx"
)

func TestBindClientIP_XFFFirstEntry(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	req.RemoteAddr = "203.0.113.1:12345"

	ctx := &rctx.Context{Request: req, ByteSlots: make([][]byte, 4)}
	ctx.InitSlots()
	st := &engine.ExecutionState{PC: 10}

	instr := BindClientIP(0, 0)
	next := instr.Action(ctx, st)

	if next != st.PC+1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	if got := string(ctx.ByteSlots[0]); got != "1.2.3.4" {
		t.Fatalf("expected '1.2.3.4', got '%s'", got)
	}
}

func TestBindClientIP_XFFSecondEntry(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	req.RemoteAddr = "203.0.113.1:12345"

	ctx := &rctx.Context{Request: req, ByteSlots: make([][]byte, 4)}
	ctx.InitSlots()
	st := &engine.ExecutionState{PC: 10}

	instr := BindClientIP(0, 1)
	next := instr.Action(ctx, st)

	if next != st.PC+1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	if got := string(ctx.ByteSlots[0]); got != "5.6.7.8" {
		t.Fatalf("expected '5.6.7.8', got '%s'", got)
	}
}

func TestBindClientIP_XFFNegativeIndex(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8, 9.9.9.9")
	req.RemoteAddr = "203.0.113.1:12345"

	ctx := &rctx.Context{Request: req, ByteSlots: make([][]byte, 4)}
	ctx.InitSlots()
	st := &engine.ExecutionState{PC: 10}

	instr := BindClientIP(0, -1)
	next := instr.Action(ctx, st)

	if next != st.PC+1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	if got := string(ctx.ByteSlots[0]); got != "9.9.9.9" {
		t.Fatalf("expected '9.9.9.9' (last entry), got '%s'", got)
	}
}

func TestBindClientIP_NoXFFHeaderUsesRemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.RemoteAddr = "9.9.9.9:1234"

	ctx := &rctx.Context{Request: req, ByteSlots: make([][]byte, 4)}
	ctx.InitSlots()
	st := &engine.ExecutionState{PC: 10}

	instr := BindClientIP(0, 0)
	next := instr.Action(ctx, st)

	if next != st.PC+1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	if got := string(ctx.ByteSlots[0]); got != "9.9.9.9" {
		t.Fatalf("expected '9.9.9.9', got '%s'", got)
	}
}

func TestBindClientIP_XFFOutOfRangeFallsback(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	req.RemoteAddr = "9.9.9.9:1234"

	ctx := &rctx.Context{Request: req, ByteSlots: make([][]byte, 4)}
	ctx.InitSlots()
	st := &engine.ExecutionState{PC: 10}

	// xffIndex=99 is out of range; should fall back to X-Real-IP or RemoteAddr
	instr := BindClientIP(0, 99)
	next := instr.Action(ctx, st)

	if next != st.PC+1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	// Since XFF entry 99 is out of range and there's no X-Real-IP, RemoteAddr is used
	if got := string(ctx.ByteSlots[0]); got != "9.9.9.9" {
		t.Fatalf("expected '9.9.9.9' (fallback to RemoteAddr), got '%s'", got)
	}
}

func TestBindClientIP_XRealIPHeaderUsedWhenXFFAbsent(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-Real-IP", "7.7.7.7")
	req.RemoteAddr = "9.9.9.9:1234"

	ctx := &rctx.Context{Request: req, ByteSlots: make([][]byte, 4)}
	ctx.InitSlots()
	st := &engine.ExecutionState{PC: 10}

	instr := BindClientIP(0, 0)
	next := instr.Action(ctx, st)

	if next != st.PC+1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	if got := string(ctx.ByteSlots[0]); got != "7.7.7.7" {
		t.Fatalf("expected '7.7.7.7' (from X-Real-IP), got '%s'", got)
	}
}

func TestBindClientIP_InvalidSlotIndexIgnored(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.RemoteAddr = "9.9.9.9:1234"

	ctx := &rctx.Context{Request: req, ByteSlots: make([][]byte, 4)}
	ctx.InitSlots()
	st := &engine.ExecutionState{PC: 10}

	// Slot index 99 is out of bounds
	instr := BindClientIP(99, 0)
	next := instr.Action(ctx, st)

	// Should still return PC+1 (no error, just silently skips)
	if next != st.PC+1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
}

func TestBindClientIP_EmptyXFFHeaderFallsback(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.Header.Set("X-Forwarded-For", "")
	req.RemoteAddr = "8.8.8.8:5678"

	ctx := &rctx.Context{Request: req, ByteSlots: make([][]byte, 4)}
	ctx.InitSlots()
	st := &engine.ExecutionState{PC: 10}

	instr := BindClientIP(0, 0)
	next := instr.Action(ctx, st)

	if next != st.PC+1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	if got := string(ctx.ByteSlots[0]); got != "8.8.8.8" {
		t.Fatalf("expected '8.8.8.8', got '%s'", got)
	}
}
