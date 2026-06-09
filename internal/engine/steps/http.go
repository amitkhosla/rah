package steps

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"rah/internal/egress"
	"rah/internal/engine"
	"rah/internal/gatewaylog"
	"rah/internal/observability"
	"rah/internal/rctx"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// bytesReaderPool recycles bytes.Reader instances to avoid allocations in the hot path.
var bytesReaderPool = sync.Pool{New: func() any { return new(bytes.Reader) }}

// responseBodyPool recycles bytes.Buffer instances for capturing response bodies.
var responseBodyPool = sync.Pool{New: func() any {
	b := new(bytes.Buffer)
	b.Grow(4096)
	return b
}}

// streamCopyPool recycles 32 KB scratch buffers for io.CopyBuffer in the streaming
// path. Stores *[]byte (pointer) to avoid boxing the slice header into the interface,
// which would allocate. Prevents per-request heap allocation that io.Copy triggers
// when the destination does not implement io.ReaderFrom.
var streamCopyPool = sync.Pool{New: func() any {
	b := make([]byte, 32*1024)
	return &b
}}

// HttpActionConfig holds the bake-time configuration for an http_call instruction.
// All slot indices use -1 to indicate "not set / use static value".
type HttpActionConfig struct {
	StaticURL         string
	StaticMethod      string
	StaticContentType string
	URLSlot           int // -1 = use StaticURL
	MethodSlot        int // -1 = use StaticMethod (unused today; reserved for future)
	BodySlot          int // -1 = use StagedRequestBody (or no body)
	ContentTypeSlot   int // -1 = use StaticContentType or StagedContentType
	Timeout           uint32
	MaxRetries        int
	RetryCondFunc     ConditionFunc // nil = no condition-based retry
	ResponseBodySlot  int           // -1 = not captured
	ResponseStatusSlot int          // -1 = not captured
	ResponseHeaderSlots []HeaderSlotBinding
	ForwardIncomingHeaders  bool
	ForwardResponseHeaders  bool
	BlockHeadersMap         map[string]struct{} // pre-built at bake time; nil = no blocking
	// FlowInput carries http.* tuning keys forwarded from the step Input map.
	FlowInput map[string]string
	// EgressProfile selects the transport protocol for this call.
	// nil = Auto (ForceAttemptHTTP2:true, same as legacy behavior).
	EgressProfile *egress.EgressProfile
	// TLSClientCert and TLSClientKey are PEM-encoded bytes loaded at bake time.
	// Both must be non-nil to enable mTLS. nil = no client certificate (default behaviour unchanged).
	TLSClientCert []byte
	TLSClientKey  []byte
}

// HeaderSlotBinding maps one response header name to a ByteSlots index.
type HeaderSlotBinding struct {
	HeaderName string
	Slot       int
}

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

// statusBitset covers HTTP status codes 400–655 (256 bits = 4×uint64, 32 bytes).
// Replaces map[int]struct{} in httpClientConfig — value type, no heap, no GC, O(1) lookup.
type statusBitset [4]uint64

func (b *statusBitset) set(code int) {
	if code < 400 || code >= 656 {
		return
	}
	idx := code - 400
	b[idx>>6] |= 1 << uint(idx&63)
}

func (b statusBitset) has(code int) bool {
	if code < 400 || code >= 656 {
		return false
	}
	idx := code - 400
	return b[idx>>6]&(1<<uint(idx&63)) != 0
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
	RetryOnStatuses       statusBitset
	HonorRetryAfter       bool
	RetryAfterMaxWait     time.Duration
	UpstreamCooldown      bool
}

type cachedClient struct {
	Client *http.Client
	Cfg    httpClientConfig
}

// transportShard owns one http.Client/Transport and its connection pool.
// Padded to a full cache line to prevent false sharing between adjacent shards.
type transportShard struct {
	_      [64]byte
	client *http.Client
}

// upstreamTransportPool distributes requests across N independent transports
// for a single upstream target, reducing http.Transport.idleMu contention.
type upstreamTransportPool struct {
	shards []transportShard
	mask   uint64 // shards-1; bitwise AND replaces modulo
	ctr    atomic.Uint64
}

