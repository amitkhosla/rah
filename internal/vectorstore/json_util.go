package vectorstore

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// marshalJSON is a thin wrapper around json.Marshal.
func marshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// unmarshalJSON is a thin wrapper around json.Unmarshal.
func unmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// contentID generates a deterministic hex ID from content bytes using SHA-256.
func contentID(content string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", sum[:16]) // 32 hex chars (128-bit)
}

// vectorToFloatSlice converts []float32 to []float64 for JSON compatibility.
func vectorToFloatSlice(v []float32) []float64 {
	out := make([]float64, len(v))
	for i, f := range v {
		out[i] = float64(f)
	}
	return out
}

// vectorString formats a float32 slice as "[0.1,0.2,...]" for pgvector text format.
func vectorString(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	sb := strings.Builder{}
	sb.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(formatFloat(f))
	}
	sb.WriteByte(']')
	return sb.String()
}

func formatFloat(f float32) string {
	// Use strconv-style formatting without importing strconv to keep it simple.
	// We use fmt.Sprintf with %g which gives compact representation.
	return fmt.Sprintf("%g", f)
}

// float32ToBytes encodes a float32 slice as little-endian IEEE 754 bytes.
func float32ToBytes(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		bits := math.Float32bits(f)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

// getOption returns cfg option value or a default.
func getOption(options map[string]string, key, defaultVal string) string {
	if options == nil {
		return defaultVal
	}
	if v, ok := options[key]; ok && v != "" {
		return v
	}
	return defaultVal
}
