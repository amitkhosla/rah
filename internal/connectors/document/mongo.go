package document

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/config"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// MongoProvider implements DocumentProvider for MongoDB
type MongoProvider struct {
	cfg     config.DocumentConnectorConfig
	secrets SecretResolver
	client  *mongo.Client
	db      *mongo.Database
	mu      sync.Mutex
}

// connect establishes the MongoDB connection (lazy initialization with reconnect on error)
func (p *MongoProvider) connect(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil {
		return nil
	}
	return p.performConnect(ctx)
}

// performConnect does the actual connection work
func (p *MongoProvider) performConnect(ctx context.Context) error {
	clientOpts := options.Client().ApplyURI(p.cfg.URI)

	// Set pool size if configured
	if p.cfg.PoolSize > 0 {
		clientOpts.SetMaxPoolSize(uint64(p.cfg.PoolSize))
	}

	// Configure TLS if enabled
	if p.cfg.TLSEnabled {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}

		// If TLSCARef is provided, load the CA certificate (resolve secret first)
		if p.cfg.TLSCARef != "" {
			caBytes, err := p.secrets.Resolve(ctx, p.cfg.TLSCARef)
			if err != nil {
				return fmt.Errorf("mongo[%s]: resolve TLS CA: %w", p.cfg.Name, err)
			}

			caCertPool := x509.NewCertPool()
			if !caCertPool.AppendCertsFromPEM(caBytes) {
				return fmt.Errorf("mongo[%s]: failed to parse CA certificate PEM", p.cfg.Name)
			}
			tlsConfig.RootCAs = caCertPool
		}

		clientOpts.SetTLSConfig(tlsConfig)
	}

	// Create client (v2 API: Connect takes only options, no context)
	client, err := mongo.Connect(clientOpts)
	if err != nil {
		return fmt.Errorf("mongo[%s]: failed to connect: %w", p.cfg.Name, err)
	}

	p.client = client
	p.db = client.Database(p.cfg.Database)

	return nil
}

// applyTimeout wraps context with timeout if configured
func (p *MongoProvider) applyTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if p.cfg.TimeoutMs > 0 {
		return context.WithTimeout(ctx, time.Duration(p.cfg.TimeoutMs)*time.Millisecond)
	}
	return ctx, func() {}
}

// Get retrieves a document from MongoDB
func (p *MongoProvider) Get(ctx context.Context, req GetRequest) ([]byte, error) {
	if err := p.connect(ctx); err != nil {
		return nil, err
	}

	ctx, cancel := p.applyTimeout(ctx)
	defer cancel()

	coll := p.db.Collection(req.Collection)
	var filter bson.D

	if req.ID != "" {
		// Filter by ID
		filter = bson.D{{Key: "_id", Value: req.ID}}
	} else {
		// Unmarshal provided filter
		if len(req.Filter) > 0 {
			err := bson.UnmarshalExtJSON(req.Filter, true, &filter)
			if err != nil {
				return nil, fmt.Errorf("mongo[%s]: invalid filter: %w", p.cfg.Name, err)
			}
		}
	}

	var result bson.M
	err := coll.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil // Not an error, just not found
		}
		return nil, fmt.Errorf("mongo[%s]: get failed: %w", p.cfg.Name, err)
	}

	// Marshal result to JSON
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("mongo[%s]: marshal failed: %w", p.cfg.Name, err)
	}

	return jsonBytes, nil
}

// Put inserts or replaces a document in MongoDB
func (p *MongoProvider) Put(ctx context.Context, req PutRequest) error {
	if err := p.connect(ctx); err != nil {
		return err
	}

	ctx, cancel := p.applyTimeout(ctx)
	defer cancel()

	coll := p.db.Collection(req.Collection)

	// Parse the document
	var doc bson.D
	err := bson.UnmarshalExtJSON(req.Document, true, &doc)
	if err != nil {
		return fmt.Errorf("mongo[%s]: invalid document: %w", p.cfg.Name, err)
	}

	if req.Upsert {
		// Use ReplaceOne with upsert
		var filter bson.D
		if req.ID != "" {
			filter = bson.D{{Key: "_id", Value: req.ID}}
		} else {
			// Extract _id from document if present
			for _, elem := range doc {
				if elem.Key == "_id" {
					filter = bson.D{{Key: "_id", Value: elem.Value}}
					break
				}
			}
		}

		opts := options.Replace().SetUpsert(true)
		_, err = coll.ReplaceOne(ctx, filter, doc, opts)
		if err != nil {
			return fmt.Errorf("mongo[%s]: upsert failed: %w", p.cfg.Name, err)
		}
	} else {
		if req.ID != "" {
			// Set the ID in the document
			hasID := false
			for i, elem := range doc {
				if elem.Key == "_id" {
					doc[i].Value = req.ID
					hasID = true
					break
				}
			}
			if !hasID {
				doc = append(bson.D{{Key: "_id", Value: req.ID}}, doc...)
			}
		}

		// Use InsertOne
		_, err = coll.InsertOne(ctx, doc)
		if err != nil {
			return fmt.Errorf("mongo[%s]: insert failed: %w", p.cfg.Name, err)
		}
	}

	return nil
}

