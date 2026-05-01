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

// ─── Truncation: MaxMessages ──────────────────────────────────────────────────

func TestTransformTruncateByCount(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: RoleUser, Content: "turn 1"},
		{Role: RoleAssistant, Content: "reply 1"},
		{Role: RoleUser, Content: "turn 2"},
		{Role: RoleAssistant, Content: "reply 2"},
		{Role: RoleUser, Content: "turn 3"},
	}
	cfg := TransformMessagesConfig{
		HistorySlot:  0,
		SystemSlot:   -1,
		ThinkingSlot: -1,
		FormatSlot:   -1,
		MaxMessages:  3,
	}
	_, result := runTransform(t, cfg, msgs)
	if len(result) != 3 {
		t.Errorf("expected 3 messages after truncation, got %d", len(result))
	}
	// Should be the last 3 messages.
	if result[0].Content != "turn 2" {
		t.Errorf("expected first retained message to be 'turn 2', got %q", result[0].Content)
	}
}

// ─── Truncation: MaxTokens ────────────────────────────────────────────────────

func TestTransformTruncateByTokens(t *testing.T) {
	// Each message: content ~7 chars → estimateTokens = (7+3)/4 = 2, +4 overhead = 6 tokens.
	// 5 messages ≈ 30 tokens; budget of 15 → should drop oldest until under budget.
	msgs := []CanonicalMessage{
		{Role: RoleUser, Content: "turn 1a"},
		{Role: RoleAssistant, Content: "reply1a"},
		{Role: RoleUser, Content: "turn 2a"},
		{Role: RoleAssistant, Content: "reply2a"},
		{Role: RoleUser, Content: "turn 3a"},
	}
	cfg := TransformMessagesConfig{
		HistorySlot:  0,
		SystemSlot:   -1,
		ThinkingSlot: -1,
		FormatSlot:   -1,
		MaxTokens:    15,
	}
	_, result := runTransform(t, cfg, msgs)
	if len(result) >= len(msgs) {
		t.Errorf("expected fewer messages after token truncation, got %d (same as input %d)", len(result), len(msgs))
	}
}

// ─── Truncation: RemoveOrphans ────────────────────────────────────────────────

func TestTransformRemovesOrphanedToolResults(t *testing.T) {
	// Full history: user → assistant(tool_use tu_1) → user(tool_result tu_1) → assistant → user
	// Truncate to last 2: only [assistant("Done"), user("What next?")] — no tool_use/tool_result.
	msgs := []CanonicalMessage{
		{Role: RoleUser, Content: "Do something"},
		{Role: RoleAssistant, ContentBlocks: []ContentBlock{
			{Type: BlockToolUse, ToolUseID: "tu_1", ToolName: "bash", ToolInput: json.RawMessage(`{}`)},
		}},
		{Role: RoleUser, ContentBlocks: []ContentBlock{
			{Type: BlockToolResult, ToolCallID: "tu_1", ToolResult: "output"},
		}},
		{Role: RoleAssistant, Content: "Done"},
		{Role: RoleUser, Content: "What next?"},
	}
	// Truncate to last 2 → [assistant("Done"), user("What next?")] — no orphans expected
	cfg := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    -1,
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		MaxMessages:   2,
		RemoveOrphans: true,
	}
	_, result := runTransform(t, cfg, msgs)
	for _, m := range result {
		for _, b := range m.ContentBlocks {
			if b.Type == BlockToolResult {
				t.Errorf("unexpected tool_result block after truncation+orphan removal: %+v", m)
			}
		}
	}

	// Now truncate to last 3 → [user(tool_result tu_1), assistant("Done"), user("What next?")]
	// tu_1 tool_use is NOT in window → tool_result is orphaned → that message should be removed.
	cfg3 := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    -1,
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		MaxMessages:   3,
		RemoveOrphans: true,
	}
	_, result3 := runTransform(t, cfg3, msgs)
	for _, m := range result3 {
		for _, b := range m.ContentBlocks {
			if b.Type == BlockToolResult {
				t.Errorf("orphaned tool_result should have been removed, found in message: %+v", m)
			}
		}
	}
}

func TestTransformRetainsNonOrphanedToolResults(t *testing.T) {
	// Truncate to last 4 → tool_use tu_1 IS retained → tool_result tu_1 is NOT orphaned.
	msgs := []CanonicalMessage{
		{Role: RoleUser, Content: "Do something"},
		{Role: RoleAssistant, ContentBlocks: []ContentBlock{
			{Type: BlockToolUse, ToolUseID: "tu_1", ToolName: "bash", ToolInput: json.RawMessage(`{}`)},
		}},
		{Role: RoleUser, ContentBlocks: []ContentBlock{
			{Type: BlockToolResult, ToolCallID: "tu_1", ToolResult: "output"},
		}},
		{Role: RoleAssistant, Content: "Done"},
		{Role: RoleUser, Content: "What next?"},
	}
	cfg := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    -1,
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		MaxMessages:   4,
		RemoveOrphans: true,
	}
	_, result := runTransform(t, cfg, msgs)
	// After truncation to 4: [assistant(tool_use tu_1), user(tool_result tu_1), assistant("Done"), user("What next?")]
	// tool_use is present → tool_result is not orphaned → all 4 messages kept.
	if len(result) != 4 {
		t.Errorf("expected 4 messages when tool_use is retained, got %d", len(result))
	}
}

// ─── Truncation: EnsureStartUser ─────────────────────────────────────────────

func TestTransformEnsureStartUser(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: RoleAssistant, Content: "I was thinking..."},
		{Role: RoleUser, Content: "Hello"},
		{Role: RoleAssistant, Content: "Hi"},
	}
	cfg := TransformMessagesConfig{
		HistorySlot:     0,
		SystemSlot:      -1,
		ThinkingSlot:    -1,
		FormatSlot:      -1,
		MaxMessages:     3, // must set MaxMessages or MaxTokens to enable truncation block
		EnsureStartUser: true,
	}
	_, result := runTransform(t, cfg, msgs)
	if len(result) == 0 {
		t.Fatal("result should not be empty")
	}
	if result[0].Role != RoleUser {
		t.Errorf("first message should be user, got %s", result[0].Role)
	}
}

// ─── Truncation: combined StripThinking + MaxMessages ────────────────────────

func TestTransformTruncateAndStripThinking(t *testing.T) {
	msgs := []CanonicalMessage{
		{Role: RoleUser, Content: "Hello"},
		{Role: RoleAssistant, ContentBlocks: []ContentBlock{
			{Type: BlockThinking, Text: "I should say hi"},
			{Type: BlockText, Text: "Hi there!"},
		}},
		{Role: RoleUser, Content: "How are you?"},
	}
	cfg := TransformMessagesConfig{
		HistorySlot:   0,
		SystemSlot:    -1,
		ThinkingSlot:  -1,
		FormatSlot:    -1,
		StripThinking: true,
		MaxMessages:   2,
	}
	_, result := runTransform(t, cfg, msgs)
	if len(result) != 2 {
		t.Errorf("expected 2 messages after truncation, got %d", len(result))
	}
	for _, m := range result {
		for _, b := range m.ContentBlocks {
			if b.Type == BlockThinking {
				t.Error("thinking block should have been stripped before truncation check")
			}
		}
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
