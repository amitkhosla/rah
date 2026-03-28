package steps

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// Note: newTestContext is defined in llm_execute_plan_test.go (uses InitSlots).

func runInstruction(instr engine.Instruction, ctx *rctx.Context) int16 {
	state := &engine.ExecutionState{PC: 0}
	return instr.Action(ctx, state)
}

func modelConfig(adapter config.LLMProviderAdapter, baseURL string) config.LLMModelConfig {
	return config.LLMModelConfig{
		Slug:      "test-model",
		Provider:  "test",
		Adapter:   adapter,
		BaseURL:   baseURL,
		MaxTokens: 1000,
		Capabilities: config.ModelCapabilities{
			MaxContextTokens: 8000,
		},
	}
}

// ── adapter marshal / unmarshal ───────────────────────────────────────────────

func TestAnthropicAdapter_Marshal(t *testing.T) {
	a := &anthropicAdapter{}
	req := LLMRequest{
		Messages:    []CanonicalMessage{{Role: RoleUser, Content: "Hello"}},
		System:      "You are helpful.",
		Model:       "claude-sonnet-4-6",
		MaxTokens:   100,
		Temperature: 0.7,
	}
	b, err := a.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal check: %v", err)
	}
	if got["system"] != "You are helpful." {
		t.Errorf("system: got %v", got["system"])
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages len: %d", len(msgs))
	}
}

func TestOpenAIAdapter_Marshal(t *testing.T) {
	a := &openAIAdapter{}
	req := LLMRequest{
		Messages:  []CanonicalMessage{{Role: RoleUser, Content: "Hi"}},
		System:    "Be concise.",
		Model:     "gpt-4",
		MaxTokens: 50,
	}
	b, err := a.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(b, &got)
	msgs := got["messages"].([]any)
	// system prepended as first message
	if len(msgs) != 2 {
		t.Fatalf("messages len: want 2, got %d", len(msgs))
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Errorf("first role: %v", first["role"])
	}
}

func TestGeminiAdapter_Marshal(t *testing.T) {
	a := &geminiAdapter{}
	req := LLMRequest{
		Messages:  []CanonicalMessage{{Role: RoleUser, Content: "Translate this"}},
		System:    "You translate.",
		Model:     "gemini-pro",
		MaxTokens: 200,
	}
	b, err := a.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(b, &got)
	if got["systemInstruction"] == nil {
		t.Error("systemInstruction missing")
	}
	if got["contents"] == nil {
		t.Error("contents missing")
	}
}

func TestOllamaAdapter_Marshal(t *testing.T) {
	a := &ollamaAdapter{}
	req := LLMRequest{
		Messages: []CanonicalMessage{{Role: RoleUser, Content: "Hi"}},
		Model:    "llama3",
	}
	b, err := a.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	json.Unmarshal(b, &got)
	if got["stream"] != false {
		t.Errorf("stream should be false, got %v", got["stream"])
	}
}

// ── adapter endpoints ─────────────────────────────────────────────────────────

func TestAdapterEndpoints(t *testing.T) {
	cases := []struct {
		adapter  ProviderAdapter
		wantSuffix string
	}{
		{&anthropicAdapter{}, "/v1/messages"},
		{&openAIAdapter{}, "/v1/chat/completions"},
		{&geminiAdapter{}, "/v1beta/models/my-model:generateContent"},
		{&ollamaAdapter{}, "/api/chat"},
	}
	for _, c := range cases {
		ep := c.adapter.Endpoint("http://localhost", "my-model")
		if len(ep) < len(c.wantSuffix) || ep[len(ep)-len(c.wantSuffix):] != c.wantSuffix {
			t.Errorf("Endpoint: want suffix %q, got %q", c.wantSuffix, ep)
		}
	}
}

// ── LLMCall instruction ───────────────────────────────────────────────────────

func anthropicOKServer(t *testing.T, responseText string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content":     []any{map[string]any{"type": "text", "text": responseText}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
	}))
}

