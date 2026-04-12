package observability

import (
	"fmt"
	"net/http"
)

// PrometheusHandler serves GET /metrics in Prometheus text exposition format (0.0.4).
// Reads from the in-memory Telemetry snapshot — no store query needed.
// Pure stdlib, no SDK dependency.
func PrometheusHandler(obs *Telemetry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		snap := obs.Snapshot(0)
		var m GatewayMetrics
		if mv, ok := snap["metrics"].(GatewayMetrics); ok {
			m = mv
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)

		// Total requests processed.
		fmt.Fprintf(w, "# HELP rah_requests_total Total HTTP requests processed\n")
		fmt.Fprintf(w, "# TYPE rah_requests_total counter\n")
		fmt.Fprintf(w, "rah_requests_total %d\n", m.RequestsTotal)
		fmt.Fprintf(w, "\n")

		// Total 5xx responses.
		fmt.Fprintf(w, "# HELP rah_requests_5xx_total Total 5xx responses\n")
		fmt.Fprintf(w, "# TYPE rah_requests_5xx_total counter\n")
		fmt.Fprintf(w, "rah_requests_5xx_total %d\n", m.Requests5xx)
		fmt.Fprintf(w, "\n")

		// Sum of gateway latency in milliseconds (converted from nanoseconds).
		gatewayMs := float64(m.GatewayLatencyTotalNs) / 1e6
		fmt.Fprintf(w, "# HELP rah_gateway_latency_ms_total Sum of gateway latency in milliseconds\n")
		fmt.Fprintf(w, "# TYPE rah_gateway_latency_ms_total counter\n")
		fmt.Fprintf(w, "rah_gateway_latency_ms_total %.1f\n", gatewayMs)
		fmt.Fprintf(w, "\n")

		// Sum of upstream latency in milliseconds (converted from nanoseconds).
		upstreamMs := float64(m.UpstreamLatencyTotalNs) / 1e6
		fmt.Fprintf(w, "# HELP rah_upstream_latency_ms_total Sum of upstream latency in milliseconds\n")
		fmt.Fprintf(w, "# TYPE rah_upstream_latency_ms_total counter\n")
		fmt.Fprintf(w, "rah_upstream_latency_ms_total %.1f\n", upstreamMs)
		fmt.Fprintf(w, "\n")

		// Total bytes sent to clients.
		fmt.Fprintf(w, "# HELP rah_bytes_sent_total Total bytes sent to clients\n")
		fmt.Fprintf(w, "# TYPE rah_bytes_sent_total counter\n")
		fmt.Fprintf(w, "rah_bytes_sent_total %d\n", m.ClientBytesSentTotal)
		fmt.Fprintf(w, "\n")

		// Export events dropped due to a full queue.
		fmt.Fprintf(w, "# HELP rah_dropped_exports_total Export events dropped due to full queue\n")
		fmt.Fprintf(w, "# TYPE rah_dropped_exports_total counter\n")
		fmt.Fprintf(w, "rah_dropped_exports_total %d\n", m.DroppedExports)
		fmt.Fprintf(w, "\n")
	}
}
