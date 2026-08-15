package document

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/go-sql-driver/mysql"
)

// MySQLProvider implements DocumentProvider for MySQL/MariaDB/TiDB via the MySQL wire protocol.
// Documents are stored in tables with schema:
//
//	CREATE TABLE IF NOT EXISTS <collection> (
//	    id VARCHAR(36) PRIMARY KEY DEFAULT (UUID()),
//	    doc JSON NOT NULL,
//	    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
//	    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
//	);
//
// Requires MySQL 8.0.13+ or MariaDB 10.7+ for DEFAULT (UUID()).
// The provider does NOT create tables; users are responsible for migrations.
type MySQLProvider struct {
	db      *sql.DB
	cfg     config.DocumentConnectorConfig
	secrets SecretResolver
	mu      sync.Mutex // protects initialization of db connection
	initErr error      // stores initialization error
}

// Note: collectionNameRegex is defined in helpers.go and shared across providers

// ensureConnected initializes the database connection if not already done.
// This allows lazy connection establishment on first use.
func (p *MySQLProvider) ensureConnected(ctx context.Context) error {
	if p.db != nil {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring lock
	if p.db != nil {
		return nil
	}

	if p.initErr != nil {
		return p.initErr
	}

	// Parse DSN from URI
	dsn := p.cfg.URI
	if dsn == "" {
		p.initErr = fmt.Errorf("mysql[%s]: URI is empty", p.cfg.Name)
		return p.initErr
	}

	// Ensure parseTime=true is in the DSN for proper time parsing
	if !strings.Contains(dsn, "parseTime=true") {
		if strings.Contains(dsn, "?") {
			dsn += "&parseTime=true"
		} else {
			dsn += "?parseTime=true"
		}
	}

	// Register TLS config if enabled
	if p.cfg.TLSEnabled && p.secrets != nil {
		tlsRef := p.cfg.TLSCARef
		if tlsRef != "" {
			// Resolve CA certificate from secrets
			caBytes, err := p.secrets.Resolve(ctx, tlsRef)
			if err == nil && len(caBytes) > 0 {
				tlsCfg, err := createTLSConfigMySQL(caBytes)
				if err == nil {
					// Register with driver
					tlsName := "mysql_tls_" + p.cfg.Name
					_ = mysql.RegisterTLSConfig(tlsName, tlsCfg)
					if strings.Contains(dsn, "?") {
						dsn += "&tls=" + tlsName
					} else {
						dsn += "?tls=" + tlsName
					}
				}
			}
		}
	}

	// Open database connection
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		p.initErr = fmt.Errorf("mysql[%s]: failed to open connection: %w", p.cfg.Name, err)
		return p.initErr
	}

	// Configure connection pool
	if p.cfg.PoolSize > 0 {
		db.SetMaxOpenConns(p.cfg.PoolSize)
	} else {
		db.SetMaxOpenConns(25) // Default MySQL pool size
	}
	db.SetMaxIdleConns(5)

	// Set connection max lifetime to 5 minutes
	db.SetConnMaxLifetime(5 * time.Minute)

	// Set context timeout
	var timeout time.Duration
	if p.cfg.TimeoutMs > 0 {
		timeout = time.Duration(p.cfg.TimeoutMs) * time.Millisecond
	} else {
		timeout = 30 * time.Second
	}

	// Test connectivity
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := db.PingContext(testCtx); err != nil {
		_ = db.Close()
		p.initErr = fmt.Errorf("mysql[%s]: ping failed: %w", p.cfg.Name, err)
		return p.initErr
	}

	p.db = db
	return nil
}

