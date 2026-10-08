package control

import (
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/engine/steps"
)

// newGuardrailCompiler creates a minimal Compiler suitable for guardrail tests.
func newGuardrailCompiler(t *testing.T) *Compiler {
	t.Helper()
	fm := engine.NewFlowManager(16, config.GlobalLayout{MaxBytesSlots: 32, MaxIntsSlots: 8, MaxBoolsSlots: 8})
	return NewCompiler(fm)
}

// ── 1. No guardrail.* keys → zero-rule config ─────────────────────────────────

func TestCompileGuardrailNoKeys(t *testing.T) {
	c := newGuardrailCompiler(t)
	// Allocate resultSlot 0
	resultSlot := 0

	step := StepConfig{
		Action: "llm_call",
		Input:  map[string]string{"model": "gpt-4"},
	}

	cfg, err := compileGuardrailConfig(c, step, resultSlot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.RegexRules) != 0 {
		t.Errorf("want 0 regex rules, got %d", len(cfg.RegexRules))
	}
	if len(cfg.Providers) != 0 {
		t.Errorf("want 0 providers, got %d", len(cfg.Providers))
	}
	if cfg.ResultSlot != resultSlot {
		t.Errorf("want ResultSlot=%d, got %d", resultSlot, cfg.ResultSlot)
	}
	if cfg.FlagSlot != -1 {
		t.Errorf("want FlagSlot=-1 (disabled), got %d", cfg.FlagSlot)
	}
}

// ── 2. Valid regex rules parsed correctly ─────────────────────────────────────

func TestCompileGuardrailRegexRulesValid(t *testing.T) {
	c := newGuardrailCompiler(t)
	resultSlot := 0

	rulesJSON := `[{"pattern":"\\d{3}-\\d{4}","action":"block","label":"phone"},{"pattern":"\\S+@\\S+","action":"redact","replacement":"[EMAIL]"}]`
	step := StepConfig{
		Action: "llm_call",
		Input: map[string]string{
			"guardrail.regex_rules": rulesJSON,
		},
	}

	cfg, err := compileGuardrailConfig(c, step, resultSlot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.RegexRules) != 2 {
		t.Fatalf("want 2 regex rules, got %d", len(cfg.RegexRules))
	}
	if cfg.RegexRules[0].Pattern != `\d{3}-\d{4}` {
		t.Errorf("rule[0].Pattern mismatch: %q", cfg.RegexRules[0].Pattern)
	}
	if cfg.RegexRules[0].Action != steps.GuardrailBlock {
		t.Errorf("rule[0].Action want block, got %q", cfg.RegexRules[0].Action)
	}
	if cfg.RegexRules[0].Label != "phone" {
		t.Errorf("rule[0].Label want 'phone', got %q", cfg.RegexRules[0].Label)
	}
	if cfg.RegexRules[1].Action != steps.GuardrailRedact {
		t.Errorf("rule[1].Action want redact, got %q", cfg.RegexRules[1].Action)
	}
	if cfg.RegexRules[1].Replacement != "[EMAIL]" {
		t.Errorf("rule[1].Replacement want '[EMAIL]', got %q", cfg.RegexRules[1].Replacement)
	}
}

// ── 3. Invalid JSON → error ───────────────────────────────────────────────────

func TestCompileGuardrailRegexRulesInvalidJSON(t *testing.T) {
	c := newGuardrailCompiler(t)

	step := StepConfig{
		Action: "llm_call",
		Input: map[string]string{
			"guardrail.regex_rules": `{bad json`,
		},
	}

	_, err := compileGuardrailConfig(c, step, 0)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// ── 4. Flag slot resolved correctly ──────────────────────────────────────────

func TestCompileGuardrailFlagSlot(t *testing.T) {
	c := newGuardrailCompiler(t)
	resultSlot := 0

	step := StepConfig{
		Action: "llm_call",
		Input: map[string]string{
			"guardrail.regex_rules": `[{"pattern":"bad","action":"flag"}]`,
			"guardrail.flag_slot":   "guardrail_flagged",
		},
	}

	cfg, err := compileGuardrailConfig(c, step, resultSlot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// FlagSlot should be >= 0 (a valid slot was allocated)
	if cfg.FlagSlot < 0 {
		t.Errorf("want FlagSlot >= 0, got %d", cfg.FlagSlot)
	}
	if len(cfg.RegexRules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(cfg.RegexRules))
	}
	// When no action set, defaults to guardrail.on_violation which defaults to block,
	// but since the JSON here has "flag" explicitly set it should remain flag.
	if cfg.RegexRules[0].Action != steps.GuardrailFlag {
		t.Errorf("want action=flag, got %q", cfg.RegexRules[0].Action)
	}
}

// ── 5. Default action applied when rule has no action ─────────────────────────

func TestCompileGuardrailDefaultAction(t *testing.T) {
	c := newGuardrailCompiler(t)

	step := StepConfig{
		Action: "llm_call",
		Input: map[string]string{
			"guardrail.on_violation": "redact",
			"guardrail.regex_rules":  `[{"pattern":"secret"}]`,
		},
	}

	cfg, err := compileGuardrailConfig(c, step, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RegexRules[0].Action != steps.GuardrailRedact {
		t.Errorf("want default action redact, got %q", cfg.RegexRules[0].Action)
	}
}
