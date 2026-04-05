package ingest

import (
	"log"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ── sink worker ───────────────────────────────────────────────────────────────

// sinkWorker manages one named sink: channel, formatter, and a dynamic goroutine
// pool that scales between minWorkers and maxWorkers based on channel fill level.
type sinkWorker struct {
	name           string
	ch             chan Event  // buffered; capacity = SinkQueueSize
	sink           Sink
	formatter      Formatter
	maxItems       int // flush when batch reaches this count
	maxBytes       int // flush when batch bytes reach this
	flushMs        int // flush timer interval in milliseconds
	minWorkers     int // permanent goroutine count
	maxWorkers     int // goroutine cap
	currentWorkers atomic.Int32
	stopCh         chan struct{} // closed on Stop()
	wg             sync.WaitGroup
}

// startOneWorker spawns exactly one run goroutine, incrementing currentWorkers
// before launch so scaleMonitor and runWorker stay in sync.
func (sw *sinkWorker) startOneWorker() {
	sw.wg.Add(1)
	sw.currentWorkers.Add(1)
	go func() {
		defer sw.wg.Done()
		defer sw.currentWorkers.Add(-1)
		sw.runWorker()
	}()
}

// startWorkers spawns n run goroutines (called at startup).
func (sw *sinkWorker) startWorkers(n int) {
	for i := 0; i < n; i++ {
		sw.startOneWorker()
	}
}

// runWorker is the per-goroutine work loop. It batches events from ch and
// flushes them via formatter → sink. It volunteers to exit when the channel
// is lightly loaded and currentWorkers > minWorkers (scale-down).
func (sw *sinkWorker) runWorker() {
	batch := make([]Event, 0, sw.maxItems)
	batchBytes := 0
	ticker := time.NewTicker(time.Duration(sw.flushMs) * time.Millisecond)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		formatted, err := sw.formatter.Format(batch)
		if err == nil {
			if werr := sw.sink.Write(formatted); werr != nil {
				log.Printf("[ingest] sink %s write error: %v", sw.name, werr)
			}
		} else {
			log.Printf("[ingest] sink %s format error: %v", sw.name, err)
		}
		for i := range batch {
			batch[i].releasePayload()
		}
		batch = batch[:0]
		batchBytes = 0
	}

	for {
		select {
		case e, ok := <-sw.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, e)
			batchBytes += len(e.Payload())
			if len(batch) >= sw.maxItems || batchBytes >= sw.maxBytes {
				flush()
			}

		case <-ticker.C:
			flush()
			// Scale-down check after timer flush: if we are above minWorkers
			// and the channel is lightly loaded, volunteer to exit.
			if sw.currentWorkers.Load() > int32(sw.minWorkers) {
				fill := 0
				if cap(sw.ch) > 0 {
					fill = len(sw.ch) * 100 / cap(sw.ch)
				}
				if fill < 20 {
					return // defer will decrement currentWorkers
				}
			}

		case <-sw.stopCh:
			// Drain remaining items from channel before exiting.
		drainLoop:
			for {
				select {
				case e, ok := <-sw.ch:
					if !ok {
						flush()
						return
					}
					batch = append(batch, e)
					batchBytes += len(e.Payload())
				default:
					break drainLoop
				}
			}
			flush()
			return
		}
	}
}

// scaleMonitor runs as a background goroutine and spawns additional runWorker
// goroutines when channel fill is high or growing. Scale-down is handled inside
// runWorker itself (timer-flush path).
func (sw *sinkWorker) scaleMonitor(stopCh chan struct{}) {
	defer sw.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	prevLen := 0

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			cur := len(sw.ch)
			cCap := cap(sw.ch)
			fill := 0
			if cCap > 0 {
				fill = cur * 100 / cCap
			}
			growing := cur > prevLen
			prevLen = cur

			workers := sw.currentWorkers.Load()
			if (fill > 70 || (fill > 40 && growing)) && workers < int32(sw.maxWorkers) {
				sw.startOneWorker()
			}
		}
	}
}

// ── kindRing ──────────────────────────────────────────────────────────────────

// kindRing holds the ring and routing info for one EventKind.
type kindRing struct {
	ring      *Ring
	overflow  chan Event // non-nil only when allowDrop=false (backpressure path)
	allowDrop bool
	priority  int // lower = drained first by fanout goroutines
	sinks     []*sinkWorker
}

// ── Pipeline ──────────────────────────────────────────────────────────────────

// Pipeline is the central per-kind event bus. Callers call Emit() non-blocking;
// fanout goroutines drain rings and route events to per-sink worker pools.
// Safe for concurrent use from any goroutine.
type Pipeline struct {
	kinds      map[EventKind]*kindRing
	allWorkers []*sinkWorker // deduplicated across all kinds
	sorted     []*kindRing   // allWorkers sorted by ascending priority; built once
	fanoutN    int
	timeout    time.Duration // backpressure timeout for allowDrop=false rings
	wg         sync.WaitGroup
	once       sync.Once
	stopCh     chan struct{}
}

