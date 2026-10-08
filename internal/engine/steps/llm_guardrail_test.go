package steps

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
)


// ── 1. Noop ───────────────────────────────────────────────────────────────────

func TestGuardrailNoop(t *testing.T) {
	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("hello world")

	cfg := GuardrailConfig{ResultSlot: 0, FlagSlot: -1}
	instr := GuardrailCheck(cfg)

	next := runInstruction(instr, ctx)
	if next != 1 {
		t.Fatalf("want PC+1=1, got %d", next)
	}
	if string(ctx.ByteSlots[0]) != "hello world" {
		t.Errorf("slot should be unchanged, got %q", ctx.ByteSlots[0])
	}
}

// ── 2. Regex block ────────────────────────────────────────────────────────────

func TestGuardrailRegexBlock(t *testing.T) {
	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("call me at 555-1234 please")

	cfg := GuardrailConfig{
		ResultSlot: 0,
		FlagSlot:   -1,
		RegexRules: []GuardrailRegexRule{
			{Pattern: `\d{3}-\d{4}`, Action: GuardrailBlock, Label: "phone"},
		},
	}
	instr := GuardrailCheck(cfg)
	next := runInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("want StopPlan, got %d", next)
	}
	if !ctx.Failed {
		t.Error("ctx.Failed should be true")
	}
	if ctx.ResponseStatus != 400 {
		t.Errorf("want ResponseStatus 400, got %d", ctx.ResponseStatus)
	}
	if ctx.ErrorCode != 400 {
		t.Errorf("want ErrorCode 400, got %d", ctx.ErrorCode)
	}
}

// ── 3. Regex no match ─────────────────────────────────────────────────────────

func TestGuardrailRegexNoMatch(t *testing.T) {
	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("nothing sensitive here")

	cfg := GuardrailConfig{
		ResultSlot: 0,
		FlagSlot:   -1,
		RegexRules: []GuardrailRegexRule{
			{Pattern: `\d{3}-\d{4}`, Action: GuardrailBlock},
		},
	}
	instr := GuardrailCheck(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want PC+1=1, got %d", next)
	}
	if ctx.Failed {
		t.Error("ctx.Failed should be false")
	}
	if string(ctx.ByteSlots[0]) != "nothing sensitive here" {
		t.Errorf("slot should be unchanged, got %q", ctx.ByteSlots[0])
	}
}

// ── 4. Regex redact ───────────────────────────────────────────────────────────

func TestGuardrailRegexRedact(t *testing.T) {
	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("email me at user@example.com ok")

	cfg := GuardrailConfig{
		ResultSlot: 0,
		FlagSlot:   -1,
		RegexRules: []GuardrailRegexRule{
			{Pattern: `\S+@\S+\.\S+`, Replacement: "[EMAIL]", Action: GuardrailRedact},
		},
	}
	instr := GuardrailCheck(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want PC+1=1, got %d", next)
	}
	if ctx.Failed {
		t.Error("ctx.Failed should be false on redact")
	}
	got := string(ctx.ByteSlots[0])
	if got != "email me at [EMAIL] ok" {
		t.Errorf("want redacted text, got %q", got)
	}
}

// ── 5. Regex flag ─────────────────────────────────────────────────────────────

func TestGuardrailRegexFlag(t *testing.T) {
	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("some bad word here")
	// BoolSlots[1] is the flag slot
	flagSlot := 1

	cfg := GuardrailConfig{
		ResultSlot: 0,
		FlagSlot:   flagSlot,
		RegexRules: []GuardrailRegexRule{
			{Pattern: `bad word`, Action: GuardrailFlag},
		},
	}
	instr := GuardrailCheck(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want PC+1=1, got %d", next)
	}
	if ctx.Failed {
		t.Error("ctx.Failed should be false on flag")
	}
	if !ctx.BoolSlots[flagSlot] {
		t.Error("BoolSlots[flagSlot] should be true after flag action")
	}
}

// ── 6. Bad regex ──────────────────────────────────────────────────────────────

func TestGuardrailBadRegex(t *testing.T) {
	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("some content")

	cfg := GuardrailConfig{
		ResultSlot: 0,
		FlagSlot:   -1,
		RegexRules: []GuardrailRegexRule{
			{Pattern: `[invalid(`, Action: GuardrailBlock},
		},
	}
	instr := GuardrailCheck(cfg)
	// The bad-regex instruction fires 500 on invocation
	next := runInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("want StopPlan for bad regex instruction, got %d", next)
	}
	if ctx.ResponseStatus != 500 {
		t.Errorf("want ResponseStatus 500, got %d", ctx.ResponseStatus)
	}
}

