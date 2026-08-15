package document

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresProvider implements DocumentProvider for PostgreSQL and compatible databases
// (CockroachDB, Aurora, Neon, YugabyteDB via PostgreSQL wire protocol).
//
// Documents are stored in JSONB tables with schema:
//
//	CREATE TABLE IF NOT EXISTS <collection> (
//	    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
//	    doc JSONB NOT NULL,
//	    created_at TIMESTAMPTZ DEFAULT now(),
//	    updated_at TIMESTAMPTZ DEFAULT now()
//	);
//
// The provider does NOT create tables; users are responsible for migrations.
type PostgresProvider struct {
	mu       sync.Mutex
	pool     *pgxpool.Pool
	initErr  error
	cfg      config.DocumentConnectorConfig
	secrets  SecretResolver
}


// ensureConnected ensures the connection pool is initialized and healthy.
// This implements lazy initialization with double-checked locking to prevent race conditions:
// the factory creates a bare struct, and actual connection is established on first use.
// Multiple concurrent calls are serialized via mutex; the first one initializes the pool,
// subsequent callers reuse the result or cached error.
func (p *PostgresProvider) ensureConnected(ctx context.Context) error {
	// Fast path: check if pool is already initialized (no lock)
	if p.pool != nil {
		return p.initErr
	}

	// Slow path: acquire mutex for initialization
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check: another goroutine may have initialized while we waited for the lock
	if p.pool != nil {
		return p.initErr
	}

	// Parse the connection URI into a pgxpool config
	poolConfig, err := pgxpool.ParseConfig(p.cfg.URI)
	if err != nil {
		p.initErr = fmt.Errorf("postgres[%s]: parse URI failed: %w", p.cfg.Name, err)
		return p.initErr
	}

	// Configure connection pool size if specified
	if p.cfg.PoolSize > 0 {
		poolConfig.MaxConns = int32(p.cfg.PoolSize)
	} else {
		poolConfig.MaxConns = 25 // Default pool size
	}

	// Configure idle connections
	poolConfig.MinConns = 1

	// Configure connection max lifetime
	poolConfig.MaxConnLifetime = 5 * time.Minute

	// Configure context timeout
	if p.cfg.TimeoutMs > 0 {
		poolConfig.HealthCheckPeriod = time.Duration(p.cfg.TimeoutMs) * time.Millisecond / 10
	} else {
		poolConfig.HealthCheckPeriod = 1 * time.Second
	}

	// Configure TLS if enabled
	if p.cfg.TLSEnabled && p.cfg.TLSCARef != "" {
		// Resolve CA certificate from secrets
		caBytes, err := p.secrets.Resolve(ctx, p.cfg.TLSCARef)
		if err != nil {
			p.initErr = fmt.Errorf("postgres[%s]: failed to resolve TLS CA: %w", p.cfg.Name, err)
			return p.initErr
		}
		defer func() {
			// Zero the secret bytes
			for i := range caBytes {
				caBytes[i] = 0
			}
		}()

		tlsConfig, err := createTLSConfig(caBytes)
		if err != nil {
			p.initErr = fmt.Errorf("postgres[%s]: TLS config failed: %w", p.cfg.Name, err)
			return p.initErr
		}
		poolConfig.ConnConfig.TLSConfig = tlsConfig
	}

	// Create the connection pool
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		p.initErr = fmt.Errorf("postgres[%s]: pool creation failed: %w", p.cfg.Name, err)
		return p.initErr
	}

	// Test connectivity
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		p.initErr = fmt.Errorf("postgres[%s]: ping failed: %w", p.cfg.Name, err)
		return p.initErr
	}

	p.pool = pool
	return nil
}

// createTLSConfig creates a TLS configuration from a CA certificate.
func createTLSConfig(caBytes []byte) (*tls.Config, error) {
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	return &tls.Config{
		RootCAs:            caCertPool,
		InsecureSkipVerify: false,
	}, nil
}

// validateCollectionName ensures the collection name only contains alphanumeric and underscore
func (p *PostgresProvider) validateCollectionName(name string) error {
	if name == "" {
		return fmt.Errorf("postgres[%s]: collection name is empty", p.cfg.Name)
	}
	if !collectionNameRegex.MatchString(name) {
		return fmt.Errorf("postgres[%s]: invalid collection name %q (only [a-zA-Z0-9_] allowed)", p.cfg.Name, name)
	}
	return nil
}

