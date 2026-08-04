package ws

import "context"

// EventHandler processes a single WSEvent.
type EventHandler func(event WSEvent)

// Dispatcher is a fixed-size goroutine pool that processes WSEvents.
type Dispatcher struct {
	ch      chan WSEvent
	handler EventHandler
	workers int
}

// NewDispatcher creates a Dispatcher with the given worker count and channel buffer size.
func NewDispatcher(workers int, bufSize int, handler EventHandler) *Dispatcher {
	return &Dispatcher{
		ch:      make(chan WSEvent, bufSize),
		handler: handler,
		workers: workers,
	}
}

// Start launches the worker goroutines. It returns immediately; workers run
// until ctx is cancelled and the channel is drained.
func (d *Dispatcher) Start(ctx context.Context) {
	for i := 0; i < d.workers; i++ {
		go func() {
			for {
				select {
				case event, ok := <-d.ch:
					if !ok {
						return
					}
					d.handler(event)
				case <-ctx.Done():
					return
				}
			}
		}()
	}
}

// Dispatch submits an event to the worker pool non-blocking.
// Returns false if the channel buffer is full.
func (d *Dispatcher) Dispatch(event WSEvent) bool {
	select {
	case d.ch <- event:
		return true
	default:
		return false
	}
}
