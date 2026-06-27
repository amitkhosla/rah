package avro

import (
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []int64{
		0, 1, -1, 63, 64, 127, 128, 300, -300,
		math.MaxInt32, math.MinInt32,
		math.MaxInt64, math.MinInt64,
	}
	for _, n := range cases {
		enc := appendLong(nil, n)
		got, nc, ok := readLong(enc)
		if !ok {
			t.Errorf("readLong(%d): not ok", n)
			continue
		}
		if nc != len(enc) {
			t.Errorf("readLong(%d): consumed %d bytes, expected %d", n, nc, len(enc))
		}
		if got != n {
			t.Errorf("round-trip(%d): got %d", n, got)
		}
	}
}

func TestZigzagRoundTrip(t *testing.T) {
	cases := []int64{
		0, 1, -1, math.MaxInt32, math.MinInt32,
		math.MaxInt64, math.MinInt64,
		127, -127, 128, -128,
	}
	for _, n := range cases {
		enc := encodeZigzag(n)
		got := decodeZigzag(enc)
		if got != n {
			t.Errorf("zigzag(%d): got %d (encoded=%d)", n, got, enc)
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	// Build a valid varint for 300, then truncate it
	full := appendVarint(nil, 300)
	// 300 needs 2 bytes; test with 1 byte (MSB set → expects more)
	_, _, ok := readVarint(full[:1])
	if ok {
		t.Error("readVarint: expected ok=false for truncated input")
	}

	// Empty slice
	_, _, ok = readVarint(nil)
	if ok {
		t.Error("readVarint: expected ok=false for empty input")
	}
}

func TestVarintUnsignedValues(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 0x3fff, 0x4000, math.MaxUint32, math.MaxUint64}
	for _, u := range cases {
		enc := appendVarint(nil, u)
		got, nc, ok := readVarint(enc)
		if !ok {
			t.Errorf("readVarint(u=%d): not ok", u)
			continue
		}
		if nc != len(enc) {
			t.Errorf("readVarint(u=%d): consumed %d, expected %d", u, nc, len(enc))
		}
		if got != u {
			t.Errorf("readVarint(u=%d): got %d", u, got)
		}
	}
}

func TestAppendVarintMaxLen(t *testing.T) {
	// MaxUint64 should encode in at most 10 bytes
	enc := appendVarint(nil, math.MaxUint64)
	if len(enc) > 10 {
		t.Errorf("appendVarint(MaxUint64): %d bytes, expected ≤10", len(enc))
	}
	got, _, ok := readVarint(enc)
	if !ok || got != math.MaxUint64 {
		t.Errorf("readVarint(MaxUint64): got=%d, ok=%v", got, ok)
	}
}
