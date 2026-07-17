package steps

import (
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

func TestIPRestriction_AllowModeBlocksOutsideCIDR(t *testing.T) {
	ctx := &rctx.Context{Request: httptest.NewRequest("GET", "http://example.com", nil), ByteSlots: make([][]byte, 4)}
	ctx.Request.RemoteAddr = "203.0.113.10:12345"
	st := &engine.ExecutionState{PC: 10}

	instr := IPRestriction(IPRestrictionConfig{
		Mode:              "allow",
		CIDRs:             "10.0.0.0/8",
		Source:            "remote_addr",
		OnViolationStatus: 403,
		OnViolationBody:   "ip not allowed",
	}, -1)

	next := instr.Action(ctx, st)
	if next != engine.StopPlan {
		t.Fatalf("expected StopPlan, got %d", next)
	}
	if ctx.ResponseStatus != 403 || !ctx.Failed {
		t.Fatalf("expected blocked request (403, failed), got status=%d failed=%v", ctx.ResponseStatus, ctx.Failed)
	}
}

func TestIPRestriction_AllowModePassesInsideCIDR(t *testing.T) {
	ctx := &rctx.Context{Request: httptest.NewRequest("GET", "http://example.com", nil), ByteSlots: make([][]byte, 4)}
	ctx.Request.RemoteAddr = "10.1.2.3:12345"
	st := &engine.ExecutionState{PC: 7}

	instr := IPRestriction(IPRestrictionConfig{
		Mode:              "allow",
		CIDRs:             "10.0.0.0/8",
		Source:            "remote_addr",
		OnViolationStatus: 403,
		OnViolationBody:   "ip not allowed",
	}, -1)

	next := instr.Action(ctx, st)
	if next != st.PC+1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	if ctx.Failed {
		t.Fatalf("expected allowed request, got failed=true")
	}
}

func TestIPRestriction_UsesSourceSlotWhenProvided(t *testing.T) {
	ctx := &rctx.Context{Request: httptest.NewRequest("GET", "http://example.com", nil), ByteSlots: make([][]byte, 4)}
	ctx.Request.RemoteAddr = "10.0.0.2:12345"
	ctx.ByteSlots[1] = []byte("203.0.113.8")
	st := &engine.ExecutionState{PC: 1}

	instr := IPRestriction(IPRestrictionConfig{
		Mode:              "deny",
		CIDRs:             "203.0.113.0/24",
		Source:            "remote_addr",
		OnViolationStatus: 403,
		OnViolationBody:   "blocked",
	}, 1)

	next := instr.Action(ctx, st)
	if next != engine.StopPlan {
		t.Fatalf("expected StopPlan due to slot IP deny match, got %d", next)
	}
	if !ctx.Failed || ctx.ResponseStatus != 403 {
		t.Fatalf("expected 403 failed due to slot IP, got status=%d failed=%v", ctx.ResponseStatus, ctx.Failed)
	}
}
