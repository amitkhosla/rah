package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/amitkhosla/rah/internal/gatewaylog"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
)

// PostgresStore is a PostgreSQL-backed scheduler store.
type PostgresStore struct {
	pool       *pgxpool.Pool
	cronParser cron.Parser
}

// NewPostgresStore creates a PostgresStore and ensures the schema exists.
func NewPostgresStore(ctx context.Context, pool *pgxpool.Pool) (*PostgresStore, error) {
	s := &PostgresStore{
		pool:       pool,
		cronParser: cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) ensureSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS rah_system;
		CREATE TABLE IF NOT EXISTS rah_system.sch_schedules (
			name          TEXT PRIMARY KEY,
			cron          TEXT NOT NULL,
			flow_name     TEXT NOT NULL,
			tenant_alias  TEXT NOT NULL,
			constants     JSONB,
			enabled       BOOL NOT NULL DEFAULT true,
			on_failure    JSONB,
			max_concurrent INT NOT NULL DEFAULT 1,
			next_run_at   TIMESTAMPTZ,
			last_run_at   TIMESTAMPTZ,
			last_status   TEXT,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS rah_system.sch_execution_history (
			id            BIGSERIAL PRIMARY KEY,
			schedule_name TEXT NOT NULL,
			started_at    TIMESTAMPTZ NOT NULL,
			finished_at   TIMESTAMPTZ,
			status        TEXT NOT NULL,
			error_msg     TEXT,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			FOREIGN KEY (schedule_name) REFERENCES rah_system.sch_schedules(name) ON DELETE CASCADE
		);
		ALTER TABLE rah_system.sch_schedules ADD COLUMN IF NOT EXISTS timeout_sec INT NOT NULL DEFAULT 300;
		ALTER TABLE rah_system.sch_schedules ADD COLUMN IF NOT EXISTS claimed_by TEXT;
		ALTER TABLE rah_system.sch_schedules ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ;
		CREATE INDEX IF NOT EXISTS idx_sch_schedules_next_run ON rah_system.sch_schedules(next_run_at) WHERE enabled = true;
		CREATE INDEX IF NOT EXISTS idx_sch_exec_hist_name ON rah_system.sch_execution_history(schedule_name);
	`)
	return err
}

// Upsert inserts or updates a schedule and computes NextRunAt.
func (s *PostgresStore) Upsert(ctx context.Context, sc *Schedule) error {
	if sc.Cron == "" {
		return fmt.Errorf("cron expression required")
	}

	// Parse cron and compute next run time.
	schedule, err := s.cronParser.Parse(sc.Cron)
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	sc.NextRunAt = schedule.Next(time.Now())

	// Serialize optional fields as JSONB.
	var constantsJSON []byte
	if sc.Constants != nil {
		var err error
		constantsJSON, err = json.Marshal(sc.Constants)
		if err != nil {
			return fmt.Errorf("marshal constants: %w", err)
		}
	}

	var onFailureJSON []byte
	if sc.OnFailure.RetryCount > 0 || sc.OnFailure.RetryIntervalSec > 0 || sc.OnFailure.DeadLetterFlow != "" {
		var err error
		onFailureJSON, err = json.Marshal(sc.OnFailure)
		if err != nil {
			return fmt.Errorf("marshal on_failure: %w", err)
		}
	}

	// Upsert the schedule.
	_, err = s.pool.Exec(ctx, `
		INSERT INTO rah_system.sch_schedules (
			name, cron, flow_name, tenant_alias, constants, enabled, on_failure, max_concurrent,
			timeout_sec, next_run_at, last_run_at, last_status, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())
		ON CONFLICT (name) DO UPDATE SET
			cron = EXCLUDED.cron,
			flow_name = EXCLUDED.flow_name,
			tenant_alias = EXCLUDED.tenant_alias,
			constants = EXCLUDED.constants,
			enabled = EXCLUDED.enabled,
			on_failure = EXCLUDED.on_failure,
			max_concurrent = EXCLUDED.max_concurrent,
			timeout_sec = EXCLUDED.timeout_sec,
			next_run_at = EXCLUDED.next_run_at,
			last_run_at = EXCLUDED.last_run_at,
			last_status = EXCLUDED.last_status,
			updated_at = NOW()
	`,
		sc.Name, sc.Cron, sc.FlowName, sc.TenantAlias, constantsJSON, sc.Enabled, onFailureJSON,
		sc.MaxConcurrent, sc.TimeoutSec, sc.NextRunAt, sc.LastRunAt, sc.LastStatus,
	)
	return err
}

// Delete removes a schedule by name.
func (s *PostgresStore) Delete(ctx context.Context, name string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM rah_system.sch_schedules WHERE name = $1", name)
	return err
}

// ListDueWithin returns schedules whose NextRunAt is within the next windowSec seconds.
func (s *PostgresStore) ListDueWithin(ctx context.Context, windowSec int) ([]*ScheduledEvent, error) {
	now := time.Now()
	cutoff := now.Add(time.Duration(windowSec) * time.Second)

	rows, err := s.pool.Query(ctx, `
		SELECT name, cron, flow_name, tenant_alias,
		       COALESCE(constants, '{}'::jsonb),
		       timeout_sec,
		       on_failure,
		       next_run_at
		FROM rah_system.sch_schedules
		WHERE enabled = true
		  AND next_run_at >= $1
		  AND next_run_at < $2
		ORDER BY next_run_at ASC
	`, now, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*ScheduledEvent
	for rows.Next() {
		var name, cronExpr, flowName, tenantAlias string
		var constantsJSON, onFailureJSON []byte
		var timeoutSec int
		var scheduledAt time.Time

		if err := rows.Scan(&name, &cronExpr, &flowName, &tenantAlias, &constantsJSON, &timeoutSec, &onFailureJSON, &scheduledAt); err != nil {
			gatewaylog.Default.Error("scheduler postgres_store: rows.Scan error (row skipped)",
				gatewaylog.F("func", "ListDueWithin"),
				gatewaylog.F("error", err.Error()))
			continue
		}

		constants := make(map[string]string)
		if len(constantsJSON) > 0 {
			_ = json.Unmarshal(constantsJSON, &constants)
		}

		var onFailure struct {
			RetryCount       int    `json:"retry_count"`
			RetryIntervalSec int    `json:"retry_interval_sec"`
			DeadLetterFlow   string `json:"dead_letter_flow"`
		}
		if len(onFailureJSON) > 0 {
			_ = json.Unmarshal(onFailureJSON, &onFailure)
		}

		events = append(events, &ScheduledEvent{
			Name:             name,
			FlowName:         flowName,
			TenantAlias:      tenantAlias,
			TimeoutSec:       timeoutSec,
			Cron:             cronExpr,
			Constants:        constants,
			ScheduledAt:      scheduledAt,
			RetryCount:       onFailure.RetryCount,
			RetryIntervalSec: onFailure.RetryIntervalSec,
			DeadLetterFlow:   onFailure.DeadLetterFlow,
		})
	}

	return events, rows.Err()
}

// Claim attempts to atomically claim a schedule for this instance.
// Uses an UPDATE statement to set claimed_by and claimed_until atomically.
func (s *PostgresStore) Claim(ctx context.Context, name string, instanceID string) (ClaimResult, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE rah_system.sch_schedules
		SET    claimed_by    = $2,
		       claimed_until = NOW() + INTERVAL '5 minutes'
		WHERE  name          = $1
		  AND  enabled       = true
		  AND  (claimed_until IS NULL OR claimed_until < NOW())
	`, name, instanceID)
	if err != nil {
		return ClaimError, fmt.Errorf("claim %s: %w", name, err)
	}
	if tag.RowsAffected() == 0 {
		return ClaimLost, nil
	}
	return ClaimWon, nil
}

