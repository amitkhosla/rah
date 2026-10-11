package steps

import (
	"encoding/json"
	"strconv"
	"sync"

	"github.com/amitkhosla/rah/internal/datacache"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// valuesPool is a sync.Pool of []interface{} scratch slices for MemAppendStep.
// Pre-allocated with cap 64 to avoid heap pressure.
var valuesPool = sync.Pool{
	New: func() any {
		s := make([]interface{}, 0, 64)
		return &s
	},
}

// appFromCtx extracts the app name from the context.
// Currently returns empty string; Phase 2 will set app identity via flow constants.
func appFromCtx(ctx *rctx.Context) string {
	return ""
}

// MemCreateStep creates a new named collection in the DataCache.
// Reads collection name from ByteSlots[nameSlot].
// Capacity: if capacitySlot >= 0, read from IntSlots[capacitySlot]; else use staticCapacity.
func MemCreateStep(mgr *datacache.DataCacheManager, nameSlot int, fields []datacache.FieldDef, capacitySlot int, staticCapacity int, access datacache.AccessMode, growth datacache.GrowthMode, ttlSec int) engine.Instruction {
	return engine.Instruction{
		Name: "MEM_CREATE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if nameSlot < 0 || nameSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_create: nameSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			name := string(ctx.ByteSlots[nameSlot])

			// Determine capacity
			capacity := staticCapacity
			if capacitySlot >= 0 {
				if capacitySlot < 0 || capacitySlot >= len(ctx.IntSlots) {
					ctx.Failed = true
					msg := "mem_create: capacitySlot out of range"
					b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
					return engine.StopPlan
				}
				capacity = int(ctx.IntSlots[capacitySlot])
			}

			// Call Create
			err := mgr.Create(name, datacache.CreateOpts{
				TenantID: ctx.TenantID,
				App:      appFromCtx(ctx),
				Fields:   fields,
				Capacity: capacity,
				Access:   access,
				Growth:   growth,
				TTLSec:   ttlSec,
			})

			if err != nil {
				ctx.Failed = true
				errStr := err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(errStr))
				copy(ctx.ErrorMsg, errStr)
				RecordStepError(ctx, "mem_ops", "MEM_CREATE failed", err)
				return engine.StopPlan
			}

			return state.PC + 1
		},
	}
}

// MemDropStep drops (deletes) a named collection.
// Reads collection name from ByteSlots[nameSlot].
func MemDropStep(mgr *datacache.DataCacheManager, nameSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "MEM_DROP",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if nameSlot < 0 || nameSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_drop: nameSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			name := string(ctx.ByteSlots[nameSlot])

			err := mgr.Drop(name, ctx.TenantID, appFromCtx(ctx))
			if err != nil {
				ctx.Failed = true
				errStr := err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(errStr))
				copy(ctx.ErrorMsg, errStr)
				RecordStepError(ctx, "mem_ops", "MEM_DROP failed", err)
				return engine.StopPlan
			}

			return state.PC + 1
		},
	}
}

// MemLoadStep loads a JSON array of objects into a collection.
// Reads collection name from ByteSlots[nameSlot].
// Reads JSON array from ByteSlots[jsonArraySlot] and appends each object as a record.
func MemLoadStep(mgr *datacache.DataCacheManager, nameSlot int, jsonArraySlot int) engine.Instruction {
	return engine.Instruction{
		Name: "MEM_LOAD",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if nameSlot < 0 || nameSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_load: nameSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			if jsonArraySlot < 0 || jsonArraySlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_load: jsonArraySlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			name := string(ctx.ByteSlots[nameSlot])
			jsonArray := ctx.ByteSlots[jsonArraySlot]

			// Parse JSON array
			var objs []interface{}
			if err := json.Unmarshal(jsonArray, &objs); err != nil {
				ctx.Failed = true
				errStr := err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(errStr))
				copy(ctx.ErrorMsg, errStr)
				RecordStepError(ctx, "mem_ops", "MEM_LOAD JSON parse failed", err)
				return engine.StopPlan
			}

			// Append each object
			for _, obj := range objs {
				objBytes, err := json.Marshal(obj)
				if err != nil {
					ctx.Failed = true
					errStr := err.Error()
					ctx.ErrorMsg = ctx.Alloc(len(errStr))
					copy(ctx.ErrorMsg, errStr)
					RecordStepError(ctx, "mem_ops", "MEM_LOAD marshal failed", err)
					return engine.StopPlan
				}

				err = mgr.AppendJSON(name, objBytes)
				if err != nil {
					ctx.Failed = true
					errStr := err.Error()
					ctx.ErrorMsg = ctx.Alloc(len(errStr))
					copy(ctx.ErrorMsg, errStr)
					RecordStepError(ctx, "mem_ops", "MEM_LOAD append failed", err)
					return engine.StopPlan
				}
			}

			return state.PC + 1
		},
	}
}

