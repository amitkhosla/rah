package redis

import (
	"context"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"rah/internal/rctx"
)

const (
	DefaultWorkerCount      = 8
	DefaultWorkerQueue      = 64
	DefaultMaxCmdsPerPipeline  = 256
	DefaultMaxBytesPerPipeline = 1 << 20 // 1MB
)

// PipelineConfig controls batching behaviour for RedisExecutor.
type PipelineConfig struct {
	// Workers is the number of concurrent pipeline goroutines (= connections
	// used simultaneously). 0 = derive from PoolSize; falls back to
	// DefaultWorkerCount if PoolSize is also 0.
	Workers int

	// PoolSize is the Redis connection pool size. Used only to derive Workers
	// when Workers == 0. Must match goredis.Options.PoolSize.
	PoolSize int

	// QueueDepth is the channel buffer size per worker. 0 = DefaultWorkerQueue.
	QueueDepth int

	// MaxCmdsPerPipeline caps the number of Redis commands in one pipeline exec.
	// 0 = use DefaultMaxCmdsPerPipeline.
	MaxCmdsPerPipeline int

	// MaxBytesPerPipeline caps the estimated payload in one pipeline exec.
	// PUT ops: key+value bytes. GET ops: key bytes only (value size unknown).
	// 0 = use DefaultMaxBytesPerPipeline.
	MaxBytesPerPipeline int64
}

func (c *PipelineConfig) workers() int {
	if c.Workers > 0 {
		return c.Workers
	}
	if c.PoolSize > 0 {
		return c.PoolSize
	}
	return DefaultWorkerCount
}

func (c *PipelineConfig) queueDepth() int {
	if c.QueueDepth > 0 {
		return c.QueueDepth
	}
	return DefaultWorkerQueue
}

func (c *PipelineConfig) maxCmds() int {
	if c.MaxCmdsPerPipeline > 0 {
		return c.MaxCmdsPerPipeline
	}
	return DefaultMaxCmdsPerPipeline
}

func (c *PipelineConfig) maxBytes() int64 {
	if c.MaxBytesPerPipeline > 0 {
		return c.MaxBytesPerPipeline
	}
	return DefaultMaxBytesPerPipeline
}

type worker struct {
	ch chan rctx.Batch
}

// RedisExecutor fans submitted rctx.Batches across multiple worker goroutines.
// Each worker opportunistically merges queued rctx.Batches into a single pipeline
// exec — no timers, no artificial delays.
type RedisExecutor struct {
	workers []worker
	client  goredis.UniversalClient
	cfg     PipelineConfig
	next    atomic.Uint32
}

// NewRedisExecutor creates a RedisExecutor and starts its worker goroutines.
// ctx controls goroutine lifetime; cancel it to shut the executor down.
func NewRedisExecutor(ctx context.Context, client goredis.UniversalClient, cfg PipelineConfig) *RedisExecutor {
	n := cfg.workers()
	q := cfg.queueDepth()
	e := &RedisExecutor{
		workers: make([]worker, n),
		client:  client,
		cfg:     cfg,
	}
	for i := range e.workers {
		e.workers[i].ch = make(chan rctx.Batch, q)
		go e.runWorker(ctx, &e.workers[i])
	}
	return e
}

// Submit dispatches a rctx.Batch using round-robin with non-blocking fallback.
// Tries the preferred worker first, then remaining workers non-blocking,
// then falls back to a blocking send on the original worker.
func (e *RedisExecutor) Submit(batch rctx.Batch) {
	n := len(e.workers)
	idx := int(e.next.Add(1) % uint32(n))

	// Fast path: preferred worker has space.
	select {
	case e.workers[idx].ch <- batch:
		return
	default:
	}

	// Try remaining workers non-blocking.
	for i := 1; i < n; i++ {
		j := (idx + i) % n
		select {
		case e.workers[j].ch <- batch:
			return
		default:
		}
	}

	// All workers busy — block on the original worker.
	// This is the natural back-pressure point; no sleep needed.
	e.workers[idx].ch <- batch
}

