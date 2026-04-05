package ingest

import (
	"os"
	"sync"
)

// StdoutSink writes pre-formatted bytes directly to os.Stdout.
type StdoutSink struct {
	mu   sync.Mutex
	name string
}

func NewStdoutSink(name string) *StdoutSink {
	return &StdoutSink{name: name}
}

func (s *StdoutSink) Name() string { return s.name }

func (s *StdoutSink) Write(formatted []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := os.Stdout.Write(formatted)
	return err
}

func (s *StdoutSink) Close() error { return nil }
