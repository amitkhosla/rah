package egress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"strings"

	"github.com/amitkhosla/rah/internal/config"
)

// SecretsResolver resolves secret references (e.g. "ref:secrets/certs/ca") to their values.
// config.CredentialResolver satisfies this interface.
type SecretsResolver interface {
	ResolveString(ctx context.Context, ref string) (string, error)
}

// secretRefPrefixes lists the prefixes that identify a secrets manager reference.
// Entries in TLSCACerts starting with one of these are resolved via SecretsResolver.
var secretRefPrefixes = []string{"ref:", "env:", "file:", "secret:"}

// isSecretRef reports whether entry should be resolved via a SecretsResolver rather
// than treated as a literal PEM string.
func isSecretRef(entry string) bool {
	if !strings.ContainsAny(entry, ":") {
		return false
	}
	for _, pfx := range secretRefPrefixes {
		if strings.HasPrefix(entry, pfx) {
			return true
		}
	}
	return false
}

// BuildTLSConfig constructs a *tls.Config from an EgressProfileConfig.
//
// TLSCACerts: each entry is either a PEM string or a secrets ref ("ref:...", "env:...",
// "file:...", or "secret:..."). All resolved PEM blocks are appended to a single
// x509.CertPool that starts as a copy of the system pool, enabling multi-cert rotation
// (old and new cert trusted simultaneously during changeover with no connection drop).
//
// Returns nil, nil when no custom TLS config is needed (no certs, no skip-verify),
// signalling that the system CA pool should be used as-is.
// Returns an error if any cert ref cannot be resolved or any PEM block fails to parse.
func BuildTLSConfig(ctx context.Context, cfg config.EgressProfileConfig, secrets SecretsResolver) (*tls.Config, error) {
	if len(cfg.TLSCACerts) == 0 && !cfg.TLSSkipVerify {
		return nil, nil
	}

	// Start from the system pool so custom CAs are additive, not replacing.
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}

	for _, entry := range cfg.TLSCACerts {
		pemStr := entry

		if isSecretRef(entry) {
			if secrets == nil {
				return nil, fmt.Errorf("egress: cert ref %q requires a secrets resolver (nil)", entry)
			}
			resolved, err := secrets.ResolveString(ctx, entry)
			if err != nil {
				return nil, fmt.Errorf("egress: resolve cert ref %q for profile %q: %w", entry, cfg.Name, err)
			}
			pemStr = resolved
		}

		// Parse all PEM blocks in the resolved/literal string — supports bundles.
		rest := []byte(pemStr)
		for {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			if block.Type != "CERTIFICATE" {
				continue
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("egress: parse cert %q: %w", cfg.Name, err)
			}
			pool.AddCert(cert)
		}
	}

	if cfg.TLSSkipVerify {
		log.Printf("[egress] WARNING: TLS verification disabled for profile %q — dev/test only", cfg.Name)
	}

	tlsCfg := &tls.Config{
		RootCAs:            pool,
		InsecureSkipVerify: cfg.TLSSkipVerify, //nolint:gosec // intentional, caller opted in
	}

	return tlsCfg, nil
}

// ApplyTLSConfig resolves TLS config from cfg and sets it on profile.TLSConfig.
// No-op if BuildTLSConfig returns nil (system pool).
func ApplyTLSConfig(ctx context.Context, profile *EgressProfile, cfg config.EgressProfileConfig, secrets SecretsResolver) error {
	tlsCfg, err := BuildTLSConfig(ctx, cfg, secrets)
	if err != nil {
		return err
	}
	profile.TLSConfig = tlsCfg
	return nil
}
