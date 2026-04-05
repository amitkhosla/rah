package ingest

import (
	"bufio"
	"fmt"
	"os"
	"sync"
)

// FileSink appends pre-formatted bytes to a file using a buffered writer.
type FileSink struct {
	mu   sync.Mutex
	name string
	path string
	f    *os.File
	bw   *bufio.Writer
}

// NewFileSink opens (or creates) path for appending.
// bufSize 0 defaults to 256 KB.
func NewFileSink(name, path string, bufSize int) (*FileSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("ingest file sink: %w", err)
	}
	if bufSize <= 0 {
		bufSize = 256 * 1024
	}
	bw := bufio.NewWriterSize(f, bufSize)
	return &FileSink{name: name, path: path, f: f, bw: bw}, nil
}

func (s *FileSink) Name() string { return s.name }

func (s *FileSink) Write(formatted []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.bw.Write(formatted); err != nil {
		return err
	}
	return s.bw.Flush()
}

func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.bw.Flush(); err != nil {
		return err
	}
	return s.f.Close()
}
