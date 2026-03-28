package steps

import (
	"encoding/json"
	"testing"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func mustEncodeHistory(t *testing.T, msgs []CanonicalMessage) []byte {
	t.Helper()
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("encodeHistory: %v", err)
	}
	return b
}

func mustDecodeHistoryT(t *testing.T, raw []byte) []CanonicalMessage {
	t.Helper()
	msgs := decodeHistory(raw)
	return msgs
}

func runTransform(t *testing.T, cfg TransformMessagesConfig, msgs []CanonicalMessage) (*rctx.Context, []CanonicalMessage) {
	t.Helper()
	ctx := newTestCtx()
	state := &engine.ExecutionState{PC: 0}
	ctx.ByteSlots[cfg.HistorySlot] = mustEncodeHistory(t, msgs)
	instr := TransformMessages(cfg)
	next := instr.Action(ctx, state)
	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}
	result := mustDecodeHistoryT(t, ctx.ByteSlots[cfg.HistorySlot])
	return ctx, result
}

// ─── StripThinking ────────────────────────────────────────────────────────────

func TestTransformMessages_StripThinking_RemovesThinkingBlocksKeepsText(t *testing.T) {
	content := `[{"type":"thinking","thinking":"internal reasoning"},{"type":"text","text":"Hello world"}]`
	msgs := []CanonicalMessage{
		{Role: RoleUser, Content: "What is 2+2?"},
		{Role: RoleAssistant, Content: content},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    -1,
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		TargetFormat:  "openai",
		StripThinking: true,
	}
	_, result := runTransform(t, cfg, msgs)

	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[1].Role != RoleAssistant {
		t.Errorf("expected assistant role, got %s", result[1].Role)
	}
	// Single text block should be collapsed to plain string
	if result[1].Content != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", result[1].Content)
	}
}

func TestTransformMessages_StripThinking_MultipleTextBlocks(t *testing.T) {
	content := `[{"type":"thinking","thinking":"reasoning"},{"type":"text","text":"part1"},{"type":"text","text":"part2"}]`
	msgs := []CanonicalMessage{
		{Role: RoleAssistant, Content: content},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    -1,
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		TargetFormat:  "openai",
		StripThinking: true,
	}
	_, result := runTransform(t, cfg, msgs)

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	// Multiple text blocks → re-serialized as array
	var blocks []contentBlock
	if err := json.Unmarshal([]byte(result[0].Content), &blocks); err != nil {
		t.Fatalf("expected JSON array content, got %q: %v", result[0].Content, err)
	}
	if len(blocks) != 2 {
		t.Errorf("expected 2 text blocks, got %d", len(blocks))
	}
}

func TestTransformMessages_StripThinking_PlainStringPassthrough(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: RoleAssistant, Content: "plain string response"},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    -1,
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		TargetFormat:  "openai",
		StripThinking: true,
	}
	_, result := runTransform(t, cfg, msgs)

	if result[0].Content != "plain string response" {
		t.Errorf("expected plain string unchanged, got %q", result[0].Content)
	}
}

// ─── ExtractThinking ──────────────────────────────────────────────────────────

func TestTransformMessages_ExtractThinking_MovesThinkingToSlot(t *testing.T) {
	content := `[{"type":"thinking","thinking":"my internal thoughts"},{"type":"text","text":"final answer"}]`
	msgs := []CanonicalMessage{
		{Role: RoleAssistant, Content: content},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:     0,
		SystemSlot:      -1,
		ThinkingSlot:    1,
		FormatSlot:      -1,
		TargetFormat:    "openai",
		ExtractThinking: true,
	}
	ctx, result := runTransform(t, cfg, msgs)

	// Thinking should be in slot 1
	if string(ctx.ByteSlots[1]) != "my internal thoughts" {
		t.Errorf("expected thinking in slot 1, got %q", string(ctx.ByteSlots[1]))
	}
	// Message content should be just the text
	if result[0].Content != "final answer" {
		t.Errorf("expected 'final answer', got %q", result[0].Content)
	}
}

func TestTransformMessages_ExtractThinking_MultipleThinkingBlocks(t *testing.T) {
	content := `[{"type":"thinking","thinking":"thought1"},{"type":"text","text":"answer"},{"type":"thinking","thinking":"thought2"}]`
	msgs := []CanonicalMessage{
		{Role: RoleAssistant, Content: content},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:     0,
		SystemSlot:      -1,
		ThinkingSlot:    1,
		FormatSlot:      -1,
		TargetFormat:    "openai",
		ExtractThinking: true,
	}
	ctx, _ := runTransform(t, cfg, msgs)

	thinking := string(ctx.ByteSlots[1])
	if thinking != "thought1\n---\nthought2" {
		t.Errorf("expected concatenated thinking, got %q", thinking)
	}
}

// ─── ExtractSystem ────────────────────────────────────────────────────────────