// Delete removes documents from MongoDB
func (p *MongoProvider) Delete(ctx context.Context, req DeleteRequest) error {
	if err := p.connect(ctx); err != nil {
		return err
	}

	ctx, cancel := p.applyTimeout(ctx)
	defer cancel()

	coll := p.db.Collection(req.Collection)

	// Parse filter
	var filter bson.D
	if len(req.Filter) > 0 {
		err := bson.UnmarshalExtJSON(req.Filter, true, &filter)
		if err != nil {
			return fmt.Errorf("mongo[%s]: invalid filter: %w", p.cfg.Name, err)
		}
	}

	_, err := coll.DeleteMany(ctx, filter)
	if err != nil {
		return fmt.Errorf("mongo[%s]: delete failed: %w", p.cfg.Name, err)
	}

	return nil
}

// GetMany retrieves multiple documents from MongoDB by IDs
func (p *MongoProvider) GetMany(ctx context.Context, req GetManyRequest) (map[string][]byte, error) {
	if err := p.connect(ctx); err != nil {
		return nil, err
	}
	if len(req.IDs) == 0 {
		return map[string][]byte{}, nil
	}
	ctx, cancel := p.applyTimeout(ctx)
	defer cancel()

	// Convert string IDs to interface slice for $in query
	ids := make([]interface{}, len(req.IDs))
	for i, id := range req.IDs {
		ids[i] = id
	}

	coll := p.db.Collection(req.Collection)
	cursor, err := coll.Find(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	results := make(map[string][]byte, len(req.IDs))
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		id, _ := doc["_id"].(string)
		// Marshal back to JSON
		data, err := bson.MarshalExtJSON(doc, false, false)
		if err != nil {
			return nil, err
		}
		results[id] = data
	}
	return results, cursor.Err()
}

// PutMany inserts or replaces multiple documents in MongoDB
func (p *MongoProvider) PutMany(ctx context.Context, req PutManyRequest) error {
	if err := p.connect(ctx); err != nil {
		return err
	}
	if len(req.Docs) == 0 {
		return nil
	}
	ctx, cancel := p.applyTimeout(ctx)
	defer cancel()

	coll := p.db.Collection(req.Collection)

	models := make([]mongo.WriteModel, 0, len(req.Docs))
	for id, docBytes := range req.Docs {
		var doc bson.M
		if err := bson.UnmarshalExtJSON(docBytes, false, &doc); err != nil {
			return fmt.Errorf("document %q: %w", id, err)
		}
		doc["_id"] = id
		if req.Upsert {
			models = append(models, mongo.NewReplaceOneModel().
				SetFilter(bson.M{"_id": id}).
				SetReplacement(doc).
				SetUpsert(true))
		} else {
			models = append(models, mongo.NewReplaceOneModel().
				SetFilter(bson.M{"_id": id}).
				SetReplacement(doc))
		}
	}

	opts := options.BulkWrite().SetOrdered(false)
	_, err := coll.BulkWrite(ctx, models, opts)
	return err
}

// DeleteMany removes multiple documents from MongoDB by IDs
func (p *MongoProvider) DeleteMany(ctx context.Context, req DeleteManyRequest) error {
	if err := p.connect(ctx); err != nil {
		return err
	}
	if len(req.IDs) == 0 {
		return nil
	}
	ctx, cancel := p.applyTimeout(ctx)
	defer cancel()

	ids := make([]interface{}, len(req.IDs))
	for i, id := range req.IDs {
		ids[i] = id
	}

	coll := p.db.Collection(req.Collection)
	_, err := coll.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
	return err
}