// createTLSConfigMySQL creates a TLS configuration from a CA certificate.
func createTLSConfigMySQL(caBytes []byte) (*tls.Config, error) {
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
func (p *MySQLProvider) validateCollectionName(name string) error {
	if name == "" {
		return fmt.Errorf("mysql[%s]: collection name is empty", p.cfg.Name)
	}
	if !collectionNameRegex.MatchString(name) {
		return fmt.Errorf("mysql[%s]: invalid collection name %q (only [a-zA-Z0-9_] allowed)", p.cfg.Name, name)
	}
	return nil
}

// Get retrieves a single document by ID or filter.
// Returns nil, nil if not found.
func (p *MySQLProvider) Get(ctx context.Context, req GetRequest) ([]byte, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return nil, err
	}

	var (
		id  string
		doc string
	)

	if req.ID != "" {
		// Direct lookup by ID
		query := fmt.Sprintf(
			"SELECT id, doc FROM `%s` WHERE id = ? LIMIT 1",
			req.Collection,
		)
		err := p.db.QueryRowContext(ctx, query, req.ID).Scan(&id, &doc)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("mysql[%s]: get by id failed: %w", p.cfg.Name, err)
		}
	} else if len(req.Filter) > 0 {
		// Filter-based lookup using JSON_EXTRACT
		where, args, err := p.buildFilterWhere(req.Filter)
		if err != nil {
			return nil, fmt.Errorf("mysql[%s]: build filter failed: %w", p.cfg.Name, err)
		}
		query := fmt.Sprintf(
			"SELECT id, doc FROM `%s` WHERE %s LIMIT 1",
			req.Collection, where,
		)
		err = p.db.QueryRowContext(ctx, query, args...).Scan(&id, &doc)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("mysql[%s]: get by filter failed: %w", p.cfg.Name, err)
		}
	} else {
		return nil, fmt.Errorf("mysql[%s]: get requires ID or Filter", p.cfg.Name)
	}

	// Merge id and doc fields into result
	result := map[string]interface{}{
		"id": id,
	}

	// Parse and merge document
	var docObj interface{}
	if err := json.Unmarshal([]byte(doc), &docObj); err != nil {
		return nil, fmt.Errorf("mysql[%s]: failed to unmarshal document: %w", p.cfg.Name, err)
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

// Put creates or updates a document.
// If Upsert=false and ID is empty, MySQL generates UUID via DEFAULT.
// If Upsert=true, uses INSERT...ON DUPLICATE KEY UPDATE.
func (p *MySQLProvider) Put(ctx context.Context, req PutRequest) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return err
	}

	if len(req.Document) == 0 {
		return fmt.Errorf("mysql[%s]: document is empty", p.cfg.Name)
	}

	// Validate document is valid JSON
	if !gjson.Valid(string(req.Document)) {
		return fmt.Errorf("mysql[%s]: document is not valid JSON", p.cfg.Name)
	}

	if req.Upsert {
		// UPSERT: INSERT...ON DUPLICATE KEY UPDATE
		if req.ID == "" {
			return fmt.Errorf("mysql[%s]: upsert requires ID", p.cfg.Name)
		}
		query := fmt.Sprintf(
			"INSERT INTO `%s` (id, doc) VALUES (?, ?) ON DUPLICATE KEY UPDATE doc=VALUES(doc), updated_at=CURRENT_TIMESTAMP",
			req.Collection,
		)
		_, err := p.db.ExecContext(ctx, query, req.ID, string(req.Document))
		if err != nil {
			return fmt.Errorf("mysql[%s]: upsert failed: %w", p.cfg.Name, err)
		}
	} else {
		// INSERT: let MySQL generate UUID if ID is empty
		if req.ID == "" {
			query := fmt.Sprintf(
				"INSERT INTO `%s` (doc) VALUES (?)",
				req.Collection,
			)
			_, err := p.db.ExecContext(ctx, query, string(req.Document))
			if err != nil {
				return fmt.Errorf("mysql[%s]: insert failed: %w", p.cfg.Name, err)
			}
		} else {
			query := fmt.Sprintf(
				"INSERT INTO `%s` (id, doc) VALUES (?, ?)",
				req.Collection,
			)
			_, err := p.db.ExecContext(ctx, query, req.ID, string(req.Document))
			if err != nil {
				return fmt.Errorf("mysql[%s]: insert with id failed: %w", p.cfg.Name, err)
			}
		}
	}

	return nil
}

// Delete removes all documents matching the filter.
func (p *MySQLProvider) Delete(ctx context.Context, req DeleteRequest) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return err
	}

	if len(req.Filter) == 0 {
		return fmt.Errorf("mysql[%s]: delete requires a filter", p.cfg.Name)
	}

	where, args, err := p.buildFilterWhere(req.Filter)
	if err != nil {
		return fmt.Errorf("mysql[%s]: build filter failed: %w", p.cfg.Name, err)
	}

	query := fmt.Sprintf("DELETE FROM `%s` WHERE %s", req.Collection, where)
	_, err = p.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("mysql[%s]: delete failed: %w", p.cfg.Name, err)
	}

	return nil
}

