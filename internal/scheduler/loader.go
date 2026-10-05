package scheduler

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// Loader pre-loads upcoming scheduled events from the store into the wheel.
// Called at startup and periodically to advance the lookahead window.
type Loader struct {
	store        Store
	wheel        *SchedulerWheel
	lookaheadSec int
	armed        sync.Map // key: "name:unix_timestamp", value: struct{}{}
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

	now := time.Now()
	for _, ev := range events {
		key := ev.Name + ":" + strconv.FormatInt(ev.ScheduledAt.Unix(), 10)
		if _, loaded := l.armed.LoadOrStore(key, struct{}{}); loaded {
			continue // already armed this fire-time
		}

		pooled := globalEventPool.Get()
		pooled.Name = ev.Name
		pooled.FlowName = ev.FlowName
		pooled.TenantAlias = ev.TenantAlias
		pooled.TimeoutSec = ev.TimeoutSec
		pooled.Cron = ev.Cron
		pooled.RetryCount = ev.RetryCount
		pooled.RetryIntervalSec = ev.RetryIntervalSec
		pooled.DeadLetterFlow = ev.DeadLetterFlow
		pooled.ScheduledAt = ev.ScheduledAt
		pooled.Epoch = uint32(ev.ScheduledAt.Unix() / 3600)
		if ev.Constants != nil {
			if pooled.Constants == nil {
				pooled.Constants = make(map[string]string, len(ev.Constants))
			} else {
				for k := range pooled.Constants {
					delete(pooled.Constants, k)
				}
			}
			for k, v := range ev.Constants {
				pooled.Constants[k] = v
			}
		} else {
			pooled.Constants = nil
		}

		if !l.wheel.Schedule(pooled) {
			globalEventPool.Put(pooled)
			l.armed.Delete(key) // slot full: undo so we retry next cycle
		}
	}

	// Prune keys whose fire-time has passed.
	nowUnix := now.Unix()
	l.armed.Range(func(k, _ any) bool {
		key := k.(string)
		if idx := strings.LastIndex(key, ":"); idx >= 0 {
			if ts, err := strconv.ParseInt(key[idx+1:], 10, 64); err == nil && ts < nowUnix {
				l.armed.Delete(key)
			}
		}
		return true
	})

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
