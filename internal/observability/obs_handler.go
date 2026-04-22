package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ObsHandler serves the observability REST API on the management mux (port 8081).
type ObsHandler struct {
	writer *ObsWriter
	obs    *Telemetry
}

// NewObsHandler creates an ObsHandler backed by the given writer and telemetry.
func NewObsHandler(writer *ObsWriter, obs *Telemetry) *ObsHandler {
	return &ObsHandler{writer: writer, obs: obs}
}

// RegisterObsRoutes registers all observability REST routes on mux.
func RegisterObsRoutes(mux *http.ServeMux, writer *ObsWriter, obs *Telemetry) {
	h := NewObsHandler(writer, obs)
	mux.HandleFunc("/observability/metrics", h.MetricsHandler)
	mux.HandleFunc("/observability/access-log", h.AccessLogHandler)
	mux.HandleFunc("/observability/traces", h.TracesHandler)
	mux.HandleFunc("/observability/apis", h.APIsHandler)
	mux.HandleFunc("/observability/apis/", h.APIDetailHandler)
	mux.HandleFunc("/observability/tenants/", h.TenantDetailHandler)
}

// parseFrom parses the ?from query param: tries duration first, then RFC3339.
// Returns zero if empty/unparseable (no filter applied).
func parseFrom(s string) int64 {
	if s == "" {
		return 0
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d).Unix()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix()
	}
	return 0
}

// isNoop returns true when the store is a NoopObsStore (no persistence configured).
func isNoop(s ObsStore) bool {
	_, ok := s.(NoopObsStore)
	return ok
}

// MetricsHandler handles GET /observability/metrics.
// Returns current in-memory aggregated gateway metrics as JSON.
// Optional query params: ?api=name (filter top-N api list) and ?tenant=alias.
func (h *ObsHandler) MetricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	snap := h.obs.Snapshot(20)
	// Optional client-side hint filtering for top-lists (applied in-memory).
	apiFilter := r.URL.Query().Get("api")
	tenantFilter := r.URL.Query().Get("tenant")

	if apiFilter != "" || tenantFilter != "" {
		if m, ok := snap["metrics"].(GatewayMetrics); ok {
			if apiFilter != "" {
				filtered := m.InstructionTopSlow[:0:0]
				for _, nl := range m.InstructionTopSlow {
					if strings.Contains(nl.Name, apiFilter) {
						filtered = append(filtered, nl)
					}
				}
				m.InstructionTopSlow = filtered

				filteredUp := m.UpstreamTopSlow[:0:0]
				for _, nl := range m.UpstreamTopSlow {
					if strings.Contains(nl.Name, apiFilter) {
						filteredUp = append(filteredUp, nl)
					}
				}
				m.UpstreamTopSlow = filteredUp
			}
			snap["metrics"] = m
		}
	}

	writeJSON(w, http.StatusOK, snap)
}

