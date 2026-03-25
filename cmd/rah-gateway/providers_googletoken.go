//go:build googletoken

package main

// providers_googletoken.go registers the Google ID Token provider when the
// binary is built with -tags googletoken.
//
// To enable:
//  1. go build -tags googletoken ./cmd/rah-gateway/
//     (no extra go get needed — google.golang.org/api is already in go.mod)
//
// In gateway.yaml:
//
//	secrets:
//	  google_token:
//	    enabled: true

import (
	"context"

	"rah/internal/config"
	"rah/internal/secrets"
	"rah/internal/secrets/googletoken"
)

func init() {
	secrets.RegisterProviderFactory("googletoken",
		func(ctx context.Context, cfg config.SecretsConfig, _ secrets.Resolver) (secrets.Provider, error) {
			p, err := googletoken.New(ctx, cfg.GoogleToken)
			if err != nil || p == nil {
				return nil, err
			}
			return p, nil
		},
	)
}
