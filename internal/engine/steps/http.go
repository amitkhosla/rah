package steps

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"rah/internal/engine"
	"rah/internal/observability"
	"rah/internal/rctx"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// upstreamCooldownEntry records when a host is blocked until.
type upstreamCooldownEntry struct {
	until time.Time
}

var upstreamCooldowns sync.Map // key: string (host) → upstreamCooldownEntry

func markCooldown(host string, until time.Time) {
	upstreamCooldowns.Store(host, upstreamCooldownEntry{until: until})
}

func inCooldown(host string) (bool, time.Duration) {
	v, ok := upstreamCooldowns.Load(host)
	if !ok {
		return false, 0
	}
	entry := v.(upstreamCooldownEntry)
	remaining := time.Until(entry.until)
	if remaining <= 0 {
		upstreamCooldowns.Delete(host)
		return false, 0
	}
	return true, remaining
}

// parseRetryAfter parses the value of a Retry-After header.
// Supports integer seconds ("120") and HTTP-date ("Wed, 21 Oct 2015 07:28:00 GMT").
func parseRetryAfter(h string) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs <= 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	for _, layout := range []string{time.RFC1123, time.RFC850, time.ANSIC} {
		if t, err := time.Parse(layout, h); err == nil {
			d := time.Until(t)
			if d <= 0 {
				return 0, false
			}
			return d, true
		}
	}
	return 0, false
}

func flowBool(flowInput map[string]string, key string, fallback bool) bool {
	if flowInput == nil {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(flowInput[key])) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	return fallback
}

// All duration values are in milliseconds (ms) when provided via flow input.
// DialTimeout is the maximum time allowed to establish a TCP connection to upstream.
type httpClientConfig struct {
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
	MaxConnsPerHost       int
	IdleConnTimeout       time.Duration
	DialTimeout           time.Duration
	KeepAlive             time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	ExpectContinueTimeout time.Duration
	RequestTimeout        time.Duration
	RetryMaxAttempts      int
	RetryBaseBackoff      time.Duration
	RetryMaxBackoff       time.Duration
	RetryJitter           time.Duration
	RetryOnStatuses       map[int]struct{}
	HonorRetryAfter       bool
	RetryAfterMaxWait     time.Duration
	UpstreamCooldown      bool
}

type cachedClient struct {
	Client *http.Client
	Cfg    httpClientConfig
}

type clientCacheKey struct {
	Upstream  string
	ConfigKey string
}

var (
	cfgOnce                  sync.Once
	cachedDefaultHTTPConfig  httpClientConfig
	defaultHTTPClient        *http.Client
	perTargetClientCache     sync.Map
	perTargetClientCacheSize atomic.Int64
)

func loadHTTPClientConfig() httpClientConfig {
	return httpClientConfig{
		MaxIdleConns:          2000,
		MaxIdleConnsPerHost:   200,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		DialTimeout:           2 * time.Second,
		KeepAlive:             30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		RequestTimeout:        10 * time.Second,
		RetryMaxAttempts:      2,
		RetryBaseBackoff:      25 * time.Millisecond,
		RetryMaxBackoff:       250 * time.Millisecond,
		RetryJitter:           10 * time.Millisecond,
		RetryOnStatuses:       map[int]struct{}{429: {}, 502: {}, 503: {}, 504: {}},
		HonorRetryAfter:       true,
		RetryAfterMaxWait:     30 * time.Second,
		UpstreamCooldown:      true,
	}
}

func getDefaultHTTPConfig() httpClientConfig {
	cfgOnce.Do(func() {
		cachedDefaultHTTPConfig = loadHTTPClientConfig()
		defaultHTTPClient = buildHTTPClient(cachedDefaultHTTPConfig)
	})
	return cachedDefaultHTTPConfig
}

func buildHTTPClient(cfg httpClientConfig) *http.Client {
	dialer := &net.Dialer{Timeout: cfg.DialTimeout, KeepAlive: cfg.KeepAlive}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			MaxIdleConns:          cfg.MaxIdleConns,
			MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
			MaxConnsPerHost:       cfg.MaxConnsPerHost,
			IdleConnTimeout:       cfg.IdleConnTimeout,
			TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
			ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
			ExpectContinueTimeout: cfg.ExpectContinueTimeout,
		},
		Timeout: cfg.RequestTimeout,
	}
}

