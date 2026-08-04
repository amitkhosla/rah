package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"
)

const defaultBatchWindow = 500 * time.Microsecond
const defaultBatchMax = 100

// batchReq is a pooled single-key load request.
type batchReq struct {
	key  string
	resp chan batchResp
}

type batchResp struct {
	row []byte
	err error
}

var batchReqPool = sync.Pool{New: func() any { return &batchReq{resp: make(chan batchResp, 1)} }}

// BatchLoader coalesces single-key loads into ANY($1) bulk queries.
type BatchLoader struct {
	pool        *pgxpool.Pool
	paramSQL    string        // e.g. "SELECT id, data FROM orders WHERE id = ANY($1::bigint[])"
	window      time.Duration
	maxKeys     int
	queue       chan *batchReq
	sf          singleflight.Group
}

// NewBatchLoader creates and starts a BatchLoader.
func NewBatchLoader(ctx context.Context, pool *pgxpool.Pool, cfg NamedQueryConfig) *BatchLoader {
	window := defaultBatchWindow
	if cfg.BatchWindow != "" {
		if d, err := time.ParseDuration(cfg.BatchWindow); err == nil {
			window = d
		}
	}
	maxKeys := cfg.BatchMax
	if maxKeys <= 0 {
		maxKeys = defaultBatchMax
	}
	bl := &BatchLoader{
		pool:     pool,
		paramSQL: cfg.SQL,
		window:   window,
		maxKeys:  maxKeys,
		queue:    make(chan *batchReq, maxKeys*4),
	}
	go bl.dispatch(ctx)
	return bl
}

// Load fetches a single key, coalescing with concurrent callers within the batch window.
func (bl *BatchLoader) Load(ctx context.Context, key string) ([]byte, error) {
	// Singleflight: deduplicate identical concurrent keys.
	v, err, _ := bl.sf.Do(key, func() (any, error) {
		req := batchReqPool.Get().(*batchReq)
		req.key = key
		select {
		case bl.queue <- req:
		case <-ctx.Done():
			batchReqPool.Put(req)
			return nil, ctx.Err()
		}
		select {
		case r := <-req.resp:
			req.key = ""
			batchReqPool.Put(req)
			return r.row, r.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	return v.([]byte), nil
}

func (bl *BatchLoader) dispatch(ctx context.Context) {
	timer := time.NewTimer(bl.window)
	defer timer.Stop()
	batch := make([]*batchReq, 0, bl.maxKeys)
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-bl.queue:
			batch = append(batch, req)
			if len(batch) >= bl.maxKeys {
				bl.flush(ctx, batch)
				batch = batch[:0]
				if !timer.Stop() {
					select { case <-timer.C: default: }
				}
				timer.Reset(bl.window)
			}
		case <-timer.C:
			if len(batch) > 0 {
				bl.flush(ctx, batch)
				batch = batch[:0]
			}
			timer.Reset(bl.window)
		}
	}
}

func (bl *BatchLoader) flush(ctx context.Context, batch []*batchReq) {
	keys := make([]string, len(batch))
	for i, r := range batch {
		keys[i] = r.key
	}

	rows, err := bl.pool.Query(ctx, bl.paramSQL, keys)
	if err != nil {
		for _, r := range batch {
			r.resp <- batchResp{err: err}
		}
		return
	}
	defer rows.Close()

	// Build key→row map from results.
	results := make(map[string][]byte, len(batch))
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			continue
		}
		if len(vals) < 2 {
			continue
		}
		// First column is the key, remaining columns are marshalled as JSON.
		keyVal := fmt.Sprintf("%v", vals[0])
		rowBytes, _ := json.Marshal(vals[1:])
		results[keyVal] = rowBytes
	}

	for _, r := range batch {
		r.resp <- batchResp{row: results[r.key]}
	}
}
