package steps

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// newFitContext creates a test context with enough slots for context-fit tests.
func newFitContext() *rctx.Context {
	ctx := &rctx.Context{}
	ctx.ByteSlots = make([][]byte, 16)
	ctx.IntSlots = make([]int64, 8)
	ctx.BoolSlots = make([]bool, 8)
	return ctx
}

func runFitInstruction(instr engine.Instruction, ctx *rctx.Context) int16 {
	state := &engine.ExecutionState{PC: 0}
	return instr.Action(ctx, state)
}

// â”€â”€ Plain text fits â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestCheckContextFit_PlainText_Fits(t *testing.T) {
	ctx := newFitContext()
	ctx.ByteSlots[0] = []byte("Hello, how are you?") // ~5 tokens

	cfg := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       -1,
		HistorySlot:      -1,
		ToolsSlot:        -1,
		MaxContextTokens: 1000,
		MaxOutputTokens:  200,
		FitsSlot:         0,
		OverflowSlot:     0,
		TotalSlot:        1,
	}

	next := runFitInstruction(CheckContextFit(cfg), ctx)

	if next != 1 {
		t.Fatalf("want next PC=1, got %d", next)
	}
	if !ctx.BoolSlots[0] {
		t.Error("want fits=true, got false")
	}
	if ctx.IntSlots[0] != 0 {
		t.Errorf("want overflow=0, got %d", ctx.IntSlots[0])
	}
	if ctx.IntSlots[1] <= 0 {
		t.Errorf("want total > 0, got %d", ctx.IntSlots[1])
	}
}

// â”€â”€ Plain text exceeds limit â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestCheckContextFit_PlainText_Exceeds(t *testing.T) {
	ctx := newFitContext()
	// Build a long string â€” easily > 10 tokens worth
	ctx.ByteSlots[0] = []byte(strings.Repeat("word ", 200)) // 200 * 5 chars = 1000 chars â‰ˆ 250 tokens

	cfg := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       -1,
		HistorySlot:      -1,
		ToolsSlot:        -1,
		MaxContextTokens: 100, // very small budget
		MaxOutputTokens:  50,  // budget = 50 tokens
		FitsSlot:         0,
		OverflowSlot:     0,
		TotalSlot:        1,
	}

	runFitInstruction(CheckContextFit(cfg), ctx)

	if ctx.BoolSlots[0] {
		t.Error("want fits=false, got true")
	}
	if ctx.IntSlots[0] <= 0 {
		t.Errorf("want overflow > 0, got %d", ctx.IntSlots[0])
	}
	// overflow should equal total - budget
	budget := int64(cfg.MaxContextTokens - cfg.MaxOutputTokens)
	expectedOverflow := ctx.IntSlots[1] - budget
	if ctx.IntSlots[0] != expectedOverflow {
		t.Errorf("overflow mismatch: want %d, got %d (total=%d, budget=%d)",
			expectedOverflow, ctx.IntSlots[0], ctx.IntSlots[1], budget)
	}
}

// â”€â”€ HistorySlot included â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestCheckContextFit_HistorySlot_Included(t *testing.T) {
	ctx := newFitContext()
	ctx.ByteSlots[0] = []byte("Short prompt") // ~3 tokens

	history := []CanonicalMessage{
		{Role: RoleUser, Content: strings.Repeat("history word ", 100)},     // ~325 tokens each
		{Role: RoleAssistant, Content: strings.Repeat("assistant reply ", 100)},
	}
	histJSON, _ := json.Marshal(history)
	ctx.ByteSlots[1] = histJSON

	cfgNoHistory := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       -1,
		HistorySlot:      -1,
		ToolsSlot:        -1,
		MaxContextTokens: 10000,
		MaxOutputTokens:  0,
		FitsSlot:         0,
		OverflowSlot:     0,
		TotalSlot:        0,
	}
	runFitInstruction(CheckContextFit(cfgNoHistory), ctx)
	totalWithout := ctx.IntSlots[0]

	cfgWithHistory := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       -1,
		HistorySlot:      1,
		ToolsSlot:        -1,
		MaxContextTokens: 10000,
		MaxOutputTokens:  0,
		FitsSlot:         0,
		OverflowSlot:     0,
		TotalSlot:        0,
	}
	runFitInstruction(CheckContextFit(cfgWithHistory), ctx)
	totalWith := ctx.IntSlots[0]

	if totalWith <= totalWithout {
		t.Errorf("history should increase token count: without=%d, with=%d", totalWithout, totalWith)
	}
}

