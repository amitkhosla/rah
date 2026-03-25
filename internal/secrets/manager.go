package secrets

import (
	"context"
	"fmt"
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
// ctx is used to cancel the background cache-rotation goroutine.
//
// Providers registered in priority order:
//  1. env  — always available (no config needed)
//  2. file — always available (no config needed)
//  3. enc  — when cfg.Encrypted.Enabled
//  4. gsm  — when cfg.GSM.Enabled
//  5. vault — when cfg.Vault.Enabled
//  6. awssm — when cfg.AWSSM.Enabled
func New(ctx context.Context, cfg config.SecretsConfig) (*Manager, error) {
	m := &Manager{
		providers: make(map[string]Provider),
		cache:     newSecretCache(),
		cacheTTL:  defaultCacheTTL,
	}

	// Bootstrap providers — always registered, no external deps.
	m.register(newEnvProvider())
	m.register(newFileProvider())

	// Encrypted provider — requires a master key resolved via env/file.
	if cfg.Encrypted.Enabled {
		p, err := newEncryptedProvider(ctx, cfg.Encrypted, m)
		if err != nil {
			return nil, fmt.Errorf("secrets: encrypted provider: %w", err)
		}
		m.register(p)
	}

	// Cloud providers — stubs until SDK deps are added.
	if cfg.GSM.Enabled {
		p, err := newGSMProvider(ctx, cfg.GSM, m)
		if err != nil {
			return nil, fmt.Errorf("secrets: gsm provider: %w", err)
		}
		m.register(p)
	}
	if cfg.Vault.Enabled {
		p, err := newVaultProvider(ctx, cfg.Vault, m)
		if err != nil {
			return nil, fmt.Errorf("secrets: vault provider: %w", err)
		}
		m.register(p)
	}
	if cfg.AWSSM.Enabled {
		p, err := newAWSSMProvider(ctx, cfg.AWSSM, m)
		if err != nil {
			return nil, fmt.Errorf("secrets: aws_sm provider: %w", err)
		}
		m.register(p)
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

		// Literal — no provider needed.
		if scheme == "" {
			return []byte(ref), nil
		}

		p, ok := m.providers[scheme]
		if !ok {
			return nil, fmt.Errorf("secrets: no provider registered for scheme %q in ref %q", scheme, ref)
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

// Close zeroes all cached secrets and stops background goroutines.
func (m *Manager) Close() {
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
