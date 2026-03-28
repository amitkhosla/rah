package secrets

import (
	"context"
	"fmt"
	"strings"
)

// ErrNotHandled is returned by EncryptedValueProvider.Resolve when the ref is
// not in the versioned "enc:<version>:<base64>" format and should be handled
// by a different provider in the resolver chain.
var ErrNotHandled = fmt.Errorf("secrets/encvalue: ref not handled by this provider")

// EncryptedValueProvider is a Resolver that decrypts credential values
// encrypted by an Encryptor (format "enc:<version>:<base64>").
//
// It handles ONLY refs that start with "enc:" AND contain a version segment,
// e.g. "enc:k1:AAABBB...". Old-format refs "enc:<base64>" (no second colon
// in the payload) are NOT claimed by this provider — they belong to the
// existing encryptedProvider registered in the secrets Manager.
//
// This provider is used by CredentialRegistry.Resolve to transparently
// decrypt values stored via the per-tenant credential API at runtime.
type EncryptedValueProvider struct {
	enc Encryptor
}

// NewEncryptedValueProvider creates an EncryptedValueProvider backed by enc.
func NewEncryptedValueProvider(enc Encryptor) *EncryptedValueProvider {
	return &EncryptedValueProvider{enc: enc}
}

// isVersionedEncRef reports whether ref is a versioned encrypted reference.
// Versioned refs have the form "enc:<version>:<base64>" — i.e. "enc:" prefix
// and the remainder contains at least one more colon (the version separator).
//
// Old-format refs are "enc:<base64>" — after stripping "enc:" the remainder
// is pure base64 which contains no colon, so they are rejected here.
func isVersionedEncRef(ref string) bool {
	payload, ok := strings.CutPrefix(ref, "enc:")
	if !ok {
		return false
	}
	// A versioned ref has the form "<version>:<base64>", so at least one colon.
	return strings.Contains(payload, ":")
}

// Resolve decrypts a versioned encrypted ref of the form "enc:<version>:<base64>".
// Returns ErrNotHandled for refs that are not in the versioned format so that
// callers can fall back to the next provider in the chain.
func (p *EncryptedValueProvider) Resolve(_ context.Context, ref string) ([]byte, error) {
	if !isVersionedEncRef(ref) {
		return nil, ErrNotHandled
	}
	plaintext, err := p.enc.Decrypt(ref)
	if err != nil {
		return nil, fmt.Errorf("secrets/encvalue: decrypt %q: %w", ref, err)
	}
	return plaintext, nil
}