// â”€â”€ SystemSlot included â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestCheckContextFit_SystemSlot_Included(t *testing.T) {
	ctx := newFitContext()
	ctx.ByteSlots[0] = []byte("Hello") // tiny prompt

	cfgNoSystem := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       -1,
		HistorySlot:      -1,
		ToolsSlot:        -1,
		MaxContextTokens: 10000,
		MaxOutputTokens:  0,
		FitsSlot:         -1,
		OverflowSlot:     -1,
		TotalSlot:        0,
	}
	runFitInstruction(CheckContextFit(cfgNoSystem), ctx)
	totalWithout := ctx.IntSlots[0]

	ctx.ByteSlots[2] = []byte("You are a helpful assistant that answers only in pirate speak.")
	cfgWithSystem := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       2,
		HistorySlot:      -1,
		ToolsSlot:        -1,
		MaxContextTokens: 10000,
		MaxOutputTokens:  0,
		FitsSlot:         -1,
		OverflowSlot:     -1,
		TotalSlot:        0,
	}
	runFitInstruction(CheckContextFit(cfgWithSystem), ctx)
	totalWith := ctx.IntSlots[0]

	if totalWith <= totalWithout {
		t.Errorf("system prompt should increase token count: without=%d, with=%d", totalWithout, totalWith)
	}
}

// â”€â”€ MaxContextTokens=0 means no limit â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestCheckContextFit_NoLimit_AlwaysFits(t *testing.T) {
	ctx := newFitContext()
	ctx.ByteSlots[0] = []byte(strings.Repeat("enormous prompt content ", 5000)) // ~30k tokens

	cfg := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       -1,
		HistorySlot:      -1,
		ToolsSlot:        -1,
		MaxContextTokens: 0, // no limit
		MaxOutputTokens:  0,
		FitsSlot:         0,
		OverflowSlot:     0,
		TotalSlot:        1,
	}

	runFitInstruction(CheckContextFit(cfg), ctx)

	if !ctx.BoolSlots[0] {
		t.Error("MaxContextTokens=0: want fits=true (no limit), got false")
	}
	if ctx.IntSlots[0] != 0 {
		t.Errorf("MaxContextTokens=0: want overflow=0, got %d", ctx.IntSlots[0])
	}
	if ctx.IntSlots[1] <= 0 {
		t.Errorf("MaxContextTokens=0: total should still be computed, got %d", ctx.IntSlots[1])
	}
}

// â”€â”€ MaxOutputTokens reserved â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestCheckContextFit_MaxOutputTokens_Reserved(t *testing.T) {
	ctx := newFitContext()
	// Prompt of exactly 40 tokens (160 chars)
	ctx.ByteSlots[0] = []byte(strings.Repeat("a", 160))

	// budget = 100 - 80 = 20 tokens; prompt is ~40 â†’ overflow = 20
	cfg := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       -1,
		HistorySlot:      -1,
		ToolsSlot:        -1,
		MaxContextTokens: 100,
		MaxOutputTokens:  80,
		FitsSlot:         0,
		OverflowSlot:     0,
		TotalSlot:        1,
	}

	runFitInstruction(CheckContextFit(cfg), ctx)

	if ctx.BoolSlots[0] {
		t.Error("want fits=false when output reservation exceeds budget")
	}
	if ctx.IntSlots[0] <= 0 {
		t.Errorf("want overflow > 0, got %d", ctx.IntSlots[0])
	}
}

