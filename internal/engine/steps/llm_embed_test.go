package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// makeEmbedCtx creates a minimal Context with enough slots for tests.
func makeEmbedCtx() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.InitSlots()
	ctx.ResponseStatus = 200
	return ctx
}

// makeEmbedState returns an ExecutionState at PC=0.
func makeEmbedState() *engine.ExecutionState {
	return &engine.ExecutionState{PC: 0}
}

// openAIEmbedResponse returns a valid OpenAI embeddings response JSON.
func openAIEmbedResponse(vec []float64) []byte {
	type dataItem struct {
		Embedding []float64 `json:"embedding"`
	}
	type resp struct {
		Data []dataItem `json:"data"`
	}
	b, _ := json.Marshal(resp{Data: []dataItem{{Embedding: vec}}})
	return b
}

// ollamaEmbedResponse returns a valid Ollama embeddings response JSON.
func ollamaEmbedResponse(vec []float64) []byte {
	type resp struct {
		Embedding []float64 `json:"embedding"`
	}
	b, _ := json.Marshal(resp{Embedding: vec})
	return b
}

// geminiEmbedResponse returns a valid Gemini embedContent response JSON.
func geminiEmbedResponse(vec []float64) []byte {
	type valuesWrap struct {
		Values []float64 `json:"values"`
	}
	type resp struct {
		Embedding valuesWrap `json:"embedding"`
	}
	b, _ := json.Marshal(resp{Embedding: valuesWrap{Values: vec}})
	return b
}

func TestEmbedText_OpenAI_Success(t *testing.T) {
	wantVec := []float64{0.1, -0.2, 0.3}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAIEmbedResponse(wantVec))
	}))
	defer srv.Close()

	ctx := makeEmbedCtx()
	ctx.ByteSlots[0] = []byte("hello world")

	cfg := EmbedTextConfig{
		InputSlot:  0,
		ResultSlot: 1,
		DimSlot:    0,
		Provider:   EmbedProviderOpenAI,
		Model:      "text-embedding-3-small",
		BaseURL:    srv.URL,
		APIKey:     "sk-test",
		APIKeySlot: -1,
		TimeoutMs:  5000,
		MaxRetries: 0,
	}

	state := makeEmbedState()
	instr := EmbedText(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1=%d, got %d (failed=%v, errorMsg=%s)", state.PC+1, nextPC, ctx.Failed, ctx.ErrorMsg)
	}
	if ctx.Failed {
		t.Fatalf("expected no failure, got Failed=true, errorMsg=%s", ctx.ErrorMsg)
	}

	// Check result slot has JSON float array
	var gotVec []float64
	if err := json.Unmarshal(ctx.ByteSlots[1], &gotVec); err != nil {
		t.Fatalf("result slot is not valid JSON float array: %v", err)
	}
	if len(gotVec) != len(wantVec) {
		t.Fatalf("expected vector length %d, got %d", len(wantVec), len(gotVec))
	}
	for i, v := range wantVec {
		if gotVec[i] != v {
			t.Errorf("vector[%d]: want %f, got %f", i, v, gotVec[i])
		}
	}

	// Check dim slot
	if ctx.IntSlots[0] != int64(len(wantVec)) {
		t.Errorf("DimSlot: want %d, got %d", len(wantVec), ctx.IntSlots[0])
	}
}

func TestEmbedText_Ollama_Success(t *testing.T) {
	wantVec := []float64{0.5, 0.6, 0.7, 0.8}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(ollamaEmbedResponse(wantVec))
	}))
	defer srv.Close()

	ctx := makeEmbedCtx()
	ctx.ByteSlots[0] = []byte("embed this text")

	cfg := EmbedTextConfig{
		InputSlot:  0,
		ResultSlot: 1,
		DimSlot:    0,
		Provider:   EmbedProviderOllama,
		Model:      "nomic-embed-text",
		BaseURL:    srv.URL,
		APIKeySlot: -1,
		TimeoutMs:  5000,
		MaxRetries: 0,
	}

	state := makeEmbedState()
	instr := EmbedText(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (failed=%v, errorMsg=%s)", nextPC, ctx.Failed, ctx.ErrorMsg)
	}

	var gotVec []float64
	if err := json.Unmarshal(ctx.ByteSlots[1], &gotVec); err != nil {
		t.Fatalf("result slot not valid JSON: %v", err)
	}
	if len(gotVec) != len(wantVec) {
		t.Fatalf("expected vector length %d, got %d", len(wantVec), len(gotVec))
	}
	if ctx.IntSlots[0] != int64(len(wantVec)) {
		t.Errorf("DimSlot: want %d, got %d", len(wantVec), ctx.IntSlots[0])
	}
}

