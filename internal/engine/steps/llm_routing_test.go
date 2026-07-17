package steps

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// newRoutingContext creates a test context with enough slot space for routing tests.
func newRoutingContext() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 16)
	ctx.IntSlots = make([]int64, 8)
	ctx.BoolSlots = make([]bool, 8)
	return ctx
}

func runRoutingInstruction(instr engine.Instruction, ctx *rctx.Context) int16 {
	state := &engine.ExecutionState{PC: 0}
	return instr.Action(ctx, state)
}

// â”€â”€ RouteLLM tests â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestRouteLLM_DefaultWhenNoRules(t *testing.T) {
	ctx := newRoutingContext()

	cfg := RouteLLMConfig{
		Rules:      nil, // no rules
		Default:    "claude-haiku-4-5",
		ResultSlot: 0,
		TokenSlot:  -1,
		MetaSlot:   -1,
	}
	instr := RouteLLM(cfg)
	next := runRoutingInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next PC=1, got %d", next)
	}
	if string(ctx.ByteSlots[0]) != "claude-haiku-4-5" {
		t.Errorf("default model: want %q, got %q", "claude-haiku-4-5", string(ctx.ByteSlots[0]))
	}
}

func TestRouteLLM_TokenCountRule(t *testing.T) {
	ctx := newRoutingContext()
	ctx.IntSlots[0] = 9000 // token count > 8000 â†’ should route to gemini-pro

	cfg := RouteLLMConfig{
		Rules: []RoutingRule{
			{Condition: "token_count > 8000", Model: "gemini-pro"},
			{Condition: "true", Model: "claude-haiku-4-5"},
		},
		Default:    "claude-haiku-4-5",
		ResultSlot: 1,
		TokenSlot:  0,
		MetaSlot:   -1,
	}
	instr := RouteLLM(cfg)
	next := runRoutingInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next PC=1, got %d", next)
	}
	if string(ctx.ByteSlots[1]) != "gemini-pro" {
		t.Errorf("token routing: want %q, got %q", "gemini-pro", string(ctx.ByteSlots[1]))
	}
}

func TestRouteLLM_TokenCountRule_NoMatch(t *testing.T) {
	ctx := newRoutingContext()
	ctx.IntSlots[0] = 500 // token count < 8000 â†’ should NOT trigger "token_count > 8000" rule

	cfg := RouteLLMConfig{
		Rules: []RoutingRule{
			{Condition: "token_count > 8000", Model: "gemini-pro"},
		},
		Default:    "claude-haiku-4-5",
		ResultSlot: 1,
		TokenSlot:  0,
		MetaSlot:   -1,
	}
	instr := RouteLLM(cfg)
	runRoutingInstruction(instr, ctx)

	if string(ctx.ByteSlots[1]) != "claude-haiku-4-5" {
		t.Errorf("no-match default: want %q, got %q", "claude-haiku-4-5", string(ctx.ByteSlots[1]))
	}
}

func TestRouteLLM_MetaRule(t *testing.T) {
	ctx := newRoutingContext()
	ctx.ByteSlots[2] = []byte("premium") // meta value

	cfg := RouteLLMConfig{
		Rules: []RoutingRule{
			{Condition: "meta == premium", Model: "claude-opus-4-6"},
			{Condition: "true", Model: "claude-haiku-4-5"},
		},
		Default:    "claude-haiku-4-5",
		ResultSlot: 0,
		TokenSlot:  -1,
		MetaSlot:   2,
	}
	instr := RouteLLM(cfg)
	runRoutingInstruction(instr, ctx)

	if string(ctx.ByteSlots[0]) != "claude-opus-4-6" {
		t.Errorf("meta routing: want %q, got %q", "claude-opus-4-6", string(ctx.ByteSlots[0]))
	}
}

func TestRouteLLM_MetaRule_NoMatch(t *testing.T) {
	ctx := newRoutingContext()
	ctx.ByteSlots[2] = []byte("free") // meta = free, not premium

	cfg := RouteLLMConfig{
		Rules: []RoutingRule{
			{Condition: "meta == premium", Model: "claude-opus-4-6"},
		},
		Default:    "claude-haiku-4-5",
		ResultSlot: 0,
		TokenSlot:  -1,
		MetaSlot:   2,
	}
	instr := RouteLLM(cfg)
	runRoutingInstruction(instr, ctx)

	if string(ctx.ByteSlots[0]) != "claude-haiku-4-5" {
		t.Errorf("meta no-match default: want %q, got %q", "claude-haiku-4-5", string(ctx.ByteSlots[0]))
	}
}

func TestRouteLLM_FirstMatchWins(t *testing.T) {
	ctx := newRoutingContext()
	ctx.IntSlots[0] = 9000 // token_count > 8000 AND meta = premium â†’ first rule (token) wins
	ctx.ByteSlots[2] = []byte("premium")

	cfg := RouteLLMConfig{
		Rules: []RoutingRule{
			{Condition: "token_count > 8000", Model: "gemini-pro"},
			{Condition: "meta == premium", Model: "claude-opus-4-6"},
			{Condition: "true", Model: "claude-haiku-4-5"},
		},
		Default:    "claude-haiku-4-5",
		ResultSlot: 0,
		TokenSlot:  0,
		MetaSlot:   2,
	}
	instr := RouteLLM(cfg)
	runRoutingInstruction(instr, ctx)

	if string(ctx.ByteSlots[0]) != "gemini-pro" {
		t.Errorf("first match wins: want %q, got %q", "gemini-pro", string(ctx.ByteSlots[0]))
	}
}

