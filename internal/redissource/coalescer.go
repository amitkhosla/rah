package redissource

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const defaultBatchWindow = 200 * time.Microsecond
const defaultBatchMax    = 100

// getReq is a pooled get request.
type getReq struct {
	key  string
	resp chan getResp
}

type getResp struct {
	val []byte
	err error
}

// setReq is a pooled set request.
type setReq struct {
	key  string
	val  []byte
	ttl  time.Duration
	resp chan error
}

// delReq is a pooled delete request.
type delReq struct {
	keys []string
	resp chan error
}

var getReqPool = sync.Pool{New: func() any { return &getReq{resp: make(chan getResp, 1)} }}
var setReqPool = sync.Pool{New: func() any { return &setReq{resp: make(chan error, 1)} }}
var delReqPool = sync.Pool{New: func() any { return &delReq{resp: make(chan error, 1)} }}

// RedisCoalescer wraps a redis client with batching for GET/SET/DEL.
type RedisCoalescer struct {
	client      redis.UniversalClient
	batchWindow time.Duration
	batchMax    int
	getQueue    chan *getReq
	setQueue    chan *setReq
	delQueue    chan *delReq
}

// newCoalescer creates a coalescer around an existing client.
func newCoalescer(client redis.UniversalClient, batchWindow time.Duration, batchMax int) *RedisCoalescer {
	if batchWindow <= 0 {
		batchWindow = defaultBatchWindow
	}
	if batchMax <= 0 {
		batchMax = defaultBatchMax
	}
	return &RedisCoalescer{
		client:      client,
		batchWindow: batchWindow,
		batchMax:    batchMax,
		getQueue:    make(chan *getReq, batchMax*4),
		setQueue:    make(chan *setReq, batchMax*4),
		delQueue:    make(chan *delReq, batchMax*4),
	}
}

// Start launches dispatch workers. Must be called before any Get/Set/Del.
func (c *RedisCoalescer) Start(ctx context.Context) {
	go c.getWorker(ctx)
	go c.setWorker(ctx)
	go c.delWorker(ctx)
}

// Close closes the underlying redis client.
func (c *RedisCoalescer) Close() error {
	return c.client.Close()
}

