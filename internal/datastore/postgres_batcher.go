package datastore

import "context"

// putReq is a single Put or Delete request submitted to the WriteBatcher.
type putReq struct {
	tenant   Tenant
	key      string
	value    []byte
	isDelete bool
	errCh    chan error
}

// WriteBatcher coalesces concurrent Put and Delete calls into single MultiPut / Delete
// batch queries. The shared writeBuffer provides read-your-writes semantics.
type WriteBatcher struct {
	inbox    chan putReq
	buf      *writeBuffer
	inner    KeyValueStore
	maxBatch int
}

// newWriteBatcher creates a WriteBatcher and starts its dispatch goroutine.
// The goroutine exits when ctx is cancelled.
func newWriteBatcher(ctx context.Context, inner KeyValueStore, buf *writeBuffer, maxBatch int) *WriteBatcher {
	wb := &WriteBatcher{
		inbox:    make(chan putReq, maxBatch*4),
		buf:      buf,
		inner:    inner,
		maxBatch: maxBatch,
	}
	go wb.run(ctx)
	return wb
}

// Put marks the key as pending in the write buffer (before inbox), then blocks
// until the dispatch goroutine has flushed the batch.
func (wb *WriteBatcher) Put(ctx context.Context, tenant Tenant, key string, value []byte) error {
	bufKey := bufferKey(tenant, key)
	// Critical: update pending BEFORE sending to inbox so concurrent reads see it.
	wb.buf.SetPending(bufKey, value)

	errCh := make(chan error, 1)
	select {
	case wb.inbox <- putReq{tenant: tenant, key: key, value: value, isDelete: false, errCh: errCh}:
	case <-ctx.Done():
		wb.buf.Remove([]string{bufKey}, nil)
		return ctx.Err()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Delete marks the key as delete-pending in the write buffer, then blocks until
// the dispatch goroutine has flushed the batch.
func (wb *WriteBatcher) Delete(ctx context.Context, tenant Tenant, key string) error {
	bufKey := bufferKey(tenant, key)
	// Critical: update pending BEFORE sending to inbox.
	wb.buf.SetDeletePending(bufKey)

	errCh := make(chan error, 1)
	select {
	case wb.inbox <- putReq{tenant: tenant, key: key, isDelete: true, errCh: errCh}:
	case <-ctx.Done():
		wb.buf.Remove(nil, []string{bufKey})
		return ctx.Err()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run is the single dispatch goroutine for WriteBatcher.
func (wb *WriteBatcher) run(ctx context.Context) {
	for {
		// 1. Block on the first request — no busy-loop when idle.
		var first putReq
		select {
		case first = <-wb.inbox:
		case <-ctx.Done():
			return
		}

		batch := make([]putReq, 1, wb.maxBatch)
		batch[0] = first

		// 2. Non-blocking drain — pick up every goroutine already waiting.
	drain:
		for len(batch) < wb.maxBatch {
			select {
			case req := <-wb.inbox:
				batch = append(batch, req)
			default:
				break drain
			}
		}

		// 3. Group requests by tenant; last-writer-wins per (tenant, key) for puts.
		putsByTenant := make(map[Tenant]map[string][]byte)
		deletesByTenant := make(map[Tenant][]string)

		for _, req := range batch {
			if req.isDelete {
				deletesByTenant[req.tenant] = append(deletesByTenant[req.tenant], req.key)
			} else {
				if putsByTenant[req.tenant] == nil {
					putsByTenant[req.tenant] = make(map[string][]byte)
				}
				putsByTenant[req.tenant][req.key] = req.value // last-writer-wins
			}
		}

		// 4. Execute puts and deletes; capture first error.
		var batchErr error
		batchCtx := context.Background()

		for tenant, kvs := range putsByTenant {
			if bs, ok := wb.inner.(BatchStore); ok {
				if err := bs.MultiPut(batchCtx, tenant, kvs); err != nil && batchErr == nil {
					batchErr = err
				}
			} else {
				for k, v := range kvs {
					if err := wb.inner.Put(batchCtx, tenant, k, v); err != nil && batchErr == nil {
						batchErr = err
					}
				}
			}
		}
		for tenant, keys := range deletesByTenant {
			for _, k := range keys {
				if err := wb.inner.Delete(batchCtx, tenant, k); err != nil && batchErr == nil {
					batchErr = err
				}
			}
		}

		// 5. Update buffer and notify all callers.
		if batchErr == nil {
			flushedKVs := make(map[string][]byte, len(batch))
			var flushedDeletes []string
			for _, req := range batch {
				bk := bufferKey(req.tenant, req.key)
				if req.isDelete {
					flushedDeletes = append(flushedDeletes, bk)
				} else {
					flushedKVs[bk] = req.value
				}
			}
			wb.buf.Rotate(flushedKVs, flushedDeletes)
			for _, req := range batch {
				req.errCh <- nil
			}
		} else {
			var failedKeys []string
			var failedDeletes []string
			for _, req := range batch {
				bk := bufferKey(req.tenant, req.key)
				if req.isDelete {
					failedDeletes = append(failedDeletes, bk)
				} else {
					failedKeys = append(failedKeys, bk)
				}
			}
			wb.buf.Remove(failedKeys, failedDeletes)
			for _, req := range batch {
				req.errCh <- batchErr
			}
		}
	}
}

// ---------------------------------------------------------------------------
// ReadBatcher
// ---------------------------------------------------------------------------

// getReq is a single Get request submitted to ReadBatcher.
type getReq struct {
	tenant Tenant
	key    string
	respCh chan getResp
}

// getResp carries the result back to the requesting goroutine.
type getResp struct {
	value []byte
	found bool
}

// ReadBatcher coalesces concurrent Get calls into single MultiGet batch queries.
// It checks the shared writeBuffer first for read-your-writes semantics.
type ReadBatcher struct {
	inbox    chan getReq
	buf      *writeBuffer
	inner    KeyValueStore
	maxBatch int
}

// newReadBatcher creates a ReadBatcher and starts its dispatch goroutine.
func newReadBatcher(ctx context.Context, inner KeyValueStore, buf *writeBuffer, maxBatch int) *ReadBatcher {
	rb := &ReadBatcher{
		inbox:    make(chan getReq, maxBatch*4),
		buf:      buf,
		inner:    inner,
		maxBatch: maxBatch,
	}
	go rb.run(ctx)
	return rb
}

// Get checks the write buffer first (read-your-writes), then fans into the
// dispatch goroutine for a batched DB lookup if the key is not buffered.
func (rb *ReadBatcher) Get(ctx context.Context, tenant Tenant, key string) ([]byte, bool, error) {
	bufKey := bufferKey(tenant, key)
	if val, found, deleted := rb.buf.Lookup(bufKey); found {
		if deleted {
			return nil, false, nil
		}
		return val, true, nil
	}

	respCh := make(chan getResp, 1)
	select {
	case rb.inbox <- getReq{tenant: tenant, key: key, respCh: respCh}:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}

	select {
	case resp := <-respCh:
		return resp.value, resp.found, nil
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
}

// run is the single dispatch goroutine for ReadBatcher.
func (rb *ReadBatcher) run(ctx context.Context) {
	for {
		// 1. Block on the first request — no busy-loop when idle.
		var first getReq
		select {
		case first = <-rb.inbox:
		case <-ctx.Done():
			return
		}

		batch := make([]getReq, 1, rb.maxBatch)
		batch[0] = first

		// 2. Non-blocking drain — pick up every goroutine already waiting.
	drain:
		for len(batch) < rb.maxBatch {
			select {
			case req := <-rb.inbox:
				batch = append(batch, req)
			default:
				break drain
			}
		}

		// 3. Deduplicate keys per tenant.
		type tenantKey struct {
			tenant Tenant
			key    string
		}
		keyToChans := make(map[tenantKey][]chan getResp, len(batch))
		for _, req := range batch {
			tk := tenantKey{tenant: req.tenant, key: req.key}
			keyToChans[tk] = append(keyToChans[tk], req.respCh)
		}

		// Build unique-key slices per tenant.
		keysByTenant := make(map[Tenant][]string)
		for tk := range keyToChans {
			keysByTenant[tk.tenant] = append(keysByTenant[tk.tenant], tk.key)
		}

		// 4. Execute MultiGet per tenant and fan results back.
		batchCtx := context.Background()
		for tenant, keys := range keysByTenant {
			var results map[string][]byte
			var err error

			if bs, ok := rb.inner.(BatchStore); ok {
				results, err = bs.MultiGet(batchCtx, tenant, keys)
			} else {
				results = make(map[string][]byte, len(keys))
				for _, k := range keys {
					v, found, e := rb.inner.Get(batchCtx, tenant, k)
					if e != nil {
						err = e
						break
					}
					if found {
						results[k] = v
					}
				}
			}

			for _, k := range keys {
				tk := tenantKey{tenant: tenant, key: k}
				chans := keyToChans[tk]
				var resp getResp
				if err != nil {
					resp = getResp{found: false}
				} else {
					v, ok := results[k]
					resp = getResp{value: v, found: ok}
				}
				for _, ch := range chans {
					ch <- resp
				}
			}
		}
	}
}

// bufferKey returns a compound key for the writeBuffer that namespaces entries
// per tenant, preventing collisions between tenants sharing the same logical key.
func bufferKey(tenant Tenant, key string) string {
	return string(tenant) + "\x00" + key
}
