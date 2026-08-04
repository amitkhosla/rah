package ws

import (
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// DynamicConn represents an on-demand upstream WebSocket connection with a TTL.
type DynamicConn struct {
	conn      *websocket.Conn
	mu        sync.Mutex
	expiresAt time.Time
	name      string
	scopeKey  string
}

// Send sends a message on the dynamic connection. Thread-safe.
func (dc *DynamicConn) Send(payload []byte) error {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.conn == nil {
		return fmt.Errorf("dynamic connection %q closed", dc.name)
	}
	return dc.conn.WriteMessage(websocket.BinaryMessage, payload)
}

// Close closes the dynamic connection.
func (dc *DynamicConn) Close() {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.conn != nil {
		_ = dc.conn.Close()
		dc.conn = nil
	}
}

// Expired reports whether the connection TTL has elapsed.
func (dc *DynamicConn) Expired() bool {
	return time.Now().After(dc.expiresAt)
}

// DynamicPool manages on-demand upstream WebSocket connections keyed by name+scopeKey.
type DynamicPool struct {
	mu    sync.RWMutex
	conns map[string]*DynamicConn // key: name+":"+scopeKey
	// upstreamURLs is populated at construction from the gateway config ws_upstreams.
	upstreamURLs map[string]string // upstream name → URL
}

// NewDynamicPool creates an empty DynamicPool.
func NewDynamicPool(upstreamURLs map[string]string) *DynamicPool {
	dp := &DynamicPool{
		conns:        make(map[string]*DynamicConn),
		upstreamURLs: upstreamURLs,
	}
	go dp.reaper()
	return dp
}

// Connect opens or returns an existing connection for the given upstream name and scope key.
// ttl specifies how long the connection should live (0 = use default of 5 minutes).
func (dp *DynamicPool) Connect(name, scopeKey string, ttl time.Duration) (*DynamicConn, error) {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	key := name + ":" + scopeKey

	dp.mu.RLock()
	if dc, ok := dp.conns[key]; ok && !dc.Expired() {
		dp.mu.RUnlock()
		return dc, nil
	}
	dp.mu.RUnlock()

	url, ok := dp.upstreamURLs[name]
	if !ok {
		return nil, fmt.Errorf("unknown upstream %q", name)
	}

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial upstream %q: %w", name, err)
	}

	dc := &DynamicConn{
		conn:      conn,
		expiresAt: time.Now().Add(ttl),
		name:      name,
		scopeKey:  scopeKey,
	}

	dp.mu.Lock()
	// Close any existing expired connection
	if old, ok := dp.conns[key]; ok {
		old.Close()
	}
	dp.conns[key] = dc
	dp.mu.Unlock()

	gatewaylog.Default.Info("[DynamicPool] connected", gatewaylog.F("upstream", name), gatewaylog.F("scope", scopeKey))
	return dc, nil
}

// Disconnect explicitly closes and removes a connection.
func (dp *DynamicPool) Disconnect(name, scopeKey string) {
	key := name + ":" + scopeKey
	dp.mu.Lock()
	if dc, ok := dp.conns[key]; ok {
		dc.Close()
		delete(dp.conns, key)
	}
	dp.mu.Unlock()
}

// reaper periodically removes expired connections.
func (dp *DynamicPool) reaper() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		dp.mu.Lock()
		for key, dc := range dp.conns {
			if dc.Expired() {
				dc.Close()
				delete(dp.conns, key)
				gatewaylog.Default.Debug("[DynamicPool] reaped expired connection", gatewaylog.F("key", key))
			}
		}
		dp.mu.Unlock()
	}
}
