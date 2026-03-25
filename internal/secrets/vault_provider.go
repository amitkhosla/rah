package secrets

import (
	"context"
	"fmt"

	"rah/internal/config"
)

// vaultProvider resolves "vault://mount/path#field" references via the
// HashiCorp Vault KV secrets engine.
//
// # URI format
//
//	vault://secret/data/redis#password     (KV v2)
//	vault://kv/myapp/config#api_key        (KV v1)
//
// The fragment (#field) selects a key from the secret's data map.
// If omitted the entire JSON payload is returned.
//
// # Authentication
//
// Configured via cfg.Auth:
//   - "kubernetes": reads the pod's service-account token automatically.
//     Requires cfg.Role to be set.
//   - "approle":  cfg.RoleID + cfg.SecretID (both accept env: references).
//   - "token":    cfg.Token (accepts env: references).
//
// # Enabling this provider
//
// Add the SDK dependency:
//
//	go get github.com/hashicorp/vault-client-go
//
// Then implement the Resolve() body using the Vault client.
type vaultProvider struct {
	addr string
	auth string
	role string
}

func newVaultProvider(_ context.Context, cfg config.VaultConfig, bootstrap Resolver) (*vaultProvider, error) {
	addr := cfg.Addr
	if addr == "" {
		addr = "http://127.0.0.1:8200"
	}
	// Resolve addr in case it's an "env:" reference.
	if resolved, err := bootstrap.Resolve(context.Background(), addr); err == nil {
		addr = string(resolved)
		clear(resolved)
	}
	// TODO: initialise the Vault client and authenticate here.
	return &vaultProvider{addr: addr, auth: cfg.Auth, role: cfg.Role}, nil
}

func (p *vaultProvider) Scheme() string { return "vault" }

func (p *vaultProvider) Resolve(_ context.Context, ref string) ([]byte, error) {
	// TODO: implement using the HashiCorp Vault client library.
	//
	//   import vault "github.com/hashicorp/vault-client-go"
	//
	//   mount, path, field := parseVaultRef(ref)
	//   secret, err := p.client.Secrets.KvV2Read(ctx, path, vault.WithMountPath(mount))
	//   return []byte(secret.Data.Data[field].(string)), nil
	//
	return nil, fmt.Errorf("secrets/vault: provider not yet implemented — add github.com/hashicorp/vault-client-go dep and implement Resolve() in vault_provider.go (ref: %q)", ref)
}
