package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
		constantsJSON, _ = json.Marshal(sc.Constants)
	}

	var onFailureJSON []byte
	if sc.OnFailure.RetryCount > 0 || sc.OnFailure.RetryIntervalSec > 0 || sc.OnFailure.DeadLetterFlow != "" {
		onFailureJSON, _ = json.Marshal(sc.OnFailure)
	}

	// Upsert the schedule.
	_, err = s.pool.Exec(ctx, `
		INSERT INTO rah_system.sch_schedules (
			name, cron, flow_name, tenant_alias, constants, enabled, on_failure, max_concurrent,
			next_run_at, last_run_at, last_status, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		ON CONFLICT (name) DO UPDATE SET
			cron = EXCLUDED.cron,
			flow_name = EXCLUDED.flow_name,
			tenant_alias = EXCLUDED.tenant_alias,
			constants = EXCLUDED.constants,
			enabled = EXCLUDED.enabled,
			on_failure = EXCLUDED.on_failure,
			max_concurrent = EXCLUDED.max_concurrent,
			next_run_at = EXCLUDED.next_run_at,
			last_run_at = EXCLUDED.last_run_at,
			last_status = EXCLUDED.last_status,
			updated_at = NOW()
	`,
		sc.Name, sc.Cron, sc.FlowName, sc.TenantAlias, constantsJSON, sc.Enabled, onFailureJSON,
		sc.MaxConcurrent, sc.NextRunAt, sc.LastRunAt, sc.LastStatus,
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
		SELECT name, flow_name, tenant_alias, COALESCE(constants, '{}'::jsonb),
		       COALESCE((constants->>'timeout_sec')::int, 0) as timeout_sec,
		       next_run_at
		FROM rah_system.sch_schedules
		WHERE enabled = true
		  AND next_run_at > $1
		  AND next_run_at < $2
		ORDER BY next_run_at ASC
	`, now, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*ScheduledEvent
	for rows.Next() {
		var name, flowName, tenantAlias string
		var constantsJSON []byte
		var timeoutSec int
		var scheduledAt time.Time

		if err := rows.Scan(&name, &flowName, &tenantAlias, &constantsJSON, &timeoutSec, &scheduledAt); err != nil {
			continue
		}

		constants := make(map[string]string)
		if len(constantsJSON) > 0 {
			_ = json.Unmarshal(constantsJSON, &constants)
		}

		events = append(events, &ScheduledEvent{
			Name:        name,
			FlowName:    flowName,
			TenantAlias: tenantAlias,
			TimeoutSec:  timeoutSec,
			Constants:   constants,
			ScheduledAt: scheduledAt,
		})
	}

	return events, rows.Err()
}

// Claim attempts to atomically claim an event for this instance.
// Uses SELECT FOR UPDATE SKIP LOCKED to ensure only one instance claims the event.
func (s *PostgresStore) Claim(ctx context.Context, event *ScheduledEvent, instanceID string) (ClaimResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ClaimError, err
	}
	defer tx.Rollback(ctx)

	// Try to acquire exclusive lock without blocking.
	// If another instance has locked the row, we lose the claim.
	var claimedName string
	err = tx.QueryRow(ctx, `
		SELECT name FROM rah_system.sch_schedules
		WHERE name = $1
		FOR UPDATE SKIP LOCKED
	`, event.Name).Scan(&claimedName)

	if err != nil {
		// Row is locked by another instance or doesn't exist.
		return ClaimLost, nil
	}

	// We have the lock; this instance wins the claim.
	if err := tx.Commit(ctx); err != nil {
		return ClaimError, err
	}

	return ClaimWon, nil
}

// RecordExecution writes an execution record and advances NextRunAt.
func (s *PostgresStore) RecordExecution(ctx context.Context, rec ExecutionRecord, nextRun time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Insert execution history record.
	_, err = tx.Exec(ctx, `
		INSERT INTO rah_system.sch_execution_history (schedule_name, started_at, finished_at, status, error_msg)
		VALUES ($1, $2, $3, $4, $5)
	`, rec.Name, rec.StartedAt, rec.FinishedAt, rec.Status, rec.Error)
	if err != nil {
		return fmt.Errorf("insert execution history: %w", err)
	}

	// Update schedule with new last run and next run times.
	_, err = tx.Exec(ctx, `
		UPDATE rah_system.sch_schedules
		SET last_run_at = $1, last_status = $2, next_run_at = $3, updated_at = NOW()
		WHERE name = $4
	`, rec.FinishedAt, rec.Status, nextRun, rec.Name)
	if err != nil {
		return fmt.Errorf("update schedule: %w", err)
	}

	return tx.Commit(ctx)
}

// ListAll returns all schedules (for management API).
func (s *PostgresStore) ListAll(ctx context.Context) ([]*Schedule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name, cron, flow_name, tenant_alias, constants, enabled, on_failure,
		       max_concurrent, next_run_at, last_run_at, last_status
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

		if err := rows.Scan(
			&sc.Name, &sc.Cron, &sc.FlowName, &sc.TenantAlias, &constantsJSON, &sc.Enabled,
			&onFailureJSON, &sc.MaxConcurrent, &sc.NextRunAt, &sc.LastRunAt, &sc.LastStatus,
		); err != nil {
			continue
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
