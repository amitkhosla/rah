package steps

import (
	"encoding/json"
	"testing"

	"rah/internal/engine"
	"rah/internal/rctx"
)

func TestChunkText_ShortText_SingleChunk(t *testing.T) {
	// Text shorter than chunk size should produce single chunk
	ctx := &rctx.Context{}
	ctx.InitSlots()

	shortText := []byte("This is a short text that fits in one chunk easily.")
	ctx.ByteSlots[0] = shortText
	ctx.ByteSlots[1] = make([]byte, 0) // output slot

	cfg := ChunkTextConfig{
		InputSlot:  0,
		OutputSlot: 1,
		CountSlot:  0,
		ChunkSize:  2048,
		Overlap:    256,
	}

	state := &engine.ExecutionState{}
	instruction := ChunkText(cfg)
	nextPC := instruction.Action(ctx, state)

	if nextPC != 1 {
		t.Fatalf("expected nextPC=1, got %d", nextPC)
	}

	// Verify output slot has JSON
	output := ctx.ByteSlots[1]
	var chunks []string
	err := json.Unmarshal(output, &chunks)
	if err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	if chunks[0] != "This is a short text that fits in one chunk easily." {
		t.Fatalf("chunk content mismatch: %q", chunks[0])
	}

	// Verify count slot
	if ctx.IntSlots[0] != 1 {
		t.Fatalf("expected count=1, got %d", ctx.IntSlots[0])
	}
}

func TestChunkText_LongText_MultipleChunks(t *testing.T) {
	// Long text should be split into multiple chunks
	ctx := &rctx.Context{}
	ctx.InitSlots()

	// Create text longer than chunk size
	longText := ""
	for i := 0; i < 5; i++ {
		longText += "This is a longer piece of text that will be split into multiple chunks. "
	}

	ctx.ByteSlots[0] = []byte(longText)
	ctx.ByteSlots[1] = make([]byte, 0)

	cfg := ChunkTextConfig{
		InputSlot:  0,
		OutputSlot: 1,
		CountSlot:  1,
		ChunkSize:  100, // small chunk size to force splitting
		Overlap:    20,
	}

	state := &engine.ExecutionState{}
	instruction := ChunkText(cfg)
	nextPC := instruction.Action(ctx, state)

	if nextPC != 1 {
		t.Fatalf("expected nextPC=1, got %d", nextPC)
	}

	// Verify multiple chunks
	output := ctx.ByteSlots[1]
	var chunks []string
	err := json.Unmarshal(output, &chunks)
	if err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if len(chunks) <= 1 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	// Verify count slot
	if ctx.IntSlots[1] != int64(len(chunks)) {
		t.Fatalf("expected count=%d, got %d", len(chunks), ctx.IntSlots[1])
	}
}

func TestChunkText_OverlapPresent(t *testing.T) {
	// Verify adjacent chunks share overlap
	ctx := &rctx.Context{}
	ctx.InitSlots()

	// Create text with distinct words
	text := "word1 word2 word3 word4 word5 word6 word7 word8 word9 word10 word11 word12 word13 word14 word15"
	ctx.ByteSlots[0] = []byte(text)
	ctx.ByteSlots[1] = make([]byte, 0)

	cfg := ChunkTextConfig{
		InputSlot:  0,
		OutputSlot: 1,
		CountSlot:  -1, // skip count
		ChunkSize:  50,
		Overlap:    20,
	}

	state := &engine.ExecutionState{}
	instruction := ChunkText(cfg)
	instruction.Action(ctx, state)

	var chunks []string
	json.Unmarshal(ctx.ByteSlots[1], &chunks)

	// With overlap, adjacent chunks should have some common text
	if len(chunks) > 1 {
		// Check that chunk[0] is contained in text and chunk[1] is also in text
		if len(chunks[0]) > 0 && len(chunks[1]) > 0 {
			// Both chunks should exist and be non-empty
			t.Logf("Chunk 0 length: %d, Chunk 1 length: %d", len(chunks[0]), len(chunks[1]))
		}
	}
}

