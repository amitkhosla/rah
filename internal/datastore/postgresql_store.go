package datastore

import (
	"context"
	"fmt"
	"github.com/amitkhosla/rah/internal/config"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type postgresqlStore struct {
	name   string
	domain string
	pool   *pgxpool.Pool
}

func newPostgreSQLStore(cfg config.StoreConfig, domain string) (KeyValueStore, error) {
	connString := buildConnString(cfg.Connection)

	poolCfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("postgresql: parse config: %w", err)
	}

	if cfg.Connection.Params != nil {
		if raw, ok := cfg.Connection.Params["pool_max_open"]; ok {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				poolCfg.MaxConns = int32(n)
			}
		}
		if raw, ok := cfg.Connection.Params["pool_min_open"]; ok {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				poolCfg.MinConns = int32(n)
			}
		}
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgresql: connect: %w", err)
	}

	store := &postgresqlStore{
		name:   cfg.Name,
		domain: domain,
		pool:   pool,
	}

	if err := store.ensureSchema(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgresql: ensure schema: %w", err)
	}

	return store, nil
}

func buildConnString(cfg config.StoreConnection) string {
	addr := strings.TrimSpace(cfg.EffectiveAddress())
	// Full DSN / URL â€” pass through directly
	if strings.HasPrefix(addr, "postgres://") || strings.HasPrefix(addr, "postgresql://") {
		return addr
	}
	// host:port format â€” split and pass separately
	host := addr
	port := ""
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
		port = addr[i+1:]
	}
	if port != "" {
		return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			host, port, cfg.Username, cfg.Password, cfg.Database)
	}
	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable",
		host, cfg.Username, cfg.Password, cfg.Database)
}

func (s *postgresqlStore) ensureSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS kv_entries (
			full_key TEXT PRIMARY KEY,
			value    BYTEA NOT NULL
		)
	`)
	return err
}

func (s *postgresqlStore) Put(ctx context.Context, tenant Tenant, key string, value []byte) error {
	fullKey, err := BuildScopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO kv_entries(full_key, value) VALUES($1,$2)
		 ON CONFLICT(full_key) DO UPDATE SET value=EXCLUDED.value`,
		fullKey, value,
	)
	return err
}

func (s *postgresqlStore) Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error) {
	fullKey, err := BuildScopedKey(tenant, s.domain, key)
	if err != nil {
		return nil, false, err
	}
	var value []byte
	err = s.pool.QueryRow(ctx,
		`SELECT value FROM kv_entries WHERE full_key=$1`, fullKey,
	).Scan(&value)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return value, true, nil
}

func (s *postgresqlStore) Delete(ctx context.Context, tenant Tenant, key string) error {
	fullKey, err := BuildScopedKey(tenant, s.domain, key)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`DELETE FROM kv_entries WHERE full_key=$1`, fullKey,
	)
	return err
}

func (s *postgresqlStore) ListKeys(ctx context.Context, tenant Tenant, prefix string) ([]string, error) {
	scopedPrefix, err := BuildScopedPrefix(tenant, s.domain, prefix)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT full_key FROM kv_entries WHERE full_key LIKE $1`,
		scopedPrefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var fullKey string
		if err := rows.Scan(&fullKey); err != nil {
			return nil, err
		}
		if strings.HasPrefix(fullKey, scopedPrefix) {
			keys = append(keys, fullKey[len(scopedPrefix):])
		}
	}
	return keys, rows.Err()
}

// MultiGet fetches multiple keys in a single round-trip using ANY($1).
// Missing keys are simply absent from the result map; no error is returned for them.
func (s *postgresqlStore) MultiGet(ctx context.Context, tenant Tenant, keys []string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	fullKeys := make([]string, len(keys))
	for i, k := range keys {
		fk, err := BuildScopedKey(tenant, s.domain, k)
		if err != nil {
			return nil, err
		}
		fullKeys[i] = fk
	}
	rows, err := s.pool.Query(ctx,
		`SELECT full_key, value FROM kv_entries WHERE full_key = ANY($1)`,
		fullKeys,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	prefix, _ := BuildScopedPrefix(tenant, s.domain, "")
	result := make(map[string][]byte, len(keys))
	for rows.Next() {
		var fullKey string
		var value []byte
		if err := rows.Scan(&fullKey, &value); err != nil {
			return nil, err
		}
		result[strings.TrimPrefix(fullKey, prefix)] = value
	}
	return result, rows.Err()
}

// MultiPut writes all key-value pairs in a single round-trip using unnest + upsert.
func (s *postgresqlStore) MultiPut(ctx context.Context, tenant Tenant, kvs map[string][]byte) error {
	if len(kvs) == 0 {
		return nil
	}
	fullKeys := make([]string, 0, len(kvs))
	values := make([][]byte, 0, len(kvs))
	for k, v := range kvs {
		fk, err := BuildScopedKey(tenant, s.domain, k)
		if err != nil {
			return err
		}
		fullKeys = append(fullKeys, fk)
		values = append(values, v)
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO kv_entries(full_key, value)
		 SELECT * FROM unnest($1::text[], $2::bytea[])
		 ON CONFLICT(full_key) DO UPDATE SET value = EXCLUDED.value`,
		fullKeys, values,
	)
	return err
}

func (s *postgresqlStore) Kind() string { return "postgresql" }
func (s *postgresqlStore) Name() string { return s.name }

func (s *postgresqlStore) PoolStats() PoolStats {
	stat := s.pool.Stat()
	return PoolStats{
		MaxOpen: int64(stat.MaxConns()),
		InUse:   int64(stat.AcquiredConns()),
		Waiters: 0,
	}
}

func (s *postgresqlStore) Close() error {
	s.pool.Close()
	return nil
}