// Query finds documents in MongoDB
func (p *MongoProvider) Query(ctx context.Context, req QueryRequest) ([]byte, error) {
	if err := p.connect(ctx); err != nil {
		return nil, err
	}

	ctx, cancel := p.applyTimeout(ctx)
	defer cancel()

	coll := p.db.Collection(req.Collection)

	// Parse filter
	var filter bson.D
	if len(req.Filter) > 0 {
		err := bson.UnmarshalExtJSON(req.Filter, true, &filter)
		if err != nil {
			return nil, fmt.Errorf("mongo[%s]: invalid filter: %w", p.cfg.Name, err)
		}
	}

	// Build FindOptions
	opts := options.Find()

	// Add projection
	if len(req.Projection) > 0 {
		var projection bson.D
		err := bson.UnmarshalExtJSON(req.Projection, true, &projection)
		if err != nil {
			return nil, fmt.Errorf("mongo[%s]: invalid projection: %w", p.cfg.Name, err)
		}
		opts.SetProjection(projection)
	}

	// Add sort
	if len(req.Sort) > 0 {
		var sort bson.D
		err := bson.UnmarshalExtJSON(req.Sort, true, &sort)
		if err != nil {
			return nil, fmt.Errorf("mongo[%s]: invalid sort: %w", p.cfg.Name, err)
		}
		opts.SetSort(sort)
	}

	// Add limit and skip
	if req.Limit > 0 {
		opts.SetLimit(int64(req.Limit))
	}
	if req.Skip > 0 {
		opts.SetSkip(int64(req.Skip))
	}

	// Execute query
	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo[%s]: query failed: %w", p.cfg.Name, err)
	}
	defer cursor.Close(ctx)

	// Collect documents
	var docs []json.RawMessage
	err = cursor.All(ctx, &docs)
	if err != nil {
		return nil, fmt.Errorf("mongo[%s]: cursor iteration failed: %w", p.cfg.Name, err)
	}

	// Marshal to JSON array
	result, err := json.Marshal(docs)
	if err != nil {
		return nil, fmt.Errorf("mongo[%s]: marshal failed: %w", p.cfg.Name, err)
	}

	return result, nil
}

// Count counts documents in MongoDB
func (p *MongoProvider) Count(ctx context.Context, req QueryRequest) (int64, error) {
	if err := p.connect(ctx); err != nil {
		return 0, err
	}

	ctx, cancel := p.applyTimeout(ctx)
	defer cancel()

	coll := p.db.Collection(req.Collection)

	// Parse filter
	var filter bson.D
	if len(req.Filter) > 0 {
		err := bson.UnmarshalExtJSON(req.Filter, true, &filter)
		if err != nil {
			return 0, fmt.Errorf("mongo[%s]: invalid filter: %w", p.cfg.Name, err)
		}
	}

	count, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("mongo[%s]: count failed: %w", p.cfg.Name, err)
	}

	return count, nil
}

// Execute runs a raw MongoDB command
func (p *MongoProvider) Execute(ctx context.Context, req ExecuteRequest) ([]byte, error) {
	if err := p.connect(ctx); err != nil {
		return nil, err
	}

	ctx, cancel := p.applyTimeout(ctx)
	defer cancel()

	// Parse command
	var command bson.D
	err := bson.UnmarshalExtJSON(req.Command, true, &command)
	if err != nil {
		return nil, fmt.Errorf("mongo[%s]: invalid command: %w", p.cfg.Name, err)
	}

	// Run command
	result := p.db.RunCommand(ctx, command)
	if result.Err() != nil {
		return nil, fmt.Errorf("mongo[%s]: command failed: %w", p.cfg.Name, result.Err())
	}

	// Decode result to bson.M then marshal to JSON (v2: use Decode, not DecodeBytes)
	var resultMap bson.M
	if err := result.Decode(&resultMap); err != nil {
		return nil, fmt.Errorf("mongo[%s]: decode result failed: %w", p.cfg.Name, err)
	}

	jsonBytes, err := json.Marshal(resultMap)
	if err != nil {
		return nil, fmt.Errorf("mongo[%s]: marshal result failed: %w", p.cfg.Name, err)
	}

	return jsonBytes, nil
}

// Ping checks the MongoDB connection
func (p *MongoProvider) Ping(ctx context.Context) error {
	if err := p.connect(ctx); err != nil {
		return err
	}

	ctx, cancel := p.applyTimeout(ctx)
	defer cancel()

	err := p.client.Ping(ctx, readpref.Primary())
	if err != nil {
		return fmt.Errorf("mongo[%s]: ping failed: %w", p.cfg.Name, err)
	}

	return nil
}

// Close closes the MongoDB connection
func (p *MongoProvider) Close() error {
	if p.client == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := p.client.Disconnect(ctx)
	if err != nil {
		return fmt.Errorf("mongo[%s]: disconnect failed: %w", p.cfg.Name, err)
	}

	return nil
}
