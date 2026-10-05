package scheduler

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/gatewaylog"
	"github.com/robfig/cron/v3"
)

// FlowRunner is a function that executes a named flow in a given tenant context.
// Implemented in cmd/rah-gateway/main.go and injected at startup.
type FlowRunner func(flowName string, tenantAlias string, constants map[string]string, timeoutSec int) error

// Executor drains the dispatch ring with a worker pool and executes scheduled flows.
type Executor struct {
	store      Store
	instanceID string
	runner     FlowRunner
	dispatch   *DispatchRing
	cronParser cron.Parser
	cancelled  *sync.Map
	workers    int
}

// NewExecutor creates a new executor.
// cancelled is a pointer to Scheduler.cancelled so DeleteSchedule can cancel in-flight events.
func NewExecutor(store Store, instanceID string, runner FlowRunner, dispatch *DispatchRing, cancelled *sync.Map) *Executor {
	return &Executor{
		store:      store,
		instanceID: instanceID,
		runner:     runner,
		dispatch:   dispatch,
		cronParser: cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
		cancelled:  cancelled,
		workers:    runtime.NumCPU() * 4,
	}
}

// Start spawns the worker goroutines that drain the dispatch ring.
func (e *Executor) Start(ctx context.Context) {
	for i := 0; i < e.workers; i++ {
		go e.workerLoop(ctx)
	}
}

func (e *Executor) workerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		ev := e.dispatch.TryPop()
		if ev == nil {
			select {
			case <-ctx.Done():
				return
			case <-e.dispatch.NotifyCh:
			}
			continue
		}
		e.handle(ev)
	}
}

func (e *Executor) handle(event *ScheduledEvent) {
	// Check if the schedule was deleted after this event was armed.
	if v, ok := e.cancelled.Load(event.Name); ok {
		e.cancelled.Delete(event.Name)
		if exp, ok := v.(time.Time); ok && time.Now().Before(exp) {
			globalEventPool.Put(event)
			return
		}
		// TTL expired — stale entry, process normally
	}

	// Claim the schedule via UPDATE-based atomic claim.
	result, err := e.store.Claim(context.Background(), event.Name, e.instanceID)
	if err != nil {
		gatewaylog.Default.Error("[Scheduler] claim failed",
			gatewaylog.F("event", event.Name),
			gatewaylog.F("error", err.Error()),
		)
		globalEventPool.Put(event)
		return
	}
	if result != ClaimWon {
		globalEventPool.Put(event)
		return
	}

	// Build flow execution context. Default 300s if TimeoutSec is not set.
	timeoutSec := event.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	flowCtx, flowCancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)

	startedAt := time.Now()
	execErr := e.runWithRetry(flowCtx, event)
	flowCancel()
	finishedAt := time.Now()

	// Compute next run from cron expression.
	var nextRun time.Time
	if event.Cron != "" {
		if sched, parseErr := e.cronParser.Parse(event.Cron); parseErr == nil {
			nextRun = sched.Next(finishedAt)
		}
	}
	if nextRun.IsZero() {
		nextRun = finishedAt.Add(time.Minute) // fallback only when cron is blank or unparseable
	}

	status := "ok"
	errMsg := ""
	if execErr != nil {
		status = "failed"
		errMsg = execErr.Error()
		if event.DeadLetterFlow != "" && e.runner != nil {
			if dlErr := e.runner(event.DeadLetterFlow, event.TenantAlias, event.Constants, event.TimeoutSec); dlErr != nil {
				gatewaylog.Default.Error("[Scheduler] dead-letter flow failed",
					gatewaylog.F("event", event.Name),
					gatewaylog.F("flow", event.DeadLetterFlow),
					gatewaylog.F("error", dlErr.Error()),
				)
			}
		}
	}

	rec := ExecutionRecord{
		Name:       event.Name,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Status:     status,
		Error:      errMsg,
	}

	// Use a fresh context for RecordExecution — flowCtx may already be expired.
	recCtx, recCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if recErr := e.store.RecordExecution(recCtx, rec, nextRun); recErr != nil {
		gatewaylog.Default.Error("[Scheduler] record execution failed",
			gatewaylog.F("event", event.Name),
			gatewaylog.F("error", recErr.Error()),
		)
	}
	recCancel()

	gatewaylog.Default.Debug("[Scheduler] execution completed",
		gatewaylog.F("event", event.Name),
		gatewaylog.F("status", status),
	)

	globalEventPool.Put(event)
}

func (e *Executor) runWithRetry(ctx context.Context, event *ScheduledEvent) error {
	if e.runner == nil {
		return fmt.Errorf("no runner configured for scheduler")
	}

	attempts := event.RetryCount + 1
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 && event.RetryIntervalSec > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(event.RetryIntervalSec) * time.Second):
			}
		}
		lastErr = e.runner(event.FlowName, event.TenantAlias, event.Constants, event.TimeoutSec)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}