func (p *upstreamTransportPool) get() *http.Client {
	return p.shards[p.ctr.Add(1)&p.mask].client
}

type clientCacheKey struct {
	Upstream   string
	ConfigKey  string
	EgressType egress.EgressType // 0=Auto, 1=HTTP1, 2=HTTPS, 3=H2C
}

var globalShardsPerCPU = 4

// SetTransportShardsPerCPU sets the per-CPU transport shard multiplier.
// Must be called before the first HTTP request. Default is 4.
func SetTransportShardsPerCPU(n int) {
	if n >= 1 {
		globalShardsPerCPU = n
	}
}

var (
	cfgOnce                  sync.Once
	cachedDefaultHTTPConfig  httpClientConfig
	defaultHTTPClient        *http.Client
	defaultTransportPool     *upstreamTransportPool
	perTargetClientCache     sync.Map
	perTargetClientCacheSize atomic.Int64
)

func loadHTTPClientConfig() httpClientConfig {
	var retryStatuses statusBitset
	retryStatuses.set(429)
	retryStatuses.set(502)
	retryStatuses.set(503)
	retryStatuses.set(504)
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
		RetryOnStatuses:       retryStatuses,
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
			ForceAttemptHTTP2:     true,
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

// buildClientForProfile constructs an *http.Client using the appropriate
// transport for the given EgressProfile. nil profile → Auto (same as buildHTTPClient).
func buildClientForProfile(profile *egress.EgressProfile, cfg httpClientConfig) *http.Client {
	if profile == nil || profile.Type == egress.EgressTypeAuto {
		return buildHTTPClient(cfg)
	}
	switch profile.Type {
	case egress.EgressTypeHTTP1:
		t := egress.BuildHTTP1Transport(
			cfg.DialTimeout, cfg.KeepAlive, cfg.IdleConnTimeout,
			cfg.TLSHandshakeTimeout, cfg.ResponseHeaderTimeout, cfg.ExpectContinueTimeout,
			cfg.MaxIdleConns, cfg.MaxIdleConnsPerHost, cfg.MaxConnsPerHost,
		)
		return &http.Client{Transport: t, Timeout: cfg.RequestTimeout}
	case egress.EgressTypeHTTPS:
		t := egress.BuildHTTPSTransport(
			profile,
			cfg.DialTimeout, cfg.KeepAlive, cfg.IdleConnTimeout,
			cfg.TLSHandshakeTimeout, cfg.ResponseHeaderTimeout, cfg.ExpectContinueTimeout,
			cfg.MaxIdleConns, cfg.MaxIdleConnsPerHost, cfg.MaxConnsPerHost,
		)
		return &http.Client{Transport: t, Timeout: cfg.RequestTimeout}
	case egress.EgressTypeH2C:
		t := egress.BuildH2CTransport(profile, cfg.DialTimeout, cfg.KeepAlive)
		return &http.Client{Transport: t, Timeout: cfg.RequestTimeout}
	default:
		return buildHTTPClient(cfg)
	}
}

// getClientForProfile is the profile-aware replacement for getClientForTarget.
// When profile is nil, behavior is identical to getClientForTarget (Auto).
func getClientForProfile(profile *egress.EgressProfile, upstreamHost string, flowInput map[string]string) cachedClient {
	getDefaultHTTPConfig()
	cfg := resolveHTTPConfigForTarget(upstreamHost, flowInput)
	var et egress.EgressType
	if profile != nil {
		et = profile.Type
	}
	key := clientCacheKey{Upstream: upstreamHost, ConfigKey: configFingerprint(cfg), EgressType: et}
	if existing, ok := perTargetClientCache.Load(key); ok {
		return existing.(cachedClient)
	}

	created := cachedClient{Client: buildClientForProfile(profile, cfg), Cfg: cfg}
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

// getClientForBakedConfig is the per-request client resolver for dynamic-URL flows.
// bakedCfg and bakedFingerprint are computed once at bake time; the hot path here
// is a single sync.Map.Load (cache hit). Cache misses build and store a new client.
func getClientForBakedConfig(profile *egress.EgressProfile, upstreamHost string, bakedCfg httpClientConfig, bakedFingerprint string, maxEntries int) cachedClient {
	var et egress.EgressType
	if profile != nil {
		et = profile.Type
	}
	key := clientCacheKey{Upstream: upstreamHost, ConfigKey: bakedFingerprint, EgressType: et}
	if existing, ok := perTargetClientCache.Load(key); ok {
		return existing.(cachedClient)
	}
	created := cachedClient{Client: buildClientForProfile(profile, bakedCfg), Cfg: bakedCfg}
	if int(perTargetClientCacheSize.Load()) >= maxEntries {
		return cachedClient{Client: defaultHTTPClient, Cfg: getDefaultHTTPConfig()}
	}
	actual, loaded := perTargetClientCache.LoadOrStore(key, created)
	if loaded {
		return actual.(cachedClient)
	}
	perTargetClientCacheSize.Add(1)
	return created
}

func parseStatusSet(raw string, fallback statusBitset) statusBitset {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	var out statusBitset
	found := false
	for part := range strings.SplitSeq(raw, ",") {
		code, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && code > 0 {
			out.set(code)
			found = true
		}
	}
	if !found {
		return fallback
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
	cfg := base // statusBitset copies by value — no allocation

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
	// statusBitset encoded as 4 hex words — no sorting needed, deterministic.
	statusKey := strconv.FormatUint(cfg.RetryOnStatuses[0], 16) + "," +
		strconv.FormatUint(cfg.RetryOnStatuses[1], 16) + "," +
		strconv.FormatUint(cfg.RetryOnStatuses[2], 16) + "," +
		strconv.FormatUint(cfg.RetryOnStatuses[3], 16)
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
		statusKey,
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
	return cfg.RetryOnStatuses.has(status)
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

				// Detect client disconnect before each attempt — avoids hitting
				// the upstream on behalf of an already-gone caller.
				if pc, stop := StopIfCancelled(ctx); stop {
					return pc
				}

				// Set deadline on ctx — ctx implements context.Context so it is passed
				// directly to NewRequestWithContext. No timerCtx allocation needed.
				var deadlineTimer *time.Timer
				if timeout > 0 {
					dur := time.Duration(timeout) * time.Millisecond
					capturedGen := ctx.SetUpstreamTimeout(dur)
					deadlineTimer = time.AfterFunc(dur, func() {
						ctx.CancelIfGeneration(capturedGen, context.DeadlineExceeded)
					})
				}

				req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
				if err != nil {
					if deadlineTimer != nil {
						deadlineTimer.Stop()
						ctx.ClearUpstreamTimeout()
					}
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
				// Stop timer — if it already fired, Cancel was already called (fine;
				// the request was aborted). If it hasn't fired yet, stop it from firing
				// after ctx is returned to the pool. Unlike context.WithTimeout.cancel(),
				// stopping our timer never marks the connection as broken.
				if deadlineTimer != nil {
					deadlineTimer.Stop()
					deadlineTimer = nil
					ctx.ClearUpstreamTimeout()
				}
				totalUpstream := time.Since(upstreamStart)
				event.TotalNs = totalUpstream.Nanoseconds()
				if !firstByteStart.IsZero() && event.TTFBNs == 0 {
					event.TTFBNs = firstByteStart.Sub(upstreamStart).Nanoseconds()
				}

				atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, int64(totalUpstream))
				atomic.AddInt64(&ctx.Timing.UpstreamBytesTx, reqBytesSent)
				atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)

				if err != nil {
					// Client disconnected during upstream call — mark and stop cleanly.
					if errors.Is(err, context.Canceled) {
						atomic.StoreInt32(&ctx.Cancelled, 1)
						return engine.StopCancelled
					}
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

// HttpActionFromConfig builds an http_call Instruction from a fully-resolved
// HttpActionConfig. This replaces the positional-argument HttpAction function
// and is the target of the S11 compiler rewrite.
//
// If cfg.TLSClientCert and cfg.TLSClientKey are both non-nil the cert is parsed
// once here (bake time) and stored in the closure.  Per-request cost: zero.
func HttpActionFromConfig(cfg HttpActionConfig) engine.Instruction {
	flowInput := cfg.FlowInput

	// ── HTTP config resolved once at bake time ────────────────────────────────
	// flowInput is fixed; resolveHTTPConfigForTarget ignores upstreamHost entirely.
	// configFingerprint, retry budget, and (for static URLs) the full *http.Client
	// are all computed here so the per-request hot path carries zero config work.
	bakedCfg := resolveHTTPConfigForTarget("", flowInput)
	bakedFingerprint := configFingerprint(bakedCfg)
	bakedMaxCacheEntries := flowInt(flowInput, "http.max_client_cache_entries", 2048)

	bakedAttempts := bakedCfg.RetryMaxAttempts + 1
	if cfg.MaxRetries >= 0 {
		bakedAttempts = cfg.MaxRetries + 1
	}
	if bakedAttempts < 1 {
		bakedAttempts = 1
	}

	// For static URLs the upstream host is known — build and cache the *http.Client
	// once here. Per-request cost becomes a single pointer read from the closure.
	var staticHTTPClient *http.Client
	hasStaticURL := cfg.StaticURL != "" && cfg.URLSlot < 0
	if hasStaticURL {
		effectiveProfile := cfg.EgressProfile
		effectiveURL := cfg.StaticURL
		if strings.HasPrefix(effectiveURL, "h2c://") {
			effectiveURL = "http://" + effectiveURL[len("h2c://"):]
			if effectiveProfile == nil || effectiveProfile.Type != egress.EgressTypeH2C {
				effectiveProfile = &egress.EgressProfile{Type: egress.EgressTypeH2C}
			}
		}
		staticHTTPClient = getClientForBakedConfig(effectiveProfile, extractUpstreamHost(effectiveURL), bakedCfg, bakedFingerprint, bakedMaxCacheEntries).Client
	}

	// ── Parse mTLS client certificate at bake time ─────────────────────────────
	// tls.X509KeyPair is called once here, not per request. If parsing fails we
	// return an instruction that always fails with a clear 500 error so the
	// operator sees it immediately rather than at runtime.
	var mtlsCert *tls.Certificate
	if len(cfg.TLSClientCert) > 0 && len(cfg.TLSClientKey) > 0 {
		cert, err := tls.X509KeyPair(cfg.TLSClientCert, cfg.TLSClientKey)
		if err != nil {
			return engine.Instruction{
				Name: "HTTP_CALL",
				Action: func(ctx *rctx.Context, _ *engine.ExecutionState) int16 {
					ctx.ResponseStatus = 500
					ctx.Failed = true
					ctx.ErrorCode = 500
					msg := "http_call: invalid tls_client_cert/key: " + err.Error()
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				},
			}
		}
		mtlsCert = &cert
	}

	// mtlsClient is a dedicated *http.Client that presents the client certificate.
	// Built lazily on first use (sync.Once) so we only pay the construction cost
	// when this instruction actually executes, not during bake.
	var (
		mtlsClientOnce sync.Once
		mtlsClient     *http.Client
	)
	getMTLSClient := func() *http.Client {
		if mtlsCert == nil {
			return nil
		}
		mtlsClientOnce.Do(func() {
			dialer := &net.Dialer{Timeout: bakedCfg.DialTimeout, KeepAlive: bakedCfg.KeepAlive}
			tlsCfg := &tls.Config{
				Certificates: []tls.Certificate{*mtlsCert},
				MinVersion:   tls.VersionTLS12,
			}
			mtlsClient = &http.Client{
				Transport: &http.Transport{
					Proxy:                 http.ProxyFromEnvironment,
					DialContext:           dialer.DialContext,
					ForceAttemptHTTP2:     true,
					TLSClientConfig:       tlsCfg,
					MaxIdleConns:          bakedCfg.MaxIdleConns,
					MaxIdleConnsPerHost:   bakedCfg.MaxIdleConnsPerHost,
					MaxConnsPerHost:       bakedCfg.MaxConnsPerHost,
					IdleConnTimeout:       bakedCfg.IdleConnTimeout,
					TLSHandshakeTimeout:   bakedCfg.TLSHandshakeTimeout,
					ResponseHeaderTimeout: bakedCfg.ResponseHeaderTimeout,
					ExpectContinueTimeout: bakedCfg.ExpectContinueTimeout,
				},
				Timeout: bakedCfg.RequestTimeout,
			}
		})
		return mtlsClient
	}

	return engine.Instruction{
		Name: "HTTP_CALL",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// ── Resolve URL ───────────────────────────────────────────────────────
			rawUpstream := cfg.StaticURL
			if cfg.URLSlot >= 0 && cfg.URLSlot < len(ctx.ByteSlots) && len(ctx.ByteSlots[cfg.URLSlot]) > 0 {
				rawUpstream = string(ctx.ByteSlots[cfg.URLSlot])
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

			// ── Egress profile + h2c scheme rewriting ─────────────────────────
			profile := cfg.EgressProfile
			if strings.HasPrefix(url, "h2c://") {
				// Rewrite h2c:// → http:// so net/http can parse it.
				url = "http://" + url[len("h2c://"):]
				// Auto-upgrade to H2C transport if not already set.
				if profile == nil || profile.Type != egress.EgressTypeH2C {
					profile = &egress.EgressProfile{Type: egress.EgressTypeH2C}
				}
			}

			upstreamHost := extractUpstreamHost(url)

			// ── Select HTTP client ────────────────────────────────────────────
			// mTLS: dedicated client (lazy sync.Once). Static URL: pointer baked at
			// instruction creation. Dynamic URL: sync.Map.Load with baked fingerprint.
			var httpClient *http.Client
			if mtlsCert != nil {
				httpClient = getMTLSClient()
			} else if hasStaticURL {
				httpClient = staticHTTPClient
			} else {
				httpClient = getClientForBakedConfig(profile, upstreamHost, bakedCfg, bakedFingerprint, bakedMaxCacheEntries).Client
			}

			// ── Retry budget (baked at instruction creation time) ─────────────
			attempts := bakedAttempts

			// ── Method ────────────────────────────────────────────────────────────
			method := cfg.StaticMethod
			if method == "" {
				method = "GET"
			}
			if cfg.MethodSlot >= 0 && cfg.MethodSlot < len(ctx.ByteSlots) && len(ctx.ByteSlots[cfg.MethodSlot]) > 0 {
				method = string(ctx.ByteSlots[cfg.MethodSlot])
			}

			for attempt := 1; attempt <= attempts; attempt++ {
				// ── Cooldown guard ────────────────────────────────────────────────
				if bakedCfg.UpstreamCooldown {
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

				// ── Disconnect check ──────────────────────────────────────────────
				if pc, stop := StopIfCancelled(ctx); stop {
					return pc
				}

				upstreamStart := time.Now()
				event := observability.UpstreamEvent{Host: upstreamHost, URL: url, Attempt: attempt}
				var dnsStart, connectStart, tlsStart, wroteReqStart, firstByteStart time.Time

				// ── Set deadline on ctx — ctx implements context.Context ─────────
				// Passed directly to NewRequestWithContext; no timerCtx allocation.
				var deadlineTimer *time.Timer
				if cfg.Timeout > 0 {
					dur := time.Duration(cfg.Timeout) * time.Millisecond
					capturedGen := ctx.SetUpstreamTimeout(dur)
					deadlineTimer = time.AfterFunc(dur, func() {
						ctx.CancelIfGeneration(capturedGen, context.DeadlineExceeded)
					})
				}

				// ── Resolve body ──────────────────────────────────────────────────
				// Priority: StagedRequestBody > BodySlot > nil (no body)
				var bodyBytes []byte
				if len(ctx.StagedRequestBody) > 0 {
					bodyBytes = ctx.StagedRequestBody
				} else if cfg.BodySlot >= 0 && cfg.BodySlot < len(ctx.ByteSlots) {
					bodyBytes = ctx.ByteSlots[cfg.BodySlot]
				}

				// ── Resolve Content-Type ──────────────────────────────────────────
				contentType := cfg.StaticContentType
				if len(ctx.StagedContentType) > 0 {
					contentType = string(ctx.StagedContentType)
				} else if cfg.ContentTypeSlot >= 0 && cfg.ContentTypeSlot < len(ctx.ByteSlots) && len(ctx.ByteSlots[cfg.ContentTypeSlot]) > 0 {
					contentType = string(ctx.ByteSlots[cfg.ContentTypeSlot])
				}

				// ── Build http.Request ────────────────────────────────────────────
				var req *http.Request
				var pooledReader *bytes.Reader // tracked so we can return it to pool
				if len(bodyBytes) > 0 {
					pooledReader = bytesReaderPool.Get().(*bytes.Reader)
					pooledReader.Reset(bodyBytes)
					req, err = http.NewRequestWithContext(ctx, method, url, pooledReader)
					if err != nil {
						bytesReaderPool.Put(pooledReader)
						pooledReader = nil
						if deadlineTimer != nil {
							deadlineTimer.Stop()
							ctx.ClearUpstreamTimeout()
						}
						ctx.ResponseStatus = 500
						ctx.Failed = true
						ctx.ErrorCode = 500
						ctx.ErrorMsg = ctx.Alloc(len("upstream call failed"))
						copy(ctx.ErrorMsg, "upstream call failed")
						return engine.StopPlan
					}
					req.ContentLength = int64(len(bodyBytes))
				} else {
					req, err = http.NewRequestWithContext(ctx, method, url, nil)
					if err != nil {
						if deadlineTimer != nil {
							deadlineTimer.Stop()
							ctx.ClearUpstreamTimeout()
						}
						ctx.ResponseStatus = 500
						ctx.Failed = true
						ctx.ErrorCode = 500
						ctx.ErrorMsg = ctx.Alloc(len("upstream call failed"))
						copy(ctx.ErrorMsg, "upstream call failed")
						return engine.StopPlan
					}
				}

				// ── Forward incoming headers ──────────────────────────────────────
				if cfg.ForwardIncomingHeaders && ctx.Request != nil {
					for key, vals := range ctx.Request.Header {
						if _, skip := hopByHopHeaders[key]; skip {
							continue
						}
						if cfg.BlockHeadersMap != nil {
							if _, blocked := cfg.BlockHeadersMap[key]; blocked {
								continue
							}
						}
						req.Header[key] = vals
					}
				}

				// ── Apply MutationLog (BUG FIX: was never applied to http_call) ──
				for i := 0; i < ctx.MutationCount; i++ {
					m := ctx.MutationLog[i]
					if m.Op == 1 {
						req.Header.Del(string(m.Key))
					} else {
						req.Header.Set(string(m.Key), string(m.Value))
					}
				}

				// ── Block headers override (explicit block list) ───────────────────
				if cfg.BlockHeadersMap != nil {
					for key := range cfg.BlockHeadersMap {
						req.Header.Del(key)
					}
				}

				// ── Content-Type ──────────────────────────────────────────────────
				if contentType != "" && len(bodyBytes) > 0 {
					req.Header.Set("Content-Type", contentType)
				}

				// ── Tracing ───────────────────────────────────────────────────────
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

				reqBytesSent := req.ContentLength
				if reqBytesSent < 0 {
					reqBytesSent = 0
				}

				// ── Capture outgoing request headers for tracing ──────────────────
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

				resp, doErr := httpClient.Do(req)
				// Stop timer immediately — unlike context.WithTimeout.cancel(), stopping
				// our timer never marks the connection as broken, preserving connection reuse.
				if deadlineTimer != nil {
					deadlineTimer.Stop()
					deadlineTimer = nil
					ctx.ClearUpstreamTimeout()
				}

				// Return the bytes.Reader to pool now that Do() has consumed it.
				if pooledReader != nil {
					bytesReaderPool.Put(pooledReader)
					pooledReader = nil
				}
				// Clear staged body after first attempt (not per-retry).
				if attempt == 1 {
					ctx.StagedRequestBody = nil
					ctx.StagedContentType = nil
				}

				totalUpstream := time.Since(upstreamStart)
				event.TotalNs = totalUpstream.Nanoseconds()
				if !firstByteStart.IsZero() && event.TTFBNs == 0 {
					event.TTFBNs = firstByteStart.Sub(upstreamStart).Nanoseconds()
				}
				atomic.AddInt64(&ctx.Timing.UpstreamTimeNs, int64(totalUpstream))
				atomic.AddInt64(&ctx.Timing.UpstreamBytesTx, reqBytesSent)
				atomic.AddInt32(&ctx.Timing.UpstreamCalls, 1)

				if doErr != nil {
					// Client disconnect
					if errors.Is(doErr, context.Canceled) || ctx.Request.Context().Err() != nil {
						atomic.StoreInt32(&ctx.Cancelled, 1)
						return engine.StopCancelled
					}
					event.BytesSent = reqBytesSent
					event.Err = doErr.Error()
					if ctx.Obs != nil {
						ctx.Obs.RecordUpstream(upstreamHost, totalUpstream, reqBytesSent, 0)
					}
					if ctx.Trace != nil && ctx.Obs != nil {
						ctx.Obs.AppendUpstreamEvent(ctx.Trace, event)
					}
					if ctx.Obs != nil {
						ctx.Obs.LogUpstream(ctx.ApiId, ctx.TenantID, event)
					}
					if attempt < attempts && shouldRetryError(doErr) {
						time.Sleep(backoffDelay(attempt, bakedCfg, upstreamHost))
						continue
					}
					ctx.ResponseStatus = 502
					ctx.Failed = true
					ctx.ErrorCode = 502
					ctx.ErrorMsg = ctx.Alloc(len("upstream call failed"))
					copy(ctx.ErrorMsg, "upstream call failed")
					return engine.StopPlan
				}

				// ── Capture response headers for tracing ──────────────────────────
				if ctx.Trace != nil && len(resp.Header) > 0 {
					hdrs := make(map[string]string, len(resp.Header))
					for k, vals := range resp.Header {
						if len(vals) > 0 {
							hdrs[k] = vals[0]
						}
					}
					event.ResponseHeaders = hdrs
				}

				// ── Capture response header slots ─────────────────────────────────
				for _, hsb := range cfg.ResponseHeaderSlots {
					val := resp.Header.Get(hsb.HeaderName)
					if val != "" && hsb.Slot >= 0 && hsb.Slot < len(ctx.ByteSlots) {
						buf := ctx.Alloc(len(val))
						copy(buf, val)
						ctx.ByteSlots[hsb.Slot] = buf
					}
				}

				// ── Forward upstream response headers to client ───────────────────
				if cfg.ForwardResponseHeaders {
					for name, vals := range resp.Header {
						if _, skip := hopByHopHeaders[name]; skip {
							continue
						}
						if cfg.BlockHeadersMap != nil {
							if _, blocked := cfg.BlockHeadersMap[name]; blocked {
								continue
							}
						}
						for _, v := range vals {
							keyBuf := ctx.Alloc(len(name))
							copy(keyBuf, name)
							valBuf := ctx.Alloc(len(v))
							copy(valBuf, v)
							ctx.SetResponseHeader(keyBuf, valBuf)
						}
					}
				}

				// ── Read response body ────────────────────────────────────────────
				// Compiler sets StreamResponseBody=true when the body needs no slot
				// capture (no response_body_var, or only used in respond/return).
				// In that case: pipe directly to client, track bytes only.
				// Otherwise: read into arena slot for downstream steps.
				var respBytes int64
				if ctx.StreamResponseBody {
					ctx.ResponseStatus = resp.StatusCode
					if ctx.Trace != nil {
						bodyPreview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
						if len(bodyPreview) > 0 {
							event.ResponseBody = string(bodyPreview)
							nw, _ := ctx.Write(bodyPreview)
							respBytes = int64(nw)
						}
					}
					copyBuf := streamCopyPool.Get().(*[]byte)
					n, _ := io.CopyBuffer(ctx, resp.Body, *copyBuf)
					streamCopyPool.Put(copyBuf)
					respBytes += n
					resp.Body.Close()
				} else if cfg.ResponseBodySlot >= 0 && cfg.ResponseBodySlot < len(ctx.ByteSlots) {
					cl := resp.ContentLength
					if cl > 0 {
						// Fast path: known Content-Length — allocate exactly.
						bodyBuf := ctx.Alloc(int(cl))
						n, readErr := io.ReadFull(resp.Body, bodyBuf)
						respBytes = int64(n)
						resp.Body.Close()
						if readErr != nil && readErr != io.ErrUnexpectedEOF {
							event.Err = readErr.Error()
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
						ctx.ByteSlots[cfg.ResponseBodySlot] = bodyBuf[:n]
					} else {
						// Unknown Content-Length — use pool buffer.
						buf := responseBodyPool.Get().(*bytes.Buffer)
						buf.Reset()
						var bodyPreview []byte
						if ctx.Trace != nil {
							bodyPreview, _ = io.ReadAll(io.LimitReader(resp.Body, 1024))
							buf.Write(bodyPreview)
							_, _ = io.Copy(buf, resp.Body)
						} else {
							_, _ = io.Copy(buf, resp.Body)
						}
						resp.Body.Close()
						respBytes = int64(buf.Len())
						// Copy captured body into arena.
						arena := ctx.Alloc(buf.Len())
						copy(arena, buf.Bytes())
						ctx.ByteSlots[cfg.ResponseBodySlot] = arena
						if ctx.Trace != nil && len(bodyPreview) > 0 {
							event.ResponseBody = string(bodyPreview)
							respBytes += int64(len(bodyPreview))
						}
						buf.Reset()
						responseBodyPool.Put(buf)
					}
				} else {
					respBytes, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}

				event.BytesSent = reqBytesSent
				event.BytesReceived = respBytes
				atomic.AddInt64(&ctx.Timing.UpstreamBytesRx, respBytes)

				if ctx.Obs != nil {
					ctx.Obs.RecordUpstream(upstreamHost, totalUpstream, reqBytesSent, respBytes)
				}

				// ── Set response status ───────────────────────────────────────────
				// For streaming: status was already set before io.Copy (headers already sent).
				if !ctx.StreamResponseBody {
					ctx.ResponseStatus = resp.StatusCode
				}
				event.Status = resp.StatusCode

				// ── Capture response status into slot ─────────────────────────────
				if cfg.ResponseStatusSlot >= 0 && cfg.ResponseStatusSlot < len(ctx.IntSlots) {
					ctx.IntSlots[cfg.ResponseStatusSlot] = int64(resp.StatusCode)
				}

				if ctx.Trace != nil && ctx.Obs != nil {
					ctx.Obs.AppendUpstreamEvent(ctx.Trace, event)
				}
				if ctx.Obs != nil {
					ctx.Obs.LogUpstream(ctx.ApiId, ctx.TenantID, event)
				}

				// ── Structured upstream log ───────────────────────────────────────
				if gatewaylog.Default.ShouldLog(gatewaylog.INFO) {
					info := gatewaylog.UpstreamCallInfo{
						Method:       method,
						URL:          url,
						StatusCode:   resp.StatusCode,
						DurationMs:   float64(totalUpstream.Nanoseconds()) / 1e6,
						RequestSize:  reqBytesSent,
						ResponseSize: respBytes,
						ConnectMs:    float64(event.ConnectDurationNs) / 1e6,
						TLSMs:        float64(event.TLSDurationNs) / 1e6,
						TTFBMs:       float64(event.TTFBNs) / 1e6,
						RetryCount:   attempt - 1,
					}
					fields := gatewaylog.BuildUpstreamFields(gatewaylog.DefaultUpstreamFields, info)
					gatewaylog.Default.Info("[upstream] call completed", fields...)
				}

				// ── Retry condition ───────────────────────────────────────────────
				if attempt < attempts {
					// Config-level retry-on-status
					doStatusRetry := isRetryableStatus(resp.StatusCode, bakedCfg)
					// User-supplied condition retry (overrides config-level)
					doCondRetry := cfg.RetryCondFunc != nil && cfg.RetryCondFunc(ctx)

					if doStatusRetry || doCondRetry {
						sleepFor := backoffDelay(attempt, bakedCfg, upstreamHost)
						if bakedCfg.HonorRetryAfter {
							if ra, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
								if ra > bakedCfg.RetryAfterMaxWait {
									if bakedCfg.UpstreamCooldown {
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
