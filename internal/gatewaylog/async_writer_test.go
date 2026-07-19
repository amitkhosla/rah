package gatewaylog

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// newTestPool creates a BufPool with default capacity for tests.
func newTestPool() *BufPool {
	return NewBufPool(0)
}

// newMsg creates a LogBuf from the pool with the given content.
func newMsg(pool *BufPool, content string) *LogBuf {
	lb := pool.Get()
	_, _ = lb.WriteString(content)
	return lb
}

// splitLines splits output into non-empty lines.
func splitLines(data []byte) []string {
	raw := strings.Split(string(data), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// -----------------------------------------------------------------------------
// 1. Basic: write 100 msgs, Stop, verify all 100 in order
// -----------------------------------------------------------------------------

func TestAsyncWriterBasic(t *testing.T) {
	pool := newTestPool()
	var buf bytes.Buffer

	w := NewAsyncWriter(&buf, pool, overflowDepth, batchDepth)
	w.Start()

	const n = 100
	for i := 0; i < n; i++ {
		msg := newMsg(pool, fmt.Sprintf("line-%04d", i))
		w.Write(msg, BlockOnFull)
	}
	w.Stop()

	lines := splitLines(buf.Bytes())
	if len(lines) != n {
		t.Fatalf("expected %d lines, got %d\noutput:\n%s", n, len(lines), buf.String())
	}
	for i, line := range lines {
		want := fmt.Sprintf("line-%04d", i)
		if line != want {
			t.Errorf("line[%d]: want %q, got %q", i, want, line)
		}
	}
}

// -----------------------------------------------------------------------------
// 2. NeverSplitsEntry: varied-length entries are never split mid-entry
// -----------------------------------------------------------------------------

func TestAsyncWriterNeverSplitsEntry(t *testing.T) {
	pool := newTestPool()
	var buf bytes.Buffer

	w := NewAsyncWriter(&buf, pool, overflowDepth, batchDepth)
	w.Start()

	const n = 50
	for i := 0; i < n; i++ {
		// Each entry has a unique fixed prefix followed by padding to make lengths vary.
		padLen := 10 + (i * 40) // 10..1970 bytes of padding
		content := fmt.Sprintf("entry-%03d:", i) + strings.Repeat("x", padLen)
		msg := newMsg(pool, content)
		w.Write(msg, BlockOnFull)
	}
	w.Stop()

	lines := splitLines(buf.Bytes())
	if len(lines) != n {
		t.Fatalf("expected %d lines, got %d", n, len(lines))
	}
	for i, line := range lines {
		prefix := fmt.Sprintf("entry-%03d:", i)
		if !strings.HasPrefix(line, prefix) {
			t.Errorf("line[%d] does not start with %q (len=%d): %q...", i, prefix, len(line), line[:min(len(line), 30)])
		}
		padLen := 10 + (i * 40)
		wantLen := len(prefix) + padLen
		if len(line) != wantLen {
			t.Errorf("line[%d]: want len %d, got %d", i, wantLen, len(line))
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// -----------------------------------------------------------------------------
// 3. IOStall: slow I/O writer; Write() must return quickly; no deadlock
// -----------------------------------------------------------------------------

type stallWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	stallMs int
}

func (s *stallWriter) Write(p []byte) (int, error) {
	time.Sleep(time.Duration(s.stallMs) * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func TestAsyncWriterIOStall(t *testing.T) {
	pool := newTestPool()
	stall := &stallWriter{stallMs: 100}

	w := NewAsyncWriter(stall, pool, overflowDepth, batchDepth)
	w.Start()

	const n = 10_000
	start := time.Now()
	for i := 0; i < n; i++ {
		msg := newMsg(pool, fmt.Sprintf("stall-%d", i))
		t0 := time.Now()
		w.Write(msg, DropSilently)
		elapsed := time.Since(t0)
		if elapsed > 5*time.Millisecond {
			t.Errorf("Write(%d) took %v (> 5ms); ring should not block", i, elapsed)
		}
	}
	writePhase := time.Since(start)
	if writePhase > 1*time.Second {
		t.Errorf("10k Write() calls took %v (> 1s total)", writePhase)
	}

	// Stop drains; allow enough time for stall writer to flush.
	stopped := make(chan struct{})
	go func() {
		w.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		// ok
	case <-time.After(30 * time.Second):
		t.Fatal("Stop() did not return within 30s (deadlock?)")
	}
}

// -----------------------------------------------------------------------------
// 4. BlockOnFull: goroutine unblocks when space is available
// -----------------------------------------------------------------------------

func TestAsyncWriterBlockOnFull(t *testing.T) {
	pool := newTestPool()

	// Use a blocking writer (io.Discard would be fine but we want controlled drain).
	pr, pw := io.Pipe()

	w := NewAsyncWriter(pw, pool, 4, 2) // tiny overflow + batchCh to trigger full
	w.Start()

	// Fill ring + overflow so the next BlockOnFull write will block.
	// Ring capacity = 8192; overflow = 4. We send enough to saturate both.
	filled := 0
	for filled < RingCap+4 {
		msg := newMsg(pool, fmt.Sprintf("fill-%d", filled))
		select {
		case w.overflow <- msg:
			filled++
		default:
			if !w.ring.TryEnqueue(msg) {
				pool.Put(msg)
				break
			}
			filled++
		}
		if filled >= RingCap+4 {
			break
		}
	}

	// Now send one more with BlockOnFull — it should block.
	blocked := make(chan struct{})
	unblocked := make(chan struct{})
	go func() {
		msg := newMsg(pool, "blocker")
		close(blocked)
		w.Write(msg, BlockOnFull)
		close(unblocked)
	}()

	<-blocked // goroutine started

	// Drain the pipe (read from pr) so the writer goroutine can proceed.
	go func() {
		io.Copy(io.Discard, pr) //nolint:errcheck
	}()

	// The Write should unblock within a reasonable time.
	select {
	case <-unblocked:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("BlockOnFull Write did not unblock within 5s")
	}

	_ = pw.Close()
	w.Stop()
}

// -----------------------------------------------------------------------------
// 5. DropCounter: filling ring+overflow with DropSilently increments Drops()
//
// Strategy: since the test is in package gatewaylog (white-box), we directly
// fill the ring (all RingCap slots) and the overflow channel, then call Write
// with DropSilently. The ring and overflow are both full, so the drop counter
// must increment. No goroutine scheduling dependency.
// -----------------------------------------------------------------------------

func TestAsyncWriterDropCounter(t *testing.T) {
	pool := newTestPool()

	// Use io.Discard so the pipeline can drain after the test.
	w := NewAsyncWriter(io.Discard, pool, overflowDepth, batchDepth)
	// Do NOT Start() yet — we fill the ring before the collector starts draining.

	// Fill the ring completely (white-box: w.ring is accessible within package).
	for i := 0; i < RingCap; i++ {
		msg := pool.Get()
		_, _ = msg.WriteString("ring-fill")
		if !w.ring.TryEnqueue(msg) {
			t.Fatalf("ring should have capacity for slot %d", i)
		}
	}

	// Verify ring is full.
	sentinel := pool.Get()
	_, _ = sentinel.WriteString("sentinel")
	if w.ring.TryEnqueue(sentinel) {
		t.Fatal("ring should be full but TryEnqueue succeeded")
	}
	pool.Put(sentinel)

	// Fill the overflow channel completely.
	for i := 0; i < overflowDepth; i++ {
		msg := pool.Get()
		_, _ = msg.WriteString("overflow-fill")
		w.overflow <- msg
	}

	// Now call Write with DropSilently — both ring and overflow are full.
	dropMsg := pool.Get()
	_, _ = dropMsg.WriteString("to-be-dropped")
	w.Write(dropMsg, DropSilently)

	if w.Drops() != 1 {
		t.Fatalf("expected Drops()=1, got %d", w.Drops())
	}

	// Start the pipeline and Stop it so it drains cleanly.
	w.Start()
	stopped := make(chan struct{})
	go func() {
		w.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() timed out during drain")
	}
}

// -----------------------------------------------------------------------------
// 6. GracefulStop: enqueue 500, immediately Stop, all 500 in output
// -----------------------------------------------------------------------------

func TestAsyncWriterGracefulStop(t *testing.T) {
	pool := newTestPool()
	var buf bytes.Buffer

	w := NewAsyncWriter(&buf, pool, overflowDepth, batchDepth)
	w.Start()

	const n = 500
	for i := 0; i < n; i++ {
		msg := newMsg(pool, fmt.Sprintf("graceful-%04d", i))
		w.Write(msg, BlockOnFull)
	}

	// Stop immediately — all entries must be flushed.
	w.Stop()

	lines := splitLines(buf.Bytes())
	if len(lines) != n {
		t.Fatalf("graceful stop: expected %d lines in output, got %d (drops=%d)",
			n, len(lines), w.Drops())
	}
}

// -----------------------------------------------------------------------------
// 7. Concurrent: 100 goroutines × 500 writes, DropSilently; lines+drops == total
// -----------------------------------------------------------------------------

func TestAsyncWriterConcurrent(t *testing.T) {
	pool := newTestPool()
	var buf bytes.Buffer

	safe := &safeWriter{w: &buf}

	w := NewAsyncWriter(safe, pool, overflowDepth, batchDepth)
	w.Start()

	const goroutines = 100
	const perGoroutine = 500
	const total = goroutines * perGoroutine

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				msg := newMsg(pool, fmt.Sprintf("g%03d-i%04d", gid, i))
				w.Write(msg, DropSilently)
			}
		}(g)
	}
	wg.Wait()
	w.Stop()

	lines := splitLines(buf.Bytes())
	got := int64(len(lines)) + int64(w.Drops())
	if got != total {
		t.Errorf("lines(%d) + drops(%d) = %d, want %d",
			len(lines), w.Drops(), got, total)
	}
}

// safeWriter wraps an io.Writer with a mutex for test use.
type safeWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *safeWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// Ensure atomic.Uint64 usage in the test compiles (import check).
var _ = atomic.Uint64{}
