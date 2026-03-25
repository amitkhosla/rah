package observability

import (
	"testing"
	"time"
)

func TestTelemetryRecordsInstructionAndRequest(t *testing.T) {
	tel := New(Config{Enabled: true, TraceMode: true, SampleRate: 1.0, MaxEvents: 8, MaxTraces: 8, InstructionTimingEnabled: true, AlwaysExportSummary: true})
	if !tel.ShouldTrace() {
		t.Fatalf("expected ShouldTrace true")
	}
	trace := tel.StartRequest(7, 11, "GET", "/v1/test")
	tel.RecordInstruction("HTTP_CALL", 3*time.Millisecond)
	tel.RecordUpstream("api.acme.com", 2*time.Millisecond, 10, 20)
	tel.AppendInstructionEvent(&trace, InstructionEvent{Name: "HTTP_CALL", DurationNs: int64(time.Millisecond)})
	tel.AppendInstructionEvent(&trace, InstructionEvent{Name: "HTTP_CALL", DurationNs: int64(time.Millisecond)})
	if trace.Instructions[0].Seq != 1 || trace.Instructions[1].Seq != 2 {
		t.Fatalf("instruction seq should increment")
	}
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
