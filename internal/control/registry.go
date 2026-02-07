package control

import "sync"

type NameRegistry struct {
	mu          sync.RWMutex
	ApiNameToId map[string]uint32
	nextId      uint32
}

func NewNameRegistry() *NameRegistry {
	return &NameRegistry{
		ApiNameToId: make(map[string]uint32),
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
	nr.nextId++
	return id
}
