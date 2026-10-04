package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// Scheduler is the top-level coordinator. Created once at gateway startup.
type Scheduler struct {
	Store        Store
	Wheel        *SchedulerWheel
	Executor     *Executor
	Loader       *Loader
	lookaheadSec int
	cancelled    sync.Map
}

// NewWithStore creates a Scheduler with an explicitly provided store.
// Use this when the backend requires external initialisation (e.g. postgres).
func NewWithStore(store Store, instanceID string, runner FlowRunner, cfg SchedulerConfig) *Scheduler {
	lookahead := cfg.LookaheadSec
	if lookahead <= 0 {
		lookahead = 3600
	}
	dispatch := NewDispatchRing(512)
	sched := &Scheduler{Store: store, lookaheadSec: lookahead}
	sched.Executor = NewExecutor(store, instanceID, runner, dispatch, &sched.cancelled)
	sched.Wheel = NewSchedulerWheel(dispatch)
	sched.Loader = NewLoader(store, sched.Wheel, lookahead)
	return sched
}

// New creates a Scheduler from gateway config.
// runner is injected from main to avoid import cycles.
func New(cfg SchedulerConfig, instanceID string, runner FlowRunner) *Scheduler {
	store := NewStore(cfg)

	lookahead := cfg.LookaheadSec
	if lookahead <= 0 {
		lookahead = 3600
	}

	dispatch := NewDispatchRing(512)
	sched := &Scheduler{Store: store, lookaheadSec: lookahead}
	sched.Executor = NewExecutor(store, instanceID, runner, dispatch, &sched.cancelled)
	sched.Wheel = NewSchedulerWheel(dispatch)
	sched.Loader = NewLoader(store, sched.Wheel, lookahead)
	return sched
}

// Start launches the executor, wheel ticker and loader background goroutine.
func (s *Scheduler) Start(ctx context.Context) {
	s.Executor.Start(ctx)
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
		lookaheadDur := time.Duration(s.lookaheadSec) * time.Second
		if !sc.NextRunAt.Before(now) && sc.NextRunAt.Before(now.Add(lookaheadDur)) {
			pooled := globalEventPool.Get()
			pooled.Name = sc.Name
			pooled.FlowName = sc.FlowName
			pooled.TenantAlias = sc.TenantAlias
			pooled.TimeoutSec = sc.TimeoutSec
			pooled.Cron = sc.Cron
			pooled.RetryCount = sc.OnFailure.RetryCount
			pooled.RetryIntervalSec = sc.OnFailure.RetryIntervalSec
			pooled.DeadLetterFlow = sc.OnFailure.DeadLetterFlow
			pooled.ScheduledAt = sc.NextRunAt
			pooled.Epoch = uint32(sc.NextRunAt.Unix() / 3600)
			if sc.Constants != nil {
				if pooled.Constants == nil {
					pooled.Constants = make(map[string]string, len(sc.Constants))
				} else {
					for k := range pooled.Constants {
						delete(pooled.Constants, k)
					}
				}
				for k, v := range sc.Constants {
					pooled.Constants[k] = v
				}
			} else {
				pooled.Constants = nil
			}
			if !s.Wheel.Schedule(pooled) {
				globalEventPool.Put(pooled) // slot full; loader will retry
			}
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
	s.cancelled.Store(name, time.Now().Add(time.Duration(s.lookaheadSec+60)*time.Second))
	err := s.Store.Delete(ctx, name)
	if err != nil {
		s.cancelled.Delete(name)
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
