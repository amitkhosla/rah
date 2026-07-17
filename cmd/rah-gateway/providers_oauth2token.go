//go:build oauth2token

package main

// providers_oauth2token.go registers the OAuth2 client credentials provider
// when the binary is built with -tags oauth2token.
//
// To enable:
//  1. go build -tags oauth2token ./cmd/rah-gateway/
//     (no extra go get needed â€” golang.org/x/oauth2 is already in go.mod)
//
// In gateway.yaml:
//
//	secrets:
//	  oauth2:
//	    clients:
//	      payment-service:
//	        token_url:     "https://auth.payment.example.com/token"
//	        client_id:     "my-client"
//	        client_secret: "env:PAYMENT_SECRET"
//	        scopes:        ["read", "write"]

import (
	"context"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/secrets"
	"github.com/amitkhosla/rah/internal/secrets/oauth2token"
)

func init() {
	secrets.RegisterProviderFactory("oauth2token",
		func(ctx context.Context, cfg config.SecretsConfig, bootstrap secrets.Resolver) (secrets.Provider, error) {
			p, err := oauth2token.New(ctx, cfg.OAuth2, bootstrap)
			if err != nil || p == nil {
				return nil, err
			}
			return p, nil
		},
	)
}