// AccessLogHandler handles GET /observability/access-log.
// Queries persisted access log records using optional filters.
func (h *ObsHandler) AccessLogHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if isNoop(h.writer.Store()) {
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}, "note": "persistence not configured"})
		return
	}

	q := r.URL.Query()
	limit := 100
	if lStr := q.Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	status := 0
	if sStr := q.Get("status"); sStr != "" {
		if n, err := strconv.Atoi(sStr); err == nil {
			status = n
		}
	}

	f := AccessLogFilter{
		ApiName:   q.Get("api"),
		TenantKey: q.Get("tenant"),
		Status:    status,
		FromUnixS: parseFrom(q.Get("from")),
		Limit:     limit,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	records, err := h.writer.Store().QueryAccessLog(ctx, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if records == nil {
		records = []AccessLogRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": records})
}

// TracesHandler handles GET /observability/traces.
// Queries persisted trace records using optional filters.
func (h *ObsHandler) TracesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if isNoop(h.writer.Store()) {
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}, "note": "persistence not configured"})
		return
	}

	q := r.URL.Query()
	limit := 100
	if lStr := q.Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	var tenantID uint16
	if tStr := q.Get("tenant_id"); tStr != "" {
		if n, err := strconv.ParseUint(tStr, 10, 16); err == nil {
			tenantID = uint16(n)
		}
	}

	var minMs float64
	if mStr := q.Get("min_ms"); mStr != "" {
		if f, err := strconv.ParseFloat(mStr, 64); err == nil {
			minMs = f
		}
	}

	f := TraceFilter{
		ApiName:   q.Get("api"),
		TenantID:  tenantID,
		MinMs:     minMs,
		FromUnixS: parseFrom(q.Get("from")),
		Limit:     limit,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	traces, err := h.writer.Store().QueryTraces(ctx, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if traces == nil {
		traces = []TraceRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": traces})
}

// APIsHandler handles GET /observability/apis.
// Returns the top-slow APIs derived from in-memory telemetry.
func (h *ObsHandler) APIsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Primary source: aggregate true API performance from persisted access logs.
	// This keeps API Performance focused on customer-facing APIs (api_name),
	// not internal upstream/model calls.
	if !isNoop(h.writer.Store()) {
		logs, err := h.writer.Store().QueryAccessLog(ctx, AccessLogFilter{
			FromUnixS: time.Now().Add(-1 * time.Hour).Unix(),
			Limit:     1000,
		})
		if err == nil && len(logs) > 0 {
			agg := make(map[string]NameLatency, len(logs))
			for _, rec := range logs {
				name := strings.TrimSpace(rec.ApiName)
				if name == "" {
					continue
				}
				a := agg[name]
				a.Name = name
				a.Count++
				a.TotalLatencyNs += uint64(rec.TotalMs * 1e6)
				if rec.ReqBytes > 0 {
					a.BytesTx += uint64(rec.ReqBytes)
				}
				if rec.ResBytes > 0 {
					a.BytesRx += uint64(rec.ResBytes)
				}
				agg[name] = a
			}

			apis := make([]NameLatency, 0, len(agg))
			for _, v := range agg {
				apis = append(apis, v)
			}
			sort.Slice(apis, func(i, j int) bool {
				if apis[i].Count == apis[j].Count {
					return apis[i].TotalLatencyNs > apis[j].TotalLatencyNs
				}
				return apis[i].Count > apis[j].Count
			})
			writeJSON(w, http.StatusOK, map[string]any{"apis": apis})
			return
		}
	}

	// Fallback for environments without persisted access logs.
	snap := h.obs.Snapshot(20)
	apis := []NameLatency{}
	if m, ok := snap["metrics"].(GatewayMetrics); ok {
		apis = m.UpstreamTopSlow
	}
	writeJSON(w, http.StatusOK, map[string]any{"apis": apis})
}

// APIDetailHandler handles GET /observability/apis/{name}.
// Returns traces and recent access log entries for a single API.
func (h *ObsHandler) APIDetailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract the API name from the path: strip "/observability/apis/" prefix.
	const prefix = "/observability/apis/"
	apiName := strings.TrimPrefix(r.URL.Path, prefix)
	if apiName == "" {
		writeError(w, http.StatusBadRequest, "missing api name in path")
		return
	}

	result := map[string]any{"api": apiName}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if !isNoop(h.writer.Store()) {
		traces, err := h.writer.Store().QueryTraces(ctx, TraceFilter{
			ApiName:   apiName,
			FromUnixS: time.Now().Add(-1 * time.Hour).Unix(),
			Limit:     50,
		})
		if err == nil {
			if traces == nil {
				traces = []TraceRecord{}
			}
			result["traces"] = traces
		}

		logs, err := h.writer.Store().QueryAccessLog(ctx, AccessLogFilter{
			ApiName:   apiName,
			FromUnixS: time.Now().Add(-1 * time.Hour).Unix(),
			Limit:     100,
		})
		if err == nil {
			if logs == nil {
				logs = []AccessLogRecord{}
			}
			result["access_log"] = logs
		}
	} else {
		result["note"] = "persistence not configured"
		result["traces"] = []any{}
		result["access_log"] = []any{}
	}

	writeJSON(w, http.StatusOK, result)
}

// TenantDetailHandler handles GET /observability/tenants/{alias}.
// Returns access log and traces for a single tenant.
func (h *ObsHandler) TenantDetailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract the tenant alias from the path: strip "/observability/tenants/" prefix.
	const prefix = "/observability/tenants/"
	tenantAlias := strings.TrimPrefix(r.URL.Path, prefix)
	if tenantAlias == "" {
		writeError(w, http.StatusBadRequest, "missing tenant alias in path")
		return
	}

	result := map[string]any{"tenant": tenantAlias}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if !isNoop(h.writer.Store()) {
		logs, err := h.writer.Store().QueryAccessLog(ctx, AccessLogFilter{
			TenantKey: tenantAlias,
			FromUnixS: time.Now().Add(-1 * time.Hour).Unix(),
			Limit:     100,
		})
		if err == nil {
			if logs == nil {
				logs = []AccessLogRecord{}
			}
			result["access_log"] = logs
		}
	} else {
		result["note"] = "persistence not configured"
		result["access_log"] = []any{}
		result["traces"] = []any{}
	}

	writeJSON(w, http.StatusOK, result)
}
