package registry

import (
	"bytes"
	"context"
	"hash/maphash"
	"sort"
	"sync"
	"sync/atomic"
)

// nextAvailableTenantID is a global counter for row allocation.
// Starts at 1; TenantID 0 is reserved as the "empty slot" sentinel in AliasTable.
var nextAvailableTenantID uint32 = 1

// RegistryManager coordinates the management plane of the gateway.
// A single Mutex serializes all writes; reads are lock-free via atomic snapshot.
type RegistryManager struct {
	mu             sync.Mutex
	aliasMap       map[string]uint16        // alias → TenantID; management-plane source of truth
	seed           maphash.Seed             // fixed per-process; used for all AliasTable builds
	rateLimitNames map[string]uint16        // name → RateLimitConfigId
	nextRLConfigID uint16                   // auto-increment; starts at 0, gateway default is 0
	tenantData     map[uint16]*TenantRecord // management-plane mirror; keyed by TenantID
	tenantIDs      []uint16                 // sorted slice of active TenantIDs; enables cursor pagination
	store          RegistryDatastore        // optional; nil = no persistence
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