func TestEmbedText_Gemini_Success(t *testing.T) {
	wantVec := []float64{0.01, 0.02, 0.03}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(geminiEmbedResponse(wantVec))
	}))
	defer srv.Close()

	ctx := makeEmbedCtx()
	ctx.ByteSlots[0] = []byte("gemini test input")

	cfg := EmbedTextConfig{
		InputSlot:  0,
		ResultSlot: 1,
		DimSlot:    0,
		Provider:   EmbedProviderGemini,
		Model:      "text-embedding-004",
		BaseURL:    srv.URL,
		APIKey:     "gemini-key",
		APIKeySlot: -1,
		TimeoutMs:  5000,
		MaxRetries: 0,
	}

	state := makeEmbedState()
	instr := EmbedText(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (failed=%v, errorMsg=%s)", nextPC, ctx.Failed, ctx.ErrorMsg)
	}

	var gotVec []float64
	if err := json.Unmarshal(ctx.ByteSlots[1], &gotVec); err != nil {
		t.Fatalf("result slot not valid JSON: %v", err)
	}
	if len(gotVec) != len(wantVec) {
		t.Fatalf("expected vector length %d, got %d", len(wantVec), len(gotVec))
	}
}

func TestEmbedText_EmptyInputSlot_Noop(t *testing.T) {
	ctx := makeEmbedCtx()
	// InputSlot[0] is empty (nil)

	cfg := EmbedTextConfig{
		InputSlot:  0,
		ResultSlot: 1,
		DimSlot:    -1,
		Provider:   EmbedProviderOpenAI,
		Model:      "text-embedding-3-small",
		APIKey:     "sk-test",
		APIKeySlot: -1,
	}

	state := makeEmbedState()
	instr := EmbedText(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1 (no-op), got %d", nextPC)
	}
	if ctx.Failed {
		t.Errorf("expected no failure on empty input, got Failed=true")
	}
	if ctx.ByteSlots[1] != nil {
		t.Errorf("expected result slot to remain nil, got %v", ctx.ByteSlots[1])
	}
}

func TestEmbedText_APIKeyFromSlot(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAIEmbedResponse([]float64{1.0, 2.0}))
	}))
	defer srv.Close()

	ctx := makeEmbedCtx()
	ctx.ByteSlots[0] = []byte("input text")
	ctx.ByteSlots[2] = []byte("sk-runtime-key") // APIKeySlot=2

	cfg := EmbedTextConfig{
		InputSlot:  0,
		ResultSlot: 1,
		DimSlot:    -1,
		Provider:   EmbedProviderOpenAI,
		Model:      "text-embedding-3-small",
		BaseURL:    srv.URL,
		APIKey:     "sk-baked-key",
		APIKeySlot: 2,
		TimeoutMs:  5000,
		MaxRetries: 0,
	}

	state := makeEmbedState()
	instr := EmbedText(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (failed=%v, errorMsg=%s)", nextPC, ctx.Failed, ctx.ErrorMsg)
	}
	if gotAuthHeader != "Bearer sk-runtime-key" {
		t.Errorf("expected Authorization 'Bearer sk-runtime-key', got %q", gotAuthHeader)
	}
}

