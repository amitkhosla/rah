// Package secrets provides pluggable credential resolution for the RAH gateway.
//
// # Reference format
//
// Any string credential field can contain one of these URI schemes:
//
//	env:VAR_NAME          — read from environment variable (also: $VAR, ${VAR})
//	file:///path/to/file  — read file contents, whitespace trimmed
//	enc:base64value       — AES-256-GCM encrypted value (requires EncryptedConfig.Key)
//	gsm://project/secrets/name/versions/latest — Google Secret Manager
//	vault://mount/path#field                   — HashiCorp Vault KV
//	awssm://region/secret-name#field           — AWS Secrets Manager
//	<anything else>       — returned as-is (literal plaintext, dev only)
//
// # Adding a new provider
//
// 1. Create internal/secrets/<name>/provider.go implementing Provider.
// 2. Create cmd/rah-gateway/providers_<name>.go with a build tag and an
//    init() that calls secrets.RegisterProviderFactory("<scheme>", ...).
// 3. Run go get for the provider's SDK dep.
// 4. Build with -tags <name> to include it.
//
// # Secure memory
//
// All resolved values are stored and returned as []byte so callers can zero
// them when done. The internal cache also zeroes evicted entries.
// Callers that must pass to string-based APIs (e.g. go-redis Options) should
// convert at the last possible moment: string(secret).
package secrets

import (
	"context"
	"sync"
	"time"

	"rah/internal/config"
)

// Provider resolves secret references for a specific URI scheme.
// Each provider handles exactly one scheme prefix.
type Provider interface {
	// Scheme returns the URI prefix this provider handles, without the colon
	// or slashes (e.g. "gsm", "vault", "env", "file", "enc").
	Scheme() string

	// Resolve fetches and returns the plaintext secret for ref.
	// ref is the full original reference string.
	// The caller owns the returned []byte and should zero it when done.
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// Resolver is the public interface exposed to other packages.
// Implemented by Manager.
type Resolver interface {
	// Resolve returns the plaintext secret for a credential reference.
	// Literal values (no recognised scheme prefix) are returned as-is.
	// Results are served from the in-memory cache after the first fetch.
	// The returned []byte is a fresh copy — the caller may zero it freely.
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// TTLProvider is an optional extension of Provider for values whose TTL is
// known at resolution time (e.g. OAuth2 tokens that include an expiry in the
// response). When the Manager resolves a ref via a TTLProvider it uses the
// returned TTL to cache the value instead of the default 30-minute TTL.
// This allows short-lived tokens to be refreshed before they expire.
type TTLProvider interface {
	Provider
	ResolveTTL(ctx context.Context, ref string) ([]byte, time.Duration, error)
}

// ProviderFactory creates a Provider from the gateway secrets config.
//
// ctx is the gateway root context (passed to providers that need background
// goroutines, e.g. token refresh).
//
// cfg is the full SecretsConfig — the factory reads only the section it owns.
//
// bootstrap is the Manager itself, pre-loaded with env and file providers.
// Use it to resolve env: / file:// refs for provider credentials (e.g. a
// service-account key path). Never pass a cloud provider ref to bootstrap —
// that would be circular.
//
// Return (nil, nil) when the provider is not enabled in cfg — the Manager
// will skip registration silently. Return a non-nil error only for
// mis-configuration that should abort startup.
type ProviderFactory func(ctx context.Context, cfg config.SecretsConfig, bootstrap Resolver) (Provider, error)

// global factory registry — populated by init() functions in provider sub-packages.
var (
	globalFactories   = map[string]ProviderFactory{}
	globalFactoriesMu sync.Mutex
)

// RegisterProviderFactory registers a ProviderFactory for the given scheme.
// Call this from an init() function in a provider sub-package so that the
// provider is available to all Manager instances created in the same binary.
//
// Registering the same scheme twice overwrites the previous factory.
// Safe to call from multiple init() goroutines (init() is sequential, but
// the mutex guards against any edge cases in test binaries).
func RegisterProviderFactory(scheme string, f ProviderFactory) {
	globalFactoriesMu.Lock()
	globalFactories[scheme] = f
	globalFactoriesMu.Unlock()
}
