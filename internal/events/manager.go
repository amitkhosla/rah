package events

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/connectors/messaging"
	"github.com/amitkhosla/rah/internal/engine"
	goredis "github.com/redis/go-redis/v9"
)

// MessageHandler is an interface for handling messages.
type MessageHandler interface {
	Handle(ctx context.Context, msg messaging.ConsumedMessage) error
}

// listenerEntry holds a compiled event listener with its consumer and executor.
type listenerEntry struct {
	cfg      config.EventListenerConfig
	consumer messaging.MessageConsumer
	executor MessageHandler
}

// EventListenerManager starts and stops all configured event consumers.
type EventListenerManager struct {
	entries       []listenerEntry
	cancels       []context.CancelFunc
	wg            sync.WaitGroup
	registry      *SlotRegistry     // optional; for distributed claiming
	rebalanceDone chan struct{}      // signal that rebalance should stop
	rebalanceWg   sync.WaitGroup
}

// NewEventListenerManager creates an EventListenerManager from the gateway config.
// pubConfigs is the indexed slice of MessagingPublisher configs (to look up broker details by name).
// fm is the FlowManager (used by executors to run flows).
// secrets is used by consumers for credential resolution.
// redisClient is optional; if provided, enables Redis-backed deduplication; if nil, in-memory dedup is used.
// registry is optional; if provided, enables distributed claiming of listener slots.
func NewEventListenerManager(
	ctx context.Context,
	listenerCfgs []config.EventListenerConfig,
	pubConfigs []config.PublisherConfig,
	fm *engine.FlowManager,
	secrets messaging.SecretResolver,
	redisClient goredis.UniversalClient,
	registry *SlotRegistry,
) (*EventListenerManager, error) {
	m := &EventListenerManager{
		registry:      registry,
		rebalanceDone: make(chan struct{}),
	}

	// Index publisher configs by name.
	pubByName := make(map[string]config.PublisherConfig, len(pubConfigs))
	for _, p := range pubConfigs {
		pubByName[p.Name] = p
	}

	for _, lCfg := range listenerCfgs {
		pubCfg, ok := pubByName[lCfg.Publisher]
		if !ok {
			return nil, fmt.Errorf("event_listener[%s]: publisher %q not found in messaging_publishers", lCfg.Name, lCfg.Publisher)
		}

		groupID := lCfg.GroupID
		if groupID == "" {
			groupID = "rah-" + lCfg.Name
		}

		consumer, err := messaging.NewMessageConsumer(pubCfg, lCfg.Topic, groupID, secrets)
		if err != nil {
			return nil, fmt.Errorf("event_listener[%s]: %w", lCfg.Name, err)
		}

		var dedupStore DedupStore
		if lCfg.DedupWindowSec > 0 && redisClient != nil {
			dedupStore = newRedisDedupStore(redisClient)
		}

		executor := NewEventExecutor(ctx, fm, lCfg.FlowName, lCfg.PayloadVar, lCfg.KeyVar, lCfg.HeaderMappings, lCfg.AppName, lCfg.DedupKey, lCfg.DedupWindowSec, dedupStore)

		var handler MessageHandler = executor

		// Wrap with batching if configured
		if lCfg.BatchSize > 0 || lCfg.BatchWindowMs > 0 {
			state := fm.State.Load()
			payloadSlot := -1
			if state != nil {
				slots := state.GetFlowSlots(lCfg.FlowName)
				if lCfg.BatchPayloadVar != "" && slots != nil {
					if idx, ok := slots[lCfg.BatchPayloadVar]; ok {
						payloadSlot = idx
					}
				}
			}
			handler = newBatchingEventExecutor(ctx, fm, lCfg.FlowName, payloadSlot, lCfg.BatchSize, lCfg.BatchWindowMs)
		}

		m.entries = append(m.entries, listenerEntry{
			cfg:      lCfg,
			consumer: consumer,
			executor: handler,
		})
	}

	return m, nil
}

