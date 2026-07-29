package observability

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	maxRootAttrs  = 12
	maxInstrAttrs = 3
	maxLLMAttrs   = 8
)

// OTELObsStore bridges internal TraceRecords into OTEL spans.
//
// WriteTraceBatch is called exclusively from the single drain goroutine of
// traceWriteRing, so the pre-allocated attribute buffers (rootBuf, instrBuf,
// llmBuf) require no locks — they are only accessed by that one goroutine.
//
// The tracer is obtained lazily on the first WriteTraceBatch call via sync.Once,
// ensuring it uses the configured global TracerProvider (set by main.go S8 OTEL
// init) rather than the noop provider present at store construction time.
//
// All read/query methods are no-ops; Studio UI reads from the primary MemObsStore
// via FanOutObsStore.
type OTELObsStore struct {
	// initOnce guards one-time tracer initialisation from the global provider.
	// After the first call the tracer field is set and all subsequent calls are
	// a single atomic load with no lock acquisition.
	initOnce sync.Once
	tracer   trace.Tracer

	// schema maps instruction PC → human-readable name.
	// Populated via UpsertInstrSchema (called at bake time from the main goroutine).
	// Swapped atomically so the drain goroutine always sees a consistent snapshot.
	schema atomic.Pointer[map[int16]string]

	// Pre-allocated attribute buffers — reused across every WriteTraceBatch call.
	// Safe without locks because WriteTraceBatch is called from a single goroutine.
	rootBuf  [maxRootAttrs]attribute.KeyValue
	instrBuf [maxInstrAttrs]attribute.KeyValue
	llmBuf   [maxLLMAttrs]attribute.KeyValue
}

// newOTELObsStore creates an OTELObsStore that uses the global TracerProvider.
// The tracer is resolved lazily on the first WriteTraceBatch call.
func newOTELObsStore() *OTELObsStore {
	return &OTELObsStore{}
}

// getTracer returns the tracer, initialising it from the global provider on the
// first call. After initialisation every call is a no-op atomic check.
func (o *OTELObsStore) getTracer() trace.Tracer {
	o.initOnce.Do(func() {
		o.tracer = otel.GetTracerProvider().Tracer("rah-gateway")
	})
	return o.tracer
}

// otelTraceID converts our uint64 TraceID to a 128-bit OTEL TraceID.
// The uint64 is placed in the upper 8 bytes; lower 8 bytes are zeroed.
func otelTraceID(id uint64) trace.TraceID {
	var tid [16]byte
	binary.BigEndian.PutUint64(tid[:8], id)
	return trace.TraceID(tid)
}

// otelRootSpanID converts our uint64 TraceID to an 8-byte OTEL SpanID for the root span.
func otelRootSpanID(traceID uint64) trace.SpanID {
	var sid [8]byte
	binary.BigEndian.PutUint64(sid[:], traceID)
	return trace.SpanID(sid)
}


// WriteTraceBatch converts each TraceRecord into OTEL spans and submits them
// to the configured TracerProvider. Called only from the drain goroutine.
func (o *OTELObsStore) WriteTraceBatch(_ context.Context, records []TraceRecord) error {
	tracer := o.getTracer()
	schemaPtr := o.schema.Load()

	for i := range records {
		o.emitSpans(tracer, schemaPtr, &records[i])
	}
	return nil
}

