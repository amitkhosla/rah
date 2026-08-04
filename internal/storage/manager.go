package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// StorageManager holds named S3 clients.
type StorageManager struct {
	clients map[string]*s3Client
}

type s3Client struct {
	client *s3.Client
	bucket string
}

// New creates a StorageManager from configs.
func New(configs []StorageProviderConfig) (*StorageManager, error) {
	m := &StorageManager{clients: make(map[string]*s3Client, len(configs))}
	for _, cfg := range configs {
		bucket := resolveRef(cfg.BucketRef)
		accessKey := resolveRef(cfg.AccessKeyRef)
		secretKey := resolveRef(cfg.SecretKeyRef)

		opts := []func(*awsconfig.LoadOptions) error{
			awsconfig.WithRegion(cfg.Region),
		}
		if accessKey != "" && secretKey != "" {
			opts = append(opts, awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
			))
		}

		awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
		if err != nil {
			return nil, fmt.Errorf("storage %q: aws config: %w", cfg.Name, err)
		}

		s3Opts := []func(*s3.Options){}
		if cfg.EndpointURL != "" {
			s3Opts = append(s3Opts, func(o *s3.Options) {
				o.BaseEndpoint = aws.String(cfg.EndpointURL)
				o.UsePathStyle = true
			})
		}

		client := s3.NewFromConfig(awsCfg, s3Opts...)
		m.clients[cfg.Name] = &s3Client{client: client, bucket: bucket}
		gatewaylog.Default.Info("[Storage] registered", gatewaylog.F("name", cfg.Name))
	}
	return m, nil
}

// Get retrieves an object by key. Returns the content bytes.
func (m *StorageManager) Get(ctx context.Context, providerName, key string) ([]byte, error) {
	c, ok := m.clients[providerName]
	if !ok {
		return nil, fmt.Errorf("storage provider %q not found", providerName)
	}
	out, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("storage get %q: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	return io.ReadAll(out.Body)
}

// Put stores content at the given key. contentType defaults to application/octet-stream.
func (m *StorageManager) Put(ctx context.Context, providerName, key string, content []byte, contentType string) error {
	c, ok := m.clients[providerName]
	if !ok {
		return fmt.Errorf("storage provider %q not found", providerName)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("storage put %q: %w", key, err)
	}
	return nil
}

// Delete removes an object by key.
func (m *StorageManager) Delete(ctx context.Context, providerName, key string) error {
	c, ok := m.clients[providerName]
	if !ok {
		return fmt.Errorf("storage provider %q not found", providerName)
	}
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("storage delete %q: %w", key, err)
	}
	return nil
}

func resolveRef(ref string) string {
	if strings.HasPrefix(ref, "env:") {
		return os.Getenv(strings.TrimPrefix(ref, "env:"))
	}
	return ref
}
