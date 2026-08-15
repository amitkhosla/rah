package steps

import (
	"context"
	"errors"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// â"€â"€ mock CredentialLookup for LLM key tests â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// mockLLMCredLookup is a minimal in-memory CredentialLookup for testing LoadLLMKey.
type mockLLMCredLookup struct {
	// creds maps "tenant/name" or "__global__/name" → plaintext value.
	creds map[string]string
	// resolveErr is returned by Resolve if set.
	resolveErr error
}

func newMockLLMCredLookup() *mockLLMCredLookup {
	return &mockLLMCredLookup{creds: make(map[string]string)}
}

func (m *mockLLMCredLookup) set(name, tenant, value string) {
	if tenant == "" {
		m.creds["__global__/"+name] = value
	} else {
		m.creds[tenant+"/"+name] = value
	}
}

func (m *mockLLMCredLookup) Resolve(_ context.Context, name, tenant string) ([]byte, error) {
	if m.resolveErr != nil {
		return nil, m.resolveErr
	}
	// Tenant-specific first.
	if tenant != "" {
		if v, ok := m.creds[tenant+"/"+name]; ok {
			return []byte(v), nil
		}
	}
	// Global fallback.
	if v, ok := m.creds["__global__/"+name]; ok {
		return []byte(v), nil
	}
	return nil, errors.New("credential not found")
}

// â"€â"€ helpers â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func newLoadLLMKeyCtx() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 16)
	ctx.IntSlots = make([]int64, 8)
	ctx.BoolSlots = make([]bool, 8)
	return ctx
}

func runLoadLLMKeyInstr(instr engine.Instruction, ctx *rctx.Context) int16 {
	state := &engine.ExecutionState{PC: 0}
	return instr.Action(ctx, state)
}

// â"€â"€ tests â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

func TestLoadLLMKey_NilReg_Noop(t *testing.T) {
	ctx := newLoadLLMKeyCtx()
	ctx.ByteSlots[0] = []byte("anthropic_key")

	cfg := LoadLLMKeyConfig{
		CredNameSlot: 0,
		OutSlot:      1,
		Reg:          nil, // no registry
	}
	instr := LoadLLMKey(cfg)
	next := runLoadLLMKeyInstr(instr, ctx)

	if next != 1 {
		t.Errorf("want PC+1 (1), got %d", next)
	}
	if len(ctx.ByteSlots[1]) != 0 {
		t.Errorf("OutSlot should be empty, got %q", ctx.ByteSlots[1])
	}
}

func TestLoadLLMKey_EmptyCredNameSlot_Noop(t *testing.T) {
	reg := newMockLLMCredLookup()
	reg.set("anthropic_key", "", "sk-global-key")

	ctx := newLoadLLMKeyCtx()
	// slot 0 is empty — no credential name supplied

	cfg := LoadLLMKeyConfig{
		CredNameSlot: 0,
		OutSlot:      1,
		Reg:          reg,
	}
	instr := LoadLLMKey(cfg)
	next := runLoadLLMKeyInstr(instr, ctx)

	if next != 1 {
		t.Errorf("want PC+1 (1), got %d", next)
	}
	if len(ctx.ByteSlots[1]) != 0 {
		t.Errorf("OutSlot should be empty on no-op, got %q", ctx.ByteSlots[1])
	}
}

func TestLoadLLMKey_CredFound_WritesDecrypted(t *testing.T) {
	reg := newMockLLMCredLookup()
	reg.set("anthropic_key", "tenant-A", "sk-tenant-secret")

	ctx := newLoadLLMKeyCtx()
	ctx.ByteSlots[0] = []byte("anthropic_key")
	ctx.TenantKey = "tenant-A"

	cfg := LoadLLMKeyConfig{
		CredNameSlot: 0,
		OutSlot:      1,
		Reg:          reg,
	}
	instr := LoadLLMKey(cfg)
	next := runLoadLLMKeyInstr(instr, ctx)

	if next != 1 {
		t.Fatalf("want PC+1, got %d", next)
	}
	if string(ctx.ByteSlots[1]) != "sk-tenant-secret" {
		t.Errorf("OutSlot: want %q, got %q", "sk-tenant-secret", ctx.ByteSlots[1])
	}
}

func TestLoadLLMKey_CredFound_GlobalFallback(t *testing.T) {
	reg := newMockLLMCredLookup()
	reg.set("openai_key", "", "sk-global-openai") // global only

	ctx := newLoadLLMKeyCtx()
	ctx.ByteSlots[0] = []byte("openai_key")
	ctx.TenantKey = "some-tenant" // tenant has no specific override

	cfg := LoadLLMKeyConfig{
		CredNameSlot: 0,
		OutSlot:      2,
		Reg:          reg,
	}
	instr := LoadLLMKey(cfg)
	next := runLoadLLMKeyInstr(instr, ctx)

	if next != 1 {
		t.Fatalf("want PC+1, got %d", next)
	}
	if string(ctx.ByteSlots[2]) != "sk-global-openai" {
		t.Errorf("OutSlot: want %q, got %q", "sk-global-openai", ctx.ByteSlots[2])
	}
}

func TestLoadLLMKey_CredNotFound_Noop(t *testing.T) {
	reg := newMockLLMCredLookup()
	// No credentials registered

	ctx := newLoadLLMKeyCtx()
	ctx.ByteSlots[0] = []byte("missing_key")
	ctx.ByteSlots[1] = []byte("original-value") // sentinel — must remain unchanged

	cfg := LoadLLMKeyConfig{
		CredNameSlot: 0,
		OutSlot:      1,
		Reg:          reg,
	}
	instr := LoadLLMKey(cfg)
	next := runLoadLLMKeyInstr(instr, ctx)

	if next != 1 {
		t.Errorf("want PC+1, got %d", next)
	}
	// Instruction returns early on error without writing — sentinel is preserved.
	if string(ctx.ByteSlots[1]) != "original-value" {
		t.Errorf("OutSlot should be unchanged, got %q", ctx.ByteSlots[1])
	}
}

func TestLoadLLMKey_DecryptError_Noop(t *testing.T) {
	reg := newMockLLMCredLookup()
	reg.resolveErr = errors.New("decrypt failed")

	ctx := newLoadLLMKeyCtx()
	ctx.ByteSlots[0] = []byte("anthropic_key")
	ctx.ByteSlots[1] = []byte("untouched") // sentinel

	cfg := LoadLLMKeyConfig{
		CredNameSlot: 0,
		OutSlot:      1,
		Reg:          reg,
	}
	instr := LoadLLMKey(cfg)
	next := runLoadLLMKeyInstr(instr, ctx)

	if next != 1 {
		t.Errorf("want PC+1, got %d", next)
	}
	if string(ctx.ByteSlots[1]) != "untouched" {
		t.Errorf("OutSlot should be unchanged on decrypt error, got %q", ctx.ByteSlots[1])
	}
}
