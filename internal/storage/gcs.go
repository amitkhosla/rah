package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// Ensure gcsProvider implements presigner at compile time.
var _ presigner = (*gcsProvider)(nil)

type gcsProvider struct {
	client      *gcs.Client
	bucket      string
	clientEmail string // cached from credential JSON at init for presigning
	privateKey  []byte // cached from credential JSON at init for presigning
}

func newGCSProvider(cfg StorageProviderConfig) (*gcsProvider, error) {
	bucket := resolveRef(cfg.BucketRef)

	p := &gcsProvider{bucket: bucket}

	opts := []option.ClientOption{}
	if cfg.CredentialRef != "" {
		credFile := resolveRef(cfg.CredentialRef)
		if credFile != "" {
			opts = append(opts, option.WithCredentialsFile(credFile)) //nolint:staticcheck

			// Parse credentials once at init so Presign never does per-call disk I/O.
			data, err := os.ReadFile(credFile)
			if err != nil {
				return nil, fmt.Errorf("storage %q: read gcs credentials: %w", cfg.Name, err)
			}
			var creds struct {
				ClientEmail string `json:"client_email"`
				PrivateKey  string `json:"private_key"`
			}
			if err := json.Unmarshal(data, &creds); err != nil {
				return nil, fmt.Errorf("storage %q: parse gcs credentials: %w", cfg.Name, err)
			}
			p.clientEmail = creds.ClientEmail
			p.privateKey = []byte(creds.PrivateKey)
		}
	}

	client, err := gcs.NewClient(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("storage %q: gcs client: %w", cfg.Name, err)
	}
	p.client = client
	return p, nil
}

func (p *gcsProvider) Get(ctx context.Context, key string) ([]byte, error) {
	obj := p.client.Bucket(p.bucket).Object(key)
	r, err := obj.NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcs get %q: %w", key, err)
	}
	defer func() { _ = r.Close() }()
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

// List returns all objects whose keys start with prefix.
func (p *gcsProvider) List(ctx context.Context, prefix string) ([]ListItem, error) {
	it := p.client.Bucket(p.bucket).Objects(ctx, &gcs.Query{Prefix: prefix})
	var items []ListItem
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gcs list %q: %w", prefix, err)
		}
		ct := attrs.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		items = append(items, ListItem{Key: attrs.Name, Size: attrs.Size, ContentType: ct, UpdatedAt: attrs.Updated})
	}
	return items, nil
}

func (p *gcsProvider) Presign(_ context.Context, key, method string, expirySeconds int) (string, error) {
	if p.clientEmail == "" || len(p.privateKey) == 0 {
		return "", fmt.Errorf("gcs presign: credential_ref with service account JSON required for signed URLs")
	}
	opts := &gcs.SignedURLOptions{
		GoogleAccessID: p.clientEmail,
		PrivateKey:     p.privateKey,
		Method:         strings.ToUpper(method),
		Expires:        time.Now().Add(time.Duration(expirySeconds) * time.Second),
		Scheme:         gcs.SigningSchemeV4,
	}
	url, err := p.client.Bucket(p.bucket).SignedURL(key, opts)
	if err != nil {
		return "", fmt.Errorf("gcs presign %q: %w", key, err)
	}
	return url, nil
}
