package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/maphash"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// nextAvailableTenantID is a global counter for row allocation.
// Starts at 1; TenantID 0 is reserved as the "empty slot" sentinel in AliasTable.
var nextAvailableTenantID uint32 = 1

// Test tenant ID range — reserved for ephemeral test tenants that are never
// persisted to the datastore and exist only for the lifetime of a test run.
const (
	TestTenantRangeStart uint16 = 0xF000
	TestTenantRangeEnd   uint16 = 0xFFFF
)

// RegistryManager coordinates the management plane of the gateway.
// A single Mutex serializes all writes; reads are lock-free via atomic snapshot.
type RegistryManager struct {
	mu               sync.Mutex
	aliasMap         map[string]uint16        // alias → TenantID; management-plane source of truth
	seed             maphash.Seed             // fixed per-process; used for all AliasTable builds
	rateLimitNames   map[string]uint16        // name → RateLimitConfigId
	nextRLConfigID   uint16                   // auto-increment; starts at 0, gateway default is 0
	tenantData       map[uint16]*TenantRecord // management-plane mirror; keyed by TenantID
	tenantIDs        []uint16                 // sorted slice of active TenantIDs; enables cursor pagination
	store            RegistryDatastore        // optional; nil = no persistence
	nextTestTenantID uint16                   // next ID to allocate from test range; starts at TestTenantRangeStart
}

// NewRegistryManager returns an initialized RegistryManager.
func NewRegistryManager() *RegistryManager {
	return &RegistryManager{
		aliasMap:       make(map[string]uint16),
		seed:           maphash.MakeSeed(),
		rateLimitNames: make(map[string]uint16),
		tenantData:     make(map[uint16]*TenantRecord),
		tenantIDs:      make([]uint16, 0, 64),
	}
}

// ensureInit lazily initialises aliasMap and seed for managers created via
// struct literal (&RegistryManager{}) rather than NewRegistryManager.
func (m *RegistryManager) ensureInit() {
	if m.aliasMap == nil {
		m.aliasMap = make(map[string]uint16)
		m.seed = maphash.MakeSeed()
	}
	if m.rateLimitNames == nil {
		m.rateLimitNames = make(map[string]uint16)
	}
	if m.tenantData == nil {
		m.tenantData = make(map[uint16]*TenantRecord)
	}
	if m.tenantIDs == nil {
		m.tenantIDs = make([]uint16, 0, 64)
	}
}

// insertSortedTenantID adds tID into the sorted tenantIDs slice if not present.
// Must be called under the manager mutex.
func (m *RegistryManager) insertSortedTenantID(tID uint16) {
	pos := sort.Search(len(m.tenantIDs), func(i int) bool { return m.tenantIDs[i] >= tID })
	if pos < len(m.tenantIDs) && m.tenantIDs[pos] == tID {
		return // already present
	}
	m.tenantIDs = append(m.tenantIDs, 0)
	copy(m.tenantIDs[pos+1:], m.tenantIDs[pos:])
	m.tenantIDs[pos] = tID
}

// removeSortedTenantID removes tID from the sorted tenantIDs slice.
// Must be called under the manager mutex.
func (m *RegistryManager) removeSortedTenantID(tID uint16) {
	pos := sort.Search(len(m.tenantIDs), func(i int) bool { return m.tenantIDs[i] >= tID })
	if pos >= len(m.tenantIDs) || m.tenantIDs[pos] != tID {
		return // not found
	}
	m.tenantIDs = append(m.tenantIDs[:pos], m.tenantIDs[pos+1:]...)
}

// TenantSummary is a lightweight tenant record for list responses.
type TenantSummary struct {
	TenantID        uint16   `json:"tenant_id"`
	Aliases         []string `json:"aliases"`
	ServiceURLCount int      `json:"service_url_count"`
	IdentifierCount int      `json:"identifier_count"`
	MetadataCount   int      `json:"metadata_count"`
}

// ListTenants returns a page of TenantSummaries starting after cursor (exclusive).
// cursor=0 means start from the beginning.
// Returns summaries and the next cursor (0 if no more pages).
func (m *RegistryManager) ListTenants(cursor uint16, limit int) ([]TenantSummary, uint16) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find start position after cursor.
	start := 0
	if cursor > 0 {
		pos := sort.Search(len(m.tenantIDs), func(i int) bool { return m.tenantIDs[i] > cursor })
		start = pos
	}

	end := start + limit
	if end > len(m.tenantIDs) {
		end = len(m.tenantIDs)
	}

	page := m.tenantIDs[start:end]
	out := make([]TenantSummary, 0, len(page))
	for _, tID := range page {
		rec := m.tenantData[tID]
		if rec == nil {
			out = append(out, TenantSummary{TenantID: tID})
			continue
		}
		out = append(out, TenantSummary{
			TenantID:        tID,
			Aliases:         rec.Aliases,
			ServiceURLCount: len(rec.ServiceURLs),
			IdentifierCount: len(rec.Identifiers),
			MetadataCount:   len(rec.Metadata),
		})
	}

	var nextCursor uint16
	if end < len(m.tenantIDs) {
		nextCursor = m.tenantIDs[end-1]
	}
	return out, nextCursor
}

// EnsureURLKeyID resolves a URL key name to its stable KeyID in the URLs store,
// creating the key if it does not exist yet. Safe to call at bake time from the
// control plane. Use the returned KeyID with GetURLByKeyID on the hot path.
func (m *RegistryManager) EnsureURLKeyID(key string) uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()
	reg := m.activeOrEmpty()
	kID, reg := m.resolveOrCreateURLKey(reg, key)
	State.Active.Store(reg)
	return kID
}

// EnsureIDKeyID resolves an identifier key name to its stable KeyID in the IDs
// store, creating the key if it does not exist yet. Safe to call at bake time.
// Use the returned KeyID with GetIDByKeyID on the hot path.
func (m *RegistryManager) EnsureIDKeyID(key string) uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()
	reg := m.activeOrEmpty()
	kID, reg := m.resolveOrCreateIDKey(reg, key)
	State.Active.Store(reg)
	return kID
}

// EnsureMetaKeyID resolves a metadata key name to its stable KeyID in the Meta
// store, creating the key if it does not exist yet. Safe to call at bake time.
// Use the returned KeyID with GetMetaByKeyID on the hot path.
func (m *RegistryManager) EnsureMetaKeyID(key string) uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()
	reg := m.activeOrEmpty()
	kID, reg := m.resolveOrCreateMetaKey(reg, key)
	State.Active.Store(reg)
	return kID
}

// GetTenantRecord returns the management-plane mirror for the given TenantID, or nil.
func (m *RegistryManager) GetTenantRecord(tID uint16) *TenantRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tenantData[tID]
}

// GetRateLimitConfigs returns all named rate limit configs with their IDs.
func (m *RegistryManager) GetRateLimitConfigs() []RateLimitRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	reg := State.Active.Load()
	out := make([]RateLimitRecord, 0, len(m.rateLimitNames))
	for name, id := range m.rateLimitNames {
		cfg := RateLimitConfig{}
		if reg != nil && int(id) < len(reg.RateLimitConfigs) {
			cfg = reg.RateLimitConfigs[id]
		}
		out = append(out, RateLimitRecord{Name: name, Config: cfg})
	}
	return out
}

