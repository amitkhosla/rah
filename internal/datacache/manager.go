package datacache

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrCollectionNotFound = errors.New("datacache: collection not found")
	ErrCollectionExists   = errors.New("datacache: collection already exists")
)

// AccessMode controls how a collection can be accessed.
type AccessMode uint8

const (
	AccessReadWrite AccessMode = 0
	AccessReadOnly  AccessMode = 1
)

// GrowthMode controls capacity growth behavior.
type GrowthMode uint8

const (
	GrowthAuto  GrowthMode = 0 // double capacity up to MaxCap on overflow
	GrowthFixed GrowthMode = 1 // fail with ErrArenaFull on overflow
)

// CollectionMeta holds the metadata for a named collection.
type CollectionMeta struct {
	PoolIdx  uint8
	Schema   Schema
	Access   AccessMode
	Growth   GrowthMode
	MaxCap   int
	QuotaKey quotaKey
	TTLSec   int
}

// CreateOpts are passed to Create.
type CreateOpts struct {
	TenantID uint16
	App      string
	Fields   []FieldDef // field definitions; baked into Schema at Create time
	Capacity int        // initial capacity (number of records)
	MaxCap   int        // max capacity for GrowthAuto; ignored for GrowthFixed
	Access   AccessMode
	Growth   GrowthMode
	TTLSec   int // 0 = no expiry
}

// DataCacheManager manages the lifecycle of named arena collections.
// Get is lock-free (sync.Map load). Create/Drop acquire sync.Map write path.
type DataCacheManager struct {
	pool   *ArenaPool
	quotas *QuotaRegistry
	ttl    *TTLCleaner
	named  sync.Map // map[string]*CollectionMeta
	dropMu sync.Mutex
}

// NewDataCacheManager creates a manager. cfg provides pool/quota configuration.
// Start() is called on the TTLCleaner.
func NewDataCacheManager(pool *ArenaPool, quotas *QuotaRegistry) *DataCacheManager {
	m := &DataCacheManager{
		pool:   pool,
		quotas: quotas,
	}
	cleaner := NewTTLCleaner(pool, quotas, &m.named)
	cleaner.Start()
	m.ttl = cleaner
	return m
}

// Create declares a new named collection. Returns ErrCollectionExists if name
// is already registered. Enforces quota before claiming a slot.
// Sets TTL on the slot if opts.TTLSec > 0.
// Stores the collection name in slot.name ([128]byte, null-padded, truncated if > 127 chars).
func (m *DataCacheManager) Create(name string, opts CreateOpts) error {
	// Check if already exists (lock-free load)
	if _, exists := m.named.Load(name); exists {
		return ErrCollectionExists
	}

	// Validate capacity
	if opts.Capacity > m.pool.maxRecords {
		return fmt.Errorf("datacache: capacity %d exceeds pool maxRecords %d", opts.Capacity, m.pool.maxRecords)
	}
	if opts.Capacity <= 0 {
		opts.Capacity = 1024
	}

	// Build schema from caller-provided fields
	schema := BakeSchema(opts.Fields, opts.Capacity)

	if int64(schema.BufSize) > m.pool.maxBufBytes {
		return fmt.Errorf("datacache: schema BufSize %d exceeds pool maxBufBytes %d", schema.BufSize, m.pool.maxBufBytes)
	}

	// Build quota key
	qk := makeQuotaKey(opts.TenantID, opts.App)

	// Reserve quota
	err := m.quotas.Reserve(opts.TenantID, opts.App, int64(schema.BufSize), 1)
	if err != nil {
		return err
	}

	// Claim slot from pool (non-blocking)
	poolIdx, err := m.pool.Claim(0)
	if err != nil {
		// Rollback quota
		m.quotas.Release(opts.TenantID, opts.App, int64(schema.BufSize), 1)
		return err
	}

	// Initialize slot
	m.pool.InitSlot(poolIdx, opts.Capacity, schema)

	// Set owner key
	slot := &m.pool.slots[poolIdx]
	slot.ownerKey = encodeOwnerKey(opts.TenantID, opts.App)

	// Set TTL if requested
	if opts.TTLSec > 0 {
		expUnix := time.Now().Unix() + int64(opts.TTLSec)
		slot.ttlExpUnix.Store(expUnix)
	}

	// Copy collection name to slot
	copy(slot.name[:], name)

	// Create metadata
	meta := &CollectionMeta{
		PoolIdx:  poolIdx,
		Schema:   schema,
		Access:   opts.Access,
		Growth:   opts.Growth,
		MaxCap:   opts.MaxCap,
		QuotaKey: qk,
		TTLSec:   opts.TTLSec,
	}

	// Store in named map
	m.named.Store(name, meta)

	return nil
}

