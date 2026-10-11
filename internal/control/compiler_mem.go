package control

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/amitkhosla/rah/internal/datacache"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/engine/steps"
	"github.com/amitkhosla/rah/internal/rctx"
)

// parseMemFields reads field.* keys from step.Input and returns a FieldDef slice.
// Format: field.<name>=float64 | int64 | enum | string:<width>
func parseMemFields(input map[string]string) ([]datacache.FieldDef, error) {
	var fields []datacache.FieldDef
	for k, v := range input {
		if !strings.HasPrefix(k, "field.") {
			continue
		}
		name := k[6:]
		fd, err := parseOneMemField(name, v)
		if err != nil {
			return nil, err
		}
		fields = append(fields, fd)
	}
	return fields, nil
}

func parseOneMemField(name, spec string) (datacache.FieldDef, error) {
	switch {
	case spec == "float64":
		return datacache.FieldDef{Name: name, Kind: datacache.FieldFloat64, Width: 8}, nil
	case spec == "int64":
		return datacache.FieldDef{Name: name, Kind: datacache.FieldInt64, Width: 8}, nil
	case spec == "enum":
		return datacache.FieldDef{Name: name, Kind: datacache.FieldEnum, Width: 1}, nil
	case strings.HasPrefix(spec, "string:"):
		w, err := strconv.Atoi(spec[7:])
		if err != nil || w <= 0 {
			return datacache.FieldDef{}, fmt.Errorf("invalid string width in %q", spec)
		}
		return datacache.FieldDef{Name: name, Kind: datacache.FieldStringN, Width: w}, nil
	default:
		return datacache.FieldDef{}, fmt.Errorf("unknown field type %q (use float64, int64, enum, string:<n>)", spec)
	}
}

// memName packages bake-time collection name resolution: static string or slot index.
type memName struct {
	static  string
	slotIdx int // -1 means use static
}

func (c *Compiler) resolveMemName(input map[string]string) (memName, error) {
	if v := input["collection_slot"]; v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return memName{}, fmt.Errorf("collection_slot: %w", err)
		}
		return memName{slotIdx: s}, nil
	}
	if v := input["collection"]; v != "" {
		return memName{static: v, slotIdx: -1}, nil
	}
	return memName{}, fmt.Errorf("'collection' or 'collection_slot' is required")
}

func (mn memName) get(ctx *rctx.Context) string {
	if mn.slotIdx >= 0 {
		return string(ctx.ByteSlots[mn.slotIdx])
	}
	return mn.static
}

// memFail records a step error on ctx and returns engine.StopPlan.
func memFail(ctx *rctx.Context, msg string, err error) int16 {
	ctx.Failed = true
	errStr := err.Error()
	ctx.ErrorMsg = ctx.Alloc(len(errStr))
	copy(ctx.ErrorMsg, errStr)
	steps.RecordStepError(ctx, "mem_ops", msg, err)
	return engine.StopPlan
}

// compileMemCreate handles the "mem_create" step type.
//
// Inputs:
//
//	collection        — static collection name
//	collection_slot   — ByteSlot variable with collection name (overrides collection)
//	capacity          — initial record capacity (default 1024)
//	max_capacity      — max capacity for auto-growth; 0 = same as capacity
//	growth            — "auto" (default) or "fixed"
//	access            — "read_write" (default) or "read_only"
//	ttl_sec           — TTL in seconds; 0 = no expiry
//	field.<name>      — field definitions: float64, int64, enum, string:<width>
func (c *Compiler) compileMemCreate(step StepConfig) error {
	if c.DataCacheMgr == nil {
		return fmt.Errorf("mem_create requires DataCache to be configured")
	}
	mn, err := c.resolveMemName(step.Input)
	if err != nil {
		return fmt.Errorf("mem_create: %w", err)
	}
	fields, err := parseMemFields(step.Input)
	if err != nil {
		return fmt.Errorf("mem_create: %w", err)
	}

	capacity := 1024
	if v := step.Input["capacity"]; v != "" {
		if capacity, err = strconv.Atoi(v); err != nil {
			return fmt.Errorf("mem_create capacity: %w", err)
		}
	}
	maxCap := capacity
	if v := step.Input["max_capacity"]; v != "" && v != "0" {
		if maxCap, err = strconv.Atoi(v); err != nil {
			return fmt.Errorf("mem_create max_capacity: %w", err)
		}
	}
	growth := datacache.GrowthAuto
	if step.Input["growth"] == "fixed" {
		growth = datacache.GrowthFixed
	}
	access := datacache.AccessReadWrite
	if step.Input["access"] == "read_only" {
		access = datacache.AccessReadOnly
	}
	ttlSec := 0
	if v := step.Input["ttl_sec"]; v != "" {
		ttlSec, _ = strconv.Atoi(v)
	}

	app := step.Input["app"] // optional app name for quota tracking and ownership

	mgr := c.DataCacheMgr
	opts := datacache.CreateOpts{
		Fields:   fields,
		Capacity: capacity,
		MaxCap:   maxCap,
		Access:   access,
		Growth:   growth,
		TTLSec:   ttlSec,
	}

	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "MEM_CREATE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			o := opts
			o.TenantID = ctx.TenantID
			o.App = app
			if err := mgr.Create(mn.get(ctx), o); err != nil {
				return memFail(ctx, "mem_create failed", err)
			}
			return state.PC + 1
		},
	})
	return nil
}

