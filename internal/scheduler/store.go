package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// Store persists schedules and coordinates cross-instance claiming.
type Store interface {
	// Upsert inserts or updates a schedule. Computes NextRunAt from Cron.
	Upsert(ctx context.Context, s *Schedule) error
	// Delete removes a schedule by name.
	Delete(ctx context.Context, name string) error
	// ListDueWithin returns schedules whose NextRunAt is within the next windowSec seconds.
	ListDueWithin(ctx context.Context, windowSec int) ([]*ScheduledEvent, error)
	// Claim attempts to atomically claim an event for this instance.
	// Returns ClaimWon if this instance should execute it.
	Claim(ctx context.Context, event *ScheduledEvent, instanceID string) (ClaimResult, error)
	// RecordExecution writes an execution record and advances NextRunAt.
	RecordExecution(ctx context.Context, rec ExecutionRecord, nextRun time.Time) error
	// ListAll returns all schedules (for management API).
	ListAll(ctx context.Context) ([]*Schedule, error)
	// ListHistory returns recent execution records for a schedule.
	ListHistory(ctx context.Context, name string, limit int) ([]ExecutionRecord, error)
}

// MemoryStore is an in-process implementation of Store.
type MemoryStore struct {
	mu       sync.RWMutex
	schedules map[string]*Schedule
	history  map[string][]ExecutionRecord
	cronParser cron.Parser
}

// NewMemoryStore creates a new in-process store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		schedules: make(map[string]*Schedule),
		history:  make(map[string][]ExecutionRecord),
		cronParser: cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

// Upsert inserts or updates a schedule and computes NextRunAt.
func (ms *MemoryStore) Upsert(ctx context.Context, s *Schedule) error {
	if s.Cron == "" {
		return fmt.Errorf("cron expression required")
	}

	schedule, err := ms.cronParser.Parse(s.Cron)
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}

	s.NextRunAt = schedule.Next(time.Now())

	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.schedules[s.Name] = s
	if _, ok := ms.history[s.Name]; !ok {
		ms.history[s.Name] = []ExecutionRecord{}
	}

	return nil
}

// Delete removes a schedule by name.
func (ms *MemoryStore) Delete(ctx context.Context, name string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	delete(ms.schedules, name)
	delete(ms.history, name)
	return nil
}

// ListDueWithin returns schedules due within the lookahead window.
func (ms *MemoryStore) ListDueWithin(ctx context.Context, windowSec int) ([]*ScheduledEvent, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var events []*ScheduledEvent
	now := time.Now()
	cutoff := now.Add(time.Duration(windowSec) * time.Second)

	for _, s := range ms.schedules {
		if !s.Enabled {
			continue
		}
		if s.NextRunAt.After(now) && s.NextRunAt.Before(cutoff) {
			events = append(events, &ScheduledEvent{
				Name:        s.Name,
				FlowName:    s.FlowName,
				TenantAlias: s.TenantAlias,
				TimeoutSec:  s.TimeoutSec,
				Constants:   s.Constants,
				ScheduledAt: s.NextRunAt,
			})
		}
	}

	return events, nil
}

// Claim always returns ClaimWon for single-instance mode.
func (ms *MemoryStore) Claim(ctx context.Context, event *ScheduledEvent, instanceID string) (ClaimResult, error) {
	return ClaimWon, nil
}

// RecordExecution writes an execution record and advances NextRunAt.
func (ms *MemoryStore) RecordExecution(ctx context.Context, rec ExecutionRecord, nextRun time.Time) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	// Append to history (keep last 100)
	history, ok := ms.history[rec.Name]
	if !ok {
		history = []ExecutionRecord{}
	}
	history = append(history, rec)
	if len(history) > 100 {
		history = history[1:]
	}
	ms.history[rec.Name] = history

	// Update schedule
	s, ok := ms.schedules[rec.Name]
	if !ok {
		return fmt.Errorf("schedule not found: %s", rec.Name)
	}

	s.LastRunAt = rec.FinishedAt
	s.LastStatus = rec.Status
	s.NextRunAt = nextRun

	return nil
}

// ListAll returns all schedules.
func (ms *MemoryStore) ListAll(ctx context.Context) ([]*Schedule, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var result []*Schedule
	for _, s := range ms.schedules {
		result = append(result, s)
	}
	return result, nil
}

// ListHistory returns execution history for a schedule.
func (ms *MemoryStore) ListHistory(ctx context.Context, name string, limit int) ([]ExecutionRecord, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	history, ok := ms.history[name]
	if !ok {
		return []ExecutionRecord{}, nil
	}

	if limit <= 0 || limit > len(history) {
		limit = len(history)
	}

	result := make([]ExecutionRecord, limit)
	copy(result, history[len(history)-limit:])
	return result, nil
}