// GetRateLimitConfig returns the named rate limit config, or (zero, false) if not found.
func (m *RegistryManager) GetRateLimitConfig(name string) (RateLimitRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.rateLimitNames[name]
	if !ok {
		return RateLimitRecord{}, false
	}
	reg := State.Active.Load()
	cfg := RateLimitConfig{}
	if reg != nil && int(id) < len(reg.RateLimitConfigs) {
		cfg = reg.RateLimitConfigs[id]
	}
	return RateLimitRecord{Name: name, Config: cfg}, true
}

// AddServiceURL sets a single service URL for the tenant identified by alias.
// name is the URL key (e.g. "primary", "fallback") without any prefix.
func (m *RegistryManager) AddServiceURL(alias string, name string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	reg := m.activeOrEmpty()
	tID, reg := m.resolveOrCreateTenant(reg, alias)
	kID, reg := m.resolveOrCreateURLKey(reg, name)
	vID := m.internValue(reg, value)
	idx := uint32(tID)*reg.URLs.Stride + uint32(kID)
	atomic.StoreUint32(&reg.URLs.Matrix[idx], vID)
	reg.Aliases = m.rebuildAliasTable()
	m.ensureTenantRecord(tID)
	rec := m.tenantData[tID]
	if rec.ServiceURLs == nil {
		rec.ServiceURLs = make(map[string]string)
	}
	rec.ServiceURLs[name] = string(value)
	State.Active.Store(reg)
	m.persistServiceURL(rec.Aliases[0], name, string(value))
}

// AddIdentifier sets a single identifier for the tenant identified by alias.
// name is the identifier key (e.g. "api_key", "client_id") without any prefix.
func (m *RegistryManager) AddIdentifier(alias string, name string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	reg := m.activeOrEmpty()
	tID, reg := m.resolveOrCreateTenant(reg, alias)
	kID, reg := m.resolveOrCreateIDKey(reg, name)
	vID := m.internValue(reg, value)
	idx := uint32(tID)*reg.IDs.Stride + uint32(kID)
	atomic.StoreUint32(&reg.IDs.Matrix[idx], vID)
	reg.Aliases = m.rebuildAliasTable()
	m.ensureTenantRecord(tID)
	rec := m.tenantData[tID]
	if rec.Identifiers == nil {
		rec.Identifiers = make(map[string]string)
	}
	rec.Identifiers[name] = string(value)
	State.Active.Store(reg)
	m.persistIdentifier(rec.Aliases[0], name, string(value))
}

// AddMeta sets a single metadata value for the tenant identified by alias.
// name is the metadata key (e.g. "tier", "region") without any prefix.
func (m *RegistryManager) AddMeta(alias string, name string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	reg := m.activeOrEmpty()
	tID, reg := m.resolveOrCreateTenant(reg, alias)
	kID, reg := m.resolveOrCreateMetaKey(reg, name)
	vID := m.internValue(reg, value)
	idx := uint32(tID)*reg.Meta.Stride + uint32(kID)
	atomic.StoreUint32(&reg.Meta.Matrix[idx], vID)
	reg.Aliases = m.rebuildAliasTable()
	m.ensureTenantRecord(tID)
	rec := m.tenantData[tID]
	if rec.Metadata == nil {
		rec.Metadata = make(map[string]string)
	}
	rec.Metadata[name] = string(value)
	State.Active.Store(reg)
	m.persistMetadata(rec.Aliases[0], name, string(value))
}

// DeleteServiceURL removes a service URL for the tenant identified by alias.
// If the tenant or key does not exist, this is a no-op.
// name is the URL key (e.g. "primary", "fallback") without any prefix.
func (m *RegistryManager) DeleteServiceURL(alias string, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	// Resolve alias → tenantID (no create)
	tID, found := m.aliasMap[alias]
	if !found {
		return
	}

	reg := m.activeOrEmpty()

	// Resolve key → keyID (no create)
	kID, found := findKeyID(reg.URLs.Keys, reg.URLs.StringPool, name)
	if !found {
		return
	}

	// Set matrix cell to 0 (delete the entry)
	idx := uint32(tID)*reg.URLs.Stride + uint32(kID)
	if idx < uint32(len(reg.URLs.Matrix)) {
		atomic.StoreUint32(&reg.URLs.Matrix[idx], 0)
	}

	// Remove from tenantData map
	m.ensureTenantRecord(tID)
	rec := m.tenantData[tID]
	if rec.ServiceURLs != nil {
		delete(rec.ServiceURLs, name)
	}

	State.Active.Store(reg)
	m.persistServiceURL(rec.Aliases[0], name, "")
}

// DeleteIdentifier removes an identifier for the tenant identified by alias.
// If the tenant or key does not exist, this is a no-op.
// name is the identifier key (e.g. "api_key", "client_id") without any prefix.
func (m *RegistryManager) DeleteIdentifier(alias string, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	// Resolve alias → tenantID (no create)
	tID, found := m.aliasMap[alias]
	if !found {
		return
	}

	reg := m.activeOrEmpty()

	// Resolve key → keyID (no create)
	kID, found := findKeyID(reg.IDs.Keys, reg.IDs.StringPool, name)
	if !found {
		return
	}

	// Set matrix cell to 0 (delete the entry)
	idx := uint32(tID)*reg.IDs.Stride + uint32(kID)
	if idx < uint32(len(reg.IDs.Matrix)) {
		atomic.StoreUint32(&reg.IDs.Matrix[idx], 0)
	}

	// Remove from tenantData map
	m.ensureTenantRecord(tID)
	rec := m.tenantData[tID]
	if rec.Identifiers != nil {
		delete(rec.Identifiers, name)
	}

	State.Active.Store(reg)
	m.persistIdentifier(rec.Aliases[0], name, "")
}

// DeleteMeta removes a metadata value for the tenant identified by alias.
// If the tenant or key does not exist, this is a no-op.
// name is the metadata key (e.g. "tier", "region") without any prefix.
func (m *RegistryManager) DeleteMeta(alias string, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	// Resolve alias → tenantID (no create)
	tID, found := m.aliasMap[alias]
	if !found {
		return
	}

	reg := m.activeOrEmpty()

	// Resolve key → keyID (no create)
	kID, found := findKeyID(reg.Meta.Keys, reg.Meta.StringPool, name)
	if !found {
		return
	}

	// Set matrix cell to 0 (delete the entry)
	idx := uint32(tID)*reg.Meta.Stride + uint32(kID)
	if idx < uint32(len(reg.Meta.Matrix)) {
		atomic.StoreUint32(&reg.Meta.Matrix[idx], 0)
	}

	// Remove from tenantData map
	m.ensureTenantRecord(tID)
	rec := m.tenantData[tID]
	if rec.Metadata != nil {
		delete(rec.Metadata, name)
	}

	State.Active.Store(reg)
	m.persistMetadata(rec.Aliases[0], name, "")
}