// â”€â”€ JSON messages array in PromptSlot â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestCheckContextFit_JSONMessagesInPromptSlot(t *testing.T) {
	ctx := newFitContext()

	msgs := []CanonicalMessage{
		{Role: RoleUser, Content: "What is the capital of France?"},
		{Role: RoleAssistant, Content: "The capital of France is Paris."},
	}
	msgsJSON, _ := json.Marshal(msgs)
	ctx.ByteSlots[0] = msgsJSON // starts with '[' â€” should be decoded

	cfgJSON := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       -1,
		HistorySlot:      -1,
		ToolsSlot:        -1,
		MaxContextTokens: 10000,
		MaxOutputTokens:  0,
		FitsSlot:         -1,
		OverflowSlot:     -1,
		TotalSlot:        0,
	}
	runFitInstruction(CheckContextFit(cfgJSON), ctx)
	totalDecoded := ctx.IntSlots[0]

	// Cross-check: sum manually using estimateTokens
	expectedTotal := 0
	for _, m := range msgs {
		expectedTotal += estimateTokens(m.Content) + 4
	}
	if totalDecoded != int64(expectedTotal) {
		t.Errorf("decoded JSON messages: want total=%d, got %d", expectedTotal, totalDecoded)
	}
}

// â”€â”€ All optional slots absent â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestCheckContextFit_OnlyPrompt_NoOptionalSlots(t *testing.T) {
	ctx := newFitContext()
	ctx.ByteSlots[0] = []byte("Just a simple user message.")

	cfg := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       -1,
		HistorySlot:      -1,
		ToolsSlot:        -1,
		MaxContextTokens: 10000,
		MaxOutputTokens:  0,
		FitsSlot:         0,
		OverflowSlot:     0,
		TotalSlot:        1,
	}
	runFitInstruction(CheckContextFit(cfg), ctx)

	expected := int64(estimateTokens("Just a simple user message."))
	if ctx.IntSlots[1] != expected {
		t.Errorf("only prompt: want total=%d, got %d", expected, ctx.IntSlots[1])
	}
	if !ctx.BoolSlots[0] {
		t.Error("want fits=true, got false")
	}
}

// â”€â”€ TotalSlot written correctly â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestCheckContextFit_TotalSlot_WrittenCorrectly(t *testing.T) {
	ctx := newFitContext()
	ctx.ByteSlots[0] = []byte("sixteen characters!!")  // 20 chars â†’ 5 tokens

	cfg := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       -1,
		HistorySlot:      -1,
		ToolsSlot:        -1,
		MaxContextTokens: 1000,
		MaxOutputTokens:  0,
		FitsSlot:         -1,
		OverflowSlot:     -1,
		TotalSlot:        3,
	}
	runFitInstruction(CheckContextFit(cfg), ctx)

	expected := int64(estimateTokens("sixteen characters!!"))
	if ctx.IntSlots[3] != expected {
		t.Errorf("TotalSlot: want %d, got %d", expected, ctx.IntSlots[3])
	}
}

// â”€â”€ Slot out-of-range handled gracefully (no panic) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestCheckContextFit_OutOfRangeSlots_NoPanic(t *testing.T) {
	ctx := newFitContext()
	ctx.ByteSlots[0] = []byte("test prompt")

	cfg := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       99,  // out of range â€” must not panic
		HistorySlot:      99,  // out of range â€” must not panic
		ToolsSlot:        99,  // out of range â€” must not panic
		MaxContextTokens: 1000,
		MaxOutputTokens:  0,
		FitsSlot:         99, // out of range â€” must not panic
		OverflowSlot:     99, // out of range â€” must not panic
		TotalSlot:        99, // out of range â€” must not panic
	}

	// Should complete without panicking and return PC+1
	next := runFitInstruction(CheckContextFit(cfg), ctx)
	if next != 1 {
		t.Errorf("want next PC=1, got %d", next)
	}
}

// â”€â”€ AlwaysContinues: step never returns StopPlan â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestCheckContextFit_AlwaysReturnsPCPlus1(t *testing.T) {
	ctx := newFitContext()
	ctx.ByteSlots[0] = []byte(strings.Repeat("overflow ", 100000)) // huge prompt

	cfg := CheckContextFitConfig{
		PromptSlot:       0,
		SystemSlot:       -1,
		HistorySlot:      -1,
		ToolsSlot:        -1,
		MaxContextTokens: 10,
		MaxOutputTokens:  0,
		FitsSlot:         0,
		OverflowSlot:     0,
		TotalSlot:        -1,
	}

	state := &engine.ExecutionState{PC: 42}
	next := CheckContextFit(cfg).Action(ctx, state)

	if next != 43 {
		t.Errorf("want PC+1=43, got %d (step must never stop the flow)", next)
	}
}
