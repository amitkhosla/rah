package engine

import (
	"context"
	"log"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// luaRateLimitScript atomically increments the key and sets expiry on first
// increment. Returns the new counter value as an integer.
// We intentionally avoid MULTI/EXEC so each pipeline entry is independent —
// callers get individual counts, not all-or-none.
const luaRateLimitScript = `
local v = redis.call('INCR', KEYS[1])
if v == 1 then redis.call('EXPIRE', KEYS[1], tonumber(ARGV[1])) end
return v
`

const (
	rlChannelBuf   = 4096
	rlBatchMax     = 256
	rlBatchDeadline = time.Millisecond
	rlCheckTimeout = 5 * time.Millisecond
)

// rlPendingEntry is one in-flight rate limit request waiting for a Redis result.
type rlPendingEntry struct {
	key        string
	limit      uint32
	windowSecs int
	resultCh   chan rlResult
}

// rlResult carries the outcome of a single Redis counter increment.
type rlResult struct {
	allowed   bool
	remaining uint32
}

// RedisRateLimitProvider implements ExternalRateLimitProvider using a Redis
// pipeline batcher. Multiple concurrent Check calls for any keys are batched
// into a single Redis pipeline but each gets its own count back independently —
// no all-or-none semantics.
//
// On Redis failure the provider fails-open (allows traffic) to avoid taking
// down the gateway when Redis is temporarily unavailable.
type RedisRateLimitProvider struct {
	client    goredis.UniversalClient
	scriptSha string
	requestCh chan rlPendingEntry
	stopCh    chan struct{}
}

// NewRedisRateLimitProvider creates and starts a RedisRateLimitProvider.
// It loads the Lua script into Redis via SCRIPT LOAD and starts the background
// pipeline flusher goroutine. The provided context is used only for the initial
// SCRIPT LOAD call.
func NewRedisRateLimitProvider(ctx context.Context, client goredis.UniversalClient) (*RedisRateLimitProvider, error) {
	sha, err := client.ScriptLoad(ctx, luaRateLimitScript).Result()
	if err != nil {
		return nil, err
	}

	p := &RedisRateLimitProvider{
		client:    client,
		scriptSha: sha,
		requestCh: make(chan rlPendingEntry, rlChannelBuf),
		stopCh:    make(chan struct{}),
	}
	go p.flusher()
	return p, nil
}

// Check atomically increments the distributed counter for key and returns
// (allowed, remaining). windowSecs is the Redis TTL set when the key is new.
//
// Fail-open behaviour:
//   - If the request channel is full, returns (true, limit) immediately.
//   - If Redis does not respond within 5 ms, returns (true, limit).
//   - If Redis returns an error for this entry, returns (true, limit).
func (p *RedisRateLimitProvider) Check(key string, limit uint32, windowSecs int) (bool, uint32) {
	resultCh := make(chan rlResult, 1)
	entry := rlPendingEntry{
		key:        key,
		limit:      limit,
		windowSecs: windowSecs,
		resultCh:   resultCh,
	}

	// Non-blocking send; drop and fail-open if channel is at capacity.
	select {
	case p.requestCh <- entry:
	default:
		return true, limit // channel full — fail-open
	}

	// Wait for the pipeline flusher to deliver the result.
	select {
	case r := <-resultCh:
		return r.allowed, r.remaining
	case <-time.After(rlCheckTimeout):
		return true, limit // Redis too slow — fail-open
	}
}

// Stop signals the background flusher to exit. Any in-flight entries are
// abandoned and their result channels are never written, so callers that are
// still waiting will time out and fail-open.
func (p *RedisRateLimitProvider) Stop() {
	close(p.stopCh)
}

// flusher runs in a background goroutine. It collects pending entries from
// requestCh (up to rlBatchMax or 1 ms, whichever comes first), executes them
// as a single Redis pipeline, and distributes individual results back to callers.
// Each pipeline command is independent — Redis processes them one by one and
// returns each counter value separately, so callers get distinct incrementing
// counts (not all-or-none).
func (p *RedisRateLimitProvider) flusher() {
	batch := make([]rlPendingEntry, 0, rlBatchMax)
	for {
		batch = batch[:0]

		// Wait for at least one entry before starting the deadline timer.
		select {
		case <-p.stopCh:
			return
		case first := <-p.requestCh:
			batch = append(batch, first)
		}

		// Drain more entries until the batch is full or the deadline fires.
		deadline := time.NewTimer(rlBatchDeadline)
	drain:
		for len(batch) < rlBatchMax {
			select {
			case entry := <-p.requestCh:
				batch = append(batch, entry)
			case <-deadline.C:
				break drain
			case <-p.stopCh:
				deadline.Stop()
				// Fail-open for anything already in batch.
				for i := range batch {
					batch[i].resultCh <- rlResult{allowed: true, remaining: batch[i].limit}
				}
				return
			}
		}
		deadline.Stop()

		p.execBatch(batch)
	}
}

// execBatch sends all entries in batch as a Redis pipeline and routes results.
func (p *RedisRateLimitProvider) execBatch(batch []rlPendingEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	pipe := p.client.Pipeline()
	cmds := make([]*goredis.Cmd, len(batch))
	for i, entry := range batch {
		cmds[i] = pipe.EvalSha(ctx, p.scriptSha, []string{entry.key}, entry.windowSecs)
	}

	_, pipeErr := pipe.Exec(ctx)
	// pipeErr is non-nil if ANY command failed, but individual cmds may still
	// have valid results. We inspect each cmd independently.

	for i, entry := range batch {
		var res rlResult
		if pipeErr != nil && cmds[i].Err() != nil {
			// This specific command failed — fail-open.
			log.Printf("[rate-limit] redis pipeline cmd error for key %q: %v", entry.key, cmds[i].Err())
			res = rlResult{allowed: true, remaining: entry.limit}
		} else {
			count, err := cmds[i].Int64()
			if err != nil {
				// Parse error — fail-open.
				res = rlResult{allowed: true, remaining: entry.limit}
			} else {
				c := uint32(count)
				if c <= entry.limit {
					remaining := uint32(0)
					if entry.limit > c {
						remaining = entry.limit - c
					}
					res = rlResult{allowed: true, remaining: remaining}
				} else {
					res = rlResult{allowed: false, remaining: 0}
				}
			}
		}
		entry.resultCh <- res
	}
}
