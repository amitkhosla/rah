package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type s3Provider struct {
	client *s3.Client
	bucket string
}

func newS3Provider(cfg StorageProviderConfig) (*s3Provider, error) {
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
	return &s3Provider{client: client, bucket: bucket}, nil
}

func (p *s3Provider) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := p.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("storage get %q: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	return io.ReadAll(out.Body)
}

func (p *s3Provider) Put(ctx context.Context, key string, content []byte, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := p.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(p.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("storage put %q: %w", key, err)
	}
	return nil
}

func (p *s3Provider) Delete(ctx context.Context, key string) error {
	_, err := p.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("storage delete %q: %w", key, err)
	}
	return nil
}

func (p *s3Provider) Presign(ctx context.Context, key, method string, expirySeconds int) (string, error) {
	presignClient := s3.NewPresignClient(p.client)
	expiry := time.Duration(expirySeconds) * time.Second
	switch strings.ToUpper(method) {
	case "GET":
		req, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(p.bucket),
			Key:    aws.String(key),
		}, s3.WithPresignExpires(expiry))
		if err != nil {
			return "", fmt.Errorf("s3 presign GET %q: %w", key, err)
		}
		return req.URL, nil
	case "PUT":
		req, err := presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(p.bucket),
			Key:    aws.String(key),
		}, s3.WithPresignExpires(expiry))
		if err != nil {
			return "", fmt.Errorf("s3 presign PUT %q: %w", key, err)
		}
		return req.URL, nil
	default:
		return "", fmt.Errorf("s3 presign: unsupported method %q", method)
	}
}

// List returns all objects whose keys start with prefix.
// ContentType is inferred from the key extension (S3 ListObjectsV2 does not return it).
func (p *s3Provider) List(ctx context.Context, prefix string) ([]ListItem, error) {
	var items []ListItem
	paginator := s3.NewListObjectsV2Paginator(p.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(p.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3 list %q: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			ct := mime.TypeByExtension(filepath.Ext(key))
			if ct == "" {
				ct = "application/octet-stream"
			}
			var updatedAt time.Time
			if obj.LastModified != nil {
				updatedAt = *obj.LastModified
			}
			var size int64
			if obj.Size != nil {
				size = *obj.Size
			}
			items = append(items, ListItem{Key: key, Size: size, ContentType: ct, UpdatedAt: updatedAt})
		}
	}
	return items, nil
}

func resolveRef(ref string) string {
	if strings.HasPrefix(ref, "env:") {
		return os.Getenv(strings.TrimPrefix(ref, "env:"))
	}
	return ref
}