// RecordExecution writes an execution record and advances NextRunAt.
func (s *PostgresStore) RecordExecution(ctx context.Context, rec ExecutionRecord, nextRun time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	var commitErr error
	defer func() {
		rbErr := tx.Rollback(ctx)
		if commitErr != nil && rbErr != nil {
			gatewaylog.Default.Error("[Scheduler] rollback failed after commit error",
				gatewaylog.F("commit_err", commitErr.Error()),
				gatewaylog.F("rollback_err", rbErr.Error()),
			)
		}
	}()

	// Insert execution history record.
	_, err = tx.Exec(ctx, `
		INSERT INTO rah_system.sch_execution_history (schedule_name, started_at, finished_at, status, error_msg)
		VALUES ($1, $2, $3, $4, $5)
	`, rec.Name, rec.StartedAt, rec.FinishedAt, rec.Status, rec.Error)
	if err != nil {
		return fmt.Errorf("insert execution history: %w", err)
	}

	// Update schedule with new last run and next run times.
	// Clear claimed_by/claimed_until so the next fire cycle can be claimed immediately.
	_, err = tx.Exec(ctx, `
		UPDATE rah_system.sch_schedules
		SET last_run_at = $1, last_status = $2, next_run_at = $3, updated_at = NOW(),
		    claimed_by = NULL, claimed_until = NULL
		WHERE name = $4
	`, rec.FinishedAt, rec.Status, nextRun, rec.Name)
	if err != nil {
		return fmt.Errorf("update schedule: %w", err)
	}

	commitErr = tx.Commit(ctx)
	return commitErr
}

