package secrets

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
	"rah/internal/config"
)

const (
	// defaultCacheTTL is how long a resolved secret is cached before re-fetch.
	defaultCacheTTL = 30 * time.Minute
	// rotationInterval is how often the background goroutine evicts expired entries.
	rotationInterval = 5 * time.Minute
)

// Manager resolves credential references using a chain of pluggable providers.
// Results are cached in memory with zeroing on eviction.
// Safe for concurrent use.
type Manager struct {
	providers map[string]Provider // scheme → provider
	cache     *secretCache
	sf        singleflight.Group // coalesces concurrent resolutions of the same ref
	cacheTTL  time.Duration
}

// New builds a Manager from the gateway secrets config.
// ctx is used to cancel the background cache-rotation goroutine and is
// forwarded to all provider factories (e.g. for token-refresh goroutines).
//
// Built-in providers (always available, no config required):
//   - env  — "env:VAR", "$VAR", "${VAR}"
//   - file — "file:///path"
//
// Built-in providers (require config):
//   - enc  — "enc:base64" — needs cfg.Encrypted.Enabled + Key
//
// Pluggable providers (activated by blank imports in main.go):
//   - gsm, vault, awssm, … — registered via RegisterProviderFactory in init()
func New(ctx context.Context, cfg config.SecretsConfig) (*Manager, error) {
	m := &Manager{
		providers: make(map[string]Provider),
		cache:     newSecretCache(),
		cacheTTL:  defaultCacheTTL,
	}

	// Bootstrap providers — always registered, no external deps.
	// Must be registered first so the encrypted provider and cloud provider
	// factories can use them to resolve their own bootstrap credentials.
	m.register(newEnvProvider())
	m.register(newFileProvider())

	// Encrypted provider — built in, needs master key config.
	if cfg.Encrypted.Enabled {
		p, err := newEncryptedProvider(ctx, cfg.Encrypted, m)
		if err != nil {
			return nil, fmt.Errorf("secrets: encrypted provider: %w", err)
		}
		m.register(p)
	}

	// Pluggable cloud providers — registered via init() in sub-packages.
	// Copy the global map under lock so concurrent test binaries are safe.
	globalFactoriesMu.Lock()
	factories := make(map[string]ProviderFactory, len(globalFactories))
	for k, v := range globalFactories {
		factories[k] = v
	}
	globalFactoriesMu.Unlock()

	for scheme, factory := range factories {
		p, err := factory(ctx, cfg, m)
		if err != nil {
			return nil, fmt.Errorf("secrets: provider %q: %w", scheme, err)
		}
		if p != nil {
			m.register(p)
		}
		// nil return means "not enabled in config" — silently skip.
	}

	go m.rotationLoop(ctx)
	return m, nil
}

func (m *Manager) register(p Provider) {
	m.providers[p.Scheme()] = p
}

// Resolve returns the plaintext secret for ref.
// Literal values (no recognised scheme) are returned as-is.
func (m *Manager) Resolve(ctx context.Context, ref string) ([]byte, error) {
	// Fast path: cache hit.
	if val, ok := m.cache.get(ref); ok {
		return val, nil
	}

	// Slow path: resolve via provider, coalescing concurrent calls for the same ref.
	v, err, _ := m.sf.Do(ref, func() (any, error) {
		scheme := parseScheme(ref)

		// Literal — no provider needed, return as-is.
		if scheme == "" {
			return []byte(ref), nil
		}

		p, ok := m.providers[scheme]
		if !ok {
			return nil, fmt.Errorf("secrets: no provider registered for scheme %q in ref %q — "+
				"is the provider sub-package imported in main.go?", scheme, ref)
		}

		// If the provider knows its token TTL (e.g. OAuth2, Google ID token),
		// use the returned TTL so short-lived tokens are refreshed before expiry.
		if tp, ok := p.(TTLProvider); ok {
			val, ttl, err := tp.ResolveTTL(ctx, ref)
			if err != nil {
				return nil, err
			}
			if ttl <= 0 {
				ttl = m.cacheTTL
			}
			m.cache.set(ref, val, ttl)
			return val, nil
		}

		val, err := p.Resolve(ctx, ref)
		if err != nil {
			return nil, err
		}
		m.cache.set(ref, val, m.cacheTTL)
		return val, nil
	})
	if err != nil {
		return nil, err
	}

	// singleflight returns the same slice to all waiters — return a copy so
	// each caller can zero their own copy independently.
	raw := v.([]byte)
	out := make([]byte, len(raw))
	copy(out, raw)
	return out, nil
}

// ResolveString is a convenience wrapper for APIs that require a string.
// The caller is responsible for minimising the lifetime of the returned string.
func (m *Manager) ResolveString(ctx context.Context, ref string) (string, error) {
	b, err := m.Resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	s := string(b)
	clear(b) // zero our copy immediately after conversion
	return s, nil
}

// Close zeroes all cached secrets, closes provider clients, and stops the
// background rotation goroutine (via the context passed to New).
func (m *Manager) Close() {
	for _, p := range m.providers {
		if c, ok := p.(io.Closer); ok {
			_ = c.Close()
		}
	}
	m.cache.purge()
}

// rotationLoop periodically evicts expired cache entries.
func (m *Manager) rotationLoop(ctx context.Context) {
	t := time.NewTicker(rotationInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.cache.purge()
			return
		case <-t.C:
			m.cache.evictExpired()
		}
	}
}

// parseScheme extracts the scheme prefix from a ref string.
// Returns "" for literals (no recognised prefix).
//
//	"env:FOO"      → "env"
//	"$FOO"         → "env"   (legacy shell syntax)
//	"${FOO}"       → "env"
//	"file:///path" → "file"
//	"enc:base64"   → "enc"
//	"gsm://..."    → "gsm"
//	"vault://..."  → "vault"
//	"awssm://..."  → "awssm"
//	"plaintext"    → ""
func parseScheme(ref string) string {
	if strings.HasPrefix(ref, "$") {
		return "env"
	}
	if i := strings.Index(ref, ":"); i > 0 {
		return strings.ToLower(ref[:i])
	}
	return ""
}
