package messaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// SQSPublisher publishes messages to AWS SQS queues.
type SQSPublisher struct {
	cfg     config.PublisherConfig
	secrets SecretResolver
	client  *sqs.Client
}

// Connect establishes a connection to SQS.
func (p *SQSPublisher) Connect(ctx context.Context) error {

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(p.cfg.Region))
	if err != nil {
		return fmt.Errorf("sqs[%s]: failed to load AWS config: %w", p.cfg.Name, err)
	}

	// If credential reference is provided, use custom credentials
	if p.cfg.CredentialRef != "" {
		credBytes, err := p.secrets.Resolve(ctx, p.cfg.CredentialRef)
		if err != nil {
			return fmt.Errorf("sqs[%s]: failed to resolve credentials: %w", p.cfg.Name, err)
		}

		// Try to parse as JSON: {"access_key":"...", "secret_key":"..."}
		var creds struct {
			AccessKey string `json:"access_key"`
			SecretKey string `json:"secret_key"`
		}
		if err := json.Unmarshal(credBytes, &creds); err != nil {
			return fmt.Errorf("sqs[%s]: failed to parse credentials JSON: %w", p.cfg.Name, err)
		}

		if creds.AccessKey == "" || creds.SecretKey == "" {
			return fmt.Errorf("sqs[%s]: missing access_key or secret_key in credentials", p.cfg.Name)
		}

		credProvider := credentials.NewStaticCredentialsProvider(creds.AccessKey, creds.SecretKey, "")
		cfg.Credentials = aws.NewCredentialsCache(credProvider)
	}

	p.client = sqs.NewFromConfig(cfg)
	return nil
}

// sqsAttributes converts a map[string]string to SQS MessageAttributes.
func sqsAttributes(m map[string]string) map[string]types.MessageAttributeValue {
	if m == nil {
		return nil
	}
	attrs := make(map[string]types.MessageAttributeValue)
	for k, v := range m {
		attrs[k] = types.MessageAttributeValue{
			StringValue: aws.String(v),
			DataType:    aws.String("String"),
		}
	}
	return attrs
}

// Publish sends a single message to an SQS queue.
func (p *SQSPublisher) Publish(ctx context.Context, req PublishRequest) error {
	if p.client == nil {
		return fmt.Errorf("sqs[%s]: not connected", p.cfg.Name)
	}

	input := &sqs.SendMessageInput{
		QueueUrl:    aws.String(req.Topic), // req.Topic = queue URL
		MessageBody: aws.String(string(req.Payload)),
	}

	// Add attributes
	if len(req.Headers) > 0 {
		input.MessageAttributes = sqsAttributes(req.Headers)
	}

	// If key is provided, set for FIFO queues
	if len(req.Key) > 0 {
		// For FIFO queues, set MessageGroupId and MessageDeduplicationId
		keyStr := string(req.Key)
		input.MessageGroupId = aws.String(keyStr)

		// Generate deduplication ID from payload hash
		hash := sha256.Sum256(req.Payload)
		dedup := hex.EncodeToString(hash[:])
		input.MessageDeduplicationId = aws.String(dedup)
	}

	_, err := p.client.SendMessage(ctx, input)
	if err != nil {
		return fmt.Errorf("sqs[%s]: %w", p.cfg.Name, err)
	}

	return nil
}

// PublishBatch sends multiple messages to SQS queues using batch API.
func (p *SQSPublisher) PublishBatch(ctx context.Context, reqs []PublishRequest) error {
	if p.client == nil {
		return fmt.Errorf("sqs[%s]: not connected", p.cfg.Name)
	}

	// Group requests by queue URL (topic)
	reqsByQueue := make(map[string][]PublishRequest)
	for _, req := range reqs {
		reqsByQueue[req.Topic] = append(reqsByQueue[req.Topic], req)
	}

	// Process each queue
	for queueURL, queueReqs := range reqsByQueue {
		// SQS batch API supports up to 10 messages per batch
		for i := 0; i < len(queueReqs); i += 10 {
			end := i + 10
			if end > len(queueReqs) {
				end = len(queueReqs)
			}

			batch := queueReqs[i:end]
			entries := make([]types.SendMessageBatchRequestEntry, len(batch))

			for j, req := range batch {
				entry := types.SendMessageBatchRequestEntry{
					Id:          aws.String(fmt.Sprintf("%d", j)),
					MessageBody: aws.String(string(req.Payload)),
				}

				if len(req.Headers) > 0 {
					entry.MessageAttributes = sqsAttributes(req.Headers)
				}

				if len(req.Key) > 0 {
					keyStr := string(req.Key)
					entry.MessageGroupId = aws.String(keyStr)
					hash := sha256.Sum256(req.Payload)
					dedup := hex.EncodeToString(hash[:])
					entry.MessageDeduplicationId = aws.String(dedup)
				}

				entries[j] = entry
			}

			input := &sqs.SendMessageBatchInput{
				QueueUrl: aws.String(queueURL),
				Entries:  entries,
			}

			result, err := p.client.SendMessageBatch(ctx, input)
			if err != nil {
				return fmt.Errorf("sqs[%s]: batch send failed: %w", p.cfg.Name, err)
			}

			// Check for failed entries
			if len(result.Failed) > 0 {
				return fmt.Errorf("sqs[%s]: %d messages failed to send", p.cfg.Name, len(result.Failed))
			}
		}
	}

	return nil
}

// Ping verifies connectivity to the AWS SQS service.
func (p *SQSPublisher) Ping(ctx context.Context) error {
	if p.client == nil {
		return fmt.Errorf("sqs[%s]: not connected", p.cfg.Name)
	}

	return nil
}

// Close closes the SQS client and releases resources.
func (p *SQSPublisher) Close() error {
	if p.client != nil {
		p.client = nil
	}

	return nil
}
