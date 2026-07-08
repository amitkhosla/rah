package steps

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"rah/internal/apikey"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// setupBenchKey registers a key in the global index and returns the raw key string.
func setupBenchKey(b *testing.B) string {
	b.Helper()
	rawKey, rec, err := apikey.Generate(1, "bench-app", nil)
	if err != nil {
		b.Fatalf("apikey.Generate: %v", err)
	}
	apikey.UpsertKey(rec)
	b.Cleanup(func() { apikey.DeleteKey(rec.KeyID) })
	return rawKey
}

func newBenchCtx(rawKey string) (*rctx.Context, *engine.ExecutionState) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", rawKey)
	ctx := &rctx.Context{Request: req, TenantID: 1}
	s := &engine.ExecutionState{PC: 0}
	return ctx, s
}

// BenchmarkPhase_SHA256 — cost of hashing the raw key alone.
func BenchmarkPhase_SHA256(b *testing.B) {
	rawKey, _, err := apikey.Generate(1, "bench", nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = sha256.Sum256([]byte(rawKey))
	}
}

// BenchmarkPhase_HexEncode — cost of hex-encoding the 32-byte SHA256 result alone.
func BenchmarkPhase_HexEncode(b *testing.B) {
	rawKey, _, err := apikey.Generate(1, "bench", nil)
	if err != nil {
		b.Fatal(err)
	}
	sum := sha256.Sum256([]byte(rawKey))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = hex.EncodeToString(sum[:])
	}
}

// BenchmarkPhase_MapLookup — cost of the sync.Map lookup by hex hash alone.
func BenchmarkPhase_MapLookup(b *testing.B) {
	rawKey := setupBenchKey(b)
	sum := sha256.Sum256([]byte(rawKey))
	hash := hex.EncodeToString(sum[:])
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = apikey.LookupByHash(hash)
	}
}

// BenchmarkPhase_SHA256_And_HexEncode — combined cost (as it happens today).
func BenchmarkPhase_SHA256_And_HexEncode(b *testing.B) {
	rawKey, _, err := apikey.Generate(1, "bench", nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sum := sha256.Sum256([]byte(rawKey))
		_ = hex.EncodeToString(sum[:])
	}
}

// BenchmarkValidateAPIKey_Hit — full validate_api_key step, key exists and is valid.
func BenchmarkValidateAPIKey_Hit(b *testing.B) {
	rawKey := setupBenchKey(b)
	slots := APIKeyValidationSlots{SourceSlot: -1, ResultSlot: -1}
	cfg := ParseAPIKeyValidationConfig(map[string]string{})
	step := ValidateAPIKey(slots, cfg)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx, s := newBenchCtx(rawKey)
		step.Action(ctx, s)
	}
}

// BenchmarkValidateAPIKey_Miss — full step, key not in index (worst-case hash path).
func BenchmarkValidateAPIKey_Miss(b *testing.B) {
	slots := APIKeyValidationSlots{SourceSlot: -1, ResultSlot: -1}
	cfg := ParseAPIKeyValidationConfig(map[string]string{
		"apikey.on_failure": "continue",
	})
	step := ValidateAPIKey(slots, cfg)
	const unknownKey = "rah_unknownkeynotregistered000000000000000000"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx, s := newBenchCtx(unknownKey)
		step.Action(ctx, s)
	}
}

// BenchmarkPhase_CtxAndStateAlloc — cost of allocating Context + ExecutionState alone.
func BenchmarkPhase_CtxAndStateAlloc(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "rah_benchkey")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx := &rctx.Context{Request: req, TenantID: 1}
		s := &engine.ExecutionState{PC: 0}
		_ = ctx
		_ = s
	}
}

// BenchmarkPhase_LookupByHash — cost of LookupByHash alone (map load + value copy).
func BenchmarkPhase_LookupByHash(b *testing.B) {
	rawKey := setupBenchKey(b)
	sum := sha256.Sum256([]byte(rawKey))
	hash := hex.EncodeToString(sum[:])
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = apikey.LookupByHash(hash)
	}
}

// BenchmarkHttpRequestSetup — cost of httptest.NewRequest alone, to isolate harness overhead.
func BenchmarkHttpRequestSetup(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-API-Key", "rah_benchkey")
		_ = req
	}
}

// BenchmarkValidateAPIKey_HitReusedCtx — hit path with pre-built request to isolate validation cost.
func BenchmarkValidateAPIKey_HitReusedCtx(b *testing.B) {
	rawKey := setupBenchKey(b)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", rawKey)

	slots := APIKeyValidationSlots{SourceSlot: -1, ResultSlot: -1}
	cfg := ParseAPIKeyValidationConfig(map[string]string{})
	step := ValidateAPIKey(slots, cfg)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx := &rctx.Context{Request: req, TenantID: 1}
		s := &engine.ExecutionState{PC: 0}
		step.Action(ctx, s)
	}
}

// BenchmarkValidateAPIKey_WithTenantCheck — hit path with AllowedTenants slice check.
func BenchmarkValidateAPIKey_WithTenantCheck(b *testing.B) {
	rawKey, rec, err := apikey.Generate(1, "bench-tenant", []uint16{1, 2, 3})
	if err != nil {
		b.Fatal(err)
	}
	apikey.UpsertKey(rec)
	b.Cleanup(func() { apikey.DeleteKey(rec.KeyID) })

	slots := APIKeyValidationSlots{SourceSlot: -1, ResultSlot: -1}
	cfg := ParseAPIKeyValidationConfig(map[string]string{
		"apikey.require_tenant": "true",
	})
	step := ValidateAPIKey(slots, cfg)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx, s := newBenchCtx(rawKey)
		step.Action(ctx, s)
	}
}
