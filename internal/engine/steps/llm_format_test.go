package steps

import (
	"encoding/json"
	"testing"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// newTestCtx returns a minimal Context suitable for unit tests.
func newTestCtx() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.InitSlots()
	ctx.ResponseStatus = 200
	return ctx
}

// newTestState returns an ExecutionState with PC=0.
func newTestState() *engine.ExecutionState {
	return &engine.ExecutionState{PC: 0}
}

// ──────────────────────────────────────────────
// ParseMessageFormat tests
// ──────────────────────────────────────────────

func TestParseMessageFormat_Anthropic(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	body := `{
		"system": "You are a helpful assistant.",
		"messages": [
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi there!"},
			{"role": "user", "content": "How are you?"}
		]
	}`

	ctx.ByteSlots[0] = []byte(body)

	cfg := ParseMessageFormatConfig{
		BodySlot:        0,
		MessagesSlot:    1,
		SystemSlot:      2,
		DetectedFmtSlot: 3,
	}
	instr := ParseMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected next PC=1, got %d", next)
	}
	if ctx.Failed {
		t.Fatalf("unexpected failure: %s", ctx.ErrorMsg)
	}

	// Check system slot.
	if string(ctx.ByteSlots[2]) != "You are a helpful assistant." {
		t.Errorf("system slot: got %q, want %q", ctx.ByteSlots[2], "You are a helpful assistant.")
	}

	// Decode messages slot.
	var msgs []CanonicalMessage
	if err := json.Unmarshal(ctx.ByteSlots[1], &msgs); err != nil {
		t.Fatalf("messages slot not valid JSON: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Role != RoleUser || msgs[0].Content != "Hello" {
		t.Errorf("msg[0]: got {%s, %s}", msgs[0].Role, msgs[0].Content)
	}
	if msgs[1].Role != RoleAssistant || msgs[1].Content != "Hi there!" {
		t.Errorf("msg[1]: got {%s, %s}", msgs[1].Role, msgs[1].Content)
	}
	if msgs[2].Role != RoleUser || msgs[2].Content != "How are you?" {
		t.Errorf("msg[2]: got {%s, %s}", msgs[2].Role, msgs[2].Content)
	}
}

func TestParseMessageFormat_OpenAI(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	body := `{
		"messages": [
			{"role": "system", "content": "Be concise."},
			{"role": "user", "content": "What is 2+2?"},
			{"role": "assistant", "content": "4"}
		]
	}`

	ctx.ByteSlots[0] = []byte(body)

	cfg := ParseMessageFormatConfig{
		BodySlot:        0,
		MessagesSlot:    1,
		SystemSlot:      2,
		DetectedFmtSlot: 3,
	}
	instr := ParseMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected next PC=1, got %d", next)
	}
	if ctx.Failed {
		t.Fatalf("unexpected failure: %s", ctx.ErrorMsg)
	}

	// System should be extracted from the inline system message.
	if string(ctx.ByteSlots[2]) != "Be concise." {
		t.Errorf("system slot: got %q, want %q", ctx.ByteSlots[2], "Be concise.")
	}

	var msgs []CanonicalMessage
	if err := json.Unmarshal(ctx.ByteSlots[1], &msgs); err != nil {
		t.Fatalf("messages slot not valid JSON: %v", err)
	}
	// system role message should NOT appear in canonical messages.
	if len(msgs) != 2 {
		t.Fatalf("expected 2 canonical messages (system excluded), got %d", len(msgs))
	}
	if msgs[0].Role != RoleUser {
		t.Errorf("msg[0] role: got %s, want user", string(msgs[0].Role))
	}
	if msgs[1].Role != RoleAssistant {
		t.Errorf("msg[1] role: got %s, want assistant", string(msgs[1].Role))
	}

	// Detected format.
	if string(ctx.ByteSlots[3]) != "openai" {
		t.Errorf("detected format: got %q, want %q", ctx.ByteSlots[3], "openai")
	}
}

