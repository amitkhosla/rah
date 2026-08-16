package steps

// body_limit — Enforces a maximum request body size per flow.
//
// Performs two checks:
//  1. Fast-path: if Content-Length is present and exceeds the limit, reject immediately.
//  2. Wrap: replaces ctx.Request.Body with a limitedReadCloser so any downstream
//     read (e.g. http_call, upstream) will receive an error once maxBytes is exceeded.
//
// Config keys:
//
//	body.max_bytes   — max body size in bytes (integer or with suffix: kb/mb)
//	body.max_kb      — convenience alias: value in kilobytes
//	body.max_mb      — convenience alias: value in megabytes
//	body.failure_status — HTTP status on rejection (default 413)
//	body.failure_body   — response body on rejection (default "request body too large")

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// BodyLimitConfig is the bake-time compiled config for limit_body.
type BodyLimitConfig struct {
	// MaxBytes is the maximum allowed body size. 0 means no limit.
	MaxBytes int64
	// OnFailureStatus is the HTTP status to return when the body is too large (default 413).
	OnFailureStatus int
	// OnFailureBody is the response body to return on rejection (default "request body too large").
	OnFailureBody string
}

// parseBodySize parses a size string which may be a plain integer (bytes) or
// an integer followed by k/kb/m/mb (case-insensitive).
// Returns 0 and no error when the input is empty.
func parseBodySize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	lower := strings.ToLower(s)
	var multiplier int64 = 1
	numStr := s
	switch {
	case strings.HasSuffix(lower, "mb"):
		multiplier = 1 << 20
		numStr = s[:len(s)-2]
	case strings.HasSuffix(lower, "kb"):
		multiplier = 1 << 10
		numStr = s[:len(s)-2]
	case strings.HasSuffix(lower, "m"):
		multiplier = 1 << 20
		numStr = s[:len(s)-1]
	case strings.HasSuffix(lower, "k"):
		multiplier = 1 << 10
		numStr = s[:len(s)-1]
	}
	numStr = strings.TrimSpace(numStr)
	n, err := parseInt(numStr)
	if err != nil {
		return 0, fmt.Errorf("body_limit: invalid size %q: %w", s, err)
	}
	return int64(n) * multiplier, nil
}

// ParseBodyLimitConfig converts a step Input map into a BodyLimitConfig.
func ParseBodyLimitConfig(input map[string]string) BodyLimitConfig {
	cfg := BodyLimitConfig{
		OnFailureStatus: 413,
		OnFailureBody:   "request body too large",
	}

	// body.max_bytes takes precedence, then body.max_kb, then body.max_mb.
	if v := strings.TrimSpace(input["body.max_bytes"]); v != "" {
		if n, err := parseBodySize(v); err == nil {
			cfg.MaxBytes = n
		}
	} else if v := strings.TrimSpace(input["body.max_kb"]); v != "" {
		if n, err := parseInt(v); err == nil {
			cfg.MaxBytes = int64(n) * 1024
		}
	} else if v := strings.TrimSpace(input["body.max_mb"]); v != "" {
		if n, err := parseInt(v); err == nil {
			cfg.MaxBytes = int64(n) * 1024 * 1024
		}
	}

	if v := strings.TrimSpace(input["body.failure_status"]); v != "" {
		if n, err := parseInt(v); err == nil {
			cfg.OnFailureStatus = n
		}
	}
	if v := strings.TrimSpace(input["body.failure_body"]); v != "" {
		cfg.OnFailureBody = v
	}

	return cfg
}

// limitedReadCloser wraps an io.ReadCloser and returns an error once maxBytes
// have been read.  It does NOT close on overflow — the caller still needs to
// close the underlying body.
type limitedReadCloser struct {
	rc       io.ReadCloser
	remaining int64
	exceeded  bool
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	if l.exceeded {
		return 0, fmt.Errorf("request body too large")
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.rc.Read(p)
	l.remaining -= int64(n)
	if l.remaining <= 0 {
		// Try one more byte to detect overflow.
		var buf [1]byte
		_, overErr := l.rc.Read(buf[:])
		if overErr == nil {
			// There is more data — body exceeds the limit.
			l.exceeded = true
			return n, fmt.Errorf("request body too large")
		}
		// overErr is io.EOF (or another error) — body fits exactly.
	}
	return n, err
}

func (l *limitedReadCloser) Close() error {
	return l.rc.Close()
}

// LimitBody returns an instruction that enforces the configured body size limit.
//
// On a request with a Content-Length header larger than MaxBytes the request is
// rejected immediately (fast path).  In all cases ctx.Request.Body is replaced
// with a limitedReadCloser so downstream steps cannot read beyond MaxBytes.
func LimitBody(cfg BodyLimitConfig) engine.Instruction {
	return engine.Instruction{
		Name: "LIMIT_BODY",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if cfg.MaxBytes <= 0 || ctx.Request == nil {
				return s.PC + 1
			}

			stopFail := func() int16 {
				status := cfg.OnFailureStatus
				body := cfg.OnFailureBody
				ctx.ResponseStatus = status
				_, _ = ctx.Write([]byte(body))
				ctx.Failed = true
				ctx.ErrorCode = int16(status)
				ctx.ErrorMsg = ctx.Alloc(len(body))
				copy(ctx.ErrorMsg, body)
				return engine.StopPlan
			}

			// Fast-path: reject based on Content-Length before reading the body.
			if ctx.Request.ContentLength > cfg.MaxBytes {
				return stopFail()
			}

			// Wrap the body to enforce the limit on reads.
			if ctx.Request.Body != nil && ctx.Request.Body != http.NoBody {
				ctx.Request.Body = &limitedReadCloser{
					rc:        ctx.Request.Body,
					remaining: cfg.MaxBytes,
				}
			}

			return s.PC + 1
		},
	}
}
