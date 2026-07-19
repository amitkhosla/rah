package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// postgresObsStore persists observability data to PostgreSQL using pgx/v5.
// The connection pool is owned by this struct and closed via Close().
type postgresObsStore struct {
	pool *pgxpool.Pool
}

// NewPostgresObsStore opens a pgx connection pool to dsn, pings the server,
// and ensures all observability tables and indexes exist.
// dsn may be a postgres:// URL or a DSN keyword string accepted by pgx.
func NewPostgresObsStore(dsn string) (ObsStore, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("obs postgres: parse dsn: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("obs postgres: connect: %w", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("obs postgres: ping: %w", err)
	}

	s := &postgresObsStore{pool: pool}
	if err := s.ensureSchema(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("obs postgres: ensure schema: %w", err)
	}
	return s, nil
}

func (s *postgresObsStore) ensureSchema(ctx context.Context) error {
	ddl := `
CREATE TABLE IF NOT EXISTS obs_access_log (
    id          BIGSERIAL PRIMARY KEY,
    ts          BIGINT    NOT NULL,
    api_name    TEXT,
    tenant_id   SMALLINT,
    tenant_key  TEXT,
    method      TEXT,
    path        TEXT,
    status      SMALLINT,
    total_ms    REAL,
    gateway_ms  REAL,
    upstream_ms REAL,
    ttfb_ms     REAL,
    req_bytes   BIGINT,
    res_bytes   BIGINT,
    extra       JSONB
);
CREATE INDEX IF NOT EXISTS obs_access_log_ts_idx     ON obs_access_log(ts DESC);
CREATE INDEX IF NOT EXISTS obs_access_log_api_idx    ON obs_access_log(api_name, ts DESC);
CREATE INDEX IF NOT EXISTS obs_access_log_tenant_idx ON obs_access_log(tenant_key, ts DESC);
CREATE INDEX IF NOT EXISTS obs_access_log_status_idx ON obs_access_log(status, ts DESC);

CREATE TABLE IF NOT EXISTS obs_metric_snapshots (
    ts          BIGINT NOT NULL,
    "window"    TEXT   NOT NULL,
    dimension   TEXT   NOT NULL,
    req_total   BIGINT,
    req_5xx     BIGINT,
    lat_p50_ms  REAL,
    lat_p95_ms  REAL,
    lat_p99_ms  REAL,
    bytes_in    BIGINT,
    bytes_out   BIGINT,
    PRIMARY KEY (ts, "window", dimension)
);
CREATE INDEX IF NOT EXISTS obs_metrics_dim_idx ON obs_metric_snapshots(dimension, ts DESC);

CREATE TABLE IF NOT EXISTS obs_traces (
    trace_id  BIGINT   PRIMARY KEY,
    ts        BIGINT   NOT NULL,
    api_name  TEXT,
    tenant_id SMALLINT,
    status    SMALLINT,
    total_ms  REAL,
    payload   JSONB
);
CREATE INDEX IF NOT EXISTS obs_traces_ts_idx     ON obs_traces(ts DESC);
CREATE INDEX IF NOT EXISTS obs_traces_api_idx    ON obs_traces(api_name, ts DESC);
CREATE INDEX IF NOT EXISTS obs_traces_tenant_idx ON obs_traces(tenant_id, ts DESC);

-- V2 typed trace records (no JSONB blob)
CREATE TABLE IF NOT EXISTS obs_traces_v2 (
    trace_id       BIGINT   PRIMARY KEY,
    ts             BIGINT   NOT NULL,
    api_name       TEXT,
    api_version_id INTEGER,
    endpoint_id    SMALLINT,
    tenant_id      SMALLINT,
    status         SMALLINT,
    method         SMALLINT,
    duration_ns    BIGINT,
    gateway_ns     BIGINT,
    upstream_ns    BIGINT,
    req_bytes      BIGINT,
    res_bytes      BIGINT,
    upstream_calls SMALLINT,
    phase_durs     INTEGER[10],
    instr_count    SMALLINT
);
CREATE INDEX IF NOT EXISTS obs_tv2_ts_idx     ON obs_traces_v2(ts DESC);
CREATE INDEX IF NOT EXISTS obs_tv2_api_idx    ON obs_traces_v2(api_name, ts DESC);
CREATE INDEX IF NOT EXISTS obs_tv2_tenant_idx ON obs_traces_v2(tenant_id, ts DESC);

-- Per-request instruction runs (join with obs_instruction_schema for names)
CREATE TABLE IF NOT EXISTS obs_instruction_runs (
    trace_id    BIGINT   NOT NULL REFERENCES obs_traces_v2(trace_id) ON DELETE CASCADE,
    pc          SMALLINT NOT NULL,
    seq         SMALLINT NOT NULL,
    dur_ns      INTEGER  NOT NULL,
    PRIMARY KEY (trace_id, pc, seq)
);
CREATE INDEX IF NOT EXISTS obs_ir_trace_idx ON obs_instruction_runs(trace_id);

-- Per-trace LLM calls
CREATE TABLE IF NOT EXISTS obs_llm_calls (
    trace_id      BIGINT   NOT NULL,
    pc            SMALLINT NOT NULL,
    seq           SMALLINT NOT NULL,
    model_name    TEXT,
    status        SMALLINT,
    input_tokens  INTEGER,
    output_tokens INTEGER,
    cost_micro    INTEGER,
    duration_ns   BIGINT,
    PRIMARY KEY (trace_id, pc, seq)
);
CREATE INDEX IF NOT EXISTS obs_llm_trace_idx ON obs_llm_calls(trace_id);

-- Instruction schema: written once at API compile time
CREATE TABLE IF NOT EXISTS obs_instruction_schema (
    api_name    TEXT     NOT NULL,
    api_hash    BIGINT   NOT NULL,
    endpoint_id SMALLINT NOT NULL DEFAULT 0,
    pc          SMALLINT NOT NULL,
    step_type   TEXT,
    step_name   TEXT,
    PRIMARY KEY (api_name, endpoint_id, pc)
);

-- Variable schema: maps slot index (var_id) to human-readable name; written at bake time
CREATE TABLE IF NOT EXISTS obs_var_schema (
    api_name  TEXT     NOT NULL,
    api_hash  BIGINT   NOT NULL,
    var_id    SMALLINT NOT NULL,
    var_name  TEXT     NOT NULL,
    step_type TEXT,
    PRIMARY KEY (api_name, var_id)
);

-- Payload store: raw LLM/upstream request+response bytes, keyed by trace_id
CREATE TABLE IF NOT EXISTS obs_payloads (
    trace_id   BIGINT   NOT NULL,
    kind       SMALLINT NOT NULL,  -- 1=LLM, 2=upstream
    seq        SMALLINT NOT NULL,  -- call sequence within trace (0-based)
    pc         SMALLINT NOT NULL,  -- instruction PC that made the call
    content    BYTEA,              -- [4B req_len][req_bytes][4B res_len][res_bytes]
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_obs_payloads_trace_id ON obs_payloads (trace_id, seq);
`
	_, err := s.pool.Exec(ctx, ddl)
	return err
}

