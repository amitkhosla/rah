package cache

// WriteEvent is a snapshot of a cache entry emitted asynchronously after every
// successful in-memory Put. The Key and Value slices reference the original
// caller buffers — copy them if you need to retain them beyond the handler.
//
// To update the cached value from inside a handler, call cm.Update (not cm.Put,
// which would re-emit another WriteEvent and recurse).
type WriteEvent struct {
	TenantID uint16
	Key      []byte
	Value    []byte
	TTL      uint32
}

// EventHandler is called asynchronously in a dedicated dispatch goroutine after
// every successful Put. Handlers are called sequentially in subscription order.
//
// Updating the cache from a handler:
//
//	cm.Subscribe(func(ev cache.WriteEvent) {
//	    if needsUpdate(ev) {
//	        cm.Update(ev.TenantID, ev.Key, enriched(ev.Value), ev.TTL)
//	    }
//	})
type EventHandler func(ev WriteEvent)
