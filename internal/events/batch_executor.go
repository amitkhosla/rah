package events

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/connectors/messaging"
	"github.com/amitkhosla/rah/internal/engine"
)

// BatchingEventExecutor accumulates ConsumedMessages and flushes them as a
// JSON array into a single named slot when either batchSize is reached or
// batchWindowMs elapses since the first message of the current window.
type BatchingEventExecutor struct {
	fm          *engine.FlowManager
	flowName    string
	payloadSlot int // slot index for the JSON array; -1 if not configured
	batchSize   int
	window      time.Duration

	mu    sync.Mutex
	batch [][]byte // accumulated payloads
	timer *time.Timer
}

func newBatchingEventExecutor(ctx context.Context, fm *engine.FlowManager, flowName string, payloadSlot, batchSize int, windowMs int) *BatchingEventExecutor {
	b := &BatchingEventExecutor{
		fm:          fm,
		flowName:    flowName,
		payloadSlot: payloadSlot,
		batchSize:   batchSize,
		window:      time.Duration(windowMs) * time.Millisecond,
	}
	// Background goroutine that flushes on context cancellation.
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		if len(b.batch) > 0 {
			b.flushLocked(ctx)
		}
		if b.timer != nil {
			b.timer.Stop()
		}
	}()
	return b
}

// Handle accumulates msg.Payload and flushes when the batch is full or the
// window timer fires.
func (b *BatchingEventExecutor) Handle(ctx context.Context, msg messaging.ConsumedMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.batch = append(b.batch, msg.Payload)

	// Start the window timer on the first message.
	if len(b.batch) == 1 && b.window > 0 {
		b.timer = time.AfterFunc(b.window, func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if len(b.batch) > 0 {
				b.flushLocked(ctx)
			}
		})
	}

	if b.batchSize > 0 && len(b.batch) >= b.batchSize {
		if b.timer != nil {
			b.timer.Stop()
			b.timer = nil
		}
		b.flushLocked(ctx)
	}

	return nil
}

// flushLocked serialises the accumulated payloads as a JSON array, injects
// them into the configured slot, and invokes the flow. Must be called with
// b.mu held.
func (b *BatchingEventExecutor) flushLocked(ctx context.Context) {
	if len(b.batch) == 0 {
		return
	}
	payloads := b.batch
	b.batch = nil

	data, err := json.Marshal(payloads)
	if err != nil {
		return
	}

	rctx := b.fm.GetContext()
	defer b.fm.ReturnContext(rctx)

	rctx.Reset(&discardWriter{})
	rctx.Timing.StartNs = time.Now().UnixNano()

	if b.payloadSlot >= 0 && b.payloadSlot < len(rctx.ByteSlots) {
		rctx.ByteSlots[b.payloadSlot] = data
	}

	b.fm.ProcessFlow(rctx, b.flowName)
}
