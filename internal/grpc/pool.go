package grpcutil

import (
	"crypto/tls"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"rah/internal/egress"
)

// connKey uniquely identifies a gRPC connection by its target address and the
// egress profile that governs it. profileID=0 means no profile (default creds).
type connKey struct {
	addr      string // normalized "host:port" — no scheme
	profileID uint8
	useTLS    bool // grpcs:// vs grpc://
}

// ConnPool maintains a pool of reusable *grpc.ClientConn instances keyed by
// (normalizedAddr, profileID, useTLS). A single ClientConn handles thousands of
// concurrent RPCs via HTTP/2 stream multiplexing — no per-request dialing.
//
// ConnPool is safe for concurrent use.
type ConnPool struct {
	mu    sync.RWMutex
	conns map[connKey]*grpc.ClientConn

	// Gateway-wide keepalive defaults. Applied when the EgressProfile does not
	// override them. Zero values disable keepalive.
	KeepaliveTime    time.Duration
	KeepaliveTimeout time.Duration
}

// NewConnPool returns an empty, ready-to-use ConnPool.
func NewConnPool() *ConnPool {
	return &ConnPool{conns: make(map[connKey]*grpc.ClientConn)}
}

// Get returns an existing ClientConn for the given URL and profile, creating one
// if needed. The URL must use the grpc:// or grpcs:// scheme.
//
//   - grpc://host:port  → insecure (no TLS)
//   - grpcs://host:port → TLS (system CA unless profile.TLSConfig is set)
//
// profile may be nil; in that case the scheme alone determines TLS use.
func (p *ConnPool) Get(rawURL string, profile *egress.EgressProfile) (*grpc.ClientConn, error) {
	addr, useTLS, err := parseGRPCURL(rawURL)
	if err != nil {
		return nil, err
	}

	var profileID uint8
	if profile != nil {
		profileID = profile.ID
	}
	key := connKey{addr: addr, profileID: profileID, useTLS: useTLS}

	// Fast path: connection already exists.
	p.mu.RLock()
	conn, ok := p.conns[key]
	p.mu.RUnlock()
	if ok {
		return conn, nil
	}

	// Slow path: create a new connection (double-checked).
	p.mu.Lock()
	defer p.mu.Unlock()

	if conn, ok = p.conns[key]; ok {
		return conn, nil
	}

	opts, err := p.buildDialOptions(useTLS, profile)
	if err != nil {
		return nil, fmt.Errorf("grpc pool: build dial options for %q: %w", addr, err)
	}

	conn, err = grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("grpc pool: dial %q: %w", addr, err)
	}

	p.conns[key] = conn
	return conn, nil
}

// Evict closes and removes the connection for the given URL and profile.
// No-op if no matching connection exists.
func (p *ConnPool) Evict(rawURL string, profile *egress.EgressProfile) {
	addr, useTLS, err := parseGRPCURL(rawURL)
	if err != nil {
		return
	}

	var profileID uint8
	if profile != nil {
		profileID = profile.ID
	}
	key := connKey{addr: addr, profileID: profileID, useTLS: useTLS}

	p.mu.Lock()
	conn, ok := p.conns[key]
	if ok {
		delete(p.conns, key)
	}
	p.mu.Unlock()

	if ok {
		conn.Close() //nolint:errcheck
	}
}

// EvictByProfile closes all connections that were created with the given profileID.
// Called when an EgressProfile is updated or deleted.
func (p *ConnPool) EvictByProfile(profileID uint8) {
	p.mu.Lock()
	var toClose []*grpc.ClientConn
	for k, conn := range p.conns {
		if k.profileID == profileID {
			toClose = append(toClose, conn)
			delete(p.conns, k)
		}
	}
	p.mu.Unlock()

	for _, conn := range toClose {
		conn.Close() //nolint:errcheck
	}
}

// Close closes all connections in the pool. Call at gateway shutdown.
func (p *ConnPool) Close() {
	p.mu.Lock()
	conns := p.conns
	p.conns = make(map[connKey]*grpc.ClientConn)
	p.mu.Unlock()

	for _, conn := range conns {
		conn.Close() //nolint:errcheck
	}
}

// buildDialOptions assembles grpc.DialOption slice from the egress profile and pool defaults.
func (p *ConnPool) buildDialOptions(useTLS bool, profile *egress.EgressProfile) ([]grpc.DialOption, error) {
	var opts []grpc.DialOption

	// Transport credentials.
	if useTLS {
		var tlsCfg *tls.Config
		if profile != nil && profile.TLSConfig != nil {
			tlsCfg = profile.TLSConfig.Clone()
		} else {
			tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Keep-alive: use pool-level defaults; profile timeouts are per-call not per-conn.
	kaTime := p.KeepaliveTime
	kaTimeout := p.KeepaliveTimeout
	if kaTime > 0 {
		if kaTimeout == 0 {
			kaTimeout = 20 * time.Second // gRPC default
		}
		opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                kaTime,
			Timeout:             kaTimeout,
			PermitWithoutStream: true,
		}))
	}

	return opts, nil
}

// parseGRPCURL splits a grpc:// or grpcs:// URL into (host:port, useTLS, error).
// Plain "host:port" without a scheme is treated as grpc:// (insecure).
func parseGRPCURL(rawURL string) (addr string, useTLS bool, err error) {
	switch {
	case strings.HasPrefix(rawURL, "grpcs://"):
		return strings.TrimPrefix(rawURL, "grpcs://"), true, nil
	case strings.HasPrefix(rawURL, "grpc://"):
		return strings.TrimPrefix(rawURL, "grpc://"), false, nil
	case strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://"):
		return "", false, fmt.Errorf("grpc pool: URL %q must use grpc:// or grpcs:// scheme", rawURL)
	default:
		// Bare host:port — treat as insecure gRPC.
		return rawURL, false, nil
	}
}