func TestEmbedText_ServerError_StopsExecution(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"server error"}}`))
	}))
	defer srv.Close()

	ctx := makeEmbedCtx()
	ctx.ByteSlots[0] = []byte("test input")

	cfg := EmbedTextConfig{
		InputSlot:  0,
		ResultSlot: 1,
		DimSlot:    -1,
		Provider:   EmbedProviderOpenAI,
		Model:      "text-embedding-3-small",
		BaseURL:    srv.URL,
		APIKey:     "sk-test",
		APIKeySlot: -1,
		TimeoutMs:  5000,
		MaxRetries: 0,
	}

	state := makeEmbedState()
	instr := EmbedText(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != engine.StopPlan {
		t.Fatalf("expected StopPlan, got %d", nextPC)
	}
	if !ctx.Failed {
		t.Error("expected Failed=true")
	}
	if ctx.ResponseStatus != 502 {
		t.Errorf("expected ResponseStatus=502, got %d", ctx.ResponseStatus)
	}
}

func TestEmbedText_Retry_On429(t *testing.T) {
	var callCount atomic.Int32
	wantVec := []float64{0.9, 0.8}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"rate limit"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAIEmbedResponse(wantVec))
	}))
	defer srv.Close()

	ctx := makeEmbedCtx()
	ctx.ByteSlots[0] = []byte("retry test input")

	cfg := EmbedTextConfig{
		InputSlot:  0,
		ResultSlot: 1,
		DimSlot:    -1,
		Provider:   EmbedProviderOpenAI,
		Model:      "text-embedding-3-small",
		BaseURL:    srv.URL,
		APIKey:     "sk-test",
		APIKeySlot: -1,
		TimeoutMs:  5000,
		MaxRetries: 2,
	}

	state := makeEmbedState()
	instr := EmbedText(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1 after retry success, got %d (failed=%v, errorMsg=%s)", nextPC, ctx.Failed, ctx.ErrorMsg)
	}
	if ctx.Failed {
		t.Errorf("expected no failure after retry, got Failed=true")
	}
	if callCount.Load() < 2 {
		t.Errorf("expected at least 2 calls (retry), got %d", callCount.Load())
	}

	var gotVec []float64
	if err := json.Unmarshal(ctx.ByteSlots[1], &gotVec); err != nil {
		t.Fatalf("result slot not valid JSON: %v", err)
	}
	if len(gotVec) != len(wantVec) {
		t.Fatalf("expected vector length %d, got %d", len(wantVec), len(gotVec))
	}
}

func TestEmbedText_DimSlotNegative_Skipped(t *testing.T) {
	wantVec := []float64{0.1, 0.2, 0.3}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(openAIEmbedResponse(wantVec))
	}))
	defer srv.Close()

	ctx := makeEmbedCtx()
	ctx.ByteSlots[0] = []byte("dim slot test")

	// Record initial IntSlots values to ensure they don't change
	initialInts := make([]int64, len(ctx.IntSlots))
	copy(initialInts, ctx.IntSlots)

	cfg := EmbedTextConfig{
		InputSlot:  0,
		ResultSlot: 1,
		DimSlot:    -1, // Skip dim write
		Provider:   EmbedProviderOpenAI,
		Model:      "text-embedding-3-small",
		BaseURL:    srv.URL,
		APIKey:     "sk-test",
		APIKeySlot: -1,
		TimeoutMs:  5000,
		MaxRetries: 0,
	}

	state := makeEmbedState()
	instr := EmbedText(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != state.PC+1 {
		t.Fatalf("expected PC+1, got %d (failed=%v, errorMsg=%s)", nextPC, ctx.Failed, ctx.ErrorMsg)
	}
	if ctx.Failed {
		t.Errorf("expected no failure, got Failed=true")
	}

	// Verify no IntSlots were modified
	for i, v := range ctx.IntSlots {
		if v != initialInts[i] {
			t.Errorf("IntSlots[%d] changed from %d to %d (DimSlot=-1 should skip)", i, initialInts[i], v)
		}
	}

	// Result slot should still have the vector
	var gotVec []float64
	if err := json.Unmarshal(ctx.ByteSlots[1], &gotVec); err != nil {
		t.Fatalf("result slot not valid JSON: %v", err)
	}
	if len(gotVec) != len(wantVec) {
		t.Fatalf("expected vector length %d, got %d", len(wantVec), len(gotVec))
	}
}

func TestEmbedText_BadProvider_ReturnsStopPlan(t *testing.T) {
	ctx := makeEmbedCtx()
	ctx.ByteSlots[0] = []byte("test")

	cfg := EmbedTextConfig{
		InputSlot:  0,
		ResultSlot: 1,
		DimSlot:    -1,
		Provider:   EmbedProvider("unknown_provider"),
		Model:      "some-model",
		APIKeySlot: -1,
	}

	state := makeEmbedState()
	instr := EmbedText(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != engine.StopPlan {
		t.Fatalf("expected StopPlan for bad provider, got %d", nextPC)
	}
	if !ctx.Failed {
		t.Error("expected Failed=true for bad provider")
	}
	if ctx.ResponseStatus != 500 {
		t.Errorf("expected ResponseStatus=500, got %d", ctx.ResponseStatus)
	}
}

// TestEmbedText_Gemini_EndpointFormat verifies the Gemini endpoint URL contains the model name.
func TestEmbedText_Gemini_EndpointFormat(t *testing.T) {
	model := "text-embedding-004"
	ep := embedEndpoint(EmbedProviderGemini, "", model)
	expected := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:embedContent", model)
	if ep != expected {
		t.Errorf("expected endpoint %q, got %q", expected, ep)
	}
}

// TestEmbedText_OpenAI_DefaultBaseURL verifies the OpenAI default base URL.
func TestEmbedText_OpenAI_DefaultBaseURL(t *testing.T) {
	ep := embedEndpoint(EmbedProviderOpenAI, "", "text-embedding-3-small")
	expected := "https://api.openai.com/v1/embeddings"
	if ep != expected {
		t.Errorf("expected endpoint %q, got %q", expected, ep)
	}
}

// TestEmbedText_Ollama_DefaultBaseURL verifies the Ollama default base URL.
func TestEmbedText_Ollama_DefaultBaseURL(t *testing.T) {
	ep := embedEndpoint(EmbedProviderOllama, "", "nomic-embed-text")
	expected := "http://localhost:11434/api/embeddings"
	if ep != expected {
		t.Errorf("expected endpoint %q, got %q", expected, ep)
	}
}