// compileMemDrop handles the "mem_drop" step type.
func (c *Compiler) compileMemDrop(step StepConfig) error {
	if c.DataCacheMgr == nil {
		return fmt.Errorf("mem_drop requires DataCache to be configured")
	}
	mn, err := c.resolveMemName(step.Input)
	if err != nil {
		return fmt.Errorf("mem_drop: %w", err)
	}
	app := step.Input["app"] // optional; must match the app used during Create
	mgr := c.DataCacheMgr
	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "MEM_DROP",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if err := mgr.Drop(mn.get(ctx), ctx.TenantID, app); err != nil {
				return memFail(ctx, "mem_drop failed", err)
			}
			return state.PC + 1
		},
	})
	return nil
}

// compileMemLoad handles the "mem_load" step type.
//
// Inputs:
//
//	collection / collection_slot — collection name
//	data_slot                    — ByteSlot variable holding a JSON array of objects
func (c *Compiler) compileMemLoad(step StepConfig) error {
	if c.DataCacheMgr == nil {
		return fmt.Errorf("mem_load requires DataCache to be configured")
	}
	mn, err := c.resolveMemName(step.Input)
	if err != nil {
		return fmt.Errorf("mem_load: %w", err)
	}
	dataSlotVar := step.Input["data_slot"]
	if dataSlotVar == "" {
		return fmt.Errorf("mem_load: 'data_slot' is required")
	}
	dataSlot, err := c.getSlot(dataSlotVar)
	if err != nil {
		return fmt.Errorf("mem_load data_slot: %w", err)
	}
	mgr := c.DataCacheMgr
	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "MEM_LOAD",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			name := mn.get(ctx)
			jsonArray := ctx.ByteSlots[dataSlot]
			if err := mgr.AppendJSONBatch(name, jsonArray); err != nil {
				errStr := "mem_load: " + err.Error()
				ctx.Failed = true
				b := ctx.Alloc(len(errStr))
				copy(b, errStr)
				ctx.ErrorMsg = b
				steps.RecordStepError(ctx, "mem_load", errStr, err)
				return engine.StopPlan
			}
			return state.PC + 1
		},
	})
	return nil
}

// compileMemAppend handles the "mem_append" step type.
//
// Inputs:
//
//	collection / collection_slot — collection name
//	field.<name>                 — slot variable for each field value (e.g. field.price=var.price)
func (c *Compiler) compileMemAppend(step StepConfig) error {
	if c.DataCacheMgr == nil {
		return fmt.Errorf("mem_append requires DataCache to be configured")
	}
	mn, err := c.resolveMemName(step.Input)
	if err != nil {
		return fmt.Errorf("mem_append: %w", err)
	}
	// Collect field name → slot mappings
	type fieldSlot struct {
		name string
		slot int
	}
	var fieldSlots []fieldSlot
	for k, v := range step.Input {
		if !strings.HasPrefix(k, "field.") {
			continue
		}
		fieldName := k[6:]
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("mem_append field.%s: %w", fieldName, err)
		}
		fieldSlots = append(fieldSlots, fieldSlot{name: fieldName, slot: s})
	}
	mgr := c.DataCacheMgr
	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "MEM_APPEND",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			name := mn.get(ctx)
			// Build JSON object from field slots and append via AppendJSON.
			// Pre-allocate a small buffer.
			buf := make([]byte, 0, 128)
			buf = append(buf, '{')
			for i, fs := range fieldSlots {
				if i > 0 {
					buf = append(buf, ',')
				}
				buf = append(buf, '"')
				buf = append(buf, fs.name...)
				buf = append(buf, '"', ':')
				v := ctx.ByteSlots[fs.slot]
				if len(v) == 0 {
					buf = append(buf, "null"...)
				} else {
					buf = append(buf, v...)
				}
			}
			buf = append(buf, '}')
			if err := mgr.AppendJSON(name, buf); err != nil {
				return memFail(ctx, "mem_append failed", err)
			}
			return state.PC + 1
		},
	})
	return nil
}

