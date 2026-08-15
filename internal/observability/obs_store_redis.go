package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// redisObsStore persists observability data to Redis / Dragonfly.
//
// Data layout:
//
//	Access log:       LPUSH obs:log:{api_name}    {json}  — LTRIM to 10 000 entries
//	Metric snapshots: HSET  obs:m:{window}:{dimension}:{bucket} field value ...
//	Traces:           LPUSH obs:traces             {json}  — LTRIM to 10 000 entries
//
// Redis is the "fast but less queryable" backend; it does not support rich
// time-range or field-equality filtering beyond key-level lookups.
type redisObsStore struct {
	client  goredis.Cmdable
	closeFn func() error
}

const (
	redisMaxLogEntries   = 10_000
	redisMaxTraceEntries = 10_000
	// Metric hash TTL: 25 hours so hourly buckets survive a day.
	redisMetricTTL = 25 * time.Hour
)

// NewRedisObsStore creates an ObsStore backed by a single Redis instance.
// addr is a "host:port" string (e.g. "localhost:6379").
// password may be empty. poolSize <= 0 defaults to 16.
func NewRedisObsStore(ctx context.Context, addr, password string, poolSize int) (ObsStore, error) {
	if poolSize <= 0 {
		poolSize = 16
	}
	c := goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: password,
		PoolSize: poolSize,
	})
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("obs redis: ping: %w", err)
	}
	return &redisObsStore{client: c, closeFn: c.Close}, nil
}

// NewRedisObsStoreFromClient wraps an existing go-redis Cmdable (useful when
// the caller already manages the Redis connection, e.g. in tests or when
// sharing a connection with the cache layer).
// closeFn is called by Close(); pass nil to make Close a no-op.
func NewRedisObsStoreFromClient(client goredis.Cmdable, closeFn func() error) ObsStore {
	if closeFn == nil {
		closeFn = func() error { return nil }
	}
	return &redisObsStore{client: client, closeFn: closeFn}
}

// ── WriteAccessLog ────────────────────────────────────────────────────────────

