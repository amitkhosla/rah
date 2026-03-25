// Package eventmanager routes cache.WriteEvents to a set of named, filterable
// sinks. It registers a single cache.EventHandler subscriber with one or more
// CacheManager instances and fans events out internally.
//
// Dependency direction: eventmanager → cache (one-way; cache has no knowledge
// of this package).
//
// Typical wiring at startup:
//
//	em := eventmanager.New()
//	em.Register(cacheManager)
//	em.Add(eventmanager.Handler{
//	    Name:  "datastore-sync",
//	    Async: true,
//	    Handle: func(ev cache.WriteEvent) {
//	        ds.Set(ev.TenantID, ev.Key, ev.Value, ev.TTL)
//	    },
//	})
package eventmanager

import (
	"rah/internal/cache"
	"sync"
)

// Handler is a named sink for cache write events.
type Handler struct {
	// Name identifies this handler; must be unique within an EventManager.
	// Add replaces any existing handler with the same name.
	Name string

	// Handle is called for each event that passes Filter.
	Handle func(ev cache.WriteEvent)

	// Filter, if non-nil, gates delivery. Handle is only called when Filter
	// returns true. A nil Filter matches every event.
	Filter func(ev cache.WriteEvent) bool

	// Async, when true, spawns Handle in a new goroutine per event so it
	// cannot stall the cache dispatch loop. Use for handlers that do I/O
	// (datastore writes, HTTP callbacks, metrics, etc.).
	// When false (default), Handle runs synchronously in the dispatch goroutine.
	Async bool
}

// EventManager fans out cache.WriteEvents to a set of named handlers.
// A single instance can be registered with multiple CacheManagers.
type EventManager struct {
	handlers []Handler
	mu       sync.RWMutex
}

// New creates an idle EventManager with no handlers registered.
// Call Register to attach it to a CacheManager.
func New() *EventManager {
	return &EventManager{}
}

// Register wires the EventManager into cm as a single cache subscriber.
// All cache write events will flow through this manager's handler set.
// May be called more than once to attach to multiple CacheManagers.
func (em *EventManager) Register(cm *cache.CacheManager) {
	cm.Subscribe(em.dispatch)
}

// Add registers a handler. If a handler with the same Name already exists
// it is replaced in-place, preserving its position in dispatch order.
// Safe to call after Register; takes effect for the next dispatched event.
func (em *EventManager) Add(h Handler) {
	em.mu.Lock()
	defer em.mu.Unlock()
	for i, existing := range em.handlers {
		if existing.Name == h.Name {
			em.handlers[i] = h
			return
		}
	}
	em.handlers = append(em.handlers, h)
}

// Remove unregisters the handler with the given name.
// No-op if the name is not found.
func (em *EventManager) Remove(name string) {
	em.mu.Lock()
	defer em.mu.Unlock()
	for i, h := range em.handlers {
		if h.Name == name {
			em.handlers = append(em.handlers[:i], em.handlers[i+1:]...)
			return
		}
	}
}

// Handlers returns a snapshot of currently registered handler names
// in dispatch order.
func (em *EventManager) Handlers() []string {
	em.mu.RLock()
	defer em.mu.RUnlock()
	names := make([]string, len(em.handlers))
	for i, h := range em.handlers {
		names[i] = h.Name
	}
	return names
}

// dispatch is the cache.EventHandler registered with each CacheManager.
// It fans ev out to all registered handlers, respecting Filter and Async.
func (em *EventManager) dispatch(ev cache.WriteEvent) {
	em.mu.RLock()
	handlers := em.handlers
	em.mu.RUnlock()

	for _, h := range handlers {
		if h.Filter != nil && !h.Filter(ev) {
			continue
		}
		if h.Async {
			fn := h.Handle // capture for goroutine
			go fn(ev)
		} else {
			h.Handle(ev)
		}
	}
}
