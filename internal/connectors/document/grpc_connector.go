package document

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	connectorv1 "github.com/amitkhosla/rah/api/connectors/v1"
	"github.com/amitkhosla/rah/internal/config"
)

// GRPCProvider implements DocumentProvider backed by an external gRPC service.
type GRPCProvider struct {
	cfg     config.DocumentConnectorConfig
	conn    *grpc.ClientConn
	client  connectorv1.RahConnectorClient
	secrets SecretResolver
}

// NewGRPCProvider creates a new gRPC-based DocumentProvider.
// cfg.GRPCEndpoint should be a gRPC target address (e.g., "localhost:50051").
// If cfg.TLSEnabled is false, uses insecure credentials.
// If cfg.TLSEnabled is true, uses system CA or cfg.TLSCARef if provided.
// TLSCARef is resolved using the secrets resolver.
func NewGRPCProvider(ctx context.Context, cfg config.DocumentConnectorConfig, secrets SecretResolver, opts ...grpc.DialOption) (*GRPCProvider, error) {
	if cfg.GRPCEndpoint == "" {
		return nil, fmt.Errorf("grpc-connector[%s]: GRPCEndpoint is required", cfg.Name)
	}

	provider := &GRPCProvider{
		cfg:     cfg,
		secrets: secrets,
	}

	// Build dial options.
	dialOpts := []grpc.DialOption{}

	// Add transport credentials based on TLS configuration.
	if cfg.TLSEnabled {
		if cfg.TLSCARef != "" {
			// Resolve the CA certificate from the secret resolver.
			caBytes, err := secrets.Resolve(ctx, cfg.TLSCARef)
			if err != nil {
				return nil, fmt.Errorf("grpc-connector[%s]: resolve TLS CA: %w", cfg.Name, err)
			}

			// Parse the CA certificate.
			caCertPool := x509.NewCertPool()
			if !caCertPool.AppendCertsFromPEM(caBytes) {
				return nil, fmt.Errorf("grpc-connector[%s]: failed to parse CA certificate", cfg.Name)
			}

			// Build tls.Config with custom CA pool.
			tlsCfg := &tls.Config{
				RootCAs:    caCertPool,
				MinVersion: tls.VersionTLS12,
			}
			dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
		} else {
			// Use system CA bundle.
			tlsCfg := &tls.Config{
				MinVersion: tls.VersionTLS12,
			}
			dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
		}
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Apply any additional user-provided options.
	dialOpts = append(dialOpts, opts...)

	// Establish connection.
	conn, err := grpc.NewClient(cfg.GRPCEndpoint, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("grpc-connector[%s]: failed to dial %q: %w", cfg.Name, cfg.GRPCEndpoint, err)
	}

	provider.conn = conn
	provider.client = connectorv1.NewRahConnectorClient(conn)

	return provider, nil
}

// Get retrieves a document from the gRPC service.
func (p *GRPCProvider) Get(ctx context.Context, req GetRequest) ([]byte, error) {
	if p.client == nil {
		return nil, fmt.Errorf("grpc-connector[%s]: client not initialized", p.cfg.Name)
	}

	callCtx := ctx
	if p.cfg.TimeoutMs > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, time.Duration(p.cfg.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	grpcReq := &connectorv1.GetRequest{
		Collection: req.Collection,
		Id:         req.ID,
		Filter:     req.Filter,
	}

	resp, err := p.client.Get(callCtx, grpcReq)
	if err != nil {
		return nil, fmt.Errorf("grpc-connector[%s]: Get failed: %w", p.cfg.Name, err)
	}

	// Empty document means not found; return nil, nil (not an error).
	if len(resp.Document) == 0 {
		return nil, nil
	}

	return resp.Document, nil
}

// Put inserts or replaces a document via the gRPC service.
func (p *GRPCProvider) Put(ctx context.Context, req PutRequest) error {
	if p.client == nil {
		return fmt.Errorf("grpc-connector[%s]: client not initialized", p.cfg.Name)
	}

	callCtx := ctx
	if p.cfg.TimeoutMs > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, time.Duration(p.cfg.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	grpcReq := &connectorv1.PutRequest{
		Collection: req.Collection,
		Document:   req.Document,
		Id:         req.ID,
		Upsert:     req.Upsert,
	}

	_, err := p.client.Put(callCtx, grpcReq)
	if err != nil {
		return fmt.Errorf("grpc-connector[%s]: Put failed: %w", p.cfg.Name, err)
	}

	return nil
}

// Delete removes documents matching a filter via the gRPC service.
func (p *GRPCProvider) Delete(ctx context.Context, req DeleteRequest) error {
	if p.client == nil {
		return fmt.Errorf("grpc-connector[%s]: client not initialized", p.cfg.Name)
	}

	callCtx := ctx
	if p.cfg.TimeoutMs > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, time.Duration(p.cfg.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	grpcReq := &connectorv1.DeleteRequest{
		Collection: req.Collection,
		Filter:     req.Filter,
	}

	_, err := p.client.Delete(callCtx, grpcReq)
	if err != nil {
		return fmt.Errorf("grpc-connector[%s]: Delete failed: %w", p.cfg.Name, err)
	}

	return nil
}

// Query retrieves multiple documents matching a filter via the gRPC service.
func (p *GRPCProvider) Query(ctx context.Context, req QueryRequest) ([]byte, error) {
	if p.client == nil {
		return nil, fmt.Errorf("grpc-connector[%s]: client not initialized", p.cfg.Name)
	}

	callCtx := ctx
	if p.cfg.TimeoutMs > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, time.Duration(p.cfg.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	grpcReq := &connectorv1.QueryRequest{
		Collection: req.Collection,
		Filter:     req.Filter,
		Projection: req.Projection,
		Sort:       req.Sort,
		Limit:      int32(req.Limit),
		Skip:       int32(req.Skip),
	}

	resp, err := p.client.Query(callCtx, grpcReq)
	if err != nil {
		return nil, fmt.Errorf("grpc-connector[%s]: Query failed: %w", p.cfg.Name, err)
	}

	return resp.Documents, nil
}

// Count returns the number of documents matching a filter via the gRPC service.
func (p *GRPCProvider) Count(ctx context.Context, req QueryRequest) (int64, error) {
	if p.client == nil {
		return 0, fmt.Errorf("grpc-connector[%s]: client not initialized", p.cfg.Name)
	}

	callCtx := ctx
	if p.cfg.TimeoutMs > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, time.Duration(p.cfg.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	grpcReq := &connectorv1.CountRequest{
		Collection: req.Collection,
		Filter:     req.Filter,
	}

	resp, err := p.client.Count(callCtx, grpcReq)
	if err != nil {
		return 0, fmt.Errorf("grpc-connector[%s]: Count failed: %w", p.cfg.Name, err)
	}

	return resp.Count, nil
}

// Execute sends a provider-specific command via the gRPC service.
func (p *GRPCProvider) Execute(ctx context.Context, req ExecuteRequest) ([]byte, error) {
	if p.client == nil {
		return nil, fmt.Errorf("grpc-connector[%s]: client not initialized", p.cfg.Name)
	}

	callCtx := ctx
	if p.cfg.TimeoutMs > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, time.Duration(p.cfg.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	grpcReq := &connectorv1.ExecuteRequest{
		Command: req.Command,
	}

	resp, err := p.client.Execute(callCtx, grpcReq)
	if err != nil {
		return nil, fmt.Errorf("grpc-connector[%s]: Execute failed: %w", p.cfg.Name, err)
	}

	return resp.Result, nil
}

// Ping checks connectivity to the gRPC service.
func (p *GRPCProvider) Ping(ctx context.Context) error {
	if p.client == nil {
		return fmt.Errorf("grpc-connector[%s]: client not initialized", p.cfg.Name)
	}

	callCtx := ctx
	if p.cfg.TimeoutMs > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, time.Duration(p.cfg.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	_, err := p.client.Ping(callCtx, &connectorv1.PingRequest{})
	if err != nil {
		return fmt.Errorf("grpc-connector[%s]: Ping failed: %w", p.cfg.Name, err)
	}

	return nil
}

// GetMany retrieves multiple documents by ID from the same collection (serial fallback).
func (p *GRPCProvider) GetMany(ctx context.Context, req GetManyRequest) (map[string][]byte, error) {
	if len(req.IDs) == 0 {
		return map[string][]byte{}, nil
	}
	results := make(map[string][]byte, len(req.IDs))
	for _, id := range req.IDs {
		data, err := p.Get(ctx, GetRequest{Collection: req.Collection, ID: id})
		if err != nil {
			return nil, fmt.Errorf("id %q: %w", id, err)
		}
		if data != nil {
			results[id] = data
		}
	}
	return results, nil
}

// PutMany upserts multiple documents into the same collection (serial fallback).
func (p *GRPCProvider) PutMany(ctx context.Context, req PutManyRequest) error {
	for id, docBytes := range req.Docs {
		if err := p.Put(ctx, PutRequest{Collection: req.Collection, Document: docBytes, ID: id, Upsert: req.Upsert}); err != nil {
			return fmt.Errorf("id %q: %w", id, err)
		}
	}
	return nil
}

// DeleteMany deletes multiple documents by ID from the same collection (serial fallback).
func (p *GRPCProvider) DeleteMany(ctx context.Context, req DeleteManyRequest) error {
	for _, id := range req.IDs {
		// Build a simple ID filter; the filter is raw JSON bytes.
		filter := []byte(`{"_id":"` + id + `"}`)
		if err := p.Delete(ctx, DeleteRequest{Collection: req.Collection, Filter: filter}); err != nil {
			return fmt.Errorf("id %q: %w", id, err)
		}
	}
	return nil
}

// Close closes the gRPC connection.
func (p *GRPCProvider) Close() error {
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}