// WriteAccessLog pushes each record as a JSON string to
// "obs:log:{api_name}" and trims the list to the last 10 000 entries.
// Records with an empty ApiName are stored under the key "obs:log:_".
func (s *redisObsStore) WriteAccessLog(ctx context.Context, records []AccessLogRecord) error {
	if len(records) == 0 {
		return nil
	}

	// Group by api_name to minimise the number of LPUSH commands.
	byKey := make(map[string][]any, 4)
	for _, r := range records {
		key := redisLogKey(r.ApiName)
		b, err := json.Marshal(r)
		if err != nil {
			continue
		}
		byKey[key] = append(byKey[key], string(b))
	}

	pipe := s.client.Pipeline()
	for key, vals := range byKey {
		pipe.LPush(ctx, key, vals...)
		pipe.LTrim(ctx, key, 0, redisMaxLogEntries-1)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// ── WriteMetricSnapshot ───────────────────────────────────────────────────────

// WriteMetricSnapshot stores a metric snapshot in a Redis hash keyed by
// "obs:m:{window}:{dimension}:{bucket}" where bucket is the unix timestamp
// truncated to the window granularity.  Each field is stored individually
// so partial updates and HINCRBY are possible in future extensions.
func (s *redisObsStore) WriteMetricSnapshot(ctx context.Context, snap MetricSnapshot) error {
	key := redisMetricKey(snap.Window, snap.Dimension, snap.Timestamp)
	pipe := s.client.Pipeline()
	pipe.HSet(ctx, key,
		"ts", snap.Timestamp,
		"window", snap.Window,
		"dimension", snap.Dimension,
		"req_total", snap.ReqTotal,
		"req_5xx", snap.Req5xx,
		"lat_p50_ms", fmt.Sprintf("%.4f", snap.LatP50Ms),
		"lat_p95_ms", fmt.Sprintf("%.4f", snap.LatP95Ms),
		"lat_p99_ms", fmt.Sprintf("%.4f", snap.LatP99Ms),
		"bytes_in", snap.BytesIn,
		"bytes_out", snap.BytesOut,
	)
	pipe.Expire(ctx, key, redisMetricTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// ── WriteTrace ────────────────────────────────────────────────────────────────

// WriteTraceBatch pushes each trace record as JSON to "obs:traces" and trims to 10 000.
func (s *redisObsStore) WriteTraceBatch(ctx context.Context, records []TraceRecord) error {
	if len(records) == 0 {
		return nil
	}
	pipe := s.client.Pipeline()
	for _, trace := range records {
		b, err := json.Marshal(trace)
		if err != nil {
			continue
		}
		pipe.LPush(ctx, "obs:traces", string(b))
	}
	pipe.LTrim(ctx, "obs:traces", 0, redisMaxTraceEntries-1)
	_, err := pipe.Exec(ctx)
	return err
}

// ── QueryAccessLog ────────────────────────────────────────────────────────────

// QueryAccessLog reads the access log list for a single ApiName from Redis.
// If f.ApiName is empty it reads from "obs:log:_".
// Time-range, tenant, and status filters are applied in-process after retrieval.
// Limit defaults to 100 (max 1000).
func (s *redisObsStore) QueryAccessLog(ctx context.Context, f AccessLogFilter) ([]AccessLogRecord, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	key := redisLogKey(f.ApiName)
	// Fetch more than needed so in-process filtering can still satisfy limit.
	fetchN := int64(limit * 10)
	if fetchN > redisMaxLogEntries {
		fetchN = redisMaxLogEntries
	}

	raw, err := s.client.LRange(ctx, key, 0, fetchN-1).Result()
	if err != nil {
		return nil, err
	}

	fromNs := f.FromUnixS * 1_000_000_000
	toNs := f.ToUnixS * 1_000_000_000

	out := make([]AccessLogRecord, 0, limit)
	for _, item := range raw {
		if len(out) >= limit {
			break
		}
		var r AccessLogRecord
		if err := json.Unmarshal([]byte(item), &r); err != nil {
			continue
		}
		if f.TenantKey != "" && r.TenantKey != f.TenantKey {
			continue
		}
		if f.Status > 0 && r.Status != f.Status {
			continue
		}
		if fromNs > 0 && r.TimestampNs < fromNs {
			continue
		}
		if toNs > 0 && r.TimestampNs > toNs {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// ── QueryMetrics ──────────────────────────────────────────────────────────────

// QueryMetrics scans for metric hash keys matching the window and dimension
// prefix via the Redis SCAN command, then fetches each hash with HGETALL.
// Note: SCAN is O(keyspace) and may be slow on large Redis instances.
func (s *redisObsStore) QueryMetrics(ctx context.Context, f MetricsFilter) ([]MetricSnapshot, error) {
	pattern := redisMetricPattern(f.Window, f.Dimension)

	var (
		cursor uint64
		keys   []string
	)
	for {
		batch, next, err := s.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}

	out := make([]MetricSnapshot, 0, len(keys))
	for _, key := range keys {
		fields, err := s.client.HGetAll(ctx, key).Result()
		if err != nil || len(fields) == 0 {
			continue
		}
		snap := parseMetricHash(fields)
		if f.FromUnixS > 0 && snap.Timestamp < f.FromUnixS {
			continue
		}
		if f.ToUnixS > 0 && snap.Timestamp > f.ToUnixS {
			continue
		}
		out = append(out, snap)
	}
	return out, nil
}

// ── QueryTraces ───────────────────────────────────────────────────────────────

// QueryTraces reads from the global "obs:traces" list and applies filters
// in-process.  Limit defaults to 50 (max 500).
func (s *redisObsStore) QueryTraces(ctx context.Context, f TraceFilter) ([]TraceRecord, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	fetchN := int64(limit * 10)
	if fetchN > redisMaxTraceEntries {
		fetchN = redisMaxTraceEntries
	}

	raw, err := s.client.LRange(ctx, "obs:traces", 0, fetchN-1).Result()
	if err != nil {
		return nil, err
	}

	out := make([]TraceRecord, 0, limit)
	for _, item := range raw {
		if len(out) >= limit {
			break
		}
		var t TraceRecord
		if err := json.Unmarshal([]byte(item), &t); err != nil {
			continue
		}
		if f.ApiName != "" && t.ApiName != f.ApiName {
			continue
		}
		if f.TenantID > 0 && t.TenantID != f.TenantID {
			continue
		}
		if f.MinMs > 0 && t.TotalMs < f.MinMs {
			continue
		}
		if f.FromUnixS > 0 && t.Timestamp < f.FromUnixS {
			continue
		}
		if f.ToUnixS > 0 && t.Timestamp > f.ToUnixS {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// ── UpsertInstrSchema ─────────────────────────────────────────────────────────

// UpsertInstrSchema is a no-op for Redis (V1 store).
func (s *redisObsStore) UpsertInstrSchema(_ context.Context, _ []InstrSchemaRow) error {
	return nil
}

// ── QueryInstrSchema ──────────────────────────────────────────────────────────

// QueryInstrSchema returns nil for Redis (V1 store).
func (s *redisObsStore) QueryInstrSchema(_ context.Context, _ string) ([]InstrSchemaRow, error) {
	return nil, nil
}

// ── UpsertVarSchema / QueryVarSchema ─────────────────────────────────────────

// UpsertVarSchema is a no-op for Redis (payloads too large for Redis).
func (s *redisObsStore) UpsertVarSchema(_ context.Context, _ []VarSchemaRow) error { return nil }

// QueryVarSchema returns nil for Redis (V1 store).
func (s *redisObsStore) QueryVarSchema(_ context.Context, _ string) ([]VarSchemaRow, error) {
	return nil, nil
}

// ── WritePayloadBatch / QueryPayloads ─────────────────────────────────────────

// WritePayloadBatch is a no-op for the Redis store (V1 — payload bytes stored elsewhere).
func (s *redisObsStore) WritePayloadBatch(_ context.Context, _ []PayloadRecord) error { return nil }

// QueryPayloads returns nil for the Redis store (V1 — not supported).
func (s *redisObsStore) QueryPayloads(_ context.Context, _ uint64) ([]PayloadRecord, error) {
	return nil, nil
}

// GetDistinctApps returns an empty slice for the Redis store (not supported).
func (s *redisObsStore) GetDistinctApps(_ context.Context, _ int64) ([]string, error) {
	return []string{}, nil
}

// ── Close ─────────────────────────────────────────────────────────────────────

func (s *redisObsStore) Close() error { return s.closeFn() }

// ── key helpers ───────────────────────────────────────────────────────────────

func redisLogKey(apiName string) string {
	if apiName == "" {
		return "obs:log:_"
	}
	return "obs:log:" + apiName
}

// redisMetricKey builds "obs:m:{window}:{dimension}:{bucket}".
func redisMetricKey(window, dimension string, ts int64) string {
	return "obs:m:" + window + ":" + dimension + ":" + strconv.FormatInt(ts, 10)
}

// redisMetricPattern returns a SCAN glob for the given window and dimension
// prefix.  Empty fields are replaced with "*".
func redisMetricPattern(window, dimension string) string {
	w := window
	if w == "" {
		w = "*"
	}
	d := dimension
	if d == "" {
		d = "*"
	} else if strings.HasSuffix(d, ":") {
		d = d + "*"
	}
	return "obs:m:" + w + ":" + d + ":*"
}

// parseMetricHash reconstructs a MetricSnapshot from a Redis HGETALL result.
func parseMetricHash(h map[string]string) MetricSnapshot {
	parseInt := func(s string) int64 {
		v, _ := strconv.ParseInt(s, 10, 64)
		return v
	}
	parseFloat := func(s string) float64 {
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	return MetricSnapshot{
		Timestamp: parseInt(h["ts"]),
		Window:    h["window"],
		Dimension: h["dimension"],
		ReqTotal:  parseInt(h["req_total"]),
		Req5xx:    parseInt(h["req_5xx"]),
		LatP50Ms:  parseFloat(h["lat_p50_ms"]),
		LatP95Ms:  parseFloat(h["lat_p95_ms"]),
		LatP99Ms:  parseFloat(h["lat_p99_ms"]),
		BytesIn:   parseInt(h["bytes_in"]),
		BytesOut:  parseInt(h["bytes_out"]),
	}
}
