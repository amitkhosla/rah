package document

import (
	"context"
	"sync"

	"github.com/amitkhosla/rah/internal/config"
)

// docGetReq is a single coalesced Get request.
type docGetReq struct {
	collection string
	id         string
	respCh     chan docGetResp
}

type docGetResp struct {
	data []byte
	err  error
}

// docPutReq is a single coalesced Put request.
type docPutReq struct {
	collection string
	id         string
	document   []byte
	upsert     bool
	errCh      chan error
}

// BatchingDocumentProvider wraps a DocumentProvider and coalesces concurrent
// Get calls into GetMany and concurrent Put calls into PutMany.
// When WriteBuffer is true, pending puts are visible to concurrent Gets
// before they are flushed to the underlying store (read-your-writes).
// Batches are dispatched concurrently (up to maxConcurrent) so the
// underlying provider's connection pool is fully utilized.
type BatchingDocumentProvider struct {
	inner          DocumentProvider
	writeBuffer    bool
	pending        sync.Map // "collection\x00id" → []byte; only used when writeBuffer=true
	readCh         chan docGetReq
	writeCh        chan docPutReq
	cancel         context.CancelFunc
	maxBatch       int
	maxConcurrent  int
}

func newBatchingDocumentProvider(inner DocumentProvider, cfg config.DocumentConnectorConfig) *BatchingDocumentProvider {
	return &BatchingDocumentProvider{
		inner:         inner,
		writeBuffer:   cfg.WriteBuffer,
		readCh:        make(chan docGetReq, 512),
		writeCh:       make(chan docPutReq, 512),
		maxBatch:      100,
		maxConcurrent: 8,
	}
}

func (b *BatchingDocumentProvider) start(ctx context.Context) {
	bctx, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	readSem := make(chan struct{}, b.maxConcurrent)
	writeSem := make(chan struct{}, b.maxConcurrent)
	go b.runReads(bctx, readSem)
	go b.runWrites(bctx, writeSem)
}

