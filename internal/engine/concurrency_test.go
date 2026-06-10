package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"rah/internal/config"
)

// newTestFM returns a minimal FlowManager suitable for concurrency tests.
// Most fields are zero-safe; only those touched by StartController matter.
func newTestFM() *FlowManager {
	return &FlowManager{}
}

// ── Test 1: StartController with Enabled=false ────────────────────────────────

func TestStartControllerDisabled(t *testing.T) {
	fm := newTestFM()

	fm.StartController(context.Background(), config.ConcurrencyConfig{
		Enabled: false,
	})

	if fm.LimiterEnabled() {
		t.Error("LimiterEnabled() should be false when cfg.Enabled=false")
	}

	// liveConfig.Load() must not panic and must return a typed value.
	v := fm.liveConfig.Load()
	if v == nil {
		t.Fatal("liveConfig.Load() returned nil — should always hold a typed value after StartController")
	}
	cfg, ok := v.(config.ConcurrencyConfig)
	if !ok {
		t.Fatalf("liveConfig.Load() is %T, want config.ConcurrencyConfig", v)
	}
	if cfg.Enabled {
		t.Error("stored config should have Enabled=false")
	}

	// Limit should be 0 (never set when disabled).
	if got := fm.Limiter.Limit(); got != 0 {
		t.Errorf("Limiter.Limit() = %d, want 0 when disabled", got)
	}
}

// ── Test 2: StartController with Enabled=true, explicit InitialLimit ─────────

func TestStartControllerEnabled(t *testing.T) {
	fm := newTestFM()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm.StartController(ctx, config.ConcurrencyConfig{
		Enabled:          true,
		InitialLimit:     500,
		TargetOverheadMs: 50,
	})

	if !fm.LimiterEnabled() {
		t.Error("LimiterEnabled() should be true when cfg.Enabled=true")
	}

	if got := fm.Limiter.Limit(); got != 500 {
		t.Errorf("Limiter.Limit() = %d, want 500", got)
	}
}

// ── Test 3: StartController fills defaults when all fields are zero ───────────

func TestStartControllerDefaults(t *testing.T) {
	fm := newTestFM()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm.StartController(ctx, config.ConcurrencyConfig{
		Enabled: true,
		// All other fields left at zero — should be filled with GOMAXPROCS-derived defaults.
	})

	if !fm.LimiterEnabled() {
		t.Error("LimiterEnabled() should be true")
	}

	if got := fm.Limiter.Limit(); got <= 0 {
		t.Errorf("Limiter.Limit() = %d, want > 0 (defaults should have been applied)", got)
	}
}

// ── Test 4: ConcurrencyHandler GET returns valid JSON ─────────────────────────

func TestConcurrencyHandlerGet(t *testing.T) {
	fm := newTestFM()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm.StartController(ctx, config.ConcurrencyConfig{
		Enabled:      true,
		InitialLimit: 200,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/concurrency", nil)
	rec := httptest.NewRecorder()
	fm.ConcurrencyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v — body: %s", err, rec.Body.String())
	}

	for _, field := range []string{"enabled", "limit", "active", "rejected", "adaptive"} {
		if _, ok := body[field]; !ok {
			t.Errorf("response JSON missing field %q", field)
		}
	}
}

// ── Test 5: ConcurrencyHandler PATCH updates limit ───────────────────────────

func TestConcurrencyHandlerPatch(t *testing.T) {
	fm := newTestFM()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm.StartController(ctx, config.ConcurrencyConfig{
		Enabled:      true,
		InitialLimit: 1000,
	})

	body := strings.NewReader(`{"limit":777}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/concurrency", body)
	rec := httptest.NewRecorder()
	fm.ConcurrencyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v — body: %s", err, rec.Body.String())
	}

	// JSON numbers unmarshal as float64.
	if got, ok := resp["limit"].(float64); !ok || int64(got) != 777 {
		t.Errorf(`response["limit"] = %v, want 777`, resp["limit"])
	}

	if got := fm.Limiter.Limit(); got != 777 {
		t.Errorf("Limiter.Limit() = %d, want 777 after PATCH", got)
	}
}

// ── Test 6: ConcurrencyHandler PATCH with enabled=false disables limiter ─────

func TestConcurrencyHandlerPatchDisable(t *testing.T) {
	fm := newTestFM()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fm.StartController(ctx, config.ConcurrencyConfig{
		Enabled:      true,
		InitialLimit: 500,
	})

	if !fm.LimiterEnabled() {
		t.Fatal("precondition: limiter should be enabled before PATCH")
	}

	body := strings.NewReader(`{"enabled":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/concurrency", body)
	rec := httptest.NewRecorder()
	fm.ConcurrencyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}

	if fm.LimiterEnabled() {
		t.Error("LimiterEnabled() should return false after PATCH {\"enabled\":false}")
	}
}

// ── Test 7: ConcurrencyHandler PATCH with invalid JSON returns 400 ───────────

func TestConcurrencyHandlerPatchInvalidJSON(t *testing.T) {
	fm := newTestFM()
	// StartController not required — handler reads liveConfig which may be nil,
	// but invalid JSON is rejected before any config read.
	// Store a typed zero so liveConfig.Load() is safe if handler ever reaches it.
	fm.liveConfig.Store(config.ConcurrencyConfig{})

	body := strings.NewReader(`{invalid`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/concurrency", body)
	rec := httptest.NewRecorder()
	fm.ConcurrencyHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", rec.Code)
	}
}

// ── Test 8: LimiterEnabled race detector test ─────────────────────────────────

func TestLimiterEnabledRace(t *testing.T) {
	fm := newTestFM()

	var wg sync.WaitGroup
	wg.Add(2)

	// Reader goroutine.
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = fm.LimiterEnabled()
		}
	}()

	// Writer goroutine alternates true/false.
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			fm.limiterEnabled.Store(i%2 == 0)
		}
	}()

	wg.Wait()
	// Success = no data race detected, no panic.
}

// ── Test 9: liveConfig race detector test ────────────────────────────────────

func TestLiveConfigRace(t *testing.T) {
	fm := newTestFM()
	// Seed with a typed value so the reader's type assertion doesn't panic
	// before the first writer store.
	fm.liveConfig.Store(config.ConcurrencyConfig{})

	var wg sync.WaitGroup
	wg.Add(2)

	// Reader goroutine.
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = fm.liveConfig.Load().(config.ConcurrencyConfig)
		}
	}()

	// Writer goroutine stores different configs.
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			fm.liveConfig.Store(config.ConcurrencyConfig{
				Enabled:      i%2 == 0,
				InitialLimit: int64(i * 10),
			})
		}
	}()

	wg.Wait()
	// Success = no data race detected, no panic.
}
