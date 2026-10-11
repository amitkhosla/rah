package steps

import (
	"fmt"
	"strconv"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// Requires: go get github.com/expr-lang/expr
// The expr package is not yet in go.mod; it will be added in Phase 5.

// EvalExprConfig holds the baked configuration for an eval_expr step.
type EvalExprConfig struct {
	program       *vm.Program // compiled expression, baked once at compile time
	varNames      []string    // variable names referenced in the expression
	varSlots      []int       // corresponding ByteSlot indices (pre-resolved at compile time)
	outputSlot    int         // ByteSlot index to write result string
	outputIntSlot int         // IntSlot index for fixed-point result (×1000); -1 = skip
	exprStr       string      // original expression string (for tracing/debugging)
}

// NewEvalExprStep creates an eval_expr instruction that evaluates a float expression
// at runtime. Variable names in the expression are mapped to ByteSlot values (which
// hold float strings). The result is written to a ByteSlot as a string and optionally
// to an IntSlot as a fixed-point integer (multiplied by 1000).
//
// Compile-time behavior:
// - Compiles the expression string into a vm.Program once
// - Returns compile-time error if expression syntax is invalid
//
// Runtime behavior:
// - Reads variable values from ByteSlots and parses them as floats
// - Evaluates the compiled expression against those variables
// - Writes the result as a string to outputSlot via ctx.Alloc
// - If outputIntSlot >= 0, also writes result * 1000 as int64
// - On evaluation error, sets ctx.Failed and ctx.ErrorMsg, then stops the plan
//
// Note: The env map allocation is acceptable because eval_expr targets batch/background
// flows, not the REST hot path. For performance-critical paths, pre-compute or use
// a different approach.
func NewEvalExprStep(
	exprStr string,
	varNames []string,
	varSlotIndices []int,
	outputSlot int,
	outputIntSlot int,
) (engine.Instruction, error) {
	// Compile the expression at bake time. Return error if syntax is invalid.
	program, err := expr.Compile(
		exprStr,
		expr.Env(map[string]interface{}{}),
		expr.AsFloat64(),
	)
	if err != nil {
		return engine.Instruction{}, fmt.Errorf("eval_expr compile error: %w", err)
	}

	cfg := &EvalExprConfig{
		program:       program,
		varNames:      varNames,
		varSlots:      varSlotIndices,
		outputSlot:    outputSlot,
		outputIntSlot: outputIntSlot,
		exprStr:       exprStr,
	}

	return engine.Instruction{
		Name: "eval_expr",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			return evalExprAction(ctx, state, cfg)
		},
	}, nil
}

// evalExprAction is the runtime handler for eval_expr instructions.
func evalExprAction(ctx *rctx.Context, state *engine.ExecutionState, cfg *EvalExprConfig) int16 {
	// Build environment map: one allocation (unavoidable for dynamic variable binding).
	// This is acceptable because eval_expr targets batch flows, not the ns-level hot path.
	env := make(map[string]interface{}, len(cfg.varNames))

	// Populate env with variable values from ByteSlots.
	for i, varName := range cfg.varNames {
		slotIdx := cfg.varSlots[i]
		if slotIdx < 0 || slotIdx >= len(ctx.ByteSlots) {
			env[varName] = 0.0
			continue
		}

		// Parse ByteSlot value as float64.
		valBytes := ctx.ByteSlots[slotIdx]
		f, err := strconv.ParseFloat(string(valBytes), 64)
		if err != nil {
			// On parse error, use 0.0 as fallback (lenient).
			f = 0.0
		}
		env[varName] = f
	}

	// Evaluate the compiled expression.
	result, err := expr.Run(cfg.program, env)
	if err != nil {
		ctx.Failed = true
		errMsg := fmt.Sprintf("eval_expr runtime error: %v", err)
		ctx.ErrorMsg = ctx.Alloc(len(errMsg))
		copy(ctx.ErrorMsg, errMsg)
		return engine.StopPlan
	}

	// Extract float64 result (guaranteed by expr.AsFloat64() at compile time).
	f := result.(float64)

	// Write result string to outputSlot.
	resultStr := strconv.FormatFloat(f, 'f', -1, 64)
	outputBytes := ctx.Alloc(len(resultStr))
	copy(outputBytes, resultStr)
	if cfg.outputSlot < 0 || cfg.outputSlot >= len(ctx.ByteSlots) {
		// handle gracefully — skip writing output rather than panic
		return state.PC + 1
	}
	ctx.ByteSlots[cfg.outputSlot] = outputBytes

	// If outputIntSlot is set, write fixed-point result (×1000).
	if cfg.outputIntSlot >= 0 && cfg.outputIntSlot < len(ctx.IntSlots) {
		ctx.IntSlots[cfg.outputIntSlot] = int64(f * 1000)
	}

	// Add trace attribute if tracing is active.
	if state != nil {
		state.AddTraceAttr("eval_expr", cfg.exprStr)
	}

	// Continue to next instruction.
	return state.PC + 1
}
