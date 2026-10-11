package steps

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// NOTE: array_ops parse JSON on each call. Use for batch/background flows, not sub-ms REST hot path.

// jsonArrayPool is a shared pool for []map[string]interface{} slices used by all array operations.
var jsonArrayPool = sync.Pool{New: func() any {
	s := make([]map[string]interface{}, 0, 256)
	return &s
}}

// parseJSONArray unmarshals a JSON byte slice into a pooled []map[string]interface{}.
func parseJSONArray(data []byte) ([]map[string]interface{}, error) {
	var arr []map[string]interface{}
	err := json.Unmarshal(data, &arr)
	return arr, err
}

// marshalJSONArray marshals a []map[string]interface{} into JSON bytes.
func marshalJSONArray(arr []map[string]interface{}) ([]byte, error) {
	return json.Marshal(arr)
}

// getFloat retrieves a numeric field from a map, converting int/float64/json.Number to float64.
func getFloat(m map[string]interface{}, field string) (float64, bool) {
	v, ok := m[field]
	if !ok {
		return 0, false
	}
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case json.Number:
		f, err := val.Float64()
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

// ArraySortStep sorts a JSON array by a numeric field, with optional descending order.
// Parses input array → sorts by field → marshals → writes to output via ctx.Alloc
func ArraySortStep(inputSlot, outputSlot int, field string, descending bool) engine.Instruction {
	return engine.Instruction{
		Name: "ARRAY_SORT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if inputSlot < 0 || inputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_sort: inputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			if outputSlot < 0 || outputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_sort: outputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			raw := ctx.ByteSlots[inputSlot]
			if len(raw) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			arr, err := parseJSONArray(raw)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			if len(arr) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			sort.Slice(arr, func(i, j int) bool {
				vi, okI := getFloat(arr[i], field)
				vj, okJ := getFloat(arr[j], field)
				if !okI || !okJ {
					return false
				}
				if descending {
					return vi > vj
				}
				return vi < vj
			})

			marshaled, err := marshalJSONArray(arr)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			out := ctx.Alloc(len(marshaled))
			copy(out, marshaled)
			ctx.ByteSlots[outputSlot] = out
			return state.PC + 1
		},
	}
}

// FilterConditionFunc is a compiled filter predicate that evaluates an element.
type FilterConditionFunc func(m map[string]interface{}) bool

// ArrayFilterStep filters a JSON array using a compiled condition function.
// Parses input → filters using condFn → marshals → writes to output via ctx.Alloc
func ArrayFilterStep(inputSlot, outputSlot int, condFn FilterConditionFunc) engine.Instruction {
	return engine.Instruction{
		Name: "ARRAY_FILTER",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if inputSlot < 0 || inputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_filter: inputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			if outputSlot < 0 || outputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_filter: outputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			raw := ctx.ByteSlots[inputSlot]
			if len(raw) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			arr, err := parseJSONArray(raw)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			filtered := make([]map[string]interface{}, 0, len(arr))
			for _, elem := range arr {
				if condFn(elem) {
					filtered = append(filtered, elem)
				}
			}

			marshaled, err := marshalJSONArray(filtered)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			out := ctx.Alloc(len(marshaled))
			copy(out, marshaled)
			ctx.ByteSlots[outputSlot] = out
			return state.PC + 1
		},
	}
}

// CompileArrayFilterCondition parses a simple filter expression into a FilterConditionFunc.
// Supports "field op value" where op is one of: ==, !=, >, <, >=, <=
// Example: "score > 50", "status == active"
func CompileArrayFilterCondition(expr string) (FilterConditionFunc, error) {
	// Simple implementation: parse "field op value"
	// For complex conditions, CompileCondition from cond_expr.go could be adapted
	// by wrapping its ConditionFunc to operate on a single element map.
	// For now, we provide a basic parser that handles common cases.

	// Tokenize into field, op, value
	var field, op, value string
	var foundOp bool

	// Quick scan for operator
	for i := 0; i < len(expr); i++ {
		if i+1 < len(expr) {
			twoChar := expr[i : i+2]
			if twoChar == "==" || twoChar == "!=" || twoChar == "<=" || twoChar == ">=" {
				field = expr[:i]
				op = twoChar
				value = expr[i+2:]
				foundOp = true
				break
			}
		}
		if expr[i] == '>' || expr[i] == '<' || expr[i] == '=' {
			field = expr[:i]
			op = string(expr[i])
			value = expr[i+1:]
			foundOp = true
			break
		}
	}

	if !foundOp {
		// No operator found; treat as existence check
		field = expr
		return func(m map[string]interface{}) bool {
			_, ok := m[field]
			return ok
		}, nil
	}

	field = trimSpace(field)
	value = trimSpace(value)

	// Try to parse value as number or keep as string
	if numVal, err := parseFloat(value); err == nil {
		// Numeric comparison
		switch op {
		case "==":
			return func(m map[string]interface{}) bool {
				v, ok := getFloat(m, field)
				return ok && v == numVal
			}, nil
		case "!=":
			return func(m map[string]interface{}) bool {
				v, ok := getFloat(m, field)
				return !ok || v != numVal
			}, nil
		case ">":
			return func(m map[string]interface{}) bool {
				v, ok := getFloat(m, field)
				return ok && v > numVal
			}, nil
		case "<":
			return func(m map[string]interface{}) bool {
				v, ok := getFloat(m, field)
				return ok && v < numVal
			}, nil
		case ">=":
			return func(m map[string]interface{}) bool {
				v, ok := getFloat(m, field)
				return ok && v >= numVal
			}, nil
		case "<=":
			return func(m map[string]interface{}) bool {
				v, ok := getFloat(m, field)
				return ok && v <= numVal
			}, nil
		}
	}

	// String comparison
	switch op {
	case "==":
		return func(m map[string]interface{}) bool {
			if str, ok := m[field].(string); ok {
				return str == value
			}
			return false
		}, nil
	case "!=":
		return func(m map[string]interface{}) bool {
			if str, ok := m[field].(string); ok {
				return str != value
			}
			return true
		}, nil
	default:
		// Numeric comparison operators not valid for strings
		return func(_ map[string]interface{}) bool { return false }, nil
	}
}

