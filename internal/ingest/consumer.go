package ingest

import (
	"context"
	"log"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/amitkhosla/rah/internal/config"
)

// EventHandler is called for each event read from a source.
type EventHandler func(e Event)

// RunConsumer reads events from src and calls handler for each until ctx
// is cancelled. Transient read errors are logged and retried with a short
// backoff; the loop only exits when ctx is done or src is closed.
func RunConsumer(ctx context.Context, src EventSource, handler EventHandler) {
	for {
		if ctx.Err() != nil {
			return
		}
		events, err := src.Read(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("[ingest/consumer] read error: %v — retrying in 2s", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, e := range events {
			handler(e)
		}
	}
}

// StartConsumers builds all sources defined in cfg and starts a RunConsumer
// goroutine for each one. handler is called for every event received.
// The goroutines run until ctx is cancelled. Returns the number of sources started.
func StartConsumers(ctx context.Context, cfg config.IngestConfig, handler EventHandler) int {
	started := 0
	for _, sc := range cfg.Sources {
		src, err := newSourceFromConfig(sc)
		if err != nil {
			log.Printf("[ingest/consumer] source %q build error: %v — skipping", sc.Stream, err)
			continue
		}
		go func(s EventSource) {
			defer func() { _ = s.Close() }()
			RunConsumer(ctx, s, handler)
		}(src)
		log.Printf("[ingest/consumer] started %s source on stream %q (filter=%v)",
			sc.Kind, sc.Stream, sc.Events)
		started++
	}
	return started
}

// newSourceFromConfig builds an EventSource from a single IngestSourceConfig.
func newSourceFromConfig(sc config.IngestSourceConfig) (EventSource, error) {
	switch sc.Kind {
	case config.IngestSourceRedisStream:
		opt, err := goredis.ParseURL(sc.DSN)
		if err != nil {
			return nil, err
		}
		client := goredis.NewClient(opt)
		return NewRedisStreamSource(client, sc.Stream, sc.TimeoutMs, sc.Events), nil
	default:
		return nil, nil // unknown kind — caller skips
	}
}
