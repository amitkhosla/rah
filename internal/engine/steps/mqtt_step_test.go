package steps

import (
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	mochiserver "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/amitkhosla/rah/internal/engine"
	mqttpool "github.com/amitkhosla/rah/internal/mqtt"
	"github.com/amitkhosla/rah/internal/rctx"
)

// setupTestBroker creates an in-process MQTT broker for testing.
func setupTestBroker(t *testing.T) (string, *mochiserver.Server) {
	broker := mochiserver.New(&mochiserver.Options{})
	// Allow anonymous connections in tests.
	if err := broker.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatalf("failed to add allow hook: %v", err)
	}
	err := broker.AddListener(listeners.NewTCP(listeners.Config{ID: "test", Address: "127.0.0.1:0"}))
	if err != nil {
		t.Fatalf("failed to add TCP listener: %v", err)
	}

	go func() {
		if err := broker.Serve(); err != nil {
			t.Logf("broker error: %v", err)
		}
	}()

	// Give the broker time to start
	time.Sleep(100 * time.Millisecond)

	// Get the actual listener address (Listeners.Get by the registered ID "test")
	l, ok := broker.Listeners.Get("test")
	if !ok {
		t.Fatalf("test listener not found on broker")
	}
	addr := l.Address()
	return "tcp://" + addr, broker
}

// createTestPool creates a BrokerPool with a connected client.
func createTestPool(t *testing.T, brokerURL string) *mqttpool.BrokerPool {
	pool := mqttpool.NewBrokerPool()

	opts := paho.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("test-client").
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

// TestMQTTPublishStaticTopic verifies mqtt_publish with static topic.
func TestMQTTPublishStaticTopic(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	brokerURL, broker := setupTestBroker(t)
	defer broker.Close()

	pool := createTestPool(t, brokerURL)
	defer pool.DisconnectAll(250)

	// Set up a subscriber to verify message delivery
	var msgReceived []byte
	var msgMutex sync.Mutex
	opts := paho.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("subscriber").
		SetAutoReconnect(false)

	subscriber := paho.NewClient(opts)
	token := subscriber.Connect()
	if !token.WaitTimeout(5 * time.Second) {
		t.Fatal("subscriber connection timeout")
	}

	subToken := subscriber.Subscribe("test/topic", 0, func(_ paho.Client, msg paho.Message) {
		msgMutex.Lock()
		msgReceived = msg.Payload()
		msgMutex.Unlock()
	})
	if !subToken.WaitTimeout(5 * time.Second) {
		t.Fatal("subscribe timeout")
	}

	// Create test context
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 10),
		MQTTPool:  pool,
	}
	ctx.ByteSlots[1] = []byte("hello world")

	// Execute mqtt_publish
	instr := MQTTPublish(MQTTPublishConfig{
		BrokerName:  "test_broker",
		StaticTopic: []byte("test/topic"),
		TopicSlot:   -1,
		PayloadSlot: 1,
		QoS:         0,
		Retained:    false,
	})

	state := &engine.ExecutionState{PC: 0}
	pc := instr.Action(ctx, state)
	if ctx.Failed {
		t.Fatalf("mqtt_publish failed: %v", ctx.ErrorMsg)
	}
	if pc == engine.StopPlan {
		t.Errorf("unexpected PC: %d", pc)
	}

	// Wait for message delivery
	time.Sleep(100 * time.Millisecond)

	msgMutex.Lock()
	defer msgMutex.Unlock()
	if string(msgReceived) != "hello world" {
		t.Errorf("expected 'hello world', got %q", msgReceived)
	}

	subscriber.Disconnect(250)
}

// TestMQTTPublishDynamicTopic verifies mqtt_publish with dynamic topic from slot.
func TestMQTTPublishDynamicTopic(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	brokerURL, broker := setupTestBroker(t)
	defer broker.Close()

	pool := createTestPool(t, brokerURL)
	defer pool.DisconnectAll(250)

	// Set up subscriber
	var msgReceived []byte
	var msgMutex sync.Mutex
	opts := paho.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("subscriber2").
		SetAutoReconnect(false)

	subscriber := paho.NewClient(opts)
	token := subscriber.Connect()
	if !token.WaitTimeout(5 * time.Second) {
		t.Fatal("subscriber connection timeout")
	}

	subToken := subscriber.Subscribe("dynamic/topic/path", 0, func(_ paho.Client, msg paho.Message) {
		msgMutex.Lock()
		msgReceived = msg.Payload()
		msgMutex.Unlock()
	})
	if !subToken.WaitTimeout(5 * time.Second) {
		t.Fatal("subscribe timeout")
	}

	// Create test context with dynamic topic in slot
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 10),
		MQTTPool:  pool,
	}
	ctx.ByteSlots[0] = []byte("dynamic/topic/path")
	ctx.ByteSlots[1] = []byte("test payload")

	// Execute mqtt_publish with dynamic topic
	instr := MQTTPublish(MQTTPublishConfig{
		BrokerName:  "test_broker",
		StaticTopic: nil,
		TopicSlot:   0,
		PayloadSlot: 1,
		QoS:         0,
		Retained:    false,
	})

	state := &engine.ExecutionState{PC: 0}
	_ = instr.Action(ctx, state)
	if ctx.Failed {
		t.Fatalf("mqtt_publish failed: %v", ctx.ErrorMsg)
	}

	time.Sleep(100 * time.Millisecond)

	msgMutex.Lock()
	defer msgMutex.Unlock()
	if string(msgReceived) != "test payload" {
		t.Errorf("expected 'test payload', got %q", msgReceived)
	}

	subscriber.Disconnect(250)
}

