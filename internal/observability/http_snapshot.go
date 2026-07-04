package observability

import (
	"net/http"
	"strings"
)

// sensitiveHeaders contains header names to ALWAYS redact (case-insensitive).
// These structurally exclude credentials from all snapshots.
var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	"cookie":              true,
	"set-cookie":          true,
	"proxy-authorization": true,
	"x-auth-token":        true,
	"x-access-token":      true,
}

// BuildRequestSnapshot serializes the HTTP request metadata into compact bytes
// WITHOUT using json.Marshal (to avoid allocation from the encoder).
// Format: a JSON object with keys: method, path, query, headers (map of non-sensitive values).
// Sensitive headers are replaced with "[REDACTED]".
// Returns nil if req is nil.
func BuildRequestSnapshot(method, path, rawQuery string, headers http.Header) []byte {
	// Pre-compute size estimate to avoid repeated grows.
	buf := make([]byte, 0, 256)
	buf = append(buf, `{"method":`...)
	buf = appendJSONString(buf, method)
	buf = append(buf, `,"path":`...)
	buf = appendJSONString(buf, path)
	if rawQuery != "" {
		buf = append(buf, `,"query":`...)
		buf = appendJSONString(buf, rawQuery)
	}
	if len(headers) > 0 {
		buf = append(buf, `,"headers":{`...)
		first := true
		for k, vs := range headers {
			if len(vs) == 0 {
				continue
			}
			if !first {
				buf = append(buf, ',')
			}
			first = false
			buf = appendJSONString(buf, k)
			buf = append(buf, ':')
			lk := strings.ToLower(k)
			if sensitiveHeaders[lk] {
				buf = append(buf, `"[REDACTED]"`...)
			} else {
				buf = appendJSONString(buf, vs[0])
			}
		}
		buf = append(buf, '}')
	}
	buf = append(buf, '}')
	return buf
}

// BuildResponseSnapshot serializes the HTTP response metadata into compact bytes.
// Format: JSON object with keys: status, headers (map of non-sensitive values).
func BuildResponseSnapshot(status int, headers http.Header) []byte {
	buf := make([]byte, 0, 128)
	buf = append(buf, `{"status":`...)
	buf = appendJSONInt(buf, int64(status))
	if len(headers) > 0 {
		buf = append(buf, `,"headers":{`...)
		first := true
		for k, vs := range headers {
			if len(vs) == 0 {
				continue
			}
			if !first {
				buf = append(buf, ',')
			}
			first = false
			buf = appendJSONString(buf, k)
			buf = append(buf, ':')
			lk := strings.ToLower(k)
			if sensitiveHeaders[lk] {
				buf = append(buf, `"[REDACTED]"`...)
			} else {
				buf = appendJSONString(buf, vs[0])
			}
		}
		buf = append(buf, '}')
	}
	buf = append(buf, '}')
	return buf
}

// appendJSONString appends a JSON-encoded string (with surrounding quotes and
// escape sequences) to buf. No allocation beyond buf growth.
func appendJSONString(buf []byte, s string) []byte {
	buf = append(buf, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			buf = append(buf, '\\', '"')
		case '\\':
			buf = append(buf, '\\', '\\')
		case '\n':
			buf = append(buf, '\\', 'n')
		case '\r':
			buf = append(buf, '\\', 'r')
		case '\t':
			buf = append(buf, '\\', 't')
		default:
			if c < 0x20 {
				buf = append(buf, '\\', 'u', '0', '0', hexChar(c>>4), hexChar(c&0xf))
			} else {
				buf = append(buf, c)
			}
		}
	}
	buf = append(buf, '"')
	return buf
}

func appendJSONInt(buf []byte, n int64) []byte {
	if n == 0 {
		return append(buf, '0')
	}
	if n < 0 {
		buf = append(buf, '-')
		n = -n
	}
	start := len(buf)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	// Reverse the digits just appended.
	for i, j := start, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return buf
}

func hexChar(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'a' + b - 10
}
