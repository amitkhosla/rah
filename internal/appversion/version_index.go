package appversion

import (
	"sync"
	"sync/atomic"
)

// versionKey is the routing cache lookup key.
// isDefault=true means this is the nil-tenant default rule (TenantID ignored for matching).
type versionKey struct {
	isDefault  bool
	tenantID   uint16
	logicalApp string
	env        string
}

// VersionIndex is the gateway's in-memory tenant-to-version routing cache.
// Reads are lock-free via atomic.Pointer. Writes copy-on-write under mu.
type VersionIndex struct {
	mu sync.Mutex
	p  atomic.Pointer[map[versionKey]string]
}

func (v *VersionIndex) load() map[versionKey]string {
	if p := v.p.Load(); p != nil {
		return *p
	}
	return nil
}

// Set stores a routing rule. tenantID=nil means default for all tenants.
func (v *VersionIndex) Set(tenantID *uint16, logicalApp, env, activeApp string) {
	key := makeKey(tenantID, logicalApp, env)
	v.mu.Lock()
	defer v.mu.Unlock()
	old := v.load()
	newM := make(map[versionKey]string, len(old)+1)
	for k, val := range old {
		newM[k] = val
	}
	newM[key] = activeApp
	v.p.Store(&newM)
}

// Resolve returns the active physical app name for the given tenant+env.
// Priority: tenant-specific > default > logicalApp passthrough.
// Lock-free: reads the map pointer atomically.
func (v *VersionIndex) Resolve(tenantID uint16, logicalApp, env string) string {
	m := v.load()
	if m == nil {
		return logicalApp
	}
	// 1. tenant-specific override
	if val, ok := m[versionKey{false, tenantID, logicalApp, env}]; ok {
		return val
	}
	// 2. default (nil-tenant)
	if val, ok := m[versionKey{true, 0, logicalApp, env}]; ok {
		return val
	}
	// 3. no versioning configured
	return logicalApp
}

// Delete removes a routing rule. tenantID=nil removes the default rule.
func (v *VersionIndex) Delete(tenantID *uint16, logicalApp, env string) {
	key := makeKey(tenantID, logicalApp, env)
	v.mu.Lock()
	defer v.mu.Unlock()
	old := v.load()
	if _, ok := old[key]; !ok {
		return // nothing to delete
	}
	newM := make(map[versionKey]string, len(old))
	for k, val := range old {
		if k != key {
			newM[k] = val
		}
	}
	v.p.Store(&newM)
}

// LoadAll atomically replaces the entire index with the given rules.
// Concurrent Resolve calls always see either the old complete map or the new one.
func (v *VersionIndex) LoadAll(rules []RoutingRule) {
	newM := make(map[versionKey]string, len(rules))
	for _, r := range rules {
		key := makeKey(r.TenantID, r.LogicalApp, r.Environment)
		newM[key] = r.ActiveApp
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.p.Store(&newM)
}

func makeKey(tenantID *uint16, logicalApp, env string) versionKey {
	if tenantID == nil {
		return versionKey{isDefault: true, tenantID: 0, logicalApp: logicalApp, env: env}
	}
	return versionKey{isDefault: false, tenantID: *tenantID, logicalApp: logicalApp, env: env}
}
