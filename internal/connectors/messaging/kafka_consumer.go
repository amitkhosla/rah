package messaging

import (
	"context"
	"fmt"
	"log"

	"github.com/IBM/sarama"
	"github.com/amitkhosla/rah/internal/config"
)

// KafkaConsumer subscribes to a Kafka topic using a consumer group.
type KafkaConsumer struct {
	cfg     config.PublisherConfig
	topic   string
	groupID string
	secrets SecretResolver
	group   sarama.ConsumerGroup
}

// NewKafkaConsumer creates a KafkaConsumer. Connection is deferred to Start().
func NewKafkaConsumer(cfg config.PublisherConfig, topic, groupID string, secrets SecretResolver) *KafkaConsumer {
	return &KafkaConsumer{cfg: cfg, topic: topic, groupID: groupID, secrets: secrets}
}

func (c *KafkaConsumer) Start(ctx context.Context, handler MessageHandler) error {
	saramaCfg := sarama.NewConfig()
	saramaCfg.Version = sarama.V2_1_0_0
	saramaCfg.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	saramaCfg.Consumer.Offsets.Initial = sarama.OffsetNewest

	if c.cfg.CredentialRef != "" {
		credBytes, err := c.secrets.Resolve(ctx, c.cfg.CredentialRef)
		if err != nil {
			return fmt.Errorf("kafka_consumer[%s]: resolve credentials: %w", c.cfg.Name, err)
		}
		saramaCfg.Net.SASL.Enable = true
		saramaCfg.Net.SASL.Mechanism = sarama.SASLTypePlaintext
		saramaCfg.Net.SASL.User = string(credBytes) // "user:pass" or just token
		saramaCfg.Net.SASL.Password = ""
	}

	group, err := sarama.NewConsumerGroup(c.cfg.Brokers, c.groupID, saramaCfg)
	if err != nil {
		return fmt.Errorf("kafka_consumer[%s]: create group: %w", c.cfg.Name, err)
	}
	c.group = group
	defer func() { _ = group.Close() }()

	h := &kafkaGroupHandler{handler: handler, name: c.cfg.Name}
	for {
		if err := group.Consume(ctx, []string{c.topic}, h); err != nil {
			if ctx.Err() != nil {
				return nil // normal shutdown
			}
			log.Printf("[KafkaConsumer:%s] consume error: %v", c.cfg.Name, err)
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

func (c *KafkaConsumer) Stop() error {
	if c.group != nil {
		return c.group.Close()
	}
	return nil
}

// kafkaGroupHandler implements sarama.ConsumerGroupHandler.
type kafkaGroupHandler struct {
	handler MessageHandler
	name    string
}

func (h *kafkaGroupHandler) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (h *kafkaGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

func (h *kafkaGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		headers := make(map[string]string, len(msg.Headers))
		for _, hdr := range msg.Headers {
			if hdr != nil {
				headers[string(hdr.Key)] = string(hdr.Value)
			}
		}
		cm := ConsumedMessage{
			Topic:   msg.Topic,
			Key:     msg.Key,
			Payload: msg.Value,
			Headers: headers,
		}
		if err := h.handler(session.Context(), cm); err != nil {
			log.Printf("[KafkaConsumer:%s] handler error: %v", h.name, err)
		}
		session.MarkMessage(msg, "")
	}
	return nil
}