func TestLLMCall_Success(t *testing.T) {
	srv := anthropicOKServer(t, "Hello from LLM!")
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("What is 2+2?")

	cfg := LLMCallConfig{
		ModelConfig: modelConfig(config.AdapterAnthropic, srv.URL),
		APIKey:      "test-key",
		PromptSlot:  0,
		ResultSlot:  1,
		SystemSlot:  -1,
		MaxTokens:   100,
		Temperature: 0.5,
	}
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("next PC: want 1, got %d", next)
	}
	if string(ctx.ByteSlots[1]) != "Hello from LLM!" {
		t.Errorf("result: %q", ctx.ByteSlots[1])
	}
}

func TestLLMCall_EmptyPrompt_Skips(t *testing.T) {
	ctx := newTestContext()
	// ByteSlots[0] is nil → empty prompt
	cfg := LLMCallConfig{
		ModelConfig: modelConfig(config.AdapterAnthropic, "http://localhost:1"),
		PromptSlot:  0,
		ResultSlot:  1,
		SystemSlot:  -1,
		MaxTokens:   100,
	}
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)
	if next != 1 {
		t.Errorf("empty prompt should skip, got PC %d", next)
	}
}

func TestLLMCall_TokenLimitExceeded_Returns413(t *testing.T) {
	ctx := newTestContext()
	// ~4000 chars ≈ 1000 tokens; add maxTokens → exceeds 1100 limit
	longPrompt := make([]byte, 4000)
	for i := range longPrompt {
		longPrompt[i] = 'a'
	}
	ctx.ByteSlots[0] = longPrompt

	cfg := LLMCallConfig{
		ModelConfig: config.LLMModelConfig{
			Slug:    "small-model",
			Adapter: config.AdapterAnthropic,
			Capabilities: config.ModelCapabilities{
				MaxContextTokens: 1100, // prompt ~1000 + maxTokens 200 > 1100
			},
		},
		PromptSlot: 0,
		ResultSlot: 1,
		SystemSlot: -1,
		MaxTokens:  200,
	}
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Fatalf("want StopPlan, got %d", next)
	}
	if ctx.ResponseStatus != 413 {
		t.Errorf("want 413, got %d", ctx.ResponseStatus)
	}
}

func TestLLMCall_RetryOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(429)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content":     []any{map[string]any{"type": "text", "text": "ok"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 2},
		})
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("retry me")

	cfg := LLMCallConfig{
		ModelConfig: modelConfig(config.AdapterAnthropic, srv.URL),
		APIKey:      "key",
		PromptSlot:  0,
		ResultSlot:  1,
		SystemSlot:  -1,
		MaxRetries:  2,
		MaxTokens:   50,
	}
	// Patch backoff to zero for test speed
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Errorf("want success after retry, got PC %d (status %d)", next, ctx.ResponseStatus)
	}
	if attempts < 2 {
		t.Errorf("want at least 2 attempts, got %d", attempts)
	}
}

func TestLLMCall_MissingAPIKey_StillCallsProvider(t *testing.T) {
	// Provider returns 401 for missing key — verify we get StopPlan + non-200 status
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("hello")

	cfg := LLMCallConfig{
		ModelConfig: modelConfig(config.AdapterAnthropic, srv.URL),
		APIKey:      "", // no key
		PromptSlot:  0,
		ResultSlot:  1,
		SystemSlot:  -1,
		MaxTokens:   50,
		MaxRetries:  0,
	}
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Errorf("want StopPlan, got %d", next)
	}
	if ctx.ResponseStatus != 401 {
		t.Errorf("want 401, got %d", ctx.ResponseStatus)
	}
}

func TestLLMCall_UnknownAdapter(t *testing.T) {
	cfg := LLMCallConfig{
		ModelConfig: config.LLMModelConfig{
			Slug:    "bad",
			Adapter: "nonexistent",
		},
		PromptSlot: 0,
		ResultSlot: 1,
		SystemSlot: -1,
	}
	instr := LLMCall(cfg)
	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("test")
	next := runInstruction(instr, ctx)

	if next != engine.StopPlan {
		t.Errorf("want StopPlan for bad adapter, got %d", next)
	}
	if ctx.ResponseStatus != 500 {
		t.Errorf("want 500, got %d", ctx.ResponseStatus)
	}
}

