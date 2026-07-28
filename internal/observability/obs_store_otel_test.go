package observability

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// testSpanExporter collects exported spans for assertions.
type testSpanExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *testSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	e.spans = append(e.spans, spans...)
	e.mu.Unlock()
	return nil
}

func (e *testSpanExporter) Shutdown(_ context.Context) error { return nil }

func (e *testSpanExporter) reset() {
	e.mu.Lock()
	e.spans = e.spans[:0]
	e.mu.Unlock()
}

func (e *testSpanExporter) snapshot() []sdktrace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]sdktrace.ReadOnlySpan, len(e.spans))
	copy(result, e.spans)
	return result
}

// newTestTracerProvider creates a TracerProvider with a synchronous exporter.
func newTestTracerProvider(exp sdktrace.SpanExporter) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
	)
}

// findAttr searches for an attribute by key and returns its value.
func findAttr(attrs []attribute.KeyValue, key string) (attribute.Value, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestOTELObsStore_WriteTraceBatch_RootSpan(t *testing.T) {
	// Save and restore global tracer provider
	prevProvider := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prevProvider) })

	// Set up test tracer provider with exporter
	exporter := &testSpanExporter{}
	tp := newTestTracerProvider(exporter)
	otel.SetTracerProvider(tp)

	// Create store
	store := newOTELObsStore()

	// Build a TraceRecord
	rec := TraceRecord{
		TraceID:      12345,
		Timestamp:    1700000000,
		DurationNs:   50_000_000,
		ApiName:      "test-api",
		Method:       "POST",
		Status:       200,
		TenantID:     7,
		GatewayNs:    5_000_000,
		UpstreamNs:   40_000_000,
		ReqBytes:     256,
		ResBytes:     1024,
		ApiVersionID: 1,
		EndpointID:   2,
		UpstreamCalls: 1,
	}

	// Write trace batch
	err := store.WriteTraceBatch(context.Background(), []TraceRecord{rec})
	if err != nil {
		t.Fatalf("WriteTraceBatch failed: %v", err)
	}

	// Get exported spans
	spans := exporter.snapshot()
	if len(spans) < 1 {
		t.Fatalf("expected at least 1 span, got %d", len(spans))
	}

	// Check root span
	rootSpan := spans[0]
	if rootSpan.Name() != "test-api" {
		t.Errorf("root span Name: want %q, got %q", "test-api", rootSpan.Name())
	}

	// Check attributes
	attrs := rootSpan.Attributes()
	if val, ok := findAttr(attrs, "rah.api_name"); ok {
		if val.AsString() != "test-api" {
			t.Errorf("rah.api_name: want %q, got %q", "test-api", val.AsString())
		}
	} else {
		t.Error("rah.api_name attribute not found")
	}

	if val, ok := findAttr(attrs, "http.status_code"); ok {
		if val.AsInt64() != 200 {
			t.Errorf("http.status_code: want 200, got %d", val.AsInt64())
		}
	} else {
		t.Error("http.status_code attribute not found")
	}
}

func TestOTELObsStore_WriteTraceBatch_InstrChildren(t *testing.T) {
	// Save and restore global tracer provider
	prevProvider := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prevProvider) })

	// Set up test tracer provider with exporter
	exporter := &testSpanExporter{}
	tp := newTestTracerProvider(exporter)
	otel.SetTracerProvider(tp)

	// Create store
	store := newOTELObsStore()

	// Build a TraceRecord with instruction children
	rec := TraceRecord{
		TraceID:      12345,
		Timestamp:    1700000000,
		DurationNs:   50_000_000,
		ApiName:      "test-api",
		Method:       "POST",
		Status:       200,
		TenantID:     7,
		GatewayNs:    5_000_000,
		UpstreamNs:   40_000_000,
		ReqBytes:     256,
		ResBytes:     1024,
		ApiVersionID: 1,
		EndpointID:   2,
		UpstreamCalls: 1,
		InstrPCs:     []int16{1, 2, 3},
		InstrDursNs:  []int32{1_000_000, 2_000_000, 3_000_000},
	}

	// Write trace batch
	err := store.WriteTraceBatch(context.Background(), []TraceRecord{rec})
	if err != nil {
		t.Fatalf("WriteTraceBatch failed: %v", err)
	}

	// Get exported spans
	spans := exporter.snapshot()
	if len(spans) != 4 {
		t.Fatalf("expected 4 spans (1 root + 3 instr), got %d", len(spans))
	}

	// Find spans by PC value — they may be exported in any order
	pcMap := make(map[int64]bool)
	for _, span := range spans {
		attrs := span.Attributes()
		if val, ok := findAttr(attrs, "rah.instr_pc"); ok {
			pcMap[val.AsInt64()] = true
		}
	}

	// Check that PCs 1, 2, 3 are all present
	if !pcMap[1] || !pcMap[2] || !pcMap[3] {
		t.Errorf("expected PCs 1, 2, 3 in child spans, got %v", pcMap)
	}
}