// ensureTenantRecord ensures m.tenantData[tID] exists, populating aliases from aliasMap.
// Must be called under the manager mutex.
func (m *RegistryManager) ensureTenantRecord(tID uint16) {
	if m.tenantData[tID] != nil {
		return
	}
	aliases := make([]string, 0, 1)
	for a, id := range m.aliasMap {
		if id == tID {
			aliases = append(aliases, a)
		}
	}
	m.tenantData[tID] = &TenantRecord{Aliases: aliases}
}

// AddAlias links a new alias to an existing tenant identified by existingAlias.
// If existingAlias is not found, a new tenant is created with both aliases.
func (m *RegistryManager) AddAlias(existingAlias string, newAlias string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	reg := m.activeOrEmpty()

	tID, found := m.aliasMap[existingAlias]
	if !found {
		// Tenant doesn't exist yet — create it with both aliases.
		tID, reg = m.resolveOrCreateTenant(reg, existingAlias)
	}

	m.aliasMap[newAlias] = tID
	reg.Aliases = m.rebuildAliasTable()

	// Ensure tenantData record exists and contains both aliases.
	rec := m.tenantData[tID]
	if rec == nil {
		rec = &TenantRecord{}
		m.tenantData[tID] = rec
	}
	// Rebuild alias list from aliasMap (source of truth).
	rec.Aliases = rec.Aliases[:0]
	for a, id := range m.aliasMap {
		if id == tID {
			rec.Aliases = append(rec.Aliases, a)
		}
	}

	State.Active.Store(reg)
	m.persistTenantAliases(rec.Aliases[0], rec.Aliases)
}

// UpsertTenantState applies a batch of service URL, identifier, and metadata
// updates for a tenant. All three maps are optional — pass nil to skip.
func (m *RegistryManager) UpsertTenantState(
	aliases []string,
	serviceURLs map[string]string,
	identifiers map[string]string,
	metadata map[string]string,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	if len(aliases) == 0 {
		return
	}

	reg := m.activeOrEmpty()
	tID, reg := m.resolveOrCreateTenant(reg, aliases[0])

	// Register additional aliases to the same tenant.
	for i := 1; i < len(aliases); i++ {
		m.aliasMap[aliases[i]] = tID
	}

	for k, v := range serviceURLs {
		kID, r := m.resolveOrCreateURLKey(reg, k)
		reg = r
		vID := m.internValue(reg, []byte(v))
		idx := uint32(tID)*reg.URLs.Stride + uint32(kID)
		atomic.StoreUint32(&reg.URLs.Matrix[idx], vID)
	}

	for k, v := range identifiers {
		kID, r := m.resolveOrCreateIDKey(reg, k)
		reg = r
		vID := m.internValue(reg, []byte(v))
		idx := uint32(tID)*reg.IDs.Stride + uint32(kID)
		atomic.StoreUint32(&reg.IDs.Matrix[idx], vID)
	}

	for k, v := range metadata {
		kID, r := m.resolveOrCreateMetaKey(reg, k)
		reg = r
		vID := m.internValue(reg, []byte(v))
		idx := uint32(tID)*reg.Meta.Stride + uint32(kID)
		atomic.StoreUint32(&reg.Meta.Matrix[idx], vID)
	}

	reg.Aliases = m.rebuildAliasTable()

	// Merge into existing tenantData record — stores accumulate so mirror must too.
	existing := m.tenantData[tID]
	if existing == nil {
		existing = &TenantRecord{}
	}
	// Rebuild alias list from aliasMap (source of truth for aliases).
	existing.Aliases = existing.Aliases[:0]
	for alias, id := range m.aliasMap {
		if id == tID {
			existing.Aliases = append(existing.Aliases, alias)
		}
	}
	// Merge maps — don't replace; preserves keys set in prior calls.
	for k, v := range serviceURLs {
		if existing.ServiceURLs == nil {
			existing.ServiceURLs = make(map[string]string)
		}
		existing.ServiceURLs[k] = v
	}
	for k, v := range identifiers {
		if existing.Identifiers == nil {
			existing.Identifiers = make(map[string]string)
		}
		existing.Identifiers[k] = v
	}
	for k, v := range metadata {
		if existing.Metadata == nil {
			existing.Metadata = make(map[string]string)
		}
		existing.Metadata[k] = v
	}
	m.tenantData[tID] = existing

	State.Active.Store(reg)
	m.persistTenant(*existing)
}

// DeleteTenant removes all aliases and data for the tenant identified by alias.
func (m *RegistryManager) DeleteTenant(alias string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	tID, found := m.aliasMap[alias]
	if !found {
		return
	}

	// Capture the primary alias (aliases[0]) before deleting — needed for store.
	var primaryAlias string
	if rec := m.tenantData[tID]; rec != nil && len(rec.Aliases) > 0 {
		primaryAlias = rec.Aliases[0]
	} else {
		primaryAlias = alias // fallback: use the alias we were given
	}

	// Remove every alias that points to this tenant.
	for a, id := range m.aliasMap {
		if id == tID {
			delete(m.aliasMap, a)
		}
	}

	reg := m.activeOrEmpty()

	// Zero out the tenant's property rows in all three stores.
	zeroStoreRow := func(store *PropStore, tid uint16) {
		if store.Stride == 0 {
			return
		}
		start := uint32(tid) * store.Stride
		if start+store.Stride > uint32(len(store.Matrix)) {
			return
		}
		for i := uint32(0); i < store.Stride; i++ {
			atomic.StoreUint32(&store.Matrix[start+i], 0)
		}
	}
	zeroStoreRow(&reg.URLs, tID)
	zeroStoreRow(&reg.IDs, tID)
	zeroStoreRow(&reg.Meta, tID)

	// Recycle the TenantID for future tenants.
	State.FreeSlots = append(State.FreeSlots, tID)

	reg.Aliases = m.rebuildAliasTable()
	State.Active.Store(reg)
	delete(m.tenantData, tID)
	m.removeSortedTenantID(tID)
	m.persistDeleteTenant(primaryAlias)
}

// ─── Test Tenant Support ───────────────────────────────────────────────────────