// newPipeline builds and starts the pipeline. Called only from factory.go.
//
//   - kinds:      per-EventKind routing config
//   - allWorkers: all unique sinkWorker instances (no duplicates)
//   - fanoutN:    number of fanout goroutines (use 2 when unsure)
//   - timeout:    backpressure park timeout for allowDrop=false kinds (use 30s)
func newPipeline(
	kinds map[EventKind]*kindRing,
	allWorkers []*sinkWorker,
	fanoutN int,
	timeout time.Duration,
) *Pipeline {
	if fanoutN <= 0 {
		fanoutN = 2
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	p := &Pipeline{
		kinds:      kinds,
		allWorkers: allWorkers,
		fanoutN:    fanoutN,
		timeout:    timeout,
		stopCh:     make(chan struct{}),
	}

	// Build priority-sorted slice once; fanOut goroutines reuse it.
	p.sorted = p.sortedKinds()

	// Start sink worker pools.
	for _, sw := range allWorkers {
		sw.startWorkers(sw.minWorkers)
		sw.wg.Add(1)
		go sw.scaleMonitor(p.stopCh)
	}

	// Start fanout goroutines.
	for i := 0; i < fanoutN; i++ {
		p.wg.Add(1)
		go p.fanOut()
	}

	return p
}

// sortedKinds returns a slice of all kindRings ordered by ascending priority.
func (p *Pipeline) sortedKinds() []*kindRing {
	out := make([]*kindRing, 0, len(p.kinds))
	for _, kr := range p.kinds {
		out = append(out, kr)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].priority < out[j].priority
	})
	return out
}

// Emit sends an event into the pipeline. Never blocks the caller.
// If the kind's ring is full and allowDrop is true the event is silently
// dropped. If allowDrop is false the event is parked in the overflow channel
// for up to p.timeout before being dropped as a last resort.
func (p *Pipeline) Emit(e Event) {
	kr := p.kinds[e.Kind]
	if kr == nil {
		return
	}
	if kr.ring.TryPush(e) {
		return
	}
	// Ring full — try overflow channel as second level before any drop.
	if kr.allowDrop {
		// Low-priority: non-blocking overflow attempt; drop only if overflow is also full.
		select {
		case kr.overflow <- e:
		default:
			// Both ring and overflow full — true last-resort drop.
			log.Printf("[ingest] ring+overflow full for kind %s — dropping event", e.Kind)
		}
		return
	}
	// High-priority (allowDrop=false): block in overflow with timeout as absolute last resort.
	select {
	case kr.overflow <- e:
	case <-time.After(p.timeout):
		log.Printf("[ingest] backpressure timeout for kind %s — dropping event", e.Kind)
	}
}

// NumSinksForKind returns the number of sink workers that receive events of
// this kind. Used by the emit step to pre-set sharedPayload ref counts.
// Returns 0 if the kind is not registered.
func (p *Pipeline) NumSinksForKind(kind EventKind) int {
	if kr := p.kinds[kind]; kr != nil {
		return len(kr.sinks)
	}
	return 0
}

// Stop drains remaining events, closes all sinks, and waits for all goroutines
// to exit. Safe to call multiple times; only the first call takes effect.
func (p *Pipeline) Stop() {
	p.once.Do(func() {
		close(p.stopCh)
		p.wg.Wait() // wait for fanout goroutines

		// After fanout goroutines finish, close each sink worker's channel
		// so its runWorker goroutines flush and exit.
		for _, sw := range p.allWorkers {
			close(sw.ch)
			sw.wg.Wait()
			if err := sw.sink.Close(); err != nil {
				log.Printf("[ingest] sink %s close error: %v", sw.name, err)
			}
		}
	})
}

// ── fanOut ────────────────────────────────────────────────────────────────────

// fanOut is the drain goroutine. It iterates kindRings in priority order,
// popping events from each ring (and overflow channel) and forwarding them to
// each sink worker's channel. Multiple fanOut goroutines run concurrently;
// the underlying Ring is MPMC-safe.
func (p *Pipeline) fanOut() {
	defer p.wg.Done()

	sorted := p.sorted // immutable after newPipeline; safe to read concurrently

	idleRounds := 0
	for {
		select {
		case <-p.stopCh:
			p.drainAll(sorted)
			return
		default:
		}

		dispatched := false
		for _, kr := range sorted {
			// Drain ring.
			for {
				e, ok := kr.ring.Pop()
				if !ok {
					break
				}
				dispatched = true
				for _, sw := range kr.sinks {
					sw.ch <- e // blocking: never drop on fanout→sink path
				}
			}
			// Drain overflow channel (non-blocking).
			if kr.overflow != nil {
			drainOverflow:
				for {
					select {
					case e := <-kr.overflow:
						for _, sw := range kr.sinks {
							sw.ch <- e
						}
					default:
						break drainOverflow
					}
				}
			}
		}

		if !dispatched {
			idleRounds++
			if idleRounds < 128 {
				runtime.Gosched()
			} else {
				time.Sleep(1 * time.Millisecond)
				idleRounds = 0
			}
		} else {
			idleRounds = 0
		}
	}
}

// drainAll performs a best-effort drain of all rings after stopCh is closed.
// Events are forwarded to sink channels with a non-blocking send so a full
// sink queue does not stall the shutdown path.
func (p *Pipeline) drainAll(sorted []*kindRing) {
	for _, kr := range sorted {
		for {
			e, ok := kr.ring.Pop()
			if !ok {
				break
			}
			for _, sw := range kr.sinks {
				select {
				case sw.ch <- e:
				default: // sink queue also stopping; best effort
				}
			}
		}
	}
}
