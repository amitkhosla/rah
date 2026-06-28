package control

// MQTTStepDescriptors returns the step descriptors for MQTT-related steps.
func MQTTStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:           "mqtt_publish",
			Title:          "MQTT Publish",
			Category:       "messaging",
			Capability:     "mqtt",
			Description:    "Publish a message to an MQTT topic. Topic can be static or dynamic (from a slot).",
			SupportsNested: false,
			Defaults: map[string]string{
				"broker":         "",
				"topic":          "",
				"payload_slot":   "",
				"qos":            "0",
				"retained":       "false",
			},
			Fields: []StepField{
				sf("broker", "Broker Name", "Name of the MQTT broker to publish to (must be configured in mqtt.brokers)", "my_broker"),
				sf("topic", "Topic (Static)", "The MQTT topic to publish to (leave empty to use topic_slot)", "devices/sensor/temperature"),
				sf("topic_slot", "Topic (Dynamic Slot)", "Variable name whose slot holds the topic string (use instead of topic for dynamic values)", "topic_var"),
				sf("payload_slot", "Payload Slot", "Variable name whose slot holds the message payload bytes (required)", "message_body"),
				sf("qos", "QoS", "Quality of Service: 0 (at most once) or 1 (at least once). QoS 2 is not supported.", "0"),
				sf("retained", "Retained", "Set to 'true' to retain the message on the broker (default: false)", "false"),
			},
		},
		{
			Type:           "mqtt_call",
			Title:          "MQTT Call (Request-Reply)",
			Category:       "messaging",
			Capability:     "mqtt",
			Description:    "Publish a message to an MQTT topic and wait for a reply on another topic. Implements request-reply pattern.",
			SupportsNested: false,
			Defaults: map[string]string{
				"broker":           "",
				"topic":            "",
				"payload_slot":     "",
				"reply_topic":      "",
				"response_slot":    "",
				"qos":              "0",
				"retained":         "false",
				"timeout_ms":       "30000",
			},
			Fields: []StepField{
				sf("broker", "Broker Name", "Name of the MQTT broker (must be configured in mqtt.brokers)", "my_broker"),
				sf("topic", "Request Topic (Static)", "The MQTT topic to publish the request to (leave empty to use topic_slot)", "devices/requests"),
				sf("topic_slot", "Request Topic (Dynamic Slot)", "Variable name whose slot holds the request topic string (use for dynamic values)", "req_topic_var"),
				sf("payload_slot", "Request Payload Slot", "Variable name whose slot holds the request message payload (required)", "request_body"),
				sf("reply_topic", "Reply Topic (Static)", "The MQTT topic to subscribe to for the reply (leave empty to use reply_topic_slot)", "devices/replies"),
				sf("reply_topic_slot", "Reply Topic (Dynamic Slot)", "Variable name whose slot holds the reply topic string (use for dynamic values)", "reply_topic_var"),
				sf("response_slot", "Response Slot", "Variable name whose slot will store the reply payload bytes (required)", "response_body"),
				sf("qos", "QoS", "Quality of Service: 0 (at most once) or 1 (at least once). QoS 2 is not supported.", "0"),
				sf("retained", "Retained", "Set to 'true' to retain the request message on the broker (default: false)", "false"),
				sf("timeout_ms", "Reply Timeout (ms)", "Maximum time to wait for a reply in milliseconds (default: 30000 = 30 seconds)", "30000"),
			},
		},
	}
}
