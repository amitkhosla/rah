package ws

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/control"
)

const (
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
)

// Handler upgrades HTTP connections to WebSocket and manages session lifecycle.
type Handler struct {
	sessions      *SessionMap
	subscriptions *SubscriptionIndex
	dispatcher    *Dispatcher
	upgrader      websocket.Upgrader
	maxConns      int
	connCount     atomic.Int64
}

// NewHandler creates a Handler wired to the given session map, subscription index, and dispatcher.
func NewHandler(sessions *SessionMap, subs *SubscriptionIndex, dispatcher *Dispatcher, cfg config.WSGatewayConfig) *Handler {
	readBuf := cfg.ReadBufferSize
	if readBuf == 0 {
		readBuf = 4096
	}
	writeBuf := cfg.WriteBufferSize
	if writeBuf == 0 {
		writeBuf = 4096
	}
	maxConns := cfg.MaxConnections
	if maxConns == 0 {
		maxConns = 10000
	}
	return &Handler{
		sessions:      sessions,
		subscriptions: subs,
		dispatcher:    dispatcher,
		maxConns:      maxConns,
		upgrader: websocket.Upgrader{
			ReadBufferSize:    readBuf,
			WriteBufferSize:   writeBuf,
			EnableCompression: cfg.Compression,
			// CheckOrigin is set per-request in Upgrade.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// CanHandle returns true if the request carries WebSocket upgrade headers and
// the API config has WebSocket enabled.
func CanHandle(req *http.Request, wsCfg *control.WSApiConfig) bool {
	return wsCfg != nil && wsCfg.Enabled && req.Header.Get("Upgrade") == "websocket"
}

// Upgrade performs the HTTP→WS handshake, registers the session, starts the
// outbound goroutine, and runs the inbound read loop. Blocks until the
// connection closes. Called from the gateway's HTTP handler.
func (h *Handler) Upgrade(w http.ResponseWriter, req *http.Request, wsCfg *control.WSApiConfig, tenantID uint16, apiID uint32) {
	// Connection limit check.
	if h.connCount.Load() >= int64(h.maxConns) {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}

	// Origin check.
	if len(wsCfg.AllowedOrigins) > 0 {
		origin := req.Header.Get("Origin")
		if !isOriginAllowed(origin, wsCfg.AllowedOrigins) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
	}

	// Perform the HTTP→WS upgrade.
	h.upgrader.CheckOrigin = func(r *http.Request) bool { return true } // already checked above
	rawConn, err := h.upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Printf("ws: upgrade error: %v", err)
		return
	}

	// Generate a session ID.
	sessionID := generateSessionID()

	// Create session.
	conn := newWSConn(rawConn)
	session := &WSSession{
		id:          sessionID,
		tenantID:    tenantID,
		apiID:       apiID,
		connectedAt: time.Now(),
		conn:        conn,
		outCh:       make(chan OutboundMsg, outboundBufSize),
		upgradeReq:  req,
	}

	// Register session.
	h.sessions.Store(sessionID, session)
	h.connCount.Add(1)

	defer func() {
		h.connCount.Add(-1)
		h.sessions.Delete(sessionID)
		// Clean up subscriptions.
		h.subscriptions.RemoveSession(sessionID, session.Subscriptions())
		// Dispatch disconnect event.
		h.dispatcher.Dispatch(WSEvent{
			Kind:      EventDisconnect,
			SessionID: sessionID,
			TenantID:  tenantID,
			ApiID:     apiID,
		})
	}()

	// Set pong handler — resets read deadline on each pong.
	pongTimeout := pongWait
	if wsCfg.PongTimeoutSec > 0 {
		pongTimeout = time.Duration(wsCfg.PongTimeoutSec) * time.Second
	}
	_ = rawConn.SetReadDeadline(time.Now().Add(pongTimeout))
	conn.SetPongHandler(func(string) error {
		return rawConn.SetReadDeadline(time.Now().Add(pongTimeout))
	})

	// Start outbound goroutine.
	outCtx, outCancel := context.WithCancel(req.Context())
	defer outCancel()
	go RunOutbound(outCtx, session)

	// Dispatch connect event.
	h.dispatcher.Dispatch(WSEvent{
		Kind:        EventConnect,
		SessionID:   sessionID,
		TenantID:    tenantID,
		ApiID:       apiID,
		ConnectedAt: session.connectedAt,
	})

	// Configure max message size.
	if wsCfg.MaxMessageSize > 0 {
		rawConn.SetReadLimit(int64(wsCfg.MaxMessageSize))
	}

	// Inbound read loop.
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			// Connection closed or error — exit; defer handles cleanup.
			return
		}
		// Copy payload for the event (conn buffer may be reused).
		payload := make([]byte, len(data))
		copy(payload, data)
		h.dispatcher.Dispatch(WSEvent{
			Kind:      EventInbound,
			SessionID: sessionID,
			TenantID:  tenantID,
			ApiID:     apiID,
			Payload:   payload,
		})
	}
}

// isOriginAllowed checks whether the request origin is in the allowed list.
func isOriginAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if a == "*" || a == origin {
			return true
		}
	}
	return false
}

// generateSessionID produces a 16-byte random hex session ID.
func generateSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