// compileMemQuery handles the "mem_query" step type.
//
// Inputs:
//
//	collection / collection_slot — collection name
//	output_slot                  — ByteSlot variable to write JSON array into
func (c *Compiler) compileMemQuery(step StepConfig) error {
	if c.DataCacheMgr == nil {
		return fmt.Errorf("mem_query requires DataCache to be configured")
	}
	mn, err := c.resolveMemName(step.Input)
	if err != nil {
		return fmt.Errorf("mem_query: %w", err)
	}
	outVar := step.Input["output_slot"]
	if outVar == "" {
		return fmt.Errorf("mem_query: 'output_slot' is required")
	}
	outSlot, err := c.getSlot(outVar)
	if err != nil {
		return fmt.Errorf("mem_query output_slot: %w", err)
	}
	mgr := c.DataCacheMgr
	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "MEM_QUERY",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			result, err := mgr.QueryAll(mn.get(ctx), ctx.Alloc)
			if err != nil {
				return memFail(ctx, "mem_query failed", err)
			}
			ctx.ByteSlots[outSlot] = result
			return state.PC + 1
		},
	})
	return nil
}

// compileMemFilter handles the "mem_filter" step type.
//
// Inputs:
//
//	collection / collection_slot — collection name
//	field                        — field name to filter on
//	op                           — eq, ne, lt, gt, lte, gte
//	value                        — static comparison value
//	value_slot                   — ByteSlot variable (overrides value)
//	output_slot                  — ByteSlot variable for filtered JSON array
func (c *Compiler) compileMemFilter(step StepConfig) error {
	if c.DataCacheMgr == nil {
		return fmt.Errorf("mem_filter requires DataCache to be configured")
	}
	mn, err := c.resolveMemName(step.Input)
	if err != nil {
		return fmt.Errorf("mem_filter: %w", err)
	}
	fieldName := step.Input["field"]
	if fieldName == "" {
		return fmt.Errorf("mem_filter: 'field' is required")
	}
	op := step.Input["op"]
	if op == "" {
		op = "eq"
	}
	outVar := step.Input["output_slot"]
	if outVar == "" {
		return fmt.Errorf("mem_filter: 'output_slot' is required")
	}
	outSlot, err := c.getSlot(outVar)
	if err != nil {
		return fmt.Errorf("mem_filter output_slot: %w", err)
	}
	staticVal := step.Input["value"]
	valueSlot := -1
	if v := step.Input["value_slot"]; v != "" {
		valueSlot, err = c.getSlot(v)
		if err != nil {
			return fmt.Errorf("mem_filter value_slot: %w", err)
		}
	}
	mgr := c.DataCacheMgr
	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "MEM_FILTER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			cmp := staticVal
			if valueSlot >= 0 {
				cmp = string(ctx.ByteSlots[valueSlot])
			}
			result, err := mgr.FilterJSON(mn.get(ctx), fieldName, op, cmp, ctx.Alloc)
			if err != nil {
				return memFail(ctx, "mem_filter failed", err)
			}
			ctx.ByteSlots[outSlot] = result
			return state.PC + 1
		},
	})
	return nil
}

// compileMemSort handles the "mem_sort" step type.
//
// Inputs:
//
//	collection / collection_slot — collection name
//	field                        — static field name to sort by
//	field_slot                   — ByteSlot variable with field name (overrides field)
//	desc                         — "true" for descending order
func (c *Compiler) compileMemSort(step StepConfig) error {
	if c.DataCacheMgr == nil {
		return fmt.Errorf("mem_sort requires DataCache to be configured")
	}
	mn, err := c.resolveMemName(step.Input)
	if err != nil {
		return fmt.Errorf("mem_sort: %w", err)
	}
	staticField := step.Input["field"]
	fieldSlot := -1
	if v := step.Input["field_slot"]; v != "" {
		fieldSlot, err = c.getSlot(v)
		if err != nil {
			return fmt.Errorf("mem_sort field_slot: %w", err)
		}
	}
	if staticField == "" && fieldSlot < 0 {
		return fmt.Errorf("mem_sort: 'field' or 'field_slot' is required")
	}
	desc := step.Input["desc"] == "true"
	mgr := c.DataCacheMgr
	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "MEM_SORT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			field := staticField
			if fieldSlot >= 0 {
				field = string(ctx.ByteSlots[fieldSlot])
			}
			if err := mgr.SortCollection(mn.get(ctx), field, desc); err != nil {
				return memFail(ctx, "mem_sort failed", err)
			}
			return state.PC + 1
		},
	})
	return nil
}

