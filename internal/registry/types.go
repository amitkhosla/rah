package registry

import "sync/atomic"

// RegistryNode is a 16-byte structure representing a segment in our Radix Tree.
// We use offsets (uint32) instead of pointers to keep the GC from scanning the arena.
type RegistryNode struct {
	PrefixOffset uint32 // Position in the StringPool
	PrefixLen    uint16 // Length of the prefix string
	ChildBase    uint32 // Start index of children in the Nodes arena
	ChildCount   uint16 // Number of children
	Value        uint16 // The ID (TenantID, KeyID, or ValueID)
	Padding      uint16 // 2 bytes (Keeps the struct at 16 bytes)
}

// TenantRegistry is the immutable snapshot of the system configuration.
type TenantRegistry struct {
	Identity   []RegistryNode // Radix for Alias -> TenantID
	Properties []RegistryNode // Radix for KeyName -> KeyID

	// Matrix is a flat grid of [TenantID * Stride + KeyID] = ValueID
	Matrix     []uint32
	Stride     uint32 // Total unique keys (the "width" of the matrix)
	MaxTenants uint16 // Total capacity of the matrix

	ValuePool  [][]byte // The actual data (URLs, JSON, etc.) indexed by ValueID
	StringPool []byte   // Raw bytes for Radix prefixes
}

// GlobalState holds the active registry and the free-slot tracker.
type GlobalState struct {
	Active    atomic.Pointer[TenantRegistry]
	FreeSlots []uint16 // Stack of deleted TenantIDs available for reuse
}

var State = &GlobalState{}
