package rctx

// OpType identifies the kind of storage operation.
type OpType uint8

const (
	// OpPut writes Value to the store at Key.
	OpPut OpType = iota
	// OpGet reads a value from the store; result is written to Result field.
	OpGet
)

// OpTarget routes the operation to the correct manager.
type OpTarget uint8

const (
	TargetCache        OpTarget = iota // route to CacheManager
	TargetRegistryURL                  // route to registry URLs PropStore
	TargetRegistryID                   // route to registry IDs PropStore
	TargetRegistryMeta                 // route to registry Meta PropStore
)

// DefaultMaxOps is the default capacity of the per-request op buffer.
// Sized to fit in the inline backing array without heap allocation.
const DefaultMaxOps = 64

// StorageOp is a single unit of work buffered by EmitPut / EmitGet.
// GET results are written to Result before Batch.Done is closed;
// callers must not read Result until they receive the done signal.
type StorageOp struct {
	Type     OpType
	Target   OpTarget
	Async    bool   // PUT only, cache only: caller does not wait for Redis write
	TenantID uint16 // captured from ctx.TenantID at emit time
	TTL      uint32 // PUT only: time-to-live in seconds; 0 = backend default
	Key      []byte
	Value    []byte // nil for GET
	DestSlot int    // GET only: slot index where instruction writes Result
	Result   []byte // populated by executor after GET completes; nil = miss/error
}

// EmitPut enqueues a PUT operation on ctx. If the buffer is full it triggers
// ctx.OnFlush before writing. No allocations; writes directly into opsBase.
func EmitPut(ctx *Context, key, value []byte, target OpTarget, async bool, ttl uint32) {
	if ctx.MaxOps > 0 && ctx.OpCount >= ctx.MaxOps {
		if ctx.OnFlush != nil {
			ctx.OnFlush(ctx)
		}
	}
	ctx.opsBase[ctx.OpCount] = StorageOp{
		Type:     OpPut,
		Target:   target,
		Async:    async,
		TenantID: ctx.TenantID,
		TTL:      ttl,
		Key:      key,
		Value:    value,
	}
	ctx.OpCount++
	ctx.Ops = ctx.opsBase[:ctx.OpCount]
}

// EmitGet enqueues a GET operation on ctx. If the buffer is full it triggers
// ctx.OnFlush before writing. No allocations; writes directly into opsBase.
func EmitGet(ctx *Context, key []byte, target OpTarget, destSlot int) {
	if ctx.MaxOps > 0 && ctx.OpCount >= ctx.MaxOps {
		if ctx.OnFlush != nil {
			ctx.OnFlush(ctx)
		}
	}
	ctx.opsBase[ctx.OpCount] = StorageOp{
		Type:     OpGet,
		Target:   target,
		TenantID: ctx.TenantID,
		Key:      key,
		DestSlot: destSlot,
	}
	ctx.OpCount++
	ctx.Ops = ctx.opsBase[:ctx.OpCount]
}

// Batch groups StorageOps for dispatch to an OpFlusher.
// Done is closed by the executor after all Result fields are written.
// Done may be nil for fire-and-forget batches (async PUT only) — the
// executor must check for nil before closing.
// Callers must not read any op.Result until Done is closed.
type Batch struct {
	Ops  []StorageOp
	Done chan struct{}
}

// ResetOps resets the op buffer for pool reuse. Elements in opsBase are not
// zeroed — they are overwritten on next use. MaxOps and OnFlush are not reset
// here; they are pool-level config set once by FlowManager.
func (ctx *Context) ResetOps() {
	ctx.Ops = ctx.opsBase[:0]
	ctx.OpCount = 0
	ctx.opKeysUsed = 0
}
