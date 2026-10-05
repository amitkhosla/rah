package steps

import (
	"net/http/httptest"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

func BenchmarkPrimitiveAddResponseHeader_ZeroAlloc(b *testing.B) {
	// Pre-construct the instruction with string-to-[]byte conversion at creation time (0 allocs/op goal)
	instr := PrimitiveAddResponseHeader("X-Request-Id", "test-value-123", 1)
	rw := &rctx.NoopResponseWriter{}
	req := httptest.NewRequest("GET", "/test", nil)
	state := &engine.ExecutionState{PC: 0}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := &rctx.Context{
			Request: req,
			Writer:  rw,
		}
		instr.Action(ctx, state)
	}
}

func BenchmarkPrimitiveExtractHeader_ZeroAlloc(b *testing.B) {
	// Uses unsafe.Slice to avoid []byte(string) allocation (0 allocs/op goal)
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Tenant", "acme-corp")
	instr := PrimitiveExtractHeader("X-Tenant", 0, 1)
	state := &engine.ExecutionState{PC: 0}
	slots := make([][]byte, 10)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := &rctx.Context{
			Request:   req,
			ByteSlots: slots,
		}
		instr.Action(ctx, state)
	}
}

func BenchmarkCachedQuery_MultipleAccesses(b *testing.B) {
	// Multiple accesses to CachedQuery should parse URL once and cache (1 alloc total, not per call)
	req := httptest.NewRequest("GET", "/test?token=abc123&tenant=acme&version=2", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := &rctx.Context{
			Request: req,
		}
		// Three accesses — should parse once, cache for the rest
		_ = ctx.CachedQuery().Get("token")
		_ = ctx.CachedQuery().Get("tenant")
		_ = ctx.CachedQuery().Get("version")
	}
}

func BenchmarkCachedQuery_SingleParse(b *testing.B) {
	// Measures the cost of parsing URL.RawQuery into url.Values
	req := httptest.NewRequest("GET", "/test?token=abc123&tenant=acme&version=2&key1=val1&key2=val2", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := &rctx.Context{
			Request: req,
		}
		// One call — measures parse cost
		_ = ctx.CachedQuery()
	}
}
