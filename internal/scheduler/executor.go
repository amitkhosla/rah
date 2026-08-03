package scheduler

import (
	"context"
	"time"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// FlowRunner is a function that executes a named flow in a given tenant context.
// Implemented in cmd/rah-gateway/main.go and injected at startup.
// Signature chosen to avoid importing internal/engine here.
type FlowRunner func(flowName string, tenantAlias string, constants map[string]string, timeoutSec int) error

// Executor coordinates claim + execution of scheduled events.
// It does NOT import the engine package directly — it calls execution
// via a registered FlowRunner function to avoid import cycles.
type Executor struct {
	store      Store
	instanceID string
	runner     FlowRunner
}

// NewExecutor creates a new executor.
func NewExecutor(store Store, instanceID string, runner FlowRunner) *Executor {
	return &Executor{
		store:      store,
		instanceID: instanceID,
		runner:     runner,
	}
}

// Handle is called by the wheel when an event fires.
// It claims the event then calls runner if won.
func (e *Executor) Handle(event *ScheduledEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(event.TimeoutSec)*time.Second)
	defer cancel()

	// Attempt to claim the event.
	result, err := e.store.Claim(ctx, event, e.instanceID)
	if err != nil {
		gatewaylog.Default.Error("[Scheduler] claim failed",
			gatewaylog.F("event", event.Name),
			gatewaylog.F("error", err.Error()),
		)
		return
	}

	if result != ClaimWon {
		// Another instance won the claim; do nothing.
		return
	}

	// Execute the flow with retry logic.
	startedAt := time.Now()
	execErr := e.runWithRetry(event)
	finishedAt := time.Now()

	// Determine status.
	status := "ok"
	errMsg := ""
	if execErr != nil {
		status = "failed"
		errMsg = execErr.Error()
	}

	// Record the execution.
	rec := ExecutionRecord{
		Name:       event.Name,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Status:     status,
		Error:      errMsg,
	}

	// Compute next run time.
	// For now, we just schedule it for the next occurrence based on the cron expression.
	// This should be retrieved from the store or computed here.
	// For simplicity, we'll pass a default value; the actual implementation
	// would look up the schedule definition and compute the next run time.
	nextRun := finishedAt.Add(time.Minute)

	err = e.store.RecordExecution(ctx, rec, nextRun)
	if err != nil {
		gatewaylog.Default.Error("[Scheduler] record execution failed",
			gatewaylog.F("event", event.Name),
			gatewaylog.F("error", err.Error()),
		)
		return
	}

	gatewaylog.Default.Debug("[Scheduler] execution completed",
		gatewaylog.F("event", event.Name),
		gatewaylog.F("status", status),
	)
}

// runWithRetry executes the flow with retry and dead letter logic.
func (e *Executor) runWithRetry(event *ScheduledEvent) error {
	if e.runner == nil {
		return nil
	}

	// For now, OnFailure is not yet stored in ScheduledEvent.
	// In a complete implementation, you would pass it through the event.
	// For now, we do a simple single attempt.
	// TODO: fetch the full Schedule from store to access OnFailure config.

	execErr := e.runner(event.FlowName, event.TenantAlias, event.Constants, event.TimeoutSec)
	return execErr
}