func TestTransformMessages_ExtractSystem_PullsSystemMessagesOut(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: RoleSystem, Content: "You are a helpful assistant."},
		{Role: RoleUser, Content: "Hello"},
		{Role: RoleAssistant, Content: "Hi!"},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    1,
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		TargetFormat:  "anthropic",
		ExtractSystem: true,
	}
	ctx, result := runTransform(t, cfg, msgs)

	// System slot should contain the extracted system prompt
	if string(ctx.ByteSlots[1]) != "You are a helpful assistant." {
		t.Errorf("expected system prompt in slot 1, got %q", string(ctx.ByteSlots[1]))
	}
	// History should have 2 messages (system removed)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages after extract, got %d", len(result))
	}
	if result[0].Role != RoleUser {
		t.Errorf("expected first message to be user, got %s", result[0].Role)
	}
}

func TestTransformMessages_ExtractSystem_MultipleSystemMessages(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: RoleSystem, Content: "Part 1"},
		{Role: RoleUser, Content: "Hello"},
		{Role: RoleSystem, Content: "Part 2"},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    1,
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		TargetFormat:  "anthropic",
		ExtractSystem: true,
	}
	ctx, result := runTransform(t, cfg, msgs)

	if string(ctx.ByteSlots[1]) != "Part 1\nPart 2" {
		t.Errorf("expected joined system content, got %q", string(ctx.ByteSlots[1]))
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 message remaining, got %d", len(result))
	}
}

// ─── InjectSystem ─────────────────────────────────────────────────────────────

func TestTransformMessages_InjectSystem_PrependSystemMessage(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: RoleUser, Content: "Hello"},
	}

	ctx := newTestCtx()
	state := &engine.ExecutionState{PC: 0}
	ctx.ByteSlots[0] = mustEncodeHistory(t, msgs)
	ctx.ByteSlots[1] = []byte("Be helpful.")

	cfg := TransformMessagesConfig{
		HistorySlot:  0,
		SystemSlot:   1,
		ThinkingSlot: -1,
		FormatSlot:   -1,
		TargetFormat: "openai",
		InjectSystem: true,
	}
	instr := TransformMessages(cfg)
	instr.Action(ctx, state)

	result := mustDecodeHistoryT(t, ctx.ByteSlots[0])
	if len(result) != 2 {
		t.Fatalf("expected 2 messages after inject, got %d", len(result))
	}
	if result[0].Role != RoleSystem {
		t.Errorf("expected first message to be system, got %s", result[0].Role)
	}
	if result[0].Content != "Be helpful." {
		t.Errorf("expected system content 'Be helpful.', got %q", result[0].Content)
	}
}

func TestTransformMessages_InjectSystem_EmptySystemSlot_NoOp(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: RoleUser, Content: "Hello"},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:  0,
		SystemSlot:   1,
		ThinkingSlot: -1,
		FormatSlot:   -1,
		TargetFormat: "openai",
		InjectSystem: true,
	}
	_, result := runTransform(t, cfg, msgs)

	// System slot is empty, so no injection
	if len(result) != 1 {
		t.Fatalf("expected 1 message (no injection with empty slot), got %d", len(result))
	}
}

// ─── FlattenContent ──────────────────────────────────────────────────────────

func TestTransformMessages_FlattenContent_CollapsesContentArray(t *testing.T) {
	content := `[{"type":"text","text":"Hello"},{"type":"text","text":" world"}]`
	msgs := []CanonicalMessage{
		{Role: RoleAssistant, Content: content},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:    0,
		SystemSlot:     -1,
		ThinkingSlot:   -1,
		FormatSlot:     -1,
		TargetFormat:   "openai",
		FlattenContent: true,
	}
	_, result := runTransform(t, cfg, msgs)

	if result[0].Content != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", result[0].Content)
	}
}

func TestTransformMessages_FlattenContent_PlainStringUnchanged(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: RoleUser, Content: "plain string"},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:    0,
		SystemSlot:     -1,
		ThinkingSlot:   -1,
		FormatSlot:     -1,
		TargetFormat:   "openai",
		FlattenContent: true,
	}
	_, result := runTransform(t, cfg, msgs)

	if result[0].Content != "plain string" {
		t.Errorf("expected 'plain string', got %q", result[0].Content)
	}
}

// ─── AdaptRoles ──────────────────────────────────────────────────────────────

func TestTransformMessages_AdaptRoles_ModelToAssistantForNonGemini(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: "model", Content: "Gemini response"},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:  0,
		SystemSlot:   -1,
		ThinkingSlot: -1,
		FormatSlot:   -1,
		TargetFormat: "openai",
		AdaptRoles:   true,
	}
	_, result := runTransform(t, cfg, msgs)

	if result[0].Role != RoleAssistant {
		t.Errorf("expected 'assistant' role for openai target, got %s", result[0].Role)
	}
}

func TestTransformMessages_AdaptRoles_AssistantToModelForGemini(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: RoleAssistant, Content: "response"},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:  0,
		SystemSlot:   -1,
		ThinkingSlot: -1,
		FormatSlot:   -1,
		TargetFormat: "gemini",
		AdaptRoles:   true,
	}
	_, result := runTransform(t, cfg, msgs)

	if result[0].Role != "model" {
		t.Errorf("expected 'model' role for gemini target, got %s", result[0].Role)
	}
}

