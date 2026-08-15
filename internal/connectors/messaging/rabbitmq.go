package messaging

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/rabbitmq/amqp091-go"
)

// RabbitMQPublisher publishes messages to RabbitMQ queues and exchanges.
type RabbitMQPublisher struct {
	cfg     config.PublisherConfig
	secrets SecretResolver
	conn    *amqp091.Connection
	ch      *amqp091.Channel
}

// buildTLSConfig creates a tls.Config from a CA certificate PEM.
func buildTLSConfig(caCert []byte) (*tls.Config, error) {
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate PEM")
	}

	return &tls.Config{
		RootCAs:            caCertPool,
		InsecureSkipVerify: false,
	}, nil
}

// Connect establishes a connection to the RabbitMQ broker.
func (p *RabbitMQPublisher) Connect(ctx context.Context) error {
	uri := p.cfg.URI
	var err error
	var conn *amqp091.Connection

	if p.cfg.TLSEnabled && p.cfg.TLSCARef != "" {
		caCert, err := p.secrets.Resolve(ctx, p.cfg.TLSCARef)
		if err != nil {
			return fmt.Errorf("rabbitmq[%s]: failed to resolve CA certificate: %w", p.cfg.Name, err)
		}

		tlsCfg, err := buildTLSConfig(caCert)
		if err != nil {
			return fmt.Errorf("rabbitmq[%s]: failed to build TLS config: %w", p.cfg.Name, err)
		}

		conn, err = amqp091.DialTLS(uri, tlsCfg)
	} else {
		conn, err = amqp091.Dial(uri)
	}

	if err != nil {
		return fmt.Errorf("rabbitmq[%s]: failed to connect: %w", p.cfg.Name, err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("rabbitmq[%s]: failed to open channel: %w", p.cfg.Name, err)
	}

	p.conn = conn
	p.ch = ch
	return nil
}

// amqp091Table converts a map[string]string to amqp091.Table.
func amqp091Table(m map[string]string) amqp091.Table {
	if m == nil {
		return nil
	}
	table := make(amqp091.Table)
	for k, v := range m {
		table[k] = v
	}
	return table
}

// Publish sends a single message to a RabbitMQ queue or exchange.
func (p *RabbitMQPublisher) Publish(ctx context.Context, req PublishRequest) error {
	if p.ch == nil {
		return fmt.Errorf("rabbitmq[%s]: not connected", p.cfg.Name)
	}

	publishing := amqp091.Publishing{
		ContentType: "application/octet-stream",
		Body:        req.Payload,
		Headers:     amqp091Table(req.Headers),
	}

	err := p.ch.PublishWithContext(ctx, p.cfg.Exchange, req.Topic, false, false, publishing)
	if err != nil {
		return fmt.Errorf("rabbitmq[%s]: %w", p.cfg.Name, err)
	}

	return nil
}

// PublishBatch sends multiple messages to RabbitMQ.
func (p *RabbitMQPublisher) PublishBatch(ctx context.Context, reqs []PublishRequest) error {
	for _, req := range reqs {
		if err := p.Publish(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

// Ping verifies connectivity to the RabbitMQ broker.
func (p *RabbitMQPublisher) Ping(ctx context.Context) error {
	if p.conn == nil || p.conn.IsClosed() {
		return fmt.Errorf("rabbitmq[%s]: not connected", p.cfg.Name)
	}

	return nil
}

// Close closes the RabbitMQ connection and releases resources.
func (p *RabbitMQPublisher) Close() error {
	var err error
	if p.ch != nil {
		if closeErr := p.ch.Close(); closeErr != nil {
			err = closeErr
		}
		p.ch = nil
	}

	if p.conn != nil {
		if closeErr := p.conn.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		p.conn = nil
	}

	return err
}
