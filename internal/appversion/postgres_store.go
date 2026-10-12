package appversion

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements Store using a shared Postgres database.
// Tables are created in the rah_system schema on first use via ensureSchema.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a store and ensures the schema exists.
func NewPostgresStore(ctx context.Context, pool *pgxpool.Pool) (*PostgresStore, error) {
	s := &PostgresStore{pool: pool}
	if err := s.ensureSchema(ctx); err != nil {
		return nil, fmt.Errorf("appversion: ensureSchema: %w", err)
	}
	return s, nil
}

func (s *PostgresStore) ensureSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
CREATE SCHEMA IF NOT EXISTS rah_system;

CREATE TABLE IF NOT EXISTS rah_system.app_versions (
    app_name     TEXT        PRIMARY KEY,
    logical_app  TEXT        NOT NULL,
    version      TEXT        NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'draft',
    bundle_hash  TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_av_logical
    ON rah_system.app_versions(logical_app, status);

CREATE TABLE IF NOT EXISTS rah_system.tenant_app_assignments (
    tenant_id    SMALLINT,
    logical_app  TEXT        NOT NULL,
    environment  TEXT        NOT NULL DEFAULT 'production',
    active_app   TEXT        NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by   TEXT,
    CONSTRAINT pk_taa PRIMARY KEY (COALESCE(tenant_id, -1::SMALLINT), logical_app, environment),
    FOREIGN KEY (active_app) REFERENCES rah_system.app_versions(app_name)
);
CREATE INDEX IF NOT EXISTS idx_taa_resolve
    ON rah_system.tenant_app_assignments(logical_app, environment, tenant_id NULLS LAST);
`)
	return err
}

func (s *PostgresStore) UpsertVersion(ctx context.Context, v AppVersion) error {
	now := time.Now()
	_, err := s.pool.Exec(ctx, `
INSERT INTO rah_system.app_versions (app_name, logical_app, version, status, bundle_hash, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (app_name) DO UPDATE SET
    logical_app  = EXCLUDED.logical_app,
    version      = EXCLUDED.version,
    status       = EXCLUDED.status,
    bundle_hash  = EXCLUDED.bundle_hash,
    updated_at   = EXCLUDED.updated_at
`, v.AppName, v.LogicalApp, v.Version, v.Status, v.BundleHash, now, now)
	return err
}

func (s *PostgresStore) GetVersion(ctx context.Context, appName string) (*AppVersion, error) {
	row := s.pool.QueryRow(ctx, `
SELECT app_name, logical_app, version, status, bundle_hash, created_at, updated_at
FROM rah_system.app_versions WHERE app_name = $1
`, appName)
	var v AppVersion
	err := row.Scan(&v.AppName, &v.LogicalApp, &v.Version, &v.Status, &v.BundleHash, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("appversion: version %q not found", appName)
		}
		return nil, err
	}
	return &v, nil
}

func (s *PostgresStore) ListVersions(ctx context.Context, logicalApp string) ([]AppVersion, error) {
	rows, err := s.pool.Query(ctx, `
SELECT app_name, logical_app, version, status, bundle_hash, created_at, updated_at
FROM rah_system.app_versions WHERE logical_app = $1 ORDER BY created_at DESC
`, logicalApp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppVersion
	for rows.Next() {
		var v AppVersion
		if err := rows.Scan(&v.AppName, &v.LogicalApp, &v.Version, &v.Status, &v.BundleHash, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PostgresStore) RetireVersion(ctx context.Context, appName string) error {
	// Block if any routing rule still references this version.
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM rah_system.tenant_app_assignments WHERE active_app = $1`, appName,
	).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("appversion: cannot retire %q — still referenced by %d routing rule(s)", appName, count)
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE rah_system.app_versions SET status = 'retired', updated_at = NOW() WHERE app_name = $1`, appName)
	return err
}

func (s *PostgresStore) UpsertRoutingRule(ctx context.Context, r RoutingRule) error {
	var tenantID interface{}
	if r.TenantID != nil {
		tenantID = *r.TenantID
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO rah_system.tenant_app_assignments (tenant_id, logical_app, environment, active_app, updated_at, updated_by)
VALUES ($1, $2, $3, $4, NOW(), $5)
ON CONFLICT ON CONSTRAINT pk_taa DO UPDATE SET
    active_app = EXCLUDED.active_app,
    updated_at = NOW(),
    updated_by = EXCLUDED.updated_by
`, tenantID, r.LogicalApp, r.Environment, r.ActiveApp, r.UpdatedBy)
	return err
}

func (s *PostgresStore) DeleteRoutingRule(ctx context.Context, tenantID *uint16, logicalApp, env string) error {
	var tid interface{}
	if tenantID != nil {
		tid = *tenantID
	}
	_, err := s.pool.Exec(ctx, `
DELETE FROM rah_system.tenant_app_assignments
WHERE logical_app = $1 AND environment = $2
  AND (($3::SMALLINT IS NULL AND tenant_id IS NULL) OR tenant_id = $3)
`, logicalApp, env, tid)
	return err
}

func (s *PostgresStore) ListRoutingRules(ctx context.Context, logicalApp string) ([]RoutingRule, error) {
	rows, err := s.pool.Query(ctx, `
SELECT tenant_id, logical_app, environment, active_app, updated_at, COALESCE(updated_by, '')
FROM rah_system.tenant_app_assignments WHERE logical_app = $1
ORDER BY tenant_id NULLS LAST
`, logicalApp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoutingRule
	for rows.Next() {
		var r RoutingRule
		if err := rows.Scan(&r.TenantID, &r.LogicalApp, &r.Environment, &r.ActiveApp, &r.UpdatedAt, &r.UpdatedBy); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ResolveRoute(ctx context.Context, tenantID uint16, logicalApp, env string) (string, error) {
	var activeApp string
	err := s.pool.QueryRow(ctx, `
SELECT active_app FROM rah_system.tenant_app_assignments
WHERE logical_app = $1 AND environment = $2
  AND (tenant_id = $3 OR tenant_id IS NULL)
ORDER BY tenant_id NULLS LAST
LIMIT 1
`, logicalApp, env, tenantID).Scan(&activeApp)
	if err != nil {
		if err == pgx.ErrNoRows {
			return logicalApp, nil // no rule — use logical app name as-is
		}
		return "", err
	}
	return activeApp, nil
}

// Compile-time interface check.
var _ Store = (*PostgresStore)(nil)