func TestOTELObsStore_WriteTraceBatch_LLMChildren(t *testing.T) {
	// Save and restore global tracer provider
	prevProvider := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prevProvider) })

	// Set up test tracer provider with exporter
	exporter := &testSpanExporter{}
	tp := newTestTracerProvider(exporter)
	otel.SetTracerProvider(tp)

	// Create store
	store := newOTELObsStore()

	// Build a TraceRecord with LLM call
	rec := TraceRecord{
		TraceID:       12345,
		Timestamp:     1700000000,
		DurationNs:    50_000_000,
		ApiName:       "test-api",
		Method:        "POST",
		Status:        200,
		TenantID:      7,
		GatewayNs:     5_000_000,
		UpstreamNs:    40_000_000,
		ReqBytes:      256,
		ResBytes:      1024,
		ApiVersionID:  1,
		EndpointID:    2,
		UpstreamCalls: 1,
		LLMCalls: []LLMCallRow{
			{
				PC:           5,
				Seq:          0,
				ModelName:    "gpt-4o",
				Status:       200,
				InputTokens:  100,
				OutputTokens: 50,
				CostMicro:    42,
				DurationNs:   30_000_000,
			},
		},
	}

	// Write trace batch
	err := store.WriteTraceBatch(context.Background(), []TraceRecord{rec})
	if err != nil {
		t.Fatalf("WriteTraceBatch failed: %v", err)
	}

	// Get exported spans
	spans := exporter.snapshot()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans (1 root + 1 LLM), got %d", len(spans))
	}

	// Find the LLM span by looking for the one with "llm:" prefix in the name
	var llmSpan sdktrace.ReadOnlySpan
	found := false
	for _, span := range spans {
		if len(span.Name()) > 4 && span.Name()[:4] == "llm:" {
			llmSpan = span
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected LLM span with name starting with 'llm:', got spans: %v",
			func() []string {
				var names []string
				for _, s := range spans {
					names = append(names, s.Name())
				}
				return names
			}())
	}

	if llmSpan.Name() != "llm:gpt-4o" {
		t.Errorf("LLM span Name: want %q, got %q", "llm:gpt-4o", llmSpan.Name())
	}

	// Check LLM attributes
	attrs := llmSpan.Attributes()
	if val, ok := findAttr(attrs, "rah.model"); ok {
		if val.AsString() != "gpt-4o" {
			t.Errorf("rah.model: want %q, got %q", "gpt-4o", val.AsString())
		}
	} else {
		t.Error("rah.model attribute not found")
	}
}