// Get retrieves a single document by ID or filter.
// Returns nil, nil if not found.
func (p *PostgresProvider) Get(ctx context.Context, req GetRequest) ([]byte, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return nil, err
	}

	var id string
	var doc []byte

	if req.ID != "" {
		// Query by ID
		query := fmt.Sprintf(`SELECT id, doc FROM %s WHERE id = $1 LIMIT 1`, req.Collection)
		err := p.pool.QueryRow(ctx, query, req.ID).Scan(&id, &doc)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil, nil
			}
			return nil, fmt.Errorf("postgres[%s]: get by id failed: %w", p.cfg.Name, err)
		}
	} else if len(req.Filter) > 0 {
		// Query by filter (JSONB contains)
		query := fmt.Sprintf(`SELECT id, doc FROM %s WHERE doc @> $1::jsonb LIMIT 1`, req.Collection)
		err := p.pool.QueryRow(ctx, query, string(req.Filter)).Scan(&id, &doc)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil, nil
			}
			return nil, fmt.Errorf("postgres[%s]: get by filter failed: %w", p.cfg.Name, err)
		}
	} else {
		return nil, fmt.Errorf("postgres[%s]: either ID or Filter must be provided", p.cfg.Name)
	}

	// Merge id and doc into a single JSON object
	result := map[string]interface{}{
		"id": id,
	}

	// Parse the doc JSONB and merge
	var docObj interface{}
	if err := json.Unmarshal(doc, &docObj); err != nil {
		return nil, fmt.Errorf("postgres[%s]: failed to unmarshal document: %w", p.cfg.Name, err)
	}

	// If doc is an object, merge fields; otherwise include as "doc"
	if docMap, ok := docObj.(map[string]interface{}); ok {
		for k, v := range docMap {
			result[k] = v
		}
	} else {
		result["doc"] = docObj
	}

	return json.Marshal(result)
}

// Put creates a new document or updates an existing one.
func (p *PostgresProvider) Put(ctx context.Context, req PutRequest) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return err
	}

	if req.Upsert {
		// Use INSERT...ON CONFLICT for upsert
		id := req.ID

		var query string
		var args []interface{}

		if id == "" {
			// Use gen_random_uuid() for new documents
			query = fmt.Sprintf(`
				INSERT INTO %s (id, doc, updated_at) 
				VALUES (gen_random_uuid()::text, $1::jsonb, now())
				ON CONFLICT(id) DO UPDATE 
				SET doc = EXCLUDED.doc, updated_at = now()
			`, req.Collection)
			args = []interface{}{string(req.Document)}
		} else {
			query = fmt.Sprintf(`
				INSERT INTO %s (id, doc, updated_at) 
				VALUES ($1, $2::jsonb, now())
				ON CONFLICT(id) DO UPDATE 
				SET doc = EXCLUDED.doc, updated_at = now()
			`, req.Collection)
			args = []interface{}{id, string(req.Document)}
		}

		_, err := p.pool.Exec(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("postgres[%s]: upsert failed: %w", p.cfg.Name, err)
		}
	} else {
		// Simple INSERT for new document
		if req.ID != "" {
			// INSERT with specified ID
			query := fmt.Sprintf(`INSERT INTO %s (id, doc) VALUES ($1, $2::jsonb)`, req.Collection)
			_, err := p.pool.Exec(ctx, query, req.ID, string(req.Document))
			if err != nil {
				return fmt.Errorf("postgres[%s]: insert with id failed: %w", p.cfg.Name, err)
			}
		} else {
			// INSERT without ID (database generates one)
			query := fmt.Sprintf(`INSERT INTO %s (doc) VALUES ($1::jsonb)`, req.Collection)
			_, err := p.pool.Exec(ctx, query, string(req.Document))
			if err != nil {
				return fmt.Errorf("postgres[%s]: insert failed: %w", p.cfg.Name, err)
			}
		}
	}

	return nil
}

// Delete removes all documents matching the filter.
func (p *PostgresProvider) Delete(ctx context.Context, req DeleteRequest) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return err
	}

	if len(req.Filter) == 0 {
		return fmt.Errorf("postgres[%s]: filter is required for delete", p.cfg.Name)
	}

	query := fmt.Sprintf(`DELETE FROM %s WHERE doc @> $1::jsonb`, req.Collection)
	_, err := p.pool.Exec(ctx, query, string(req.Filter))
	if err != nil {
		return fmt.Errorf("postgres[%s]: delete failed: %w", p.cfg.Name, err)
	}

	return nil
}

