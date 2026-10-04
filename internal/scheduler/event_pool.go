package scheduler

import (
	"sync"
	"sync/atomic"
)

const eventPoolCap = 256

type eventPool struct {
	items [eventPoolCap]*ScheduledEvent
	count atomic.Int32
	mu    sync.Mutex
}

func newEventPool() *eventPool {
	p := &eventPool{}
	for i := range p.items {
		p.items[i] = &ScheduledEvent{}
	}
	p.count.Store(eventPoolCap)
	return p
}

// Get returns a pooled ScheduledEvent or allocates one if the pool is empty.
func (p *eventPool) Get() *ScheduledEvent {
	p.mu.Lock()
	n := p.count.Load()
	if n == 0 {
		p.mu.Unlock()
		return &ScheduledEvent{}
	}
	ev := p.items[n-1]
	p.items[n-1] = nil
	p.count.Store(n - 1)
	p.mu.Unlock()
	return ev
}

// Put resets ev and returns it to the pool.
func (p *eventPool) Put(ev *ScheduledEvent) {
	ev.Reset()
	p.mu.Lock()
	n := p.count.Load()
	if n < eventPoolCap {
		p.items[n] = ev
		p.count.Store(n + 1)
	}
	p.mu.Unlock()
}

var globalEventPool = newEventPool()