// RedisStore is a stub implementation for Redis backend.
type RedisStore struct {
	addr     string
	password string
	db       int
}

// NewRedisStore creates a Redis store (not yet implemented).
func NewRedisStore(addr, password string, db int) *RedisStore {
	return &RedisStore{
		addr:     addr,
		password: password,
		db:       db,
	}
}

// Upsert is not yet implemented.
func (rs *RedisStore) Upsert(ctx context.Context, s *Schedule) error {
	return fmt.Errorf("redis store: not yet implemented")
}

// Delete is not yet implemented.
func (rs *RedisStore) Delete(ctx context.Context, name string) error {
	return fmt.Errorf("redis store: not yet implemented")
}

// ListDueWithin is not yet implemented.
func (rs *RedisStore) ListDueWithin(ctx context.Context, windowSec int) ([]*ScheduledEvent, error) {
	return nil, fmt.Errorf("redis store: not yet implemented")
}

// Claim is not yet implemented.
func (rs *RedisStore) Claim(ctx context.Context, event *ScheduledEvent, instanceID string) (ClaimResult, error) {
	return ClaimError, fmt.Errorf("redis store: not yet implemented")
}

// RecordExecution is not yet implemented.
func (rs *RedisStore) RecordExecution(ctx context.Context, rec ExecutionRecord, nextRun time.Time) error {
	return fmt.Errorf("redis store: not yet implemented")
}

// ListAll is not yet implemented.
func (rs *RedisStore) ListAll(ctx context.Context) ([]*Schedule, error) {
	return nil, fmt.Errorf("redis store: not yet implemented")
}

// ListHistory is not yet implemented.
func (rs *RedisStore) ListHistory(ctx context.Context, name string, limit int) ([]ExecutionRecord, error) {
	return nil, fmt.Errorf("redis store: not yet implemented")
}

// TODO: implement using ZADD/ZRANGEBYSCORE/ZREM for atomic claiming

// NewStore factory creates the appropriate store implementation from config.
func NewStore(cfg SchedulerConfig) Store {
	switch cfg.Backend {
	case "redis":
		// For now, return a stub; in production, parse connection details from cfg
		return NewRedisStore("localhost:6379", "", 0)
	case "postgres":
		// PostgreSQL store must be wired separately via NewPostgresStore(ctx, pool)
		// This factory returns a stub that will fail at runtime.
		// See scheduler initialization in cmd/rah-gateway/main.go for proper wiring.
		return &memoryStoreStub{err: fmt.Errorf("postgres store not initialized: must wire via NewPostgresStore(ctx, pool)")}
	default:
		return NewMemoryStore()
	}
}

// memoryStoreStub is a placeholder that fails at runtime if used.
type memoryStoreStub struct {
	err error
}

func (s *memoryStoreStub) Upsert(ctx context.Context, sc *Schedule) error                                  { return s.err }
func (s *memoryStoreStub) Delete(ctx context.Context, name string) error                                    { return s.err }
func (s *memoryStoreStub) ListDueWithin(ctx context.Context, windowSec int) ([]*ScheduledEvent, error)      { return nil, s.err }
func (s *memoryStoreStub) Claim(ctx context.Context, event *ScheduledEvent, instanceID string) (ClaimResult, error) { return ClaimError, s.err }
func (s *memoryStoreStub) RecordExecution(ctx context.Context, rec ExecutionRecord, nextRun time.Time) error { return s.err }
func (s *memoryStoreStub) ListAll(ctx context.Context) ([]*Schedule, error)                                  { return nil, s.err }
func (s *memoryStoreStub) ListHistory(ctx context.Context, name string, limit int) ([]ExecutionRecord, error) { return nil, s.err }

// SchedulerConfig is imported from internal/config
type SchedulerConfig struct {
	Enabled        bool   `json:"enabled,omitempty"         yaml:"enabled,omitempty"`
	Backend        string `json:"backend,omitempty"         yaml:"backend,omitempty"`
	LookaheadSec   int    `json:"lookahead_sec,omitempty"   yaml:"lookahead_sec,omitempty"`
	MaxConcurrent  int    `json:"max_concurrent,omitempty"  yaml:"max_concurrent,omitempty"`
	LeaderElection bool   `json:"leader_election,omitempty" yaml:"leader_election,omitempty"`
	LeaderTTLSec   int    `json:"leader_ttl_sec,omitempty"   yaml:"leader_ttl_sec,omitempty"`
}