// emitSpans converts one TraceRecord into a root span plus instruction and LLM child spans.
func (o *OTELObsStore) emitSpans(tracer trace.Tracer, schema *map[int16]string, rec *TraceRecord) {
	// Build TraceID and root SpanID from our uint64 — stack-allocated value types.
	traceID := otelTraceID(rec.TraceID)
	rootSpanID := otelRootSpanID(rec.TraceID)

	// Root span time bounds.
	rootStart := time.Unix(rec.Timestamp, 0)
	rootEnd := rootStart.Add(time.Duration(rec.DurationNs))

	// Fill root attributes into pre-allocated buffer — no heap allocation.
	n := 0
	o.rootBuf[n] = semconv.HTTPMethodKey.String(rec.Method)
	n++
	o.rootBuf[n] = semconv.HTTPStatusCodeKey.Int(rec.Status)
	n++
	o.rootBuf[n] = attribute.String("rah.api_name", rec.ApiName)
	n++
	o.rootBuf[n] = attribute.Int("rah.tenant_id", int(rec.TenantID))
	n++
	o.rootBuf[n] = attribute.Int64("rah.duration_ns", rec.DurationNs)
	n++
	o.rootBuf[n] = attribute.Int64("rah.gateway_ns", rec.GatewayNs)
	n++
	o.rootBuf[n] = attribute.Int64("rah.upstream_ns", rec.UpstreamNs)
	n++
	o.rootBuf[n] = attribute.Int("rah.upstream_calls", int(rec.UpstreamCalls))
	n++
	o.rootBuf[n] = attribute.Int64("rah.req_bytes", rec.ReqBytes)
	n++
	o.rootBuf[n] = attribute.Int64("rah.res_bytes", rec.ResBytes)
	n++
	o.rootBuf[n] = attribute.Int("rah.api_version_id", int(rec.ApiVersionID))
	n++
	o.rootBuf[n] = attribute.Int("rah.endpoint_id", int(rec.EndpointID))
	n++

	// Build a remote SpanContext so the SDK uses our TraceID and root SpanID,
	// then start the root span as a child of it (which gives it the same TraceID).
	remoteSC := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     rootSpanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	rootCtx := trace.ContextWithRemoteSpanContext(context.Background(), remoteSC)

	rootCtx, rootSpan := tracer.Start(rootCtx, rec.ApiName,
		trace.WithTimestamp(rootStart),
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(o.rootBuf[:n]...),
	)

	// Emit instruction child spans.
	// InstrPCs and InstrDursNs are slices into pooledTraceRec inline arrays —
	// valid only for the duration of this WriteTraceBatch call.
	if len(rec.InstrPCs) > 0 {
		var offsetNs int64 // cumulative offset from root start
		for j, pc := range rec.InstrPCs {
			durNs := int64(rec.InstrDursNs[j])
			instrStart := rootStart.Add(time.Duration(offsetNs))
			instrEnd := instrStart.Add(time.Duration(durNs))
			offsetNs += durNs

			name := o.instrName(schema, pc)

			k := 0
			o.instrBuf[k] = attribute.String("rah.instr_name", name)
			k++
			o.instrBuf[k] = attribute.Int("rah.instr_pc", int(pc))
			k++
			o.instrBuf[k] = attribute.Int64("rah.duration_ns", durNs)
			k++

			_, s := tracer.Start(rootCtx, name,
				trace.WithTimestamp(instrStart),
				trace.WithSpanKind(trace.SpanKindInternal),
				trace.WithAttributes(o.instrBuf[:k]...),
			)
			s.End(trace.WithTimestamp(instrEnd))
		}
	}

	// Emit LLM call child spans.
	for _, llm := range rec.LLMCalls {
		llmStart := rootStart // LLM start offset not stored separately; use root start
		llmEnd := llmStart.Add(time.Duration(llm.DurationNs))

		spanName := "llm:" + llm.ModelName

		k := 0
		o.llmBuf[k] = attribute.String("rah.model", llm.ModelName)
		k++
		o.llmBuf[k] = attribute.Int("rah.llm_status", int(llm.Status))
		k++
		o.llmBuf[k] = attribute.Int("rah.input_tokens", int(llm.InputTokens))
		k++
		o.llmBuf[k] = attribute.Int("rah.output_tokens", int(llm.OutputTokens))
		k++
		o.llmBuf[k] = attribute.Int("rah.cost_micro", int(llm.CostMicro))
		k++
		o.llmBuf[k] = attribute.Int64("rah.duration_ns", llm.DurationNs)
		k++
		o.llmBuf[k] = attribute.Int("rah.instr_pc", int(llm.PC))
		k++
		o.llmBuf[k] = attribute.Int("rah.seq", int(llm.Seq))
		k++

		_, s := tracer.Start(rootCtx, spanName,
			trace.WithTimestamp(llmStart),
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(o.llmBuf[:k]...),
		)
		s.End(trace.WithTimestamp(llmEnd))
	}

	rootSpan.End(trace.WithTimestamp(rootEnd))
}

// instrName resolves a PC to its human-readable name from the schema map.
// Falls back to "instr_N" if the schema is not yet loaded or the PC is unknown.
func (o *OTELObsStore) instrName(schema *map[int16]string, pc int16) string {
	if schema != nil {
		if name, ok := (*schema)[pc]; ok {
			return name
		}
	}
	return fmt.Sprintf("instr_%d", pc)
}

// UpsertInstrSchema builds a PC→name map from rows and swaps it atomically.
// Called at bake time from the main goroutine; the drain goroutine loads the
// pointer atomically on each WriteTraceBatch call and sees either the old or
// new map — always consistent.
func (o *OTELObsStore) UpsertInstrSchema(_ context.Context, rows []InstrSchemaRow) error {
	if len(rows) == 0 {
		return nil
	}
	m := make(map[int16]string, len(rows))
	for _, r := range rows {
		m[r.PC] = r.StepName
	}
	o.schema.Store(&m)
	return nil
}

// WriteAccessLog is a no-op — access logs are handled by the primary store.
func (o *OTELObsStore) WriteAccessLog(_ context.Context, _ []AccessLogRecord) error {
	return nil
}

// WriteMetricSnapshot is a no-op — metrics are handled by the primary store.
func (o *OTELObsStore) WriteMetricSnapshot(_ context.Context, _ MetricSnapshot) error {
	return nil
}

// UpsertVarSchema is a no-op — variable schema is handled by the primary store.
func (o *OTELObsStore) UpsertVarSchema(_ context.Context, _ []VarSchemaRow) error {
	return nil
}

// WritePayloadBatch is a no-op — raw payloads are handled by the primary store.
func (o *OTELObsStore) WritePayloadBatch(_ context.Context, _ []PayloadRecord) error {
	return nil
}

// QueryAccessLog returns nil — reads are handled by the primary store.
func (o *OTELObsStore) QueryAccessLog(_ context.Context, _ AccessLogFilter) ([]AccessLogRecord, error) {
	return nil, nil
}

// QueryMetrics returns nil — reads are handled by the primary store.
func (o *OTELObsStore) QueryMetrics(_ context.Context, _ MetricsFilter) ([]MetricSnapshot, error) {
	return nil, nil
}

// QueryTraces returns nil — reads are handled by the primary store.
func (o *OTELObsStore) QueryTraces(_ context.Context, _ TraceFilter) ([]TraceRecord, error) {
	return nil, nil
}

// QueryInstrSchema returns nil — reads are handled by the primary store.
func (o *OTELObsStore) QueryInstrSchema(_ context.Context, _ string) ([]InstrSchemaRow, error) {
	return nil, nil
}

// QueryVarSchema returns nil — reads are handled by the primary store.
func (o *OTELObsStore) QueryVarSchema(_ context.Context, _ string) ([]VarSchemaRow, error) {
	return nil, nil
}

// QueryPayloads returns nil — reads are handled by the primary store.
func (o *OTELObsStore) QueryPayloads(_ context.Context, _ uint64) ([]PayloadRecord, error) {
	return nil, nil
}

// Close is a no-op — the OTEL SDK lifecycle is managed by main.go.
func (o *OTELObsStore) Close() error { return nil }
