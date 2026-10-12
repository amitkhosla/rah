package studio

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/appversion"
)

// AppVersionStore manages app versions and tenant routing rules in Studio.
// Backed by memory, file, or Postgres depending on ServerConfig.StoreKind.
type AppVersionStore interface {
	UpsertVersion(v appversion.AppVersion) error
	GetVersion(appName string) (*appversion.AppVersion, error)
	ListVersions(logicalApp string) ([]appversion.AppVersion, error)
	RetireVersion(appName string) error

	UpsertRoutingRule(r appversion.RoutingRule) error
	DeleteRoutingRule(tenantID *uint16, logicalApp, env string) error
	ListRoutingRules(logicalApp string) ([]appversion.RoutingRule, error)
}

// ---- In-memory implementation ----

type inMemoryAppVersionStore struct {
	mu       sync.RWMutex
	versions map[string]appversion.AppVersion
	rules    []appversion.RoutingRule
}

func newInMemoryAppVersionStore() *inMemoryAppVersionStore {
	return &inMemoryAppVersionStore{
		versions: make(map[string]appversion.AppVersion),
	}
}

func (s *inMemoryAppVersionStore) UpsertVersion(v appversion.AppVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if v.CreatedAt.IsZero() {
		if existing, ok := s.versions[v.AppName]; ok {
			v.CreatedAt = existing.CreatedAt
		} else {
			v.CreatedAt = now
		}
	}
	v.UpdatedAt = now
	s.versions[v.AppName] = v
	return nil
}

func (s *inMemoryAppVersionStore) GetVersion(appName string) (*appversion.AppVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.versions[appName]
	if !ok {
		return nil, fmt.Errorf("studio: app version %q not found", appName)
	}
	return &v, nil
}

func (s *inMemoryAppVersionStore) ListVersions(logicalApp string) ([]appversion.AppVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []appversion.AppVersion
	for _, v := range s.versions {
		if v.LogicalApp == logicalApp {
			out = append(out, v)
		}
	}
	return out, nil
}

func (s *inMemoryAppVersionStore) RetireVersion(appName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rules {
		if r.ActiveApp == appName {
			return fmt.Errorf("studio: cannot retire %q — still referenced by routing rule (env=%s)", appName, r.Environment)
		}
	}
	v, ok := s.versions[appName]
	if !ok {
		return fmt.Errorf("studio: app version %q not found", appName)
	}
	v.Status = "retired"
	v.UpdatedAt = time.Now()
	s.versions[appName] = v
	return nil
}

func (s *inMemoryAppVersionStore) UpsertRoutingRule(r appversion.RoutingRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.UpdatedAt = time.Now()
	for i, existing := range s.rules {
		if existing.LogicalApp == r.LogicalApp && existing.Environment == r.Environment && ptrEq16(existing.TenantID, r.TenantID) {
			s.rules[i] = r
			return nil
		}
	}
	s.rules = append(s.rules, r)
	return nil
}

func (s *inMemoryAppVersionStore) DeleteRoutingRule(tenantID *uint16, logicalApp, env string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rules {
		if r.LogicalApp == logicalApp && r.Environment == env && ptrEq16(r.TenantID, tenantID) {
			s.rules = append(s.rules[:i], s.rules[i+1:]...)
			return nil
		}
	}
	return nil
}

func (s *inMemoryAppVersionStore) ListRoutingRules(logicalApp string) ([]appversion.RoutingRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []appversion.RoutingRule
	for _, r := range s.rules {
		if r.LogicalApp == logicalApp {
			out = append(out, r)
		}
	}
	return out, nil
}

// ---- File-backed implementation ----

type appVersionFileData struct {
	Versions []appversion.AppVersion  `json:"versions"`
	Rules    []appversion.RoutingRule `json:"rules"`
}

type fileAppVersionStore struct {
	mu   sync.RWMutex
	path string
	inMemoryAppVersionStore
}

func newFileAppVersionStore(dir string) (*fileAppVersionStore, error) {
	s := &fileAppVersionStore{
		path:                    filepath.Join(dir, "app_versions.json"),
		inMemoryAppVersionStore: inMemoryAppVersionStore{versions: make(map[string]appversion.AppVersion)},
	}
	// Load existing data if file exists
	if data, err := os.ReadFile(s.path); err == nil {
		var fd appVersionFileData
		if jsonErr := json.Unmarshal(data, &fd); jsonErr == nil {
			for _, v := range fd.Versions {
				s.inMemoryAppVersionStore.versions[v.AppName] = v
			}
			s.inMemoryAppVersionStore.rules = fd.Rules
		}
	}
	return s, nil
}

func (s *fileAppVersionStore) persist() error {
	fd := appVersionFileData{Rules: s.inMemoryAppVersionStore.rules}
	for _, v := range s.inMemoryAppVersionStore.versions {
		fd.Versions = append(fd.Versions, v)
	}
	data, err := json.MarshalIndent(fd, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

func (s *fileAppVersionStore) UpsertVersion(v appversion.AppVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.inMemoryAppVersionStore.UpsertVersion(v); err != nil {
		return err
	}
	return s.persist()
}

func (s *fileAppVersionStore) RetireVersion(appName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.inMemoryAppVersionStore.RetireVersion(appName); err != nil {
		return err
	}
	return s.persist()
}

func (s *fileAppVersionStore) UpsertRoutingRule(r appversion.RoutingRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.inMemoryAppVersionStore.UpsertRoutingRule(r); err != nil {
		return err
	}
	return s.persist()
}

func (s *fileAppVersionStore) DeleteRoutingRule(tenantID *uint16, logicalApp, env string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.inMemoryAppVersionStore.DeleteRoutingRule(tenantID, logicalApp, env); err != nil {
		return err
	}
	return s.persist()
}

// newAppVersionStore creates the appropriate AppVersionStore based on storeKind.
// storeKind: "memory" | "file". (Postgres variant wired separately via gateway DB pool.)
func newAppVersionStore(storeKind, storePath string) (AppVersionStore, error) {
	switch storeKind {
	case "file":
		return newFileAppVersionStore(storePath)
	default:
		return newInMemoryAppVersionStore(), nil
	}
}

func ptrEq16(a, b *uint16) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
