package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// LoadTestRequest is the body of POST /test/load/run.
type LoadTestRequest struct {
	FlowName  string     `json:"flow_name"`
	Mode      string     `json:"mode"`        // "draft" | "published"
	Input     TestInput  `json:"input"`
	TargetTPS int        `json:"target_tps"`  // requests per second to send
	DurationS int        `json:"duration_s"`  // how long to run (seconds); max 300
	Mocks     []StepMock `json:"mocks,omitempty"`
}

// LoadTestResult is returned by POST /test/load/run.
type LoadTestResult struct {
	FlowName      string       `json:"flow_name"`
	TargetTPS     int          `json:"target_tps"`
	ActualTPS     float64      `json:"actual_tps"`      // requests completed / duration
	DurationS     float64      `json:"duration_s"`
	TotalRequests int          `json:"total_requests"`
	Errors        int          `json:"errors"`          // non-2xx responses
	Latency       LatencyStats `json:"latency"`
	RahOverheadMs float64      `json:"rah_overhead_ms"` // p50 of total - external
	TPSCeiling    int          `json:"tps_ceiling"`     // estimated max TPS from profile
	FlowProfile   *FlowProfile `json:"flow_profile,omitempty"`
}

// LatencyStats holds percentile latency breakdown.
type LatencyStats struct {
	P50Ms  float64 `json:"p50_ms"`
	P95Ms  float64 `json:"p95_ms"`
	P99Ms  float64 `json:"p99_ms"`
	MaxMs  float64 `json:"max_ms"`
	MeanMs float64 `json:"mean_ms"`
}

// LoadTestHandler handles POST /test/load/run.
func (h *TestHandler) LoadTestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req LoadTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Validate required fields.
	if req.FlowName == "" {
		writeError(w, http.StatusBadRequest, "flow_name is required")
		return
	}
	if req.TargetTPS < 1 || req.TargetTPS > 1000 {
		writeError(w, http.StatusBadRequest, "target_tps must be between 1 and 1000")
		return
	}
	if req.DurationS == 0 {
		req.DurationS = 10
	}
	if req.DurationS < 1 || req.DurationS > 300 {
		writeError(w, http.StatusBadRequest, "duration_s must be between 1 and 300")
		return
	}

	// Resolve instruction table.
	table, err := h.resolveTable(req.Mode, req.FlowName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if table == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("flow %q not found", req.FlowName))
		return
	}

	// Look up flow profile (best-effort; nil if not found).
	var profilePtr *FlowProfile
	var tpsCeiling int
	if h.ms != nil && h.ms.Compiler != nil {
		if p, ok := h.ms.Compiler.FlowProfiles[req.FlowName]; ok {
			profileCopy := p
			profilePtr = &profileCopy
			if p.EstimatedMinLatencyNs > 0 {
				tpsCeiling = int(1e9 / float64(p.EstimatedMinLatencyNs))
			}
		}
	}

	// Run the load test.
	latencies, errorCount := runLoadTest(req, table)

	actualDuration := float64(req.DurationS)
	totalRequests := len(latencies)
	var actualTPS float64
	if actualDuration > 0 {
		actualTPS = float64(totalRequests) / actualDuration
	}

	// Compute latency stats.
	stats := computeLatencyStats(latencies)

	result := LoadTestResult{
		FlowName:      req.FlowName,
		TargetTPS:     req.TargetTPS,
		ActualTPS:     actualTPS,
		DurationS:     actualDuration,
		TotalRequests: totalRequests,
		Errors:        errorCount,
		Latency:       stats,
		RahOverheadMs: stats.P50Ms,
		TPSCeiling:    tpsCeiling,
		FlowProfile:   profilePtr,
	}

	writeJSON(w, http.StatusOK, result)
}

// runLoadTest executes the flow at the requested TPS for the requested duration.
// Returns collected latencies (nanoseconds) and error count.
func runLoadTest(req LoadTestRequest, table []engine.Instruction) ([]int64, int) {
	capacity := req.TargetTPS * req.DurationS
	if capacity < 1 {
		capacity = 1
	}

	latencies := make([]int64, 0, capacity)
	var mu sync.Mutex
	errorCount := 0

	// Semaphore to cap concurrency.
	maxConcurrency := req.TargetTPS * 2
	if maxConcurrency > 200 {
		maxConcurrency = 200
	}
	sem := make(chan struct{}, maxConcurrency)

	// Ticker to pace requests at the desired TPS.
	interval := time.Second / time.Duration(req.TargetTPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	deadline := time.Now().Add(time.Duration(req.DurationS) * time.Second)

	var wg sync.WaitGroup

	method := req.Input.Method
	if method == "" {
		method = http.MethodGet
	}

	for time.Now().Before(deadline) {
		select {
		case <-ticker.C:
			// Acquire semaphore slot (non-blocking; skip if saturated).
			select {
			case sem <- struct{}{}:
			default:
				// At capacity — skip this tick rather than queue unboundedly.
				continue
			}

			wg.Add(1)
			runID := fmt.Sprintf("%d", time.Now().UnixNano())
			go func(id string) {
				defer wg.Done()
				defer func() { <-sem }()

				ctx := &rctx.Context{}
				ctx.InitSlots()
				ctx.TestMode = true
				ctx.TestRunID = id
				ctx.Method = []byte(method)
				ctx.Path = []byte(req.Input.Path)
				ctx.TenantKey = req.Input.TenantKey

				mock := newMockResponseWriter()
				ctx.Writer = mock

				if req.Input.Body != "" {
					ctx.RequestBuffer = []byte(req.Input.Body)
					ctx.IsBuffered = true
				}

				start := time.Now()
				engine.Execute(ctx, table, 0)
				elapsed := time.Since(start).Nanoseconds()

				// Determine effective status for error counting.
				status := ctx.ResponseStatus
				if status == 0 {
					status = mock.status
				}
				if status == 0 {
					status = http.StatusOK
				}

				isError := status < 200 || status >= 300

				mu.Lock()
				latencies = append(latencies, elapsed)
				if isError {
					errorCount++
				}
				mu.Unlock()
			}(runID)
		}
	}

	// Drain all in-flight goroutines before returning.
	wg.Wait()

	return latencies, errorCount
}

// computeLatencyStats calculates percentile and mean latency from a nanosecond slice.
// Returns zero-value LatencyStats if the slice is empty.
func computeLatencyStats(latencies []int64) LatencyStats {
	if len(latencies) == 0 {
		return LatencyStats{}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	n := len(latencies)
	toMs := func(ns int64) float64 { return float64(ns) / 1e6 }

	var sum int64
	for _, v := range latencies {
		sum += v
	}

	return LatencyStats{
		P50Ms:  toMs(latencies[n*50/100]),
		P95Ms:  toMs(latencies[n*95/100]),
		P99Ms:  toMs(latencies[n*99/100]),
		MaxMs:  toMs(latencies[n-1]),
		MeanMs: toMs(sum / int64(n)),
	}
}
