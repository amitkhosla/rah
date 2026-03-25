//go:build gsm

package gsm

import (
	"context"
	"fmt"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/option"

	"rah/internal/config"
)

// BootstrapResolver resolves env: and file:// credential references.
// Defined here to avoid importing internal/secrets (which would be circular).
// Satisfied structurally by *secrets.Manager.
type BootstrapResolver interface {
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// Provider resolves "gsm://..." references via the Google Secret Manager API.
type Provider struct {
	client  *secretmanager.Client
	project string // default GCP project — used for short-form URIs
}

// New creates a GSM Provider from config.
// bootstrap resolves cfg.CredentialsFile (must be env: or file:// only).
// Returns (nil, nil) when cfg.Enabled is false — caller skips registration.
func New(ctx context.Context, cfg config.GSMConfig, bootstrap BootstrapResolver) (*Provider, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	var opts []option.ClientOption

	if cfg.CredentialsFile != "" {
		// Resolve the credentials file path/value via bootstrap (env/file only).
		keyJSON, err := bootstrap.Resolve(ctx, cfg.CredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("gsm: resolving credentials_file %q: %w", cfg.CredentialsFile, err)
		}
		opts = append(opts, option.WithCredentialsJSON(keyJSON))
		clear(keyJSON) // zero after handing to the SDK
	}
	// No opts → SDK uses ADC (Workload Identity, GOOGLE_APPLICATION_CREDENTIALS,
	// gcloud user credentials — whichever is available in the environment).

	client, err := secretmanager.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("gsm: creating client: %w", err)
	}

	return &Provider{client: client, project: cfg.Project}, nil
}

// Scheme returns the URI scheme this provider handles.
func (p *Provider) Scheme() string { return "gsm" }

// Resolve fetches the secret version payload from Google Secret Manager.
//
// Supported URI formats:
//
//	gsm://projects/my-project/secrets/my-secret/versions/latest
//	gsm://projects/my-project/secrets/my-secret          → /versions/latest
//	gsm://my-secret                                       → uses default project
//	gsm://my-secret/versions/3                            → uses default project
func (p *Provider) Resolve(ctx context.Context, ref string) ([]byte, error) {
	name, err := resourceName(ref, p.project)
	if err != nil {
		return nil, err
	}

	result, err := p.client.AccessSecretVersion(ctx,
		&secretmanagerpb.AccessSecretVersionRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("gsm: accessing %q: %w", name, err)
	}

	// Copy the payload — the SDK may reuse the underlying buffer.
	payload := result.Payload.GetData()
	out := make([]byte, len(payload))
	copy(out, payload)
	return out, nil
}

// Close shuts down the underlying gRPC connection.
func (p *Provider) Close() error {
	return p.client.Close()
}

// resourceName converts a gsm:// URI to the canonical GSM resource name
// expected by the API: projects/{project}/secrets/{secret}/versions/{version}.
//
//	"gsm://projects/p/secrets/s/versions/latest" → "projects/p/secrets/s/versions/latest"
//	"gsm://projects/p/secrets/s"                 → "projects/p/secrets/s/versions/latest"
//	"gsm://my-secret"                            → "projects/{default}/secrets/my-secret/versions/latest"
//	"gsm://my-secret/versions/3"                 → "projects/{default}/secrets/my-secret/versions/3"
func resourceName(ref, defaultProject string) (string, error) {
	path := strings.TrimPrefix(ref, "gsm://")

	if strings.HasPrefix(path, "projects/") {
		// Full form — already a valid resource name prefix.
		if !strings.Contains(path, "/versions/") {
			path += "/versions/latest"
		}
		return path, nil
	}

	// Short form — requires a default project from config.
	if defaultProject == "" {
		return "", fmt.Errorf("gsm: ref %q uses short form but no default project is "+
			"configured — set secrets.gsm.project in gateway config", ref)
	}

	if strings.Contains(path, "/versions/") {
		// e.g. "my-secret/versions/3"
		return fmt.Sprintf("projects/%s/secrets/%s", defaultProject, path), nil
	}

	// e.g. "my-secret"
	return fmt.Sprintf("projects/%s/secrets/%s/versions/latest", defaultProject, path), nil
}
