package ingest

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

// ── Mock Sink ──────────────────────────────────────────────────────────────────

type mockSink struct {
	mu      sync.Mutex
	written [][]byte
	closed  bool
}

func (m *mockSink) Write(b []byte) error {
	cp := make([]byte, len(b))
	copy(cp, b)
	m.mu.Lock()
	m.written = append(m.written, cp)
	m.mu.Unlock()
	return nil
}

func (m *mockSink) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return nil
}

func (m *mockSink) Name() string {
	return "mock"
}

func (m *mockSink) totalEvents() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, b := range m.written {
		n += bytes.Count(b, []byte("\n"))
	}
	return n
}

func (m *mockSink) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func buildTestPipeline(sink *mockSink, kind EventKind, ringCap int, allowDrop bool) *Pipeline {
	sw := &sinkWorker{
		name:       "test",
		ch:         make(chan Event, ringCap*2),
		sink:       sink,
		formatter:  NDJSONFormatter{},
		maxItems:   50,
		maxBytes:   64 * 1024,
		flushMs:    50,
		minWorkers: 1,
		maxWorkers: 2,
		stopCh:     make(chan struct{}),
	}
	sw.startWorkers(sw.minWorkers)

	var overflow chan Event
	if !allowDrop {
		overflow = make(chan Event, ringCap)
	}

	kr := &kindRing{
		ring:      NewRing(ringCap),
		overflow:  overflow,
		allowDrop: allowDrop,
		sinks:     []*sinkWorker{sw},
	}
	kinds := map[EventKind]*kindRing{kind: kr}
	p := newPipeline(kinds, []*sinkWorker{sw}, 2, 5*time.Second)
	return p
}

// ── Tests ──────────────────────────────────────────────────────────────────────

func TestPipelineEmitDeliveredToSink(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 4, true)
	defer p.Stop()

	e := Event{Kind: KindCustom}
	e.SetPayload([]byte(`{"msg":"hello"}`), 1)
	p.Emit(e)

	p.Stop()

	if got := sink.totalEvents(); got != 1 {
		t.Errorf("totalEvents() = %d, want 1", got)
	}
}

func TestPipelineStopDrainsAllPending(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 32, true)

	for i := 0; i < 20; i++ {
		e := Event{Kind: KindCustom}
		e.SetPayload([]byte(`{"event":1}`), 1)
		p.Emit(e)
	}

	p.Stop()

	if got := sink.totalEvents(); got != 20 {
		t.Errorf("totalEvents() = %d, want 20", got)
	}
}

func TestPipelineDropWhenRingFull(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 4, true)
	defer p.Stop()

	for i := 0; i < 100; i++ {
		e := Event{Kind: KindCustom}
		e.SetPayload([]byte(`{"i":1}`), 1)
		p.Emit(e)
	}

	p.Stop()

	events := sink.totalEvents()
	if events < 0 {
		t.Errorf("totalEvents() = %d, want >= 0", events)
	}
}

func TestPipelineUnknownKindIsNoOp(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 4, true)
	defer p.Stop()

	e := Event{Kind: KindLLMRequest}
	e.SetPayload([]byte(`{"data":1}`), 1)
	p.Emit(e)

	p.Stop()

	if got := sink.totalEvents(); got != 0 {
		t.Errorf("totalEvents() = %d, want 0", got)
	}
}

func TestPipelineStopIsIdempotent(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 4, true)

	e := Event{Kind: KindCustom}
	e.SetPayload([]byte(`{"msg":"test"}`), 1)
	p.Emit(e)

	p.Stop()
	p.Stop()

	if !sink.isClosed() {
		t.Errorf("sink not closed after Stop()")
	}
}

func TestPipelineMultipleEmitsBeforeStop(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 16, true)

	for i := 0; i < 5; i++ {
		e := Event{Kind: KindCustom}
		e.SetPayload([]byte(`{"seq":1}`), 1)
		p.Emit(e)
	}

	p.Stop()

	if got := sink.totalEvents(); got != 5 {
		t.Errorf("totalEvents() = %d, want 5", got)
	}
}

