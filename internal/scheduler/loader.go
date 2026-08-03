package scheduler

import (
	"context"
	"time"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// Loader pre-loads upcoming scheduled events from the store into the wheel.
// Called at startup and periodically to advance the lookahead window.
type Loader struct {
	store         Store
	wheel         *SchedulerWheel
	lookaheadSec  int
}

// NewLoader creates a new loader.
func NewLoader(store Store, wheel *SchedulerWheel, lookaheadSec int) *Loader {
	return &Loader{
		store:        store,
		wheel:        wheel,
		lookaheadSec: lookaheadSec,
	}
}

// LoadUpcoming reads events due within lookaheadSec from now and arms them in the wheel.
func (l *Loader) LoadUpcoming(ctx context.Context) error {
	events, err := l.store.ListDueWithin(ctx, l.lookaheadSec)
	if err != nil {
		return err
	}

	for _, event := range events {
		l.wheel.Schedule(event)
	}

	if len(events) > 0 {
		gatewaylog.Default.Debug("[Scheduler] loaded upcoming events")
	}

	return nil
}

// Start runs LoadUpcoming every (lookaheadSec/2) seconds in a background goroutine.
func (l *Loader) Start(ctx context.Context) {
	go l.runLoader(ctx)
}

// runLoader periodically loads upcoming events.
func (l *Loader) runLoader(ctx context.Context) {
	// Reload every half the lookahead window to ensure coverage.
	interval := time.Duration(l.lookaheadSec/2) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Load immediately on start.
	if err := l.LoadUpcoming(ctx); err != nil {
		gatewaylog.Default.Error("[Scheduler] initial load failed",
			gatewaylog.F("error", err.Error()),
		)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.LoadUpcoming(ctx); err != nil {
				gatewaylog.Default.Error("[Scheduler] load failed",
					gatewaylog.F("error", err.Error()),
				)
			}
		}
	}
}
