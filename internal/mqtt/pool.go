package mqtt

import (
	"fmt"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// GlobalMQTTPool is the gateway-wide MQTT broker pool.
// Initialized in main.go during startup; read-only after that.
var GlobalMQTTPool *BrokerPool

// BrokerPool manages a collection of MQTT client connections keyed by broker name.
// The clients map is read-only after NewBrokerPool returns, making it safe for
// concurrent access without any locking.
type BrokerPool struct {
	clients map[string]paho.Client
}

// NewBrokerPool creates an empty broker pool ready for addition of client connections.
func NewBrokerPool() *BrokerPool {
	return &BrokerPool{
		clients: make(map[string]paho.Client),
	}
}

// Add registers a new MQTT client in the pool under the given broker name.
// Must be called during initialization; not safe for concurrent calls.
func (p *BrokerPool) Add(name string, client paho.Client) {
	p.clients[name] = client
}

// Get retrieves an MQTT client by broker name.
// Returns an error if the broker name is not found.
// Safe for concurrent calls (read-only map access).
func (p *BrokerPool) Get(name string) (paho.Client, error) {
	client, ok := p.clients[name]
	if !ok {
		return nil, fmt.Errorf("mqtt broker %q not found", name)
	}
	return client, nil
}

// DisconnectAll disconnects all MQTT clients in the pool.
// quiesceMs is the grace period (in milliseconds) to wait for in-flight messages.
// Called at gateway shutdown.
func (p *BrokerPool) DisconnectAll(quiesceMs uint) {
	for _, client := range p.clients {
		if client != nil && client.IsConnected() {
			client.Disconnect(quiesceMs)
		}
	}
}
