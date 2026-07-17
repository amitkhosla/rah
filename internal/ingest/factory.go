package ingest

import (
	"fmt"
	"log"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/amitkhosla/rah/internal/config"
)

// NewPipelineFromConfig builds and starts an ingestion pipeline from the gateway config.
// Returns nil, nil when cfg.Enabled=false (ingest is disabled; emit steps become no-ops).
// Returns a non-nil error only for configuration mistakes; runtime errors are logged but not fatal.
func NewPipelineFromConfig(cfg config.IngestConfig) (*Pipeline, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	cfg = applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}

	// Build sinkWorkers (one per named sink).
	workerByName := make(map[string]*sinkWorker, len(cfg.Sinks))
	allWorkers := make([]*sinkWorker, 0, len(cfg.Sinks))
	for _, sc := range cfg.Sinks {
		formatter, err := NewFormatter(sc.Format, sc.Template)
		if err != nil {
			return nil, fmt.Errorf("ingest: sink %q formatter: %w", sc.Name, err)
		}
		sink, err := newSinkFromConfig(sc, formatter.ContentType())
		if err != nil {
			return nil, fmt.Errorf("ingest: sink %q: %w", sc.Name, err)
		}
		sw := &sinkWorker{
			name:       sc.Name,
			ch:         make(chan Event, sc.SinkQueueSize),
			sink:       sink,
			formatter:  formatter,
			maxItems:   sc.MaxBatchItems,
			maxBytes:   sc.MaxBatchBytes,
			flushMs:    sc.FlushMs,
			minWorkers: sc.MinWorkers,
			maxWorkers: sc.MaxWorkers,
			stopCh:     make(chan struct{}),
		}
		workerByName[sc.Name] = sw
		allWorkers = append(allWorkers, sw)
		log.Printf("[ingest] sink %q (%s) configured: workers=%d..%d queue=%d batch=%d/%dB flush=%dms",
			sc.Name, sc.Kind, sc.MinWorkers, sc.MaxWorkers, sc.SinkQueueSize,
			sc.MaxBatchItems, sc.MaxBatchBytes, sc.FlushMs)
	}

	// Build kindRings (one per EventKind).
	kinds := make(map[EventKind]*kindRing, len(cfg.Kinds))
	for _, kc := range cfg.Kinds {
		sinks := make([]*sinkWorker, 0, len(kc.Sinks))
		for _, sName := range kc.Sinks {
			sinks = append(sinks, workerByName[sName])
		}
		ring := NewRing(kc.RingSize)
		kr := &kindRing{
			ring:      ring,
			allowDrop: kc.AllowDrop,
			priority:  kc.Priority,
			sinks:     sinks,
		}
		// All kinds get an overflow channel â€” drop is always a last resort.
		// For allowDrop=false: fanout blocks in overflow until timeout.
		// For allowDrop=true: overflow is tried non-blocking; drop only if both ring and overflow are full.
		kr.overflow = make(chan Event, kc.RingSize)
		kinds[EventKind(kc.Kind)] = kr
		log.Printf("[ingest] kind %q: ring=%d allowDrop=%v priority=%d sinks=%v",
			kc.Kind, kc.RingSize, kc.AllowDrop, kc.Priority, kc.Sinks)
	}

	timeout := time.Duration(cfg.BackpressureTimeoutSec) * time.Second
	return newPipeline(kinds, allWorkers, cfg.FanoutWorkers, timeout), nil
}

// newSinkFromConfig constructs the raw Sink implementation for one sink config.
// contentType is passed to HTTPSink so it can set the correct Content-Type header.
func newSinkFromConfig(sc config.IngestSinkConfig, contentType string) (Sink, error) {
	switch sc.Kind {
	case config.IngestSinkStdout:
		return NewStdoutSink(sc.Name), nil
	case config.IngestSinkFile:
		return NewFileSink(sc.Name, sc.FilePath, sc.FileBufKB*1024)
	case config.IngestSinkHTTP:
		return NewHTTPSink(sc.Name, sc.URL, sc.Headers, sc.TimeoutMs, contentType), nil
	case config.IngestSinkRedisStream:
		opt, err := goredis.ParseURL(sc.DSN)
		if err != nil {
			return nil, fmt.Errorf("invalid dsn: %w", err)
		}
		client := goredis.NewClient(opt)
		return NewRedisSink(sc.Name, client, sc.StreamName, sc.MaxLen, sc.TimeoutMs), nil
	default:
		return nil, fmt.Errorf("unknown kind %q", sc.Kind)
	}
}