// helper: parseFloat attempts to parse a string as float64
func parseFloat(s string) (float64, error) {
	trimmed := trimSpace(s)
	var f float64
	_, err := parseNum(trimmed)
	if err == nil {
		f = parseNumUnsafe(trimmed)
		return f, nil
	}
	return 0, err
}

// helper: trimSpace removes leading/trailing whitespace
func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// helper: parseNum and parseNumUnsafe do simple numeric parsing
func parseNum(s string) (float64, error) {
	var result float64
	err := json.Unmarshal([]byte(s), &result)
	return result, err
}

func parseNumUnsafe(s string) float64 {
	result, _ := parseNum(s)
	return result
}

// ArraySliceStep slices an array by offset and limit.
// Offset and limit can come from slots or be static.
func ArraySliceStep(inputSlot, outputSlot, offsetSlot, limitSlot int, staticOffset, staticLimit int) engine.Instruction {
	return engine.Instruction{
		Name: "ARRAY_SLICE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if inputSlot < 0 || inputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_slice: inputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			if outputSlot < 0 || outputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_slice: outputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			raw := ctx.ByteSlots[inputSlot]
			if len(raw) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			arr, err := parseJSONArray(raw)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			// Determine offset
			offset := staticOffset
			if offsetSlot >= 0 && offsetSlot < len(ctx.IntSlots) {
				offset = int(ctx.IntSlots[offsetSlot])
			}
			if offset < 0 {
				offset = 0
			}
			if offset > len(arr) {
				offset = len(arr)
			}

			// Determine limit
			limit := staticLimit
			if limitSlot >= 0 && limitSlot < len(ctx.IntSlots) {
				limit = int(ctx.IntSlots[limitSlot])
			}
			if limit < 0 {
				limit = len(arr) - offset
			}
			if offset+limit > len(arr) {
				limit = len(arr) - offset
			}

			sliced := arr[offset : offset+limit]
			marshaled, err := marshalJSONArray(sliced)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			out := ctx.Alloc(len(marshaled))
			copy(out, marshaled)
			ctx.ByteSlots[outputSlot] = out
			return state.PC + 1
		},
	}
}

// ArrayMapStep projects an array to keep only specified fields.
func ArrayMapStep(inputSlot, outputSlot int, fields []string) engine.Instruction {
	return engine.Instruction{
		Name: "ARRAY_MAP",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if inputSlot < 0 || inputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_map: inputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			if outputSlot < 0 || outputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_map: outputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			raw := ctx.ByteSlots[inputSlot]
			if len(raw) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			arr, err := parseJSONArray(raw)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			// Project each element to keep only specified fields
			projected := make([]map[string]interface{}, len(arr))
			for i, elem := range arr {
				proj := make(map[string]interface{})
				for _, field := range fields {
					if v, ok := elem[field]; ok {
						proj[field] = v
					}
				}
				projected[i] = proj
			}

			marshaled, err := marshalJSONArray(projected)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			out := ctx.Alloc(len(marshaled))
			copy(out, marshaled)
			ctx.ByteSlots[outputSlot] = out
			return state.PC + 1
		},
	}
}

