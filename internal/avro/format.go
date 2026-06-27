package avro

import "strconv"

// format.go — number formatting helpers for JSON output.
// appendStrconvFloat is the only place strconv is used; it is NOT in the hot decode path.

// appendStrconvFloat appends the float f to dst using strconv.AppendFloat.
// bits: 32 for float32, 64 for float64.
func appendStrconvFloat(dst []byte, f float64, bits int) []byte {
	return strconv.AppendFloat(dst, f, 'f', -1, bits)
}
