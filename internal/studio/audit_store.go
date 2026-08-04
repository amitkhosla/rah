package studio

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type AuditRecord struct {
	ID           string            `json:"id"`
	Timestamp    time.Time         `json:"timestamp"`
	Actor        string            `json:"actor"`
	Action       string            `json:"action"`
	ResourceType string            `json:"resource_type"`
	ResourceID   string            `json:"resource_id"`
	Status       string            `json:"status"`
	Summary      string            `json:"summary"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type AuditStore interface {
	Append(ctx context.Context, r AuditRecord) error
	List(ctx context.Context, limit int) ([]AuditRecord, error)
}

type InMemoryAuditStore struct {
	mu      sync.RWMutex
	records []AuditRecord
}

func NewInMemoryAuditStore() *InMemoryAuditStore {
	return &InMemoryAuditStore{records: []AuditRecord{}}
}

func (s *InMemoryAuditStore) Append(_ context.Context, r AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
	// Drop oldest record if over capacity (max 1000).
	if len(s.records) > 1000 {
		s.records = s.records[1:]
	}
	return nil
}

func (s *InMemoryAuditStore) List(_ context.Context, limit int) ([]AuditRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return newest-first up to limit.
	out := make([]AuditRecord, len(s.records))
	copy(out, s.records)
	// Reverse to get newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type FileAuditStore struct {
	mu   sync.Mutex
	path string
}

func NewFileAuditStore(path string) *FileAuditStore {
	return &FileAuditStore{path: filepath.Join(path, "audit.ndjson")}
}

func (s *FileAuditStore) Append(_ context.Context, r AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

func (s *FileAuditStore) List(_ context.Context, limit int) ([]AuditRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []AuditRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r AuditRecord
		if err := json.Unmarshal([]byte(line), &r); err == nil {
			out = append(out, r)
		}
	}
	// Return newest-first up to limit.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func newAuditStore(kind, path string) AuditStore {
	if kind == "file" && path != "" {
		return NewFileAuditStore(path)
	}
	return NewInMemoryAuditStore()
}