// Query retrieves multiple documents matching filter, with projection, sort, limit, and offset.
func (p *MySQLProvider) Query(ctx context.Context, req QueryRequest) ([]byte, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return nil, err
	}

	// Build WHERE clause
	var where string
	var args []interface{}
	if len(req.Filter) > 0 {
		var err error
		where, args, err = p.buildFilterWhere(req.Filter)
		if err != nil {
			return nil, fmt.Errorf("mysql[%s]: build filter failed: %w", p.cfg.Name, err)
		}
		where = " WHERE " + where
	}

	// Build ORDER BY clause
	var orderBy string
	if len(req.Sort) > 0 {
		var err error
		orderBy, err = p.buildOrderBy(req.Sort)
		if err != nil {
			return nil, fmt.Errorf("mysql[%s]: build sort failed: %w", p.cfg.Name, err)
		}
	}

	// Build main query
	query := fmt.Sprintf("SELECT id, doc FROM `%s`%s", req.Collection, where)
	if orderBy != "" {
		query += " " + orderBy
	}
	if req.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, req.Limit)
	}
	if req.Skip > 0 {
		query += " OFFSET ?"
		args = append(args, req.Skip)
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("mysql[%s]: query failed: %w", p.cfg.Name, err)
	}
	defer func() { _ = rows.Close() }()

	var results []interface{}
	for rows.Next() {
		var id, doc string
		if err := rows.Scan(&id, &doc); err != nil {
			return nil, fmt.Errorf("mysql[%s]: scan failed: %w", p.cfg.Name, err)
		}

		// Apply projection if specified
		var docBytes []byte
		if len(req.Projection) > 0 {
			docBytes = p.applyProjection([]byte(doc), req.Projection)
		} else {
			docBytes = []byte(doc)
		}

		// Merge id into result
		var docObj interface{}
		if err := json.Unmarshal(docBytes, &docObj); err != nil {
			return nil, fmt.Errorf("mysql[%s]: unmarshal failed: %w", p.cfg.Name, err)
		}

		result := map[string]interface{}{
			"id": id,
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

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mysql[%s]: rows iteration failed: %w", p.cfg.Name, err)
	}

	return json.Marshal(results)
}

// Count returns the number of documents matching the filter.
func (p *MySQLProvider) Count(ctx context.Context, req QueryRequest) (int64, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return 0, err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return 0, err
	}

	var where string
	var args []interface{}
	if len(req.Filter) > 0 {
		var err error
		where, args, err = p.buildFilterWhere(req.Filter)
		if err != nil {
			return 0, fmt.Errorf("mysql[%s]: build filter failed: %w", p.cfg.Name, err)
		}
		where = " WHERE " + where
	}

	query := fmt.Sprintf("SELECT COUNT(*) FROM `%s`%s", req.Collection, where)
	var count int64
	err := p.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("mysql[%s]: count failed: %w", p.cfg.Name, err)
	}

	return count, nil
}

// Execute runs a raw provider-specific command.
// Command format: {"sql":"SELECT ...","args":[...]}
// The sql field is passed directly to the database — callers must ensure it is trusted/internal.
func (p *MySQLProvider) Execute(ctx context.Context, req ExecuteRequest) ([]byte, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}

	if len(req.Command) == 0 {
		return nil, fmt.Errorf("mysql[%s]: command is empty", p.cfg.Name)
	}

	var cmd struct {
		SQL  string        `json:"sql"`
		Args []interface{} `json:"args"`
	}

	if err := json.Unmarshal(req.Command, &cmd); err != nil {
		return nil, fmt.Errorf("mysql[%s]: invalid command format: %w", p.cfg.Name, err)
	}

	if cmd.SQL == "" {
		return nil, fmt.Errorf("mysql[%s]: sql field is empty", p.cfg.Name)
	}

	// Execute the query
	rows, err := p.db.QueryContext(ctx, cmd.SQL, cmd.Args...)
	if err != nil {
		return nil, fmt.Errorf("mysql[%s]: execute query failed: %w", p.cfg.Name, err)
	}
	defer func() { _ = rows.Close() }()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("mysql[%s]: get columns failed: %w", p.cfg.Name, err)
	}

	var results []map[string]interface{}
	for rows.Next() {
		// Create a slice of interfaces to hold the values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("mysql[%s]: scan row failed: %w", p.cfg.Name, err)
		}

		// Build result map
		result := make(map[string]interface{})
		for i, col := range columns {
			var v interface{}
			val := values[i]
			b, ok := val.([]byte)
			if ok {
				v = string(b)
			} else {
				v = val
			}
			result[col] = v
		}

		results = append(results, result)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mysql[%s]: rows iteration failed: %w", p.cfg.Name, err)
	}

	return json.Marshal(results)
}

