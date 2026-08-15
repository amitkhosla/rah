package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// Scheduler is the top-level coordinator. Created once at gateway startup.
type Scheduler struct {
	Store    Store
	Wheel    *SchedulerWheel
	Executor *Executor
	Loader   *Loader
}

// NewWithStore creates a Scheduler with an explicitly provided store.
// Use this when the backend requires external initialisation (e.g. postgres).
func NewWithStore(store Store, instanceID string, runner FlowRunner, cfg SchedulerConfig) *Scheduler {
	executor := NewExecutor(store, instanceID, runner)
	wheel := NewSchedulerWheel(func(event *ScheduledEvent) { executor.Handle(event) })
	lookahead := cfg.LookaheadSec
	if lookahead <= 0 {
		lookahead = 3600
	}
	loader := NewLoader(store, wheel, lookahead)
	return &Scheduler{Store: store, Wheel: wheel, Executor: executor, Loader: loader}
}

// New creates a Scheduler from gateway config.
// runner is injected from main to avoid import cycles.
func New(cfg SchedulerConfig, instanceID string, runner FlowRunner) *Scheduler {
	store := NewStore(cfg)

	executor := NewExecutor(store, instanceID, runner)

	wheel := NewSchedulerWheel(func(event *ScheduledEvent) {
		executor.Handle(event)
	})

	// Default lookahead window: 1 hour
	lookahead := cfg.LookaheadSec
	if lookahead <= 0 {
		lookahead = 3600
	}

	loader := NewLoader(store, wheel, lookahead)

	return &Scheduler{
		Store:    store,
		Wheel:    wheel,
		Executor: executor,
		Loader:   loader,
	}
}

// Start launches the wheel ticker and loader background goroutine.
func (s *Scheduler) Start(ctx context.Context) {
	s.Wheel.Start(ctx)
	s.Loader.Start(ctx)
	gatewaylog.Default.Info("[Scheduler] started")
}

// UpsertSchedule adds or updates a schedule and arms it in the wheel if due within lookahead.
// The caller (main.go or management_server.go) is responsible for mapping from
// control.ScheduleConfig to *Schedule to avoid an import cycle.
func (s *Scheduler) UpsertSchedule(ctx context.Context, sc *Schedule) error {
	if sc.Name == "" {
		return fmt.Errorf("schedule name required")
	}
	if sc.Cron == "" {
		return fmt.Errorf("cron expression required")
	}
	if sc.FlowName == "" {
		return fmt.Errorf("flow name required")
	}

	// Upsert in store (which computes NextRunAt).
	if err := s.Store.Upsert(ctx, sc); err != nil {
		return err
	}

	// If due within lookahead, arm in wheel.
	if sc.Enabled {
		now := time.Now()
		if sc.NextRunAt.After(now) && sc.NextRunAt.Before(now.Add(time.Hour)) {
			event := &ScheduledEvent{
				Name:        sc.Name,
				FlowName:    sc.FlowName,
				TenantAlias: sc.TenantAlias,
				TimeoutSec:  sc.TimeoutSec,
				Constants:   sc.Constants,
				ScheduledAt: sc.NextRunAt,
			}
			s.Wheel.Schedule(event)
		}
	}

	gatewaylog.Default.Debug("[Scheduler] upserted schedule",
		gatewaylog.F("name", sc.Name),
		gatewaylog.F("flow", sc.FlowName),
	)

	return nil
}

// DeleteSchedule removes a schedule by name.
func (s *Scheduler) DeleteSchedule(ctx context.Context, name string) error {
	err := s.Store.Delete(ctx, name)
	if err != nil {
		return err
	}

	gatewaylog.Default.Debug("[Scheduler] deleted schedule",
		gatewaylog.F("name", name),
	)

	return nil
}

// ListSchedules returns all schedules.
func (s *Scheduler) ListSchedules(ctx context.Context) ([]*Schedule, error) {
	return s.Store.ListAll(ctx)
}
