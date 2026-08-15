package events

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/amitkhosla/rah/internal/connectors/messaging"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/tidwall/gjson"
)

// EventExecutor runs a named flow for each incoming message.
// Slot indices are resolved once at startup from the FlowSlotRegistry.
type EventExecutor struct {
	fm                      *engine.FlowManager
	flowName                string
	wellKnownPayloadSlot    int            // __event__ slot; -1 if not found
	wellKnownKeySlot        int            // __event_key__ slot; -1 if not found
	topicSlot               int            // __event_topic__ slot; -1 if not found
	configuredPayloadSlot   int            // from PayloadVar config; -1 if not set
	configuredKeySlot       int            // from KeyVar config; -1 if not set
	headerSlots             map[string]int // message header name → slot index
	appName                 string         // from EventListenerConfig.AppName; empty = disabled
	dedupKey                string
	dedupWindow             time.Duration
	dedupStore              DedupStore // nil when dedup disabled
}

// NewEventExecutor creates an EventExecutor.
// payloadVar and keyVar are slot names; -1 is used when a var is not configured.
// headerMappings maps header names to variable names for injection.
// Slot indices are resolved from the FlowManager's current state at call time.
// dedupStore can be nil; if dedupWindowSec > 0 and dedupStore is nil, an InMemoryDedupStore is created.
func NewEventExecutor(ctx context.Context, fm *engine.FlowManager, flowName, payloadVar, keyVar string, headerMappings map[string]string, appName, dedupKey string, dedupWindowSec int, dedupStore DedupStore) *EventExecutor {
	e := &EventExecutor{
		fm:                    fm,
		flowName:              flowName,
		wellKnownPayloadSlot:  -1,
		wellKnownKeySlot:      -1,
		topicSlot:             -1,
		configuredPayloadSlot: -1,
		configuredKeySlot:     -1,
		headerSlots:           make(map[string]int),
		appName:               appName,
		dedupKey:              dedupKey,
		dedupWindow:           time.Duration(dedupWindowSec) * time.Second,
		dedupStore:            dedupStore,
	}

	state := fm.State.Load()
	if state != nil {
		slots := state.GetFlowSlots(flowName)
		if slots != nil {
			// Resolve well-known slots
			if idx, ok := slots["__event__"]; ok {
				e.wellKnownPayloadSlot = idx
			}
			if idx, ok := slots["__event_key__"]; ok {
				e.wellKnownKeySlot = idx
			}
			if idx, ok := slots["__event_topic__"]; ok {
				e.topicSlot = idx
			}

			// Resolve configured slots (backward compat)
			if payloadVar != "" {
				if idx, ok := slots[payloadVar]; ok {
					e.configuredPayloadSlot = idx
				}
			}
			if keyVar != "" {
				if idx, ok := slots[keyVar]; ok {
					e.configuredKeySlot = idx
				}
			}

			// Resolve header slot mappings
			for headerName, varName := range headerMappings {
				if idx, ok := slots[varName]; ok {
					e.headerSlots[headerName] = idx
				}
			}
		}
	}

	// Create in-memory dedup store if enabled and no external store provided
	if dedupWindowSec > 0 && e.dedupStore == nil {
		e.dedupStore = newInMemoryDedupStore(e.dedupWindow / 2)
	}

	return e
}

// Handle processes one incoming message by executing the configured flow.
func (e *EventExecutor) Handle(ctx context.Context, msg messaging.ConsumedMessage) error {
	// Deduplication check
	if e.dedupWindow > 0 && e.dedupKey != "" && e.dedupStore != nil {
		keyVal := gjson.GetBytes(msg.Payload, e.dedupKey).String()
		if keyVal != "" {
			isDup, err := e.dedupStore.IsDuplicate(context.Background(), keyVal, e.dedupWindow)
			if err == nil && isDup {
				return nil // duplicate — drop
			}
		}
	}

	rctxCtx := e.fm.GetContext()
	defer e.fm.ReturnContext(rctxCtx)

	rctxCtx.Reset(&discardWriter{})
	rctxCtx.Timing.StartNs = time.Now().UnixNano()

	// Set app identity
	if e.appName != "" {
		rctxCtx.AppName = e.appName
	}

	// Inject well-known slots
	if e.wellKnownPayloadSlot >= 0 && e.wellKnownPayloadSlot < len(rctxCtx.ByteSlots) {
		rctxCtx.ByteSlots[e.wellKnownPayloadSlot] = msg.Payload
	}
	if e.wellKnownKeySlot >= 0 && e.wellKnownKeySlot < len(rctxCtx.ByteSlots) {
		rctxCtx.ByteSlots[e.wellKnownKeySlot] = msg.Key
	}
	if e.topicSlot >= 0 && e.topicSlot < len(rctxCtx.ByteSlots) {
		rctxCtx.ByteSlots[e.topicSlot] = []byte(msg.Topic)
	}

	// Inject configured slots (backward compat)
	if e.configuredPayloadSlot >= 0 && e.configuredPayloadSlot < len(rctxCtx.ByteSlots) {
		rctxCtx.ByteSlots[e.configuredPayloadSlot] = msg.Payload
	}
	if e.configuredKeySlot >= 0 && e.configuredKeySlot < len(rctxCtx.ByteSlots) {
		rctxCtx.ByteSlots[e.configuredKeySlot] = msg.Key
	}

	// Inject header mappings
	for header, slotIdx := range e.headerSlots {
		if slotIdx >= 0 && slotIdx < len(rctxCtx.ByteSlots) {
			if val, ok := msg.Headers[header]; ok {
				rctxCtx.ByteSlots[slotIdx] = []byte(val)
			}
		}
	}

	e.fm.ProcessFlow(rctxCtx, e.flowName)

	if rctxCtx.Failed {
		errMsg := ""
		if len(rctxCtx.ErrorMsg) > 0 {
			errMsg = string(rctxCtx.ErrorMsg)
		}
		log.Printf("[EventExecutor] flow %q failed for topic %q: %s", e.flowName, msg.Topic, errMsg)
	}

	return nil
}

// discardWriter is a no-op rctx.ResponseWriter used for event flows (no HTTP response).
type discardWriter struct{}

func (d *discardWriter) Header() http.Header {
	return make(http.Header)
}

func (d *discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (d *discardWriter) WriteHeader(statusCode int) {
	// no-op
}
