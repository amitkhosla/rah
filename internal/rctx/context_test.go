package rctx

import (
	"net/http"
	"testing"
)

type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
func (noopWriter) WriteHeader(statusCode int)  {}
func (noopWriter) Header() http.Header         { return make(http.Header) }

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
