package steps

import (
	"fmt"
	"time"
	"unsafe"

	paho "github.com/eclipse/paho.mqtt.golang"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// MQTTPublishConfig holds bake-time configuration for an mqtt_publish instruction.
type MQTTPublishConfig struct {
	// BrokerName identifies which MQTT broker to use (must exist in MQTTPool).
	BrokerName string

	// StaticTopic is the pre-baked topic string (used when TopicSlot == -1).
	StaticTopic []byte

	// TopicSlot is the ByteSlot index for dynamic topic values (-1 means use StaticTopic).
	TopicSlot int

	// PayloadSlot is the ByteSlot index for the message payload.
	PayloadSlot int

	// QoS is the MQTT quality of service level (0 or 1).
	QoS byte

	// Retained indicates whether the message should be retained by the broker.
	Retained bool
}

// MQTTCallConfig holds bake-time configuration for an mqtt_call instruction (request-reply).
type MQTTCallConfig struct {
	MQTTPublishConfig

	// StaticReplyTopic is the pre-baked reply topic string (used when ReplyTopicSlot == -1).
	StaticReplyTopic []byte

	// ReplyTopicSlot is the ByteSlot index for dynamic reply topic values (-1 means use StaticReplyTopic).
	ReplyTopicSlot int

	// ResponseSlot is the ByteSlot index where the reply payload will be stored.
	ResponseSlot int

	// TimeoutMs is the maximum time (in milliseconds) to wait for a reply.
	TimeoutMs int
}

// MQTTPublish creates an instruction that publishes a message to an MQTT topic.
// The topic can be static (pre-baked) or dynamic (read from a ByteSlot).
// Topic zero-copy is achieved via unsafe.String(unsafe.SliceData(...), len(...)).
func MQTTPublish(cfg MQTTPublishConfig) engine.Instruction {
	return engine.Instruction{
		Name: "MQTT_PUBLISH",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Resolve the topic: static or from slot
			var topic string
			if cfg.TopicSlot == -1 {
				topic = unsafe.String(unsafe.SliceData(cfg.StaticTopic), len(cfg.StaticTopic))
			} else {
				topicBytes := ctx.ByteSlots[cfg.TopicSlot]
				topic = unsafe.String(unsafe.SliceData(topicBytes), len(topicBytes))
			}

			// Get the payload from the slot
			payload := ctx.ByteSlots[cfg.PayloadSlot]

			// Acquire the MQTT client from the pool
			if ctx.MQTTPool == nil {
				ctx.Failed = true
				ctx.ResponseStatus = 500
				ctx.ErrorCode = 500
				msg := "mqtt_publish: MQTT pool not initialized"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}
			broker, err := ctx.MQTTPool.Get(cfg.BrokerName)
			if err != nil {
				ctx.Failed = true
				ctx.ResponseStatus = 500
				ctx.ErrorCode = 500
				msg := fmt.Sprintf("mqtt_publish: %v", err)
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// Publish the message
			token := broker.Publish(topic, cfg.QoS, cfg.Retained, payload)
			if cfg.QoS == 1 {
				// For QoS 1, wait for PUBACK
				if !token.WaitTimeout(10 * time.Second) {
					ctx.Failed = true
					ctx.ResponseStatus = 504
					ctx.ErrorCode = 504
					msg := "mqtt_publish: QoS 1 timeout"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
				if token.Error() != nil {
					ctx.Failed = true
					ctx.ResponseStatus = 500
					ctx.ErrorCode = 500
					msg := fmt.Sprintf("mqtt_publish: %v", token.Error())
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					return engine.StopPlan
				}
			}
			// For QoS 0, return immediately (no PUBACK wait)

			// Return next PC
			return state.PC + 1
		},
	}
}

// MQTTCall creates an instruction that publishes a message to an MQTT topic
// and waits for a reply on a reply topic.
// Uses a one-shot subscribe + publish + await pattern with timeout.
// The response is copied to ctx.Alloc() before the function returns to avoid
// goroutine leaks from the background MQTT goroutines.
func MQTTCall(cfg MQTTCallConfig) engine.Instruction {
	return engine.Instruction{
		Name: "MQTT_CALL",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Resolve the publish topic
			var pubTopic string
			if cfg.TopicSlot == -1 {
				pubTopic = unsafe.String(unsafe.SliceData(cfg.StaticTopic), len(cfg.StaticTopic))
			} else {
				topicBytes := ctx.ByteSlots[cfg.TopicSlot]
				pubTopic = unsafe.String(unsafe.SliceData(topicBytes), len(topicBytes))
			}

			// Resolve the reply topic
			var replyTopic string
			if cfg.ReplyTopicSlot == -1 {
				replyTopic = unsafe.String(unsafe.SliceData(cfg.StaticReplyTopic), len(cfg.StaticReplyTopic))
			} else {
				replyTopicBytes := ctx.ByteSlots[cfg.ReplyTopicSlot]
				replyTopic = unsafe.String(unsafe.SliceData(replyTopicBytes), len(replyTopicBytes))
			}

			// Get the payload from the slot
			payload := ctx.ByteSlots[cfg.PayloadSlot]

			// Acquire the MQTT client from the pool
			if ctx.MQTTPool == nil {
				ctx.Failed = true
				ctx.ResponseStatus = 500
				ctx.ErrorCode = 500
				msg := "mqtt_call: MQTT pool not initialized"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}
			broker, err := ctx.MQTTPool.Get(cfg.BrokerName)
			if err != nil {
				ctx.Failed = true
				ctx.ResponseStatus = 500
				ctx.ErrorCode = 500
				msg := fmt.Sprintf("mqtt_call: %v", err)
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// Create a one-shot reply channel
			replyCh := make(chan []byte, 1)

			// Subscribe to the reply topic with a callback
			token := broker.Subscribe(replyTopic, 1, func(_ paho.Client, msg paho.Message) {
				// Send the payload on the reply channel (non-blocking, buffer=1)
				select {
				case replyCh <- msg.Payload():
				default:
					// Channel full or closed; discard
				}
			})

			if !token.WaitTimeout(5 * time.Second) {
				ctx.Failed = true
				ctx.ResponseStatus = 504
				ctx.ErrorCode = 504
				msg := "mqtt_call: subscribe timeout"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				// Unsubscribe and return
				broker.Unsubscribe(replyTopic)
				close(replyCh)
				return engine.StopPlan
			}

			if token.Error() != nil {
				ctx.Failed = true
				ctx.ResponseStatus = 500
				ctx.ErrorCode = 500
				msg := fmt.Sprintf("mqtt_call: subscribe error: %v", token.Error())
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				close(replyCh)
				return engine.StopPlan
			}

			// Publish the message
			pubToken := broker.Publish(pubTopic, cfg.QoS, cfg.Retained, payload)
			if cfg.QoS == 1 {
				// Wait for PUBACK for QoS 1
				if !pubToken.WaitTimeout(10 * time.Second) {
					ctx.Failed = true
					ctx.ResponseStatus = 504
					ctx.ErrorCode = 504
					msg := "mqtt_call: publish QoS 1 timeout"
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					broker.Unsubscribe(replyTopic)
					close(replyCh)
					return engine.StopPlan
				}
				if pubToken.Error() != nil {
					ctx.Failed = true
					ctx.ResponseStatus = 500
					ctx.ErrorCode = 500
					msg := fmt.Sprintf("mqtt_call: publish error: %v", pubToken.Error())
					ctx.ErrorMsg = ctx.Alloc(len(msg))
					copy(ctx.ErrorMsg, msg)
					broker.Unsubscribe(replyTopic)
					close(replyCh)
					return engine.StopPlan
				}
			}

			// Wait for the reply with timeout
			timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
			select {
			case response := <-replyCh:
				// Copy response to context (to prevent goroutine leak from MQTT callback)
				respCopy := ctx.Alloc(len(response))
				copy(respCopy, response)
				ctx.ByteSlots[cfg.ResponseSlot] = respCopy
			case <-time.After(timeout):
				ctx.Failed = true
				ctx.ResponseStatus = 504
				ctx.ErrorCode = 504
				msg := fmt.Sprintf("mqtt_call: timeout waiting for reply (%d ms)", cfg.TimeoutMs)
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
			}

			// Clean up: unsubscribe and close the channel
			broker.Unsubscribe(replyTopic)
			close(replyCh)

			return state.PC + 1
		},
	}
}
