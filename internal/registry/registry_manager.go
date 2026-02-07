package registry

import (
	"bytes"
	"sync"
	"sync/atomic"
)

// nextAvailableTenantID tracks the highest slot index ever allocated.
// We start at 1 because 0 is often reserved for a 'Global' or 'Default' tenant.
var nextAvailableTenantID uint32 = 1

// Manager handles the "Bake" phase of the Registry.
// It is designed to be called by Management APIs or Background workers.
type Manager struct {
	mu sync.Mutex // Ensures only one management operation happens at a time.
}

/**
 * WORKFLOW COVERED BY THIS MANAGER:
 * 1. setTenant: Define a new logical tenant.
 * 2. addAlias: Map a UUID, Hostname, or Alphanumeric ID to a Tenant Slot.
 * 3. addTenantData (Shared): Map a Key (e.g., "service-url") to a Value shared by many.
 * 4. addTenantData (Unique): Map a Key to a value unique to that tenant.
 * 5. expand/recycle: Automatically grow the matrix or reuse slots from deleted tenants.
 */

// AddTenantData coordinates the mapping of a Key-Value pair to a specific Tenant Alias.
func (m *Manager) AddTenantData(alias string, key string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Load the current snapshot of the registry to find coordinates.
	current := State.Active.Load()

	// STEP 1: RESOLVE TENANT ID
	// We translate the "Alias" (e.g., "pepsi-nonprod") into a Slot Index (Row).
	// If the alias doesn't exist, it checks the FreeList for a deleted slot to reuse.
	tID := m.resolveOrCreateTenant(current, alias)

	// STEP 2: RESOLVE KEY ID
	// We translate the "Key" (e.g., "upstream-url") into a Column Index.
	// NOTE: If this is a brand-new key never seen by the system, it triggers a
	// Stride Shift—the matrix is reshuffled to add a new column for all tenants.
	kID := m.resolveOrCreateKey(current, key)

	// Reload 'current' because resolveOrCreateKey might have triggered an expansion/swap.
	current = State.Active.Load()

	// STEP 3: INTERN VALUE (DEDUPLICATION)
	// We check if the 'value' bytes already exist in the ValuePool.
	// If 'Pepsi' and 'Coke' use the same URL, they will both point to the same ValueID.
	vID := m.internValue(current, value)

	// STEP 4: CAPACITY CHECK (90/50 RULE)
	// If the resolved TenantID is nearing the limit of our allocated Matrix rows,
	// we trigger a background-style expansion that grows the matrix by 50%.
	if tID >= current.MaxTenants {
		current = m.expandAndBake(current, tID, current.Stride)
	}

	// STEP 5: ATOMIC MATRIX UPDATE
	// We calculate the exact memory offset: (Row * Width) + Column.
	// The ValueID is placed there. The Engine (Fast Path) will see this instantly.
	idx := (tID * current.Stride) + kID
	current.Matrix[idx] = vID
}

// resolveOrCreateTenant handles mapping any string alias to a persistent Tenant Slot.
func (m *Manager) resolveOrCreateTenant(reg *TenantRegistry, alias string) uint32 {
	// Search the Identity Radix Tree for the alias.
	if id, found := walkRadix(reg.Identity, reg.StringPool, alias); found {
		return id
	}

	// If alias is new, we need a slot.
	// PRIORITY: Reuse a slot from a previously deleted tenant to keep the matrix dense.
	var tID uint32
	if len(State.FreeSlots) > 0 {
		lastIdx := len(State.FreeSlots) - 1
		tID = State.FreeSlots[lastIdx]
		State.FreeSlots = State.FreeSlots[:lastIdx]
	} else {
		// If no deleted slots, take the next fresh ID from the high-water mark.
		tID = m.getNextGlobalID()
	}

	// Shred the alias string into the Identity Radix tree so it can be found in the fast path.
	m.insertIntoRadix(&reg.Identity, &reg.StringPool, alias, tID)
	return tID
}

// resolveOrCreateKey handles the column definitions (The "Stride" of the matrix).
func (m *Manager) resolveOrCreateKey(reg *TenantRegistry, key string) uint32 {
	// Search the Property Radix Tree for the key name.
	if id, found := walkRadix(reg.Properties, reg.StringPool, key); found {
		return id
	}

	// If the key is new, our Matrix "Stride" (Width) must increase.
	// This requires a "Bake" because all existing data must be realigned.
	newKeyID := reg.Stride
	newStride := reg.Stride + 1

	// Reshuffle the matrix to accommodate the new column.
	m.expandAndBake(reg, reg.MaxTenants-1, newStride)

	// Add the new key to the Property Radix tree.
	m.insertIntoRadix(&reg.Properties, &reg.StringPool, key, newKeyID)
	return newKeyID
}

