package scheduler

import (
	"testing"
	"time"
)

// TestDispatchRing_PushNotifiesWorker verifies that TryPush sends a notification
// on NotifyCh to wake up blocked workers.
func TestDispatchRing_PushNotifiesWorker(t *testing.T) {
	ring := NewDispatchRing(4)

	// Get an event from the pool
	ev := globalEventPool.Get()
	ev.Name = "notify-test"
	defer globalEventPool.Put(ev)

	// Channel to track when the worker wakes up
	workerWoke := make(chan struct{}, 1)

	// Goroutine that blocks on NotifyCh (simulating a worker)
	go func() {
		<-ring.NotifyCh
		workerWoke <- struct{}{}
	}()

	// Give the goroutine time to block on the channel
	time.Sleep(50 * time.Millisecond)

	// Push the event — should trigger a notify
	if !ring.TryPush(ev) {
		t.Fatal("TryPush failed; ring should not be full")
	}

	// Verify the worker woke up within 10ms
	select {
	case <-workerWoke:
		// Success: worker was notified
	case <-time.After(10 * time.Millisecond):
		t.Error("timeout: worker was not notified within 10ms")
	}
}