// RegisterTestTenant allocates an ephemeral test tenant in the reserved ID range
// (TestTenantRangeStart–TestTenantRangeEnd). The tenant is registered in memory
// only — it is never persisted to the datastore. The alias is "__test__"+runID.
// IDs wrap around to TestTenantRangeStart when the range is exhausted.
func (m *RegistryManager) RegisterTestTenant(runID string) (alias string, tenantID uint16, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	// Initialise the counter on first use.
	if m.nextTestTenantID == 0 {
		m.nextTestTenantID = TestTenantRangeStart
	}

	tID := m.nextTestTenantID
	// Advance and wrap around within the test range.
	if m.nextTestTenantID >= TestTenantRangeEnd {
		m.nextTestTenantID = TestTenantRangeStart
	} else {
		m.nextTestTenantID++
	}

	al := "__test__" + runID
	m.aliasMap[al] = tID
	m.insertSortedTenantID(tID)

	now := time.Now().Unix()
	m.tenantData[tID] = &TenantRecord{
		Aliases:   []string{al},
		CreatedAt: now,
	}

	reg := m.activeOrEmpty()

	// Grow per-tenant slices so the new ID is in-bounds.
	needed := int(tID) + 1
	if needed > len(reg.TenantModifiers) {
		grown := make([]TenantRateLimitModifier, needed)
		copy(grown, reg.TenantModifiers)
		reg.TenantModifiers = grown
	}
	if needed > len(reg.TenantRateLimits) {
		grown := make([]*TenantRateLimitTable, needed)
		copy(grown, reg.TenantRateLimits)
		reg.TenantRateLimits = grown
	}
	if needed > int(reg.MaxTenants) {
		newMax := uint16(needed + 127)
		if reg.URLs.Stride > 0 {
			reg.URLs = expandPropStore(reg.URLs, newMax, reg.URLs.Stride)
		}
		if reg.IDs.Stride > 0 {
			reg.IDs = expandPropStore(reg.IDs, newMax, reg.IDs.Stride)
		}
		if reg.Meta.Stride > 0 {
			reg.Meta = expandPropStore(reg.Meta, newMax, reg.Meta.Stride)
		}
		reg.MaxTenants = newMax
	}

	reg.Aliases = m.rebuildAliasTable()
	State.Active.Store(reg)

	return al, tID, nil
}

// DeleteTestTenant removes the ephemeral test tenant identified by runID.
// It delegates to DeleteTenant using the computed alias "__test__"+runID.
func (m *RegistryManager) DeleteTestTenant(runID string) error {
	m.DeleteTenant("__test__" + runID)
	return nil
}

// StartTestTenantSweep starts a background goroutine that removes test tenants
// older than maxAgeSeconds. It checks every 60 seconds and stops when ctx is done.
func (m *RegistryManager) StartTestTenantSweep(ctx context.Context, maxAgeSeconds int64) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.sweepExpiredTestTenants(maxAgeSeconds)
			}
		}
	}()
}

// sweepExpiredTestTenants removes all test tenants whose CreatedAt is older than
// maxAgeSeconds. Must NOT be called while the manager mutex is held.
func (m *RegistryManager) sweepExpiredTestTenants(maxAgeSeconds int64) {
	cutoff := time.Now().Unix() - maxAgeSeconds

	m.mu.Lock()
	var expiredAliases []string
	for _, tID := range m.tenantIDs {
		if tID < TestTenantRangeStart || tID > TestTenantRangeEnd {
			continue
		}
		rec := m.tenantData[tID]
		if rec == nil || rec.CreatedAt == 0 {
			continue
		}
		if rec.CreatedAt < cutoff && len(rec.Aliases) > 0 {
			expiredAliases = append(expiredAliases, rec.Aliases[0])
		}
	}
	m.mu.Unlock()

	// DeleteTenant acquires the mutex itself; call outside the lock.
	for _, al := range expiredAliases {
		m.DeleteTenant(al)
	}
}

// SetRateLimitConfig upserts a rate limit config at the given RateLimitConfigId.
// Grows the RateLimitConfigs slice if the ID is beyond the current length.
func (m *RegistryManager) SetRateLimitConfig(configID uint16, p RateLimitConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	reg := m.activeOrEmpty()
	needed := int(configID) + 1
	if needed > len(reg.RateLimitConfigs) {
		grown := make([]RateLimitConfig, needed)
		copy(grown, reg.RateLimitConfigs)
		reg.RateLimitConfigs = grown
	}
	reg.RateLimitConfigs[configID] = p
	State.Active.Store(reg)
}

// SetTenantRateLimitModifier sets the scale percentage and flags for a tenant.
func (m *RegistryManager) SetTenantRateLimitModifier(tenantID uint16, mod TenantRateLimitModifier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	reg := m.activeOrEmpty()
	needed := int(tenantID) + 1
	if needed > len(reg.TenantModifiers) {
		grown := make([]TenantRateLimitModifier, needed)
		copy(grown, reg.TenantModifiers)
		reg.TenantModifiers = grown
	}
	reg.TenantModifiers[tenantID] = mod
	State.Active.Store(reg)
}

// UpsertTenantRateLimitOverride inserts or updates a single rate limit override for a tenant.
// policyID may be an API-level or endpoint-level RateLimitConfigId.
func (m *RegistryManager) UpsertTenantRateLimitOverride(tenantID, policyID uint16, entry TenantRateLimitEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	reg := m.activeOrEmpty()

	// Grow TenantRateLimits slice if needed.
	needed := int(tenantID) + 1
	if needed > len(reg.TenantRateLimits) {
		grown := make([]*TenantRateLimitTable, needed)
		copy(grown, reg.TenantRateLimits)
		reg.TenantRateLimits = grown
	}

	tbl := reg.TenantRateLimits[tenantID]
	if tbl == nil {
		tbl = &TenantRateLimitTable{}
		reg.TenantRateLimits[tenantID] = tbl
	}

	// Insert in sorted order (binary search + splice).
	lo, hi := 0, len(tbl.PolicyIDs)-1
	insertAt := len(tbl.PolicyIDs)
	for lo <= hi {
		mid := (lo + hi) >> 1
		if tbl.PolicyIDs[mid] == policyID {
			tbl.Entries[mid] = entry // update existing
			State.Active.Store(reg)
			return
		} else if tbl.PolicyIDs[mid] < policyID {
			lo = mid + 1
		} else {
			hi = mid - 1
			insertAt = mid
		}
	}

	// Insert new entry at insertAt.
	tbl.PolicyIDs = append(tbl.PolicyIDs, 0)
	copy(tbl.PolicyIDs[insertAt+1:], tbl.PolicyIDs[insertAt:])
	tbl.PolicyIDs[insertAt] = policyID

	tbl.Entries = append(tbl.Entries, TenantRateLimitEntry{})
	copy(tbl.Entries[insertAt+1:], tbl.Entries[insertAt:])
	tbl.Entries[insertAt] = entry

	State.Active.Store(reg)
}

// UpsertNamedRateLimitConfig creates or updates a named rate limit config.
// Returns the stable RateLimitConfigId (auto-assigned on first insert).
func (m *RegistryManager) UpsertNamedRateLimitConfig(name string, cfg RateLimitConfig) uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	if id, exists := m.rateLimitNames[name]; exists {
		reg := m.activeOrEmpty()
		if int(id) < len(reg.RateLimitConfigs) {
			reg.RateLimitConfigs[id] = cfg
		}
		State.Active.Store(reg)
		m.persistRateLimitConfig(name, cfg)
		return id
	}

	m.nextRLConfigID++
	id := m.nextRLConfigID
	m.rateLimitNames[name] = id

	reg := m.activeOrEmpty()
	needed := int(id) + 1
	if needed > len(reg.RateLimitConfigs) {
		grown := make([]RateLimitConfig, needed)
		copy(grown, reg.RateLimitConfigs)
		reg.RateLimitConfigs = grown
	}
	reg.RateLimitConfigs[id] = cfg
	State.Active.Store(reg)
	m.persistRateLimitConfig(name, cfg)
	return id
}

