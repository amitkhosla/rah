package control

import (
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	mochiserver "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	mqttpool "rah/internal/mqtt"
)

// setupTestMQTTBroker creates an in-process MQTT broker for testing.
func setupTestMQTTBroker(t *testing.T) (string, *mochiserver.Server) {
	broker := mochiserver.New(&mochiserver.Options{})
	err := broker.AddListener(listeners.NewTCP(listeners.Config{ID: "test", Address: "127.0.0.1:0"}))
	if err != nil {
		t.Fatalf("failed to add TCP listener: %v", err)
	}

	go func() {
		if err := broker.Serve(); err != nil {
			t.Logf("broker error: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)

	l, ok := broker.Listeners.Get("test")
	if !ok {
		t.Fatalf("test listener not found on broker")
	}
	addr := l.Address()
	return "tcp://" + addr, broker
}

// createTestMQTTPool creates a BrokerPool with a connected client.
func createTestMQTTPool(t *testing.T, brokerURL string) *mqttpool.BrokerPool {
	pool := mqttpool.NewBrokerPool()

	opts := paho.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("compiler-test-client").
		SetAutoReconnect(false)

	client := paho.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(5 * time.Second) {
		t.Fatalf("timeout connecting to MQTT broker")
	}
	if token.Error() != nil {
		t.Fatalf("failed to connect to MQTT broker: %v", token.Error())
	}

	pool.Add("test_broker", client)
	return pool
}

// TestCompileMQTTPublishStaticTopic verifies mqtt_publish compilation with static topic.
func TestCompileMQTTPublishStaticTopic(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	compiler := NewCompiler(nil)

	// Compile mqtt_publish step
	step := StepConfig{
		Action: "mqtt_publish",
		Input: map[string]string{
			"broker":       "test_broker",
			"topic":        "devices/sensor",
			"payload_slot": "message",
			"qos":          "0",
			"retained":     "false",
		},
	}

	err := compiler.compileMQTTPublish(step)
	if err != nil {
		t.Fatalf("compilation failed: %v", err)
	}

	if len(compiler.GlobalTable) == 0 {
		t.Errorf("no instruction was compiled")
	}
}

// TestCompileMQTTPublishDynamicTopic verifies mqtt_publish compilation with dynamic topic.
func TestCompileMQTTPublishDynamicTopic(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	compiler := NewCompiler(nil)

	step := StepConfig{
		Action: "mqtt_publish",
		Input: map[string]string{
			"broker":       "test_broker",
			"topic_slot":   "topic",
			"payload_slot": "message",
			"qos":          "1",
		},
	}

	err := compiler.compileMQTTPublish(step)
	if err != nil {
		t.Fatalf("compilation failed: %v", err)
	}

	if len(compiler.GlobalTable) == 0 {
		t.Errorf("no instruction was compiled")
	}
}

// TestCompileMQTTPublishMissingBroker verifies error when broker is not specified.
func TestCompileMQTTPublishMissingBroker(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	compiler := NewCompiler(nil)

	step := StepConfig{
		Action: "mqtt_publish",
		Input: map[string]string{
			"topic":        "devices/sensor",
			"payload_slot": "message",
		},
	}

	err := compiler.compileMQTTPublish(step)
	if err == nil {
		t.Errorf("expected error for missing broker")
	}
}

// TestCompileMQTTPublishMissingTopic verifies error when both topic and topic_slot are missing.
func TestCompileMQTTPublishMissingTopic(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	compiler := NewCompiler(nil)

	step := StepConfig{
		Action: "mqtt_publish",
		Input: map[string]string{
			"broker":       "test_broker",
			"payload_slot": "message",
		},
	}

	err := compiler.compileMQTTPublish(step)
	if err == nil {
		t.Errorf("expected error for missing topic")
	}
}

// TestCompileMQTTPublishInvalidQoS verifies error for invalid QoS.
func TestCompileMQTTPublishInvalidQoS(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	compiler := NewCompiler(nil)

	step := StepConfig{
		Action: "mqtt_publish",
		Input: map[string]string{
			"broker":       "test_broker",
			"topic":        "devices/sensor",
			"payload_slot": "message",
			"qos":          "2",
		},
	}

	err := compiler.compileMQTTPublish(step)
	if err == nil {
		t.Errorf("expected error for QoS 2 (not supported)")
	}
}

// TestCompileMQTTCall verifies mqtt_call compilation.
func TestCompileMQTTCall(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	compiler := NewCompiler(nil)

	step := StepConfig{
		Action: "mqtt_call",
		Input: map[string]string{
			"broker":          "test_broker",
			"topic":           "devices/request",
			"payload_slot":    "message",
			"reply_topic":     "devices/reply",
			"response_slot":   "response",
			"qos":             "1",
			"timeout_ms":      "5000",
		},
	}

	err := compiler.compileMQTTCall(step)
	if err != nil {
		t.Fatalf("compilation failed: %v", err)
	}

	if len(compiler.GlobalTable) == 0 {
		t.Errorf("no instruction was compiled")
	}
}

// TestCompileMQTTCallMissingReplyTopic verifies error when reply_topic is missing.
func TestCompileMQTTCallMissingReplyTopic(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	compiler := NewCompiler(nil)

	step := StepConfig{
		Action: "mqtt_call",
		Input: map[string]string{
			"broker":        "test_broker",
			"topic":         "devices/request",
			"payload_slot":  "message",
			"response_slot": "response",
		},
	}

	err := compiler.compileMQTTCall(step)
	if err == nil {
		t.Errorf("expected error for missing reply_topic")
	}
}

// TestCompileMQTTCallMissingResponseSlot verifies error when response_slot is missing.
func TestCompileMQTTCallMissingResponseSlot(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	compiler := NewCompiler(nil)

	step := StepConfig{
		Action: "mqtt_call",
		Input: map[string]string{
			"broker":       "test_broker",
			"topic":        "devices/request",
			"payload_slot": "message",
			"reply_topic":  "devices/reply",
		},
	}

	err := compiler.compileMQTTCall(step)
	if err == nil {
		t.Errorf("expected error for missing response_slot")
	}
}

// TestMQTTStepDescriptors verifies the step descriptors are properly defined.
func TestMQTTStepDescriptors(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	descriptors := MQTTStepDescriptors()
	if len(descriptors) != 2 {
		t.Errorf("expected 2 MQTT step descriptors, got %d", len(descriptors))
	}

	// Verify mqtt_publish descriptor
	var publishDesc *StepDescriptor
	var callDesc *StepDescriptor
	for i := range descriptors {
		if descriptors[i].Type == "mqtt_publish" {
			publishDesc = &descriptors[i]
		} else if descriptors[i].Type == "mqtt_call" {
			callDesc = &descriptors[i]
		}
	}

	if publishDesc == nil {
		t.Errorf("mqtt_publish descriptor not found")
	} else {
		if publishDesc.Title != "MQTT Publish" {
			t.Errorf("unexpected title: %s", publishDesc.Title)
		}
		if publishDesc.Category != "messaging" {
			t.Errorf("unexpected category: %s", publishDesc.Category)
		}
	}

	if callDesc == nil {
		t.Errorf("mqtt_call descriptor not found")
	} else {
		if callDesc.Title != "MQTT Call (Request-Reply)" {
			t.Errorf("unexpected title: %s", callDesc.Title)
		}
		if callDesc.Category != "messaging" {
			t.Errorf("unexpected category: %s", callDesc.Category)
		}
	}
}
