package steps

import (
	"net/http"
	"net/http/httptest"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func resetHTTPConfigForTest() {
	cfgOnce = sync.Once{}
	cachedDefaultHTTPConfig = httpClientConfig{}
	defaultHTTPClient = nil
	perTargetClientCache = sync.Map{}
	perTargetClientCacheSize.Store(0)
}

func TestHttpActionRetriesTransientStatuses(t *testing.T) {
	resetHTTPConfigForTest()

	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
		Request:   req,
	}
	ctx.ByteSlots[0] = []byte(ts.URL)
	state := &engine.ExecutionState{PC: 7}

	flowInput := map[string]string{
		"http.retry_base_backoff_ms": "1",
		"http.retry_max_backoff_ms":  "1",
		"http.retry_jitter_ms":       "0",
	}

	next := HttpAction(0, "", 0, "status >= 500", 3, flowInput).Action(ctx, state)

	if next != 8 {
		t.Fatalf("expected next pc 8, got %d", next)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("expected 2 attempts, got %d", hits)
	}
	if ctx.ResponseStatus != http.StatusOK {
		t.Fatalf("expected final status 200, got %d", ctx.ResponseStatus)
	}
}

func TestHttpActionDoesNotRetryBadResponse(t *testing.T) {
	resetHTTPConfigForTest()

	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
		Request:   req,
	}
	ctx.ByteSlots[0] = []byte(ts.URL)
	state := &engine.ExecutionState{PC: 2}

	flowInput := map[string]string{
		"http.retry_base_backoff_ms": "1",
		"http.retry_max_backoff_ms":  "1",
		"http.retry_jitter_ms":       "0",
	}

	next := HttpAction(0, "", 0, "status >= 500", 3, flowInput).Action(ctx, state)

	if next != 3 {
		t.Fatalf("expected next pc 3, got %d", next)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected 1 attempt for bad response, got %d", hits)
	}
	if ctx.ResponseStatus != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", ctx.ResponseStatus)
	}
}

func TestHttpActionUsesStaticURLWhenNoSlot(t *testing.T) {
	resetHTTPConfigForTest()

	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
		Request:   req,
	}
	state := &engine.ExecutionState{PC: 5}

	next := HttpAction(-1, ts.URL, 0, "", 0, nil).Action(ctx, state)
	if next != 6 {
		t.Fatalf("expected next pc 6, got %d", next)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected one call, got %d", hits)
	}
}

func TestResolveHTTPConfigForTargetUsesFlowInputOverride(t *testing.T) {
	resetHTTPConfigForTest()

	cfg := resolveHTTPConfigForTarget("api.example.com:443", map[string]string{"http.max_idle_conns": "150"})
	if cfg.MaxIdleConns != 150 {
		t.Fatalf("expected flow input override to win, got %d", cfg.MaxIdleConns)
	}
}

func TestDialTimeoutConfiguredInMs(t *testing.T) {
	resetHTTPConfigForTest()
	cfg := resolveHTTPConfigForTarget("", map[string]string{"http.dial_timeout_ms": "1234"})
	if cfg.DialTimeout != 1234*time.Millisecond {
		t.Fatalf("expected 1234ms dial timeout, got %s", cfg.DialTimeout)
	}
}
