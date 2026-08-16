//go:build vault

package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/amitkhosla/rah/internal/config"
)

// BootstrapResolver resolves env: and file:// credential references.
// Satisfied structurally by *secrets.Manager.
type BootstrapResolver interface {
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// Provider resolves vault:// references via HashiCorp Vault KV v2.
type Provider struct {
	client *vaultapi.Client
}

// New creates a Vault Provider from config.
// Returns (nil, nil) when cfg.Enabled is false.
func New(ctx context.Context, cfg config.VaultConfig, bootstrap BootstrapResolver) (*Provider, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	addr := cfg.Addr
	if addr == "" {
		addr = "http://127.0.0.1:8200"
	}
	if isRef(addr) {
		raw, err := bootstrap.Resolve(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("vault: resolving addr: %w", err)
		}
		addr = strings.TrimSpace(string(raw))
	}

	vcfg := vaultapi.DefaultConfig()
	vcfg.Address = addr
	client, err := vaultapi.NewClient(vcfg)
	if err != nil {
		return nil, fmt.Errorf("vault: creating client: %w", err)
	}

	switch cfg.Auth {
	case "token", "":
		token, err := resolveString(ctx, cfg.Token, bootstrap)
		if err != nil {
			return nil, fmt.Errorf("vault: resolving token: %w", err)
		}
		if token == "" {
			token = os.Getenv("VAULT_TOKEN")
		}
		client.SetToken(token)

	case "approle":
		roleID, err := resolveString(ctx, cfg.RoleID, bootstrap)
		if err != nil {
			return nil, fmt.Errorf("vault: resolving role_id: %w", err)
		}
		secretID, err := resolveString(ctx, cfg.SecretID, bootstrap)
		if err != nil {
			return nil, fmt.Errorf("vault: resolving secret_id: %w", err)
		}
		secret, err := client.Logical().WriteWithContext(ctx, "auth/approle/login",
			map[string]any{"role_id": roleID, "secret_id": secretID})
		if err != nil {
			return nil, fmt.Errorf("vault: approle login: %w", err)
		}
		if secret == nil || secret.Auth == nil {
			return nil, fmt.Errorf("vault: approle login: no auth token returned")
		}
		client.SetToken(secret.Auth.ClientToken)

	case "kubernetes":
		// JWT is read from the default K8s service-account path.
		const jwtPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
		jwtBytes, err := os.ReadFile(jwtPath)
		if err != nil {
			return nil, fmt.Errorf("vault: reading k8s JWT from %s: %w", jwtPath, err)
		}
		role := cfg.Role
		if role == "" {
			return nil, fmt.Errorf("vault: auth=kubernetes requires role to be set")
		}
		secret, err := client.Logical().WriteWithContext(ctx, "auth/kubernetes/login",
			map[string]any{"role": role, "jwt": strings.TrimSpace(string(jwtBytes))})
		if err != nil {
			return nil, fmt.Errorf("vault: kubernetes login: %w", err)
		}
		if secret == nil || secret.Auth == nil {
			return nil, fmt.Errorf("vault: kubernetes login: no auth token returned")
		}
		client.SetToken(secret.Auth.ClientToken)

	default:
		return nil, fmt.Errorf("vault: unknown auth method %q (supported: token, approle, kubernetes)", cfg.Auth)
	}

	return &Provider{client: client}, nil
}

// Scheme returns the URI scheme this provider handles.
func (p *Provider) Scheme() string { return "vault" }

// Resolve fetches a secret from Vault KV v2.
//
// URI format:
//
//	vault://mount/path          — returns all fields marshalled as JSON
//	vault://mount/path#field    — returns the value of a single field
func (p *Provider) Resolve(ctx context.Context, ref string) ([]byte, error) {
	mount, secretPath, field, err := parseURI(ref)
	if err != nil {
		return nil, err
	}

	secret, err := p.client.KVv2(mount).Get(ctx, secretPath)
	if err != nil {
		return nil, fmt.Errorf("vault: reading %q: %w", ref, err)
	}
	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("vault: %q returned no data", ref)
	}

	if field == "" {
		b, err := json.Marshal(secret.Data)
		if err != nil {
			return nil, fmt.Errorf("vault: marshalling %q: %w", ref, err)
		}
		return b, nil
	}

	val, ok := secret.Data[field]
	if !ok {
		return nil, fmt.Errorf("vault: field %q not found in %q", field, ref)
	}
	switch v := val.(type) {
	case string:
		return []byte(v), nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("vault: marshalling field %q: %w", field, err)
		}
		return b, nil
	}
}

// parseURI parses vault://mount/path#field.
func parseURI(ref string) (mount, path, field string, err error) {
	s := strings.TrimPrefix(ref, "vault://")
	if idx := strings.LastIndex(s, "#"); idx >= 0 {
		field = s[idx+1:]
		s = s[:idx]
	}
	if idx := strings.Index(s, "/"); idx > 0 {
		mount = s[:idx]
		path = s[idx+1:]
	} else {
		return "", "", "", fmt.Errorf("vault: invalid URI %q — expected vault://mount/path[#field]", ref)
	}
	if path == "" {
		return "", "", "", fmt.Errorf("vault: invalid URI %q — secret path is empty", ref)
	}
	return mount, path, field, nil
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

func isRef(s string) bool {
	return strings.HasPrefix(s, "env:") || strings.HasPrefix(s, "file:") || strings.HasPrefix(s, "$")
}
