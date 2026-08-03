package ws

import (
	"hash/fnv"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const outboundBufSize = 64 // buffered channel size per session

// WSSession represents one live browser WebSocket connection.
// It implements rctx.WSSessionRef.
type WSSession struct {
	id          string
	tenantID    uint16
	apiID       uint32
	connectedAt time.Time
	conn        *wsConn
	outCh       chan OutboundMsg
	closed      atomic.Bool
	upgradeReq  *http.Request

	subMu         sync.Mutex
	subscriptions map[string]struct{}
}

// UpgradeRequest returns the HTTP request used to upgrade this connection.
func (s *WSSession) UpgradeRequest() *http.Request { return s.upgradeReq }

// WSSessionID implements rctx.WSSessionRef.
func (s *WSSession) WSSessionID() string { return s.id }

// WSTenantID implements rctx.WSSessionRef.
func (s *WSSession) WSTenantID() uint16 { return s.tenantID }

// WSSend implements rctx.WSSessionRef.
// It copies the payload (the caller may reuse the buffer) and sends it to outCh
// non-blocking. Returns false if the channel is full or the session is closed.
func (s *WSSession) WSSend(payload []byte) bool {
	if s.closed.Load() {
		return false
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	msg := OutboundMsg{Payload: cp}
	select {
	case s.outCh <- msg:
		return true
	default:
		return false
	}
}

// Subscribe records that this session is subscribed to a channel.
func (s *WSSession) Subscribe(channel string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if s.subscriptions == nil {
		s.subscriptions = make(map[string]struct{})
	}
	s.subscriptions[channel] = struct{}{}
}

// Unsubscribe removes a channel subscription.
func (s *WSSession) Unsubscribe(channel string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	delete(s.subscriptions, channel)
}

// Subscriptions returns a snapshot copy of all subscribed channel names.
func (s *WSSession) Subscriptions() []string {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	result := make([]string, 0, len(s.subscriptions))
	for ch := range s.subscriptions {
		result = append(result, ch)
	}
	return result
}

// WSSubscribe implements rctx.WSSessionRef — delegates to Subscribe.
func (s *WSSession) WSSubscribe(channel string) { s.Subscribe(channel) }

// WSUnsubscribe implements rctx.WSSessionRef — delegates to Unsubscribe.
func (s *WSSession) WSUnsubscribe(channel string) { s.Unsubscribe(channel) }

// WSClose implements rctx.WSSessionRef — delegates to Close.
func (s *WSSession) WSClose(code int, text string) { s.Close(code, text) }

// Close sends a close OutboundMsg to the outbound goroutine.
// If the session is already closed or the channel is full, it closes the
// underlying connection directly.
func (s *WSSession) Close(code int, text string) {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	msg := OutboundMsg{CloseCode: code, CloseText: text}
	select {
	case s.outCh <- msg:
	default:
		// outCh full — close raw connection directly
		_ = s.conn.WriteClose(code, text)
		_ = s.conn.Close()
	}
}

// ── SessionMap ────────────────────────────────────────────────────────────────

// SessionMap is a 16-shard concurrent map from session ID to *WSSession.
type SessionMap struct {
	shards [16]sessionShard
}

type sessionShard struct {
	mu   sync.RWMutex
	data map[string]*WSSession
}

func shardIdx(id string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return int(h.Sum32() & 0xF) // % 16
}

// Store adds or replaces a session.
func (m *SessionMap) Store(id string, s *WSSession) {
	sh := &m.shards[shardIdx(id)]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.data == nil {
		sh.data = make(map[string]*WSSession)
	}
	sh.data[id] = s
}

// Load retrieves a session by ID.
func (m *SessionMap) Load(id string) (*WSSession, bool) {
	sh := &m.shards[shardIdx(id)]
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	s, ok := sh.data[id]
	return s, ok
}

// Delete removes a session by ID.
func (m *SessionMap) Delete(id string) {
	sh := &m.shards[shardIdx(id)]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	delete(sh.data, id)
}

// Count returns the total number of sessions across all shards.
func (m *SessionMap) Count() int {
	total := 0
	for i := range m.shards {
		sh := &m.shards[i]
		sh.mu.RLock()
		total += len(sh.data)
		sh.mu.RUnlock()
	}
	return total
}