// Start begins consuming messages on all configured listeners.
// Each listener runs in its own goroutine(s) based on cfg.Workers.
// If a SlotRegistry is configured, only starts listeners this instance claims.
func (m *EventListenerManager) Start(ctx context.Context) {
	for _, entry := range m.entries {
		entry := entry // capture

		// If using distributed claiming, try to claim this listener
		if m.registry != nil {
			claimed, err := m.registry.TryClaim(ctx, entry.cfg.Name)
			if err != nil {
				log.Printf("[EventListener:%s] claim attempt failed: %v", entry.cfg.Name, err)
				continue
			}
			if !claimed {
				log.Printf("[EventListener:%s] not claimed by this instance (another instance owns it)", entry.cfg.Name)
				continue
			}
		}

		// Start workers for this listener
		workers := entry.cfg.Workers
		if workers < 1 {
			workers = 1
		}
		for i := 0; i < workers; i++ {
			workerCtx, cancel := context.WithCancel(ctx)
			m.cancels = append(m.cancels, cancel)
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				if err := entry.consumer.Start(workerCtx, entry.executor.Handle); err != nil {
					if workerCtx.Err() == nil {
						log.Printf("[EventListener:%s] consumer error: %v", entry.cfg.Name, err)
					}
				}
			}()
		}
		log.Printf("[EventListener] started %q (publisher=%s topic=%s flow=%s workers=%d)",
			entry.cfg.Name, entry.cfg.Publisher, entry.cfg.Topic, entry.cfg.FlowName, workers)
	}

	// If using distributed claiming, start the rebalancer
	if m.registry != nil {
		m.startRebalancer(ctx)
	}
}

// startRebalancer runs a goroutine that periodically checks if this instance can claim
// any listener slots that aren't currently running. This allows recovery when another
// instance crashes or releases a slot.
func (m *EventListenerManager) startRebalancer(ctx context.Context) {
	m.rebalanceWg.Add(1)
	go func() {
		defer m.rebalanceWg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-m.rebalanceDone:
				return
			case <-ticker.C:
				m.rebalance(ctx)
			}
		}
	}()
}

// rebalance attempts to claim any listener slots not currently running on this instance.
func (m *EventListenerManager) rebalance(ctx context.Context) {
	// Build a set of listeners already running
	runningListeners := make(map[string]bool)
	m.registry.mu.RLock()
	for name := range m.registry.ownedBy {
		runningListeners[name] = true
	}
	m.registry.mu.RUnlock()

	// Try to claim any listener we're not running
	for _, entry := range m.entries {
		if runningListeners[entry.cfg.Name] {
			continue // Already running
		}

		claimed, err := m.registry.TryClaim(ctx, entry.cfg.Name)
		if err != nil {
			log.Printf("[EventListener:rebalance:%s] claim failed: %v", entry.cfg.Name, err)
			continue
		}
		if !claimed {
			continue // Another instance owns it
		}

		// We successfully claimed it; start it
		log.Printf("[EventListener:rebalance] claimed %q, starting consumers", entry.cfg.Name)

		workers := entry.cfg.Workers
		if workers < 1 {
			workers = 1
		}
		for i := 0; i < workers; i++ {
			workerCtx, cancel := context.WithCancel(ctx)
			m.cancels = append(m.cancels, cancel)
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				if err := entry.consumer.Start(workerCtx, entry.executor.Handle); err != nil {
					if workerCtx.Err() == nil {
						log.Printf("[EventListener:%s] consumer error: %v", entry.cfg.Name, err)
					}
				}
			}()
		}
	}
}

// Stop cancels all consumers, releases all slot claims, and waits for them to finish.
func (m *EventListenerManager) Stop() {
	// Signal rebalancer to stop
	select {
	case <-m.rebalanceDone:
	default:
		close(m.rebalanceDone)
	}
	m.rebalanceWg.Wait()

	// Release all claims
	if m.registry != nil {
		m.registry.mu.RLock()
		owned := make([]string, 0, len(m.registry.ownedBy))
		for name := range m.registry.ownedBy {
			owned = append(owned, name)
		}
		m.registry.mu.RUnlock()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		for _, name := range owned {
			if err := m.registry.Release(ctx, name); err != nil {
				log.Printf("[EventListener] release %q failed: %v", name, err)
			}
		}
		cancel()
	}

	// Cancel all running consumers
	for _, cancel := range m.cancels {
		cancel()
	}
	m.wg.Wait()
}