// GetMany retrieves multiple documents by IDs in a single batch query.
// Returns a map of id → document JSON bytes. Missing IDs are not included in the result.
func (p *MySQLProvider) GetMany(ctx context.Context, req GetManyRequest) (map[string][]byte, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return nil, err
	}

	if len(req.IDs) == 0 {
		return map[string][]byte{}, nil
	}

	// Build IN clause: WHERE id IN (?, ?, ?)
	placeholders := make([]string, len(req.IDs))
	args := make([]interface{}, len(req.IDs))
	for i, id := range req.IDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := "SELECT id, doc FROM `" + req.Collection + "` WHERE id IN (" + strings.Join(placeholders, ",") + ")"
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("mysql[%s]: getmany failed: %w", p.cfg.Name, err)
	}
	defer func() { _ = rows.Close() }()

	results := make(map[string][]byte, len(req.IDs))
	for rows.Next() {
		var id string
		var doc string
		if err := rows.Scan(&id, &doc); err != nil {
			return nil, fmt.Errorf("mysql[%s]: scan failed: %w", p.cfg.Name, err)
		}
		results[id] = []byte(doc)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("mysql[%s]: rows iteration failed: %w", p.cfg.Name, err)
	}

	return results, nil
}

// PutMany creates or updates multiple documents in a single batch.
// If Upsert=true, uses INSERT...ON DUPLICATE KEY UPDATE.
// If Upsert=false, uses INSERT IGNORE (skips duplicates).
func (p *MySQLProvider) PutMany(ctx context.Context, req PutManyRequest) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return err
	}

	if len(req.Docs) == 0 {
		return nil
	}

	// Validate all documents are valid JSON
	for id, docBytes := range req.Docs {
		if len(docBytes) == 0 {
			return fmt.Errorf("mysql[%s]: document %q is empty", p.cfg.Name, id)
		}
		if !gjson.Valid(string(docBytes)) {
			return fmt.Errorf("mysql[%s]: document %q is not valid JSON", p.cfg.Name, id)
		}
	}

	// Build VALUES clause with multiple rows: (?,?),(?,?),(...,?)
	placeholders := make([]string, 0, len(req.Docs))
	args := make([]interface{}, 0, len(req.Docs)*2)
	for id, docBytes := range req.Docs {
		placeholders = append(placeholders, "(?,?)")
		args = append(args, id, string(docBytes))
	}

	valuesClause := strings.Join(placeholders, ",")

	if req.Upsert {
		// INSERT...ON DUPLICATE KEY UPDATE
		query := fmt.Sprintf(
			"INSERT INTO `%s` (id, doc) VALUES %s ON DUPLICATE KEY UPDATE doc=VALUES(doc), updated_at=CURRENT_TIMESTAMP",
			req.Collection, valuesClause,
		)
		_, err := p.db.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("mysql[%s]: putmany upsert failed: %w", p.cfg.Name, err)
		}
	} else {
		// INSERT IGNORE (skips duplicates)
		query := fmt.Sprintf(
			"INSERT IGNORE INTO `%s` (id, doc) VALUES %s",
			req.Collection, valuesClause,
		)
		_, err := p.db.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("mysql[%s]: putmany insert failed: %w", p.cfg.Name, err)
		}
	}

	return nil
}

// DeleteMany removes multiple documents by IDs in a single batch query.
func (p *MySQLProvider) DeleteMany(ctx context.Context, req DeleteManyRequest) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	if err := p.validateCollectionName(req.Collection); err != nil {
		return err
	}

	if len(req.IDs) == 0 {
		return nil
	}

	// Build IN clause: WHERE id IN (?, ?, ?)
	placeholders := make([]string, len(req.IDs))
	args := make([]interface{}, len(req.IDs))
	for i, id := range req.IDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := "DELETE FROM `" + req.Collection + "` WHERE id IN (" + strings.Join(placeholders, ",") + ")"
	_, err := p.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("mysql[%s]: deletemany failed: %w", p.cfg.Name, err)
	}

	return nil
}

