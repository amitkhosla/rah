package rctx

import (
	"net/http"
	"testing"
)

type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
func (noopWriter) WriteHeader(statusCode int)  {}
func (noopWriter) Header() http.Header         { return make(http.Header) }

type captureWriter struct {
	status      int
	headerCalls int
}

func (w *captureWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *captureWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.headerCalls++
}
func (w *captureWriter) Header() http.Header { return make(http.Header) }

func TestMarkDetachedFromPool(t *testing.T) {
	ctx := &Context{}
	ctx.Reset(noopWriter{})

	if !ctx.ShouldReturnToPool() {
		t.Fatalf("newly reset context should be returnable to pool")
	}

	ctx.MarkDetachedFromPool()
	if ctx.ShouldReturnToPool() {
		t.Fatalf("detached context must not be returned to pool")
	}

	ctx.Reset(noopWriter{})
	if !ctx.ShouldReturnToPool() {
		t.Fatalf("reset should clear detached state")
	}
}

func TestClientBytesSentStreamingAndBuffered(t *testing.T) {
	ctx := &Context{ResponseStatus: 200}
	ctx.Reset(noopWriter{})

	if _, err := ctx.Write([]byte("abc")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if ctx.Timing.ClientBytesSent != 3 {
		t.Fatalf("streaming bytes = %d, want 3", ctx.Timing.ClientBytesSent)
	}

	ctx.Reset(noopWriter{})
	ctx.IsBuffered = true
	if _, err := ctx.Write([]byte("hello")); err != nil {
		t.Fatalf("buffered write failed: %v", err)
	}
	if ctx.Timing.ClientBytesSent != 0 {
		t.Fatalf("buffered mode should count on finalize, got %d", ctx.Timing.ClientBytesSent)
	}
	ctx.Finalize()
	if ctx.Timing.ClientBytesSent != 5 {
		t.Fatalf("buffered finalize bytes = %d, want 5", ctx.Timing.ClientBytesSent)
	}
}

func TestFinalizeWritesStatusWithoutBody(t *testing.T) {
	w := &captureWriter{}
	ctx := &Context{}
	ctx.Reset(w)

	// Simulate an instruction that marks unauthorized and stops without writing a body.
	ctx.ResponseStatus = http.StatusUnauthorized
	ctx.IsBuffered = false
	ctx.ResponseBuffer = nil

	ctx.Finalize()

	if w.status != http.StatusUnauthorized {
		t.Fatalf("final status = %d, want %d", w.status, http.StatusUnauthorized)
	}
	if w.headerCalls != 1 {
		t.Fatalf("WriteHeader called %d times, want 1", w.headerCalls)
	}
	if !ctx.headerSent {
		t.Fatalf("headerSent should be true after finalize")
	}
}