// compileMemAggregate handles the "mem_aggregate" step type.
//
// Inputs:
//
//	collection / collection_slot — collection name
//	field                        — field name to aggregate
//	op                           — sum, avg, min, max, count
//	output_slot                  — ByteSlot variable for result string
func (c *Compiler) compileMemAggregate(step StepConfig) error {
	if c.DataCacheMgr == nil {
		return fmt.Errorf("mem_aggregate requires DataCache to be configured")
	}
	mn, err := c.resolveMemName(step.Input)
	if err != nil {
		return fmt.Errorf("mem_aggregate: %w", err)
	}
	fieldName := step.Input["field"]
	op := step.Input["op"]
	if op == "" {
		op = "avg"
	}
	outVar := step.Input["output_slot"]
	if outVar == "" {
		return fmt.Errorf("mem_aggregate: 'output_slot' is required")
	}
	outSlot, err := c.getSlot(outVar)
	if err != nil {
		return fmt.Errorf("mem_aggregate output_slot: %w", err)
	}
	mgr := c.DataCacheMgr
	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "MEM_AGGREGATE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			val, err := mgr.AggregateField(mn.get(ctx), fieldName, op)
			if err != nil {
				return memFail(ctx, "mem_aggregate failed", err)
			}
			s := strconv.FormatFloat(val, 'f', -1, 64)
			out := ctx.Alloc(len(s))
			copy(out, s)
			ctx.ByteSlots[outSlot] = out
			return state.PC + 1
		},
	})
	return nil
}

// compileMemTrimOldest handles the "mem_trim_oldest" step type.
//
// Inputs:
//
//	collection / collection_slot — collection name
//	n                            — static count of records to remove
//	n_slot                       — IntSlot variable (overrides n)
func (c *Compiler) compileMemTrimOldest(step StepConfig) error {
	if c.DataCacheMgr == nil {
		return fmt.Errorf("mem_trim_oldest requires DataCache to be configured")
	}
	mn, err := c.resolveMemName(step.Input)
	if err != nil {
		return fmt.Errorf("mem_trim_oldest: %w", err)
	}
	staticN := 100
	if v := step.Input["n"]; v != "" {
		if staticN, err = strconv.Atoi(v); err != nil {
			return fmt.Errorf("mem_trim_oldest n: %w", err)
		}
	}
	nSlot := -1
	if v := step.Input["n_slot"]; v != "" {
		nSlot, err = c.getIntSlot(v)
		if err != nil {
			return fmt.Errorf("mem_trim_oldest n_slot: %w", err)
		}
	}
	mgr := c.DataCacheMgr
	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "MEM_TRIM_OLDEST",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			n := staticN
			if nSlot >= 0 {
				n = int(ctx.GetInt(nSlot))
			}
			if err := mgr.TrimOldest(mn.get(ctx), n); err != nil {
				return memFail(ctx, "mem_trim_oldest failed", err)
			}
			return state.PC + 1
		},
	})
	return nil
}

