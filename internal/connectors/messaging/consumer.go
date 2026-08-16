package messaging

import "context"

// ConsumedMessage is one message received from a broker.
type ConsumedMessage struct {
	Topic   string
	Key     []byte
	Payload []byte
	Headers map[string]string
}

// MessageHandler is called once per received message.
// Returning a non-nil error causes the consumer to log the error;
// the message is still acknowledged (at-least-once delivery).
type MessageHandler func(ctx context.Context, msg ConsumedMessage) error

// MessageConsumer subscribes to one topic and delivers messages to a handler.
// Implementations must be safe to call Start and Stop from different goroutines.
type MessageConsumer interface {
	// Start begins consuming messages. Blocks until ctx is cancelled or a fatal
	// error occurs. The handler is called synchronously per message.
	Start(ctx context.Context, handler MessageHandler) error
	// Stop signals the consumer to stop and waits for it to drain.
	Stop() error
}
