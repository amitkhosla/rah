package steps

import (
	"testing"
)

// ──────────────────────────────────────────────
// DetectMessageFormat tests
// ──────────────────────────────────────────────

func TestDetectMessageFormat_Anthropic_SystemString(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	body := `{
		"system": "You are a helpful assistant.",
		"messages": [
			{"role": "user", "content": "Hello"}
		]
	}`
	ctx.ByteSlots[0] = []byte(body)

	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}
	if got := string(ctx.ByteSlots[1]); got != "anthropic" {
		t.Errorf("format: got %q, want %q", got, "anthropic")
	}
}

func TestDetectMessageFormat_Anthropic_ModelPrefix(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	body := `{
		"model": "claude-3-5-sonnet",
		"messages": [{"role": "user", "content": "Hi"}]
	}`
	ctx.ByteSlots[0] = []byte(body)

	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}
	if got := string(ctx.ByteSlots[1]); got != "anthropic" {
		t.Errorf("format: got %q, want %q", got, "anthropic")
	}
}

func TestDetectMessageFormat_OpenAI_MessagesNoSystem(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	body := `{
		"messages": [
			{"role": "system", "content": "Be concise."},
			{"role": "user", "content": "What is 2+2?"}
		]
	}`
	ctx.ByteSlots[0] = []byte(body)

	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}
	if got := string(ctx.ByteSlots[1]); got != "openai" {
		t.Errorf("format: got %q, want %q", got, "openai")
	}
}

func TestDetectMessageFormat_OpenAI_ModelPrefix(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	body := `{
		"model": "gpt-4o",
		"messages": [{"role": "user", "content": "Hello"}]
	}`
	ctx.ByteSlots[0] = []byte(body)

	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}
	if got := string(ctx.ByteSlots[1]); got != "openai" {
		t.Errorf("format: got %q, want %q", got, "openai")
	}
}

func TestDetectMessageFormat_Gemini_ContentsKey(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	body := `{
		"contents": [
			{"role": "user", "parts": [{"text": "Tell me a joke."}]}
		]
	}`
	ctx.ByteSlots[0] = []byte(body)

	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}
	if got := string(ctx.ByteSlots[1]); got != "gemini" {
		t.Errorf("format: got %q, want %q", got, "gemini")
	}
}

func TestDetectMessageFormat_EmptyBody(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// ByteSlots[0] is nil — empty body.
	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1 on empty body, got %d", next)
	}
	if ctx.Failed {
		t.Error("should not fail on empty body")
	}
	if got := string(ctx.ByteSlots[1]); got != "unknown" {
		t.Errorf("format: got %q, want %q", got, "unknown")
	}
}

func TestDetectMessageFormat_InvalidJSON(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	ctx.ByteSlots[0] = []byte(`{not valid json`)

	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1 on invalid JSON, got %d", next)
	}
	if ctx.Failed {
		t.Error("should not fail on invalid JSON")
	}
	if got := string(ctx.ByteSlots[1]); got != "unknown" {
		t.Errorf("format: got %q, want %q", got, "unknown")
	}
}

func TestDetectMessageFormat_UnknownKeys(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	ctx.ByteSlots[0] = []byte(`{"prompt":"hello"}`)

	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}
	if got := string(ctx.ByteSlots[1]); got != "unknown" {
		t.Errorf("format: got %q, want %q", got, "unknown")
	}
}

func TestDetectMessageFormat_SystemNull_OpenAI(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// "system" key present but value is null → treat as absent → OpenAI
	body := `{"messages":[{"role":"user","content":"hi"}],"system":null}`
	ctx.ByteSlots[0] = []byte(body)

	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	instr.Action(ctx, state)

	if got := string(ctx.ByteSlots[1]); got != "openai" {
		t.Errorf("format: got %q, want %q", got, "openai")
	}
}

func TestDetectMessageFormat_SystemArray_OpenAI(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// "system" is an array → treat as OpenAI extended
	body := `{"messages":[{"role":"user","content":"hi"}],"system":[{"type":"text","text":"be helpful"}]}`
	ctx.ByteSlots[0] = []byte(body)

	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	instr.Action(ctx, state)

	if got := string(ctx.ByteSlots[1]); got != "openai" {
		t.Errorf("format: got %q, want %q", got, "openai")
	}
}

func TestDetectMessageFormat_SystemObject_OpenAI(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// "system" is a JSON object → treat as OpenAI extended
	body := `{"messages":[{"role":"user","content":"hi"}],"system":{"text":"be helpful"}}`
	ctx.ByteSlots[0] = []byte(body)

	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	instr.Action(ctx, state)

	if got := string(ctx.ByteSlots[1]); got != "openai" {
		t.Errorf("format: got %q, want %q", got, "openai")
	}
}

func TestDetectMessageFormat_SystemString_Anthropic(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// "system" is a string → Anthropic
	body := `{"messages":[{"role":"user","content":"hi"}],"system":"be helpful"}`
	ctx.ByteSlots[0] = []byte(body)

	cfg := DetectMessageFormatConfig{BodySlot: 0, FormatSlot: 1}
	instr := DetectMessageFormat(cfg)
	instr.Action(ctx, state)

	if got := string(ctx.ByteSlots[1]); got != "anthropic" {
		t.Errorf("format: got %q, want %q", got, "anthropic")
	}
}
