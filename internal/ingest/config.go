package ingest

import (
	"fmt"

	"rah/internal/config"
)

func applyDefaults(cfg config.IngestConfig) config.IngestConfig {
	if cfg.FanoutWorkers <= 0 {
		cfg.FanoutWorkers = 2
	}
	if cfg.BackpressureTimeoutSec <= 0 {
		cfg.BackpressureTimeoutSec = 30
	}
	for i := range cfg.Kinds {
		k := &cfg.Kinds[i]
		if k.RingSize <= 0 {
			k.RingSize = 65536
		}
		if k.Priority <= 0 {
			k.Priority = 10
		}
	}
	for i := range cfg.Sinks {
		s := &cfg.Sinks[i]
		if s.MinWorkers <= 0 {
			s.MinWorkers = 2
		}
		if s.MaxWorkers <= 0 {
			s.MaxWorkers = 8
		}
		if s.SinkQueueSize <= 0 {
			s.SinkQueueSize = 16384
		}
		if s.MaxBatchItems <= 0 {
			s.MaxBatchItems = 256
		}
		if s.MaxBatchBytes <= 0 {
			s.MaxBatchBytes = 1048576 // 1 MB
		}
		if s.FlushMs <= 0 {
			s.FlushMs = 200
		}
		if s.FileBufKB <= 0 {
			s.FileBufKB = 256
		}
		if s.TimeoutMs <= 0 {
			s.TimeoutMs = 5000
		}
		// MinWorkers must not exceed MaxWorkers
		if s.MinWorkers > s.MaxWorkers {
			s.MinWorkers = s.MaxWorkers
		}
	}
	return cfg
}

func validate(cfg config.IngestConfig) error {
	sinkNames := make(map[string]bool, len(cfg.Sinks))
	for _, sc := range cfg.Sinks {
		if sc.Name == "" {
			return fmt.Errorf("ingest: sink missing name")
		}
		if sinkNames[sc.Name] {
			return fmt.Errorf("ingest: duplicate sink %q", sc.Name)
		}
		switch sc.Kind {
		case config.IngestSinkFile:
			if sc.FilePath == "" {
				return fmt.Errorf("ingest: sink %q: file_path required for kind=file", sc.Name)
			}
		case config.IngestSinkHTTP:
			if sc.URL == "" {
				return fmt.Errorf("ingest: sink %q: url required for kind=http", sc.Name)
			}
		case config.IngestSinkRedisStream:
			if sc.DSN == "" {
				return fmt.Errorf("ingest: sink %q: dsn required for kind=redis_stream", sc.Name)
			}
			if sc.StreamName == "" {
				return fmt.Errorf("ingest: sink %q: stream_name required for kind=redis_stream", sc.Name)
			}
		case config.IngestSinkStdout:
			// no extra validation
		default:
			return fmt.Errorf("ingest: sink %q: unknown kind %q", sc.Name, sc.Kind)
		}
		if sc.Format == "text" && sc.Template == "" {
			return fmt.Errorf("ingest: sink %q: template required when format=text", sc.Name)
		}
		sinkNames[sc.Name] = true
	}
	for _, kc := range cfg.Kinds {
		if kc.Kind == "" {
			return fmt.Errorf("ingest: kind config missing kind field")
		}
		for _, sName := range kc.Sinks {
			if !sinkNames[sName] {
				return fmt.Errorf("ingest: kind %q references unknown sink %q", kc.Kind, sName)
			}
		}
	}
	return nil
}