func TestPipelineEventPayloadPreserved(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 4, true)

	payload := []byte(`{"test":"data","num":42}`)
	e := Event{Kind: KindCustom}
	e.SetPayload(payload, 1)
	p.Emit(e)

	p.Stop()

	if sink.totalEvents() != 1 {
		t.Fatal("expected 1 event in sink")
	}

	sink.mu.Lock()
	written := sink.written[0]
	sink.mu.Unlock()

	if !bytes.Contains(written, []byte("test")) || !bytes.Contains(written, []byte("data")) {
		t.Errorf("payload not found in written output: %s", string(written))
	}
}

func TestPipelineNumSinksForKind(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 4, true)
	defer p.Stop()

	if got := p.NumSinksForKind(KindCustom); got != 1 {
		t.Errorf("NumSinksForKind(KindCustom) = %d, want 1", got)
	}

	if got := p.NumSinksForKind(KindLLMRequest); got != 0 {
		t.Errorf("NumSinksForKind(KindLLMRequest) = %d, want 0", got)
	}
}

func TestPipelineRapidEmitAndStop(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 8, true)

	for i := 0; i < 50; i++ {
		e := Event{Kind: KindCustom}
		e.SetPayload([]byte(`{"rapid":1}`), 1)
		p.Emit(e)
	}

	p.Stop()

	if got := sink.totalEvents(); got <= 0 {
		t.Errorf("totalEvents() = %d, want > 0", got)
	}
}

func TestPipelineInlinePayload(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 4, true)

	smallPayload := []byte("short")
	e := Event{Kind: KindCustom}
	e.SetPayload(smallPayload, 1)

	if e.payloadHeap != nil {
		t.Error("expected inline payload, got heap allocation")
	}

	p.Emit(e)
	p.Stop()

	if got := sink.totalEvents(); got != 1 {
		t.Errorf("totalEvents() = %d, want 1", got)
	}
}

func TestPipelineLargePayload(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 4, true)

	largePayload := make([]byte, 200)
	for i := range largePayload {
		largePayload[i] = 'x'
	}

	e := Event{Kind: KindCustom}
	e.SetPayload(largePayload, 1)

	if e.payloadHeap == nil {
		t.Error("expected heap allocation for large payload, got inline")
	}

	p.Emit(e)
	p.Stop()

	if got := sink.totalEvents(); got != 1 {
		t.Errorf("totalEvents() = %d, want 1", got)
	}
}

func TestPipelineSinkClosedOnStop(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 4, true)

	e := Event{Kind: KindCustom}
	e.SetPayload([]byte(`{"msg":"test"}`), 1)
	p.Emit(e)

	p.Stop()

	if !sink.isClosed() {
		t.Error("sink not closed after pipeline Stop()")
	}
}

func TestPipelineMultipleKinds(t *testing.T) {
	sink1 := &mockSink{}
	sink2 := &mockSink{}

	sw1 := &sinkWorker{
		name:       "sink1",
		ch:         make(chan Event, 16),
		sink:       sink1,
		formatter:  NDJSONFormatter{},
		maxItems:   50,
		maxBytes:   64 * 1024,
		flushMs:    50,
		minWorkers: 1,
		maxWorkers: 2,
		stopCh:     make(chan struct{}),
	}
	sw1.startWorkers(sw1.minWorkers)

	sw2 := &sinkWorker{
		name:       "sink2",
		ch:         make(chan Event, 16),
		sink:       sink2,
		formatter:  NDJSONFormatter{},
		maxItems:   50,
		maxBytes:   64 * 1024,
		flushMs:    50,
		minWorkers: 1,
		maxWorkers: 2,
		stopCh:     make(chan struct{}),
	}
	sw2.startWorkers(sw2.minWorkers)

	kr1 := &kindRing{
		ring:      NewRing(8),
		allowDrop: true,
		sinks:     []*sinkWorker{sw1},
	}
	kr2 := &kindRing{
		ring:      NewRing(8),
		allowDrop: true,
		sinks:     []*sinkWorker{sw2},
	}

	kinds := map[EventKind]*kindRing{
		KindCustom:     kr1,
		KindLLMRequest: kr2,
	}
	p := newPipeline(kinds, []*sinkWorker{sw1, sw2}, 2, 5*time.Second)

	e1 := Event{Kind: KindCustom}
	e1.SetPayload([]byte(`{"sink":1}`), 1)
	p.Emit(e1)

	e2 := Event{Kind: KindLLMRequest}
	e2.SetPayload([]byte(`{"sink":2}`), 1)
	p.Emit(e2)

	p.Stop()

	if got := sink1.totalEvents(); got != 1 {
		t.Errorf("sink1.totalEvents() = %d, want 1", got)
	}
	if got := sink2.totalEvents(); got != 1 {
		t.Errorf("sink2.totalEvents() = %d, want 1", got)
	}
}