// ListAll returns all schedules (for management API).
func (s *PostgresStore) ListAll(ctx context.Context) ([]*Schedule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name, cron, flow_name, tenant_alias, constants, enabled, on_failure,
		       max_concurrent, timeout_sec, next_run_at, last_run_at, last_status
		FROM rah_system.sch_schedules
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schedules []*Schedule
	for rows.Next() {
		var sc Schedule
		var constantsJSON, onFailureJSON []byte
		var nextRunAt, lastRunAt *time.Time

		if err := rows.Scan(
			&sc.Name, &sc.Cron, &sc.FlowName, &sc.TenantAlias, &constantsJSON, &sc.Enabled,
			&onFailureJSON, &sc.MaxConcurrent, &sc.TimeoutSec, &nextRunAt, &lastRunAt, &sc.LastStatus,
		); err != nil {
			gatewaylog.Default.Error("scheduler postgres_store: rows.Scan error (row skipped)",
				gatewaylog.F("func", "ListAll"),
				gatewaylog.F("error", err.Error()))
			continue
		}

		if nextRunAt != nil {
			sc.NextRunAt = *nextRunAt
		}
		if lastRunAt != nil {
			sc.LastRunAt = *lastRunAt
		}

		if len(constantsJSON) > 0 {
			_ = json.Unmarshal(constantsJSON, &sc.Constants)
		}
		if len(onFailureJSON) > 0 {
			_ = json.Unmarshal(onFailureJSON, &sc.OnFailure)
		}

		schedules = append(schedules, &sc)
	}

	return schedules, rows.Err()
}

// ListHistory returns recent execution records for a schedule.
func (s *PostgresStore) ListHistory(ctx context.Context, name string, limit int) ([]ExecutionRecord, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.pool.Query(ctx, `
		SELECT schedule_name, started_at, finished_at, status, error_msg
		FROM rah_system.sch_execution_history
		WHERE schedule_name = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, name, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []ExecutionRecord
	for rows.Next() {
		var rec ExecutionRecord
		if err := rows.Scan(&rec.Name, &rec.StartedAt, &rec.FinishedAt, &rec.Status, &rec.Error); err != nil {
			gatewaylog.Default.Error("scheduler postgres_store: rows.Scan error (row skipped)",
				gatewaylog.F("func", "ListHistory"),
				gatewaylog.F("error", err.Error()))
			continue
		}
		records = append(records, rec)
	}

	// Reverse to get oldest-to-newest order.
	if len(records) > 1 {
		for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
			records[i], records[j] = records[j], records[i]
		}
	}

	return records, rows.Err()
}