// ── WriteAccessLog ────────────────────────────────────────────────────────────

// WriteAccessLog persists a batch of access log records in a single multi-row
// INSERT statement to minimise round-trips.
func (s *postgresObsStore) WriteAccessLog(ctx context.Context, records []AccessLogRecord) error {
	if len(records) == 0 {
		return nil
	}

	const cols = 14
	args := make([]any, 0, len(records)*cols)
	var sb strings.Builder
	sb.WriteString(
		`INSERT INTO obs_access_log` +
			`(ts,api_name,tenant_id,tenant_key,method,path,status,` +
			`total_ms,gateway_ms,upstream_ms,ttfb_ms,req_bytes,res_bytes,extra) VALUES `)

	for i, r := range records {
		if i > 0 {
			sb.WriteByte(',')
		}
		base := i * cols
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7,
			base+8, base+9, base+10, base+11, base+12, base+13, base+14)

		// Serialise Extra map to JSON; nil map → NULL.
		var extraJSON []byte
		if len(r.Extra) > 0 {
			b, _ := json.Marshal(r.Extra)
			extraJSON = b
		}

		args = append(args,
			r.TimestampNs,
			nilIfEmpty(r.ApiName),
			int16(r.TenantID),
			nilIfEmpty(r.TenantKey),
			nilIfEmpty(r.Method),
			nilIfEmpty(r.Path),
			int16(r.Status),
			float32(r.TotalMs),
			float32(r.GatewayMs),
			float32(r.UpstreamMs),
			float32(r.TTFBMs),
			r.ReqBytes,
			r.ResBytes,
			extraJSON,
		)
	}

	_, err := s.pool.Exec(ctx, sb.String(), args...)
	return err
}

