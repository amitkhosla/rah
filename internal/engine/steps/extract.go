package steps

import (
	"github.com/tidwall/gjson"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// ExtractOp describes one field extraction and the op to emit for it.
// All fields except DestSlot are captured at compile (bake) time — zero
// runtime parsing overhead.
type ExtractOp struct {
	// Path is the gjson path to extract from the JSON body.
	// Examples: "user.id", "items.0.url", "tenant"
	Path string

	// KeyPrefix is the static key prefix baked in at compile time.
	// The full key sent to the store is: KeyPrefix + extracted_value.
	// Example: "user:" produces key "user:<extracted>"
	KeyPrefix string

	// OpType is rctx.OpGet or rctx.OpPut.
	OpType rctx.OpType

	// Target is the destination store (TargetCache, TargetRegistryURL, etc.)
	Target rctx.OpTarget

	// DestSlot is used for OpGet: which ByteSlot receives the result.
	// Ignored for OpPut. Use -1 to discard GET result.
	DestSlot int

	// ValueSlot is used for OpPut: which ByteSlot holds the value to store.
	// Use -1 to store the extracted value itself as the value.
	ValueSlot int

	// Async applies to OpPut only: true = fire-and-forget.
	Async bool

	// TTL applies to OpPut targeting TargetCache: time-to-live in seconds; 0 = backend default.
	TTL uint32
}

// JSONExtractEmit extracts multiple fields from a JSON body in one gjson scan
// and emits one op per field into the op buffer. No intermediate slots are
// consumed for extracted values — keys are built directly into ctx.opKeysBuf.
//
// bodySlot: index of ByteSlot holding the JSON body bytes.
// ops: slice of ExtractOp descriptors, captured at bake time.
func JSONExtractEmit(bodySlot int, ops []ExtractOp) engine.Instruction {
	// Pre-build the path slice once at bake time.
	paths := make([]string, len(ops))
	for i, op := range ops {
		paths[i] = op.Path
	}

	return engine.Instruction{
		Name: "json_extract_emit",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			body := ctx.ByteSlots[bodySlot]
			if len(body) == 0 {
				return s.PC + 1
			}

			results := gjson.GetManyBytes(body, paths...)

			for i, result := range results {
				if !result.Exists() {
					continue
				}
				op := ops[i]
				extracted := result.String()
				if extracted == "" {
					continue
				}

				// Build key: prefix + extracted value, zero heap allocation.
				keyLen := len(op.KeyPrefix) + len(extracted)
				keyBuf := ctx.AllocOpKey(keyLen)
				n := copy(keyBuf, op.KeyPrefix)
				copy(keyBuf[n:], extracted)
				key := keyBuf[:keyLen]

				switch op.OpType {
				case rctx.OpGet:
					rctx.EmitGet(ctx, key, op.Target, op.DestSlot)
				case rctx.OpPut:
					var val []byte
					if op.ValueSlot >= 0 {
						val = ctx.ByteSlots[op.ValueSlot]
					} else {
						val = []byte(extracted)
					}
					rctx.EmitPut(ctx, key, val, op.Target, op.Async, op.TTL)
				}
			}
			return s.PC + 1
		},
	}
}

// JSONForeachEmit iterates over a JSON array (found at arrayPath in the body)
// and emits one batch of ops per array element. Handles unbounded arrays
// transparently — the op buffer auto-flushes when full (via ctx.OnFlush).
//
// bodySlot: index of ByteSlot holding the JSON body bytes.
// arrayPath: gjson path to the array. Example: "items", "data.services"
// ops: ExtractOps applied to each array element (paths are relative to element).
func JSONForeachEmit(bodySlot int, arrayPath string, ops []ExtractOp) engine.Instruction {
	return engine.Instruction{
		Name: "json_foreach_emit",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			body := ctx.ByteSlots[bodySlot]
			if len(body) == 0 {
				return s.PC + 1
			}

			arr := gjson.GetBytes(body, arrayPath)
			if !arr.IsArray() {
				return s.PC + 1
			}

			arr.ForEach(func(_, element gjson.Result) bool {
				elemBytes := []byte(element.Raw)

				for _, op := range ops {
					result := gjson.GetBytes(elemBytes, op.Path)
					if !result.Exists() {
						continue
					}
					extracted := result.String()
					if extracted == "" {
						continue
					}

					keyLen := len(op.KeyPrefix) + len(extracted)
					keyBuf := ctx.AllocOpKey(keyLen)
					n := copy(keyBuf, op.KeyPrefix)
					copy(keyBuf[n:], extracted)
					key := keyBuf[:keyLen]

					switch op.OpType {
					case rctx.OpGet:
						rctx.EmitGet(ctx, key, op.Target, op.DestSlot)
					case rctx.OpPut:
						var val []byte
						if op.ValueSlot >= 0 {
							val = ctx.ByteSlots[op.ValueSlot]
						} else {
							val = []byte(extracted)
						}
						rctx.EmitPut(ctx, key, val, op.Target, op.Async, op.TTL)
					}
				}
				return true // continue iteration
			})

			return s.PC + 1
		},
	}
}

// JsonSetStep sets a value at a JSON path in a ByteSlot, using gjson to locate
// the field and hand-rolled splicing to replace it. If valueSlot is >= 0, reads
// the value from that slot; otherwise uses staticValue.
func JsonSetStep(srcSlot, dstSlot int, path string, staticValue []byte, valueSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "JSON_SET",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			src := ctx.ByteSlots[srcSlot]
			if len(src) == 0 {
				ctx.ByteSlots[dstSlot] = src
				return state.PC + 1
			}

			var val []byte
			if valueSlot >= 0 {
				val = ctx.ByteSlots[valueSlot]
			} else {
				val = staticValue
			}

			// Use gjson to find the field and get its byte range.
			r := gjson.GetBytes(src, path)
			if !r.Exists() {
				// Key not found: copy source unchanged.
				ctx.ByteSlots[dstSlot] = src
				return state.PC + 1
			}

			// Replace the found value with val.
			// r.Index is the byte offset into src where the value begins.
			// r.Raw is the matched text (as a string) — we need len(r.Raw) to find the end.
			startIdx := r.Index
			endIdx := r.Index + len(r.Raw)

			// Allocate output: prefix + val + suffix
			totalLen := startIdx + len(val) + (len(src) - endIdx)
			out := ctx.Alloc(totalLen)

			pos := 0
			pos += copy(out[pos:], src[:startIdx])
			pos += copy(out[pos:], val)
			copy(out[pos:], src[endIdx:])

			ctx.ByteSlots[dstSlot] = out
			return state.PC + 1
		},
	}
}