func TestLLMCall_OpenAI_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"role": "assistant", "content": "OpenAI reply"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("test prompt")

	cfg := LLMCallConfig{
		ModelConfig: modelConfig(config.AdapterOpenAI, srv.URL),
		APIKey:      "sk-test",
		PromptSlot:  0,
		ResultSlot:  1,
		SystemSlot:  -1,
		MaxTokens:   100,
	}
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next=1, got %d (status %d)", next, ctx.ResponseStatus)
	}
	if string(ctx.ByteSlots[1]) != "OpenAI reply" {
		t.Errorf("result: %q", ctx.ByteSlots[1])
	}
}

func TestLLMCall_Gemini_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{map[string]any{
				"content": map[string]any{
					"parts": []any{map[string]any{"text": "Gemini reply"}},
				},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{"promptTokenCount": 5, "candidatesTokenCount": 2},
		})
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("translate")

	cfg := LLMCallConfig{
		ModelConfig: modelConfig(config.AdapterGemini, srv.URL),
		APIKey:      "gemini-key",
		PromptSlot:  0,
		ResultSlot:  1,
		SystemSlot:  -1,
		MaxTokens:   100,
	}
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next=1, got %d (status %d)", next, ctx.ResponseStatus)
	}
	if string(ctx.ByteSlots[1]) != "Gemini reply" {
		t.Errorf("result: %q", ctx.ByteSlots[1])
	}
}

func TestLLMCall_Ollama_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message":          map[string]any{"role": "assistant", "content": "Ollama reply"},
			"done_reason":      "stop",
			"prompt_eval_count": 4,
			"eval_count":       3,
		})
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("ollama prompt")

	cfg := LLMCallConfig{
		ModelConfig: modelConfig(config.AdapterOllama, srv.URL),
		PromptSlot:  0,
		ResultSlot:  1,
		SystemSlot:  -1,
		MaxTokens:   100,
	}
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next=1, got %d (status %d)", next, ctx.ResponseStatus)
	}
	if string(ctx.ByteSlots[1]) != "Ollama reply" {
		t.Errorf("result: %q", ctx.ByteSlots[1])
	}
}

func TestLLMCall_SystemSlot(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = b
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content":     []any{map[string]any{"type": "text", "text": "done"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 10, "output_tokens": 2},
		})
	}))
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("user prompt")
	ctx.ByteSlots[2] = []byte("you are a test assistant")

	cfg := LLMCallConfig{
		ModelConfig: modelConfig(config.AdapterAnthropic, srv.URL),
		APIKey:      "k",
		PromptSlot:  0,
		ResultSlot:  1,
		SystemSlot:  2,
		MaxTokens:   50,
	}
	instr := LLMCall(cfg)
	runInstruction(instr, ctx)

	var body map[string]any
	json.Unmarshal(capturedBody, &body)
	if body["system"] != "you are a test assistant" {
		t.Errorf("system prompt not sent: %v", body["system"])
	}
}

// ── token estimation ──────────────────────────────────────────────────────────

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		input string
		want  int // at least this many (rough lower bound)
	}{
		{"", 0},
		{"hello", 2},                // 5 chars → 2 tokens
		{"hello world foo bar", 5},  // 19 chars → 5 tokens
	}
	for _, c := range cases {
		got := estimateTokens(c.input)
		if got < c.want {
			t.Errorf("estimateTokens(%q): got %d, want >= %d", c.input, got, c.want)
		}
	}
}

// ── APIKeySlot and token slot tests ──────────────────────────────────────────

// captureAuthServer creates a test server that records the Authorization (or
// x-api-key) header from the incoming request, then returns a minimal
// Anthropic-format success response with the given token counts.
func captureAuthServer(t *testing.T, gotKey *string, inputTokens, outputTokens int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Anthropic adapter uses "x-api-key" header.
		*gotKey = r.Header.Get("x-api-key")
		if *gotKey == "" {
			*gotKey = r.Header.Get("Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content":     []any{map[string]any{"type": "text", "text": "ok"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens},
		})
	}))
}

