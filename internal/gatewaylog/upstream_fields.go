package gatewaylog

import "strings"

// UpstreamField is a bitmask for selecting which upstream call details to log.
type UpstreamField uint32

const (
	UpstreamFieldMethod       UpstreamField = 1 << iota // HTTP method
	UpstreamFieldURL                                     // full request URL
	UpstreamFieldStatusCode                              // response status code
	UpstreamFieldDurationMs                              // total call duration (ms)
	UpstreamFieldRequestSize                             // request body size (bytes)
	UpstreamFieldResponseSize                            // response body size (bytes)
	UpstreamFieldConnectTimeMs                           // TCP connect time (ms)
	UpstreamFieldTLSTimeMs                               // TLS handshake time (ms)
	UpstreamFieldTTFBMs                                  // time to first byte (ms)
	UpstreamFieldRetryCount                              // number of retries attempted
	UpstreamFieldError                                   // error message (if any)
)

// DefaultUpstreamFields is what gets logged when no explicit config is set.
// Logs method, URL, status code, duration, and errors only — minimal overhead.
const DefaultUpstreamFields = UpstreamFieldMethod | UpstreamFieldURL | UpstreamFieldStatusCode | UpstreamFieldDurationMs | UpstreamFieldError

// ParseUpstreamFields parses a comma-separated list of field names into a bitmask.
// Unknown names are silently ignored. Case-insensitive.
// An empty string returns DefaultUpstreamFields.
func ParseUpstreamFields(s string) UpstreamField {
	if s == "" {
		return DefaultUpstreamFields
	}
	var mask UpstreamField
	for _, part := range strings.Split(s, ",") {
		switch strings.TrimSpace(strings.ToLower(part)) {
		case "method":
			mask |= UpstreamFieldMethod
		case "url":
			mask |= UpstreamFieldURL
		case "status_code":
			mask |= UpstreamFieldStatusCode
		case "duration_ms":
			mask |= UpstreamFieldDurationMs
		case "request_size":
			mask |= UpstreamFieldRequestSize
		case "response_size":
			mask |= UpstreamFieldResponseSize
		case "connect_time_ms":
			mask |= UpstreamFieldConnectTimeMs
		case "tls_time_ms":
			mask |= UpstreamFieldTLSTimeMs
		case "ttfb_ms":
			mask |= UpstreamFieldTTFBMs
		case "retry_count":
			mask |= UpstreamFieldRetryCount
		case "error":
			mask |= UpstreamFieldError
		}
	}
	return mask
}

// UpstreamCallInfo carries the details of one upstream HTTP call.
// Pass zero values for fields you don't have — they are omitted if not in the mask.
type UpstreamCallInfo struct {
	Method       string
	URL          string
	StatusCode   int
	DurationMs   float64
	RequestSize  int64
	ResponseSize int64
	ConnectMs    float64
	TLSMs        float64
	TTFBMs       float64
	RetryCount   int
	Err          error
}

// BuildUpstreamFields constructs a []Field slice for the given upstream call details,
// including only the fields enabled in mask.
func BuildUpstreamFields(mask UpstreamField, info UpstreamCallInfo) []Field {
	fields := make([]Field, 0, 8)
	if mask&UpstreamFieldMethod != 0 {
		fields = append(fields, F("method", info.Method))
	}
	if mask&UpstreamFieldURL != 0 {
		fields = append(fields, F("url", info.URL))
	}
	if mask&UpstreamFieldStatusCode != 0 {
		fields = append(fields, Fint("status", int64(info.StatusCode)))
	}
	if mask&UpstreamFieldDurationMs != 0 {
		fields = append(fields, Ffloat("duration_ms", info.DurationMs))
	}
	if mask&UpstreamFieldRequestSize != 0 {
		fields = append(fields, Fint("req_size", info.RequestSize))
	}
	if mask&UpstreamFieldResponseSize != 0 {
		fields = append(fields, Fint("resp_size", info.ResponseSize))
	}
	if mask&UpstreamFieldConnectTimeMs != 0 {
		fields = append(fields, Ffloat("connect_ms", info.ConnectMs))
	}
	if mask&UpstreamFieldTLSTimeMs != 0 {
		fields = append(fields, Ffloat("tls_ms", info.TLSMs))
	}
	if mask&UpstreamFieldTTFBMs != 0 {
		fields = append(fields, Ffloat("ttfb_ms", info.TTFBMs))
	}
	if mask&UpstreamFieldRetryCount != 0 {
		fields = append(fields, Fint("retries", int64(info.RetryCount)))
	}
	if mask&UpstreamFieldError != 0 && info.Err != nil {
		fields = append(fields, F("error", info.Err.Error()))
	}
	return fields
}
