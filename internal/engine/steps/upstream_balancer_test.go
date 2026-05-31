package steps

import (
	"net/http"
	"net/http/httptest"
	"rah/internal/engine"
	"rah/internal/rctx"
	"sync"
	"sync/atomic"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestSelectUpstreamURLRoundRobinCSV(t *testing.T) {
	resetUpstreamBalancersForTest()
	flowInput := map[string]string{"http.upstream_strategy": "round_robin"}

	first, err := selectUpstreamURL("https://a.internal,https://b.internal", flowInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, _ := selectUpstreamURL("https://a.internal,https://b.internal", flowInput)
	third, _ := selectUpstreamURL("https://a.internal,https://b.internal", flowInput)

	if first != "https://a.internal" || second != "https://b.internal" || third != "https://a.internal" {
		t.Fatalf("unexpected round-robin sequence: %s %s %s", first, second, third)
	}
}

func TestSelectUpstreamURLWeightedFromJSON(t *testing.T) {
	resetUpstreamBalancersForTest()
	flowInput := map[string]string{"http.upstream_strategy": "weighted_round_robin"}

	raw := `[{"url":"https://a.internal","weight":2},{"url":"https://b.internal","weight":1}]`
	got := make([]string, 3)
	for i := range got {
		u, err := selectUpstreamURL(raw, flowInput)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got[i] = u
	}

	want := []string{"https://a.internal", "https://a.internal", "https://b.internal"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d want %s got %s", i, want[i], got[i])
		}
	}
}

func TestSelectUpstreamURLSkipsUnhealthyInstances(t *testing.T) {
	resetUpstreamBalancersForTest()
	flowInput := map[string]string{"http.upstream_strategy": "round_robin"}
	raw := `[{"url":"https://a.internal","healthy":false},{"url":"https://b.internal","healthy":true}]`

	next, err := selectUpstreamURL(raw, flowInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != "https://b.internal" {
		t.Fatalf("expected healthy upstream, got %s", next)
	}
}

func TestUpstreamStatsAreTracked(t *testing.T) {
	resetUpstreamBalancersForTest()
	flowInput := map[string]string{"http.upstream_strategy": "round_robin"}
	_, _ = selectUpstreamURL("https://a.internal,https://b.internal", flowInput)
	_, _ = selectUpstreamURL("https://a.internal,https://b.internal", flowInput)

	stats := readBalancerStats()
	entry, ok := stats["https://a.internal,https://b.internal|round_robin"]
	if !ok {
		t.Fatalf("expected balancer stats entry")
	}
	if entry.Selections != 2 {
		t.Fatalf("expected 2 selections, got %d", entry.Selections)
	}
}

func TestHttpActionUsesMultipleUpstreams(t *testing.T) {
	resetHTTPConfigForTest()
	resetUpstreamBalancersForTest()

	var aHits int32
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&aHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer a.Close()

	var bHits int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&bHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer b.Close()

	req, _ := http.NewRequest("GET", a.URL, nil)
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 20),
		Request:   req,
	}
	ctx.ByteSlots[0] = []byte(a.URL + "," + b.URL)
	state := &engine.ExecutionState{PC: 0}

	step := HttpAction(0, "", 0, "", 0, map[string]string{"http.upstream_strategy": "round_robin"})

	if next := step.Action(ctx, state); next != 1 {
		t.Fatalf("expected next=1, got %d", next)
	}
	if next := step.Action(ctx, state); next != 1 {
		t.Fatalf("expected next=1, got %d", next)
	}

	if atomic.LoadInt32(&aHits) != 1 || atomic.LoadInt32(&bHits) != 1 {
		t.Fatalf("expected one request to each upstream, got a=%d b=%d", aHits, bHits)
	}
}

func TestSelectUpstreamURLConcurrentSafety(t *testing.T) {
	resetUpstreamBalancersForTest()
	flowInput := map[string]string{"http.upstream_strategy": "round_robin"}

	wg := sync.WaitGroup{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := selectUpstreamURL("https://a.internal,https://b.internal", flowInput)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	stats := readBalancerStats()["https://a.internal,https://b.internal|round_robin"]
	if stats.Selections != 20 {
		t.Fatalf("expected 20 selections, got %d", stats.Selections)
	}
}
