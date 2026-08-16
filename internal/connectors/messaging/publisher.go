package messaging

import "context"

// PublishRequest is the data to publish to a message broker.
type PublishRequest struct {
	Topic   string
	Key     []byte            // optional partition/routing key
	Payload []byte
	Headers map[string]string // optional message headers
}

// MessagePublisher sends messages to a broker topic/queue.
type MessagePublisher interface {
	Connect(ctx context.Context) error
	Publish(ctx context.Context, req PublishRequest) error
	PublishBatch(ctx context.Context, reqs []PublishRequest) error
	Ping(ctx context.Context) error
	Close() error
}
