package ws

import (
	"hash/fnv"
	"sync"
)

// SubscriptionIndex maps channel name → set of session IDs.
// Sharded for reduced lock contention.
type SubscriptionIndex struct {
	shards [16]subShard
}

type subShard struct {
	mu       sync.RWMutex
	channels map[string]map[string]struct{} // channel → set of sessionIDs
}

// NewSubscriptionIndex returns a ready-to-use SubscriptionIndex.
func NewSubscriptionIndex() *SubscriptionIndex {
	idx := &SubscriptionIndex{}
	for i := range idx.shards {
		idx.shards[i].channels = make(map[string]map[string]struct{})
	}
	return idx
}

func subShardIdx(channel string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(channel))
	return int(h.Sum32() & 0xF) // % 16
}

// Subscribe records that sessionID is subscribed to channel.
func (idx *SubscriptionIndex) Subscribe(channel, sessionID string) {
	sh := &idx.shards[subShardIdx(channel)]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.channels[channel] == nil {
		sh.channels[channel] = make(map[string]struct{})
	}
	sh.channels[channel][sessionID] = struct{}{}
}

// Unsubscribe removes sessionID from the given channel's subscriber set.
func (idx *SubscriptionIndex) Unsubscribe(channel, sessionID string) {
	sh := &idx.shards[subShardIdx(channel)]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	delete(sh.channels[channel], sessionID)
	if len(sh.channels[channel]) == 0 {
		delete(sh.channels, channel)
	}
}

// Lookup returns a snapshot copy of all session IDs subscribed to channel.
// Callers do not hold the lock while iterating.
func (idx *SubscriptionIndex) Lookup(channel string) []string {
	sh := &idx.shards[subShardIdx(channel)]
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	set := sh.channels[channel]
	if len(set) == 0 {
		return nil
	}
	result := make([]string, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	return result
}

// RemoveSession removes sessionID from every listed channel.
// Used when a session disconnects to clean up all its subscriptions at once.
func (idx *SubscriptionIndex) RemoveSession(sessionID string, channels []string) {
	for _, ch := range channels {
		idx.Unsubscribe(ch, sessionID)
	}
}
