//go:build gsm

package main

// providers_gsm.go registers the Google Secret Manager provider when the
// binary is built with -tags gsm.
//
// To enable:
//  1. go get cloud.google.com/go/secretmanager@latest
//  2. go build -tags gsm ./cmd/rah-gateway/
//
// In gateway.yaml:
//
//	secrets:
//	  gsm:
//	    enabled: true
//	    project: "my-gcp-project"          # optional default project
//	    credentials_file: ""               # leave empty for ADC (recommended)

import (
	"context"

	"rah/internal/config"
	"rah/internal/secrets"
	"rah/internal/secrets/gsm"
)

func init() {
	secrets.RegisterProviderFactory("gsm",
		func(ctx context.Context, cfg config.SecretsConfig, bootstrap secrets.Resolver) (secrets.Provider, error) {
			p, err := gsm.New(ctx, cfg.GSM, bootstrap)
			if err != nil || p == nil {
				// nil, nil → not enabled; nil, err → misconfiguration.
				// In both cases return a nil interface (not a nil *gsm.Provider
				// boxed in an interface — that would panic on method calls).
				return nil, err
			}
			return p, nil
		},
	)
}
