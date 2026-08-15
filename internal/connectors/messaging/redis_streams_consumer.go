package messaging

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/redis/go-redis/v9"
)

// RedisStreamsConsumer reads from a Redis Stream using XREADGROUP.
type RedisStreamsConsumer struct {
	cfg     config.PublisherConfig
	topic   string
	groupID string
	secrets SecretResolver
	rdb     *redis.Client
}

func NewRedisStreamsConsumer(cfg config.PublisherConfig, topic, groupID string, secrets SecretResolver) *RedisStreamsConsumer {
	return &RedisStreamsConsumer{cfg: cfg, topic: topic, groupID: groupID, secrets: secrets}
}

func (c *RedisStreamsConsumer) Start(ctx context.Context, handler MessageHandler) error {
	opts := &redis.Options{
		Addr: c.cfg.RedisAddr,
		DB:   c.cfg.RedisDB,
	}
	if c.cfg.CredentialRef != "" {
		credBytes, err := c.secrets.Resolve(ctx, c.cfg.CredentialRef)
		if err != nil {
			return fmt.Errorf("redis_streams_consumer[%s]: resolve credentials: %w", c.cfg.Name, err)
		}
		opts.Password = string(credBytes)
	}

	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return fmt.Errorf("redis_streams_consumer[%s]: ping failed: %w", c.cfg.Name, err)
	}
	c.rdb = rdb
	defer rdb.Close()

	consumerName := c.cfg.Name

	// Create consumer group (ignore BUSYGROUP error if it already exists).
	if err := rdb.XGroupCreateMkStream(ctx, c.topic, c.groupID, "0").Err(); err != nil {
		if err.Error() != "BUSYGROUP Consumer Group name already exists" {
			return fmt.Errorf("redis_streams_consumer[%s]: create group: %w", c.cfg.Name, err)
		}
	}

	for {
		if ctx.Err() != nil {
			return nil
		}
		streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    c.groupID,
			Consumer: consumerName,
			Streams:  []string{c.topic, ">"},
			Count:    10,
			Block:    5 * time.Second,
		}).Result()
		if err != nil {
			if err == redis.Nil || ctx.Err() != nil {
				continue
			}
			log.Printf("[RedisStreamsConsumer:%s] read error: %v", c.cfg.Name, err)
			continue
		}
		for _, stream := range streams {
			for _, msg := range stream.Messages {
				cm := ConsumedMessage{
					Topic:   stream.Stream,
					Key:     []byte(msg.ID),
					Headers: make(map[string]string),
				}
				// Extract payload and other fields.
				if payload, ok := msg.Values["payload"]; ok {
					cm.Payload = []byte(fmt.Sprintf("%v", payload))
				}
				for k, v := range msg.Values {
					if k != "payload" && k != "key" {
						cm.Headers[k] = fmt.Sprintf("%v", v)
					}
				}
				if keyVal, ok := msg.Values["key"]; ok {
					cm.Key = []byte(fmt.Sprintf("%v", keyVal))
				}
				if err := handler(ctx, cm); err != nil {
					log.Printf("[RedisStreamsConsumer:%s] handler error: %v", c.cfg.Name, err)
				}
				// Acknowledge the message.
				if err := rdb.XAck(ctx, c.topic, c.groupID, msg.ID).Err(); err != nil {
					log.Printf("[RedisStreamsConsumer:%s] ack error %s: %v", c.cfg.Name, msg.ID, err)
				}
			}
		}
	}
}

func (c *RedisStreamsConsumer) Stop() error {
	if c.rdb != nil {
		return c.rdb.Close()
	}
	return nil
}
