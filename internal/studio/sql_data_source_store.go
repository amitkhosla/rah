package studio

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/amitkhosla/rah/internal/datasource"
)

type sqlDataSourceStore struct {
	mu       sync.RWMutex
	configs  map[string]datasource.DataSourceConfig
	filePath string
}

func newSQLDataSourceStore(initial []datasource.DataSourceConfig, filePath string) (*sqlDataSourceStore, error) {
	s := &sqlDataSourceStore{
		configs:  make(map[string]datasource.DataSourceConfig),
		filePath: filePath,
	}
	for _, c := range initial {
		s.configs[c.Name] = c
	}
	if filePath != "" {
		if err := s.load(); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("sql data source store: load: %w", err)
		}
	}
	return s, nil
}

func (s *sqlDataSourceStore) List() []datasource.DataSourceConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]datasource.DataSourceConfig, 0, len(s.configs))
	for _, c := range s.configs {
		out = append(out, c)
	}
	return out
}

func (s *sqlDataSourceStore) Get(name string) (datasource.DataSourceConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.configs[name]
	return c, ok
}

func (s *sqlDataSourceStore) Set(cfg datasource.DataSourceConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs[cfg.Name] = cfg
	return s.persist()
}

func (s *sqlDataSourceStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.configs, name)
	return s.persist()
}

func (s *sqlDataSourceStore) persist() error {
	if s.filePath == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.configs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}

func (s *sqlDataSourceStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	var loaded map[string]datasource.DataSourceConfig
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	for k, v := range loaded {
		s.configs[k] = v
	}
	return nil
}