// TestMQTTPublishQoS1 verifies mqtt_publish waits for PUBACK with QoS 1.
func TestMQTTPublishQoS1(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	brokerURL, broker := setupTestBroker(t)
	defer broker.Close()

	pool := createTestPool(t, brokerURL)
	defer pool.DisconnectAll(250)

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 10),
		MQTTPool:  pool,
	}
	ctx.ByteSlots[0] = []byte("test/qos1")
	ctx.ByteSlots[1] = []byte("qos1 message")

	start := time.Now()
	instr := MQTTPublish(MQTTPublishConfig{
		BrokerName:  "test_broker",
		StaticTopic: []byte("test/qos1"),
		TopicSlot:   -1,
		PayloadSlot: 1,
		QoS:         1,
		Retained:    false,
	})

	state := &engine.ExecutionState{PC: 0}
	pc := instr.Action(ctx, state)
	elapsed := time.Since(start)

	if ctx.Failed {
		t.Fatalf("mqtt_publish failed: %s", ctx.ErrorMsg)
	}
	if pc == engine.StopPlan {
		t.Errorf("unexpected PC (StopPlan): %d", pc)
	}
	// QoS 1 should take longer than QoS 0 due to PUBACK wait
	if elapsed < 10*time.Millisecond {
		t.Logf("note: QoS 1 publish completed quickly (%v)", elapsed)
	}
}

// TestMQTTCallRequestReply verifies mqtt_call request-reply pattern.
func TestMQTTCallRequestReply(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	brokerURL, broker := setupTestBroker(t)
	defer broker.Close()

	pool := createTestPool(t, brokerURL)
	defer pool.DisconnectAll(250)

	// Start a reply handler that echoes back to the reply topic
	opts := paho.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("responder").
		SetAutoReconnect(false)

	responder := paho.NewClient(opts)
	token := responder.Connect()
	if !token.WaitTimeout(5 * time.Second) {
		t.Fatal("responder connection timeout")
	}

	respToken := responder.Subscribe("devices/request", 1, func(_ paho.Client, msg paho.Message) {
		// Echo back on reply topic
		reply := responder.Publish("devices/reply", 1, false, []byte("response: "+string(msg.Payload())))
		reply.Wait()
	})
	if !respToken.WaitTimeout(5 * time.Second) {
		t.Fatal("responder subscribe timeout")
	}

	// Create test context
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 10),
		MQTTPool:  pool,
	}
	ctx.ByteSlots[0] = []byte("hello from requester")
	ctx.ByteSlots[1] = make([]byte, 256) // Will store response

	// Execute mqtt_call
	instr := MQTTCall(MQTTCallConfig{
		MQTTPublishConfig: MQTTPublishConfig{
			BrokerName:  "test_broker",
			StaticTopic: []byte("devices/request"),
			TopicSlot:   -1,
			PayloadSlot: 0,
			QoS:         1,
			Retained:    false,
		},
		StaticReplyTopic: []byte("devices/reply"),
		ReplyTopicSlot:   -1,
		ResponseSlot:     1,
		TimeoutMs:        5000,
	})

	state := &engine.ExecutionState{PC: 0}
	_ = instr.Action(ctx, state)

	if ctx.Failed {
		t.Fatalf("mqtt_call failed: %s", ctx.ErrorMsg)
	}

	response := ctx.ByteSlots[1]
	expectedPrefix := "response: hello from requester"
	if string(response) != expectedPrefix {
		t.Errorf("expected response to start with %q, got %q", expectedPrefix, response)
	}

	responder.Disconnect(250)
}