func TestRouteLLM_TrueConditionCatchAll(t *testing.T) {
	ctx := newRoutingContext()
	// No specific conditions match; "true" is catch-all

	cfg := RouteLLMConfig{
		Rules: []RoutingRule{
			{Condition: "token_count > 100000", Model: "gemini-ultra"},
			{Condition: "true", Model: "claude-haiku-4-5"},
		},
		Default:    "",
		ResultSlot: 0,
		TokenSlot:  0, // IntSlots[0] = 0 (default)
		MetaSlot:   -1,
	}
	instr := RouteLLM(cfg)
	runRoutingInstruction(instr, ctx)

	if string(ctx.ByteSlots[0]) != "claude-haiku-4-5" {
		t.Errorf("true catch-all: want %q, got %q", "claude-haiku-4-5", string(ctx.ByteSlots[0]))
	}
}

func TestRouteLLM_EmptyConditionCatchAll(t *testing.T) {
	ctx := newRoutingContext()

	cfg := RouteLLMConfig{
		Rules: []RoutingRule{
			{Condition: "", Model: "default-model"}, // empty condition = always match
		},
		Default:    "fallback",
		ResultSlot: 0,
		TokenSlot:  -1,
		MetaSlot:   -1,
	}
	instr := RouteLLM(cfg)
	runRoutingInstruction(instr, ctx)

	if string(ctx.ByteSlots[0]) != "default-model" {
		t.Errorf("empty condition: want %q, got %q", "default-model", string(ctx.ByteSlots[0]))
	}
}

func TestRouteLLM_UnrecognizedCondition_Skipped(t *testing.T) {
	ctx := newRoutingContext()

	cfg := RouteLLMConfig{
		Rules: []RoutingRule{
			{Condition: "unknown_field == value", Model: "bad-model"},
		},
		Default:    "safe-default",
		ResultSlot: 0,
		TokenSlot:  -1,
		MetaSlot:   -1,
	}
	instr := RouteLLM(cfg)
	runRoutingInstruction(instr, ctx)

	if string(ctx.ByteSlots[0]) != "safe-default" {
		t.Errorf("unrecognized condition skip: want %q, got %q", "safe-default", string(ctx.ByteSlots[0]))
	}
}

func TestRouteLLM_TokenCountLessThan(t *testing.T) {
	ctx := newRoutingContext()
	ctx.IntSlots[0] = 100 // small token count

	cfg := RouteLLMConfig{
		Rules: []RoutingRule{
			{Condition: "token_count < 500", Model: "fast-small-model"},
		},
		Default:    "standard-model",
		ResultSlot: 0,
		TokenSlot:  0,
		MetaSlot:   -1,
	}
	instr := RouteLLM(cfg)
	runRoutingInstruction(instr, ctx)

	if string(ctx.ByteSlots[0]) != "fast-small-model" {
		t.Errorf("token_count < N: want %q, got %q", "fast-small-model", string(ctx.ByteSlots[0]))
	}
}

func TestRouteLLM_TokenCountGTE(t *testing.T) {
	ctx := newRoutingContext()
	ctx.IntSlots[0] = 8000 // exactly 8000

	cfg := RouteLLMConfig{
		Rules: []RoutingRule{
			{Condition: "token_count >= 8000", Model: "large-context-model"},
		},
		Default:    "standard-model",
		ResultSlot: 0,
		TokenSlot:  0,
		MetaSlot:   -1,
	}
	instr := RouteLLM(cfg)
	runRoutingInstruction(instr, ctx)

	if string(ctx.ByteSlots[0]) != "large-context-model" {
		t.Errorf("token_count >= N: want %q, got %q", "large-context-model", string(ctx.ByteSlots[0]))
	}
}

func TestRouteLLM_MetaNotEqual(t *testing.T) {
	ctx := newRoutingContext()
	ctx.ByteSlots[3] = []byte("free")

	cfg := RouteLLMConfig{
		Rules: []RoutingRule{
			{Condition: "meta != premium", Model: "budget-model"},
		},
		Default:    "standard-model",
		ResultSlot: 0,
		TokenSlot:  -1,
		MetaSlot:   3,
	}
	instr := RouteLLM(cfg)
	runRoutingInstruction(instr, ctx)

	if string(ctx.ByteSlots[0]) != "budget-model" {
		t.Errorf("meta != VALUE: want %q, got %q", "budget-model", string(ctx.ByteSlots[0]))
	}
}