// Get enqueues a get request and waits for the result.
func (c *RedisCoalescer) Get(ctx context.Context, key string) ([]byte, error) {
	req := getReqPool.Get().(*getReq)
	req.key = key
	select {
	case c.getQueue <- req:
	case <-ctx.Done():
		getReqPool.Put(req)
		return nil, ctx.Err()
	}
	select {
	case r := <-req.resp:
		req.key = ""
		getReqPool.Put(req)
		return r.val, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Set enqueues a set request.
func (c *RedisCoalescer) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	req := setReqPool.Get().(*setReq)
	req.key, req.val, req.ttl = key, val, ttl
	select {
	case c.setQueue <- req:
	case <-ctx.Done():
		setReqPool.Put(req)
		return ctx.Err()
	}
	select {
	case err := <-req.resp:
		req.key, req.val = "", nil
		setReqPool.Put(req)
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// MGet retrieves multiple keys.
func (c *RedisCoalescer) MGet(ctx context.Context, keys []string) ([][]byte, error) {
	vals, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([][]byte, len(vals))
	for i, v := range vals {
		if v != nil {
			if s, ok := v.(string); ok {
				out[i] = []byte(s)
			}
		}
	}
	return out, nil
}

// MSet stores multiple pairs.
func (c *RedisCoalescer) MSet(ctx context.Context, pairs map[string][]byte, ttl time.Duration) error {
	if ttl == 0 {
		args := make([]any, 0, len(pairs)*2)
		for k, v := range pairs {
			args = append(args, k, v)
		}
		return c.client.MSet(ctx, args...).Err()
	}
	pipe := c.client.Pipeline()
	for k, v := range pairs {
		pipe.Set(ctx, k, v, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// Del enqueues a delete request.
func (c *RedisCoalescer) Del(ctx context.Context, keys ...string) error {
	req := delReqPool.Get().(*delReq)
	req.keys = keys
	select {
	case c.delQueue <- req:
	case <-ctx.Done():
		delReqPool.Put(req)
		return ctx.Err()
	}
	select {
	case err := <-req.resp:
		req.keys = nil
		delReqPool.Put(req)
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *RedisCoalescer) getWorker(ctx context.Context) {
	timer := time.NewTimer(c.batchWindow)
	defer timer.Stop()
	batch := make([]*getReq, 0, c.batchMax)
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-c.getQueue:
			batch = append(batch, req)
			if len(batch) >= c.batchMax {
				c.flushGet(ctx, batch)
				batch = batch[:0]
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(c.batchWindow)
			}
		case <-timer.C:
			if len(batch) > 0 {
				c.flushGet(ctx, batch)
				batch = batch[:0]
			}
			timer.Reset(c.batchWindow)
		}
	}
}

func (c *RedisCoalescer) flushGet(ctx context.Context, batch []*getReq) {
	keys := make([]string, len(batch))
	for i, r := range batch {
		keys[i] = r.key
	}
	vals, err := c.client.MGet(ctx, keys...).Result()
	for i, r := range batch {
		if err != nil {
			r.resp <- getResp{err: err}
			continue
		}
		var b []byte
		if vals[i] != nil {
			if s, ok := vals[i].(string); ok {
				b = []byte(s)
			}
		}
		r.resp <- getResp{val: b}
	}
}

func (c *RedisCoalescer) setWorker(ctx context.Context) {
	timer := time.NewTimer(c.batchWindow)
	defer timer.Stop()
	batch := make([]*setReq, 0, c.batchMax)
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-c.setQueue:
			batch = append(batch, req)
			if len(batch) >= c.batchMax {
				c.flushSet(ctx, batch)
				batch = batch[:0]
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(c.batchWindow)
			}
		case <-timer.C:
			if len(batch) > 0 {
				c.flushSet(ctx, batch)
				batch = batch[:0]
			}
			timer.Reset(c.batchWindow)
		}
	}
}

func (c *RedisCoalescer) flushSet(ctx context.Context, batch []*setReq) {
	// Separate TTL and no-TTL sets.
	noTTL := make([]any, 0, len(batch)*2)
	var withTTL []*setReq
	for _, r := range batch {
		if r.ttl == 0 {
			noTTL = append(noTTL, r.key, r.val)
		} else {
			withTTL = append(withTTL, r)
		}
	}
	var merr error
	if len(noTTL) > 0 {
		merr = c.client.MSet(ctx, noTTL...).Err()
	}
	if len(withTTL) > 0 {
		pipe := c.client.Pipeline()
		for _, r := range withTTL {
			pipe.Set(ctx, r.key, r.val, r.ttl)
		}
		_, _ = pipe.Exec(ctx)
	}
	for _, r := range batch {
		if r.ttl == 0 {
			r.resp <- merr
		} else {
			r.resp <- nil
		}
	}
}

func (c *RedisCoalescer) delWorker(ctx context.Context) {
	timer := time.NewTimer(c.batchWindow)
	defer timer.Stop()
	batch := make([]*delReq, 0, c.batchMax)
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-c.delQueue:
			batch = append(batch, req)
			if len(batch) >= c.batchMax {
				c.flushDel(ctx, batch)
				batch = batch[:0]
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(c.batchWindow)
			}
		case <-timer.C:
			if len(batch) > 0 {
				c.flushDel(ctx, batch)
				batch = batch[:0]
			}
			timer.Reset(c.batchWindow)
		}
	}
}

func (c *RedisCoalescer) flushDel(ctx context.Context, batch []*delReq) {
	all := make([]string, 0, len(batch)*2)
	for _, r := range batch {
		all = append(all, r.keys...)
	}
	err := c.client.Del(ctx, all...).Err()
	for _, r := range batch {
		r.resp <- err
	}
}
