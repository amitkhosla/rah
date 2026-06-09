package gatewaylog

import (
	"bufio"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// overflowDepth is the default depth of the overflow channel.
	overflowDepth = 65536
	// batchDepth is the default depth of the batch channel.
	batchDepth = 256
	// batchFlushSize is the max batch size before the collector sends it.
	batchFlushSize = 768 * 1024 // 768 KiB — safety flush during 10ms batching window
)

// AsyncWriter is a two-stage lock-free async log pipeline:
//
//	Writers → [Ring, lock-free] → Collector goroutine → io.Writer (via bufio)
//	               ↓ full
//	        [overflow chan *LogBuf]
//	               ↓ full + DropSilently  → drops counter
//	               ↓ full + BlockOnFull   → block until space
//
// AsyncWriter implements LogWriter.
type AsyncWriter struct {
	ring     *Ring
	pool     *BufPool
	overflow chan *LogBuf // safety valve when ring full
	drops    atomic.Uint64
	out      io.Writer
	stop     chan struct{} // closed to signal collector to stop
	done     sync.WaitGroup
}

// NewAsyncWriter creates an AsyncWriter that writes to out using the given BufPool.
// overflowDepth controls the overflow channel depth; batchDepth is kept for backward compatibility
// but is ignored in the 2-stage design.
// Call Start() before using Write().
func NewAsyncWriter(out io.Writer, pool *BufPool, overflowDepth, batchDepth int) *AsyncWriter {
	if overflowDepth <= 0 {
		overflowDepth = 65536
	}
	// batchDepth is ignored in the new 2-stage design
	_ = batchDepth
	w := &AsyncWriter{
		ring:     NewRing(),
		pool:     pool,
		overflow: make(chan *LogBuf, overflowDepth),
		out:      out,
		stop:     make(chan struct{}),
	}
	return w
}

// Start launches the collector goroutine. Must be called once before Write().
func (w *AsyncWriter) Start() {
	w.done.Add(1)
	go w.collector()
}

// Write enqueues msg into the pipeline. Fast path: lock-free ring TryEnqueue (~15ns).
// Falls back to overflow channel. On overflow full: drops (DropSilently) or blocks (BlockOnFull).
// msg is consumed by the pipeline; caller must not use it after this call.
func (w *AsyncWriter) Write(msg *LogBuf, policy DropPolicy) {
	// Fast path: lock-free ring enqueue
	if w.ring.TryEnqueue(msg) {
		return
	}

	// Ring full: try non-blocking overflow send
	select {
	case w.overflow <- msg:
		return
	default:
	}

	// Both ring and overflow are full
	if policy == DropSilently {
		w.drops.Add(1)
		w.pool.Put(msg)
		return
	}
	// BlockOnFull: block until overflow has space (back-pressure)
	w.overflow <- msg
}

// Drops returns the total number of log entries dropped due to backpressure.
func (w *AsyncWriter) Drops() uint64 {
	return w.drops.Load()
}

// Stop signals the collector to stop, drains all buffered entries, and waits for all
// goroutines to finish. After Stop() returns, all entries enqueued before Stop() have
// been written to out.
func (w *AsyncWriter) Stop() {
	close(w.stop)
	w.done.Wait()
}

// collector is the single consumer of the ring (MPSC contract).
// It drains ring + overflow and writes directly to out via bufio.
// It always sleeps 10ms between drain cycles for better batching.
func (w *AsyncWriter) collector() {
	defer w.done.Done()

	buf := make([]byte, 0, batchFlushSize)
	out := bufio.NewWriterSize(w.out, 64*1024)

	for {
		// Non-blocking stop check
		select {
		case <-w.stop:
			// Drain ring completely
			for {
				msg := w.ring.Dequeue()
				if msg == nil {
					break
				}
				buf = append(buf, msg.Bytes()...)
				buf = append(buf, '\n')
				w.pool.Put(msg)
				if len(buf) >= batchFlushSize {
					_, _ = out.Write(buf)
					buf = buf[:0]
				}
			}
			// Drain overflow channel completely
			for {
				select {
				case msg := <-w.overflow:
					buf = append(buf, msg.Bytes()...)
					buf = append(buf, '\n')
					w.pool.Put(msg)
					if len(buf) >= batchFlushSize {
						_, _ = out.Write(buf)
						buf = buf[:0]
					}
				default:
					goto drainDone
				}
			}
		drainDone:
			if len(buf) > 0 {
				_, _ = out.Write(buf)
			}
			_ = out.Flush()
			return
		default:
		}

		// Drain all available messages from ring, then overflow
		for {
			msg := w.ring.Dequeue()
			if msg == nil {
				select {
				case msg = <-w.overflow:
				default:
				}
			}
			if msg == nil {
				break
			}
			buf = append(buf, msg.Bytes()...)
			buf = append(buf, '\n')
			w.pool.Put(msg)
			if len(buf) >= batchFlushSize {
				_, _ = out.Write(buf)
				buf = buf[:0]
				_ = out.Flush()
			}
		}

		// Flush whatever accumulated, then always sleep 10ms to batch next window
		if len(buf) > 0 {
			_, _ = out.Write(buf)
			buf = buf[:0]
			_ = out.Flush()
		}
		time.Sleep(10 * time.Millisecond)
	}
}