func TestParseMessageFormat_Gemini(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	body := `{
		"systemInstruction": {"parts": [{"text": "Always respond in English."}]},
		"contents": [
			{"role": "user", "parts": [{"text": "Tell me a joke."}]},
			{"role": "model", "parts": [{"text": "Why did the chicken cross the road?"}]}
		]
	}`

	ctx.ByteSlots[0] = []byte(body)

	cfg := ParseMessageFormatConfig{
		BodySlot:        0,
		MessagesSlot:    1,
		SystemSlot:      2,
		DetectedFmtSlot: 3,
	}
	instr := ParseMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected next PC=1, got %d", next)
	}
	if ctx.Failed {
		t.Fatalf("unexpected failure: %s", ctx.ErrorMsg)
	}

	if string(ctx.ByteSlots[2]) != "Always respond in English." {
		t.Errorf("system slot: got %q, want %q", ctx.ByteSlots[2], "Always respond in English.")
	}

	var msgs []CanonicalMessage
	if err := json.Unmarshal(ctx.ByteSlots[1], &msgs); err != nil {
		t.Fatalf("messages slot not valid JSON: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != RoleUser {
		t.Errorf("msg[0] role: got %s, want user", msgs[0].Role)
	}
	if msgs[1].Role != RoleAssistant {
		t.Errorf("msg[1] role: got %s, want assistant (from Gemini 'model' role)", string(msgs[1].Role))
	}

	// Detected format.
	if string(ctx.ByteSlots[3]) != "gemini" {
		t.Errorf("detected format: got %q, want %q", ctx.ByteSlots[3], "gemini")
	}
}