// ── WriteMetricSnapshot ───────────────────────────────────────────────────────

// WriteMetricSnapshot upserts a metric snapshot by (ts, window, dimension).
func (s *postgresObsStore) WriteMetricSnapshot(ctx context.Context, snap MetricSnapshot) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO obs_metric_snapshots
    (ts,"window",dimension,req_total,req_5xx,lat_p50_ms,lat_p95_ms,lat_p99_ms,bytes_in,bytes_out)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (ts, "window", dimension) DO UPDATE SET
    req_total  = EXCLUDED.req_total,
    req_5xx    = EXCLUDED.req_5xx,
    lat_p50_ms = EXCLUDED.lat_p50_ms,
    lat_p95_ms = EXCLUDED.lat_p95_ms,
    lat_p99_ms = EXCLUDED.lat_p99_ms,
    bytes_in   = EXCLUDED.bytes_in,
    bytes_out  = EXCLUDED.bytes_out`,
		snap.Timestamp,
		snap.Window,
		snap.Dimension,
		snap.ReqTotal,
		snap.Req5xx,
		float32(snap.LatP50Ms),
		float32(snap.LatP95Ms),
		float32(snap.LatP99Ms),
		snap.BytesIn,
		snap.BytesOut,
	)
	return err
}

// ── WriteTraceBatch ───────────────────────────────────────────────────────────

// methodToInt encodes an HTTP method string to its int16 representation.
func methodToInt(m string) int16 {
	switch m {
	case "GET":
		return 0
	case "POST":
		return 1
	case "PUT":
		return 2
	case "DELETE":
		return 3
	case "PATCH":
		return 4
	case "HEAD":
		return 5
	case "OPTIONS":
		return 6
	default:
		return 7
	}
}

// intToMethod decodes a stored HTTP method int16 back to its string representation.
func intToMethod(m int16) string {
	switch m {
	case 0:
		return "GET"
	case 1:
		return "POST"
	case 2:
		return "PUT"
	case 3:
		return "DELETE"
	case 4:
		return "PATCH"
	case 5:
		return "HEAD"
	case 6:
		return "OPTIONS"
	default:
		return "OTHER"
	}
}

// WriteTraceBatch persists a batch of trace records.
// V2 records (InstrPCs != nil) are written to obs_traces_v2, obs_instruction_runs,
// and obs_llm_calls using pgx SendBatch for a single TCP round-trip.
// V1 records (InstrPCs == nil) fall back to the legacy obs_traces table.
func (s *postgresObsStore) WriteTraceBatch(ctx context.Context, records []TraceRecord) error {
	if len(records) == 0 {
		return nil
	}

	// Partition records into v1 and v2.
	var v1Records, v2Records []TraceRecord
	for _, rec := range records {
		if rec.InstrPCs == nil {
			v1Records = append(v1Records, rec)
		} else {
			v2Records = append(v2Records, rec)
		}
	}

	// Write v1 records individually into the legacy obs_traces table.
	for _, rec := range v1Records {
		_, err := s.pool.Exec(ctx, `
