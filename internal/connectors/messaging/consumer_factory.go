package messaging

import (
	"fmt"

	"github.com/amitkhosla/rah/internal/config"
)

// NewMessageConsumer creates a MessageConsumer for the given publisher config,
// topic, and consumer group ID. Kind is read from cfg.Kind.
func NewMessageConsumer(cfg config.PublisherConfig, topic, groupID string, secrets SecretResolver) (MessageConsumer, error) {
	switch cfg.Kind {
	case "kafka":
		return NewKafkaConsumer(cfg, topic, groupID, secrets), nil
	case "redis_streams":
		return NewRedisStreamsConsumer(cfg, topic, groupID, secrets), nil
	default:
		return nil, fmt.Errorf("event listener: unsupported publisher kind %q for consumption (supported: kafka, redis_streams)", cfg.Kind)
	}
}