// compileEvalExpr handles the "eval_expr" step type.
//
// Inputs:
//
//	expression       — float expression string (e.g. "price * (1 - discount / 100)")
//	output_slot      — ByteSlot variable for float string result
//	output_int_slot  — IntSlot variable for fixed-point result (×1000)
//	var.<name>       — map expression variable to slot: var.price=var.price_slot
func (c *Compiler) compileEvalExpr(step StepConfig) error {
	expr := step.Input["expression"]
	if expr == "" {
		return fmt.Errorf("eval_expr: 'expression' is required")
	}
	outVar := step.Input["output_slot"]
	if outVar == "" {
		return fmt.Errorf("eval_expr: 'output_slot' is required")
	}
	outSlot, err := c.getSlot(outVar)
	if err != nil {
		return fmt.Errorf("eval_expr output_slot: %w", err)
	}
	outIntSlot := -1
	if v := step.Input["output_int_slot"]; v != "" {
		outIntSlot, err = c.getIntSlot(v)
		if err != nil {
			return fmt.Errorf("eval_expr output_int_slot: %w", err)
		}
	}
	var varNames []string
	var varSlots []int
	for k, v := range step.Input {
		if !strings.HasPrefix(k, "var.") {
			continue
		}
		varName := k[4:]
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("eval_expr var.%s: %w", varName, err)
		}
		varNames = append(varNames, varName)
		varSlots = append(varSlots, s)
	}
	instr, err := steps.NewEvalExprStep(expr, varNames, varSlots, outSlot, outIntSlot)
	if err != nil {
		return fmt.Errorf("eval_expr compile: %w", err)
	}
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileRedisExec handles the "redis_exec" step type.
//
// Inputs:
//
//	connector    — named Redis connector (from data_sources)
//	command      — Redis command (e.g. HSET, ZADD)
//	args.<n>     — command arguments: static:<value> or slot:<var_name>
//	output_slot  — ByteSlot variable for command result
//	timeout_ms   — command timeout (default 5000)
//	fail_on_error — "false" to write error string to output and continue (default true)
func (c *Compiler) compileRedisExec(step StepConfig) error {
	if c.RedisSourcePool == nil {
		return fmt.Errorf("redis_exec requires a Redis connector to be configured")
	}
	connName := step.Input["connector"]
	if connName == "" {
		return fmt.Errorf("redis_exec: 'connector' is required")
	}
	client, ok := c.RedisSourcePool.GetUniversalClient(connName)
	if !ok {
		return fmt.Errorf("redis_exec: connector %q not found", connName)
	}
	command := step.Input["command"]
	if command == "" {
		return fmt.Errorf("redis_exec: 'command' is required")
	}
	// Build ordered argument list from args.0, args.1, ...
	var argKinds []uint8
	var staticArgs [][]byte
	var slotArgs []int
	for i := 0; ; i++ {
		v := step.Input[fmt.Sprintf("args.%d", i)]
		if v == "" {
			break
		}
		if strings.HasPrefix(v, "static:") {
			argKinds = append(argKinds, 0)
			staticArgs = append(staticArgs, []byte(v[7:]))
		} else if strings.HasPrefix(v, "slot:") {
			s, err := c.getSlot(v[5:])
			if err != nil {
				return fmt.Errorf("redis_exec args.%d: %w", i, err)
			}
			argKinds = append(argKinds, 1)
			slotArgs = append(slotArgs, s)
		} else {
			return fmt.Errorf("redis_exec args.%d: must be prefixed with 'static:' or 'slot:'", i)
		}
	}
	outSlot := -1
	if v := step.Input["output_slot"]; v != "" {
		var err error
		outSlot, err = c.getSlot(v)
		if err != nil {
			return fmt.Errorf("redis_exec output_slot: %w", err)
		}
	}
	timeoutMs := 5000
	if v := step.Input["timeout_ms"]; v != "" {
		timeoutMs, _ = strconv.Atoi(v)
	}
	failOnError := step.Input["fail_on_error"] != "false"
	c.GlobalTable = append(c.GlobalTable, steps.NewRedisExecStep(command, argKinds, staticArgs, slotArgs, outSlot, timeoutMs, failOnError, client))
	return nil
}

// compileArraySort handles the "array_sort" step type.
//
// Inputs:
//
//	input_slot  — ByteSlot variable holding JSON array
//	output_slot — ByteSlot variable for sorted JSON array
//	field       — field name to sort by
//	desc        — "true" for descending order
func (c *Compiler) compileArraySort(step StepConfig) error {
	inVar := step.Input["input_slot"]
	if inVar == "" {
		return fmt.Errorf("array_sort: 'input_slot' is required")
	}
	inSlot, err := c.getSlot(inVar)
	if err != nil {
		return fmt.Errorf("array_sort input_slot: %w", err)
	}
	outVar := step.Input["output_slot"]
	if outVar == "" {
		return fmt.Errorf("array_sort: 'output_slot' is required")
	}
	outSlot, err := c.getSlot(outVar)
	if err != nil {
		return fmt.Errorf("array_sort output_slot: %w", err)
	}
	field := step.Input["field"]
	desc := step.Input["desc"] == "true"
	c.GlobalTable = append(c.GlobalTable, steps.ArraySortStep(inSlot, outSlot, field, desc))
	return nil
}

// getIntSlot is a helper that resolves an IntSlot variable name to an index.
// It delegates to the compiler's existing getBoolSlot-style resolution.
func (c *Compiler) getIntSlot(name string) (int, error) {
	// IntSlot variables are resolved the same way as ByteSlots in this compiler.
	// The slot index is the same; the type (int vs byte) is determined by step usage.
	return c.getSlot(name)
}
