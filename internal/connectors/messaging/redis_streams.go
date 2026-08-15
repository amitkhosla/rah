package messaging

import (
	"context"
	"fmt"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/redis/go-redis/v9"
)

// RedisStreamsPublisher publishes messages to Redis Streams.
type RedisStreamsPublisher struct {
	cfg     config.PublisherConfig
	secrets SecretResolver
	rdb     *redis.Client
}

// Connect establishes a connection to Redis.
func (p *RedisStreamsPublisher) Connect(ctx context.Context) error {

	opts := &redis.Options{
		Addr: p.cfg.RedisAddr,
		DB:   p.cfg.RedisDB,
	}

	// Resolve password if credential reference is provided
	if p.cfg.CredentialRef != "" {
		credBytes, err := p.secrets.Resolve(ctx, p.cfg.CredentialRef)
		if err != nil {
			return fmt.Errorf("redis_streams[%s]: failed to resolve credentials: %w", p.cfg.Name, err)
		}
		opts.Password = string(credBytes)
	}

	rdb := redis.NewClient(opts)

	// Verify connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return fmt.Errorf("redis_streams[%s]: failed to connect: %w", p.cfg.Name, err)
	}

	p.rdb = rdb
	return nil
}

// buildValues constructs the field values for XADD.
func buildValues(req PublishRequest) []interface{} {
	var values []interface{}

	// Always include payload
	values = append(values, "payload", string(req.Payload))

	// Add key if provided
	if len(req.Key) > 0 {
		values = append(values, "key", string(req.Key))
	}

	// Add all headers
	for k, v := range req.Headers {
		values = append(values, k, v)
	}

	return values
}

// Publish sends a single message to a Redis Stream.
func (p *RedisStreamsPublisher) Publish(ctx context.Context, req PublishRequest) error {
	if p.rdb == nil {
		return fmt.Errorf("redis_streams[%s]: not connected", p.cfg.Name)
	}

	args := &redis.XAddArgs{
		Stream: req.Topic,
		ID:     "*", // auto-generate
		Values: buildValues(req),
	}

	if p.cfg.MaxLen > 0 {
		args.MaxLen = p.cfg.MaxLen
		args.Approx = true // ~ trimming for performance
	}

	_, err := p.rdb.XAdd(ctx, args).Result()
	if err != nil {
		return fmt.Errorf("redis_streams[%s]: %w", p.cfg.Name, err)
	}

	return nil
}

// PublishBatch sends multiple messages to Redis Streams using a pipeline.
func (p *RedisStreamsPublisher) PublishBatch(ctx context.Context, reqs []PublishRequest) error {
	if p.rdb == nil {
		return fmt.Errorf("redis_streams[%s]: not connected", p.cfg.Name)
	}

	pipe := p.rdb.Pipeline()

	for _, req := range reqs {
		args := &redis.XAddArgs{
			Stream: req.Topic,
			ID:     "*",
			Values: buildValues(req),
		}

		if p.cfg.MaxLen > 0 {
			args.MaxLen = p.cfg.MaxLen
			args.Approx = true
		}

		pipe.XAdd(ctx, args)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis_streams[%s]: batch publish failed: %w", p.cfg.Name, err)
	}

	return nil
}

// Ping verifies connectivity to the Redis instance.
func (p *RedisStreamsPublisher) Ping(ctx context.Context) error {
	if p.rdb == nil {
		return fmt.Errorf("redis_streams[%s]: not connected", p.cfg.Name)
	}

	err := p.rdb.Ping(ctx).Err()
	if err != nil {
		return fmt.Errorf("redis_streams[%s]: %w", p.cfg.Name, err)
	}

	return nil
}

// Close closes the Redis connection and releases resources.
func (p *RedisStreamsPublisher) Close() error {
	if p.rdb != nil {
		if err := p.rdb.Close(); err != nil {
			return fmt.Errorf("redis_streams[%s]: failed to close: %w", p.cfg.Name, err)
		}
		p.rdb = nil
	}

	return nil
}
