package registry

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditConsumerConfig holds tuning parameters for the registry audit consumer.
type AuditConsumerConfig struct {
	PollInterval time.Duration
	BatchSize    int
}

// AuditConsumer polls the registry_audit table for changes written by other
// gateway instances and applies them to the local in-memory RegistryManager
// without re-persisting (which would create a cascade loop).
type AuditConsumer struct {
	pool       *pgxpool.Pool
	nodeID     string
	mgr        *RegistryManager
	writer     *AuditWriter
	cfg        AuditConsumerConfig
	checkpoint int64
}

// NewAuditConsumer creates a new AuditConsumer. Call Start() to begin polling.
func NewAuditConsumer(pool *pgxpool.Pool, nodeID string, mgr *RegistryManager, writer *AuditWriter, cfg AuditConsumerConfig) *AuditConsumer {
	return &AuditConsumer{
		pool:   pool,
		nodeID: nodeID,
		mgr:    mgr,
		writer: writer,
		cfg:    cfg,
	}
}

// Start loads the checkpoint then polls registry_audit until ctx is cancelled.
// snapshotSeq is the max seq in registry_audit at the time RestoreFromSnapshot
// completed; rows at or below this sequence are skipped (already in snapshot).
func (c *AuditConsumer) Start(ctx context.Context, snapshotSeq int64) {
	// Apply defaults.
	if c.cfg.PollInterval == 0 {
		c.cfg.PollInterval = 5 * time.Second
	}
	if c.cfg.BatchSize == 0 {
		c.cfg.BatchSize = 1000
	}

	savedSeq, _ := c.writer.LoadCheckpoint(ctx)
	if savedSeq > snapshotSeq {
		c.checkpoint = savedSeq
	} else {
		c.checkpoint = snapshotSeq
	}
	log.Printf("[registry-audit] consumer starting from seq=%d", c.checkpoint)

	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.poll(ctx)
		case <-ctx.Done():
			// Save checkpoint before exit.
			_ = c.writer.SaveCheckpoint(ctx, c.checkpoint)
			return
		}
	}
}

// poll fetches one batch of audit rows after the current checkpoint and applies them.
func (c *AuditConsumer) poll(ctx context.Context) {
	const query = `SELECT seq, op, key, value FROM registry_audit
		WHERE seq > $1 AND node_id != $2
		ORDER BY seq LIMIT $3`

	rows, err := c.pool.Query(ctx, query, c.checkpoint, c.nodeID, c.cfg.BatchSize)
	if err != nil {
		if isTableNotExists(err) {
			log.Printf("[registry-audit] table registry_audit does not exist yet — skipping poll")
			return
		}
		log.Printf("[registry-audit] poll query error: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var seq int64
		var op, key string
		var value []byte
		if err := rows.Scan(&seq, &op, &key, &value); err != nil {
			log.Printf("[registry-audit] scan error: %v", err)
			continue
		}
		switch op {
		case "PUT":
			c.mgr.ApplyStorePut(key, value)
		case "DELETE":
			c.mgr.ApplyStoreDelete(key)
		default:
			log.Printf("[registry-audit] unknown op %q for key %q seq=%d — skipping", op, key, seq)
		}
		c.checkpoint = seq
	}
	if err := rows.Err(); err != nil {
		if isTableNotExists(err) {
			log.Printf("[registry-audit] table registry_audit does not exist yet — skipping poll")
			return
		}
		log.Printf("[registry-audit] rows error after scan: %v", err)
	}

	// Save checkpoint after every poll tick (even if no rows were returned).
	if err := c.writer.SaveCheckpoint(ctx, c.checkpoint); err != nil {
		log.Printf("[registry-audit] checkpoint save error: %v", err)
	}
}

// isTableNotExists reports whether err is a PostgreSQL "relation does not exist" error
// (SQLSTATE 42P01) or a message-level string match for environments where the error
// wrapping strips the PgError.
func isTableNotExists(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01"
	}
	return strings.Contains(err.Error(), "relation") && strings.Contains(err.Error(), "does not exist")
}