func TestTransformMessages_AdaptRoles_FunctionToTool(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: "function", Content: "tool result"},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:  0,
		SystemSlot:   -1,
		ThinkingSlot: -1,
		FormatSlot:   -1,
		TargetFormat: "openai",
		AdaptRoles:   true,
	}
	_, result := runTransform(t, cfg, msgs)

	if result[0].Role != "tool" {
		t.Errorf("expected 'tool' role, got %s", result[0].Role)
	}
}

// ─── FormatSlot override ─────────────────────────────────────────────────────

func TestTransformMessages_FormatSlot_OverridesSourceFormat(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: "model", Content: "response"},
	}

	ctx := newTestCtx()
	state := &engine.ExecutionState{PC: 0}
	ctx.ByteSlots[0] = mustEncodeHistory(t, msgs)
	// Set format slot to "gemini" (source format from a previous parse step)
	ctx.ByteSlots[2] = []byte("gemini")

	cfg := TransformMessagesConfig{
		HistorySlot:  0,
		SystemSlot:   -1,
		ThinkingSlot: -1,
		FormatSlot:   2, // read source format from slot 2
		TargetFormat: "openai",
		AdaptRoles:   true,
	}
	instr := TransformMessages(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}

	result := mustDecodeHistoryT(t, ctx.ByteSlots[0])
	// "model" → "assistant" for openai target (adapt_roles applies based on targetFormat)
	if result[0].Role != RoleAssistant {
		t.Errorf("expected 'assistant' role (FormatSlot didn't block AdaptRoles), got %s", result[0].Role)
	}
}

// ─── Empty history ────────────────────────────────────────────────────────────

func TestTransformMessages_EmptyHistory_NoOp_ReturnsPC1(t *testing.T) {
	ctx := newTestCtx()
	state := &engine.ExecutionState{PC: 0}
	// Empty/nil history slot
	ctx.ByteSlots[0] = nil

	cfg := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    -1,
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		TargetFormat:  "openai",
		StripThinking: true,
		AdaptRoles:    true,
	}
	instr := TransformMessages(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1 on empty history, got %d", next)
	}
}

func TestTransformMessages_EmptyJSONArray_NoOp(t *testing.T) {
	ctx := newTestCtx()
	state := &engine.ExecutionState{PC: 0}
	ctx.ByteSlots[0] = []byte("[]")

	cfg := TransformMessagesConfig{
		HistorySlot:  0,
		SystemSlot:   -1,
		ThinkingSlot: -1,
		FormatSlot:   -1,
		TargetFormat: "openai",
		AdaptRoles:   true,
	}
	instr := TransformMessages(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1 on empty array, got %d", next)
	}
}

// ─── Composition: multiple transforms in sequence ────────────────────────────

func TestTransformMessages_ComposedTransforms_AppliedInOrder(t *testing.T) {
	// Simulate an Anthropic → OpenAI conversion:
	// 1. ExtractSystem pulls system message out
	// 2. StripThinking removes thinking blocks from assistant
	// 3. AdaptRoles: no "model" roles here (already "assistant")
	// 4. InjectSystem prepends system back as message (for OpenAI)
	thinkingContent := `[{"type":"thinking","thinking":"let me think"},{"type":"text","text":"The answer is 42"}]`
	msgs := []CanonicalMessage{
		{Role: RoleSystem, Content: "You are helpful."},
		{Role: RoleUser, Content: "What is the answer?"},
		{Role: RoleAssistant, Content: thinkingContent},
	}

	ctx := newTestCtx()
	state := &engine.ExecutionState{PC: 0}
	ctx.ByteSlots[0] = mustEncodeHistory(t, msgs)

	cfg := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    1, // system extracted here, then injected back
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		TargetFormat:  "openai",
		ExtractSystem: true,
		StripThinking: true,
		InjectSystem:  true,
	}
	instr := TransformMessages(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}

	result := mustDecodeHistoryT(t, ctx.ByteSlots[0])

	// After ExtractSystem: [user, assistant]
	// After StripThinking: assistant content = "The answer is 42"
	// After InjectSystem: [system, user, assistant]
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(result), result)
	}
	if result[0].Role != RoleSystem {
		t.Errorf("expected system first, got %s", result[0].Role)
	}
	if result[0].Content != "You are helpful." {
		t.Errorf("expected system content 'You are helpful.', got %q", result[0].Content)
	}
	if result[2].Role != RoleAssistant {
		t.Errorf("expected assistant last, got %s", result[2].Role)
	}
	if result[2].Content != "The answer is 42" {
		t.Errorf("expected stripped content 'The answer is 42', got %q", result[2].Content)
	}
}

// ─── MalformedJSON ────────────────────────────────────────────────────────────

func TestTransformMessages_MalformedContentJSON_TreatedAsPlainString(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: RoleAssistant, Content: "[not valid json"},
	}

	cfg := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    -1,
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		TargetFormat:  "openai",
		StripThinking: true,
		FlattenContent: true,
	}
	_, result := runTransform(t, cfg, msgs)

	// Should not panic; content should remain unchanged
	if result[0].Content != "[not valid json" {
		t.Errorf("expected malformed content unchanged, got %q", result[0].Content)
	}
}
