package control

import (
	"fmt"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileMQTTPublish handles the "mqtt_publish" step type.
//
// Step input keys:
//
//	broker         — broker name (required; must exist in MQTTPool)
//	topic          — static topic string (required if topic_slot not set)
//	topic_slot     — var name whose slot holds the topic (use if topic is dynamic)
//	payload_slot   — var name whose slot holds the message payload (required)
//	qos            — quality of service: 0 or 1 (default: 0). QoS 2 is not supported.
//	retained       — "true" to set the retained flag (default: false)
//
// At bake time, either 'topic' or 'topic_slot' must be set. If topic_slot is set,
// the compiler validates it exists; otherwise, the static topic string is baked.
func (c *Compiler) compileMQTTPublish(step StepConfig) error {
	brokerName := step.Input["broker"]
	if brokerName == "" {
		return fmt.Errorf("mqtt_publish: 'broker' is required")
	}

	payloadSlotStr := step.Input["payload_slot"]
	if payloadSlotStr == "" {
		return fmt.Errorf("mqtt_publish: 'payload_slot' is required")
	}

	payloadSlot, err := c.getSlot(payloadSlotStr)
	if err != nil {
		return fmt.Errorf("mqtt_publish payload_slot: %w", err)
	}

	// Determine if topic is static or dynamic
	topicStr := step.Input["topic"]
	topicSlotStr := step.Input["topic_slot"]

	var staticTopic []byte
	var topicSlot int = -1

	if topicSlotStr != "" {
		// Dynamic topic from slot
		slot, err := c.getSlot(topicSlotStr)
		if err != nil {
			return fmt.Errorf("mqtt_publish topic_slot: %w", err)
		}
		topicSlot = slot
	} else if topicStr != "" {
		// Static topic
		staticTopic = []byte(topicStr)
	} else {
		return fmt.Errorf("mqtt_publish: either 'topic' or 'topic_slot' must be set")
	}

	// Parse QoS (default 0)
	qosStr := step.Input["qos"]
	var qos byte = 0
	if qosStr != "" {
		var qosVal int
		_, err := fmt.Sscanf(qosStr, "%d", &qosVal)
		if err != nil {
			return fmt.Errorf("mqtt_publish: invalid qos %q: %w", qosStr, err)
		}
		if qosVal < 0 || qosVal > 1 {
			return fmt.Errorf("mqtt_publish: qos 2 not supported; must be 0 or 1")
		}
		qos = byte(qosVal)
	}

	// Parse retained flag (default false)
	retained := step.Input["retained"] == "true"

	c.GlobalTable = append(c.GlobalTable, steps.MQTTPublish(steps.MQTTPublishConfig{
		BrokerName:  brokerName,
		StaticTopic: staticTopic,
		TopicSlot:   topicSlot,
		PayloadSlot: payloadSlot,
		QoS:         qos,
		Retained:    retained,
	}))

	return nil
}

// compileMQTTCall handles the "mqtt_call" step type (request-reply pattern).
//
// Step input keys:
//
//	broker              — broker name (required; must exist in MQTTPool)
//	topic               — static request topic (required if topic_slot not set)
//	topic_slot          — var name whose slot holds the request topic (use if dynamic)
//	payload_slot        — var name whose slot holds the request payload (required)
//	reply_topic         — static reply topic (required if reply_topic_slot not set)
//	reply_topic_slot    — var name whose slot holds the reply topic (use if dynamic)
//	response_slot       — var name whose slot will store the reply payload (required)
//	qos                 — quality of service: 0 or 1 (default: 0). QoS 2 is not supported.
//	retained            — "true" to set the retained flag on the request (default: false)
//	timeout_ms          — timeout in milliseconds for waiting for reply (default: 30000)
//
// The instruction publishes to the request topic and subscribes to the reply topic,
// waiting up to timeout_ms for a response. If a response arrives, it is copied into
// response_slot; if the timeout expires, ctx.Failed is set.
func (c *Compiler) compileMQTTCall(step StepConfig) error {
	brokerName := step.Input["broker"]
	if brokerName == "" {
		return fmt.Errorf("mqtt_call: 'broker' is required")
	}

	payloadSlotStr := step.Input["payload_slot"]
	if payloadSlotStr == "" {
		return fmt.Errorf("mqtt_call: 'payload_slot' is required")
	}

	payloadSlot, err := c.getSlot(payloadSlotStr)
	if err != nil {
		return fmt.Errorf("mqtt_call payload_slot: %w", err)
	}

	// Determine request topic (static or dynamic)
	topicStr := step.Input["topic"]
	topicSlotStr := step.Input["topic_slot"]

	var staticTopic []byte
	var topicSlot int = -1

	if topicSlotStr != "" {
		slot, err := c.getSlot(topicSlotStr)
		if err != nil {
			return fmt.Errorf("mqtt_call topic_slot: %w", err)
		}
		topicSlot = slot
	} else if topicStr != "" {
		staticTopic = []byte(topicStr)
	} else {
		return fmt.Errorf("mqtt_call: either 'topic' or 'topic_slot' must be set")
	}

	// Determine reply topic (static or dynamic)
	replyTopicStr := step.Input["reply_topic"]
	replyTopicSlotStr := step.Input["reply_topic_slot"]

	var staticReplyTopic []byte
	var replyTopicSlot int = -1

	if replyTopicSlotStr != "" {
		slot, err := c.getSlot(replyTopicSlotStr)
		if err != nil {
			return fmt.Errorf("mqtt_call reply_topic_slot: %w", err)
		}
		replyTopicSlot = slot
	} else if replyTopicStr != "" {
		staticReplyTopic = []byte(replyTopicStr)
	} else {
		return fmt.Errorf("mqtt_call: either 'reply_topic' or 'reply_topic_slot' must be set")
	}

	// Get response slot
	responseSlotStr := step.Input["response_slot"]
	if responseSlotStr == "" {
		return fmt.Errorf("mqtt_call: 'response_slot' is required")
	}

	responseSlot, err := c.getSlot(responseSlotStr)
	if err != nil {
		return fmt.Errorf("mqtt_call response_slot: %w", err)
	}

	// Parse QoS (default 0)
	qosStr := step.Input["qos"]
	var qos byte = 0
	if qosStr != "" {
		var qosVal int
		_, err := fmt.Sscanf(qosStr, "%d", &qosVal)
		if err != nil {
			return fmt.Errorf("mqtt_call: invalid qos %q: %w", qosStr, err)
		}
		if qosVal < 0 || qosVal > 1 {
			return fmt.Errorf("mqtt_call: qos 2 not supported; must be 0 or 1")
		}
		qos = byte(qosVal)
	}

	// Parse retained flag
	retained := step.Input["retained"] == "true"

	// Parse timeout (default 30000 ms = 30 seconds)
	timeoutMs := 30000
	if timeoutStr := step.Input["timeout_ms"]; timeoutStr != "" {
		_, err := fmt.Sscanf(timeoutStr, "%d", &timeoutMs)
		if err != nil {
			return fmt.Errorf("mqtt_call: invalid timeout_ms %q: %w", timeoutStr, err)
		}
	}

	c.GlobalTable = append(c.GlobalTable, steps.MQTTCall(steps.MQTTCallConfig{
		MQTTPublishConfig: steps.MQTTPublishConfig{
			BrokerName:  brokerName,
			StaticTopic: staticTopic,
			TopicSlot:   topicSlot,
			PayloadSlot: payloadSlot,
			QoS:         qos,
			Retained:    retained,
		},
		StaticReplyTopic: staticReplyTopic,
		ReplyTopicSlot:   replyTopicSlot,
		ResponseSlot:     responseSlot,
		TimeoutMs:        timeoutMs,
	}))

	return nil
}
