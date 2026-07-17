//go:build googletoken

package googletoken

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/api/idtoken"

	"github.com/amitkhosla/rah/internal/config"
)

// earlyRefresh is how early before expiry we treat a cached token as expired.
// Avoids serving a token that expires during the upstream call.
const earlyRefresh = 5 * time.Minute

// Provider fetches Google-signed OIDC ID tokens for a given audience.
// The token is suitable for authenticating to Cloud Run, Cloud Functions,
// or any service that validates Google-signed JWTs.
type Provider struct{}

// New creates a Google token Provider.
// Returns (nil, nil) when cfg.Enabled is false.
func New(_ context.Context, cfg config.GoogleTokenConfig) (*Provider, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	return &Provider{}, nil
}

// Scheme returns the URI scheme this provider handles.
func (p *Provider) Scheme() string { return "googletoken" }

// Resolve fetches an ID token for the audience encoded in ref.
// ref format: googletoken://https://audience-url
func (p *Provider) Resolve(ctx context.Context, ref string) ([]byte, error) {
	tok, _, err := p.fetchToken(ctx, ref)
	return tok, err
}

// ResolveTTL fetches an ID token and returns it with a TTL set to
// token expiry minus earlyRefresh â€” ensuring the cache is refreshed
// before the token becomes invalid.
func (p *Provider) ResolveTTL(ctx context.Context, ref string) ([]byte, time.Duration, error) {
	tok, expiry, err := p.fetchToken(ctx, ref)
	if err != nil {
		return nil, 0, err
	}
	ttl := time.Until(expiry) - earlyRefresh
	if ttl < time.Minute {
		ttl = time.Minute // minimum TTL to avoid tight retry loops
	}
	return tok, ttl, nil
}

func (p *Provider) fetchToken(ctx context.Context, ref string) ([]byte, time.Time, error) {
	audience := strings.TrimPrefix(ref, "googletoken://")
	if audience == "" || audience == ref {
		return nil, time.Time{}, fmt.Errorf("googletoken: invalid URI %q â€” expected googletoken://https://audience", ref)
	}

	ts, err := idtoken.NewTokenSource(ctx, audience)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("googletoken: creating token source for %q: %w", audience, err)
	}

	token, err := ts.Token()
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("googletoken: fetching token for %q: %w", audience, err)
	}

	return []byte(token.AccessToken), token.Expiry, nil
}
