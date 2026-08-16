package messaging

import (
	"context"
	"fmt"

	"github.com/amitkhosla/rah/internal/config"
)

// SecretResolver is the interface for resolving secret references.
// Implemented by github.com/amitkhosla/rah/internal/secrets.Resolver.
// We define this locally to avoid a circular import (config → messaging → secrets → config).
type SecretResolver interface {
	// Resolve returns the plaintext secret for a credential reference.
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// NewMessagePublisher creates a MessagePublisher from a PublisherConfig.
// It does NOT connect or establish a broker connection — it just constructs the provider struct.
// Connections are established on first use or by calling the manager's Start().
func NewMessagePublisher(ctx context.Context, cfg config.PublisherConfig, secretResolver SecretResolver) (MessagePublisher, error) {
	if secretResolver == nil {
		return nil, fmt.Errorf("publisher[%s]: secretResolver is nil", cfg.Name)
	}

	switch cfg.Kind {
	case "kafka":
		return &KafkaPublisher{cfg: cfg, secrets: secretResolver}, nil

	case "pubsub":
		return &PubSubPublisher{cfg: cfg, secrets: secretResolver}, nil

	case "rabbitmq":
		return &RabbitMQPublisher{cfg: cfg, secrets: secretResolver}, nil

	case "sqs":
		return &SQSPublisher{cfg: cfg, secrets: secretResolver}, nil

	case "redis_streams":
		return &RedisStreamsPublisher{cfg: cfg, secrets: secretResolver}, nil

	default:
		return nil, fmt.Errorf("publisher[%s]: unknown kind %q", cfg.Name, cfg.Kind)
	}
}
