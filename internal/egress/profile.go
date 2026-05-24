package egress

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

// EgressType identifies the transport protocol for an upstream connection.
type EgressType uint8

const (
	// EgressTypeAuto is the default: https URLs get HTTP/2 via ALPN with
	// automatic HTTP/1.1 fallback; http URLs get HTTP/1.1.
	EgressTypeAuto EgressType = 0
	// EgressTypeHTTP1 forces HTTP/1.1 regardless of URL scheme.
	EgressTypeHTTP1 EgressType = 1
	// EgressTypeHTTPS uses HTTPS with HTTP/2 via ALPN and an optional custom
	// TLS config (e.g. custom CA pool or skip-verify for dev).
	EgressTypeHTTPS EgressType = 2
	// EgressTypeH2C uses cleartext HTTP/2. The upstream must support h2c.
	// The caller must rewrite h2c:// → http:// before constructing requests.
	EgressTypeH2C EgressType = 3
)

// EgressProfile holds the resolved transport configuration for one named profile.
// Instances are immutable after creation.
type EgressProfile struct {
	ID          uint8
	Type        EgressType
	TLSConfig   *tls.Config   // nil = system CA pool; set for custom CA or skip-verify
	DialTimeout time.Duration // 0 = caller's default applies
	ReqTimeout  time.Duration // 0 = caller's default applies
}

// BuildHTTPSTransport creates an http.Transport that negotiates HTTP/2 via ALPN
// when connecting to HTTPS upstreams, with automatic HTTP/1.1 fallback.
// p.TLSConfig is used if non-nil (custom CA, skip-verify); nil uses the system pool.
func BuildHTTPSTransport(
	p *EgressProfile,
	dialTimeout, keepAlive, idleTimeout, tlsTimeout, respHeaderTimeout, expectContinueTimeout time.Duration,
	maxIdle, maxIdlePerHost, maxPerHost int,
) *http.Transport {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       p.TLSConfig,
		MaxIdleConns:          maxIdle,
		MaxIdleConnsPerHost:   maxIdlePerHost,
		MaxConnsPerHost:       maxPerHost,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   tlsTimeout,
		ResponseHeaderTimeout: respHeaderTimeout,
		ExpectContinueTimeout: expectContinueTimeout,
	}
}

// BuildH2CTransport creates an http2.Transport for cleartext HTTP/2 (h2c).
// AllowHTTP permits plain TCP connections without TLS negotiation.
func BuildH2CTransport(p *EgressProfile, dialTimeout, keepAlive time.Duration) *http2.Transport {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}
	return &http2.Transport{
		AllowHTTP: true,
		// DialTLSContext is used by http2.Transport for all connections.
		// When AllowHTTP=true, returning a plain TCP conn is correct.
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
	}
}

// BuildHTTP1Transport creates an http.Transport that uses HTTP/1.1 only.
// ForceAttemptHTTP2 is intentionally absent.
func BuildHTTP1Transport(
	dialTimeout, keepAlive, idleTimeout, tlsTimeout, respHeaderTimeout, expectContinueTimeout time.Duration,
	maxIdle, maxIdlePerHost, maxPerHost int,
) *http.Transport {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		MaxIdleConns:          maxIdle,
		MaxIdleConnsPerHost:   maxIdlePerHost,
		MaxConnsPerHost:       maxPerHost,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   tlsTimeout,
		ResponseHeaderTimeout: respHeaderTimeout,
		ExpectContinueTimeout: expectContinueTimeout,
	}
}