// MemAppendStep appends a single record to a collection.
// Reads collection name from ByteSlots[nameSlot].
// Reads values from ByteSlots[valueSlots] and appends them as a record.
func MemAppendStep(mgr *datacache.DataCacheManager, nameSlot int, valueSlots []int) engine.Instruction {
	return engine.Instruction{
		Name: "MEM_APPEND",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if nameSlot < 0 || nameSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_append: nameSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			name := string(ctx.ByteSlots[nameSlot])

			// Borrow scratch slice from pool
			valsp := valuesPool.Get().(*[]interface{})
			vals := (*valsp)[:0]

			// Build values from ByteSlots — all arrive as byte strings, arena.Append handles coercion
			for _, slot := range valueSlots {
				if slot < 0 || slot >= len(ctx.ByteSlots) {
					ctx.Failed = true
					msg := "mem_append: slot out of range"
					b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
					return engine.StopPlan
				}
				vals = append(vals, ctx.ByteSlots[slot])
			}

			err := mgr.AppendVals(name, vals)

			// Return to pool
			*valsp = vals
			valuesPool.Put(valsp)

			if err != nil {
				ctx.Failed = true
				errStr := err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(errStr))
				copy(ctx.ErrorMsg, errStr)
				RecordStepError(ctx, "mem_ops", "MEM_APPEND failed", err)
				return engine.StopPlan
			}

			return state.PC + 1
		},
	}
}

// MemQueryStep queries all records from a collection and serializes them to JSON.
// Reads collection name from ByteSlots[nameSlot].
// Writes JSON array to ByteSlots[outputSlot].
func MemQueryStep(mgr *datacache.DataCacheManager, nameSlot int, outputSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "MEM_QUERY",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if nameSlot < 0 || nameSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_query: nameSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			if outputSlot < 0 || outputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_query: outputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			name := string(ctx.ByteSlots[nameSlot])

			result, err := mgr.QueryAll(name, ctx.Alloc)
			if err != nil {
				ctx.Failed = true
				errStr := err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(errStr))
				copy(ctx.ErrorMsg, errStr)
				RecordStepError(ctx, "mem_ops", "MEM_QUERY failed", err)
				return engine.StopPlan
			}

			ctx.ByteSlots[outputSlot] = result
			return state.PC + 1
		},
	}
}

// MemFilterStep filters records by a field condition and serializes matching records to JSON.
// Reads collection name from ByteSlots[nameSlot].
// Supported ops: "eq", "ne", "lt", "gt", "lte", "gte".
// Writes filtered JSON array to ByteSlots[outputSlot].
func MemFilterStep(mgr *datacache.DataCacheManager, nameSlot int, outputSlot int, fieldName string, op string, valueSlot int, staticValue string) engine.Instruction {
	return engine.Instruction{
		Name: "MEM_FILTER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if nameSlot < 0 || nameSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_filter: nameSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			if outputSlot < 0 || outputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_filter: outputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			name := string(ctx.ByteSlots[nameSlot])

			// Determine filter value
			compareVal := staticValue
			if valueSlot >= 0 {
				if valueSlot < 0 || valueSlot >= len(ctx.ByteSlots) {
					ctx.Failed = true
					msg := "mem_filter: valueSlot out of range"
					b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
					return engine.StopPlan
				}
				compareVal = string(ctx.ByteSlots[valueSlot])
			}

			result, err := mgr.FilterJSON(name, fieldName, op, compareVal, ctx.Alloc)
			if err != nil {
				ctx.Failed = true
				errStr := err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(errStr))
				copy(ctx.ErrorMsg, errStr)
				RecordStepError(ctx, "mem_ops", "MEM_FILTER failed", err)
				return engine.StopPlan
			}

			ctx.ByteSlots[outputSlot] = result
			return state.PC + 1
		},
	}
}

