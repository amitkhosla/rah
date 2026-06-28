package mqtt

import (
	"testing"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// TestNewBrokerPool verifies pool creation.
func TestNewBrokerPool(t *testing.T) {
	pool := NewBrokerPool()
	if pool == nil {
		t.Errorf("NewBrokerPool returned nil")
	}
	if len(pool.clients) != 0 {
		t.Errorf("expected empty pool, got %d clients", len(pool.clients))
	}
}

// TestBrokerPoolAdd verifies adding a client to the pool.
func TestBrokerPoolAdd(t *testing.T) {
	pool := NewBrokerPool()

	// Create a mock client (we won't actually connect)
	opts := paho.NewClientOptions().AddBroker("tcp://localhost:1883")
	client := paho.NewClient(opts)

	pool.Add("test_broker", client)

	if len(pool.clients) != 1 {
		t.Errorf("expected 1 client in pool, got %d", len(pool.clients))
	}
}

// TestBrokerPoolGet verifies retrieving a client from the pool.
func TestBrokerPoolGet(t *testing.T) {
	pool := NewBrokerPool()

	opts := paho.NewClientOptions().AddBroker("tcp://localhost:1883")
	client := paho.NewClient(opts)

	pool.Add("test_broker", client)

	retrieved, err := pool.Get("test_broker")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if retrieved == nil {
		t.Errorf("retrieved client is nil")
	}
}

// TestBrokerPoolGetNotFound verifies error handling for missing broker.
func TestBrokerPoolGetNotFound(t *testing.T) {
	pool := NewBrokerPool()

	_, err := pool.Get("nonexistent")
	if err == nil {
		t.Errorf("expected error for nonexistent broker")
	}
}
