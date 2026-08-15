//go:build awssm

package awssm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/amitkhosla/rah/internal/config"
)

// BootstrapResolver resolves env: and file:// credential references.
// Satisfied structurally by *secrets.Manager.
type BootstrapResolver interface {
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// Provider resolves awssm:// references via AWS Secrets Manager.
type Provider struct {
	client        *secretsmanager.Client
	defaultRegion string
}

// New creates an AWS SM Provider from config.
// Returns (nil, nil) when cfg.Enabled is false.
func New(ctx context.Context, cfg config.AWSSMConfig, bootstrap BootstrapResolver) (*Provider, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	if isRef(region) {
		raw, err := bootstrap.Resolve(ctx, region)
		if err != nil {
			return nil, fmt.Errorf("awssm: resolving region: %w", err)
		}
		region = strings.TrimSpace(string(raw))
	}

	var loadOpts []func(*awsconfig.LoadOptions) error
	loadOpts = append(loadOpts, awsconfig.WithRegion(region))

	// Explicit credentials (optional — use IAM role / IRSA when available).
	if cfg.AccessKey != "" || cfg.SecretKey != "" {
		accessKey, err := resolveString(ctx, cfg.AccessKey, bootstrap)
		if err != nil {
			return nil, fmt.Errorf("awssm: resolving access_key: %w", err)
		}
		secretKey, err := resolveString(ctx, cfg.SecretKey, bootstrap)
		if err != nil {
			return nil, fmt.Errorf("awssm: resolving secret_key: %w", err)
		}
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("awssm: loading AWS config: %w", err)
	}

	return &Provider{
		client:        secretsmanager.NewFromConfig(awsCfg),
		defaultRegion: region,
	}, nil
}

// Scheme returns the URI scheme this provider handles.
func (p *Provider) Scheme() string { return "awssm" }

// Resolve fetches a secret value from AWS Secrets Manager.
//
// URI format:
//
//	awssm://secret-name           — uses default region
//	awssm://region/secret-name    — overrides region
//	awssm://[region/]name#field   — parse JSON, return field
func (p *Provider) Resolve(ctx context.Context, ref string) ([]byte, error) {
	region, name, field, err := parseURI(ref, p.defaultRegion)
	if err != nil {
		return nil, err
	}

	client := p.client
	if region != p.defaultRegion {
		// Create a regional client on-demand.
		// The Manager caches results, so this only happens on first access.
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
		if err != nil {
			return nil, fmt.Errorf("awssm: loading config for region %q: %w", region, err)
		}
		client = secretsmanager.NewFromConfig(awsCfg)
	}

	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(name),
	})
	if err != nil {
		return nil, fmt.Errorf("awssm: getting secret %q: %w", name, err)
	}

	var raw []byte
	if out.SecretString != nil {
		raw = []byte(*out.SecretString)
	} else if out.SecretBinary != nil {
		raw = out.SecretBinary
	} else {
		return nil, fmt.Errorf("awssm: %q returned no value", name)
	}

	if field == "" {
		return raw, nil
	}

	// Extract field from JSON secret.
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("awssm: secret %q is not valid JSON (needed to extract field %q): %w", name, field, err)
	}
	val, ok := m[field]
	if !ok {
		return nil, fmt.Errorf("awssm: field %q not found in secret %q", field, name)
	}
	switch v := val.(type) {
	case string:
		return []byte(v), nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("awssm: marshalling field %q: %w", field, err)
		}
		return b, nil
	}
}

// parseURI parses awssm://[region/]secret-name[#field].
// If the first path segment looks like an AWS region (contains a digit or has
// specific region prefixes), it is treated as the region; otherwise the whole
// path is the secret name using the default region.
func parseURI(ref, defaultRegion string) (region, name, field string, err error) {
	s := strings.TrimPrefix(ref, "awssm://")
	if idx := strings.LastIndex(s, "#"); idx >= 0 {
		field = s[idx+1:]
		s = s[:idx]
	}
	if s == "" {
		return "", "", "", fmt.Errorf("awssm: invalid URI %q — secret name is empty", ref)
	}

	// Detect region prefix: AWS regions look like "us-east-1", "eu-west-2", etc.
	// If the first segment matches the region pattern, use it; otherwise default.
	if idx := strings.Index(s, "/"); idx > 0 {
		maybeRegion := s[:idx]
		if looksLikeRegion(maybeRegion) {
			region = maybeRegion
			name = s[idx+1:]
			return region, name, field, nil
		}
	}
	return defaultRegion, s, field, nil
}

// looksLikeRegion returns true if s looks like an AWS region identifier.
func looksLikeRegion(s string) bool {
	prefixes := []string{"us-", "eu-", "ap-", "sa-", "ca-", "me-", "af-", "il-", "mx-"}
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
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
