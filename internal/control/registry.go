package control

import "sync"

type NameRegistry struct {
	mu          sync.RWMutex
	ApiNameToId map[string]uint32
	idToName    map[uint32]string
	nextId      uint32
}

func NewNameRegistry() *NameRegistry {
	return &NameRegistry{
		ApiNameToId: make(map[string]uint32),
		idToName:    make(map[uint32]string),
		nextId:      1, // ID 0 is reserved for "Not Found"
	}
}

func (nr *NameRegistry) GetOrAssignId(name string) uint32 {
	nr.mu.Lock()
	defer nr.mu.Unlock()

	if id, exists := nr.ApiNameToId[name]; exists {
		return id
	}

	id := nr.nextId
	nr.ApiNameToId[name] = id
	nr.idToName[id] = name
	nr.nextId++
	return id
}

// GetNameByID returns the API name for a given internal ID.
// Returns the empty string if the ID is unknown.
func (nr *NameRegistry) GetNameByID(id uint32) string {
	nr.mu.RLock()
	defer nr.mu.RUnlock()
	return nr.idToName[id]
}
