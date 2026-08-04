package ws

import "time"

// EventKind identifies the category of a WSEvent.
type EventKind uint8

const (
	EventInbound    EventKind = 0 // browser → gateway: message received
	EventOutbound   EventKind = 1 // upstream/redis → gateway → browser
	EventConnect    EventKind = 2 // new browser connection established
	EventDisconnect EventKind = 3 // browser connection closed
	EventTimer      EventKind = 4 // heartbeat/idle/auth-expiry timer fired
)

// WSEvent is dispatched to the worker pool for processing.
type WSEvent struct {
	Kind        EventKind
	SessionID   string
	TenantID    uint16
	ApiID       uint32
	Payload     []byte    // message payload (inbound/outbound)
	TimerKind   string    // "ping", "idle_check", "auth_check" (for EventTimer)
	ConnectedAt time.Time // set on EventConnect
}

// OutboundMsg is sent to a session's outbound goroutine.
type OutboundMsg struct {
	Payload   []byte
	CloseCode int    // 0 = data message; non-zero = close with this code
	CloseText string
}

// UpstreamEvent is fired by the upstream pool when a message arrives from an upstream.
type UpstreamEvent struct {
	UpstreamName string
	Payload      []byte
	ReceivedAt   time.Time
}