// runWorker is the per-worker dispatch loop. Blocks on first rctx.Batch, then
// drains its channel non-blocking, merging into a single pipeline until
// limits are hit or the channel is empty.
func (e *RedisExecutor) runWorker(ctx context.Context, w *worker) {
	for {
		var first rctx.Batch
		select {
		case first = <-w.ch:
		case <-ctx.Done():
			return
		}

		acc := newAccum(first)

	drain:
		for {
			select {
			case incoming := <-w.ch:
				if acc.fits(incoming, &e.cfg) {
					acc.add(incoming)
				} else {
					// Pipeline limits reached — exec current, restart with incoming.
					e.execBatch(ctx, acc)
					acc = newAccum(incoming)
					// Do not break — keep draining for the new pipeline.
				}
			default:
				// Channel empty — nothing waiting right now.
				break drain
			}
		}

		e.execBatch(ctx, acc)
	}
}

// pipelineAccum accumulates ops and tracks estimated payload size.
type pipelineAccum struct {
	ops      []rctx.StorageOp
	doneChs  []chan struct{}
	byteSize int64
}

func newAccum(b rctx.Batch) pipelineAccum {
	a := pipelineAccum{
		ops:     b.Ops,
		byteSize: batchBytes(b),
	}
	if b.Done != nil {
		a.doneChs = []chan struct{}{b.Done}
	}
	return a
}

func (a *pipelineAccum) fits(b rctx.Batch, cfg *PipelineConfig) bool {
	if len(a.ops)+len(b.Ops) > cfg.maxCmds() {
		return false
	}
	if a.byteSize+batchBytes(b) > cfg.maxBytes() {
		return false
	}
	return true
}

func (a *pipelineAccum) add(b rctx.Batch) {
	a.ops = append(a.ops, b.Ops...)
	if b.Done != nil {
		a.doneChs = append(a.doneChs, b.Done)
	}
	a.byteSize += batchBytes(b)
}

// batchBytes estimates the outbound payload size of a batch.
// PUT ops: key+value bytes (both known). GET ops: key bytes only (value unknown).
func batchBytes(b rctx.Batch) int64 {
	var n int64
	for _, op := range b.Ops {
		n += int64(len(op.Key))
		if op.Type == rctx.OpPut {
			n += int64(len(op.Value))
		}
	}
	return n
}

// getCmdEntry maps a pipeline GET command back to its position in ops.
type getCmdEntry struct {
	opIdx int
	cmd   *goredis.StringCmd
}

// execBatch builds and executes a single Redis pipeline for the accumulator,
// writes GET results back into ops, then closes all Done channels.
// Result writes happen before Done channels are closed (ordering guarantee).
func (e *RedisExecutor) execBatch(ctx context.Context, acc pipelineAccum) {
	pipe := e.client.Pipeline()

	var getCmds []getCmdEntry
	for i := range acc.ops {
		switch acc.ops[i].Type {
		case rctx.OpPut:
			ttl := time.Duration(acc.ops[i].TTL) * time.Second
			pipe.Set(ctx, string(acc.ops[i].Key), acc.ops[i].Value, ttl)
		case rctx.OpGet:
			cmd := pipe.Get(ctx, string(acc.ops[i].Key))
			getCmds = append(getCmds, getCmdEntry{opIdx: i, cmd: cmd})
		}
	}

	_, _ = pipe.Exec(ctx)

	// Write GET results back before signalling callers.
	for _, entry := range getCmds {
		val, err := entry.cmd.Bytes()
		if err == nil {
			acc.ops[entry.opIdx].Result = val
		}
		// On miss or error: Result stays nil — caller treats nil as cache miss.
	}

	// Signal waiting callers only after all Results are populated.
	// Done is nil for fire-and-forget (async PUT) batches — skip those.
	for _, ch := range acc.doneChs {
		close(ch)
	}
}
