package ingest

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// RedisSink appends pre-formatted event batches to a Redis Stream via XADD.
// Each Write() call becomes one stream entry: field "data" = the formatted bytes.
type RedisSink struct {
	name      string
	client    goredis.UniversalClient
	stream    string
	maxLen    int64
	timeoutMs int
}

// NewRedisSink creates a Redis stream sink.
func NewRedisSink(name string, client goredis.UniversalClient, stream string, maxLen int64, timeoutMs int) *RedisSink {
	if timeoutMs <= 0 {
		timeoutMs = 2000
	}
	return &RedisSink{name: name, client: client, stream: stream, maxLen: maxLen, timeoutMs: timeoutMs}
}

func (s *RedisSink) Name() string { return s.name }

func (s *RedisSink) Write(formatted []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s.timeoutMs)*time.Millisecond)
	defer cancel()

	args := &goredis.XAddArgs{
		Stream: s.stream,
		ID:     "*",
		Values: map[string]any{"data": string(formatted)},
	}
	if s.maxLen > 0 {
		args.MaxLen = s.maxLen
		args.Approx = true
	}
	if err := s.client.XAdd(ctx, args).Err(); err != nil {
		return fmt.Errorf("redis stream sink xadd: %w", err)
	}
	return nil
}

func (s *RedisSink) Close() error {
	return s.client.Close()
}