// GetMany retrieves multiple documents by their IDs in a single query.
// Returns an empty map (not nil) if no results are found.
func (p *PostgresProvider) GetMany(ctx context.Context, req GetManyRequest) (map[string][]byte, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return nil, err
	}

	if len(req.IDs) == 0 {
		return make(map[string][]byte), nil
	}

	query := fmt.Sprintf(`SELECT id, doc FROM %s WHERE id = ANY($1)`, req.Collection)
	rows, err := p.pool.Query(ctx, query, req.IDs)
	if err != nil {
		return nil, fmt.Errorf("postgres[%s]: get many failed: %w", p.cfg.Name, err)
	}
	defer rows.Close()

	result := make(map[string][]byte)
	for rows.Next() {
		var id string
		var doc []byte
		if err := rows.Scan(&id, &doc); err != nil {
			return nil, fmt.Errorf("postgres[%s]: scan failed: %w", p.cfg.Name, err)
		}
		result[id] = doc
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres[%s]: rows error: %w", p.cfg.Name, err)
	}

	return result, nil
}

// PutMany creates or updates multiple documents in a single batch operation.
func (p *PostgresProvider) PutMany(ctx context.Context, req PutManyRequest) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return err
	}

	if len(req.Docs) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for id, docBytes := range req.Docs {
		query := fmt.Sprintf(`
			INSERT INTO %s (id, doc, updated_at)
			VALUES ($1, $2::jsonb, now())
			ON CONFLICT(id) DO UPDATE
			SET doc = EXCLUDED.doc, updated_at = now()
		`, req.Collection)
		batch.Queue(query, id, string(docBytes))
	}

	br := p.pool.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()

	for range req.Docs {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("postgres[%s]: put many failed: %w", p.cfg.Name, err)
		}
	}

	return nil
}

// DeleteMany removes multiple documents by their IDs in a single query.
func (p *PostgresProvider) DeleteMany(ctx context.Context, req DeleteManyRequest) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return err
	}

	if len(req.IDs) == 0 {
		return nil
	}

	query := fmt.Sprintf(`DELETE FROM %s WHERE id = ANY($1)`, req.Collection)
	_, err := p.pool.Exec(ctx, query, req.IDs)
	if err != nil {
		return fmt.Errorf("postgres[%s]: delete many failed: %w", p.cfg.Name, err)
	}

	return nil
}

// Query retrieves multiple documents matching the filter.
func (p *PostgresProvider) Query(ctx context.Context, req QueryRequest) ([]byte, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return nil, err
	}

	// Build the query
	query := fmt.Sprintf(`SELECT id, doc FROM %s`, req.Collection)
	args := []interface{}{}
	argIndex := 1

	// Add WHERE clause if filter provided
	if len(req.Filter) > 0 {
		query += fmt.Sprintf(` WHERE doc @> $%d::jsonb`, argIndex)
		args = append(args, string(req.Filter))
		argIndex++
	}

	// Add ORDER BY if sort provided
	if req.Sort != nil && len(req.Sort) > 0 {
		sortClause, err := buildSortClause(req.Sort)
		if err != nil {
			return nil, fmt.Errorf("postgres[%s]: invalid sort: %w", p.cfg.Name, err)
		}
		query += ` ` + sortClause
	}

	// Add LIMIT if specified
	if req.Limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, req.Limit)
	}

	// Add OFFSET if skip > 0
	if req.Skip > 0 {
		query += fmt.Sprintf(` OFFSET %d`, req.Skip)
	}

	// Execute query
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres[%s]: query failed: %w", p.cfg.Name, err)
	}
	defer rows.Close()

	// Collect results
	var results []interface{}
	for rows.Next() {
		var id string
		var doc []byte
		if err := rows.Scan(&id, &doc); err != nil {
			return nil, fmt.Errorf("postgres[%s]: scan failed: %w", p.cfg.Name, err)
		}

		// Merge id and doc
		result := map[string]interface{}{
			"id": id,
		}

		var docObj interface{}
		if err := json.Unmarshal(doc, &docObj); err != nil {
			return nil, fmt.Errorf("postgres[%s]: unmarshal failed: %w", p.cfg.Name, err)
		}

		if docMap, ok := docObj.(map[string]interface{}); ok {
			for k, v := range docMap {
				result[k] = v
			}
		} else {
			result["doc"] = docObj
		}

		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres[%s]: rows error: %w", p.cfg.Name, err)
	}

	// Return as JSON array
	return json.Marshal(results)
}

