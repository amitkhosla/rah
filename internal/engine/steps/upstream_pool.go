package steps

import (
	"sync"
	"sync/atomic"
)

type hostPoolEntry struct {
	host string
	pool *upstreamTransportPool
}

// upstreamPoolTable is a per-step copy-on-write pool table.
// Lock-free reads; mutex-guarded copy-on-write for new hosts (rare).
type upstreamPoolTable struct {
	mu      sync.Mutex
	entries atomic.Pointer[[]hostPoolEntry]
}

func (t *upstreamPoolTable) get(host string) *upstreamTransportPool {
	snap := t.entries.Load()
	if snap == nil {
		return nil
	}
	for _, e := range *snap {
		if e.host == host {
			return e.pool
		}
	}
	return nil
}

// getOrCreate returns an existing pool for host, or calls build() to create one.
// build() is called at most once per host; subsequent calls return cached pool.
func (t *upstreamPoolTable) getOrCreate(host string, build func() *upstreamTransportPool) *upstreamTransportPool {
	if p := t.get(host); p != nil {
		return p
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if p := t.get(host); p != nil {
		return p
	}
	p := build()
	old := t.entries.Load()
	var prev []hostPoolEntry
	if old != nil {
		prev = *old
	}
	next := make([]hostPoolEntry, len(prev)+1)
	copy(next, prev)
	next[len(prev)] = hostPoolEntry{host: host, pool: p}
	t.entries.Store(&next)
	return p
}