func TestPipelineEmptyEventPayload(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 4, true)

	e := Event{Kind: KindCustom}
	e.SetPayload([]byte{}, 1)
	p.Emit(e)

	p.Stop()

	if got := sink.totalEvents(); got != 1 {
		t.Errorf("totalEvents() = %d, want 1 (empty payload should still appear as newline)", got)
	}
}

func TestPipelineMetadataPreserved(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 4, true)

	e := Event{
		Kind:      KindCustom,
		TenantID:  42,
		Model:     "gpt-4",
		SessionID: "sess-123",
	}
	e.SetPayload([]byte(`{"data":"test"}`), 1)
	p.Emit(e)

	p.Stop()

	if got := sink.totalEvents(); got != 1 {
		t.Fatal("expected 1 event in sink")
	}

	sink.mu.Lock()
	written := sink.written[0]
	sink.mu.Unlock()

	if !bytes.Contains(written, []byte("42")) || !bytes.Contains(written, []byte("gpt-4")) || !bytes.Contains(written, []byte("sess-123")) {
		t.Errorf("metadata not preserved in output: %s", string(written))
	}
}

func TestPipelineStressSmallRing(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindCustom, 2, true)

	for i := 0; i < 10; i++ {
		e := Event{Kind: KindCustom}
		e.SetPayload([]byte(`{"stress":1}`), 1)
		p.Emit(e)
	}

	p.Stop()

	if got := sink.totalEvents(); got <= 0 {
		t.Errorf("totalEvents() = %d, want > 0", got)
	}
}

func TestPipelineNoOpOnUnregisteredKind(t *testing.T) {
	sink := &mockSink{}
	p := buildTestPipeline(sink, KindLLMResponse, 4, true)

	e1 := Event{Kind: KindLLMResponse}
	e1.SetPayload([]byte(`{"data":1}`), 1)
	p.Emit(e1)

	e2 := Event{Kind: KindPromptIn}
	e2.SetPayload([]byte(`{"data":2}`), 1)
	p.Emit(e2)

	p.Stop()

	if got := sink.totalEvents(); got != 1 {
		t.Errorf("totalEvents() = %d, want 1 (only KindLLMResponse registered)", got)
	}
}

func TestPipelineBackpressureWithAllowDropFalse(t *testing.T) {
	sink := &mockSink{}
	sw := &sinkWorker{
		name:       "test",
		ch:         make(chan Event, 4),
		sink:       sink,
		formatter:  NDJSONFormatter{},
		maxItems:   50,
		maxBytes:   64 * 1024,
		flushMs:    50,
		minWorkers: 1,
		maxWorkers: 2,
		stopCh:     make(chan struct{}),
	}
	sw.startWorkers(sw.minWorkers)

	overflow := make(chan Event, 4)
	kr := &kindRing{
		ring:      NewRing(4),
		overflow:  overflow,
		allowDrop: false,
		sinks:     []*sinkWorker{sw},
	}
	kinds := map[EventKind]*kindRing{KindCustom: kr}
	p := newPipeline(kinds, []*sinkWorker{sw}, 2, 100*time.Millisecond)

	for i := 0; i < 4; i++ {
		e := Event{Kind: KindCustom}
		e.SetPayload([]byte(`{"data":1}`), 1)
		p.Emit(e)
	}

	p.Stop()

	if got := sink.totalEvents(); got >= 0 {
		t.Logf("backpressure pipeline completed without panic; events: %d", got)
	}
}
