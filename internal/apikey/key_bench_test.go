package apikey

import (
	"testing"
)

func BenchmarkLookupByHash_Hit(b *testing.B) {
	// Insert one key
	rawKey, rec, err := Generate(1, "bench-app", nil)
	if err != nil {
		b.Fatalf("Generate: %v", err)
	}
	UpsertKey(rec)
	b.Cleanup(func() { DeleteKey(rec.KeyID) })

	// Compute the hash as the validation step would
	hash := HashKey(rawKey)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = LookupByHash(hash)
	}
}

func BenchmarkLookupByHash_Miss(b *testing.B) {
	const unknownHash = "0000000000000000000000000000000000000000000000000000000000000000"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = LookupByHash(unknownHash)
	}
}

func BenchmarkLookupByHash_VsGetRecord(b *testing.B) {
	rawKey, rec, err := Generate(1, "bench-app-compare", nil)
	if err != nil {
		b.Fatalf("Generate: %v", err)
	}
	UpsertKey(rec)
	b.Cleanup(func() { DeleteKey(rec.KeyID) })

	hash := HashKey(rawKey)

	b.Run("ByHash", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = LookupByHash(hash)
		}
	})

	b.Run("ByID", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = GetRecord(rec.KeyID)
		}
	})
}