// GetRateLimitConfigId resolves a rate limit config name to its ID.
// Returns (0, false) if the name is not registered.
func (m *RegistryManager) GetRateLimitConfigId(name string) (uint16, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()
	id, ok := m.rateLimitNames[name]
	return id, ok
}

// TenantTraceSampleRate returns the effective trace sample rate override for a
// tenant, satisfying the observability.TenantTracer interface.
//
// Returns (1.0, true) when DebugEnabled is set.
// Returns (override, true) when TraceSampleRateOverride > 0.
// Returns (0, false) when the tenant is not found or has no override.
func (m *RegistryManager) TenantTraceSampleRate(tenantID uint16) (float64, bool) {
	m.mu.Lock()
	rec := m.tenantData[tenantID]
	m.mu.Unlock()
	if rec == nil {
		return 0, false
	}
	if rec.DebugEnabled {
		return 1.0, true
	}
	if rec.TraceSampleRateOverride > 0 {
		return rec.TraceSampleRateOverride, true
	}
	return 0, false
}

// SetTenantDebug enables or disables debug mode for a tenant.
// When enabled: sets DebugEnabled=true, LogLevel="debug", TraceSampleRateOverride=1.0.
// When disabled: clears all three fields (reverts to gateway defaults).
// The change is persisted via the existing Store mechanism if a store is wired.
// Returns an error if the tenant alias is not found.
func (m *RegistryManager) SetTenantDebug(alias string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	tID, found := m.aliasMap[alias]
	if !found {
		return fmt.Errorf("tenant not found: %s", alias)
	}
	m.ensureTenantRecord(tID)
	rec := m.tenantData[tID]
	if enabled {
		rec.DebugEnabled = true
		rec.LogLevel = "debug"
		rec.TraceSampleRateOverride = 1.0
	} else {
		rec.DebugEnabled = false
		rec.LogLevel = ""
		rec.TraceSampleRateOverride = 0
	}
	m.persistTenant(*rec)
	return nil
}