// Ping verifies the connection is alive.
func (p *MySQLProvider) Ping(ctx context.Context) error {
	if err := p.ensureConnected(ctx); err != nil {
		return err
	}

	if err := p.db.PingContext(ctx); err != nil {
		return fmt.Errorf("mysql[%s]: ping failed: %w", p.cfg.Name, err)
	}
	return nil
}

// Close releases database resources.
func (p *MySQLProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.db != nil {
		if err := p.db.Close(); err != nil {
			return fmt.Errorf("mysql[%s]: close failed: %w", p.cfg.Name, err)
		}
		p.db = nil
	}
	return nil
}

// buildFilterWhere converts a JSON filter to SQL WHERE clause and args.
// Simple key-value filter: {"key":"value"} -> "JSON_EXTRACT(doc, '$.key') = ?"
// Supports nested keys: {"user.name":"Alice"} -> "JSON_EXTRACT(doc, '$.user.name') = ?"
func (p *MySQLProvider) buildFilterWhere(filter []byte) (string, []interface{}, error) {
	filterObj := gjson.ParseBytes(filter)
	if !filterObj.IsObject() {
		return "", nil, fmt.Errorf("filter must be a JSON object")
	}

	var conditions []string
	var args []interface{}

	filterObj.ForEach(func(key, value gjson.Result) bool {
		jsonPath := "$." + key.String()
		condition := "JSON_EXTRACT(doc, ?) = ?"
		conditions = append(conditions, condition)
		args = append(args, jsonPath, value.Value())
		return true
	})

	if len(conditions) == 0 {
		return "1=1", []interface{}{}, nil
	}

	where := strings.Join(conditions, " AND ")
	return where, args, nil
}

// buildOrderBy converts sort specification to SQL ORDER BY clause.
// Sort format: {"field":"asc"} or {"field":1} (1=asc, -1=desc)
func (p *MySQLProvider) buildOrderBy(sort []byte) (string, error) {
	sortObj := gjson.ParseBytes(sort)
	if !sortObj.IsObject() {
		return "", fmt.Errorf("sort must be a JSON object")
	}

	var orderClauses []string
	var validationErr error
	sortObj.ForEach(func(key, value gjson.Result) bool {
		field := key.String()
		// Strip leading "$." if present
		bare := strings.TrimPrefix(field, "$.")
		if !sortFieldRegex.MatchString(bare) {
			validationErr = fmt.Errorf("invalid sort field %q", field)
			return false
		}

		jsonPath := "$." + bare
		direction := "ASC"

		// Check if value is a number (1 = asc, -1 = desc)
		if value.Type == gjson.Number {
			if value.Int() < 0 {
				direction = "DESC"
			}
		} else if value.Type == gjson.String && strings.ToLower(value.String()) == "desc" {
			direction = "DESC"
		}

		orderClause := fmt.Sprintf("JSON_EXTRACT(doc, '%s') %s", jsonPath, direction)
		orderClauses = append(orderClauses, orderClause)
		return true
	})

	if validationErr != nil {
		return "", validationErr
	}

	if len(orderClauses) == 0 {
		return "", nil
	}

	return "ORDER BY " + strings.Join(orderClauses, ", "), nil
}

// applyProjection filters document fields based on projection array.
// Projection format: ["field1", "field2", "nested.field3"]
func (p *MySQLProvider) applyProjection(doc []byte, projection []byte) []byte {
	projArray := gjson.ParseBytes(projection)
	if !projArray.IsArray() {
		return doc
	}

	docObj := gjson.ParseBytes(doc)
	if !docObj.IsObject() {
		return doc
	}

	result := make(map[string]interface{})
	projArray.ForEach(func(_, field gjson.Result) bool {
		fieldStr := field.String()
		val := gjson.GetBytes(doc, fieldStr)
		if val.Exists() {
			result[fieldStr] = val.Value()
		}
		return true
	})

	data, _ := json.Marshal(result)
	return data
}