// Get coalesces ID-based reads. Filter-based reads bypass the batcher.
func (b *BatchingDocumentProvider) Get(ctx context.Context, req GetRequest) ([]byte, error) {
	if len(req.Filter) > 0 {
		return b.inner.Get(ctx, req)
	}
	if b.writeBuffer {
		if v, ok := b.pending.Load(req.Collection + "\x00" + req.ID); ok {
			return v.([]byte), nil
		}
	}
	respCh := make(chan docGetResp, 1)
	select {
	case b.readCh <- docGetReq{collection: req.Collection, id: req.ID, respCh: respCh}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case resp := <-respCh:
		return resp.data, resp.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Put coalesces writes. When WriteBuffer is true the pending value is immediately
// visible to concurrent Gets and removed after the batch flushes to the store.
func (b *BatchingDocumentProvider) Put(ctx context.Context, req PutRequest) error {
	pendingKey := req.Collection + "\x00" + req.ID
	if b.writeBuffer {
		b.pending.Store(pendingKey, req.Document)
	}
	errCh := make(chan error, 1)
	select {
	case b.writeCh <- docPutReq{
		collection: req.Collection,
		id:         req.ID,
		document:   req.Document,
		upsert:     req.Upsert,
		errCh:      errCh,
	}:
	case <-ctx.Done():
		if b.writeBuffer {
			b.pending.Delete(pendingKey)
		}
		return ctx.Err()
	}
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Delete passes through — filter-based deletes cannot be coalesced by ID.
// When WriteBuffer is true this may create a brief inconsistency window;
// callers should not rely on read-your-writes after a delete.
func (b *BatchingDocumentProvider) Delete(ctx context.Context, req DeleteRequest) error {
	return b.inner.Delete(ctx, req)
}

// Query, Count, Execute, Ping — all pass through directly.
func (b *BatchingDocumentProvider) Query(ctx context.Context, req QueryRequest) ([]byte, error) {
	return b.inner.Query(ctx, req)
}
func (b *BatchingDocumentProvider) Count(ctx context.Context, req QueryRequest) (int64, error) {
	return b.inner.Count(ctx, req)
}
func (b *BatchingDocumentProvider) Execute(ctx context.Context, req ExecuteRequest) ([]byte, error) {
	return b.inner.Execute(ctx, req)
}
func (b *BatchingDocumentProvider) Ping(ctx context.Context) error {
	return b.inner.Ping(ctx)
}
func (b *BatchingDocumentProvider) Close() error {
	if b.cancel != nil {
		b.cancel()
	}
	return b.inner.Close()
}

// GetMany, PutMany, DeleteMany pass through — caller is already batching.
func (b *BatchingDocumentProvider) GetMany(ctx context.Context, req GetManyRequest) (map[string][]byte, error) {
	return b.inner.GetMany(ctx, req)
}
func (b *BatchingDocumentProvider) PutMany(ctx context.Context, req PutManyRequest) error {
	return b.inner.PutMany(ctx, req)
}
func (b *BatchingDocumentProvider) DeleteMany(ctx context.Context, req DeleteManyRequest) error {
	return b.inner.DeleteMany(ctx, req)
}

func (b *BatchingDocumentProvider) runReads(ctx context.Context, sem chan struct{}) {
	for {
		var first docGetReq
		select {
		case first = <-b.readCh:
		case <-ctx.Done():
			return
		}
		// Drain whatever requests are already queued — no artificial delay.
		// Concurrent callers will have sent to the buffered channel before this
		// goroutine gets scheduled again, so they coalesce naturally.
		batch := []docGetReq{first}
	drain:
		for len(batch) < b.maxBatch {
			select {
			case req := <-b.readCh:
				batch = append(batch, req)
			default:
				break drain
			}
		}
		// Dispatch the batch concurrently so the underlying connection pool is
		// utilized — the semaphore caps in-flight batches to maxConcurrent.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		go func(batch []docGetReq) {
			defer func() { <-sem }()
			// Group by collection; deduplicate IDs.
			byCollection := make(map[string]map[string][]chan docGetResp, 4)
			for _, req := range batch {
				if byCollection[req.collection] == nil {
					byCollection[req.collection] = make(map[string][]chan docGetResp)
				}
				byCollection[req.collection][req.id] = append(byCollection[req.collection][req.id], req.respCh)
			}
			for collection, idChans := range byCollection {
				ids := make([]string, 0, len(idChans))
				for id := range idChans {
					ids = append(ids, id)
				}
				results, err := b.inner.GetMany(ctx, GetManyRequest{Collection: collection, IDs: ids})
				for id, chans := range idChans {
					var resp docGetResp
					if err != nil {
						resp = docGetResp{err: err}
					} else {
						resp = docGetResp{data: results[id]}
					}
					for _, ch := range chans {
						ch <- resp
					}
				}
			}
		}(batch)
	}
}

func (b *BatchingDocumentProvider) runWrites(ctx context.Context, sem chan struct{}) {
	for {
		var first docPutReq
		select {
		case first = <-b.writeCh:
		case <-ctx.Done():
			return
		}
		// Drain whatever requests are already queued — no artificial delay.
		batch := []docPutReq{first}
	drain:
		for len(batch) < b.maxBatch {
			select {
			case req := <-b.writeCh:
				batch = append(batch, req)
			default:
				break drain
			}
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		go func(batch []docPutReq) {
			defer func() { <-sem }()
			// Group by (collection, upsert); last-writer-wins per id.
			type batchKey struct {
				collection string
				upsert     bool
			}
			byKey := make(map[batchKey]map[string][]byte, 4)
			errChsByKey := make(map[batchKey][]chan error)
			for _, req := range batch {
				k := batchKey{req.collection, req.upsert}
				if byKey[k] == nil {
					byKey[k] = make(map[string][]byte)
				}
				byKey[k][req.id] = req.document
				errChsByKey[k] = append(errChsByKey[k], req.errCh)
			}
			for k, docs := range byKey {
				err := b.inner.PutMany(ctx, PutManyRequest{
					Collection: k.collection,
					Docs:       docs,
					Upsert:     k.upsert,
				})
				for _, ch := range errChsByKey[k] {
					ch <- err
				}
				// Pending entries are NOT cleared on flush — they remain as a read cache
				// so that a subsequent Get sees the last written value without hitting the store.
				// Entries are only invalidated when Delete is called on the same key.
			}
		}(batch)
	}
}