func TestParseMessageFormat_DetectedFmtSlot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		expect string
	}{
		{
			name:   "anthropic",
			body:   `{"system":"sys","messages":[{"role":"user","content":"hi"}]}`,
			expect: "anthropic",
		},
		{
			name:   "openai",
			body:   `{"messages":[{"role":"user","content":"hi"}]}`,
			expect: "openai",
		},
		{
			name:   "gemini",
			body:   `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			expect: "gemini",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestCtx()
			state := newTestState()
			ctx.ByteSlots[0] = []byte(tc.body)

			cfg := ParseMessageFormatConfig{
				BodySlot:        0,
				MessagesSlot:    1,
				SystemSlot:      -1,
				DetectedFmtSlot: 2,
			}
			instr := ParseMessageFormat(cfg)
			instr.Action(ctx, state)

			if string(ctx.ByteSlots[2]) != tc.expect {
				t.Errorf("detected format: got %q, want %q", ctx.ByteSlots[2], tc.expect)
			}
		})
	}
}

func TestParseMessageFormat_EmptyBody_Skips(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// Leave ByteSlots[0] as nil (empty).
	cfg := ParseMessageFormatConfig{
		BodySlot:        0,
		MessagesSlot:    1,
		SystemSlot:      2,
		DetectedFmtSlot: 3,
	}
	instr := ParseMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Errorf("expected PC+1 on empty body, got %d", next)
	}
	if ctx.Failed {
		t.Error("should not fail on empty body")
	}
	// Output slots should remain nil.
	if ctx.ByteSlots[1] != nil {
		t.Errorf("messages slot should be nil on skip, got %v", ctx.ByteSlots[1])
	}
}

func TestParseMessageFormat_Unknown_Returns400(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	body := `{"query": "SELECT 1"}`
	ctx.ByteSlots[0] = []byte(body)

	cfg := ParseMessageFormatConfig{
		BodySlot:        0,
		MessagesSlot:    1,
		SystemSlot:      -1,
		DetectedFmtSlot: -1,
	}
	instr := ParseMessageFormat(cfg)
	next := instr.Action(ctx, state)

	if next != engine.StopPlan {
		t.Errorf("expected StopPlan for unknown format, got %d", next)
	}
	if !ctx.Failed {
		t.Error("expected ctx.Failed = true for unknown format")
	}
	if ctx.ResponseStatus != 400 {
		t.Errorf("expected ResponseStatus=400, got %d", ctx.ResponseStatus)
	}
}

// ──────────────────────────────────────────────
// FormatResponse tests
// ──────────────────────────────────────────────

func TestFormatResponse_Anthropic(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	ctx.ByteSlots[0] = []byte("Here is your answer.")

	cfg := FormatResponseConfig{
		ContentSlot:      0,
		ResultSlot:       1,
		Format:           "anthropic",
		Model:            "claude-3-haiku",
		StopReasonSlot:   -1,
		InputTokensSlot:  -1,
		OutputTokensSlot: -1,
		FormatSlot:       -1,
		ModelSlot:        -1,
	}
	instr := FormatResponse(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	if ctx.Failed {
		t.Fatalf("unexpected failure: %s", ctx.ErrorMsg)
	}

	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Role  string `json:"role"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(ctx.ByteSlots[1], &resp); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if len(resp.Content) == 0 {
		t.Fatal("expected at least one content block")
	}
	if resp.Content[0].Type != "text" {
		t.Errorf("content[0].type: got %q, want %q", resp.Content[0].Type, "text")
	}
	if resp.Content[0].Text != "Here is your answer." {
		t.Errorf("content[0].text: got %q, want %q", resp.Content[0].Text, "Here is your answer.")
	}
	if resp.Role != "assistant" {
		t.Errorf("role: got %q, want %q", resp.Role, "assistant")
	}
	if resp.Model != "claude-3-haiku" {
		t.Errorf("model: got %q, want %q", resp.Model, "claude-3-haiku")
	}
}

func TestFormatResponse_OpenAI(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	ctx.ByteSlots[0] = []byte("The answer is 42.")

	cfg := FormatResponseConfig{
		ContentSlot:      0,
		ResultSlot:       1,
		Format:           "openai",
		Model:            "gpt-4",
		StopReasonSlot:   -1,
		InputTokensSlot:  -1,
		OutputTokensSlot: -1,
		FormatSlot:       -1,
		ModelSlot:        -1,
	}
	instr := FormatResponse(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	if ctx.Failed {
		t.Fatalf("unexpected failure: %s", ctx.ErrorMsg)
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(ctx.ByteSlots[1], &resp); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("expected at least one choice")
	}
	if resp.Choices[0].Message.Content != "The answer is 42." {
		t.Errorf("choices[0].message.content: got %q, want %q", resp.Choices[0].Message.Content, "The answer is 42.")
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("choices[0].message.role: got %q, want %q", resp.Choices[0].Message.Role, "assistant")
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason: got %q, want %q", resp.Choices[0].FinishReason, "stop")
	}
	if resp.Model != "gpt-4" {
		t.Errorf("model: got %q, want %q", resp.Model, "gpt-4")
	}
}

func TestFormatResponse_Gemini(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	ctx.ByteSlots[0] = []byte("Gemini says hello.")

	cfg := FormatResponseConfig{
		ContentSlot:      0,
		ResultSlot:       1,
		Format:           "gemini",
		Model:            "gemini-pro",
		StopReasonSlot:   -1,
		InputTokensSlot:  -1,
		OutputTokensSlot: -1,
		FormatSlot:       -1,
		ModelSlot:        -1,
	}
	instr := FormatResponse(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1, got %d", next)
	}
	if ctx.Failed {
		t.Fatalf("unexpected failure: %s", ctx.ErrorMsg)
	}

	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
				Role string `json:"role"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(ctx.ByteSlots[1], &resp); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if len(resp.Candidates) == 0 {
		t.Fatal("expected at least one candidate")
	}
	if len(resp.Candidates[0].Content.Parts) == 0 {
		t.Fatal("expected at least one part in candidate content")
	}
	if resp.Candidates[0].Content.Parts[0].Text != "Gemini says hello." {
		t.Errorf("candidates[0].content.parts[0].text: got %q, want %q",
			resp.Candidates[0].Content.Parts[0].Text, "Gemini says hello.")
	}
	if resp.Candidates[0].Content.Role != "model" {
		t.Errorf("candidates[0].content.role: got %q, want %q", resp.Candidates[0].Content.Role, "model")
	}
	if resp.Candidates[0].FinishReason != "STOP" {
		t.Errorf("finishReason: got %q, want %q", resp.Candidates[0].FinishReason, "STOP")
	}
}
