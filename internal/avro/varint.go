package avro

// varint.go — zigzag + varint encode/decode helpers.
// These are the only primitives used at runtime for Avro binary wire format.
// No allocations; all operate on []byte slices with append semantics.

// encodeZigzag maps a signed int64 to an unsigned uint64 using zigzag encoding.
// The standard Avro encoding for Int/Long types.
func encodeZigzag(n int64) uint64 { return uint64((n << 1) ^ (n >> 63)) }

// decodeZigzag reverses zigzag encoding, recovering the original signed int64.
func decodeZigzag(u uint64) int64 { return int64((u >> 1) ^ -(u & 1)) }

// appendVarint appends the varint encoding of u to dst and returns the result.
// Uses 7 bits per byte; MSB set means more bytes follow.
// At most 10 bytes for a uint64.
func appendVarint(dst []byte, u uint64) []byte {
	for u >= 0x80 {
		dst = append(dst, byte(u)|0x80)
		u >>= 7
	}
	return append(dst, byte(u))
}

// readVarint reads a variable-length unsigned integer from src.
// Returns (value, bytes consumed, ok).
// ok is false if src is empty or the encoding is truncated.
func readVarint(src []byte) (uint64, int, bool) {
	var u uint64
	var shift uint
	for i, b := range src {
		if i >= 10 {
			// Overflow: varint too long
			return 0, 0, false
		}
		u |= uint64(b&0x7f) << shift
		shift += 7
		if b < 0x80 {
			return u, i + 1, true
		}
	}
	// Ran out of bytes — truncated
	return 0, 0, false
}

// appendLong appends the zigzag-varint encoding of a signed int64 to dst.
func appendLong(dst []byte, n int64) []byte {
	return appendVarint(dst, encodeZigzag(n))
}

// readLong reads a zigzag-varint signed int64 from src.
// Returns (value, bytes consumed, ok).
func readLong(src []byte) (int64, int, bool) {
	u, n, ok := readVarint(src)
	if !ok {
		return 0, 0, false
	}
	return decodeZigzag(u), n, true
}