func TestOTELObsStore_UpsertInstrSchema_NamesResolved(t *testing.T) {
	// Save and restore global tracer provider
	prevProvider := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prevProvider) })

	// Set up test tracer provider with exporter
	exporter := &testSpanExporter{}
	tp := newTestTracerProvider(exporter)
	otel.SetTracerProvider(tp)

	// Create store
	store := newOTELObsStore()

	// Upsert instruction schema
	schemaRows := []InstrSchemaRow{
		{PC: 1, StepName: "validate_token"},
		{PC: 2, StepName: "rate_limit"},
	}
	err := store.UpsertInstrSchema(context.Background(), schemaRows)
	if err != nil {
		t.Fatalf("UpsertInstrSchema failed: %v", err)
	}

	// Build a TraceRecord with instructions matching the schema
	rec := TraceRecord{
		TraceID:      12345,
		Timestamp:    1700000000,
		DurationNs:   50_000_000,
		ApiName:      "test-api",
		Method:       "POST",
		Status:       200,
		TenantID:     7,
		GatewayNs:    5_000_000,
		UpstreamNs:   40_000_000,
		ReqBytes:     256,
		ResBytes:     1024,
		ApiVersionID: 1,
		EndpointID:   2,
		UpstreamCalls: 1,
		InstrPCs:     []int16{1, 2},
		InstrDursNs:  []int32{1_000_000, 2_000_000},
	}

	// Write trace batch
	err = store.WriteTraceBatch(context.Background(), []TraceRecord{rec})
	if err != nil {
		t.Fatalf("WriteTraceBatch failed: %v", err)
	}

	// Get exported spans
	spans := exporter.snapshot()
	if len(spans) != 3 {
		t.Fatalf("expected 3 spans (1 root + 2 instr), got %d", len(spans))
	}

	// Collect instruction span names by PC
	nameByPC := make(map[int64]string)
	for _, span := range spans {
		attrs := span.Attributes()
		if val, ok := findAttr(attrs, "rah.instr_pc"); ok {
			nameByPC[val.AsInt64()] = span.Name()
		}
	}

	// Check instruction span names are resolved from schema
	if name, ok := nameByPC[1]; !ok || name != "validate_token" {
		t.Errorf("instr PC 1: want Name %q, got %q", "validate_token", name)
	}
	if name, ok := nameByPC[2]; !ok || name != "rate_limit" {
		t.Errorf("instr PC 2: want Name %q, got %q", "rate_limit", name)
	}
}

func TestOTELObsStore_NoopReadMethods(t *testing.T) {
	store := newOTELObsStore()
	ctx := context.Background()

	// QueryTraces
	traces, err := store.QueryTraces(ctx, TraceFilter{})
	if traces != nil || err != nil {
		t.Errorf("QueryTraces: want (nil, nil), got (%v, %v)", traces, err)
	}

	// QueryAccessLog
	accessLogs, err := store.QueryAccessLog(ctx, AccessLogFilter{})
	if accessLogs != nil || err != nil {
		t.Errorf("QueryAccessLog: want (nil, nil), got (%v, %v)", accessLogs, err)
	}

	// QueryMetrics
	metrics, err := store.QueryMetrics(ctx, MetricsFilter{})
	if metrics != nil || err != nil {
		t.Errorf("QueryMetrics: want (nil, nil), got (%v, %v)", metrics, err)
	}

	// QueryInstrSchema
	instrSchema, err := store.QueryInstrSchema(ctx, "test-api")
	if instrSchema != nil || err != nil {
		t.Errorf("QueryInstrSchema: want (nil, nil), got (%v, %v)", instrSchema, err)
	}

	// QueryVarSchema
	varSchema, err := store.QueryVarSchema(ctx, "test-api")
	if varSchema != nil || err != nil {
		t.Errorf("QueryVarSchema: want (nil, nil), got (%v, %v)", varSchema, err)
	}

	// QueryPayloads
	payloads, err := store.QueryPayloads(ctx, 12345)
	if payloads != nil || err != nil {
		t.Errorf("QueryPayloads: want (nil, nil), got (%v, %v)", payloads, err)
	}
}

func TestOTELObsStore_NoopWriteMethods(t *testing.T) {
	store := newOTELObsStore()
	ctx := context.Background()

	// WriteAccessLog
	err := store.WriteAccessLog(ctx, []AccessLogRecord{})
	if err != nil {
		t.Errorf("WriteAccessLog: want nil, got %v", err)
	}

	// WriteMetricSnapshot
	err = store.WriteMetricSnapshot(ctx, MetricSnapshot{})
	if err != nil {
		t.Errorf("WriteMetricSnapshot: want nil, got %v", err)
	}

	// UpsertVarSchema
	err = store.UpsertVarSchema(ctx, []VarSchemaRow{})
	if err != nil {
		t.Errorf("UpsertVarSchema: want nil, got %v", err)
	}

	// WritePayloadBatch
	err = store.WritePayloadBatch(ctx, []PayloadRecord{})
	if err != nil {
		t.Errorf("WritePayloadBatch: want nil, got %v", err)
	}

	// Close
	err = store.Close()
	if err != nil {
		t.Errorf("Close: want nil, got %v", err)
	}
}
