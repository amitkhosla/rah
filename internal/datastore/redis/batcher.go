package redis

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	// defaultBatchWindow is 0: drain whatever goroutines are already waiting in
	// the inbox and fire immediately. No artificial latency is added.
	// Set a non-zero value only when you deliberately want to trade a small
	// latency increase for larger batches (e.g. 10–50µs in write-heavy workloads).
	defaultBatchWindow = 0
	defaultMaxBatch    = 256 // max keys per MGET
)

// getReq is a single Get request submitted to the batcher by a goroutine.
type getReq struct {
	key    string       // scoped Redis key (already hash-tagged)
	orig   string       // original un-scoped key (returned in result map)
	result chan<- getResp
}

// getResp is the result routed back to the requesting goroutine.
type getResp struct {
	val   []byte
	found bool
	err   error
}

// Batcher collects concurrent Get requests within a short time window and
// dispatches them as a single MGET pipeline, then routes each result back
// to the requesting goroutine via its dedicated channel.
//
// Usage: one Batcher is created per Store. Call BatchGet instead of Get when
// high read concurrency is expected (many goroutines reading different keys).
type Batcher struct {
	inbox  chan getReq
	store  *Store
	window time.Duration
	max    int
}

// NewBatcher creates a Batcher backed by store and starts its dispatch loop.
// Cancel ctx to shut the batcher down cleanly.
func NewBatcher(ctx context.Context, store *Store, window time.Duration, maxBatch int) *Batcher {
	if window <= 0 {
		window = defaultBatchWindow
	}
	if maxBatch <= 0 {
		maxBatch = defaultMaxBatch
	}
	b := &Batcher{
		inbox:  make(chan getReq, maxBatch*4),
		store:  store,
		window: window,
		max:    maxBatch,
	}
	go b.run(ctx)
	return b
}

// BatchGet submits a Get request to the batcher and blocks until the result
// arrives. Requests from concurrent goroutines are coalesced into one MGET.
func (b *Batcher) BatchGet(ctx context.Context, tenant, key string) ([]byte, bool, error) {
	k, err := scopedKey(tenant, b.store.domain, key)
	if err != nil {
		return nil, false, err
	}

	ch := make(chan getResp, 1)
	select {
	case b.inbox <- getReq{key: k, orig: key, result: ch}:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}

	select {
	case r := <-ch:
		return r.val, r.found, r.err
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
}

// run is the single dispatch goroutine. It:
//  1. Blocks on the first request (no idle spinning).
//  2. Opens a collection window to gather more requests.
//  3. Fires one MGET for the whole batch.
//  4. Routes each response back to the correct goroutine by index position.
func (b *Batcher) run(ctx context.Context) {
	for {
		// 1. Wait for the first request — no busy loop when idle.
		var first getReq
		select {
		case first = <-b.inbox:
		case <-ctx.Done():
			return
		}

		batch := make([]getReq, 1, b.max)
		batch[0] = first

		// 2. Collect more requests.
		//
		// window=0 (default): non-blocking drain — pick up every goroutine that
		// is already waiting in the inbox right now, then fire immediately.
		// No latency is added; natural concurrency determines batch size.
		//
		// window>0: open a timer window to allow more goroutines to arrive.
		// Use only when you want to trade a small latency increase for larger
		// batches (e.g. 10–50µs). Never use 200µs+ in a latency-sensitive gateway.
		if b.window == 0 {
		drain:
			for len(batch) < b.max {
				select {
				case req := <-b.inbox:
					batch = append(batch, req)
				default:
					break drain
				}
			}
		} else {
			deadline := time.NewTimer(b.window)
		collect:
			for len(batch) < b.max {
				select {
				case req := <-b.inbox:
					batch = append(batch, req)
				case <-deadline.C:
					break collect
				case <-ctx.Done():
					deadline.Stop()
					b.failBatch(batch, ctx.Err())
					return
				}
			}
			deadline.Stop()
		}

		// 3. Execute the batch.
		//
		// Cluster mode: MGET requires all keys on the same slot. Since the batcher
		// collects requests from any tenant/domain, keys may span multiple slots
		// (different nodes). We use a pipeline of individual GETs instead:
		// ClusterClient.Pipeline() groups commands by slot internally and fans
		// them to the correct nodes concurrently, then merges results in order.
		//
		// Single/sentinel mode: pipeline with individual GETs is equivalent to
		// MGET — same single round-trip, same ordered results.
		//
		// Either way, results are returned in the same order as commands were
		// queued, so index i in cmds maps exactly to batch[i].
		pipe := b.store.client.Pipeline()
		cmds := make([]*goredis.StringCmd, len(batch))
		for i, req := range batch {
			cmds[i] = pipe.Get(ctx, req.key)
		}
		pipe.Exec(ctx) //nolint:errcheck — individual cmd errors checked below

		// 4. Route each result back to its goroutine by index.
		for i, req := range batch {
			val, err := cmds[i].Bytes()
			if err != nil {
				if errors.Is(err, goredis.Nil) {
					req.result <- getResp{found: false}
				} else {
					req.result <- getResp{err: err}
				}
				continue
			}
			req.result <- getResp{val: val, found: true}
		}
	}
}

// failBatch sends an error to every pending request (used on shutdown).
func (b *Batcher) failBatch(batch []getReq, err error) {
	for _, req := range batch {
		req.result <- getResp{err: err}
	}
}
