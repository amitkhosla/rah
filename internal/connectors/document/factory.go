package document

import (
	"context"
	"fmt"

	"github.com/amitkhosla/rah/internal/config"
)

// SecretResolver is the interface for resolving secret references.
// Implemented by github.com/amitkhosla/rah/internal/secrets.Resolver.
// We define this locally to avoid a circular import (config → document → secrets → config).
type SecretResolver interface {
	// Resolve returns the plaintext secret for a credential reference.
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// NewDocumentProvider creates a DocumentProvider from a DocumentConnectorConfig.
// It does NOT dial or connect — it just constructs the provider struct.
// Connections are established on first use or by calling the manager's Start().
func NewDocumentProvider(ctx context.Context, cfg config.DocumentConnectorConfig, secretResolver SecretResolver) (DocumentProvider, error) {
	if secretResolver == nil {
		return nil, fmt.Errorf("document connector %s: secretResolver is nil", cfg.Name)
	}

	switch cfg.Kind {
	case "mongodb":
		return &MongoProvider{cfg: cfg, secrets: secretResolver}, nil

	case "postgresql":
		return &PostgresProvider{cfg: cfg, secrets: secretResolver}, nil

	case "mysql":
		return &MySQLProvider{cfg: cfg, secrets: secretResolver}, nil

	case "grpc":
		provider, err := NewGRPCProvider(ctx, cfg, secretResolver)
		if err != nil {
			return nil, fmt.Errorf("document connector %s: %w", cfg.Name, err)
		}
		return provider, nil

	default:
		return nil, fmt.Errorf("document connector %s: unknown kind %q", cfg.Name, cfg.Kind)
	}
}