// â”€â”€ LLMCall dynamic model slot tests â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestLLMCall_DynamicModel_FromSlot(t *testing.T) {
	// Mock server that responds like an Anthropic endpoint
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content":     []any{map[string]any{"type": "text", "text": "dynamic model response"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	defer srv.Close()

	// Catalog: two models pointing to the same mock server
	dynamicModel := config.LLMModelConfig{
		Alias:      "dynamic-model",
		Provider:  "test",
		Adapter:   config.AdapterAnthropic,
		BaseURL:   srv.URL,
		MaxTokens: 500,
		Capabilities: config.ModelCapabilities{
			MaxContextTokens: 16000,
		},
	}
	defaultModel := config.LLMModelConfig{
		Alias:      "default-model",
		Provider:  "test",
		Adapter:   config.AdapterAnthropic,
		BaseURL:   srv.URL,
		MaxTokens: 500,
	}

	catalog := map[string]config.LLMModelConfig{
		"dynamic-model": dynamicModel,
		"default-model": defaultModel,
	}

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("What is the weather?") // prompt in slot 0
	ctx.ByteSlots[2] = []byte("dynamic-model")        // model override in slot 2

	cfg := LLMCallConfig{
		ModelConfig:  defaultModel,
		APIKey:       "test-key",
		PromptSlot:   0,
		ResultSlot:   1,
		SystemSlot:   -1,
		MaxTokens:    100,
		Temperature:  0.5,
		ModelSlot:    2, // read model slug from slot 2 at runtime
		ModelCatalog: catalog,
	}

	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next PC=1, got %d (status=%d, err=%s)", next, ctx.ResponseStatus, ctx.ErrorMsg)
	}
	if string(ctx.ByteSlots[1]) != "dynamic model response" {
		t.Errorf("result: want %q, got %q", "dynamic model response", string(ctx.ByteSlots[1]))
	}
}

func TestLLMCall_DynamicModel_NotFoundInCatalog(t *testing.T) {
	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("prompt text")
	ctx.ByteSlots[2] = []byte("nonexistent-model") // slug not in catalog

	baseModel := config.LLMModelConfig{
		Alias:    "base-model",
		Adapter: config.AdapterAnthropic,
	}
	catalog := map[string]config.LLMModelConfig{
		"base-model": baseModel,
	}

	cfg := LLMCallConfig{
		ModelConfig:  baseModel,
		APIKey:       "key",
		PromptSlot:   0,
		ResultSlot:   1,
		SystemSlot:   -1,
		MaxTokens:    100,
		ModelSlot:    2,
		ModelCatalog: catalog,
	}

	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("want StopPlan for missing dynamic model, got %d", next)
	}
	if ctx.ResponseStatus != 500 {
		t.Errorf("want 500, got %d", ctx.ResponseStatus)
	}
	if string(ctx.ErrorMsg) == "" {
		t.Error("want error message, got empty")
	}
}

func TestLLMCall_DynamicModel_SlotEmpty_UsesBakedModel(t *testing.T) {
	// When the model slot is empty, should fall back to the baked model
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content":     []any{map[string]any{"type": "text", "text": "baked model response"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 3},
		})
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("test prompt")
	// slot 2 is nil/empty â†’ use baked model

	bakedModel := config.LLMModelConfig{
		Alias:     "baked-model",
		Adapter:  config.AdapterAnthropic,
		BaseURL:  srv.URL,
		MaxTokens: 100,
	}
	catalog := map[string]config.LLMModelConfig{
		"baked-model": bakedModel,
	}

	cfg := LLMCallConfig{
		ModelConfig:  bakedModel,
		APIKey:       "test-key",
		PromptSlot:   0,
		ResultSlot:   1,
		SystemSlot:   -1,
		MaxTokens:    100,
		ModelSlot:    2, // slot 2 is empty â†’ no override
		ModelCatalog: catalog,
	}

	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next=1, got %d (status=%d)", next, ctx.ResponseStatus)
	}
	if string(ctx.ByteSlots[1]) != "baked model response" {
		t.Errorf("result: want %q, got %q", "baked model response", string(ctx.ByteSlots[1]))
	}
}

func TestLLMCall_DynamicModel_NilCatalog_UsesBakedModel(t *testing.T) {
	// When ModelCatalog is nil (backward compat), dynamic routing disabled
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content":     []any{map[string]any{"type": "text", "text": "baked only"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 3},
		})
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("prompt")
	ctx.ByteSlots[2] = []byte("some-model") // slot set but catalog nil â†’ no override

	bakedModel := config.LLMModelConfig{
		Alias:    "baked-model",
		Adapter: config.AdapterAnthropic,
		BaseURL: srv.URL,
	}

	cfg := LLMCallConfig{
		ModelConfig:  bakedModel,
		APIKey:       "key",
		PromptSlot:   0,
		ResultSlot:   1,
		SystemSlot:   -1,
		MaxTokens:    100,
		ModelSlot:    2,
		ModelCatalog: nil, // nil catalog disables dynamic routing
	}

	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next=1, got %d (status=%d)", next, ctx.ResponseStatus)
	}
	if string(ctx.ByteSlots[1]) != "baked only" {
		t.Errorf("result: want %q, got %q", "baked only", string(ctx.ByteSlots[1]))
	}
}
