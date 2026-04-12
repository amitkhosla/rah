package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
    window      TEXT   NOT NULL,
    dimension   TEXT   NOT NULL,
    req_total   BIGINT,
    req_5xx     BIGINT,
    lat_p50_ms  REAL,
    lat_p95_ms  REAL,
    lat_p99_ms  REAL,
    bytes_in    BIGINT,
    bytes_out   BIGINT,
    PRIMARY KEY (ts, window, dimension)
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
    (ts,window,dimension,req_total,req_5xx,lat_p50_ms,lat_p95_ms,lat_p99_ms,bytes_in,bytes_out)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (ts, window, dimension) DO UPDATE SET
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

// ── WriteTrace ────────────────────────────────────────────────────────────────

// WriteTrace persists a single trace record. If a record with the same
// trace_id already exists it is silently skipped (DO NOTHING).
func (s *postgresObsStore) WriteTrace(ctx context.Context, trace TraceRecord) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO obs_traces (trace_id,ts,api_name,tenant_id,status,total_ms,payload)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (trace_id) DO NOTHING`,
		int64(trace.TraceID),
		trace.Timestamp,
		nilIfEmpty(trace.ApiName),
		int16(trace.TenantID),
		int16(trace.Status),
		float32(trace.TotalMs),
		trace.Payload,
	)
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
		var extraJSON []byte

		if err := rows.Scan(
			&r.TimestampNs, &r.ApiName, &tenantID, &r.TenantKey,
			&r.Method, &r.Path, &status,
			&totalMs, &gatewayMs, &upstreamMs, &ttfbMs,
			&r.ReqBytes, &r.ResBytes, &extraJSON,
		); err != nil {
			return nil, err
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
		qb.add("window = $%d", f.Window)
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

	query := `SELECT ts,window,dimension,req_total,req_5xx,lat_p50_ms,lat_p95_ms,lat_p99_ms,bytes_in,bytes_out ` +
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
// Limit defaults to 50 and is capped at 500.
func (s *postgresObsStore) QueryTraces(ctx context.Context, f TraceFilter) ([]TraceRecord, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

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