// ── 7. Webhook violated ───────────────────────────────────────────────────────

func TestGuardrailWebhookViolated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"violated":true}`))
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("harmful content")

	cfg := GuardrailConfig{
		ResultSlot: 0,
		FlagSlot:   -1,
		Providers: []GuardrailProviderConfig{
			{
				Provider:  "webhook",
				Endpoint:  srv.URL,
				APIKey:    "test-key",
				Action:    GuardrailBlock,
				TimeoutMs: 5000,
			},
		},
	}
	instr := GuardrailCheck(cfg)
	next := runInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("want StopPlan, got %d", next)
	}
	if !ctx.Failed {
		t.Error("ctx.Failed should be true")
	}
	if ctx.ResponseStatus != 400 {
		t.Errorf("want ResponseStatus 400, got %d", ctx.ResponseStatus)
	}
}

// ── 8. Webhook infra error → fail open ────────────────────────────────────────

func TestGuardrailWebhookInfraError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("some content")

	cfg := GuardrailConfig{
		ResultSlot: 0,
		FlagSlot:   -1,
		Providers: []GuardrailProviderConfig{
			{
				Provider:  "webhook",
				Endpoint:  srv.URL,
				Action:    GuardrailBlock,
				TimeoutMs: 5000,
			},
		},
	}
	instr := GuardrailCheck(cfg)
	next := runInstruction(instr, ctx)

	// fail open — step continues
	if next != 1 {
		t.Fatalf("want PC+1=1 (fail open), got %d", next)
	}
	if ctx.Failed {
		t.Error("ctx.Failed should be false on infra error (fail open)")
	}
}

// ── 9. OpenAI moderation flagged ──────────────────────────────────────────────

func TestGuardrailOpenAIModerationFlagged(t *testing.T) {
	resp := map[string]any{
		"results": []map[string]any{
			{"flagged": true},
		},
	}
	body, _ := json.Marshal(resp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("potentially harmful text")

	cfg := GuardrailConfig{
		ResultSlot: 0,
		FlagSlot:   -1,
		Providers: []GuardrailProviderConfig{
			{
				Provider:  "openai_moderation",
				Endpoint:  srv.URL,
				APIKey:    "test-key",
				Action:    GuardrailBlock,
				TimeoutMs: 5000,
			},
		},
	}
	instr := GuardrailCheck(cfg)
	next := runInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("want StopPlan, got %d", next)
	}
	if !ctx.Failed {
		t.Error("ctx.Failed should be true")
	}
}

// ── 10. Bedrock guardrail intervened ─────────────────────────────────────────

func TestGuardrailBedrockIntervened(t *testing.T) {
	resp := map[string]any{
		"action": "GUARDRAIL_INTERVENED",
	}
	body, _ := json.Marshal(resp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("output to check")

	cfg := GuardrailConfig{
		ResultSlot: 0,
		FlagSlot:   -1,
		Providers: []GuardrailProviderConfig{
			{
				Provider:        "bedrock",
				Endpoint:        srv.URL, // used as "region" param; function detects full URL
				APIKey:          "",      // empty → skip SigV4
				ResourceID:      "gr-001",
				ResourceVersion: "1",
				Action:          GuardrailBlock,
				TimeoutMs:       5000,
			},
		},
	}
	instr := GuardrailCheck(cfg)
	next := runInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("want StopPlan, got %d", next)
	}
	if !ctx.Failed {
		t.Error("ctx.Failed should be true")
	}
}

// ── 11. Model Armor match ────────────────────────────────────────────────────

func TestGuardrailModelArmorMatch(t *testing.T) {
	resp := map[string]any{
		"sanitizationResult": map[string]any{
			"filterMatchState": "MATCH_FOUND",
		},
	}
	body, _ := json.Marshal(resp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("content to sanitize")

	cfg := GuardrailConfig{
		ResultSlot: 0,
		FlagSlot:   -1,
		Providers: []GuardrailProviderConfig{
			{
				Provider:  "model_armor",
				Endpoint:  srv.URL,
				APIKey:    "test-key",
				Action:    GuardrailBlock,
				TimeoutMs: 5000,
			},
		},
	}
	instr := GuardrailCheck(cfg)
	next := runInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("want StopPlan, got %d", next)
	}
	if !ctx.Failed {
		t.Error("ctx.Failed should be true")
	}
}