// ArrayAggregateStep groups an array by a field and aggregates another field.
// Op must be one of: "sum", "count", "min", "max"
// Output format: []map[string]interface{}{ {groupBy: key, result: aggregated_value}, ... }
func ArrayAggregateStep(inputSlot, outputSlot int, groupByField, valueField, op string) engine.Instruction {
	return engine.Instruction{
		Name: "ARRAY_AGGREGATE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if inputSlot < 0 || inputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_aggregate: inputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			if outputSlot < 0 || outputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_aggregate: outputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			raw := ctx.ByteSlots[inputSlot]
			if len(raw) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			arr, err := parseJSONArray(raw)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			if len(arr) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			// Group elements by groupByField
			type aggregate struct {
				count int
				sum   float64
				min   float64
				max   float64
			}
			groups := make(map[string]*aggregate)
			groupOrder := []string{}

			for _, elem := range arr {
				key := ""
				if v, ok := elem[groupByField]; ok {
					key = toString(v)
				}

				if _, exists := groups[key]; !exists {
					groups[key] = &aggregate{
						count: 0,
						sum:   0,
						min:   1e308,
						max:   -1e308,
					}
					groupOrder = append(groupOrder, key)
				}

				groups[key].count++
				if val, ok := getFloat(elem, valueField); ok {
					groups[key].sum += val
					if val < groups[key].min {
						groups[key].min = val
					}
					if val > groups[key].max {
						groups[key].max = val
					}
				}
			}

			// Build output array
			result := make([]map[string]interface{}, 0, len(groups))
			for _, key := range groupOrder {
				agg := groups[key]
				row := map[string]interface{}{
					groupByField: key,
				}

				switch op {
				case "count":
					row["result"] = float64(agg.count)
				case "sum":
					row["result"] = agg.sum
				case "min":
					if agg.count > 0 {
						row["result"] = agg.min
					}
				case "max":
					if agg.count > 0 {
						row["result"] = agg.max
					}
				default:
					row["result"] = nil
				}

				result = append(result, row)
			}

			marshaled, err := marshalJSONArray(result)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("[]")
				return state.PC + 1
			}

			out := ctx.Alloc(len(marshaled))
			copy(out, marshaled)
			ctx.ByteSlots[outputSlot] = out
			return state.PC + 1
		},
	}
}

// ArrayAvgStep computes the average of a numeric field across all array elements.
// Output is written as a float string (scalar, not JSON array) to outputSlot.
func ArrayAvgStep(inputSlot, outputSlot int, field string) engine.Instruction {
	return engine.Instruction{
		Name: "ARRAY_AVG",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if inputSlot < 0 || inputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_avg: inputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			if outputSlot < 0 || outputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_avg: outputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			raw := ctx.ByteSlots[inputSlot]
			if len(raw) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("0")
				return state.PC + 1
			}

			arr, err := parseJSONArray(raw)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("0")
				return state.PC + 1
			}

			if len(arr) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("0")
				return state.PC + 1
			}

			var sum float64
			var count int
			for _, elem := range arr {
				if val, ok := getFloat(elem, field); ok {
					sum += val
					count++
				}
			}

			if count == 0 {
				ctx.ByteSlots[outputSlot] = []byte("0")
				return state.PC + 1
			}

			avg := sum / float64(count)
			result := formatFloat(avg)
			out := ctx.Alloc(len(result))
			copy(out, result)
			ctx.ByteSlots[outputSlot] = out
			return state.PC + 1
		},
	}
}

// ArrayPercentileStep computes the percentile of a numeric field.
// P is 0-100; output is a float string (scalar).
func ArrayPercentileStep(inputSlot, outputSlot int, field string, p int) engine.Instruction {
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}

	return engine.Instruction{
		Name: "ARRAY_PERCENTILE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if inputSlot < 0 || inputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_percentile: inputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			if outputSlot < 0 || outputSlot >= len(ctx.ByteSlots) {
				ctx.Failed = true
				msg := "array_percentile: outputSlot out of range"
				b := ctx.Alloc(len(msg)); copy(b, msg); ctx.ErrorMsg = b
				return engine.StopPlan
			}
			raw := ctx.ByteSlots[inputSlot]
			if len(raw) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("0")
				return state.PC + 1
			}

			arr, err := parseJSONArray(raw)
			if err != nil {
				ctx.ByteSlots[outputSlot] = []byte("0")
				return state.PC + 1
			}

			if len(arr) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("0")
				return state.PC + 1
			}

			// Collect numeric values
			var values []float64
			for _, elem := range arr {
				if val, ok := getFloat(elem, field); ok {
					values = append(values, val)
				}
			}

			if len(values) == 0 {
				ctx.ByteSlots[outputSlot] = []byte("0")
				return state.PC + 1
			}

			// Sort values
			sort.Float64s(values)

			// Compute percentile index
			idx := int(float64(len(values)-1) * float64(p) / 100.0)
			if idx < 0 {
				idx = 0
			}
			if idx >= len(values) {
				idx = len(values) - 1
			}

			result := formatFloat(values[idx])
			out := ctx.Alloc(len(result))
			copy(out, result)
			ctx.ByteSlots[outputSlot] = out
			return state.PC + 1
		},
	}
}

// helper: toString converts a value to a string for grouping purposes
func toString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return formatFloat(val)
	case int:
		return formatInt(int64(val))
	case int64:
		return formatInt(val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case json.Number:
		return val.String()
	default:
		return ""
	}
}

// helper: formatFloat converts a float64 to a string
func formatFloat(f float64) string {
	// Use JSON to avoid precision loss
	b, _ := json.Marshal(f)
	return string(b)
}

// helper: formatInt converts an int64 to a string
func formatInt(i int64) string {
	b, _ := json.Marshal(i)
	return string(b)
}