INSERT INTO obs_traces (trace_id,ts,api_name,tenant_id,status,total_ms,payload)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (trace_id) DO NOTHING`,
			int64(rec.TraceID),
			rec.Timestamp,
			nilIfEmpty(rec.ApiName),
			int16(rec.TenantID),
			int16(rec.Status),
			float32(rec.TotalMs),
			rec.Payload,
		)
		if err != nil {
			log.Printf("obs postgres: WriteTraceBatch v1 insert error (trace_id=%d): %v", rec.TraceID, err)
		}
	}

	if len(v2Records) == 0 {
		return nil
	}

	// Build a SendBatch for all v2 records.
	batch := &pgx.Batch{}
	for _, rec := range v2Records {
		// Convert PhaseDurs [10]int32 to []int32 for pgx array encoding.
		phaseDurs := rec.PhaseDurs[:]

		batch.Queue(
			`INSERT INTO obs_traces_v2 (
				trace_id, ts, api_name, api_version_id, endpoint_id, tenant_id,
				status, method, duration_ns, gateway_ns, upstream_ns,
				req_bytes, res_bytes, upstream_calls, phase_durs, instr_count
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
			ON CONFLICT (trace_id) DO NOTHING`,
			int64(rec.TraceID),
			rec.Timestamp,
			nilIfEmpty(rec.ApiName),
			int32(rec.ApiVersionID),
			int16(rec.EndpointID),
			int16(rec.TenantID),
			int16(rec.Status),
			methodToInt(rec.Method),
			rec.DurationNs,
			rec.GatewayNs,
			rec.UpstreamNs,
			rec.ReqBytes,
			rec.ResBytes,
			int16(rec.UpstreamCalls),
			phaseDurs,
			int16(len(rec.InstrPCs)),
		)

		for i, pc := range rec.InstrPCs {
			batch.Queue(
				`INSERT INTO obs_instruction_runs (trace_id, pc, seq, dur_ns)
				VALUES ($1,$2,$3,$4)
				ON CONFLICT (trace_id, pc, seq) DO NOTHING`,
				int64(rec.TraceID),
				pc,
				int16(i),
				rec.InstrDursNs[i],
			)
		}

		for _, llm := range rec.LLMCalls {
			batch.Queue(
				`INSERT INTO obs_llm_calls (
					trace_id, pc, seq, model_name, status,
					input_tokens, output_tokens, cost_micro, duration_ns
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
				ON CONFLICT (trace_id, pc, seq) DO NOTHING`,
				int64(rec.TraceID),
				llm.PC,
				int16(llm.Seq),
				nilIfEmpty(llm.ModelName),
				int16(llm.Status),
				int32(llm.InputTokens),
				int32(llm.OutputTokens),
				int32(llm.CostMicro),
				llm.DurationNs,
			)
		}
	}

	br := s.pool.SendBatch(ctx, batch)
	defer func() {
		if err := br.Close(); err != nil {
			log.Printf("obs postgres: batch results close error: %v", err)
		}
	}()

	n := batch.Len()
	for i := 0; i < n; i++ {
		if _, err := br.Exec(); err != nil {
			log.Printf("obs postgres: WriteTraceBatch SendBatch exec[%d] error: %v", i, err)
		}
	}
	return nil
}

// ── UpsertInstrSchema ─────────────────────────────────────────────────────────

// UpsertInstrSchema writes or updates instruction schema rows for an API endpoint.
// Called once at bake time when an API is compiled.
func (s *postgresObsStore) UpsertInstrSchema(ctx context.Context, rows []InstrSchemaRow) error {
	if len(rows) == 0 {
		return nil
	}

	const cols = 6
	args := make([]any, 0, len(rows)*cols)
	var sb strings.Builder
	sb.WriteString(`INSERT INTO obs_instruction_schema (api_name,api_hash,endpoint_id,pc,step_type,step_name) VALUES `)

	for i, r := range rows {
		if i > 0 {
			sb.WriteByte(',')
		}
		base := i * cols
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6)
		args = append(args,
			r.ApiName,
			int64(r.ApiHash),
			int16(r.EndpointID),
			r.PC,
			nilIfEmpty(r.StepType),
			nilIfEmpty(r.StepName),
		)
	}

	sb.WriteString(` ON CONFLICT (api_name, endpoint_id, pc) DO UPDATE SET
		api_hash  = EXCLUDED.api_hash,
		step_type = EXCLUDED.step_type,
		step_name = EXCLUDED.step_name`)

	_, err := s.pool.Exec(ctx, sb.String(), args...)
	return err
}

// ── QueryAccessLog ────────────────────────────────────────────────────────────

// QueryAccessLog returns access log entries matching f, ordered by ts DESC.
// Limit defaults to 100 and is capped at 1000.
func (s *postgresObsStore) QueryAccessLog(ctx context.Context, f AccessLogFilter) ([]AccessLogRecord, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	qb := newQueryBuilder()

	if f.ApiName != "" {
		qb.add("api_name = $%d", f.ApiName)
	}
	if f.TenantKey != "" {
		qb.add("tenant_key = $%d", f.TenantKey)
	}
	if f.Status > 0 {
		qb.add("status = $%d", int16(f.Status))
	}
	if f.FromUnixS > 0 {
		// ts column stores unix nanoseconds; convert filter to nanoseconds.
		qb.add("ts >= $%d", f.FromUnixS*1_000_000_000)
	}
	if f.ToUnixS > 0 {
		qb.add("ts <= $%d", f.ToUnixS*1_000_000_000)
	}
	qb.add("TRUE LIMIT $%d", limit) // always append limit as last placeholder

	query := `SELECT ts,api_name,tenant_id,tenant_key,method,path,status,` +
		`total_ms,gateway_ms,upstream_ms,ttfb_ms,req_bytes,res_bytes,extra ` +
		`FROM obs_access_log` + qb.whereClause() + ` ORDER BY ts DESC`

	// Remove the fake "TRUE LIMIT $N" from WHERE; rewrite as a real LIMIT.
	query = rewriteLimit(query, qb.limitPlaceholder())

	rows, err := s.pool.Query(ctx, query, qb.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AccessLogRecord
	for rows.Next() {
		var r AccessLogRecord
		var tenantID, status int16
		var totalMs, gatewayMs, upstreamMs, ttfbMs float32
		// All TEXT columns are nullable (written via nilIfEmpty); use *string to handle NULLs.
		var apiName, tenantKey, method, path *string
		var extraJSON []byte

		if err := rows.Scan(
			&r.TimestampNs, &apiName, &tenantID, &tenantKey,
			&method, &path, &status,
			&totalMs, &gatewayMs, &upstreamMs, &ttfbMs,
			&r.ReqBytes, &r.ResBytes, &extraJSON,
		); err != nil {
			return nil, err
		}
		if apiName != nil {
			r.ApiName = *apiName
		}
		if tenantKey != nil {
			r.TenantKey = *tenantKey
		}
		if method != nil {
			r.Method = *method
		}
		if path != nil {
			r.Path = *path
		}
		r.TenantID = uint16(tenantID)
		r.Status = int(status)
		r.TotalMs = float64(totalMs)
		r.GatewayMs = float64(gatewayMs)
		r.UpstreamMs = float64(upstreamMs)
		r.TTFBMs = float64(ttfbMs)
		if len(extraJSON) > 0 {
			_ = json.Unmarshal(extraJSON, &r.Extra)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── QueryMetrics ──────────────────────────────────────────────────────────────

// QueryMetrics returns metric snapshots matching f, ordered by ts DESC.
func (s *postgresObsStore) QueryMetrics(ctx context.Context, f MetricsFilter) ([]MetricSnapshot, error) {
	qb := newQueryBuilder()

	if f.Window != "" {
		qb.add(`"window" = $%d`, f.Window)
	}
	if f.Dimension != "" {
		// Support prefix-match ("api:", "tenant:") and exact match ("gateway").
		if strings.HasSuffix(f.Dimension, ":") {
			qb.add("dimension LIKE $%d", f.Dimension+"%")
		} else {
			qb.add("dimension = $%d", f.Dimension)
		}
	}
	if f.FromUnixS > 0 {
		qb.add("ts >= $%d", f.FromUnixS)
	}
	if f.ToUnixS > 0 {
		qb.add("ts <= $%d", f.ToUnixS)
	}

	query := `SELECT ts,"window",dimension,req_total,req_5xx,lat_p50_ms,lat_p95_ms,lat_p99_ms,bytes_in,bytes_out ` +
		`FROM obs_metric_snapshots` + qb.whereClause() + ` ORDER BY ts DESC`

	rows, err := s.pool.Query(ctx, query, qb.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MetricSnapshot
	for rows.Next() {
		var snap MetricSnapshot
		var p50, p95, p99 float32
		if err := rows.Scan(
			&snap.Timestamp, &snap.Window, &snap.Dimension,
			&snap.ReqTotal, &snap.Req5xx,
			&p50, &p95, &p99,
			&snap.BytesIn, &snap.BytesOut,
		); err != nil {
			return nil, err
		}
		snap.LatP50Ms = float64(p50)
		snap.LatP95Ms = float64(p95)
		snap.LatP99Ms = float64(p99)
		out = append(out, snap)
	}
	return out, rows.Err()
}

// ── QueryTraces ───────────────────────────────────────────────────────────────

// QueryTraces returns trace records matching f, ordered by ts DESC.
// Queries obs_traces_v2 first; falls back to obs_traces (v1) if no v2 results.
// Limit defaults to 50 and is capped at 500.
func (s *postgresObsStore) QueryTraces(ctx context.Context, f TraceFilter) ([]TraceRecord, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	// ── Try v2 table first ──
	v2Results, err := s.queryTracesV2(ctx, f, limit)
	if err != nil {
		return nil, err
	}
	if len(v2Results) > 0 {
		return v2Results, nil
	}

	// ── Fall back to v1 table ──
	return s.queryTracesV1(ctx, f, limit)
}

func (s *postgresObsStore) queryTracesV2(ctx context.Context, f TraceFilter, limit int) ([]TraceRecord, error) {
	qb := newQueryBuilder()

	if f.ApiName != "" {
		qb.add("t.api_name = $%d", f.ApiName)
	}
	if f.TenantID > 0 {
		qb.add("t.tenant_id = $%d", int16(f.TenantID))
	}
	if f.MinMs > 0 {
		qb.add("t.duration_ns >= $%d", int64(f.MinMs*1e6))
	}
	if f.FromUnixS > 0 {
		qb.add("t.ts >= $%d", f.FromUnixS)
	}
	if f.ToUnixS > 0 {
		qb.add("t.ts <= $%d", f.ToUnixS)
	}
	qb.add("TRUE LIMIT $%d", limit)

	// LEFT JOIN obs_instruction_runs to populate InstrPCs/InstrDursNs in one round-trip.
	query := `SELECT t.trace_id,t.ts,t.api_name,t.api_version_id,t.endpoint_id,t.tenant_id,
		t.status,t.duration_ns,t.gateway_ns,t.upstream_ns,t.req_bytes,t.res_bytes,t.upstream_calls,
		t.phase_durs,t.instr_count,t.method,
		ARRAY_AGG(r.pc   ORDER BY r.seq) FILTER (WHERE r.pc   IS NOT NULL) AS instr_pcs,
		ARRAY_AGG(r.dur_ns ORDER BY r.seq) FILTER (WHERE r.dur_ns IS NOT NULL) AS instr_durs_ns
		FROM obs_traces_v2 t
		LEFT JOIN obs_instruction_runs r ON r.trace_id = t.trace_id` +
		qb.whereClause() +
		` GROUP BY t.trace_id,t.ts,t.api_name,t.api_version_id,t.endpoint_id,t.tenant_id,
		t.status,t.duration_ns,t.gateway_ns,t.upstream_ns,t.req_bytes,t.res_bytes,t.upstream_calls,
		t.phase_durs,t.instr_count,t.method
		ORDER BY t.ts DESC`
	query = rewriteLimit(query, qb.limitPlaceholder())

	rows, err := s.pool.Query(ctx, query, qb.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TraceRecord
	for rows.Next() {
		var t TraceRecord
		var traceID int64
		var apiVersionID int32
		var endpointID, tenantID, status, upstreamCalls, methodInt int16
		var instrCount int16
		var phaseDurs []int32
		var instrPCs []int16
		var instrDurs []int32

		if err := rows.Scan(
			&traceID, &t.Timestamp, &t.ApiName, &apiVersionID, &endpointID,
			&tenantID, &status, &t.DurationNs, &t.GatewayNs, &t.UpstreamNs,
			&t.ReqBytes, &t.ResBytes, &upstreamCalls, &phaseDurs, &instrCount, &methodInt,
			&instrPCs, &instrDurs,
		); err != nil {
			return nil, err
		}
		t.TraceID = uint64(traceID)
		t.ApiVersionID = uint32(apiVersionID)
		t.EndpointID = uint8(endpointID)
		t.TenantID = uint16(tenantID)
		t.Status = int(status)
		t.UpstreamCalls = uint16(upstreamCalls)
		t.Method = intToMethod(methodInt)
		t.TotalMs = float64(t.DurationNs) / 1e6
		if len(phaseDurs) > 0 {
			n := len(phaseDurs)
			if n > 10 {
				n = 10
			}
			copy(t.PhaseDurs[:], phaseDurs[:n])
		}
		// Use aggregated PCs when available; fall back to empty non-nil slice (signals V2).
		if len(instrPCs) > 0 {
			t.InstrPCs = instrPCs
			t.InstrDursNs = instrDurs
		} else {
			t.InstrPCs = make([]int16, 0, instrCount)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *postgresObsStore) queryTracesV1(ctx context.Context, f TraceFilter, limit int) ([]TraceRecord, error) {
	qb := newQueryBuilder()

	if f.ApiName != "" {
		qb.add("api_name = $%d", f.ApiName)
	}
	if f.TenantID > 0 {
		qb.add("tenant_id = $%d", int16(f.TenantID))
	}
	if f.MinMs > 0 {
		qb.add("total_ms >= $%d", float32(f.MinMs))
	}
	if f.FromUnixS > 0 {
		qb.add("ts >= $%d", f.FromUnixS)
	}
	if f.ToUnixS > 0 {
		qb.add("ts <= $%d", f.ToUnixS)
	}
	qb.add("TRUE LIMIT $%d", limit)

	query := `SELECT trace_id,ts,api_name,tenant_id,status,total_ms,payload FROM obs_traces` +
		qb.whereClause() + ` ORDER BY ts DESC`
	query = rewriteLimit(query, qb.limitPlaceholder())

	rows, err := s.pool.Query(ctx, query, qb.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TraceRecord
	for rows.Next() {
		var t TraceRecord
		var traceID int64
		var tenantID, status int16
		var totalMs float32
		if err := rows.Scan(&traceID, &t.Timestamp, &t.ApiName, &tenantID, &status, &totalMs, &t.Payload); err != nil {
			return nil, err
		}
		t.TraceID = uint64(traceID)
		t.TenantID = uint16(tenantID)
		t.Status = int(status)
		t.TotalMs = float64(totalMs)
		out = append(out, t)
	}
	return out, rows.Err()
}

// ── QueryInstrSchema ──────────────────────────────────────────────────────────

// QueryInstrSchema returns instruction schema rows for the given API name,
// ordered by PC ascending.
func (s *postgresObsStore) QueryInstrSchema(ctx context.Context, apiName string) ([]InstrSchemaRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT api_name, api_hash, endpoint_id, pc, step_type, step_name
		FROM obs_instruction_schema
		WHERE api_name = $1
		ORDER BY endpoint_id, pc`,
		apiName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []InstrSchemaRow
	for rows.Next() {
		var r InstrSchemaRow
		var apiHash int64
		var endpointID int16
		var stepType, stepName *string
		if err := rows.Scan(&r.ApiName, &apiHash, &endpointID, &r.PC, &stepType, &stepName); err != nil {
			return nil, err
		}
		r.ApiHash = uint64(apiHash)
		r.EndpointID = uint8(endpointID)
		if stepType != nil {
			r.StepType = *stepType
		}
		if stepName != nil {
			r.StepName = *stepName
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── UpsertVarSchema ───────────────────────────────────────────────────────────

// UpsertVarSchema writes or updates variable schema rows for a compiled API.
// Called once at bake time after each API is compiled.
func (s *postgresObsStore) UpsertVarSchema(ctx context.Context, rows []VarSchemaRow) error {
	if len(rows) == 0 {
		return nil
	}

	const cols = 5
	args := make([]any, 0, len(rows)*cols)
	var sb strings.Builder
	sb.WriteString(`INSERT INTO obs_var_schema (api_name,api_hash,var_id,var_name,step_type) VALUES `)

	for i, r := range rows {
		if i > 0 {
			sb.WriteByte(',')
		}
		base := i * cols
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5)
		args = append(args,
			r.ApiName,
			int64(r.ApiHash),
			int16(r.VarID),
			r.VarName,
			nilIfEmpty(r.StepType),
		)
	}

	sb.WriteString(` ON CONFLICT (api_name, var_id) DO UPDATE SET
		api_hash  = EXCLUDED.api_hash,
		var_name  = EXCLUDED.var_name,
		step_type = EXCLUDED.step_type`)

	_, err := s.pool.Exec(ctx, sb.String(), args...)
	return err
}

// ── QueryVarSchema ────────────────────────────────────────────────────────────

// QueryVarSchema returns variable schema rows for the given API name,
// ordered by var_id ascending.
func (s *postgresObsStore) QueryVarSchema(ctx context.Context, apiName string) ([]VarSchemaRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT api_name, api_hash, var_id, var_name, step_type
		FROM obs_var_schema
		WHERE api_name = $1
		ORDER BY var_id`,
		apiName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []VarSchemaRow
	for rows.Next() {
		var r VarSchemaRow
		var apiHash int64
		var varID int16
		var stepType *string
		if err := rows.Scan(&r.ApiName, &apiHash, &varID, &r.VarName, &stepType); err != nil {
			return nil, err
		}
		r.ApiHash = uint64(apiHash)
		r.VarID = uint16(varID)
		if stepType != nil {
			r.StepType = *stepType
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── WritePayloadBatch / QueryPayloads ─────────────────────────────────────────

// WritePayloadBatch persists a batch of payload records in a single multi-row
// INSERT statement. ON CONFLICT DO NOTHING is safe because trace_id+seq may
// conflict on retries.
func (s *postgresObsStore) WritePayloadBatch(ctx context.Context, records []PayloadRecord) error {
	if len(records) == 0 {
		return nil
	}

	const cols = 5
	args := make([]any, 0, len(records)*cols)
	var sb strings.Builder
	sb.WriteString(`INSERT INTO obs_payloads (trace_id, kind, seq, pc, content) VALUES `)

	for i, r := range records {
		if i > 0 {
			sb.WriteByte(',')
		}
		base := i * cols
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d)", base+1, base+2, base+3, base+4, base+5)
		args = append(args,
			int64(r.TraceID),
			int16(r.Kind),
			int16(r.Seq),
			r.PC,
			r.Content, // pgx handles []byte → bytea
		)
	}
	sb.WriteString(` ON CONFLICT DO NOTHING`)

	_, err := s.pool.Exec(ctx, sb.String(), args...)
	return err
}

// QueryPayloads returns all payload records for a given trace ID, ordered by seq.
func (s *postgresObsStore) QueryPayloads(ctx context.Context, traceID uint64) ([]PayloadRecord, error) {
	const q = `SELECT kind, seq, pc, content
                 FROM obs_payloads
                WHERE trace_id = $1
                ORDER BY seq`

	rows, err := s.pool.Query(ctx, q, int64(traceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []PayloadRecord
	for rows.Next() {
		var r PayloadRecord
		r.TraceID = traceID
		var kind, seq int16
		var pc int16
		if err := rows.Scan(&kind, &seq, &pc, &r.Content); err != nil {
			return nil, err
		}
		r.Kind = uint8(kind)
		r.Seq = uint8(seq)
		r.PC = pc
		records = append(records, r)
	}
	return records, rows.Err()
}

// ── Close ─────────────────────────────────────────────────────────────────────

func (s *postgresObsStore) Close() error {
	s.pool.Close()
	return nil
}

// ── query builder helpers ─────────────────────────────────────────────────────

// queryBuilder accumulates WHERE conditions and their corresponding args.
// Each condition must contain exactly one %d placeholder for the arg index.
type queryBuilder struct {
	conditions []string
	args       []any
	nextIdx    int
}

func newQueryBuilder() *queryBuilder { return &queryBuilder{nextIdx: 1} }

// add appends a condition and its argument.  condFmt must contain one %d.
func (qb *queryBuilder) add(condFmt string, val any) {
	qb.conditions = append(qb.conditions, fmt.Sprintf(condFmt, qb.nextIdx))
	qb.args = append(qb.args, val)
	qb.nextIdx++
}

// whereClause returns " WHERE cond1 AND cond2 ..." or "" if no conditions.
// The sentinel "TRUE LIMIT $N" entry is excluded from the WHERE clause here;
// callers use rewriteLimit to move the LIMIT outside.
func (qb *queryBuilder) whereClause() string {
	real := make([]string, 0, len(qb.conditions))
	for _, c := range qb.conditions {
		if !strings.HasPrefix(c, "TRUE LIMIT") {
			real = append(real, c)
		}
	}
	if len(real) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(real, " AND ")
}

// limitPlaceholder returns the $N string used for the LIMIT arg, or "".
func (qb *queryBuilder) limitPlaceholder() string {
	for _, c := range qb.conditions {
		if strings.HasPrefix(c, "TRUE LIMIT ") {
			return strings.TrimPrefix(c, "TRUE LIMIT ")
		}
	}
	return ""
}

// rewriteLimit appends "LIMIT placeholder" to query if placeholder is non-empty.
// The caller already has the limit value in qb.args at the correct position.
func rewriteLimit(query, placeholder string) string {
	if placeholder == "" {
		return query
	}
	return query + " LIMIT " + placeholder
}

// nilIfEmpty returns nil when s is the empty string so pgx stores NULL instead
// of an empty TEXT value, which is more useful for optional columns.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
