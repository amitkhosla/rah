package registry

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"
)

// nextAvailableTenantID is a global counter for row allocation.
// Starts at 1 to reserve 0 for global/system defaults.
var nextAvailableTenantID uint32 = 1

// AliasListKey is the reserved column (Column 0) for tracking all aliases of a tenant.
// Vital for clean deletion and Radix tree "shredding."
const AliasListKey = "__system_aliases__"

// RegistryManager coordinates the Management Plane of the gateway.
// It uses a Mutex to ensure that only one "Bake" (matrix expansion/re-alignment)
// happens at a time, preventing race conditions during memory reallocation.
type RegistryManager struct {
	mu sync.Mutex
}

/**
 * AddTenantData: The primary entry point for setting Metadata or URLs.
 * Logic:
 * 1. Resolves the Tenant (Row) and Key (Column).
 * 2. Deduplicates the Value in the ValuePool.
 * 3. Updates the Matrix at the calculated memory offset.
 */
func (m *RegistryManager) AddTenantData(alias string, key string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	reg := State.Active.Load()

	// Resolve the Tenant (Row). Returns uint16 to save space in Radix nodes.
	tID, reg := m.resolveOrCreateTenant(reg, alias)

	// Resolve the Key (Column). Triggers a Stride Shift if the key is new.
	kID, reg := m.resolveOrCreateKey(reg, key)

	// Intern the value (Byte-level deduplication).
	vID := m.internValue(reg, value)

	// Offset calculation: (Row Index * Row Width) + Column Index.
	// We cast to uint32 to prevent overflow during the multiplication.
	idx := (uint32(tID) * reg.Stride) + uint32(kID)
	reg.Matrix[idx] = vID
}

/**
 * AddAlias: Links a new hostname/ID to an existing tenant row.
 * This updates both the Identity Radix (for fast lookups) and the
 * internal metadata tracking (for cascading deletes).
 */
func (m *RegistryManager) AddAlias(existingAlias string, newAlias string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	reg := State.Active.Load()

	// Find the existing TenantID.
	tID, found := GetTenantID(reg, existingAlias)
	if !found {
		return
	}

	// Link new alias to the same tID in the Identity Radix arena.
	m.insertIntoRadix(&reg.Identity, &reg.StringPool, newAlias, uint32(tID))

	// Update the Alias List (Column 0) so we can delete this tenant later.
	kID, reg := m.resolveOrCreateKey(reg, AliasListKey)
	idx := (uint32(tID) * reg.Stride) + uint32(kID)

	oldVID := reg.Matrix[idx]
	var newList string
	if oldVID == 0 {
		newList = newAlias
	} else {
		newList = string(reg.ValuePool[oldVID]) + "," + newAlias
	}

	vID := m.internValue(reg, []byte(newList))
	reg.Matrix[idx] = vID
}

/**
 * UpsertTenantState: Batch-processes a tenant update.
 * Efficiency: Holds the lock once while applying multiple updates to the matrix.
 */
func (m *RegistryManager) UpsertTenantState(aliases []string, endpoints map[string]string, metadata map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(aliases) == 0 {
		return
	}

	reg := State.Active.Load()
	tID, reg := m.resolveOrCreateTenant(reg, aliases[0])

	// Link secondary aliases.
	for i := 1; i < len(aliases); i++ {
		m.insertIntoRadix(&reg.Identity, &reg.StringPool, aliases[i], uint32(tID))
	}

	// Process Endpoints (URLs).
	for k, v := range endpoints {
		kID, r := m.resolveOrCreateKey(reg, "url:"+k)
		reg = r
		vID := m.internValue(reg, []byte(v))
		reg.Matrix[(uint32(tID)*reg.Stride)+uint32(kID)] = vID
	}

	// Process Metadata.
	for k, v := range metadata {
		kID, r := m.resolveOrCreateKey(reg, "meta:"+k)
		reg = r
		vID := m.internValue(reg, []byte(v))
		reg.Matrix[(uint32(tID)*reg.Stride)+uint32(kID)] = vID
	}
}

/**
 * resolveOrCreateTenant: Maps an alias to a row index.
 * Uses a FreeList to recycle TenantIDs from deleted tenants, ensuring the Matrix
 * remains as dense as possible (important for CPU cache performance).
 */
func (m *RegistryManager) resolveOrCreateTenant(reg *TenantRegistry, alias string) (uint16, *TenantRegistry) {
	if id, found := GetTenantID(reg, alias); found {
		return id, reg
	}

	var tID uint16
	if len(State.FreeSlots) > 0 {
		lastIdx := len(State.FreeSlots) - 1
		tID = uint16(State.FreeSlots[lastIdx])
		State.FreeSlots = State.FreeSlots[:lastIdx]
	} else {
		// Allocate a new row ID from the global counter.
		tID = uint16(atomic.AddUint32(&nextAvailableTenantID, 1) - 1)
	}

	// Map the alias to the tID in the Radix tree.
	m.insertIntoRadix(&reg.Identity, &reg.StringPool, alias, uint32(tID))

	// Ensure Column 0 is initialized with the primary alias.
	kID, reg := m.resolveOrCreateKey(reg, AliasListKey)
	vID := m.internValue(reg, []byte(alias))
	reg.Matrix[(uint32(tID)*reg.Stride)+uint32(kID)] = vID

	return tID, reg
}

