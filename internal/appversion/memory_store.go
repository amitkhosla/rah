package appversion

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type MemoryStore struct {
	mu       sync.RWMutex
	versions map[string]AppVersion  // key: AppName
	rules    []RoutingRule
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{versions: make(map[string]AppVersion)}
}

func (m *MemoryStore) UpsertVersion(_ context.Context, v AppVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v.UpdatedAt.IsZero() {
		v.UpdatedAt = time.Now()
	}
	if v.CreatedAt.IsZero() {
		if existing, ok := m.versions[v.AppName]; ok {
			v.CreatedAt = existing.CreatedAt
		} else {
			v.CreatedAt = v.UpdatedAt
		}
	}
	m.versions[v.AppName] = v
	return nil
}

func (m *MemoryStore) GetVersion(_ context.Context, appName string) (*AppVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.versions[appName]
	if !ok {
		return nil, fmt.Errorf("appversion: version %q not found", appName)
	}
	return &v, nil
}

func (m *MemoryStore) ListVersions(_ context.Context, logicalApp string) ([]AppVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []AppVersion
	for _, v := range m.versions {
		if v.LogicalApp == logicalApp {
			out = append(out, v)
		}
	}
	return out, nil
}

func (m *MemoryStore) RetireVersion(_ context.Context, appName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rules {
		if r.ActiveApp == appName {
			var tid any = "<nil>"
			if r.TenantID != nil {
				tid = *r.TenantID
			}
			return fmt.Errorf("appversion: cannot retire %q — still referenced by routing rule (tenant=%v env=%s)", appName, tid, r.Environment)
		}
	}
	v, ok := m.versions[appName]
	if !ok {
		return fmt.Errorf("appversion: version %q not found", appName)
	}
	v.Status = "retired"
	v.UpdatedAt = time.Now()
	m.versions[appName] = v
	return nil
}

func (m *MemoryStore) UpsertRoutingRule(_ context.Context, r RoutingRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now()
	}
	for i, existing := range m.rules {
		if rulesMatch(existing, r) {
			m.rules[i] = r
			return nil
		}
	}
	m.rules = append(m.rules, r)
	return nil
}

func (m *MemoryStore) DeleteRoutingRule(_ context.Context, tenantID *uint16, logicalApp, env string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.rules {
		if r.LogicalApp == logicalApp && r.Environment == env && ptrEq(r.TenantID, tenantID) {
			m.rules = append(m.rules[:i], m.rules[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *MemoryStore) ListRoutingRules(_ context.Context, logicalApp string) ([]RoutingRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []RoutingRule
	for _, r := range m.rules {
		if r.LogicalApp == logicalApp {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemoryStore) ResolveRoute(_ context.Context, tenantID uint16, logicalApp, env string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var defaultApp string
	for _, r := range m.rules {
		if r.LogicalApp != logicalApp || r.Environment != env {
			continue
		}
		if r.TenantID != nil && *r.TenantID == tenantID {
			return r.ActiveApp, nil
		}
		if r.TenantID == nil {
			defaultApp = r.ActiveApp
		}
	}
	if defaultApp != "" {
		return defaultApp, nil
	}
	return logicalApp, nil
}

// Compile-time interface check.
var _ Store = (*MemoryStore)(nil)

var errUnused = errors.New("unused")
var _ = errUnused

func rulesMatch(a, b RoutingRule) bool {
	return a.LogicalApp == b.LogicalApp && a.Environment == b.Environment && ptrEq(a.TenantID, b.TenantID)
}

func ptrEq(a, b *uint16) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
