package control

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
)

// gwConnStore is a thread-safe, optionally file-backed store for connector configs.
// T must be a struct with a Name field (accessed via keyFn).
type gwConnStore[T any] struct {
	mu       sync.RWMutex
	configs  map[string]T
	filePath string        // "" = memory-only
	keyFn    func(T) string // extracts the unique name key from a config
}

func newGWConnStore[T any](initial []T, keyFn func(T) string, filePath string) (*gwConnStore[T], error) {
	s := &gwConnStore[T]{
		configs:  make(map[string]T),
		filePath: filePath,
		keyFn:    keyFn,
	}
	// Load previously persisted connectors from file first.
	if filePath != "" {
		if data, err := os.ReadFile(filePath); err == nil {
			var list []T
			if json.Unmarshal(data, &list) == nil {
				for _, cfg := range list {
					s.configs[keyFn(cfg)] = cfg
				}
			}
		}
	}
	// Seed with static initial configs — don't overwrite file-loaded ones.
	for _, cfg := range initial {
		k := keyFn(cfg)
		if _, exists := s.configs[k]; !exists {
			s.configs[k] = cfg
		}
	}
	return s, nil
}

func (s *gwConnStore[T]) List() []T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]T, 0, len(s.configs))
	for _, v := range s.configs {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		return s.keyFn(out[i]) < s.keyFn(out[j])
	})
	return out
}

func (s *gwConnStore[T]) Get(name string) (T, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.configs[name]
	return v, ok
}

func (s *gwConnStore[T]) Set(name string, cfg T) error {
	if name == "" {
		return fmt.Errorf("connector name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs[name] = cfg
	return s.persist()
}

func (s *gwConnStore[T]) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.configs[name]; !ok {
		return fmt.Errorf("connector %q not found", name)
	}
	delete(s.configs, name)
	return s.persist()
}

// persist writes the current configs to the file. Caller must hold mu.
func (s *gwConnStore[T]) persist() error {
	if s.filePath == "" {
		return nil
	}
	list := make([]T, 0, len(s.configs))
	for _, v := range s.configs {
		list = append(list, v)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0600)
}