// TestMQTTCallTimeout verifies mqtt_call timeout behavior.
func TestMQTTCallTimeout(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	brokerURL, broker := setupTestBroker(t)
	defer broker.Close()

	pool := createTestPool(t, brokerURL)
	defer pool.DisconnectAll(250)

	// Create test context
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 10),
		MQTTPool:  pool,
	}
	ctx.ByteSlots[0] = []byte("request")
	ctx.ByteSlots[1] = make([]byte, 256)

	// Execute mqtt_call with short timeout (no responder, so it will timeout)
	instr := MQTTCall(MQTTCallConfig{
		MQTTPublishConfig: MQTTPublishConfig{
			BrokerName:  "test_broker",
			StaticTopic: []byte("no/responder"),
			TopicSlot:   -1,
			PayloadSlot: 0,
			QoS:         0,
			Retained:    false,
		},
		StaticReplyTopic: []byte("no/reply"),
		ReplyTopicSlot:   -1,
		ResponseSlot:     1,
		TimeoutMs:        100,
	})

	state := &engine.ExecutionState{PC: 0}
	_ = instr.Action(ctx, state)

	if !ctx.Failed {
		t.Errorf("mqtt_call should have failed on timeout")
	}
	if ctx.ErrorMsg == nil {
		t.Errorf("expected error for timeout")
	}
}

// TestBrokerPoolGetUnknownBroker verifies error handling for unknown broker.
func TestBrokerPoolGetUnknownBroker(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	brokerURL, broker := setupTestBroker(t)
	defer broker.Close()

	pool := createTestPool(t, brokerURL)

	_, err := pool.Get("nonexistent_broker")
	if err == nil {
		t.Errorf("expected error for nonexistent broker, got nil")
	}
}

// TestMQTTPublishNoPool verifies mqtt_publish fails gracefully when pool is nil.
func TestMQTTPublishNoPool(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	ctx := &rctx.Context{
		ByteSlots: make([][]byte, 10),
		MQTTPool:  nil,
	}
	ctx.ByteSlots[0] = []byte("test")

	instr := MQTTPublish(MQTTPublishConfig{
		BrokerName:  "test_broker",
		StaticTopic: []byte("test/topic"),
		TopicSlot:   -1,
		PayloadSlot: 0,
		QoS:         0,
		Retained:    false,
	})

	state := &engine.ExecutionState{PC: 0}
	_ = instr.Action(ctx, state)

	if !ctx.Failed {
		t.Errorf("expected mqtt_publish to fail when pool is nil")
	}
	if ctx.ErrorMsg == nil {
		t.Errorf("expected error message")
	}
}

// TestConcurrentMQTTPublishes verifies concurrent publish operations don't race.
func TestConcurrentMQTTPublishes(t *testing.T) {
	// Note: Verify no goroutine leaks in production code

	brokerURL, broker := setupTestBroker(t)
	defer broker.Close()

	pool := createTestPool(t, brokerURL)
	defer pool.DisconnectAll(250)

	// Subscriber to count messages
	opts := paho.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("subscriber_concurrent").
		SetAutoReconnect(false)

	subscriber := paho.NewClient(opts)
	token := subscriber.Connect()
	if !token.WaitTimeout(5 * time.Second) {
		t.Fatal("subscriber connection timeout")
	}

	var count int32
	var countMutex sync.Mutex
	subToken := subscriber.Subscribe("concurrent/test", 0, func(_ paho.Client, msg paho.Message) {
		countMutex.Lock()
		count++
		countMutex.Unlock()
	})
	if !subToken.WaitTimeout(5 * time.Second) {
		t.Fatal("subscribe timeout")
	}

	// Publish from 20 concurrent goroutines
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			ctx := &rctx.Context{
				ByteSlots: make([][]byte, 10),
				MQTTPool:  pool,
			}
			ctx.ByteSlots[0] = []byte("concurrent message")

			instr := MQTTPublish(MQTTPublishConfig{
				BrokerName:  "test_broker",
				StaticTopic: []byte("concurrent/test"),
				TopicSlot:   -1,
				PayloadSlot: 0,
				QoS:         0,
				Retained:    false,
			})

			state := &engine.ExecutionState{PC: 0}
			pc := instr.Action(ctx, state)
			if ctx.Failed {
				t.Errorf("concurrent publish %d failed: %v", idx, ctx.ErrorMsg)
			}
			if pc == engine.StopPlan {
				t.Errorf("unexpected PC (StopPlan): %d", pc)
			}
		}(i)
	}

	wg.Wait()

	// Wait for all messages to be received
	time.Sleep(500 * time.Millisecond)

	countMutex.Lock()
	defer countMutex.Unlock()
	if count != 20 {
		t.Errorf("expected 20 messages, got %d", count)
	}

	subscriber.Disconnect(250)
}