/**
 * resolveOrCreateKey: Maps a property name to a column index.
 * If the key is new, the Matrix "Stride" (Width) must increase.
 * This triggers expandAndBake to realign all data into a new memory block.
 */
func (m *RegistryManager) resolveOrCreateKey(reg *TenantRegistry, key string) (uint16, *TenantRegistry) {
	if id, found := GetKeyID(reg, key); found {
		return id, reg
	}

	newKeyID := uint16(reg.Stride)
	newStride := reg.Stride + 1

	// Reshuffle the matrix to accommodate the new column.
	newReg := m.expandAndBake(reg, uint32(reg.MaxTenants)-1, newStride)

	m.insertIntoRadix(&newReg.Properties, &newReg.StringPool, key, uint32(newKeyID))
	return newKeyID, newReg
}

/**
 * expandAndBake: The heavy-lifter.
 * 1. Calculates new memory requirements (90/50 Rule).
 * 2. Allocates a new contiguous uint32 slice.
 * 3. Migrates data row-by-row to maintain the new Stride offset.
 * 4. Atomically swaps the old Registry for the new one.
 */
func (m *RegistryManager) expandAndBake(old *TenantRegistry, requiredTenants uint32, newStride uint32) *TenantRegistry {
	newMax := uint32(old.MaxTenants)

	if requiredTenants >= uint32(old.MaxTenants) {
		newMax = uint32(old.MaxTenants) + (uint32(old.MaxTenants) / 2)
		if requiredTenants >= newMax {
			newMax = requiredTenants + 128
		}
	}

	newMatrix := make([]uint32, newMax*newStride)

	// Data Migration: Row-by-row copy to handle stride changes.
	for tID := uint32(0); tID < uint32(old.MaxTenants); tID++ {
		oldRowStart := tID * old.Stride
		newRowStart := tID * newStride
		copy(newMatrix[newRowStart:], old.Matrix[oldRowStart:oldRowStart+old.Stride])
	}

	newReg := &TenantRegistry{
		Identity:   old.Identity,
		Properties: old.Properties,
		Matrix:     newMatrix,
		Stride:     newStride,
		MaxTenants: uint16(newMax),
		ValuePool:  old.ValuePool,
		StringPool: old.StringPool,
	}

	State.Active.Store(newReg)
	return newReg
}

/**
 * DeleteTenant: Zeroes out matrix data and invalidates all associated hostnames.
 */
func (m *RegistryManager) DeleteTenant(alias string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	reg := State.Active.Load()
	tID, found := GetTenantID(reg, alias)
	if !found {
		return
	}

	// 1. CLEAN RADIX: Remove all aliases from Column 0.
	kID, kFound := GetKeyID(reg, AliasListKey)
	if kFound {
		idx := (uint32(tID) * reg.Stride) + uint32(kID)
		if vID := reg.Matrix[idx]; vID != 0 {
			allAliases := strings.Split(string(reg.ValuePool[vID]), ",")
			for _, a := range allAliases {
				m.invalidateRadixAlias(reg.Identity, reg.StringPool, a)
			}
		}
	}

	// 2. SHREDDING: Zero out the row.
	start := uint32(tID) * reg.Stride
	for i := uint32(0); i < reg.Stride; i++ {
		reg.Matrix[start+i] = 0
	}

	// 3. RECYCLE: Add the row index back to the free list.
	State.FreeSlots = append(State.FreeSlots, tID)
}

/**
 * internValue: Byte-level deduplication.
 * Ensures that identical values (e.g., common upstream ports) are stored only once.
 */
func (m *RegistryManager) internValue(reg *TenantRegistry, value []byte) uint32 {
	if len(value) == 0 {
		return 0
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

/**
 * invalidateRadixAlias: Marks a node in the Radix tree as "dead" by setting its value to 0.
 * The entry is removed from the search path without rebuilding the tree.
 */
func (m *RegistryManager) invalidateRadixAlias(nodes []RegistryNode, pool []byte, alias string) {
	search := []byte(alias)
	for i := 1; i < len(nodes); i++ {
		prefix := pool[nodes[i].PrefixOffset : nodes[i].PrefixOffset+uint32(nodes[i].PrefixLen)]
		if bytes.Equal(prefix, search) {
			nodes[i].Value = 0
		}
	}
}

/**
 * insertIntoRadix: Manual memory management for our node arena.
 * Appends a new node to the flat slice.
 */
func (m *RegistryManager) insertIntoRadix(nodes *[]RegistryNode, pool *[]byte, key string, val uint32) {
	input := []byte(key)
	if len(*nodes) == 0 {
		*nodes = append(*nodes, RegistryNode{}) // Create Root Node
	}

	offset := uint32(len(*pool))
	*pool = append(*pool, input...)

	newNode := RegistryNode{
		PrefixOffset: offset,
		PrefixLen:    uint16(len(input)),
		Value:        uint16(val), // Stored as uint16 for row/key resolution
	}

	*nodes = append(*nodes, newNode)
	(*nodes)[0].ChildCount++
}
