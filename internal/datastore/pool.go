package datastore

import (
	"context"
	"fmt"
	"sync/atomic"
)

// PoolStats exposes lightweight pool telemetry for observability endpoints.
type PoolStats struct {
	MaxOpen int64 `json:"max_open"`
	InUse   int64 `json:"in_use"`
	Waiters int64 `json:"waiters"`
}

// ConnectionPool abstracts backend client pooling.
type ConnectionPool interface {
	Acquire(ctx context.Context) (func(), error)
	Stats() PoolStats
	Close() error
}

// tokenPool is a bounded-concurrency pool used until backend-native clients are wired.
type tokenPool struct {
	maxOpen int64
	tokens  chan struct{}
	closed  atomic.Bool
	waiters atomic.Int64
}

func NewTokenPool(maxOpen int) ConnectionPool {
	if maxOpen <= 0 {
		maxOpen = 32
	}
	return &tokenPool{maxOpen: int64(maxOpen), tokens: make(chan struct{}, maxOpen)}
}

func (p *tokenPool) Acquire(ctx context.Context) (func(), error) {
	if p.closed.Load() {
		return nil, fmt.Errorf("pool closed")
	}

	p.waiters.Add(1)
	defer p.waiters.Add(-1)

	select {
	case p.tokens <- struct{}{}:
		return func() {
			select {
			case <-p.tokens:
			default:
			}
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *tokenPool) Stats() PoolStats {
	return PoolStats{
		MaxOpen: p.maxOpen,
		InUse:   int64(len(p.tokens)),
		Waiters: p.waiters.Load(),
	}
}

func (p *tokenPool) Close() error {
	p.closed.Store(true)
	return nil
}
