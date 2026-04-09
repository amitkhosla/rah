package ingest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// EventSource is the read side of the event bus. Implementations tail an
// external stream (Redis Stream, HTTP SSE, etc.) and decode Event batches.
type EventSource interface {
	// Read blocks until at least one event is available or ctx is done.
	// Returns the decoded events; returns a non-nil error on persistent failure.
	Read(ctx context.Context) ([]Event, error)
	Close() error
}

// RedisStreamSource reads NDJSON-encoded Event batches from a Redis Stream
// using XREAD BLOCK. Each stream entry must have a "data" field containing
// one or more newline-delimited JSON event objects (same format as RedisSink).
//
// Starting offset is "$" (latest) so each instance only consumes events
// produced after it starts — replaying historical events on restart is
// intentionally skipped for cache invalidation use cases.
type RedisStreamSource struct {
	client    goredis.UniversalClient
	stream    string
	lastID    string
	timeout   time.Duration
	filterMap map[EventKind]struct{} // nil = accept all kinds
}

// NewRedisStreamSource creates a source that tails stream on client.
// filterKinds restricts which event kinds are returned; pass nil for all kinds.
func NewRedisStreamSource(client goredis.UniversalClient, stream string, timeoutMs int, filterKinds []string) *RedisStreamSource {
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	var fm map[EventKind]struct{}
	if len(filterKinds) > 0 {
		fm = make(map[EventKind]struct{}, len(filterKinds))
		for _, k := range filterKinds {
			fm[EventKind(k)] = struct{}{}
		}
	}
	return &RedisStreamSource{
		client:    client,
		stream:    stream,
		lastID:    "$", // only events produced after this instance starts
		timeout:   time.Duration(timeoutMs) * time.Millisecond,
		filterMap: fm,
	}
}

// Read calls XREAD BLOCK, decodes the "data" NDJSON field from each entry,
// and returns events matching the configured filter.
func (s *RedisStreamSource) Read(ctx context.Context) ([]Event, error) {
	// Use a slightly shorter timeout than ctx deadline so we don't time out on
	// the context before Redis returns — we want the blocking read to return
	// naturally so we can loop.
	args := &goredis.XReadArgs{
		Streams: []string{s.stream, s.lastID},
		Count:   200,
		Block:   s.timeout,
	}
	result, err := s.client.XRead(ctx, args).Result()
	if err != nil {
		if err == goredis.Nil {
			// Timeout with no messages — normal; loop back.
			return nil, nil
		}
		return nil, fmt.Errorf("xread %s: %w", s.stream, err)
	}

	var out []Event
	for _, stream := range result {
		for _, msg := range stream.Messages {
			s.lastID = msg.ID // advance cursor
			raw, ok := msg.Values["data"]
			if !ok {
				continue
			}
			data, ok := raw.(string)
			if !ok || data == "" {
				continue
			}
			events, err := decodeNDJSON([]byte(data))
			if err != nil {
				log.Printf("[ingest/source] decode error msg %s: %v", msg.ID, err)
				continue
			}
			for _, e := range events {
				if s.filterMap != nil {
					if _, pass := s.filterMap[e.Kind]; !pass {
						continue
					}
				}
				out = append(out, e)
			}
		}
	}
	return out, nil
}

func (s *RedisStreamSource) Close() error { return s.client.Close() }

// decodeNDJSON parses one JSON object per line, ignoring blank lines.
func decodeNDJSON(data []byte) ([]Event, error) {
	var events []Event
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, sc.Err()
}
