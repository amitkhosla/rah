package ingest

import (
	"encoding/json"
	"sync"
	"sync/atomic"
)

// EventKind identifies what kind of event occurred in the pipeline.
type EventKind string

const (
	KindPromptIn      EventKind = "prompt_in"
	KindPromptOut     EventKind = "prompt_out"
	KindLLMRequest    EventKind = "llm_request"
	KindLLMResponse   EventKind = "llm_response"
	KindToolCall      EventKind = "tool_call"
	KindToolResult    EventKind = "tool_result"
	KindRouteDecision EventKind = "route_decision"
	KindCacheHit      EventKind = "cache_hit"
	KindCacheMiss     EventKind = "cache_miss"
	KindHistoryTrim   EventKind = "history_trim"
	KindCostRecord    EventKind = "cost_record"
	KindCustom        EventKind = "custom"
)

// inlinePayloadMax is the threshold below which payload bytes are stored
// directly inside the Event struct (no heap allocation, cache-resident).
const inlinePayloadMax = 128

// sharedPayload is a ref-counted heap buffer shared across N sink workers.
// refs is pre-set to the number of sinks at emit time; each sink worker
// calls release() after processing — the last one returns the buffer to pool.
type sharedPayload struct {
	data []byte
	refs atomic.Int32
	pool *sync.Pool
}

func (p *sharedPayload) release() {
	if p.refs.Add(-1) == 0 {
		p.pool.Put(p.data[:0])
	}
}

// payloadPool reuses []byte buffers for large event payloads (>128 bytes).
// Reduces GC pressure at high event rates.
var payloadPool = &sync.Pool{New: func() any { return make([]byte, 0, 1024) }}

// Event is one ingestion event. Fixed-size metadata fields are value types.
// Payloads ≤128 bytes are stored inline (payloadInline); larger payloads use
// a ref-counted heap buffer (payloadHeap) drawn from payloadPool.
//
// The struct is passed by value throughout the pipeline so each goroutine owns
// its own copy of the header fields. The payloadHeap pointer (if set) is shared
// across sink workers and protected by the ref count.
type Event struct {
	TenantID     uint16
	APIID        uint32
	Kind         EventKind
	Model        string
	SessionID    string
	TxID         string
	TimestampNs  int64
	DurationNs   int64
	InputTokens  int32
	OutputTokens int32

	// Small payloads (≤128B) — stored inline, zero allocation, no pointer chase.
	payloadInline [inlinePayloadMax]byte
	payloadLen    uint16

	// Large payloads (>128B) — ref-counted heap buffer; nil when using inline.
	payloadHeap *sharedPayload
}

// Payload returns the event's payload bytes.
// Returns a slice into payloadInline for small payloads (zero allocation),
// or payloadHeap.data for large payloads.
func (e *Event) Payload() []byte {
	if e.payloadHeap != nil {
		return e.payloadHeap.data
	}
	return e.payloadInline[:e.payloadLen]
}

// SetPayload stores raw bytes as the event payload.
// If raw is nil or empty, payload is cleared.
// If len(raw) ≤ inlinePayloadMax, bytes are stored inline (no allocation).
// Otherwise a buffer is drawn from payloadPool and refs is set to numSinks.
// numSinks must equal the number of sink workers that will process this event.
func (e *Event) SetPayload(raw []byte, numSinks int) {
	if len(raw) == 0 {
		e.payloadLen = 0
		e.payloadHeap = nil
		return
	}
	if len(raw) <= inlinePayloadMax {
		copy(e.payloadInline[:], raw)
		e.payloadLen = uint16(len(raw))
		e.payloadHeap = nil
		return
	}
	buf := payloadPool.Get().([]byte)[:0]
	buf = append(buf, raw...)
	sp := &sharedPayload{data: buf, pool: payloadPool}
	sp.refs.Store(int32(numSinks))
	e.payloadHeap = sp
	e.payloadLen = 0
}

// releasePayload decrements the shared payload ref count.
// Must be called exactly once per sink worker after the event is processed.
// Safe to call when payloadHeap is nil (inline payload — no-op).
func (e *Event) releasePayload() {
	if e.payloadHeap != nil {
		e.payloadHeap.release()
	}
}

// MarshalJSON produces the canonical JSON representation of an Event.
// Called by formatters; not on the hot path.
func (e *Event) MarshalJSON() ([]byte, error) {
	type wire struct {
		TenantID     uint16          `json:"tenant_id"`
		APIID        uint32          `json:"api_id"`
		Kind         EventKind       `json:"kind"`
		Model        string          `json:"model,omitempty"`
		SessionID    string          `json:"session_id,omitempty"`
		TxID         string          `json:"tx_id,omitempty"`
		TimestampNs  int64           `json:"ts_ns"`
		DurationNs   int64           `json:"duration_ns,omitempty"`
		InputTokens  int32           `json:"input_tokens,omitempty"`
		OutputTokens int32           `json:"output_tokens,omitempty"`
		Payload      json.RawMessage `json:"payload,omitempty"`
	}
	w := wire{
		TenantID:     e.TenantID,
		APIID:        e.APIID,
		Kind:         e.Kind,
		Model:        e.Model,
		SessionID:    e.SessionID,
		TxID:         e.TxID,
		TimestampNs:  e.TimestampNs,
		DurationNs:   e.DurationNs,
		InputTokens:  e.InputTokens,
		OutputTokens: e.OutputTokens,
	}
	if p := e.Payload(); len(p) > 0 {
		if json.Valid(p) {
			w.Payload = json.RawMessage(p)
		} else {
			b, err := json.Marshal(string(p))
			if err == nil {
				w.Payload = json.RawMessage(b)
			}
		}
	}
	return json.Marshal(w)
}

// Sink is the pluggable backend that receives batches of formatted bytes.
// Implementations must be safe for concurrent calls from multiple workers.
type Sink interface {
	// Write receives a pre-formatted byte slice (e.g. NDJSON or JSON array).
	// The slice must not be retained after Write returns.
	// Errors are logged but do not stop the pipeline.
	Write(formatted []byte) error
	// Close flushes pending data and releases resources.
	Close() error
	// Name returns a human-readable label used in log messages.
	Name() string
}
