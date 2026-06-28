package config

// MQTTBrokerConfig defines the configuration for a single MQTT broker connection.
type MQTTBrokerConfig struct {
	// Name is the unique identifier for this broker (used in steps to select which broker to use).
	Name string `yaml:"name"`

	// BrokerURL is the MQTT broker endpoint (e.g., "tcp://broker.example.com:1883").
	BrokerURL string `yaml:"broker_url"`

	// ClientID is the MQTT client ID to use for this connection.
	ClientID string `yaml:"client_id"`

	// CredRef is an optional credential reference (e.g., "env:MQTT_PASSWORD", "gsm://...").
	// If set, the gateway will resolve it at startup and use it as the MQTT password.
	CredRef string `yaml:"cred_ref,omitempty"`

	// QoS is the MQTT quality of service level (0 or 1 only; QoS 2 is not supported).
	// 0 = at most once (no PUBACK wait), 1 = at least once (wait for PUBACK).
	QoS byte `yaml:"qos,omitempty"`

	// KeepAlive is the MQTT keep-alive interval in seconds (e.g., 60).
	KeepAlive int `yaml:"keepalive,omitempty"`

	// TLSCACertRef is an optional path or reference to a TLS CA certificate file.
	TLSCACertRef string `yaml:"tls_ca_cert_ref,omitempty"`
}

// MQTTConfig groups all MQTT broker definitions for the gateway.
type MQTTConfig struct {
	Brokers []MQTTBrokerConfig `yaml:"brokers,omitempty"`
}
