package eventmanager

// Redis Pub/Sub publisher and subscriber for cross-instance cache event sync.
//
// Use case: multiple gateway instances share a Redis channel. When one instance
// writes to its local cache it publishes a WriteEvent; all other instances
// receive and apply the event to their own caches, keeping them in sync.
//
// Typical wiring at startup:
//
//	client := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
//
//	// Publish local writes to Redis
//	pub := eventmanager.NewRedisPubSubPublisher(client, "rah:cache-events", 2000)
//	em.Add(pub.Handler("redis-pub", nil)) // nil filter = all events
//
//	// Apply remote writes to local cache
//	sub := eventmanager.NewRedisPubSubSubscriber(client, "rah:cache-events", func(ev cache.WriteEvent) {
//	    cm.Update(ev.TenantID, ev.Key, ev.Value, ev.TTL)
//	})
//	sub.Start()
//	defer sub.Stop()

import (
	"context"
	"encoding/json"
	"log"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"rah/internal/cache"
)

// pubSubEvent is the JSON wire format for WriteEvents published over Redis.
// Key and Value are base64-encoded by encoding/json ([]byte fields).
type pubSubEvent struct {
	TenantID uint16 `json:"t"`
	Key      []byte `json:"k"`
	Value    []byte `json:"v"`
	TTL      uint32 `json:"ttl,omitempty"`
}

// ── Publisher ─────────────────────────────────────────────────────────────────

// RedisPubSubPublisher publishes cache.WriteEvents to a Redis channel via PUBLISH.
// The caller owns and manages the Redis client's lifecycle.
type RedisPubSubPublisher struct {
	client    goredis.UniversalClient
	channel   string
	timeoutMs int
}

// NewRedisPubSubPublisher creates a publisher. timeoutMs is the per-PUBLISH
// deadline (default 2000ms when ≤0).
func NewRedisPubSubPublisher(client goredis.UniversalClient, channel string, timeoutMs int) *RedisPubSubPublisher {
	if timeoutMs <= 0 {
		timeoutMs = 2000
	}
	return &RedisPubSubPublisher{client: client, channel: channel, timeoutMs: timeoutMs}
}

// Handler returns an eventmanager.Handler ready to pass to EventManager.Add.
// The handler always runs async (Async: true) so PUBLISH never blocks the
// cache dispatch goroutine.
//
// filter may be nil to receive every WriteEvent. Key and Value are copied
// before the goroutine runs to avoid races with the original caller's buffer.
func (p *RedisPubSubPublisher) Handler(name string, filter func(cache.WriteEvent) bool) Handler {
	return Handler{
		Name:   name,
		Async:  true, // PUBLISH is I/O — must not stall the cache dispatch loop
		Filter: filter,
		Handle: p.publish,
	}
}

func (p *RedisPubSubPublisher) publish(ev cache.WriteEvent) {
	// Copy slices: the async goroutine may outlive the caller's buffer lifetime.
	key := make([]byte, len(ev.Key))
	copy(key, ev.Key)
	val := make([]byte, len(ev.Value))
	copy(val, ev.Value)

	payload, err := json.Marshal(pubSubEvent{
		TenantID: ev.TenantID,
		Key:      key,
		Value:    val,
		TTL:      ev.TTL,
	})
	if err != nil {
		log.Printf("[eventmanager/pubsub] marshal error: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.timeoutMs)*time.Millisecond)
	defer cancel()
	if err := p.client.Publish(ctx, p.channel, payload).Err(); err != nil {
		log.Printf("[eventmanager/pubsub] publish error on channel %q: %v", p.channel, err)
	}
}

// ── Subscriber ────────────────────────────────────────────────────────────────

// RedisPubSubSubscriber SUBSCRIBEs to a Redis channel and delivers received
// WriteEvents to onEvent. Reconnects automatically on channel closure.
// The caller owns and manages the Redis client's lifecycle.
type RedisPubSubSubscriber struct {
	client  goredis.UniversalClient
	channel string
	onEvent func(cache.WriteEvent)
	stopCh  chan struct{}
	done    chan struct{}
}

// NewRedisPubSubSubscriber creates a subscriber. Call Start to begin the loop.
// onEvent is called in the subscriber goroutine; it should be non-blocking
// (use cm.Update, not cm.Put, to avoid re-emitting events).
func NewRedisPubSubSubscriber(client goredis.UniversalClient, channel string, onEvent func(cache.WriteEvent)) *RedisPubSubSubscriber {
	return &RedisPubSubSubscriber{
		client:  client,
		channel: channel,
		onEvent: onEvent,
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Start begins the subscription loop in a background goroutine.
// Returns immediately; call Stop to shut down and wait for exit.
func (s *RedisPubSubSubscriber) Start() {
	go s.loop()
}

// Stop signals the subscriber to exit and blocks until the goroutine finishes.
func (s *RedisPubSubSubscriber) Stop() {
	close(s.stopCh)
	<-s.done
}

func (s *RedisPubSubSubscriber) loop() {
	defer close(s.done)

	backoff := 2 * time.Second
	for {
		// Check stop before (re-)connecting.
		select {
		case <-s.stopCh:
			return
		default:
		}

		sub := s.client.Subscribe(context.Background(), s.channel)
		log.Printf("[eventmanager/pubsub] subscribed to channel %q", s.channel)
		ch := sub.Channel()

	inner:
		for {
			select {
			case <-s.stopCh:
				_ = sub.Close()
				return
			case msg, ok := <-ch:
				if !ok {
					_ = sub.Close()
					log.Printf("[eventmanager/pubsub] channel %q closed, reconnecting in %v", s.channel, backoff)
					break inner
				}
				var e pubSubEvent
				if err := json.Unmarshal([]byte(msg.Payload), &e); err != nil {
					log.Printf("[eventmanager/pubsub] unmarshal error: %v", err)
					continue
				}
				s.onEvent(cache.WriteEvent{
					TenantID: e.TenantID,
					Key:      e.Key,
					Value:    e.Value,
					TTL:      e.TTL,
				})
			}
		}

		// Backoff before reconnecting.
		select {
		case <-s.stopCh:
			return
		case <-time.After(backoff):
		}
	}
}
