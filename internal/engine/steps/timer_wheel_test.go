package steps

import (
	"context"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amitkhosla/rah/internal/rctx"
)

type mockBody struct {
	closed atomic.Bool
}

func (m *mockBody) Read([]byte) (int, error) { return 0, io.EOF }
func (m *mockBody) Close() error             { m.closed.Store(true); return nil }

var wheelTestCtx context.Context
var wheelTestCancel context.CancelFunc

func TestMain(m *testing.M) {
	wheelTestCtx, wheelTestCancel = context.WithCancel(context.Background())
	StartTimerWheel(wheelTestCtx)
	code := m.Run()
	wheelTestCancel()
	time.Sleep(100 * time.Millisecond)
	os.Exit(code)
}

func TestTimerWheelCtxFiresAfterDuration(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()
	defer ctx.ReleaseOverflow()

	// SetUpstreamTimeout arms doneChan and returns the current generation.
	gen := ctx.SetUpstreamTimeout(10 * time.Second)
	handle := scheduleCtx(ctx, gen, 1*time.Second)

	if handle.idx == 0 {
		t.Fatal("scheduleCtx returned zero handle â€” pool or slot exhausted")
	}

	time.Sleep(2500 * time.Millisecond)

	if ctx.Err() == nil {
		t.Error("context should be cancelled after timer fires")
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", ctx.Err())
	}
}

func TestTimerWheelCancelPreventsCtxFire(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()
	defer ctx.ReleaseOverflow()

	gen := ctx.SetUpstreamTimeout(10 * time.Second)
	handle := scheduleCtx(ctx, gen, 2*time.Second)

	if handle.idx == 0 {
		t.Fatal("scheduleCtx returned zero handle")
	}

	handle.cancel()
	time.Sleep(3 * time.Second)

	if ctx.Err() != nil {
		t.Errorf("context should not be cancelled after handle.cancel(), got %v", ctx.Err())
	}
}

func TestTimerWheelBodyClosedAfterDuration(t *testing.T) {
	body := &mockBody{}
	handle := scheduleBody(body, 1*time.Second)

	if handle.idx == 0 {
		t.Fatal("scheduleBody returned zero handle")
	}

	time.Sleep(2500 * time.Millisecond)

	if !body.closed.Load() {
		t.Error("body.Close() should have been called after timer fires")
	}
}

func TestTimerWheelCancelPreventsBodyClose(t *testing.T) {
	body := &mockBody{}
	handle := scheduleBody(body, 2*time.Second)

	if handle.idx == 0 {
		t.Fatal("scheduleBody returned zero handle")
	}

	handle.cancel()
	time.Sleep(3 * time.Second)

	if body.closed.Load() {
		t.Error("body.Close() should NOT have been called after handle.cancel()")
	}
}

func TestTimerWheelConcurrentScheduleNoRace(t *testing.T) {
	const n = 50
	var wg sync.WaitGroup
	failures := make(chan string, n)

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx := &rctx.Context{}
			ctx.InitSlots()
			defer ctx.ReleaseOverflow()

			gen := ctx.SetUpstreamTimeout(10 * time.Second)
			handle := scheduleCtx(ctx, gen, 1*time.Second)
			if handle.idx == 0 {
				return // pool exhausted â€” skip; not a correctness failure
			}

			if idx%2 == 0 {
				// Let fire: wait long enough for the 1s timer to tick.
				time.Sleep(2500 * time.Millisecond)
				if ctx.Err() == nil {
					failures <- "timer did not fire when expected"
				}
			} else {
				// Cancel before fire.
				handle.cancel()
				time.Sleep(2500 * time.Millisecond)
				if ctx.Err() != nil {
					failures <- "context was cancelled despite handle.cancel()"
				}
			}
		}(i)
	}

	wg.Wait()
	close(failures)
	for msg := range failures {
		t.Error(msg)
	}
}

func TestTimerWheelZeroHandleOpsAreNoops(t *testing.T) {
	var h wheelHandle // zero value
	h.cancel()       // must not panic
}
