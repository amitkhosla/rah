package steps

import (
	"bytes"
	"net/http"
	"testing"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// mockSSEWriter implements rctx.ResponseWriter and http.Flusher.
type mockSSEWriter struct {
	buf     bytes.Buffer
	flushed int
}

func (m *mockSSEWriter) Write(p []byte) (int, error) {
	return m.buf.Write(p)
}

func (m *mockSSEWriter) Header() http.Header {
	return http.Header{}
}

func (m *mockSSEWriter) WriteHeader(int) {
	// no-op for testing
}

func (m *mockSSEWriter) Flush() {
	m.flushed++
}

func TestSendSSEEvent_DataOnly(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.Writer = &mockSSEWriter{}
	ctx.ByteSlots = make([][]byte, 4)
	ctx.ByteSlots[1] = []byte("hello world")

	cfg := SSEEventConfig{
		EventSlot: -1,
		DataSlot:  1,
		IDSlot:    -1,
	}

	state := &engine.ExecutionState{PC: 0}
	instr := SendSSEEvent(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected next PC 1, got %d", nextPC)
	}

	w := ctx.Writer.(*mockSSEWriter)
	expected := "data: hello world\n\n"
	if w.buf.String() != expected {
		t.Errorf("expected %q, got %q", expected, w.buf.String())
	}
	if w.flushed != 1 {
		t.Errorf("expected flush to be called once, got %d", w.flushed)
	}
}

func TestSendSSEEvent_WithEventName(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.Writer = &mockSSEWriter{}
	ctx.ByteSlots = make([][]byte, 4)
	ctx.ByteSlots[0] = []byte("update")
	ctx.ByteSlots[1] = []byte("test data")

	cfg := SSEEventConfig{
		EventSlot: 0,
		DataSlot:  1,
		IDSlot:    -1,
	}

	state := &engine.ExecutionState{PC: 0}
	instr := SendSSEEvent(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected next PC 1, got %d", nextPC)
	}

	w := ctx.Writer.(*mockSSEWriter)
	expected := "event: update\ndata: test data\n\n"
	if w.buf.String() != expected {
		t.Errorf("expected %q, got %q", expected, w.buf.String())
	}
	if w.flushed != 1 {
		t.Errorf("expected flush to be called once, got %d", w.flushed)
	}
}

func TestSendSSEEvent_WithID(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.Writer = &mockSSEWriter{}
	ctx.ByteSlots = make([][]byte, 4)
	ctx.ByteSlots[1] = []byte("message content")
	ctx.ByteSlots[2] = []byte("42")

	cfg := SSEEventConfig{
		EventSlot: -1,
		DataSlot:  1,
		IDSlot:    2,
	}

	state := &engine.ExecutionState{PC: 0}
	instr := SendSSEEvent(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected next PC 1, got %d", nextPC)
	}

	w := ctx.Writer.(*mockSSEWriter)
	expected := "id: 42\ndata: message content\n\n"
	if w.buf.String() != expected {
		t.Errorf("expected %q, got %q", expected, w.buf.String())
	}
	if w.flushed != 1 {
		t.Errorf("expected flush to be called once, got %d", w.flushed)
	}
}

func TestSendSSEEvent_FullFrame(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.Writer = &mockSSEWriter{}
	ctx.ByteSlots = make([][]byte, 4)
	ctx.ByteSlots[0] = []byte("progress")
	ctx.ByteSlots[1] = []byte("50% done")
	ctx.ByteSlots[2] = []byte("123")

	cfg := SSEEventConfig{
		EventSlot: 0,
		DataSlot:  1,
		IDSlot:    2,
	}

	state := &engine.ExecutionState{PC: 0}
	instr := SendSSEEvent(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected next PC 1, got %d", nextPC)
	}

	w := ctx.Writer.(*mockSSEWriter)
	expected := "event: progress\nid: 123\ndata: 50% done\n\n"
	if w.buf.String() != expected {
		t.Errorf("expected %q, got %q", expected, w.buf.String())
	}
	if w.flushed != 1 {
		t.Errorf("expected flush to be called once, got %d", w.flushed)
	}
}

func TestSendSSEEvent_NilWriter(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.Writer = nil // no writer
	ctx.ByteSlots = make([][]byte, 4)
	ctx.ByteSlots[1] = []byte("data")

	cfg := SSEEventConfig{
		EventSlot: -1,
		DataSlot:  1,
		IDSlot:    -1,
	}

	state := &engine.ExecutionState{PC: 0}
	instr := SendSSEEvent(cfg)

	// Should not panic and return next PC
	nextPC := instr.Action(ctx, state)
	if nextPC != 1 {
		t.Errorf("expected next PC 1, got %d", nextPC)
	}
}

func TestSendSSEEvent_EmptyDataSlot(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.Writer = &mockSSEWriter{}
	ctx.ByteSlots = make([][]byte, 4)
	ctx.ByteSlots[1] = []byte("") // empty data

	cfg := SSEEventConfig{
		EventSlot: -1,
		DataSlot:  1,
		IDSlot:    -1,
	}

	state := &engine.ExecutionState{PC: 0}
	instr := SendSSEEvent(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected next PC 1, got %d", nextPC)
	}

	w := ctx.Writer.(*mockSSEWriter)
	expected := "data: \n\n"
	if w.buf.String() != expected {
		t.Errorf("expected %q, got %q", expected, w.buf.String())
	}
}

func TestSendSSEEvent_EventSlotNegative(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.Writer = &mockSSEWriter{}
	ctx.ByteSlots = make([][]byte, 4)
	ctx.ByteSlots[0] = []byte("ignored")
	ctx.ByteSlots[1] = []byte("payload")

	cfg := SSEEventConfig{
		EventSlot: -1, // Skip event line
		DataSlot:  1,
		IDSlot:    -1,
	}

	state := &engine.ExecutionState{PC: 0}
	instr := SendSSEEvent(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected next PC 1, got %d", nextPC)
	}

	w := ctx.Writer.(*mockSSEWriter)
	expected := "data: payload\n\n"
	if w.buf.String() != expected {
		t.Errorf("expected %q, got %q", expected, w.buf.String())
	}
}

func TestSendSSEEvent_LargePayload(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.Writer = &mockSSEWriter{}
	ctx.ByteSlots = make([][]byte, 4)

	// Create a payload larger than the 4KB stack buffer
	largeData := make([]byte, 5000)
	for i := range largeData {
		largeData[i] = 'X'
	}
	ctx.ByteSlots[1] = largeData

	cfg := SSEEventConfig{
		EventSlot: -1,
		DataSlot:  1,
		IDSlot:    -1,
	}

	state := &engine.ExecutionState{PC: 0}
	instr := SendSSEEvent(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected next PC 1, got %d", nextPC)
	}

	w := ctx.Writer.(*mockSSEWriter)
	output := w.buf.String()
	if !bytes.Contains([]byte(output), []byte("data: ")) {
		t.Errorf("expected output to contain 'data: ', got %s", output)
	}
	if !bytes.Contains([]byte(output), largeData) {
		t.Errorf("expected output to contain large payload")
	}
	if w.flushed != 1 {
		t.Errorf("expected flush to be called once, got %d", w.flushed)
	}
}

func TestParseAnthropicStreamChunk_TextDelta(t *testing.T) {
	data := []byte(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`)
	delta, _, _, done, err := ParseAnthropicStreamChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	if delta != "hello" {
		t.Errorf("expected delta=hello, got %q", delta)
	}
	if done {
		t.Error("expected not done")
	}
}

func TestParseAnthropicStreamChunk_MessageStop(t *testing.T) {
	data := []byte(`{"type":"message_stop"}`)
	_, _, _, done, err := ParseAnthropicStreamChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Error("expected done=true on message_stop")
	}
}

func TestParseOpenAIStreamChunk_Delta(t *testing.T) {
	data := []byte(`{"choices":[{"delta":{"content":"world"},"finish_reason":null}]}`)
	delta, _, _, done, err := ParseOpenAIStreamChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	if delta != "world" {
		t.Errorf("expected delta=world, got %q", delta)
	}
	if done {
		t.Error("expected not done")
	}
}

func TestParseOpenAIStreamChunk_Done(t *testing.T) {
	data := []byte(`[DONE]`)
	_, _, _, done, err := ParseOpenAIStreamChunk(data)
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Error("expected done=true on [DONE]")
	}
}

func TestSendSSEEvent_EmptyIDSlot(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.Writer = &mockSSEWriter{}
	ctx.ByteSlots = make([][]byte, 4)
	ctx.ByteSlots[1] = []byte("data")
	ctx.ByteSlots[2] = []byte("") // empty id

	cfg := SSEEventConfig{
		EventSlot: -1,
		DataSlot:  1,
		IDSlot:    2, // set but empty
	}

	state := &engine.ExecutionState{PC: 0}
	instr := SendSSEEvent(cfg)
	nextPC := instr.Action(ctx, state)

	if nextPC != 1 {
		t.Errorf("expected next PC 1, got %d", nextPC)
	}

	w := ctx.Writer.(*mockSSEWriter)
	expected := "data: data\n\n"
	if w.buf.String() != expected {
		t.Errorf("expected %q, got %q", expected, w.buf.String())
	}
}