func parseStatusSet(raw string, fallback map[int]struct{}) map[int]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	out := make(map[int]struct{})
	for part := range strings.SplitSeq(raw, ",") {
		code, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && code > 0 {
			out[code] = struct{}{}
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func cloneStatusSet(in map[int]struct{}) map[int]struct{} {
	out := make(map[int]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

func flowInt(flowInput map[string]string, key string, fallback int) int {
	if flowInput == nil {
		return fallback
	}
	raw := strings.TrimSpace(flowInput[key])
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func flowDurationMs(flowInput map[string]string, key string, fallback time.Duration) time.Duration {
	ms := flowInt(flowInput, key, int(fallback/time.Millisecond))
	if ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

func resolveHTTPConfigForTarget(upstreamHost string, flowInput map[string]string) httpClientConfig {
	_ = upstreamHost
	base := getDefaultHTTPConfig()
	cfg := base
	cfg.RetryOnStatuses = cloneStatusSet(base.RetryOnStatuses)

	cfg.MaxIdleConns = flowInt(flowInput, "http.max_idle_conns", cfg.MaxIdleConns)
	cfg.MaxIdleConnsPerHost = flowInt(flowInput, "http.max_idle_conns_per_host", cfg.MaxIdleConnsPerHost)
	cfg.MaxConnsPerHost = flowInt(flowInput, "http.max_conns_per_host", cfg.MaxConnsPerHost)
	cfg.IdleConnTimeout = flowDurationMs(flowInput, "http.idle_conn_timeout_ms", cfg.IdleConnTimeout)
	cfg.DialTimeout = flowDurationMs(flowInput, "http.dial_timeout_ms", cfg.DialTimeout)
	cfg.KeepAlive = flowDurationMs(flowInput, "http.keep_alive_ms", cfg.KeepAlive)
	cfg.TLSHandshakeTimeout = flowDurationMs(flowInput, "http.tls_handshake_timeout_ms", cfg.TLSHandshakeTimeout)
	cfg.ResponseHeaderTimeout = flowDurationMs(flowInput, "http.response_header_timeout_ms", cfg.ResponseHeaderTimeout)
	cfg.ExpectContinueTimeout = flowDurationMs(flowInput, "http.expect_continue_timeout_ms", cfg.ExpectContinueTimeout)
	cfg.RequestTimeout = flowDurationMs(flowInput, "http.request_timeout_ms", cfg.RequestTimeout)
	cfg.RetryMaxAttempts = flowInt(flowInput, "http.retry_max_attempts", cfg.RetryMaxAttempts)
	cfg.RetryBaseBackoff = flowDurationMs(flowInput, "http.retry_base_backoff_ms", cfg.RetryBaseBackoff)
	cfg.RetryMaxBackoff = flowDurationMs(flowInput, "http.retry_max_backoff_ms", cfg.RetryMaxBackoff)
	cfg.RetryJitter = flowDurationMs(flowInput, "http.retry_jitter_ms", cfg.RetryJitter)
	cfg.RetryOnStatuses = parseStatusSet(flowInput["http.retry_on_statuses"], cfg.RetryOnStatuses)
	cfg.HonorRetryAfter = flowBool(flowInput, "http.honor_retry_after", cfg.HonorRetryAfter)
	cfg.RetryAfterMaxWait = flowDurationMs(flowInput, "http.retry_after_max_wait_ms", cfg.RetryAfterMaxWait)
	cfg.UpstreamCooldown = flowBool(flowInput, "http.upstream_cooldown", cfg.UpstreamCooldown)
	return cfg
}

func configFingerprint(cfg httpClientConfig) string {
	codes := make([]int, 0, len(cfg.RetryOnStatuses))
	for k := range cfg.RetryOnStatuses {
		codes = append(codes, k)
	}
	sort.Ints(codes)
	parts := make([]string, 0, len(codes))
	for _, c := range codes {
		parts = append(parts, strconv.Itoa(c))
	}
	return strings.Join([]string{
		strconv.Itoa(cfg.MaxIdleConns),
		strconv.Itoa(cfg.MaxIdleConnsPerHost),
		strconv.Itoa(cfg.MaxConnsPerHost),
		strconv.FormatInt(int64(cfg.IdleConnTimeout/time.Millisecond), 10),
		strconv.FormatInt(int64(cfg.DialTimeout/time.Millisecond), 10),
		strconv.FormatInt(int64(cfg.KeepAlive/time.Millisecond), 10),
		strconv.FormatInt(int64(cfg.TLSHandshakeTimeout/time.Millisecond), 10),
		strconv.FormatInt(int64(cfg.ResponseHeaderTimeout/time.Millisecond), 10),
		strconv.FormatInt(int64(cfg.ExpectContinueTimeout/time.Millisecond), 10),
		strconv.FormatInt(int64(cfg.RequestTimeout/time.Millisecond), 10),
		strconv.Itoa(cfg.RetryMaxAttempts),
		strconv.FormatInt(int64(cfg.RetryBaseBackoff/time.Millisecond), 10),
		strconv.FormatInt(int64(cfg.RetryMaxBackoff/time.Millisecond), 10),
		strconv.FormatInt(int64(cfg.RetryJitter/time.Millisecond), 10),
		strings.Join(parts, ","),
		strconv.FormatBool(cfg.HonorRetryAfter),
		strconv.FormatInt(int64(cfg.RetryAfterMaxWait/time.Millisecond), 10),
		strconv.FormatBool(cfg.UpstreamCooldown),
	}, "|")
}

func extractUpstreamHost(rawURL string) string {
	start := strings.Index(rawURL, "://")
	if start >= 0 {
		start += 3
	} else {
		start = 0
	}
	if start >= len(rawURL) {
		return ""
	}
	end := len(rawURL)
	if i := strings.IndexByte(rawURL[start:], '/'); i >= 0 {
		end = start + i
	}
	if i := strings.IndexByte(rawURL[start:end], '?'); i >= 0 {
		end = start + i
	}
	hostPort := rawURL[start:end]
	if at := strings.LastIndexByte(hostPort, '@'); at >= 0 {
		hostPort = hostPort[at+1:]
	}
	return strings.ToLower(hostPort)
}

func getClientForTarget(upstreamHost string, flowInput map[string]string) cachedClient {
	getDefaultHTTPConfig()
	cfg := resolveHTTPConfigForTarget(upstreamHost, flowInput)
	key := clientCacheKey{Upstream: upstreamHost, ConfigKey: configFingerprint(cfg)}
	if existing, ok := perTargetClientCache.Load(key); ok {
		return existing.(cachedClient)
	}

	created := cachedClient{Client: buildHTTPClient(cfg), Cfg: cfg}
	if flowInt(flowInput, "http.max_client_cache_entries", 2048) <= int(perTargetClientCacheSize.Load()) {
		return cachedClient{Client: defaultHTTPClient, Cfg: getDefaultHTTPConfig()}
	}

	actual, loaded := perTargetClientCache.LoadOrStore(key, created)
	if loaded {
		return actual.(cachedClient)
	}
	perTargetClientCacheSize.Add(1)
	return created
}

func isRetryableStatus(status int, cfg httpClientConfig) bool {
	_, ok := cfg.RetryOnStatuses[status]
	return ok
}

func shouldRetryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

func backoffDelay(attempt int, cfg httpClientConfig, upstreamHost string) time.Duration {
	d := cfg.RetryBaseBackoff
	for i := 1; i < attempt; i++ {
		if d >= cfg.RetryMaxBackoff {
			d = cfg.RetryMaxBackoff
			break
		}
		d *= 2
		if d > cfg.RetryMaxBackoff {
			d = cfg.RetryMaxBackoff
			break
		}
	}
	if cfg.RetryJitter > 0 {
		now := time.Now()
		seed := uint64(now.UnixNano()) ^ uint64(now.Unix())<<16 ^ uint64(attempt*131) ^ uint64(len(upstreamHost))
		d += time.Duration(seed % uint64(cfg.RetryJitter))
	}
	return d
}

func GetClientFromPool() *http.Client {
	getDefaultHTTPConfig()
	return defaultHTTPClient
}

func HttpAction(urlSlot int, staticURL string, timeout uint32, retryCondition string, maxRetries int, flowInput map[string]string) engine.Instruction {
	return engine.Instruction{
		Name: "HTTP_CALL",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			rawUpstream := staticURL
			if urlSlot >= 0 && urlSlot < len(ctx.ByteSlots) && len(ctx.ByteSlots[urlSlot]) > 0 {
				rawUpstream = string(ctx.ByteSlots[urlSlot])
			}

			url, err := selectUpstreamURL(rawUpstream, flowInput)
			if err != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				ctx.ErrorMsg = ctx.Alloc(len("upstream url resolution failed"))
				copy(ctx.ErrorMsg, "upstream url resolution failed")
				return engine.StopPlan
			}
			upstreamHost := extractUpstreamHost(url)
			bundle := getClientForTarget(upstreamHost, flowInput)

			attempts := bundle.Cfg.RetryMaxAttempts + 1
			if maxRetries >= 0 {
				attempts = maxRetries + 1
			}
			if attempts < 1 {
				attempts = 1
			}

			for attempt := 1; attempt <= attempts; attempt++ {
				// Check per-upstream cooldown before attempting.
				if bundle.Cfg.UpstreamCooldown {
					if cooling, _ := inCooldown(upstreamHost); cooling {
						ctx.ResponseStatus = 503
						ctx.Failed = true
						ctx.ErrorCode = 503
						msg := "upstream in cooldown"
						ctx.ErrorMsg = ctx.Alloc(len(msg))
						copy(ctx.ErrorMsg, msg)
						return engine.StopPlan
					}
				}
				upstreamStart := time.Now()
				event := observability.UpstreamEvent{Host: upstreamHost, URL: url, Attempt: attempt}
				var dnsStart, connectStart, tlsStart, wroteReqStart, firstByteStart time.Time

				reqCtx := context.Background()
				cancel := func() {}
				if timeout > 0 {
					reqCtx, cancel = context.WithTimeout(reqCtx, time.Duration(timeout)*time.Millisecond)
				}

				req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
				if err != nil {
					cancel()
					ctx.ResponseStatus = 500
					ctx.Failed = true
					ctx.ErrorCode = 500
					ctx.ErrorMsg = ctx.Alloc(len("upstream call failed"))
					copy(ctx.ErrorMsg, "upstream call failed")
					return engine.StopPlan
				}

				req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
					DNSStart: func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
					DNSDone: func(httptrace.DNSDoneInfo) {
						if !dnsStart.IsZero() {
							event.DNSDurationNs += time.Since(dnsStart).Nanoseconds()
						}
					},
					ConnectStart: func(_, _ string) { connectStart = time.Now() },
					ConnectDone: func(_, _ string, _ error) {
						if !connectStart.IsZero() {
							event.ConnectDurationNs += time.Since(connectStart).Nanoseconds()
						}
					},
					TLSHandshakeStart: func() { tlsStart = time.Now() },
					TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
						if !tlsStart.IsZero() {
							event.TLSDurationNs += time.Since(tlsStart).Nanoseconds()
						}
					},
					GotConn: func(info httptrace.GotConnInfo) {
						event.ConnReused = info.Reused
						event.ConnIdle = info.WasIdle
					},
					WroteRequest: func(httptrace.WroteRequestInfo) { wroteReqStart = time.Now() },
					GotFirstResponseByte: func() {
						firstByteStart = time.Now()
						if !wroteReqStart.IsZero() {
							event.TTFBNs = time.Since(wroteReqStart).Nanoseconds()
						}
					},
				}))

				reqBytesSent := int64(0)
				if req.ContentLength > 0 {
					reqBytesSent = req.ContentLength
				}

				// Capture outgoing request headers for tracing (only when trace is active).
				if ctx.Trace != nil && len(req.Header) > 0 {
					hdrs := make(map[string]string, len(req.Header))
					for k, vals := range req.Header {
						lower := strings.ToLower(k)
						if lower == "authorization" || lower == "x-api-key" || lower == "cookie" {
							hdrs[k] = "***"
							continue
						}
						if len(vals) > 0 {
							hdrs[k] = vals[0]
						}
					}
					if len(hdrs) > 0 {
						event.RequestHeaders = hdrs
					}
				}

				resp, err := bundle.Client.Do(req)
				cancel()
				totalUpstream := time.Since(upstreamStart)
				event.TotalNs = totalUpstream.Nanoseconds()
				if !firstByteStart.IsZero() && event.TTFBNs == 0 {
					event.TTFBNs = firstByteStart.Sub(upstreamStart).Nanoseconds()
				}

				atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, int64(totalUpstream))
				atomic.AddInt64(&ctx.Timing.UpstreamBytesTx, reqBytesSent)
				atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)

				if err != nil {
					event.BytesSent = reqBytesSent
					event.Err = err.Error()
					if ctx.Obs != nil {
						ctx.Obs.RecordUpstream(upstreamHost, totalUpstream, reqBytesSent, 0)
					}
					if ctx.Trace != nil && ctx.Obs != nil {
						ctx.Obs.AppendUpstreamEvent(ctx.Trace, event)
					}
					if ctx.Obs != nil {
						ctx.Obs.LogUpstream(ctx.ApiId, ctx.TenantID, event)
					}
					if attempt < attempts && (retryCondition == "" || strings.Contains(retryCondition, "status") || strings.Contains(retryCondition, "timeout")) && shouldRetryError(err) {
						time.Sleep(backoffDelay(attempt, bundle.Cfg, upstreamHost))
						continue
					}
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					ctx.ErrorMsg = ctx.Alloc(len("upstream call failed"))
					copy(ctx.ErrorMsg, "upstream call failed")
					return engine.StopPlan
				}

				// Capture response headers and first 1KB of body for tracing.
				if ctx.Trace != nil {
					if len(resp.Header) > 0 {
						hdrs := make(map[string]string, len(resp.Header))
						for k, vals := range resp.Header {
							if len(vals) > 0 {
								hdrs[k] = vals[0]
							}
						}
						event.ResponseHeaders = hdrs
					}
					bodyPreview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
					if len(bodyPreview) > 0 {
						event.ResponseBody = string(bodyPreview)
					}
				}
				respBytes, copyErr := io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if ctx.Trace != nil {
					respBytes += int64(len(event.ResponseBody))
				}
				event.BytesSent = reqBytesSent
				event.BytesReceived = respBytes
				atomic.AddInt64(&ctx.Timing.UpstreamBytesRx, respBytes)

				if ctx.Obs != nil {
					ctx.Obs.RecordUpstream(upstreamHost, totalUpstream, reqBytesSent, respBytes)
				}

				if copyErr != nil {
					event.Err = copyErr.Error()
					if ctx.Trace != nil && ctx.Obs != nil {
						ctx.Obs.AppendUpstreamEvent(ctx.Trace, event)
					}
					if ctx.Obs != nil {
						ctx.Obs.LogUpstream(ctx.ApiId, ctx.TenantID, event)
					}
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					ctx.ErrorMsg = ctx.Alloc(len("upstream call failed"))
					copy(ctx.ErrorMsg, "upstream call failed")
					return engine.StopPlan
				}

				ctx.ResponseStatus = resp.StatusCode
				event.Status = resp.StatusCode
				if ctx.Trace != nil && ctx.Obs != nil {
					ctx.Obs.AppendUpstreamEvent(ctx.Trace, event)
				}

				if attempt < attempts && (retryCondition == "" || strings.Contains(retryCondition, "status")) && isRetryableStatus(resp.StatusCode, bundle.Cfg) {
					sleepFor := backoffDelay(attempt, bundle.Cfg, upstreamHost)
					if bundle.Cfg.HonorRetryAfter {
						if ra, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
							if ra > bundle.Cfg.RetryAfterMaxWait {
								// Upstream wants us to wait longer than we allow — mark cooldown and stop.
								if bundle.Cfg.UpstreamCooldown {
									markCooldown(upstreamHost, time.Now().Add(ra))
								}
								ctx.ResponseStatus = resp.StatusCode
								ctx.Failed = true
								ctx.ErrorCode = int16(resp.StatusCode)
								msg := "upstream requested retry-after exceeds max wait"
								ctx.ErrorMsg = ctx.Alloc(len(msg))
								copy(ctx.ErrorMsg, msg)
								return engine.StopPlan
							}
							sleepFor = ra
						}
					}
					time.Sleep(sleepFor)
					continue
				}

				return state.PC + 1
			}

			ctx.Failed = true
			ctx.ErrorCode = int16(ctx.ResponseStatus)
			ctx.ErrorMsg = ctx.Alloc(len("upstream call failed"))
			copy(ctx.ErrorMsg, "upstream call failed")
			return engine.StopPlan
		},
	}
}
