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
// # Secure memory
//
// All resolved values are stored and returned as []byte so callers can zero
// them when done. The internal cache also zeroes evicted entries.
// Callers that must pass to string-based APIs (e.g. go-redis Options) should
// convert at the last possible moment: string(secret).
package secrets

import "context"

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
