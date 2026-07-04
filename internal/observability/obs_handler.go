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
// APISchemaInstr describes a single instruction in a compiled endpoint plan.
// PC matches the index in the InstrCounter array; Name and StepType are resolved
// from InstrMeta baked at compile time. Collectors use this to label raw metrics.
type APISchemaInstr struct {
	PC       int    `json:"pc"`
	Name     string `json:"name"`
	StepType string `json:"step_type"`
}

// APISchemaEndpoint describes one endpoint's instruction schema.
type APISchemaEndpoint struct {
	EndpointID   uint8            `json:"endpoint_id"`
	Instructions []APISchemaInstr `json:"instructions"`
}

// APISchema is the per-API schema response returned by GET /observability/api-schemas.
// Gateway emits raw (apiID, versionID, pc, durationNs) tuples; collectors fetch
// this schema to resolve pc→name offline without burdening the hot path.
type APISchema struct {
	APIID     uint32              `json:"api_id"`
	VersionID uint32              `json:"version_id"`
	Endpoints []APISchemaEndpoint `json:"endpoints"`
}

type ObsHandler struct {
	writer         *ObsWriter
	obs            *Telemetry
	nameResolver   func(uint32) string // optional; maps ApiID → name for in-memory fallback
	schemaProvider func() []APISchema  // optional; returns live API schemas
}

// NewObsHandler creates an ObsHandler backed by the given writer and telemetry.
// nameResolver is optional — pass registry.GetNameByID to enable the in-memory API stats fallback.
func NewObsHandler(writer *ObsWriter, obs *Telemetry, nameResolver ...func(uint32) string) *ObsHandler {
	h := &ObsHandler{writer: writer, obs: obs}
	if len(nameResolver) > 0 && nameResolver[0] != nil {
		h.nameResolver = nameResolver[0]
	}
	return h
}

// SetSchemaProvider wires a function that returns the current live API schemas.
// Called from main.go after the FlowManager is ready. Safe to call once before
// serving traffic; no synchronisation needed after that.
func (h *ObsHandler) SetSchemaProvider(fn func() []APISchema) {
	h.schemaProvider = fn
}

// RegisterObsRoutes registers all observability REST routes on mux and returns
// the handler so callers can call SetSchemaProvider after registration.
// nameResolver is optional; pass registry.GetNameByID to enable the in-memory API stats fallback.
func RegisterObsRoutes(mux *http.ServeMux, writer *ObsWriter, obs *Telemetry, nameResolver ...func(uint32) string) *ObsHandler {
	h := NewObsHandler(writer, obs, nameResolver...)
	mux.HandleFunc("/observability/metrics", h.MetricsHandler)
	mux.HandleFunc("/observability/access-log", h.AccessLogHandler)
	mux.HandleFunc("/observability/traces", h.TracesHandler)
	mux.HandleFunc("/observability/detail-log", h.DetailLogConfigHandler)
	mux.HandleFunc("/observability/apis", h.APIsHandler)
	mux.HandleFunc("/observability/apis/", h.APIDetailHandler)
	mux.HandleFunc("/observability/tenants/", h.TenantDetailHandler)
	mux.HandleFunc("/observability/api-schemas", h.APISchemaHandler)
	mux.HandleFunc("/observability/instr-schema", h.InstrSchemaHandler)
	return h
}

// InstrSchemaHandler handles GET /observability/instr-schema.
// Returns instruction schema rows for a given API name, used by Studio to render
// the instruction timing waterfall in the trace view.
func (h *ObsHandler) InstrSchemaHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	apiName := r.URL.Query().Get("api")
	if apiName == "" {
		writeError(w, http.StatusBadRequest, "api param required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := h.writer.Store().QueryInstrSchema(ctx, apiName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []InstrSchemaRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

// APISchemaHandler handles GET /observability/api-schemas.
// Returns the schema for every live API: (apiID, versionID, endpoint, pc, name, stepType).
// Collectors call this once on startup and after any schema-change event to
// resolve raw integer metrics to human-readable instruction names.
func (h *ObsHandler) APISchemaHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.schemaProvider == nil {
		writeJSON(w, http.StatusOK, []APISchema{})
		return
	}
	writeJSON(w, http.StatusOK, h.schemaProvider())
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
func (h *ObsHandler) MetricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	snap := h.obs.Snapshot(20)
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

// DetailLogConfigHandler handles GET and PUT for detail log config.
func (h *ObsHandler) DetailLogConfigHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, GetDetailLogConfig())
		return
	case http.MethodPut:
		var req struct {
			Enabled bool   `json:"enabled"`
			Path    string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if err := SetDetailLogConfig(req.Enabled, req.Path); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, GetDetailLogConfig())
		return
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
}

// APIsHandler handles GET /observability/apis.
func (h *ObsHandler) APIsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// 1. Primary Source: Persisted access logs
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
			writeJSON(w, http.StatusOK, map[string]any{"apis": apis, "source": "access_log"})
			return
		}
	}

	// 2. Fallback: in-memory per-API stats accumulated by RecordRequest (atomic, no GC).
	// Returns actual user-facing API names — never LLM upstream model names.
	apis := h.obs.APITop(20, h.nameResolver)
	note := "no persisted data in last hour; using in-memory API stats"
	if len(apis) == 0 {
		note = "no API data yet; make requests to populate"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"apis":   apis,
		"note":   note,
		"source": "telemetry_api_stats",
	})
}

// APIDetailHandler handles GET /observability/apis/{name}.
func (h *ObsHandler) APIDetailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

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
func (h *ObsHandler) TenantDetailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

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
