package steps

import (
	"encoding/json"
	"testing"
)

// ──────────────────────────────────────────────
// ParseToolCalls tests
// ──────────────────────────────────────────────

func TestParseToolCalls_EmptySlot_WritesEmptyArray(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// ByteSlots[0] is nil — empty response slot.
	cfg := ParseToolCallsConfig{
		ResponseSlot:     0,
		ToolCallsSlot:    1,
		CountSlot:        0,
		HasToolCallsSlot: 0,
	}
	instr := ParseToolCalls(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}
	if got := string(ctx.ByteSlots[1]); got != "[]" {
		t.Errorf("tool_calls slot: got %q, want %q", got, "[]")
	}
	if ctx.IntSlots[0] != 0 {
		t.Errorf("count slot: got %d, want 0", ctx.IntSlots[0])
	}
	if ctx.BoolSlots[0] != false {
		t.Errorf("has_tool_calls slot: got %v, want false", ctx.BoolSlots[0])
	}
}

func TestParseToolCalls_Anthropic_TextOnly_NoToolCalls(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// Anthropic content array with only text blocks.
	content := `[{"type":"text","text":"Hello, I am a helpful assistant."}]`
	ctx.ByteSlots[0] = []byte(content)

	cfg := ParseToolCallsConfig{
		ResponseSlot:     0,
		ToolCallsSlot:    1,
		CountSlot:        0,
		HasToolCallsSlot: 0,
		SourceFormat:     "anthropic",
	}
	instr := ParseToolCalls(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}
	if got := string(ctx.ByteSlots[1]); got != "[]" {
		t.Errorf("tool_calls slot: got %q, want %q", got, "[]")
	}
	// ResponseSlot should be updated to the text content only.
	if got := string(ctx.ByteSlots[0]); got != "Hello, I am a helpful assistant." {
		t.Errorf("response slot (text): got %q, want %q", got, "Hello, I am a helpful assistant.")
	}
	if ctx.IntSlots[0] != 0 {
		t.Errorf("count: got %d, want 0", ctx.IntSlots[0])
	}
	if ctx.BoolSlots[0] != false {
		t.Errorf("has_tool_calls: got %v, want false", ctx.BoolSlots[0])
	}
}

