package engine

import (
	"testing"
	"time"
)

func clearCostLimiters() {
	upstreamCostLimiters.Range(func(k, _ any) bool {
		upstreamCostLimiters.Delete(k)
		return true
	})
}

// Tier 1 — Functional Tests

func TestUpstreamCostWindow_UnderLimit(t *testing.T) {
	w := UpstreamCostWindowFromWindow("minute", 1.0)
	if w == nil {
		t.Fatal("UpstreamCostWindowFromWindow returned nil for minute")
	}

	exceeded := w.Record(0.001)
	if exceeded {
		t.Errorf("Record(0.001) with limit=1.0 should return false, got true")
	}
}

func TestUpstreamCostWindow_ExceedsLimit(t *testing.T) {
	w := UpstreamCostWindowFromWindow("minute", 1.0)
	if w == nil {
		t.Fatal("UpstreamCostWindowFromWindow returned nil for minute")
	}

	exceeded1 := w.Record(0.60)
	if exceeded1 {
		t.Errorf("Record(0.60) with limit=1.0 should return false, got true")
	}

	exceeded2 := w.Record(0.60)
	if !exceeded2 {
		t.Errorf("Record(0.60) again (total=1.20) with limit=1.0 should return true, got false")
	}
}

func TestUpstreamCostWindow_NewEpochResets(t *testing.T) {
	// Use per-second window for quick epoch transition
	w := UpstreamCostWindowFromWindow("second", 1.0)
	if w == nil {
		t.Fatal("UpstreamCostWindowFromWindow returned nil for second")
	}

	exceeded1 := w.Record(0.90)
	if exceeded1 {
		t.Errorf("Record(0.90) with limit=1.0 should return false, got true")
	}

	// Wait for epoch to advance (current epoch is 1 second)
	time.Sleep(1100 * time.Millisecond)

	exceeded2 := w.Record(0.90)
	if exceeded2 {
		t.Errorf("Record(0.90) in new epoch should return false, got true")
	}
}

func TestUpstreamCostLimiter_MultiWindow(t *testing.T) {
	clearCostLimiters()

	// Create a limiter with minute and day windows
	minute := UpstreamCostWindowFromWindow("minute", 0.50)
	day := UpstreamCostWindowFromWindow("day", 100.0)
	if minute == nil || day == nil {
		t.Fatal("UpstreamCostWindowFromWindow returned nil")
	}

	windows := []*UpstreamCostWindow{minute, day}
	RegisterModelCostLimit("test-model", windows)

	limiter := GetModelCostLimiter("test-model")
	if limiter == nil {
		t.Fatal("GetModelCostLimiter returned nil after registration")
	}

	// Record 0.60, should exceed minute limit (0.50)
	exceeded := limiter.Record(0.60)
	if !exceeded {
		t.Errorf("Record(0.60) should exceed minute limit 0.50, got false")
	}

	clearCostLimiters()

	// Fresh windows and limiter, record 0.10 (under both limits)
	minute2 := UpstreamCostWindowFromWindow("minute", 0.50)
	day2 := UpstreamCostWindowFromWindow("day", 100.0)
	if minute2 == nil || day2 == nil {
		t.Fatal("UpstreamCostWindowFromWindow returned nil for second set")
	}

	windows2 := []*UpstreamCostWindow{minute2, day2}
	RegisterModelCostLimit("test-model-2", windows2)
	limiter2 := GetModelCostLimiter("test-model-2")
	if limiter2 == nil {
		t.Fatal("GetModelCostLimiter returned nil for second model")
	}

	exceeded2 := limiter2.Record(0.10)
	if exceeded2 {
		t.Errorf("Record(0.10) should not exceed limits, got true")
	}
}

func TestUpstreamCostWindowFromWindow_KnownWindows(t *testing.T) {
	tests := []struct {
		window   string
		expected uint32
	}{
		{"second", 1},
		{"minute", 60},
		{"hour", 3600},
		{"day", 86400},
	}

	for _, tt := range tests {
		w := UpstreamCostWindowFromWindow(tt.window, 1.0)
		if w == nil {
			t.Errorf("UpstreamCostWindowFromWindow(%q) returned nil", tt.window)
			continue
		}
		if w.epochDiv != tt.expected {
			t.Errorf("UpstreamCostWindowFromWindow(%q) epochDiv = %d, want %d", tt.window, w.epochDiv, tt.expected)
		}
	}
}

func TestUpstreamCostWindowFromWindow_Unknown(t *testing.T) {
	w := UpstreamCostWindowFromWindow("week", 1.0)
	if w != nil {
		t.Errorf("UpstreamCostWindowFromWindow(\"week\") should return nil, got %v", w)
	}
}

// Tier 2 — Negative Tests

func TestUpstreamCostWindow_LimitZero(t *testing.T) {
	w := UpstreamCostWindowFromWindow("minute", 0.0)
	if w == nil {
		t.Fatal("UpstreamCostWindowFromWindow returned nil for limit=0")
	}

	exceeded := w.Record(0.001)
	if !exceeded {
		t.Errorf("Record(0.001) with limit=0 should return true, got false")
	}
}

func TestUpstreamCostWindow_ZeroCost(t *testing.T) {
	w := UpstreamCostWindowFromWindow("minute", 1.0)
	if w == nil {
		t.Fatal("UpstreamCostWindowFromWindow returned nil for minute")
	}

	exceeded := w.Record(0.0)
	if exceeded {
		t.Errorf("Record(0.0) with limit=1.0 should return false, got true")
	}
}

func TestGetModelCostLimiter_NotRegistered(t *testing.T) {
	clearCostLimiters()

	limiter := GetModelCostLimiter("nonexistent")
	if limiter != nil {
		t.Errorf("GetModelCostLimiter(\"nonexistent\") should return nil, got %v", limiter)
	}
}

// Tier 3 — Non-functional Tests

func TestUpstreamCostWindow_Concurrent(t *testing.T) {
	w := UpstreamCostWindowFromWindow("minute", 10.0)
	if w == nil {
		t.Fatal("UpstreamCostWindowFromWindow returned nil for minute")
	}

	numGoroutines := 50
	done := make(chan struct{}, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			_ = w.Record(0.001)
			done <- struct{}{}
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify accumulated cost is plausible (50 × $0.001 = $0.05)
	// Load the current slot value to verify accumulation
	old := w.slot.Load()
	accumulatedMicro := uint32(old & 0xFFFFFFFF)
	accumulatedUSD := float64(accumulatedMicro) / 1e6

	// Allow some tolerance for rounding, but should be very close to 0.05
	if accumulatedUSD < 0.04 || accumulatedUSD > 0.06 {
		t.Errorf("Concurrent Record: accumulated cost = $%.6f, want ~$0.05", accumulatedUSD)
	}
}

func TestUpstreamCostWindow_ZeroAllocs(t *testing.T) {
	w := UpstreamCostWindowFromWindow("minute", 1.0)
	if w == nil {
		t.Fatal("UpstreamCostWindowFromWindow returned nil for minute")
	}

	allocs := testing.AllocsPerRun(100, func() {
		_ = w.Record(0.001)
	})

	if allocs > 0 {
		t.Errorf("Record hot path should have 0 allocs, got %v", allocs)
	}
}
