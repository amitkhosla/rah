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
	ErrReadOnly           = errors.New("datacache: collection is read-only")
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
	Version  string // opaque schema version; re-create only when changed
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
	TTLSec   int    // 0 = no expiry
	Version  string // opaque schema version tag
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
		Version:  opts.Version,
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
	if meta.Access == AccessReadOnly {
		return ErrReadOnly
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
	allVals := make([][]any, len(rawRows))
	for i, raw := range rawRows {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return fmt.Errorf("datacache: row %d: %w", i, err)
		}
		vals := make([]any, len(meta.Schema.Fields))
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

	err = slot.arena.AppendBatch(allVals)
	if err == ErrArenaFull && meta.Growth == GrowthAuto {
		if growErr := m.growCollection(name, meta); growErr == nil {
			err = slot.arena.AppendBatch(allVals)
		}
	}
	return err
}

// growCollection doubles a collection's capacity up to MaxCap.
// It copies all existing row data column-by-column into a new buffer,
// then atomically replaces the arena state under freeMu.
func (m *DataCacheManager) growCollection(name string, meta *CollectionMeta) error {
	slot := &m.pool.slots[meta.PoolIdx]
	arena := &slot.arena

	oldCap := arena.Cap
	newCap := oldCap * 2
	if meta.MaxCap > 0 && newCap > meta.MaxCap {
		newCap = meta.MaxCap
	}
	if newCap <= oldCap {
		return ErrArenaFull
	}

	// BakeSchema mutates FieldDef.Width in place; pass a copy to avoid aliasing.
	fieldsCopy := append([]FieldDef(nil), meta.Schema.Fields...)
	newSchema := BakeSchema(fieldsCopy, newCap)
	if int64(newSchema.BufSize) > m.pool.maxBufBytes {
		return ErrArenaFull
	}

	// Reserve additional quota for the larger buffer.
	addBytes := int64(newSchema.BufSize - meta.Schema.BufSize)
	if addBytes > 0 {
		tenantID := meta.QuotaKey.TenantID
		appEnd := 0
		for appEnd < 32 && meta.QuotaKey.App[appEnd] != 0 {
			appEnd++
		}
		app := string(meta.QuotaKey.App[:appEnd])
		if err := m.quotas.Reserve(tenantID, app, addBytes, 0); err != nil {
			return err
		}
	}

	// Copy all existing row data into the new (larger) buffer column by column.
	newBuf := make([]byte, newSchema.BufSize)
	oldSize := int(arena.Size.Load())
	for colIdx, oldReg := range arena.Schema.ColRegions {
		newReg := newSchema.ColRegions[colIdx]
		stride := oldReg.Stride
		for row := range oldSize {
			oldStart := oldReg.Offset + row*stride
			newStart := newReg.Offset + row*stride
			copy(newBuf[newStart:newStart+stride], arena.buf[oldStart:oldStart+stride])
		}
	}

	// Grow sort indices slice if needed.
	newIndices := slot.indices
	if cap(newIndices) < newCap {
		newIndices = make([]uint32, newCap)
		copy(newIndices, slot.indices)
	} else {
		newIndices = newIndices[:newCap]
	}

	// Atomically replace the arena's buffer/schema/cap under freeMu.
	arena.freeMu.Lock()
	arena.buf = newBuf
	arena.Cap = newCap
	arena.Schema = newSchema
	slot.indices = newIndices
	arena.freeMu.Unlock()

	// Publish updated metadata so future Get calls see the new schema/cap.
	newMeta := &CollectionMeta{
		PoolIdx:  meta.PoolIdx,
		Schema:   newSchema,
		Access:   meta.Access,
		Growth:   meta.Growth,
		MaxCap:   meta.MaxCap,
		QuotaKey: meta.QuotaKey,
		TTLSec:   meta.TTLSec,
		Version:  meta.Version,
	}
	m.named.Store(name, newMeta)
	return nil
}

// Close stops the TTL cleaner.
func (m *DataCacheManager) Close() {
	m.ttl.Stop()
}