// SetTenantLogLevel sets an explicit log level override for a tenant without
// enabling full debug mode. level must be one of "debug","info","warn","error","".
// Empty string clears the override.
// Returns an error if the tenant alias is not found or the level value is invalid.
func (m *RegistryManager) SetTenantLogLevel(alias string, level string) error {
	switch level {
	case "debug", "info", "warn", "error", "":
		// valid
	default:
		return fmt.Errorf("invalid log level %q: must be one of debug, info, warn, error or empty", level)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	tID, found := m.aliasMap[alias]
	if !found {
		return fmt.Errorf("tenant not found: %s", alias)
	}
	m.ensureTenantRecord(tID)
	rec := m.tenantData[tID]
	rec.LogLevel = level
	m.persistTenant(*rec)
	return nil
}

// SetStore registers the RegistryDatastore used for persistence after every
// mutation. Call this after RestoreFromSnapshot so startup reads do not
// trigger redundant writes back to the store.
func (m *RegistryManager) SetStore(s RegistryDatastore) {
	m.mu.Lock()
	m.store = s
	m.mu.Unlock()
}

// RestoreFromSnapshot replays a persisted snapshot through the normal write
// path. Called at startup before SetStore so no redundant writes occur.
func (m *RegistryManager) RestoreFromSnapshot(snap RegistrySnapshot) {
	for _, rl := range snap.RateLimits {
		m.UpsertNamedRateLimitConfig(rl.Name, rl.Config)
	}
	for _, t := range snap.Tenants {
		if len(t.Aliases) == 0 {
			continue
		}
		m.UpsertTenantState(t.Aliases, t.ServiceURLs, t.Identifiers, t.Metadata)
	}
}

// persistTenant asynchronously writes the updated tenant record to the store.
// Must be called while the mutex is held (copies the record before releasing).
func (m *RegistryManager) persistTenant(rec TenantRecord) {
	if m.store == nil || len(rec.Aliases) == 0 {
		return
	}
	s := m.store
	primary := rec.Aliases[0]
	aliases := append([]string(nil), rec.Aliases...)
	urls := copyStrMap(rec.ServiceURLs)
	ids := copyStrMap(rec.Identifiers)
	meta := copyStrMap(rec.Metadata)
	go func() {
		ctx := context.Background()
		_ = s.PutTenantAliases(ctx, primary, aliases)
		for k, v := range urls {
			_ = s.PutServiceURL(ctx, primary, k, v)
		}
		for k, v := range ids {
			_ = s.PutIdentifier(ctx, primary, k, v)
		}
		for k, v := range meta {
			_ = s.PutMetadata(ctx, primary, k, v)
		}
	}()
}

// persistTenantBatch asynchronously writes all properties for a tenant in a
// single MultiPut round-trip. Falls back to persistTenant if the store does
// not implement PutBatch (i.e. is not a *TenantRegistryStore).
// Must be called while the mutex is held (copies data before releasing).
func (m *RegistryManager) persistTenantBatch(rec TenantRecord) {
	if m.store == nil || len(rec.Aliases) == 0 {
		return
	}
	s, ok := m.store.(*TenantRegistryStore)
	if !ok {
		m.persistTenant(rec)
		return
	}
	primary := rec.Aliases[0]
	urls := copyStrMap(rec.ServiceURLs)
	ids := copyStrMap(rec.Identifiers)
	meta := copyStrMap(rec.Metadata)
	go func() {
		_ = s.PutBatch(context.Background(), primary, urls, ids, meta)
	}()
}

// persistTenantAlias asynchronously writes only the alias list (used when an
// alias is added without touching properties).
func (m *RegistryManager) persistTenantAliases(primary string, aliases []string) {
	if m.store == nil {
		return
	}
	s := m.store
	cp := append([]string(nil), aliases...)
	go func() { _ = s.PutTenantAliases(context.Background(), primary, cp) }()
}

// persistServiceURL asynchronously writes a single URL entry.
func (m *RegistryManager) persistServiceURL(primary, key, value string) {
	if m.store == nil {
		return
	}
	s := m.store
	go func() { _ = s.PutServiceURL(context.Background(), primary, key, value) }()
}

// persistIdentifier asynchronously writes a single identifier entry.
func (m *RegistryManager) persistIdentifier(primary, key, value string) {
	if m.store == nil {
		return
	}
	s := m.store
	go func() { _ = s.PutIdentifier(context.Background(), primary, key, value) }()
}

// persistMetadata asynchronously writes a single metadata entry.
func (m *RegistryManager) persistMetadata(primary, key, value string) {
	if m.store == nil {
		return
	}
	s := m.store
	go func() { _ = s.PutMetadata(context.Background(), primary, key, value) }()
}

// persistDeleteTenant asynchronously removes a tenant from the store.
func (m *RegistryManager) persistDeleteTenant(primary string) {
	if m.store == nil {
		return
	}
	s := m.store
	go func() { _ = s.DeleteTenant(context.Background(), primary) }()
}

// persistRateLimitConfig asynchronously writes a rate limit config.
func (m *RegistryManager) persistRateLimitConfig(name string, cfg RateLimitConfig) {
	if m.store == nil {
		return
	}
	s := m.store
	go func() { _ = s.PutRateLimitConfig(context.Background(), name, cfg) }()
}

// copyStrMap returns a shallow copy of a string map (safe to use after mutex release).
func copyStrMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// resolveOrCreateTenant finds or allocates a TenantID for the given alias.
func (m *RegistryManager) resolveOrCreateTenant(reg *TenantRegistry, alias string) (uint16, *TenantRegistry) {
	if id, found := m.aliasMap[alias]; found {
		return id, reg
	}

	var tID uint16
	if len(State.FreeSlots) > 0 {
		last := len(State.FreeSlots) - 1
		tID = State.FreeSlots[last]
		State.FreeSlots = State.FreeSlots[:last]
	} else {
		tID = uint16(atomic.AddUint32(&nextAvailableTenantID, 1) - 1)
	}
	m.aliasMap[alias] = tID
	m.insertSortedTenantID(tID)

	// Grow per-tenant slices if this TenantID exceeds current capacity.
	needed := int(tID) + 1
	if needed > len(reg.TenantModifiers) {
		grown := make([]TenantRateLimitModifier, needed)
		copy(grown, reg.TenantModifiers)
		reg.TenantModifiers = grown
	}
	if needed > len(reg.TenantRateLimits) {
		grown := make([]*TenantRateLimitTable, needed)
		copy(grown, reg.TenantRateLimits)
		reg.TenantRateLimits = grown
	}

	// If this TenantID would be out of bounds for the property store matrices,
	// grow all three stores to accommodate the new tenant row.
	if needed > int(reg.MaxTenants) {
		newMax := uint16(needed + 127)
		if reg.URLs.Stride > 0 {
			reg.URLs = expandPropStore(reg.URLs, newMax, reg.URLs.Stride)
		}
		if reg.IDs.Stride > 0 {
			reg.IDs = expandPropStore(reg.IDs, newMax, reg.IDs.Stride)
		}
		if reg.Meta.Stride > 0 {
			reg.Meta = expandPropStore(reg.Meta, newMax, reg.Meta.Stride)
		}
		reg.MaxTenants = newMax
	}

	return tID, reg
}

// expandPropStore creates a new PropStore with an increased stride, migrating
// existing matrix data. maxTenants is the current row capacity.
func expandPropStore(old PropStore, maxTenants uint16, newStride uint32) PropStore {
	newMatrix := make([]uint32, uint32(maxTenants)*newStride)
	if old.Stride > 0 {
		for tID := uint32(0); tID < uint32(maxTenants); tID++ {
			oldStart := tID * old.Stride
			newStart := tID * newStride
			if oldStart+old.Stride <= uint32(len(old.Matrix)) {
				copy(newMatrix[newStart:newStart+old.Stride], old.Matrix[oldStart:])
			}
		}
	}
	return PropStore{
		Matrix:     newMatrix,
		Stride:     newStride,
		Keys:       old.Keys,
		StringPool: old.StringPool,
	}
}

// resolveOrCreateStoreKey finds or allocates a KeyID for key in the given store.
// Returns (keyID, needsExpand). If needsExpand is true, the store's matrix must
// be expanded to accommodate the new column before storing any value.
func resolveOrCreateStoreKey(store *PropStore, key string) (keyID uint16, needsExpand bool) {
	if kID, found := findKeyID(store.Keys, store.StringPool, key); found {
		return kID, false
	}
	return uint16(store.Stride), true
}

func (m *RegistryManager) resolveOrCreateURLKey(reg *TenantRegistry, key string) (uint16, *TenantRegistry) {
	kID, needsExpand := resolveOrCreateStoreKey(&reg.URLs, key)
	if !needsExpand {
		return kID, reg
	}
	maxTenants := reg.MaxTenants
	if maxTenants == 0 {
		maxTenants = 128
	}
	newReg := *reg
	newReg.MaxTenants = maxTenants
	newReg.URLs = expandPropStore(reg.URLs, maxTenants, reg.URLs.Stride+1)
	m.insertIntoPropertiesRadix(&newReg.URLs.Keys, &newReg.URLs.StringPool, key, uint32(kID))
	return kID, &newReg
}

func (m *RegistryManager) resolveOrCreateIDKey(reg *TenantRegistry, key string) (uint16, *TenantRegistry) {
	kID, needsExpand := resolveOrCreateStoreKey(&reg.IDs, key)
	if !needsExpand {
		return kID, reg
	}
	maxTenants := reg.MaxTenants
	if maxTenants == 0 {
		maxTenants = 128
	}
	newReg := *reg
	newReg.MaxTenants = maxTenants
	newReg.IDs = expandPropStore(reg.IDs, maxTenants, reg.IDs.Stride+1)
	m.insertIntoPropertiesRadix(&newReg.IDs.Keys, &newReg.IDs.StringPool, key, uint32(kID))
	return kID, &newReg
}

func (m *RegistryManager) resolveOrCreateMetaKey(reg *TenantRegistry, key string) (uint16, *TenantRegistry) {
	kID, needsExpand := resolveOrCreateStoreKey(&reg.Meta, key)
	if !needsExpand {
		return kID, reg
	}
	maxTenants := reg.MaxTenants
	if maxTenants == 0 {
		maxTenants = 128
	}
	newReg := *reg
	newReg.MaxTenants = maxTenants
	newReg.Meta = expandPropStore(reg.Meta, maxTenants, reg.Meta.Stride+1)
	m.insertIntoPropertiesRadix(&newReg.Meta.Keys, &newReg.Meta.StringPool, key, uint32(kID))
	return kID, &newReg
}

// internValue deduplicates a value in the ValuePool (management plane only).
func (m *RegistryManager) internValue(reg *TenantRegistry, value []byte) uint32 {
	if len(value) == 0 {
		return 0
	}
	// Sentinel: index 0 means "not set" (matches the zero-initialised matrix).
	// Real values start at index 1.
	if len(reg.ValuePool) == 0 {
		reg.ValuePool = append(reg.ValuePool, nil)
	}
	for i, existing := range reg.ValuePool {
		if bytes.Equal(existing, value) {
			return uint32(i)
		}
	}
	newID := uint32(len(reg.ValuePool))
	reg.ValuePool = append(reg.ValuePool, value)
	return newID
}

// insertIntoPropertiesRadix appends a new leaf to the flat Properties radix arena.
func (m *RegistryManager) insertIntoPropertiesRadix(nodes *[]RegistryNode, pool *[]byte, key string, val uint32) {
	input := []byte(key)
	if len(*nodes) == 0 {
		// Root node: ChildBase=1 so children start at index 1 (not 0=self).
		*nodes = append(*nodes, RegistryNode{ChildBase: 1})
	}
	offset := uint32(len(*pool))
	*pool = append(*pool, input...)
	newNode := RegistryNode{
		PrefixOffset: offset,
		PrefixLen:    uint16(len(input)),
		Value:        uint16(val),
	}
	*nodes = append(*nodes, newNode)
	(*nodes)[0].ChildCount++
}

// rebuildAliasTable constructs an immutable AliasTable from the current aliasMap.
// Called whenever the alias set changes; management plane only (under mutex).
func (m *RegistryManager) rebuildAliasTable() AliasTable {
	n := len(m.aliasMap)
	if n == 0 {
		return AliasTable{Seed: m.seed}
	}

	// Capacity: next power-of-2 above n/0.75 to keep load factor ≤75%.
	size := nextPow2(uint32(float64(n)/0.75) + 1)
	if size < 8 {
		size = 8
	}
	mask := size - 1

	totalBytes := 0
	for alias := range m.aliasMap {
		totalBytes += len(alias)
	}

	slots := make([]AliasSlot, size)
	arena := make([]byte, 0, totalBytes)

	for alias, tenantID := range m.aliasMap {
		h := maphash.String(m.seed, alias)
		idx := uint32(h) & mask
		for slots[idx].TenantID != 0 {
			idx = (idx + 1) & mask // linear probe past occupied slots
		}
		offset := uint32(len(arena))
		arena = append(arena, alias...)
		slots[idx] = AliasSlot{
			Hash:     h,
			Offset:   offset,
			Len:      uint16(len(alias)),
			TenantID: tenantID,
		}
	}

	return AliasTable{
		Slots:       slots,
		Mask:        mask,
		StringArena: arena,
		Seed:        m.seed,
	}
}

// activeOrEmpty returns a shallow clone of the current active registry, or a
// fresh empty one. The clone ensures all field assignments (reg.Aliases = ...,
// reg.TenantModifiers = grown, etc.) target the private copy — never the live
// snapshot that concurrent readers may be accessing. The final
// State.Active.Store(reg) atomically publishes the fully-mutated copy.
//
// NOTE: reg.PropMatrix is a slice whose backing array is still shared with the
// live snapshot. Element writes to PropMatrix must use atomic.StoreUint32.
func (m *RegistryManager) activeOrEmpty() *TenantRegistry {
	if live := State.Active.Load(); live != nil {
		clone := *live // shallow struct copy — we own this allocation
		return &clone
	}
	return &TenantRegistry{
		RateLimitConfigs: make([]RateLimitConfig, 1),           // index 0 = system baseline
		TenantModifiers:  make([]TenantRateLimitModifier, 128), // pre-allocate for 128 tenants
		TenantRateLimits: make([]*TenantRateLimitTable, 128),
		MaxTenants:       128,
		// URLs, IDs, Meta are zero — matrices will be allocated on first key addition.
	}
}

// nextPow2 returns the smallest power of 2 ≥ n.
func nextPow2(n uint32) uint32 {
	if n == 0 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n++
	return n
}

// ─── Remote event application ──────────────────────────────────────────────────

// ApplyStorePut applies a raw key-value pair received from a remote KindDBPut
// ingest event to the in-memory registry state WITHOUT persisting back to the
// store. rawKey is the unstored/domain-unscoped key as written by
// TenantRegistryStore (e.g. "tenant:acme:url:primary", "rl:default").
//
// Call this from the ingest consumer that handles KindDBPut events for the
// tenant_registry domain so that changes made on one gateway instance are
// reflected in the local in-memory TenantRegistry on all other instances.
func (m *RegistryManager) ApplyStorePut(rawKey string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	switch {
	case strings.HasPrefix(rawKey, tenantKeyPrefix):
		// "tenant:{primaryAlias}:..."
		rest := strings.TrimPrefix(rawKey, tenantKeyPrefix)

		if strings.HasSuffix(rest, aliasSuffix) {
			// "tenant:{primaryAlias}:aliases" → JSON []string
			primary := strings.TrimSuffix(rest, aliasSuffix)
			var aliases []string
			if json.Unmarshal(value, &aliases) != nil || len(aliases) == 0 {
				return
			}
			reg := m.activeOrEmpty()
			tID, reg := m.resolveOrCreateTenant(reg, primary)
			for _, a := range aliases[1:] {
				if _, exists := m.aliasMap[a]; !exists {
					m.aliasMap[a] = tID
				}
			}
			m.ensureTenantRecord(tID)
			rec := m.tenantData[tID]
			rec.Aliases = append(rec.Aliases[:0], aliases...)
			reg.Aliases = m.rebuildAliasTable()
			State.Active.Store(reg)
			return
		}

		if idx := strings.Index(rest, urlPropPrefix); idx >= 0 {
			primary, propKey := rest[:idx], rest[idx+len(urlPropPrefix):]
			m.applyURLProp(primary, propKey, string(value))
			return
		}
		if idx := strings.Index(rest, idPropPrefix); idx >= 0 {
			primary, propKey := rest[:idx], rest[idx+len(idPropPrefix):]
			m.applyIDProp(primary, propKey, string(value))
			return
		}
		if idx := strings.Index(rest, metaPropPrefix); idx >= 0 {
			primary, propKey := rest[:idx], rest[idx+len(metaPropPrefix):]
			m.applyMetaProp(primary, propKey, string(value))
			return
		}

	case strings.HasPrefix(rawKey, rateLimitPrefix):
		// "rl:{name}" → JSON storedRateLimitConfig
		name := strings.TrimPrefix(rawKey, rateLimitPrefix)
		var stored storedRateLimitConfig
		if json.Unmarshal(value, &stored) != nil {
			return
		}
		cfg := RateLimitConfig{
			PerSec:      stored.PerSec,
			PerMin:      stored.PerMin,
			BurstFactor: stored.BurstFactor,
		}
		// Reuse the normal upsert logic but rely on the fact that m.store is
		// already set to nil during a "suppress" path, OR call the internal
		// helper that skips persist. Since the mutex is held we inline it.
		if id, exists := m.rateLimitNames[name]; exists {
			reg := m.activeOrEmpty()
			if int(id) < len(reg.RateLimitConfigs) {
				reg.RateLimitConfigs[id] = cfg
			}
			State.Active.Store(reg)
			return
		}
		m.nextRLConfigID++
		id := m.nextRLConfigID
		m.rateLimitNames[name] = id
		reg := m.activeOrEmpty()
		needed := int(id) + 1
		if needed > len(reg.RateLimitConfigs) {
			grown := make([]RateLimitConfig, needed)
			copy(grown, reg.RateLimitConfigs)
			reg.RateLimitConfigs = grown
		}
		reg.RateLimitConfigs[id] = cfg
		State.Active.Store(reg)
	}
}

// applyURLProp sets a single service URL for primaryAlias without persisting.
// Must be called under the manager mutex.
func (m *RegistryManager) applyURLProp(primary, key, value string) {
	reg := m.activeOrEmpty()
	tID, reg := m.resolveOrCreateTenant(reg, primary)
	kID, reg := m.resolveOrCreateURLKey(reg, key)
	vID := m.internValue(reg, []byte(value))
	idx := uint32(tID)*reg.URLs.Stride + uint32(kID)
	atomic.StoreUint32(&reg.URLs.Matrix[idx], vID)
	reg.Aliases = m.rebuildAliasTable()
	m.ensureTenantRecord(tID)
	rec := m.tenantData[tID]
	if rec.ServiceURLs == nil {
		rec.ServiceURLs = make(map[string]string)
	}
	rec.ServiceURLs[key] = value
	State.Active.Store(reg)
}

// applyIDProp sets a single identifier for primaryAlias without persisting.
// Must be called under the manager mutex.
func (m *RegistryManager) applyIDProp(primary, key, value string) {
	reg := m.activeOrEmpty()
	tID, reg := m.resolveOrCreateTenant(reg, primary)
	kID, reg := m.resolveOrCreateIDKey(reg, key)
	vID := m.internValue(reg, []byte(value))
	idx := uint32(tID)*reg.IDs.Stride + uint32(kID)
	atomic.StoreUint32(&reg.IDs.Matrix[idx], vID)
	reg.Aliases = m.rebuildAliasTable()
	m.ensureTenantRecord(tID)
	rec := m.tenantData[tID]
	if rec.Identifiers == nil {
		rec.Identifiers = make(map[string]string)
	}
	rec.Identifiers[key] = value
	State.Active.Store(reg)
}

// applyMetaProp sets a single metadata value for primaryAlias without persisting.
// Must be called under the manager mutex.
func (m *RegistryManager) applyMetaProp(primary, key, value string) {
	reg := m.activeOrEmpty()
	tID, reg := m.resolveOrCreateTenant(reg, primary)
	kID, reg := m.resolveOrCreateMetaKey(reg, key)
	vID := m.internValue(reg, []byte(value))
	idx := uint32(tID)*reg.Meta.Stride + uint32(kID)
	atomic.StoreUint32(&reg.Meta.Matrix[idx], vID)
	reg.Aliases = m.rebuildAliasTable()
	m.ensureTenantRecord(tID)
	rec := m.tenantData[tID]
	if rec.Metadata == nil {
		rec.Metadata = make(map[string]string)
	}
	rec.Metadata[key] = value
	State.Active.Store(reg)
}

// ApplyStoreDelete applies a raw key deletion received from a remote
// KindDBDelete ingest event to the in-memory registry state WITHOUT persisting.
// rawKey follows the same format as ApplyStorePut.
//
// For individual property keys (url/id/meta) no in-memory removal is performed
// because the PropStore matrix stores value IDs (0 = absent) — we zero the slot.
// For tenant aliases keys, the entire tenant record is removed from memory.
// For rate limit keys, the config slot is zeroed.
func (m *RegistryManager) ApplyStoreDelete(rawKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureInit()

	switch {
	case strings.HasPrefix(rawKey, tenantKeyPrefix):
		rest := strings.TrimPrefix(rawKey, tenantKeyPrefix)

		if strings.HasSuffix(rest, aliasSuffix) {
			// Deleting the aliases key means the whole tenant is gone.
			primary := strings.TrimSuffix(rest, aliasSuffix)
			tID, found := m.aliasMap[primary]
			if !found {
				return
			}
			for a, id := range m.aliasMap {
				if id == tID {
					delete(m.aliasMap, a)
				}
			}
			reg := m.activeOrEmpty()
			zeroRow := func(store *PropStore, tid uint16) {
				if store.Stride == 0 {
					return
				}
				start := uint32(tid) * store.Stride
				if start+store.Stride > uint32(len(store.Matrix)) {
					return
				}
				for i := uint32(0); i < store.Stride; i++ {
					atomic.StoreUint32(&store.Matrix[start+i], 0)
				}
			}
			zeroRow(&reg.URLs, tID)
			zeroRow(&reg.IDs, tID)
			zeroRow(&reg.Meta, tID)
			State.FreeSlots = append(State.FreeSlots, tID)
			reg.Aliases = m.rebuildAliasTable()
			State.Active.Store(reg)
			delete(m.tenantData, tID)
			m.removeSortedTenantID(tID)
			return
		}

		// Individual property delete: zero the matrix slot.
		if idx := strings.Index(rest, urlPropPrefix); idx >= 0 {
			primary, propKey := rest[:idx], rest[idx+len(urlPropPrefix):]
			if tID, ok := m.aliasMap[primary]; ok {
				reg := m.activeOrEmpty()
				if kID, ok := m.lookupURLKeyID(reg, propKey); ok {
					slot := uint32(tID)*reg.URLs.Stride + uint32(kID)
					atomic.StoreUint32(&reg.URLs.Matrix[slot], 0)
					State.Active.Store(reg)
				}
				if rec := m.tenantData[tID]; rec != nil {
					delete(rec.ServiceURLs, propKey)
				}
			}
			return
		}
		if idx := strings.Index(rest, idPropPrefix); idx >= 0 {
			primary, propKey := rest[:idx], rest[idx+len(idPropPrefix):]
			if tID, ok := m.aliasMap[primary]; ok {
				reg := m.activeOrEmpty()
				if kID, ok := m.lookupIDKeyID(reg, propKey); ok {
					slot := uint32(tID)*reg.IDs.Stride + uint32(kID)
					atomic.StoreUint32(&reg.IDs.Matrix[slot], 0)
					State.Active.Store(reg)
				}
				if rec := m.tenantData[tID]; rec != nil {
					delete(rec.Identifiers, propKey)
				}
			}
			return
		}
		if idx := strings.Index(rest, metaPropPrefix); idx >= 0 {
			primary, propKey := rest[:idx], rest[idx+len(metaPropPrefix):]
			if tID, ok := m.aliasMap[primary]; ok {
				reg := m.activeOrEmpty()
				if kID, ok := m.lookupMetaKeyID(reg, propKey); ok {
					slot := uint32(tID)*reg.Meta.Stride + uint32(kID)
					atomic.StoreUint32(&reg.Meta.Matrix[slot], 0)
					State.Active.Store(reg)
				}
				if rec := m.tenantData[tID]; rec != nil {
					delete(rec.Metadata, propKey)
				}
			}
			return
		}

	case strings.HasPrefix(rawKey, rateLimitPrefix):
		// Zero out the config slot (can't shrink slice safely without re-bake).
		name := strings.TrimPrefix(rawKey, rateLimitPrefix)
		if id, ok := m.rateLimitNames[name]; ok {
			delete(m.rateLimitNames, name)
			reg := m.activeOrEmpty()
			if int(id) < len(reg.RateLimitConfigs) {
				reg.RateLimitConfigs[id] = RateLimitConfig{}
			}
			State.Active.Store(reg)
		}
	}
}

// lookupURLKeyID returns the KeyID for propKey in the URLs PropStore, if known.
// Must be called under the manager mutex.
func (m *RegistryManager) lookupURLKeyID(reg *TenantRegistry, propKey string) (uint16, bool) {
	return findKeyID(reg.URLs.Keys, reg.URLs.StringPool, propKey)
}

// lookupIDKeyID returns the KeyID for propKey in the IDs PropStore, if known.
func (m *RegistryManager) lookupIDKeyID(reg *TenantRegistry, propKey string) (uint16, bool) {
	return findKeyID(reg.IDs.Keys, reg.IDs.StringPool, propKey)
}

// lookupMetaKeyID returns the KeyID for propKey in the Meta PropStore, if known.
func (m *RegistryManager) lookupMetaKeyID(reg *TenantRegistry, propKey string) (uint16, bool) {
	return findKeyID(reg.Meta.Keys, reg.Meta.StringPool, propKey)
}