// internValue ensures that identical values (like the same service URL used by 100 tenants)
// occupy only one spot in the ValuePool, drastically reducing memory usage.
func (m *Manager) internValue(reg *TenantRegistry, value []byte) uint32 {
	if len(value) == 0 {
		return 0 // Reserved for "Null/Empty"
	}

	// Deduplication: Check if this byte sequence already exists.
	// For 25,000 tenants, a linear scan of unique values is highly efficient for CPU L1 caches.
	for i, existing := range reg.ValuePool {
		if bytes.Equal(existing, value) {
			return uint32(i)
		}
	}

	// If unique, append to the pool and return the new index as the ValueID.
	newID := uint32(len(reg.ValuePool))
	reg.ValuePool = append(reg.ValuePool, value)
	return newID
}

// expandAndBake handles the physical memory allocation and data migration.
// It is used for both growing the number of tenants and widening the matrix (Stride).
func (m *Manager) expandAndBake(old *TenantRegistry, requiredTenants uint32, newStride uint32) *TenantRegistry {
	newMax := old.MaxTenants

	// Growth Logic: If we need more rows, grow by 50% (The 90/50 Rule).
	if requiredTenants >= old.MaxTenants {
		newMax = old.MaxTenants + (old.MaxTenants / 2)
		if requiredTenants >= newMax {
			newMax = requiredTenants + 128 // Small safety buffer
		}
	}

	// Allocate a new contiguous block of memory.
	newMatrix := make([]uint32, newMax*newStride)

	// DATA MIGRATION
	// If the stride is the same, we can use a single fast memory copy.
	if newStride == old.Stride {
		copy(newMatrix, old.Matrix)
	} else {
		// If the stride changed (New Key added), we must move data row-by-row.
		// This prevents "Tenant B" from seeing "Tenant A's" data due to offset shifts.
		for tID := uint32(0); tID < old.MaxTenants; tID++ {
			oldRowStart := tID * old.Stride
			oldRowEnd := oldRowStart + old.Stride

			newRowStart := tID * newStride
			copy(newMatrix[newRowStart:], old.Matrix[oldRowStart:oldRowEnd])
		}
	}

	// Create the new snapshot.
	newReg := &TenantRegistry{
		Identity:   old.Identity,
		Properties: old.Properties,
		Matrix:     newMatrix,
		Stride:     newStride,
		MaxTenants: newMax,
		ValuePool:  old.ValuePool,
		StringPool: old.StringPool,
	}

	// ATOMIC SWAP: The entire system switches to the new memory layout in one instruction.
	State.Active.Store(newReg)
	return newReg
}

// DeleteTenant removes access to a tenant and zeros their data for security.
func (m *Manager) DeleteTenant(alias string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	reg := State.Active.Load()
	tID, found := walkRadix(reg.Identity, reg.StringPool, alias)
	if !found {
		return
	}

	// 1. SECURITY: Zero out the entire row in the Matrix.
	// This ensures no future tenant reusing this slot can leak this data.
	start := tID * reg.Stride
	for i := uint32(0); i < reg.Stride; i++ {
		reg.Matrix[start+i] = 0
	}

	// 2. RECYCLE: Push the ID back to the FreeSlots stack for the next new tenant.
	State.FreeSlots = append(State.FreeSlots, tID)
}

func (m *Manager) getNextGlobalID() uint32 {
	return atomic.AddUint32(&nextAvailableTenantID, 1) - 1
}

// insertIntoRadix appends a new path to our flat node arena.
func (m *Manager) insertIntoRadix(nodes *[]RegistryNode, pool *[]byte, key string, val uint32) {
	input := []byte(key)

	// Ensure root exists
	if len(*nodes) == 0 {
		*nodes = append(*nodes, RegistryNode{})
	}

	// In this bare-metal arena, we append strings to the end of the pool
	// and create a new node pointing to that segment.
	offset := uint32(len(*pool))
	*pool = append(*pool, input...)

	newNode := RegistryNode{
		PrefixOffset: offset,
		PrefixLen:    uint16(len(input)),
		Value:        val,
	}

	*nodes = append(*nodes, newNode)

	// Update parent (root) to recognize the new branch
	(*nodes)[0].ChildCount++
}
