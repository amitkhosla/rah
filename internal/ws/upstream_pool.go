package ws

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	wsconfig "github.com/amitkhosla/rah/internal/config"
)

// UpstreamPool manages named outbound WebSocket connections to upstream services.
// The conns map is read-only after construction — safe for concurrent access without locking.
type UpstreamPool struct {
	conns  map[string]*upstreamConn // read-only after construction
	cancel context.CancelFunc
}

type upstreamConn struct {
	name    string
	cfg     wsconfig.WSUpstreamConfig
	conn    *wsConn
	mu      sync.RWMutex
	handler UpstreamEventHandler
}

// UpstreamEventHandler is called when an upstream sends a message.
type UpstreamEventHandler func(event UpstreamEvent)

// NewUpstreamPool dials each configured upstream and starts read loops.
// If a dial fails, a warning is logged and the entry is still registered so
// the reconnect loop can retry (graceful degradation).
func NewUpstreamPool(cfgs []wsconfig.WSUpstreamConfig, handler UpstreamEventHandler) (*UpstreamPool, error) {
	ctx, cancel := context.WithCancel(context.Background())
	pool := &UpstreamPool{
		conns:  make(map[string]*upstreamConn, len(cfgs)),
		cancel: cancel,
	}

	for _, cfg := range cfgs {
		uc := &upstreamConn{
			name:    cfg.Name,
			cfg:     cfg,
			handler: handler,
		}

		// Attempt initial dial; failures are non-fatal.
		wc, err := dialUpstream(ctx, cfg)
		if err != nil {
			log.Printf("ws: upstream %q initial dial failed: %v (will retry)", cfg.Name, err)
		} else {
			uc.conn = wc
		}

		pool.conns[cfg.Name] = uc
		go runUpstreamReadLoop(ctx, uc)
	}

	return pool, nil
}

// Send writes payload to the named upstream connection.
// Returns an error if the name is not found or the write fails.
func (p *UpstreamPool) Send(name string, payload []byte) error {
	uc, ok := p.conns[name] // no lock — map is immutable
	if !ok {
		return &upstreamNotFoundError{name: name}
	}
	uc.mu.RLock()
	conn := uc.conn
	uc.mu.RUnlock()
	if conn == nil {
		return &upstreamNotConnectedError{name: name}
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

// SendToUpstream implements rctx.WSPoolSender — delegates to Send.
func (p *UpstreamPool) SendToUpstream(name string, payload []byte) error {
	return p.Send(name, payload)
}

// Close shuts down all upstream read loops.
func (p *UpstreamPool) Close() {
	p.cancel()
}

// dialUpstream performs a single WebSocket dial to the upstream.
func dialUpstream(ctx context.Context, cfg wsconfig.WSUpstreamConfig) (*wsConn, error) {
	c, _, err := websocket.DefaultDialer.DialContext(ctx, cfg.URL, nil)
	if err != nil {
		return nil, err
	}
	return newWSConn(c), nil
}

// runUpstreamReadLoop reads frames from the upstream in a loop.
// On disconnect it sleeps ReconnectIntervalSec seconds and re-dials.
// Sends pings every PingIntervalSec seconds.
// Exits when ctx is cancelled.
func runUpstreamReadLoop(ctx context.Context, uc *upstreamConn) {
	pingInterval := time.Duration(uc.cfg.PingIntervalSec) * time.Second
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	reconnectInterval := time.Duration(uc.cfg.ReconnectIntervalSec) * time.Second
	if reconnectInterval <= 0 {
		reconnectInterval = 5 * time.Second
	}

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		// Ensure we have a live connection.
		uc.mu.RLock()
		conn := uc.conn
		uc.mu.RUnlock()

		if conn == nil {
			// Wait for reconnect interval, then try to dial.
			select {
			case <-ctx.Done():
				return
			case <-time.After(reconnectInterval):
			}
			wc, err := dialUpstream(ctx, uc.cfg)
			if err != nil {
				log.Printf("ws: upstream %q reconnect failed: %v", uc.name, err)
				continue
			}
			uc.mu.Lock()
			uc.conn = wc
			conn = wc
			uc.mu.Unlock()
			log.Printf("ws: upstream %q reconnected", uc.name)
		}

		// Read loop for the current connection.
		readErr := runUpstreamConnReadLoop(ctx, uc, conn, ticker)
		if readErr == nil {
			// ctx cancelled.
			return
		}

		// Connection died — clear it and retry.
		log.Printf("ws: upstream %q read error: %v (will reconnect)", uc.name, readErr)
		uc.mu.Lock()
		if uc.conn == conn {
			_ = conn.Close()
			uc.conn = nil
		}
		uc.mu.Unlock()

		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectInterval):
		}
	}
}

// runUpstreamConnReadLoop reads from conn until an error occurs or ctx is done.
// Returns nil when ctx is done, non-nil on connection error.
func runUpstreamConnReadLoop(ctx context.Context, uc *upstreamConn, conn *wsConn, ticker *time.Ticker) error {
	// Use a done channel to break the blocking ReadMessage on context cancellation.
	readCh := make(chan readResult, 1)

	go func() {
		for {
			mt, p, err := conn.ReadMessage()
			readCh <- readResult{mt: mt, p: p, err: err}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := conn.WritePing(nil); err != nil {
				return err
			}
		case r := <-readCh:
			if r.err != nil {
				return r.err
			}
			uc.handler(UpstreamEvent{
				UpstreamName: uc.name,
				Payload:      r.p,
				ReceivedAt:   time.Now(),
			})
			// Re-arm the read goroutine is already running; just wait for next result.
		}
	}
}

type readResult struct {
	mt  int
	p   []byte
	err error
}

// ── error types ───────────────────────────────────────────────────────────────

type upstreamNotFoundError struct{ name string }

func (e *upstreamNotFoundError) Error() string {
	return "ws: upstream not found: " + e.name
}

type upstreamNotConnectedError struct{ name string }

func (e *upstreamNotConnectedError) Error() string {
	return "ws: upstream not connected: " + e.name
}
