package registry

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditWriter appends immutable audit rows to registry_audit in the same
// Postgres transaction as the KV write, providing a full ordered history of
// every registry mutation.
type AuditWriter struct {
	pool   *pgxpool.Pool
	nodeID string
}

// NewAuditWriter creates an AuditWriter backed by the given pool and tagged
// with nodeID (e.g. the gateway instance fingerprint).
func NewAuditWriter(pool *pgxpool.Pool, nodeID string) *AuditWriter {
	return &AuditWriter{pool: pool, nodeID: nodeID}
}

// EnsureSchema creates registry_audit and gateway_checkpoints if they don't exist.
// Safe to call multiple times; uses IF NOT EXISTS guards.
func (a *AuditWriter) EnsureSchema(ctx context.Context) error {
	_, _ = a.pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS rah_system`)

	_, _ = a.pool.Exec(ctx, `DO $$ BEGIN
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname='public' AND tablename='registry_audit')
    AND NOT EXISTS (SELECT FROM pg_tables WHERE schemaname='rah_system' AND tablename='registry_audit')
    THEN ALTER TABLE public.registry_audit SET SCHEMA rah_system; END IF;
END $$`)
	_, _ = a.pool.Exec(ctx, `DO $$ BEGIN
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname='public' AND tablename='gateway_checkpoints')
    AND NOT EXISTS (SELECT FROM pg_tables WHERE schemaname='rah_system' AND tablename='gateway_checkpoints')
    THEN ALTER TABLE public.gateway_checkpoints SET SCHEMA rah_system; END IF;
END $$`)

	_, err := a.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS rah_system.registry_audit (
			seq     BIGSERIAL    PRIMARY KEY,
			op      VARCHAR(6)   NOT NULL,
			key     TEXT         NOT NULL,
			value   BYTEA,
			node_id VARCHAR(64)  NOT NULL,
			ts      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS registry_audit_seq_node ON rah_system.registry_audit (seq, node_id);

		CREATE TABLE IF NOT EXISTS rah_system.gateway_checkpoints (
			node_id          VARCHAR(64) PRIMARY KEY,
			last_applied_seq BIGINT      NOT NULL DEFAULT 0,
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	return err
}

// WritePut appends a PUT row inside an existing transaction.
// Must be called within the same transaction as the KV upsert.
func (a *AuditWriter) WritePut(ctx context.Context, tx pgx.Tx, key string, value []byte) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO rah_system.registry_audit(op, key, value, node_id) VALUES('PUT', $1, $2, $3)`,
		key, value, a.nodeID,
	)
	return err
}

// WriteDelete appends a DELETE row inside an existing transaction.
// Must be called within the same transaction as the KV delete.
func (a *AuditWriter) WriteDelete(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO rah_system.registry_audit(op, key, value, node_id) VALUES('DELETE', $1, NULL, $2)`,
		key, a.nodeID,
	)
	return err
}

// LoadCheckpoint returns the last_applied_seq for this node.
// Returns 0 if no checkpoint row exists yet.
func (a *AuditWriter) LoadCheckpoint(ctx context.Context) (int64, error) {
	var seq int64
	err := a.pool.QueryRow(ctx,
		`SELECT last_applied_seq FROM rah_system.gateway_checkpoints WHERE node_id = $1`,
		a.nodeID,
	).Scan(&seq)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return seq, nil
}

// SaveCheckpoint upserts the checkpoint row for this node.
func (a *AuditWriter) SaveCheckpoint(ctx context.Context, seq int64) error {
	_, err := a.pool.Exec(ctx,
		`INSERT INTO rah_system.gateway_checkpoints(node_id, last_applied_seq, updated_at)
		 VALUES($1, $2, $3)
		 ON CONFLICT(node_id) DO UPDATE
		   SET last_applied_seq = EXCLUDED.last_applied_seq,
		       updated_at       = EXCLUDED.updated_at`,
		a.nodeID, seq, time.Now().UTC(),
	)
	return err
}
