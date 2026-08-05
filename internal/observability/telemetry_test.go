package observability

import (
	"testing"
	"time"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

func TestTelemetryRecordsInstructionAndRequest(t *testing.T) {
	tel := New(Config{Enabled: true, TraceMode: true, SampleRate: 1.0, MaxEvents: 8, MaxTraces: 8, InstructionTimingEnabled: true, AlwaysExportSummary: true})
	if !tel.ShouldTrace() {
		t.Fatalf("expected ShouldTrace true")
	}
	trace := tel.StartRequest(7, 0, 11, "GET", "/v1/test")
	tel.RecordInstrTiming("HTTP_CALL", int64(3*time.Millisecond))
	tel.RecordUpstream("api.acme.com", 2*time.Millisecond, 10, 20)
	tel.FinishRequest(&trace, 502, 10*time.Millisecond, 8*time.Millisecond, 2*time.Millisecond, 1, 40, 10, 20)

	s := tel.Snapshot(10)
	metrics := s["metrics"].(GatewayMetrics)
	if metrics.RequestsTotal != 1 {
		t.Fatalf("requests total = %d, want 1", metrics.RequestsTotal)
	}
	if metrics.ClientBytesSentTotal != 40 || metrics.UpstreamBytesTxTotal != 10 || metrics.UpstreamBytesRxTotal != 20 {
		t.Fatalf("byte totals not recorded")
	}
	if len(metrics.InstructionTopSlow) == 0 || metrics.InstructionTopSlow[0].Name != "HTTP_CALL" {
		t.Fatalf("instruction top slow not recorded")
	}
}

func TestUpdateConfig(t *testing.T) {
	tel := New(Config{Enabled: true})
	traceMode := true
	sample := 0.25
	instr := true
	phase := true
	always := false
	info := true
	tel.UpdateConfig(&traceMode, &sample, &instr, &phase, &always, &info, []string{"status", "duration_ns"})
	if !tel.traceMode.Load() || !tel.instrEnabled.Load() || !tel.phaseEnabled.Load() {
		t.Fatalf("expected flags updated")
	}
	if tel.alwaysExport.Load() {
		t.Fatalf("alwaysExport should be false")
	}
}

func TestLogUpstreamSuppressedAtErrorLevel(t *testing.T) {
	tel := New(Config{Enabled: true, ExportQueueSize: 16})
	tel.Stop() // stop workers so channel state is stable when we inspect it
	original := gatewaylog.Default.Level()
	gatewaylog.Default.SetLevel(gatewaylog.ERROR)
	t.Cleanup(func() { gatewaylog.Default.SetLevel(original) })
	tel.LogUpstream(1, 1, UpstreamEvent{})
	if len(tel.upstreamCh) != 0 {
		t.Fatalf("expected upstreamCh len 0, got %d", len(tel.upstreamCh))
	}
}

func TestLogUpstreamDeliveredAtInfoLevel(t *testing.T) {
	tel := New(Config{Enabled: true, ExportQueueSize: 16})
	tel.Stop() // stop workers so the item stays in the channel when we inspect it
	original := gatewaylog.Default.Level()
	gatewaylog.Default.SetLevel(gatewaylog.INFO)
	t.Cleanup(func() { gatewaylog.Default.SetLevel(original) })
	tel.LogUpstream(1, 1, UpstreamEvent{})
	if len(tel.upstreamCh) != 1 {
		t.Fatalf("expected upstreamCh len 1, got %d", len(tel.upstreamCh))
	}
}

func TestLogUpstreamSuppressedWhenTelemetryDisabled(t *testing.T) {
	tel := New(Config{Enabled: false, ExportQueueSize: 16})
	tel.Stop() // stop workers so channel state is stable when we inspect it
	original := gatewaylog.Default.Level()
	gatewaylog.Default.SetLevel(gatewaylog.INFO)
	t.Cleanup(func() { gatewaylog.Default.SetLevel(original) })
	tel.LogUpstream(1, 1, UpstreamEvent{})
	if len(tel.upstreamCh) != 0 {
		t.Fatalf("expected upstreamCh len 0, got %d", len(tel.upstreamCh))
	}
}
