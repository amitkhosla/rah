//go:build !race

package cache

import (
	"crypto/rand"
	"encoding/binary"
	"testing"
)

// BenchmarkHashFast compares the two hashing strategies for hashIdx keys:
//
//   - hashH1Only + hashLaneBig16: single maphash (H1) + real-byte sampling (H2)
//   - Hash128 + makeTagHashH2: two maphash calls (H1 + H2 via independent seeds)
//
// Run: go test -bench=BenchmarkHash -benchmem -count=5 ./internal/icache/

func BenchmarkHashH1OnlyAndBig16_12B(b *testing.B) {
	key := make([]byte, 12)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	binary.LittleEndian.PutUint16(key[0:2], 42)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			h1 := hashH1Only(42, key)
			h2 := hashLaneBig16(42, key)
			_ = (h1 & 0x0000FFFFFFFFFFFF) | (uint64(h2) << 48)
		}
	})
}

func BenchmarkHash128_12B(b *testing.B) {
	key := make([]byte, 12)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	binary.LittleEndian.PutUint16(key[0:2], 42)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			fp := Hash128(42, key)
			h1 := binary.LittleEndian.Uint64(fp[0:8])
			h2w := uint16(binary.LittleEndian.Uint64(fp[8:16]) >> 48)
			if h2w == 0 {
				h2w = 1
			}
			_ = (h1 & 0x0000FFFFFFFFFFFF) | (uint64(h2w) << 48)
		}
	})
}

func BenchmarkHashH1OnlyAndBig16_32B(b *testing.B) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			h1 := hashH1Only(42, key)
			h2 := hashLaneBig16(42, key)
			_ = (h1 & 0x0000FFFFFFFFFFFF) | (uint64(h2) << 48)
		}
	})
}

func BenchmarkHash128_32B(b *testing.B) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			fp := Hash128(42, key)
			h1 := binary.LittleEndian.Uint64(fp[0:8])
			h2w := uint16(binary.LittleEndian.Uint64(fp[8:16]) >> 48)
			if h2w == 0 {
				h2w = 1
			}
			_ = (h1 & 0x0000FFFFFFFFFFFF) | (uint64(h2w) << 48)
		}
	})
}