// MemSortStep sorts a collection by a field.
// Reads collection name from ByteSlots[nameSlot].
// Reads field name from ByteSlots[fieldNameSlot] if >= 0, else uses staticFieldName.
func MemSortStep(mgr *datacache.DataCacheManager, nameSlot int, fieldNameSlot int, staticFieldName string, descending bool) engine.Instruction {
	return engine.Instruction{
		Name: "MEM_SORT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if nameSlot < 0 || nameSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_sort: nameSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			name := string(ctx.ByteSlots[nameSlot])

			// Determine field name
			fieldName := staticFieldName
			if fieldNameSlot >= 0 {
				if fieldNameSlot < 0 || fieldNameSlot >= len(ctx.ByteSlots) {
					ctx.Failed = true
					msg := "mem_sort: fieldNameSlot out of range"
					b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
					return engine.StopPlan
				}
				fieldName = string(ctx.ByteSlots[fieldNameSlot])
			}

			err := mgr.SortCollection(name, fieldName, descending)
			if err != nil {
				ctx.Failed = true
				errStr := err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(errStr))
				copy(ctx.ErrorMsg, errStr)
				RecordStepError(ctx, "mem_ops", "MEM_SORT failed", err)
				return engine.StopPlan
			}

			return state.PC + 1
		},
	}
}

// MemAggregateStep computes an aggregate (sum, avg, min, max, count) over a field.
// Reads collection name from ByteSlots[nameSlot].
// Supported ops: "sum", "avg", "min", "max", "count".
// Writes result as a string to ByteSlots[outputSlot].
func MemAggregateStep(mgr *datacache.DataCacheManager, nameSlot int, outputSlot int, fieldName string, op string) engine.Instruction {
	return engine.Instruction{
		Name: "MEM_AGGREGATE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if nameSlot < 0 || nameSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_aggregate: nameSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			if outputSlot < 0 || outputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_aggregate: outputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			name := string(ctx.ByteSlots[nameSlot])

			result, err := mgr.AggregateField(name, fieldName, op)
			if err != nil {
				ctx.Failed = true
				errStr := err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(errStr))
				copy(ctx.ErrorMsg, errStr)
				RecordStepError(ctx, "mem_ops", "MEM_AGGREGATE failed", err)
				return engine.StopPlan
			}

			// Convert result to string
			resultStr := strconv.FormatFloat(result, 'f', -1, 64)
			output := ctx.Alloc(len(resultStr))
			copy(output, resultStr)
			ctx.ByteSlots[outputSlot] = output

			return state.PC + 1
		},
	}
}

// MemTrimOldestStep removes (tombstones) the oldest N records from a collection.
// Reads collection name from ByteSlots[nameSlot].
// Reads N from IntSlots[nSlot] if >= 0, else uses staticN.
func MemTrimOldestStep(mgr *datacache.DataCacheManager, nameSlot int, nSlot int, staticN int) engine.Instruction {
	return engine.Instruction{
		Name: "MEM_TRIM_OLDEST",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if nameSlot < 0 || nameSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "mem_trim_oldest: nameSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			name := string(ctx.ByteSlots[nameSlot])

			// Determine N
			n := staticN
			if nSlot >= 0 {
				if nSlot < 0 || nSlot >= len(ctx.IntSlots) {
					ctx.Failed = true
					msg := "mem_trim_oldest: nSlot out of range"
					b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
					return engine.StopPlan
				}
				n = int(ctx.IntSlots[nSlot])
			}

			err := mgr.TrimOldest(name, n)
			if err != nil {
				ctx.Failed = true
				errStr := err.Error()
				ctx.ErrorMsg = ctx.Alloc(len(errStr))
				copy(ctx.ErrorMsg, errStr)
				RecordStepError(ctx, "mem_ops", "MEM_TRIM_OLDEST failed", err)
				return engine.StopPlan
			}

			return state.PC + 1
		},
	}
}
