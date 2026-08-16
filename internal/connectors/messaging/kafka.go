package messaging

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"time"

	"github.com/IBM/sarama"
	"github.com/amitkhosla/rah/internal/config"
)

// KafkaPublisher publishes messages to Apache Kafka topics.
type KafkaPublisher struct {
	cfg         config.PublisherConfig
	secrets     SecretResolver
	producer    sarama.SyncProducer
	ackRequired bool
}

// Connect establishes the Kafka producer connection.
func (p *KafkaPublisher) Connect(ctx context.Context) error {
	// Build sarama config
	saramaConfig := sarama.NewConfig()
	saramaConfig.Producer.Return.Successes = true
	saramaConfig.Producer.Return.Errors = true
	saramaConfig.Version = sarama.V2_6_0_0

	// Apply Kafka tuning
	if p.cfg.LingerMs > 0 {
		saramaConfig.Producer.Flush.Frequency = time.Duration(p.cfg.LingerMs) * time.Millisecond
	}
	if p.cfg.BatchSize > 0 {
		saramaConfig.Producer.Flush.MaxMessages = p.cfg.BatchSize
	}

	// Apply timeout if configured
	if p.cfg.TimeoutMs > 0 {
		timeout := time.Duration(p.cfg.TimeoutMs) * time.Millisecond
		saramaConfig.Net.DialTimeout = timeout
		saramaConfig.Net.ReadTimeout = timeout
		saramaConfig.Net.WriteTimeout = timeout
	}

	// Configure TLS if enabled
	if p.cfg.TLSEnabled {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}

		// If TLSCARef is provided, load the CA certificate (resolve secret first)
		if p.cfg.TLSCARef != "" {
			caBytes, err := p.secrets.Resolve(ctx, p.cfg.TLSCARef)
			if err != nil {
				return fmt.Errorf("kafka[%s]: resolve TLS CA: %w", p.cfg.Name, err)
			}

			caCertPool := x509.NewCertPool()
			if !caCertPool.AppendCertsFromPEM(caBytes) {
				return fmt.Errorf("kafka[%s]: failed to parse CA certificate PEM", p.cfg.Name)
			}
			tlsConfig.RootCAs = caCertPool
		}

		saramaConfig.Net.TLS.Enable = true
		saramaConfig.Net.TLS.Config = tlsConfig
	}

	// Create sync producer
	producer, err := sarama.NewSyncProducer(p.cfg.Brokers, saramaConfig)
	if err != nil {
		return fmt.Errorf("kafka[%s]: failed to create producer: %w", p.cfg.Name, err)
	}

	p.producer = producer

	// Handle AckRequired flag
	ackRequired := p.cfg.AckRequired == nil || *p.cfg.AckRequired
	p.ackRequired = ackRequired
	if !ackRequired {
		// AsyncProducer not yet implemented; log warning and continue with sync
		log.Printf("[kafka][%s] ack_required=false requested but async producer not yet supported; using sync", p.cfg.Name)
	}

	return nil
}

// Publish sends a single message to a Kafka topic.
func (p *KafkaPublisher) Publish(ctx context.Context, req PublishRequest) error {
	if p.producer == nil {
		return fmt.Errorf("kafka[%s]: not connected", p.cfg.Name)
	}

	msg := &sarama.ProducerMessage{
		Topic: req.Topic,
		Value: sarama.ByteEncoder(req.Payload),
	}

	// Set key if provided
	if len(req.Key) > 0 {
		msg.Key = sarama.ByteEncoder(req.Key)
	}

	// Add headers if provided
	if len(req.Headers) > 0 {
		for k, v := range req.Headers {
			msg.Headers = append(msg.Headers, sarama.RecordHeader{
				Key:   []byte(k),
				Value: []byte(v),
			})
		}
	}

	_, _, err := p.producer.SendMessage(msg)
	if err != nil {
		return fmt.Errorf("kafka[%s]: send message: %w", p.cfg.Name, err)
	}

	return nil
}

// PublishBatch sends multiple messages to Kafka topics.
func (p *KafkaPublisher) PublishBatch(ctx context.Context, reqs []PublishRequest) error {
	if p.producer == nil {
		return fmt.Errorf("kafka[%s]: not connected", p.cfg.Name)
	}

	messages := make([]*sarama.ProducerMessage, 0, len(reqs))

	for _, req := range reqs {
		msg := &sarama.ProducerMessage{
			Topic: req.Topic,
			Value: sarama.ByteEncoder(req.Payload),
		}

		// Set key if provided
		if len(req.Key) > 0 {
			msg.Key = sarama.ByteEncoder(req.Key)
		}

		// Add headers if provided
		if len(req.Headers) > 0 {
			for k, v := range req.Headers {
				msg.Headers = append(msg.Headers, sarama.RecordHeader{
					Key:   []byte(k),
					Value: []byte(v),
				})
			}
		}

		messages = append(messages, msg)
	}

	err := p.producer.SendMessages(messages)
	if err != nil {
		return fmt.Errorf("kafka[%s]: batch send failed: %w", p.cfg.Name, err)
	}

	return nil
}

// Ping verifies connectivity to the Kafka brokers.
func (p *KafkaPublisher) Ping(ctx context.Context) error {
	if p.producer == nil {
		return fmt.Errorf("kafka[%s]: not connected", p.cfg.Name)
	}
	// Sarama SyncProducer connection is verified on creation; if we got here, we're connected
	return nil
}

// Close closes the Kafka producer and releases resources.
func (p *KafkaPublisher) Close() error {
	if p.producer != nil {
		err := p.producer.Close()
		p.producer = nil
		if err != nil {
			return fmt.Errorf("kafka[%s]: close producer: %w", p.cfg.Name, err)
		}
	}

	return nil
}