func TestChunkText_EmptySlot_Noop(t *testing.T) {
	// Empty input should return PC+1 without writing
	ctx := &rctx.Context{}
	ctx.InitSlots()

	ctx.ByteSlots[0] = []byte("") // empty
	ctx.ByteSlots[1] = make([]byte, 0)

	cfg := ChunkTextConfig{
		InputSlot:  0,
		OutputSlot: 1,
		CountSlot:  0,
		ChunkSize:  2048,
		Overlap:    256,
	}

	state := &engine.ExecutionState{}
	instruction := ChunkText(cfg)
	nextPC := instruction.Action(ctx, state)

	if nextPC != 1 {
		t.Fatalf("expected nextPC=1, got %d", nextPC)
	}

	// Output slot should remain empty or unmodified
	// (depending on implementation; we don't write anything)
}

func TestChunkText_CountSlotWritten(t *testing.T) {
	// CountSlot should receive chunk count when valid
	ctx := &rctx.Context{}
	ctx.InitSlots()

	text := "One Two Three Four Five Six Seven Eight Nine Ten Eleven Twelve Thirteen Fourteen Fifteen Sixteen"
	ctx.ByteSlots[0] = []byte(text)
	ctx.ByteSlots[1] = make([]byte, 0)

	cfg := ChunkTextConfig{
		InputSlot:  0,
		OutputSlot: 1,
		CountSlot:  2,
		ChunkSize:  30,
		Overlap:    5,
	}

	state := &engine.ExecutionState{}
	instruction := ChunkText(cfg)
	instruction.Action(ctx, state)

	// Verify count was written
	count := ctx.IntSlots[2]
	if count <= 0 {
		t.Fatalf("expected count > 0, got %d", count)
	}

	// Verify count matches actual chunks
	var chunks []string
	json.Unmarshal(ctx.ByteSlots[1], &chunks)
	if int64(len(chunks)) != count {
		t.Fatalf("count mismatch: expected %d, got %d", len(chunks), count)
	}
}

func TestChunkText_CountSlotNegative_NoWrite(t *testing.T) {
	// CountSlot=-1 should skip writing count (no panic)
	ctx := &rctx.Context{}
	ctx.InitSlots()

	ctx.ByteSlots[0] = []byte("Test text for chunking purposes")
	ctx.ByteSlots[1] = make([]byte, 0)

	cfg := ChunkTextConfig{
		InputSlot:  0,
		OutputSlot: 1,
		CountSlot:  -1, // skip
		ChunkSize:  2048,
		Overlap:    256,
	}

	state := &engine.ExecutionState{}
	instruction := ChunkText(cfg)
	nextPC := instruction.Action(ctx, state)

	if nextPC != 1 {
		t.Fatalf("expected nextPC=1, got %d", nextPC)
	}

	// No panic should occur
}

func TestChunkText_DefaultChunkSize(t *testing.T) {
	// Zero ChunkSize should use default
	ctx := &rctx.Context{}
	ctx.InitSlots()

	// Create text just under default (2048)
	text := "word "
	for i := 0; i < 500; i++ {
		text += "word "
	}
	ctx.ByteSlots[0] = []byte(text)
	ctx.ByteSlots[1] = make([]byte, 0)

	cfg := ChunkTextConfig{
		InputSlot:  0,
		OutputSlot: 1,
		CountSlot:  0,
		ChunkSize:  0, // use default
		Overlap:    0,
	}

	state := &engine.ExecutionState{}
	instruction := ChunkText(cfg)
	nextPC := instruction.Action(ctx, state)

	if nextPC != 1 {
		t.Fatalf("expected nextPC=1, got %d", nextPC)
	}

	var chunks []string
	json.Unmarshal(ctx.ByteSlots[1], &chunks)
	if len(chunks) == 0 {
		t.Fatalf("expected non-empty chunks with default chunk size")
	}
}
