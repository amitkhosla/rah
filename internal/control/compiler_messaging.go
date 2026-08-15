package control

import (
	"fmt"
	"strings"

	"github.com/amitkhosla/rah/internal/connectors/messaging"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// compileMessagePublish handles the "message_publish" step type.
//
// Step input keys:
//
//	Key    — publisher name (connector name; required)
//	Value  — topic/queue name (required; static topic baked at compile time)
//	Source — slot name for payload bytes (required)
//	Input["msg_key_var"]  — slot name for message key bytes (optional; -1 if missing)
//	Input["headers"]      — comma-separated "k=v,k2=v2" static headers (optional; parsed at bake time)
//	As     — optional slot name to store message ID returned by broker (SQS/PubSub)
//
// At bake time, the topic string, headers map, and slot indices are resolved
// and embedded in the instruction closure for zero per-request overhead.
func (c *Compiler) compileMessagePublish(step StepConfig) error {
	publisherName := step.Key
	if publisherName == "" {
		return fmt.Errorf("message_publish: 'Key' (publisher name) is required")
	}

	staticTopic := step.Value
	if staticTopic == "" {
		return fmt.Errorf("message_publish: 'Value' (topic) is required")
	}

	payloadSlotStr := step.Source
	if payloadSlotStr == "" {
		return fmt.Errorf("message_publish: 'Source' (payload slot) is required")
	}

	payloadSlotRaw, err := c.getSlot(payloadSlotStr)
	if err != nil {
		return fmt.Errorf("message_publish payload_slot: %w", err)
	}
	payloadSlot := int16(payloadSlotRaw)

	// Resolve message key slot (optional)
	msgKeySlot := int16(-1)
	if msgKeyVar := step.Input["msg_key_var"]; msgKeyVar != "" {
		slot, err := c.getSlot(msgKeyVar)
		if err != nil {
			return fmt.Errorf("message_publish msg_key_slot: %w", err)
		}
		msgKeySlot = int16(slot)
	}

	// Parse static headers from "key1=val1,key2=val2" format
	staticHeaders := parseStaticHeaders(step.Input["headers"])

	// Resolve message ID slot (optional)
	messageIDSlot := int16(-1)
	if messageIDVar := step.As; messageIDVar != "" {
		slot, err := c.getSlot(messageIDVar)
		if err != nil {
			return fmt.Errorf("message_publish message_id_slot: %w", err)
		}
		messageIDSlot = int16(slot)
	}

	// Capture MessagingPublisherManager for closure
	msgPubMgr := c.MessagingPublisherMgr

	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "MESSAGE_PUBLISH",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if msgPubMgr == nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				msg := "[message_publish] MessagingPublisherManager is nil — no messaging_publishers configured"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			pub, ok := msgPubMgr.Get(publisherName)
			if !ok {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				msg := fmt.Sprintf("[message_publish] publisher %q not found", publisherName)
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			req := messaging.PublishRequest{
				Topic: staticTopic,
			}
			if payloadSlot >= 0 && int(payloadSlot) < len(ctx.ByteSlots) {
				req.Payload = ctx.ByteSlots[payloadSlot]
			}
			if msgKeySlot >= 0 && int(msgKeySlot) < len(ctx.ByteSlots) {
				req.Key = ctx.ByteSlots[msgKeySlot]
			}

			if len(staticHeaders) > 0 {
				req.Headers = staticHeaders
			}

			if err := pub.Publish(ctx.Request.Context(), req); err != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				msg := fmt.Sprintf("[message_publish] publisher %q topic %q error: %v", publisherName, staticTopic, err)
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// TODO: If messageIDSlot is set, capture the message ID returned by the broker.
			// This requires extending the messaging.PublishRequest or PublishResponse.
			_ = messageIDSlot // suppress unused warning for now

			return state.PC + 1
		},
	})

	return nil
}

// parseStaticHeaders parses a comma-separated "key1=val1,key2=val2" format
// into a map[string]string. Whitespace around keys, values, and delimiters is trimmed.
// Returns nil if the input is empty.
func parseStaticHeaders(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	result := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(kv) == 2 {
			result[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return result
}
