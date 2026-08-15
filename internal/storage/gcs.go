package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

type gcsProvider struct {
	client *gcs.Client
	bucket string
}

func newGCSProvider(cfg StorageProviderConfig) (*gcsProvider, error) {
	bucket := resolveRef(cfg.BucketRef)

	opts := []option.ClientOption{}
	if cfg.CredentialRef != "" {
		// CredentialRef resolves to a file path (e.g. env:GOOGLE_CREDS_FILE → /path/to/key.json)
		credFile := resolveRef(cfg.CredentialRef)
		if credFile != "" {
			opts = append(opts, option.WithCredentialsFile(credFile))
		}
	}

	client, err := gcs.NewClient(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("storage %q: gcs client: %w", cfg.Name, err)
	}

	return &gcsProvider{client: client, bucket: bucket}, nil
}

func (p *gcsProvider) Get(ctx context.Context, key string) ([]byte, error) {
	obj := p.client.Bucket(p.bucket).Object(key)
	r, err := obj.NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcs get %q: %w", key, err)
	}
	defer r.Close()
	return io.ReadAll(r)
}

func (p *gcsProvider) Put(ctx context.Context, key string, content []byte, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	obj := p.client.Bucket(p.bucket).Object(key)
	w := obj.NewWriter(ctx)
	w.ContentType = contentType
	if _, err := io.Copy(w, bytes.NewReader(content)); err != nil {
		_ = w.Close()
		return fmt.Errorf("gcs put %q: %w", key, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("gcs put %q: close: %w", key, err)
	}
	return nil
}

func (p *gcsProvider) Delete(ctx context.Context, key string) error {
	err := p.client.Bucket(p.bucket).Object(key).Delete(ctx)
	if err != nil {
		return fmt.Errorf("gcs delete %q: %w", key, err)
	}
	return nil
}