// Drop removes a collection, releases its pool slot, and updates quotas.
// Returns ErrCollectionNotFound if name is not registered.
func (m *DataCacheManager) Drop(name string, tenantID uint16, app string) error {
	m.dropMu.Lock()
	defer m.dropMu.Unlock()

	// Load and delete from map
	val, loaded := m.named.LoadAndDelete(name)
	if !loaded {
		return ErrCollectionNotFound
	}

	meta := val.(*CollectionMeta)

	// Validate ownership
	expected := makeQuotaKey(tenantID, app)
	if meta.QuotaKey != expected {
		// Put it back — we didn't own it
		m.named.Store(name, meta)
		return fmt.Errorf("datacache: caller (tenant=%d app=%q) does not own collection %q", tenantID, app, name)
	}

	// Release pool slot
	m.pool.Release(meta.PoolIdx)

	// Release quota
	m.quotas.Release(tenantID, app, int64(meta.Schema.BufSize), 1)

	return nil
}

// Get returns the CollectionMeta for a name. Lock-free.
// Returns ErrCollectionNotFound if not registered.
func (m *DataCacheManager) Get(name string) (*CollectionMeta, error) {
	val, ok := m.named.Load(name)
	if !ok {
		return nil, ErrCollectionNotFound
	}
	return val.(*CollectionMeta), nil
}

// GetSlot is a convenience that returns the poolSlot pointer directly.
// Panics if idx is out of range (internal contract: only called with valid idx from CollectionMeta).
func (m *DataCacheManager) GetSlot(idx uint8) *poolSlot {
	return &m.pool.slots[idx]
}

// AppendJSONBatch parses a JSON array and appends all records to the named collection
// in a single allocation pass (one lock acquisition for all row indices).
func (m *DataCacheManager) AppendJSONBatch(name string, jsonArray []byte) error {
	meta, err := m.Get(name)
	if err != nil {
		return err
	}
	slot := &m.pool.slots[meta.PoolIdx]

	var rawRows []json.RawMessage
	if err := json.Unmarshal(jsonArray, &rawRows); err != nil {
		return fmt.Errorf("datacache: invalid JSON array: %w", err)
	}
	if len(rawRows) == 0 {
		return nil
	}

	// Parse all records first, then allocate rows in one shot.
	allVals := make([][]interface{}, len(rawRows))
	for i, raw := range rawRows {
		var obj map[string]interface{}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return fmt.Errorf("datacache: row %d: %w", i, err)
		}
		vals := make([]interface{}, len(meta.Schema.Fields))
		for j, f := range meta.Schema.Fields {
			v, ok := obj[f.Name]
			if !ok {
				switch f.Kind {
				case FieldFloat64:
					vals[j] = float64(0)
				case FieldInt64:
					vals[j] = int64(0)
				case FieldEnum:
					vals[j] = uint8(0)
				case FieldStringN:
					vals[j] = []byte{}
				}
				continue
			}
			vals[j] = v
		}
		allVals[i] = vals
	}

	return slot.arena.AppendBatch(allVals)
}

// Close stops the TTL cleaner.
func (m *DataCacheManager) Close() {
	m.ttl.Stop()
}
