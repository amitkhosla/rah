package ws

import (
	"runtime"

	wsconfig "github.com/amitkhosla/rah/internal/config"
)

// BroadcastChannel sends payload to all sessions subscribed to the given channel.
// Returns the number of sessions reached.
func (w *WS) BroadcastChannel(channel string, payload []byte) int {
	sessionIDs := w.Subscriptions.Lookup(channel)
	count := 0
	for _, sid := range sessionIDs {
		sess, ok := w.Sessions.Load(sid)
		if !ok {
			continue
		}
		if sess.WSSend(payload) {
			count++
		}
	}
	return count
}

// PushSession sends payload to a specific session by ID.
func (w *WS) PushSession(sessionID string, payload []byte) bool {
	sess, ok := w.Sessions.Load(sessionID)
	if !ok {
		return false
	}
	return sess.WSSend(payload)
}

// WS is the top-level coordinator created once at gateway startup.
// It wires together session management, subscription routing, event dispatch,
// upstream connections, and the HTTP upgrade handler.
type WS struct {
	Sessions      *SessionMap
	Subscriptions *SubscriptionIndex
	Dispatcher    *Dispatcher
	Handler       *Handler
	UpstreamPool  *UpstreamPool
	DynamicPool   *DynamicPool
}

// New creates a WS coordinator from config.
//
// eventHandler is called for each WSEvent dispatched to the worker pool.
// Pass a stub (e.g. func(WSEvent) {}) here; the real handler is wired in
// Wave 3 (main.go) when the flow engine is available.
func New(gatewayCfg wsconfig.WSGatewayConfig, upstreams []wsconfig.WSUpstreamConfig, eventHandler EventHandler) (*WS, error) {
	sessions := &SessionMap{}
	subs := NewSubscriptionIndex()

	workers := runtime.NumCPU()
	if workers < 4 {
		workers = 4
	}
	dispatcher := NewDispatcher(workers, 1024, eventHandler)

	// Upstream event handler: fan out each received message to all sessions
	// subscribed to the upstream's channel name.
	upstreamHandler := func(event UpstreamEvent) {
		sessionIDs := subs.Lookup(event.UpstreamName)
		for _, sid := range sessionIDs {
			if s, ok := sessions.Load(sid); ok {
				s.WSSend(event.Payload)
			}
		}
	}

	pool, err := NewUpstreamPool(upstreams, upstreamHandler)
	if err != nil {
		return nil, err
	}

	handler := NewHandler(sessions, subs, dispatcher, gatewayCfg)

	// Build upstream URL map for the dynamic pool.
	upstreamURLs := make(map[string]string, len(upstreams))
	for _, u := range upstreams {
		upstreamURLs[u.Name] = u.URL
	}
	dynamicPool := NewDynamicPool(upstreamURLs)

	return &WS{
		Sessions:      sessions,
		Subscriptions: subs,
		Dispatcher:    dispatcher,
		Handler:       handler,
		UpstreamPool:  pool,
		DynamicPool:   dynamicPool,
	}, nil
}