// Count returns the number of documents matching the filter.
func (p *PostgresProvider) Count(ctx context.Context, req QueryRequest) (int64, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return 0, err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return 0, err
	}

	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s`, req.Collection)
	args := []interface{}{}

	// Add WHERE clause if filter provided
	if req.Filter != nil && len(req.Filter) > 0 {
		query += ` WHERE doc @> $1::jsonb`
		args = append(args, string(req.Filter))
	}

	var count int64
	err := p.pool.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("postgres[%s]: count failed: %w", p.cfg.Name, err)
	}

	return count, nil
}

// Execute runs a raw SQL query. The sql field is passed directly to the database — callers must ensure it is trusted/internal.
// Command format: {"sql":"SELECT ...", "args":[...]}
func (p *PostgresProvider) Execute(ctx context.Context, req ExecuteRequest) ([]byte, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}

	if req.Command == nil || len(req.Command) == 0 {
		return nil, fmt.Errorf("postgres[%s]: command is required", p.cfg.Name)
	}

	// Parse the command JSON
	var cmd map[string]interface{}
	if err := json.Unmarshal(req.Command, &cmd); err != nil {
		return nil, fmt.Errorf("postgres[%s]: invalid command JSON: %w", p.cfg.Name, err)
	}

	sqlStr, ok := cmd["sql"].(string)
	if !ok {
		return nil, fmt.Errorf("postgres[%s]: sql field is required and must be a string", p.cfg.Name)
	}

	// Extract args if provided
	var args []interface{}
	if argsRaw, ok := cmd["args"].([]interface{}); ok {
		args = argsRaw
	}

	// Execute the query
	rows, err := p.pool.Query(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres[%s]: execute failed: %w", p.cfg.Name, err)
	}
	defer rows.Close()

	// Collect column names
	columnDescriptions := rows.FieldDescriptions()
	columnNames := make([]string, len(columnDescriptions))
	for i, col := range columnDescriptions {
		columnNames[i] = col.Name
	}

	// Collect rows as JSON
	var results []interface{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("postgres[%s]: read row failed: %w", p.cfg.Name, err)
		}

		row := make(map[string]interface{})
		for i, colName := range columnNames {
			row[colName] = values[i]
		}
		results = append(results, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres[%s]: rows error: %w", p.cfg.Name, err)
	}

	return json.Marshal(results)
}

// Ping verifies the connection is alive.
func (p *PostgresProvider) Ping(ctx context.Context) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	if err := p.pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres[%s]: ping failed: %w", p.cfg.Name, err)
	}
	return nil
}

// Close releases all resources.
func (p *PostgresProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pool != nil {
		p.pool.Close()
		p.pool = nil
	}
	return nil
}

// buildSortClause builds a SQL ORDER BY clause from JSON sort specification.
// Expected format: [{"field":"col1","direction":"asc"},{"field":"col2","direction":"desc"}]
func buildSortClause(sortJSON []byte) (string, error) {
	var sorts []map[string]interface{}
	if err := json.Unmarshal(sortJSON, &sorts); err != nil {
		return "", err
	}

	if len(sorts) == 0 {
		return "", nil
	}

	var clauses []string
	for _, sort := range sorts {
		field, ok := sort["field"].(string)
		if !ok {
			return "", fmt.Errorf("sort field is required and must be a string")
		}

		direction := "ASC"
		if dir, ok := sort["direction"].(string); ok {
			dirUpper := strings.ToUpper(dir)
			if dirUpper == "ASC" || dirUpper == "DESC" {
				direction = dirUpper
			}
		}

		// Sanitize field name - only allow alphanumeric and underscore
		if !sortFieldRegex.MatchString(field) {
			return "", fmt.Errorf("invalid sort field name: %s", field)
		}

		clauses = append(clauses, fmt.Sprintf(`%s %s`, field, direction))
	}

	if len(clauses) == 0 {
		return "", nil
	}

	return `ORDER BY ` + strings.Join(clauses, ", "), nil
}
