package datacache

import (
	"fmt"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// ComputedField is a pre-compiled derived column expression.
// Evaluated at every Append; result written into TargetIdx before writeRow.
type ComputedField struct {
	TargetIdx    int         // column index to write into
	Program      *vm.Program // compiled expr-lang program (AsFloat64)
	InputNames   []string    // variable names used in the env map
	InputIndices []int       // corresponding column indices in the vals slice
}

// compileComputedFields validates and compiles all computed-column expressions.
// fields is the full declared field list (including computed targets).
// defs maps target field name → expression string.
// Returns an error if any target name is not in fields or any expression fails to compile.
func compileComputedFields(fields []FieldDef, defs map[string]string) ([]ComputedField, error) {
	if len(defs) == 0 {
		return nil, nil
	}

	// Build name→index lookup for all fields.
	idxOf := make(map[string]int, len(fields))
	for i, f := range fields {
		idxOf[f.Name] = i
	}

	// Build compile-time env from all non-computed fields (float64 placeholders).
	compileEnv := make(map[string]any, len(fields))
	for _, f := range fields {
		if _, isComputed := defs[f.Name]; !isComputed {
			compileEnv[f.Name] = float64(0)
		}
	}

	result := make([]ComputedField, 0, len(defs))

	for targetName, exprStr := range defs {
		targetIdx, ok := idxOf[targetName]
		if !ok {
			return nil, fmt.Errorf("datacache: computed field %q not declared in fields", targetName)
		}

		program, err := expr.Compile(exprStr, expr.Env(compileEnv), expr.AsFloat64())
		if err != nil {
			return nil, fmt.Errorf("datacache: computed field %q: %w", targetName, err)
		}

		// Collect all non-computed fields as inputs (safe superset).
		var inputNames []string
		var inputIndices []int
		for _, f := range fields {
			if _, isComputed := defs[f.Name]; !isComputed {
				inputNames = append(inputNames, f.Name)
				inputIndices = append(inputIndices, idxOf[f.Name])
			}
		}

		result = append(result, ComputedField{
			TargetIdx:    targetIdx,
			Program:      program,
			InputNames:   inputNames,
			InputIndices: inputIndices,
		})
	}

	return result, nil
}

// envPool reuses env maps for evalComputed to eliminate per-row heap allocations.
var envPool = sync.Pool{New: func() any { return make(map[string]any, 8) }}

// evalComputed fills in computed column values in vals before writeRow.
// vals must be pre-sized to len(schema.Fields); computed slots may be zero/nil on entry.
func evalComputed(computed []ComputedField, vals []any) error {
	if len(computed) == 0 {
		return nil
	}
	env := envPool.Get().(map[string]any)
	defer func() {
		for k := range env {
			delete(env, k)
		}
		envPool.Put(env)
	}()
	for _, cf := range computed {
		// Clear env from previous computed field (reuse the map).
		for k := range env {
			delete(env, k)
		}
		for j, name := range cf.InputNames {
			switch vt := vals[cf.InputIndices[j]].(type) {
			case float64:
				env[name] = vt
			case int64:
				env[name] = float64(vt)
			case uint8:
				env[name] = float64(vt)
			default:
				env[name] = float64(0)
			}
		}
		out, err := expr.Run(cf.Program, env)
		if err != nil {
			return fmt.Errorf("datacache: computed field eval: %w", err)
		}
		if f, ok := out.(float64); ok {
			vals[cf.TargetIdx] = f
		}
	}
	return nil
}