func TestParseToolCalls_Anthropic_WithToolUse_ExtractsToolCall(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	content := `[
		{"type":"text","text":"Sure, let me check the weather."},
		{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{"city":"NYC"}}
	]`
	ctx.ByteSlots[0] = []byte(content)

	cfg := ParseToolCallsConfig{
		ResponseSlot:     0,
		ToolCallsSlot:    1,
		CountSlot:        0,
		HasToolCallsSlot: 0,
		SourceFormat:     "anthropic",
	}
	instr := ParseToolCalls(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}

	// ResponseSlot should now contain only the text.
	if got := string(ctx.ByteSlots[0]); got != "Sure, let me check the weather." {
		t.Errorf("response slot (text only): got %q, want %q", got, "Sure, let me check the weather.")
	}

	// Decode and verify tool calls.
	var calls []CanonicalToolCall
	if err := json.Unmarshal(ctx.ByteSlots[1], &calls); err != nil {
		t.Fatalf("failed to decode tool calls: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "toolu_01" {
		t.Errorf("tool call ID: got %q, want %q", calls[0].ID, "toolu_01")
	}
	if calls[0].Name != "get_weather" {
		t.Errorf("tool call name: got %q, want %q", calls[0].Name, "get_weather")
	}
	if string(calls[0].InputJSON) != `{"city":"NYC"}` {
		t.Errorf("tool call input: got %q, want %q", string(calls[0].InputJSON), `{"city":"NYC"}`)
	}

	if ctx.IntSlots[0] != 1 {
		t.Errorf("count: got %d, want 1", ctx.IntSlots[0])
	}
	if ctx.BoolSlots[0] != true {
		t.Errorf("has_tool_calls: got %v, want true", ctx.BoolSlots[0])
	}
}

func TestParseToolCalls_Anthropic_MultipleToolCalls(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	content := `[
		{"type":"text","text":"Calling two tools."},
		{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{"city":"NYC"}},
		{"type":"tool_use","id":"toolu_02","name":"get_time","input":{"timezone":"UTC"}}
	]`
	ctx.ByteSlots[0] = []byte(content)

	cfg := ParseToolCallsConfig{
		ResponseSlot:     0,
		ToolCallsSlot:    1,
		CountSlot:        0,
		HasToolCallsSlot: 0,
		SourceFormat:     "anthropic",
	}
	instr := ParseToolCalls(cfg)
	instr.Action(ctx, state)

	var calls []CanonicalToolCall
	if err := json.Unmarshal(ctx.ByteSlots[1], &calls); err != nil {
		t.Fatalf("failed to decode tool calls: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].ID != "toolu_01" || calls[0].Name != "get_weather" {
		t.Errorf("first tool call: got id=%q name=%q, want id=toolu_01 name=get_weather", calls[0].ID, calls[0].Name)
	}
	if calls[1].ID != "toolu_02" || calls[1].Name != "get_time" {
		t.Errorf("second tool call: got id=%q name=%q, want id=toolu_02 name=get_time", calls[1].ID, calls[1].Name)
	}
	if ctx.IntSlots[0] != 2 {
		t.Errorf("count: got %d, want 2", ctx.IntSlots[0])
	}
}

func TestParseToolCalls_OpenAI_WithToolCalls_Extracts(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// OpenAI response object with tool_calls field.
	response := `{
		"tool_calls": [
			{
				"id": "call_abc123",
				"type": "function",
				"function": {
					"name": "get_weather",
					"arguments": "{\"location\":\"San Francisco\"}"
				}
			}
		]
	}`
	ctx.ByteSlots[0] = []byte(response)

	cfg := ParseToolCallsConfig{
		ResponseSlot:     0,
		ToolCallsSlot:    1,
		CountSlot:        0,
		HasToolCallsSlot: 0,
		SourceFormat:     "openai",
	}
	instr := ParseToolCalls(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}

	var calls []CanonicalToolCall
	if err := json.Unmarshal(ctx.ByteSlots[1], &calls); err != nil {
		t.Fatalf("failed to decode tool calls: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "call_abc123" {
		t.Errorf("tool call ID: got %q, want call_abc123", calls[0].ID)
	}
	if calls[0].Name != "get_weather" {
		t.Errorf("tool call name: got %q, want get_weather", calls[0].Name)
	}
	// InputJSON should be the raw arguments object.
	var input map[string]string
	if err := json.Unmarshal(calls[0].InputJSON, &input); err != nil {
		t.Fatalf("failed to decode input JSON: %v", err)
	}
	if input["location"] != "San Francisco" {
		t.Errorf("input[location]: got %q, want San Francisco", input["location"])
	}

	if ctx.IntSlots[0] != 1 {
		t.Errorf("count: got %d, want 1", ctx.IntSlots[0])
	}
	if ctx.BoolSlots[0] != true {
		t.Errorf("has_tool_calls: got %v, want true", ctx.BoolSlots[0])
	}
}

func TestParseToolCalls_CountSlotNegative_Skipped(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	content := `[{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{"city":"NYC"}}]`
	ctx.ByteSlots[0] = []byte(content)

	// CountSlot = -1 and HasToolCallsSlot = -1 — must not panic or write.
	cfg := ParseToolCallsConfig{
		ResponseSlot:     0,
		ToolCallsSlot:    1,
		CountSlot:        -1,
		HasToolCallsSlot: -1,
		SourceFormat:     "anthropic",
	}

	// Should not panic.
	instr := ParseToolCalls(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}

	var calls []CanonicalToolCall
	if err := json.Unmarshal(ctx.ByteSlots[1], &calls); err != nil {
		t.Fatalf("tool calls slot should still be written: %v", err)
	}
	if len(calls) != 1 {
		t.Errorf("expected 1 tool call extracted, got %d", len(calls))
	}
}

func TestParseToolCalls_PlainText_NoToolCalls(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// Plain string content — no JSON structure, no tool calls.
	ctx.ByteSlots[0] = []byte("Hello! How can I help you today?")

	cfg := ParseToolCallsConfig{
		ResponseSlot:     0,
		ToolCallsSlot:    1,
		CountSlot:        0,
		HasToolCallsSlot: 0,
	}
	instr := ParseToolCalls(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}
	if got := string(ctx.ByteSlots[1]); got != "[]" {
		t.Errorf("tool_calls slot: got %q, want %q", got, "[]")
	}
	if ctx.IntSlots[0] != 0 {
		t.Errorf("count: got %d, want 0", ctx.IntSlots[0])
	}
	if ctx.BoolSlots[0] != false {
		t.Errorf("has_tool_calls: got %v, want false", ctx.BoolSlots[0])
	}
}

func TestParseToolCalls_AlwaysReturnsPC1(t *testing.T) {
	cases := []struct {
		name    string
		content string
		format  string
	}{
		{"empty", "", ""},
		{"plain_text", "just text", ""},
		{"anthropic_text_only", `[{"type":"text","text":"hi"}]`, "anthropic"},
		{"anthropic_with_tool", `[{"type":"tool_use","id":"x","name":"fn","input":{}}]`, "anthropic"},
		{"openai_no_tools", `{"content":"hello"}`, "openai"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestCtx()
			state := newTestState()
			ctx.ByteSlots[0] = []byte(tc.content)

			cfg := ParseToolCallsConfig{
				ResponseSlot:     0,
				ToolCallsSlot:    1,
				CountSlot:        0,
				HasToolCallsSlot: 0,
				SourceFormat:     tc.format,
			}
			instr := ParseToolCalls(cfg)
			next := instr.Action(ctx, state)
			if next != 1 {
				t.Errorf("expected PC+1=1, got %d", next)
			}
		})
	}
}

// ──────────────────────────────────────────────
// AppendToolResult tests
// ──────────────────────────────────────────────

func TestAppendToolResult_Anthropic_AppendsCorrectly(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// Seed history with an existing assistant message.
	existing := []CanonicalMessage{
		{Role: RoleUser, Content: "What's the weather?"},
		{Role: RoleAssistant, Content: "[tool_use]"},
	}
	historyJSON, _ := json.Marshal(existing)
	ctx.ByteSlots[0] = historyJSON
	ctx.ByteSlots[1] = []byte("toolu_01")
	ctx.ByteSlots[2] = []byte(`{"temperature":72,"unit":"F"}`)

	cfg := AppendToolResultConfig{
		HistorySlot:    0,
		ToolCallIDSlot: 1,
		ResultSlot:     2,
		TargetFormat:   "anthropic",
	}
	instr := AppendToolResult(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}

	var msgs []CanonicalMessage
	if err := json.Unmarshal(ctx.ByteSlots[0], &msgs); err != nil {
		t.Fatalf("failed to decode history: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	last := msgs[2]
	if last.Role != RoleToolResult {
		t.Errorf("role: got %q, want %q", last.Role, RoleToolResult)
	}
	if last.ToolCallID != "toolu_01" {
		t.Errorf("tool_call_id: got %q, want toolu_01", last.ToolCallID)
	}
	// Content should encode a tool_result block JSON array.
	var blocks []map[string]string
	if err := json.Unmarshal([]byte(last.Content), &blocks); err != nil {
		t.Fatalf("content is not valid JSON array: %v (content=%q)", err, last.Content)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(blocks))
	}
	if blocks[0]["type"] != "tool_result" {
		t.Errorf("block type: got %q, want tool_result", blocks[0]["type"])
	}
	if blocks[0]["tool_use_id"] != "toolu_01" {
		t.Errorf("tool_use_id: got %q, want toolu_01", blocks[0]["tool_use_id"])
	}
	if blocks[0]["content"] != `{"temperature":72,"unit":"F"}` {
		t.Errorf("block content: got %q, want the result string", blocks[0]["content"])
	}
}

func TestAppendToolResult_OpenAI_AppendsCorrectly(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	existing := []CanonicalMessage{
		{Role: RoleUser, Content: "What is 2+2?"},
	}
	historyJSON, _ := json.Marshal(existing)
	ctx.ByteSlots[0] = historyJSON
	ctx.ByteSlots[1] = []byte("call_abc123")
	ctx.ByteSlots[2] = []byte("4")

	cfg := AppendToolResultConfig{
		HistorySlot:    0,
		ToolCallIDSlot: 1,
		ResultSlot:     2,
		TargetFormat:   "openai",
	}
	instr := AppendToolResult(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}

	var msgs []CanonicalMessage
	if err := json.Unmarshal(ctx.ByteSlots[0], &msgs); err != nil {
		t.Fatalf("failed to decode history: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	last := msgs[1]
	if last.Role != "tool" {
		t.Errorf("role: got %q, want tool", last.Role)
	}
	if last.ToolCallID != "call_abc123" {
		t.Errorf("tool_call_id: got %q, want call_abc123", last.ToolCallID)
	}
	if last.Content != "4" {
		t.Errorf("content: got %q, want 4", last.Content)
	}
}

func TestAppendToolResult_EmptyHistory_CreatesHistory(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	// History slot is empty — should create fresh history with one message.
	ctx.ByteSlots[1] = []byte("toolu_01")
	ctx.ByteSlots[2] = []byte("42 degrees")

	cfg := AppendToolResultConfig{
		HistorySlot:    0,
		ToolCallIDSlot: 1,
		ResultSlot:     2,
		TargetFormat:   "anthropic",
	}
	instr := AppendToolResult(cfg)
	next := instr.Action(ctx, state)

	if next != 1 {
		t.Fatalf("expected PC+1=1, got %d", next)
	}

	var msgs []CanonicalMessage
	if err := json.Unmarshal(ctx.ByteSlots[0], &msgs); err != nil {
		t.Fatalf("failed to decode history: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != RoleToolResult {
		t.Errorf("role: got %q, want %q", msgs[0].Role, RoleToolResult)
	}
}

func TestAppendToolResult_PreservesExistingHistory(t *testing.T) {
	ctx := newTestCtx()
	state := newTestState()

	existing := []CanonicalMessage{
		{Role: RoleUser, Content: "msg1"},
		{Role: RoleAssistant, Content: "msg2"},
		{Role: RoleUser, Content: "msg3"},
	}
	historyJSON, _ := json.Marshal(existing)
	ctx.ByteSlots[0] = historyJSON
	ctx.ByteSlots[1] = []byte("call_xyz")
	ctx.ByteSlots[2] = []byte("result_data")

	cfg := AppendToolResultConfig{
		HistorySlot:    0,
		ToolCallIDSlot: 1,
		ResultSlot:     2,
		TargetFormat:   "openai",
	}
	instr := AppendToolResult(cfg)
	instr.Action(ctx, state)

	var msgs []CanonicalMessage
	if err := json.Unmarshal(ctx.ByteSlots[0], &msgs); err != nil {
		t.Fatalf("failed to decode history: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages (3 existing + 1 new), got %d", len(msgs))
	}
	// Verify existing messages are preserved.
	if msgs[0].Content != "msg1" || msgs[1].Content != "msg2" || msgs[2].Content != "msg3" {
		t.Errorf("existing messages were modified: %+v", msgs[:3])
	}
	// Verify appended message.
	if msgs[3].Content != "result_data" {
		t.Errorf("appended message content: got %q, want result_data", msgs[3].Content)
	}
}
