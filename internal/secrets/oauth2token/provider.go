//go:build oauth2token

package oauth2token

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/oauth2/clientcredentials"

	"github.com/amitkhosla/rah/internal/config"
)

// earlyRefresh is how early before expiry we treat a cached token as stale.
const earlyRefresh = time.Minute

// BootstrapResolver resolves env: and file:// credential references.
// Satisfied structurally by *secrets.Manager.
type BootstrapResolver interface {
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// Provider fetches OAuth2 access tokens using the client credentials flow.
type Provider struct {
	// clients maps client name â†’ cached token source config.
	clients map[string]*clientcredentials.Config
}

// New creates an OAuth2 token Provider from config.
// Returns (nil, nil) when no clients are configured.
func New(ctx context.Context, cfg config.OAuth2Config, bootstrap BootstrapResolver) (*Provider, error) {
	if len(cfg.Clients) == 0 {
		return nil, nil
	}

	clients := make(map[string]*clientcredentials.Config, len(cfg.Clients))
	for name, c := range cfg.Clients {
		if c.TokenURL == "" {
			return nil, fmt.Errorf("oauth2token: client %q: token_url is required", name)
		}
		if c.ClientID == "" {
			return nil, fmt.Errorf("oauth2token: client %q: client_id is required", name)
		}
		clientSecret, err := resolveString(ctx, c.ClientSecret, bootstrap)
		if err != nil {
			return nil, fmt.Errorf("oauth2token: client %q: resolving client_secret: %w", name, err)
		}
		clients[name] = &clientcredentials.Config{
			ClientID:     c.ClientID,
			ClientSecret: clientSecret,
			TokenURL:     c.TokenURL,
			Scopes:       c.Scopes,
		}
	}

	return &Provider{clients: clients}, nil
}

// Scheme returns the URI scheme this provider handles.
func (p *Provider) Scheme() string { return "oauth2token" }

// Resolve fetches an access token for the client named in ref.
// ref format: oauth2token://client-name
func (p *Provider) Resolve(ctx context.Context, ref string) ([]byte, error) {
	tok, _, err := p.fetchToken(ctx, ref)
	return tok, err
}

// ResolveTTL fetches an access token and returns it with a TTL derived from
// the token's expiry so the Manager caches it appropriately.
func (p *Provider) ResolveTTL(ctx context.Context, ref string) ([]byte, time.Duration, error) {
	tok, expiry, err := p.fetchToken(ctx, ref)
	if err != nil {
		return nil, 0, err
	}
	if expiry.IsZero() {
		// Server returned no expiry — use zero to let Manager apply its default TTL.
		return tok, 0, nil
	}
	ttl := time.Until(expiry) - earlyRefresh
	if ttl < time.Minute {
		ttl = time.Minute
	}
	return tok, ttl, nil
}

func (p *Provider) fetchToken(ctx context.Context, ref string) ([]byte, time.Time, error) {
	name := strings.TrimPrefix(ref, "oauth2token://")
	if name == "" || name == ref {
		return nil, time.Time{}, fmt.Errorf("oauth2token: invalid URI %q — expected oauth2token://client-name", ref)
	}

	ccfg, ok := p.clients[name]
	if !ok {
		return nil, time.Time{}, fmt.Errorf("oauth2token: client %q not found in secrets.oauth2.clients config", name)
	}

	token, err := ccfg.Token(ctx)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("oauth2token: fetching token for client %q: %w", name, err)
	}

	return []byte(token.AccessToken), token.Expiry, nil
}

func resolveString(ctx context.Context, val string, bootstrap BootstrapResolver) (string, error) {
	if val == "" {
		return "", nil
	}
	raw, err := bootstrap.Resolve(ctx, val)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
