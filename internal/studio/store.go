package studio

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"
)

type ReleaseStore interface {
	Put(context.Context, ReleaseRecord) error
	Get(context.Context, string) (ReleaseRecord, error)
	List(context.Context) ([]ReleaseRecord, error)
}

type InMemoryReleaseStore struct {
	mu       sync.RWMutex
	releases map[string]ReleaseRecord
}

func NewInMemoryReleaseStore() *InMemoryReleaseStore {
	return &InMemoryReleaseStore{releases: map[string]ReleaseRecord{}}
}

func (s *InMemoryReleaseStore) Put(_ context.Context, rec ReleaseRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases[rec.ReleaseID] = rec
	return nil
}

func (s *InMemoryReleaseStore) Get(_ context.Context, id string) (ReleaseRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.releases[id]
	if !ok {
		return ReleaseRecord{}, errors.New("release not found")
	}
	return rec, nil
}

func (s *InMemoryReleaseStore) List(_ context.Context) ([]ReleaseRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ReleaseRecord, 0, len(s.releases))
	for _, r := range s.releases {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.T > out[j].CreatedAt.T })
	return out, nil
}

type FileReleaseStore struct {
	mu   sync.Mutex
	path string
}

func NewFileReleaseStore(path string) *FileReleaseStore { return &FileReleaseStore{path: path} }

func (s *FileReleaseStore) Put(_ context.Context, rec ReleaseRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return err
	}
	all[rec.ReleaseID] = rec
	return s.writeAllLocked(all)
}

func (s *FileReleaseStore) Get(_ context.Context, id string) (ReleaseRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return ReleaseRecord{}, err
	}
	rec, ok := all[id]
	if !ok {
		return ReleaseRecord{}, errors.New("release not found")
	}
	return rec, nil
}

func (s *FileReleaseStore) List(_ context.Context) ([]ReleaseRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readAllLocked()
	if err != nil {
		return nil, err
	}
	out := make([]ReleaseRecord, 0, len(all))
	for _, r := range all {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.T > out[j].CreatedAt.T })
	return out, nil
}

func (s *FileReleaseStore) readAllLocked() (map[string]ReleaseRecord, error) {
	all := map[string]ReleaseRecord{}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return all, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return all, nil
	}
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, err
	}
	return all, nil
}

func (s *FileReleaseStore) writeAllLocked(all map[string]ReleaseRecord) error {
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

func releaseStoreFromConfig(kind, path string) ReleaseStore {
	if kind == "file" && path != "" {
		return NewFileReleaseStore(path)
	}
	return NewInMemoryReleaseStore()
}

func nowUTC() time.Time { return time.Now().UTC() }