func TestLLMCall_APIKeyFromSlot(t *testing.T) {
	var gotKey string
	srv := captureAuthServer(t, &gotKey, 10, 5)
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("what is 1+1?")
	ctx.ByteSlots[3] = []byte("sk-from-slot") // runtime key in slot 3

	cfg := LLMCallConfig{
		ModelConfig:  modelConfig(config.AdapterAnthropic, srv.URL),
		APIKey:       "sk-baked-key",
		PromptSlot:   0,
		ResultSlot:   1,
		SystemSlot:   -1,
		APIKeySlot:   3,
		InputTokensSlot:  -1,
		OutputTokensSlot: -1,
		MaxTokens:    50,
	}
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next=1, got %d (status %d)", next, ctx.ResponseStatus)
	}
	if gotKey != "sk-from-slot" {
		t.Errorf("want auth key %q, got %q", "sk-from-slot", gotKey)
	}
}

func TestLLMCall_APIKeySlotEmpty_FallsBackToBakedKey(t *testing.T) {
	var gotKey string
	srv := captureAuthServer(t, &gotKey, 8, 3)
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("prompt")
	// slot 3 is intentionally empty — should fall back to baked key

	cfg := LLMCallConfig{
		ModelConfig:  modelConfig(config.AdapterAnthropic, srv.URL),
		APIKey:       "sk-baked-fallback",
		PromptSlot:   0,
		ResultSlot:   1,
		SystemSlot:   -1,
		APIKeySlot:   3,
		InputTokensSlot:  -1,
		OutputTokensSlot: -1,
		MaxTokens:    50,
	}
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next=1, got %d (status %d)", next, ctx.ResponseStatus)
	}
	if gotKey != "sk-baked-fallback" {
		t.Errorf("want baked key %q, got %q", "sk-baked-fallback", gotKey)
	}
}

func TestLLMCall_TokenSlotsWritten(t *testing.T) {
	srv := captureAuthServer(t, new(string), 42, 17)
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("count my tokens")

	cfg := LLMCallConfig{
		ModelConfig:      modelConfig(config.AdapterAnthropic, srv.URL),
		APIKey:           "test-key",
		PromptSlot:       0,
		ResultSlot:       1,
		SystemSlot:       -1,
		APIKeySlot:       -1,
		InputTokensSlot:  2,
		OutputTokensSlot: 3,
		MaxTokens:        50,
	}
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next=1, got %d (status %d)", next, ctx.ResponseStatus)
	}
	if ctx.IntSlots[2] != 42 {
		t.Errorf("InputTokensSlot: want 42, got %d", ctx.IntSlots[2])
	}
	if ctx.IntSlots[3] != 17 {
		t.Errorf("OutputTokensSlot: want 17, got %d", ctx.IntSlots[3])
	}
}

func TestLLMCall_TokenSlotsNegative_NoWrite(t *testing.T) {
	srv := captureAuthServer(t, new(string), 10, 5)
	defer srv.Close()

	ctx := newTestContext()
	ctx.ByteSlots[0] = []byte("prompt")
	ctx.IntSlots[0] = 99 // pre-set sentinel; should remain unchanged
	ctx.IntSlots[1] = 88

	cfg := LLMCallConfig{
		ModelConfig:      modelConfig(config.AdapterAnthropic, srv.URL),
		APIKey:           "test-key",
		PromptSlot:       0,
		ResultSlot:       1,
		SystemSlot:       -1,
		APIKeySlot:       -1,
		InputTokensSlot:  -1, // disabled
		OutputTokensSlot: -1, // disabled
		MaxTokens:        50,
	}
	instr := LLMCall(cfg)
	next := runInstruction(instr, ctx)

	if next != 1 {
		t.Fatalf("want next=1, got %d (status %d)", next, ctx.ResponseStatus)
	}
	// Sentinel values must be untouched
	if ctx.IntSlots[0] != 99 {
		t.Errorf("IntSlots[0] should be unchanged (99), got %d", ctx.IntSlots[0])
	}
	if ctx.IntSlots[1] != 88 {
		t.Errorf("IntSlots[1] should be unchanged (88), got %d", ctx.IntSlots[1])
	}
}
