//go:build vault

package main

// providers_vault.go registers the HashiCorp Vault provider when the
// binary is built with -tags vault.
//
// To enable:
//  1. go get github.com/hashicorp/vault/api@latest
//  2. go build -tags vault ./cmd/rah-gateway/
//
// In gateway.yaml:
//
//	secrets:
//	  vault:
//	    enabled: true
//	    addr: "https://vault.internal:8200"
//	    auth: approle
//	    role_id:   "env:VAULT_ROLE_ID"
//	    secret_id: "env:VAULT_SECRET_ID"

import (
	"context"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/secrets"
	"github.com/amitkhosla/rah/internal/secrets/vault"
)

func init() {
	secrets.RegisterProviderFactory("vault",
		func(ctx context.Context, cfg config.SecretsConfig, bootstrap secrets.Resolver) (secrets.Provider, error) {
			p, err := vault.New(ctx, cfg.Vault, bootstrap)
			if err != nil || p == nil {
				return nil, err
			}
			return p, nil
		},
	)
}
