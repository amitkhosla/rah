// Package vault implements a secrets.Provider that resolves credentials from
// HashiCorp Vault KV v2.
//
// # Build tag
//
// This package requires the HashiCorp Vault Go client. Build with -tags vault:
//
//	go get github.com/hashicorp/vault/api@latest
//	go build -tags vault ./cmd/rah-gateway/
//
// # Registration
//
// The provider registers itself automatically via init() when the package is
// imported. The pre-wired adapter is in cmd/rah-gateway/providers_vault.go.
//
// # URI format
//
//	vault://mount/secret/path          — returns all fields as JSON
//	vault://mount/secret/path#field    — returns the value of a single field
//
// Examples:
//
//	vault://secret/database/prod#password
//	vault://kv/myapp/api-keys#stripe_key
//
// # Authentication
//
// Configured via secrets.vault in gateway.yaml:
//
//	secrets:
//	  vault:
//	    enabled: true
//	    addr: "https://vault.internal:8200"
//	    auth: token       # dev/test only
//	    token: "env:VAULT_TOKEN"
//
//	  # AppRole (production):
//	    auth: approle
//	    role_id:   "env:VAULT_ROLE_ID"
//	    secret_id: "env:VAULT_SECRET_ID"
//
//	  # Kubernetes (GKE, EKS, AKS):
//	    auth: kubernetes
//	    role: "my-vault-role"
package vault
