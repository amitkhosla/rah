package messaging

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/pubsub" //nolint:staticcheck
	"github.com/amitkhosla/rah/internal/config"
	"google.golang.org/api/option"
)

// PubSubPublisher publishes messages to Google Cloud Pub/Sub.
type PubSubPublisher struct {
	cfg     config.PublisherConfig
	secrets SecretResolver
	client  *pubsub.Client
	// cache for topic objects to avoid repeated lookups
	topics map[string]*pubsub.Topic
}

// Connect establishes the Pub/Sub client connection.
func (p *PubSubPublisher) Connect(ctx context.Context) error {
	opts := []option.ClientOption{}

	// If CredentialRef is provided, resolve and use JSON credentials
	if p.cfg.CredentialRef != "" {
		credBytes, err := p.secrets.Resolve(ctx, p.cfg.CredentialRef)
		if err != nil {
			return fmt.Errorf("pubsub[%s]: resolve credentials: %w", p.cfg.Name, err)
		}
		opts = append(opts, option.WithCredentialsJSON(credBytes)) //nolint:staticcheck
	}

	// Create Pub/Sub client
	client, err := pubsub.NewClient(ctx, p.cfg.ProjectID, opts...)
	if err != nil {
		return fmt.Errorf("pubsub[%s]: failed to create client: %w", p.cfg.Name, err)
	}

	p.client = client
	p.topics = make(map[string]*pubsub.Topic)

	return nil
}

// getTopic gets or creates a topic reference (cached for the lifetime of the connection).
// No locking needed — only called from Publish/PublishBatch which are single-goroutine per publisher in Rah's execution model.
func (p *PubSubPublisher) getTopic(topicName string) *pubsub.Topic {
	if topic, ok := p.topics[topicName]; ok {
		return topic
	}
	topic := p.client.Topic(topicName)
	p.topics[topicName] = topic
	return topic
}

// Publish sends a single message to a Pub/Sub topic.
func (p *PubSubPublisher) Publish(ctx context.Context, req PublishRequest) error {
	if p.client == nil {
		return fmt.Errorf("pubsub[%s]: not connected", p.cfg.Name)
	}

	topic := p.getTopic(req.Topic)

	msg := &pubsub.Message{
		Data:       req.Payload,
		Attributes: req.Headers,
	}

	// Set ordering key if key is provided (enables ordered delivery in Pub/Sub)
	if len(req.Key) > 0 {
		msg.OrderingKey = string(req.Key)
	}

	// Apply timeout if configured
	pubCtx := ctx
	if p.cfg.TimeoutMs > 0 {
		var cancel context.CancelFunc
		pubCtx, cancel = context.WithTimeout(ctx, time.Duration(p.cfg.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	// Publish and wait for confirmation
	result := topic.Publish(pubCtx, msg)
	_, err := result.Get(pubCtx)
	if err != nil {
		return fmt.Errorf("pubsub[%s]: publish message: %w", p.cfg.Name, err)
	}

	return nil
}

// PublishBatch sends multiple messages to Pub/Sub topics.
func (p *PubSubPublisher) PublishBatch(ctx context.Context, reqs []PublishRequest) error {
	if p.client == nil {
		return fmt.Errorf("pubsub[%s]: not connected", p.cfg.Name)
	}

	// Collect results for all messages
	results := make([]*pubsub.PublishResult, 0, len(reqs))

	// Apply timeout if configured
	pubCtx := ctx
	if p.cfg.TimeoutMs > 0 {
		var cancel context.CancelFunc
		pubCtx, cancel = context.WithTimeout(ctx, time.Duration(p.cfg.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	// Publish all messages (Pub/Sub batches internally)
	for _, req := range reqs {
		topic := p.getTopic(req.Topic)

		msg := &pubsub.Message{
			Data:       req.Payload,
			Attributes: req.Headers,
		}

		// Set ordering key if key is provided
		if len(req.Key) > 0 {
			msg.OrderingKey = string(req.Key)
		}

		result := topic.Publish(pubCtx, msg)
		results = append(results, result)
	}

	// Wait for all results
	for i, result := range results {
		_, err := result.Get(pubCtx)
		if err != nil {
			return fmt.Errorf("pubsub[%s]: batch message %d: %w", p.cfg.Name, i, err)
		}
	}

	return nil
}

// Ping verifies connectivity to the Pub/Sub service.
func (p *PubSubPublisher) Ping(ctx context.Context) error {
	if p.client == nil {
		return fmt.Errorf("pubsub[%s]: not connected", p.cfg.Name)
	}
	// Pub/Sub client initialization already validates credentials and connectivity
	return nil
}

// Close closes the Pub/Sub client and releases resources.
func (p *PubSubPublisher) Close() error {
	if p.client != nil {
		err := p.client.Close()
		p.client = nil
		p.topics = nil
		if err != nil {
			return fmt.Errorf("pubsub[%s]: close client: %w", p.cfg.Name, err)
		}
	}

	return nil
}
