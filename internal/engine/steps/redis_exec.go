package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/redis/go-redis/v9"
)

// RedisArgKind constants control which source (static or dynamic) to use for each argument.
const (
	redisArgKindStatic uint8 = 0 // Argument is baked; read from staticArgs
	redisArgKindSlot   uint8 = 1 // Argument is dynamic; read from ByteSlots
)

// RedisExecConfig holds the bake-time configuration for a redis_exec instruction.
// Supports any Redis command with a mix of static (baked) and dynamic (slot-sourced) arguments.
type RedisExecConfig struct {
	// command is the Redis command (e.g. "HSET", "ZADD", "ZRANGE", "INCR").
	// Passed to redis.Client.Do as the first argument.
	command string

	// argKinds controls which source (static literal or slot) to use for each argument.
	// Length must equal the total number of arguments; values are redisArgKindStatic (0) or redisArgKindSlot (1).
	argKinds []uint8

	// staticArgs holds values for redisArgKindStatic arguments (baked at compile time).
	// Indexed sequentially; must appear in the same relative order as argKinds.
	staticArgs [][]byte

	// slotArgs holds ByteSlot indices for redisArgKindSlot arguments (resolved at runtime).
	// Indexed sequentially; must appear in the same relative order as argKinds.
	slotArgs []int

	// outputSlot is the ByteSlot index where the result will be stored.
	// -1 = discard the result; otherwise the result is marshalled to JSON bytes and written here.
	outputSlot int

	// timeoutMs is the per-call timeout in milliseconds; 0 = no timeout.
	timeoutMs int

	// failOnError controls whether Redis errors should stop the plan (true) or write error
	// string to outputSlot and continue (false). Default: true.
	failOnError bool

	// redisClient is the redis.UniversalClient resolved at bake time from the connector pool.
	// Passed through the closure; never changes at runtime.
	redisClient redis.UniversalClient
}

// redisArgPool recycles []interface{} slices to avoid allocations in the hot path.
// Each pooled slice has up to 16 capacity; most Redis commands fit in this.
var redisArgPool = sync.Pool{
	New: func() any {
		s := make([]interface{}, 0, 16)
		return &s
	},
}

// NewRedisExecStep creates a redis_exec instruction from bake-time configuration.
//
// Validates that argKinds cardinality matches staticArgs + slotArgs:
// - count of redisArgKindStatic in argKinds must equal len(staticArgs)
// - count of redisArgKindSlot in argKinds must equal len(slotArgs)
//
// Returns an engine.Instruction that executes the Redis command at runtime,
// merging baked arguments with slot values, executing the command, and storing
// the result (or error) in outputSlot if configured.
func NewRedisExecStep(
	command string,
	argKinds []uint8,
	staticArgs [][]byte,
	slotArgs []int,
	outputSlot int,
	timeoutMs int,
	failOnError bool,
	redisClient redis.UniversalClient,
) engine.Instruction {
	// Validate argKinds cardinality.
	staticCount := 0
	slotCount := 0
	for _, kind := range argKinds {
		switch kind {
		case redisArgKindStatic:
			staticCount++
		case redisArgKindSlot:
			slotCount++
		}
	}
	if staticCount != len(staticArgs) {
		return failFastInstruction("redis_exec", fmt.Sprintf(
			"argKinds cardinality mismatch: found %d static args, but argKinds has %d redisArgKindStatic",
			len(staticArgs), staticCount))
	}
	if slotCount != len(slotArgs) {
		return failFastInstruction("redis_exec", fmt.Sprintf(
			"argKinds cardinality mismatch: found %d slot args, but argKinds has %d redisArgKindSlot",
			len(slotArgs), slotCount))
	}

	cfg := &RedisExecConfig{
		command:     command,
		argKinds:    argKinds,
		staticArgs:  staticArgs,
		slotArgs:    slotArgs,
		outputSlot:  outputSlot,
		timeoutMs:   timeoutMs,
		failOnError: failOnError,
		redisClient: redisClient,
	}

	return engine.Instruction{
		Name: "redis_exec",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			return cfg.Execute(ctx, state)
		},
	}
}

// Execute runs the redis_exec instruction:
// 1. Borrows an []interface{} from the pool.
// 2. Appends the command and all arguments (static + slot-sourced).
// 3. Executes ctx.Do with optional timeout.
// 4. Marshals the result to JSON bytes and stores in outputSlot (if configured).
// 5. Returns args to pool for reuse.
// 6. Records timing in ctx.Obs if available.
// On error: sets ctx.Failed + ctx.ErrorMsg and returns StopPlan (if failOnError=true),
// or writes error string to outputSlot and continues (if failOnError=false).
func (cfg *RedisExecConfig) Execute(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	if pc, stop := StopIfCancelled(ctx); stop {
		return pc
	}

	if cfg.redisClient == nil {
		cfg.failWith(ctx, "redis_exec: redisClient is nil (not configured)")
		return state.PC + 1
	}

	// Borrow []interface{} from pool.
	argsp := redisArgPool.Get().(*[]interface{})
	args := (*argsp)[:0] // Reset but keep capacity.

	// Append command as first argument.
	args = append(args, cfg.command)

	// Merge static and slot arguments in the order defined by argKinds.
	staticIdx := 0
	slotIdx := 0
	for _, kind := range cfg.argKinds {
		switch kind {
		case redisArgKindStatic:
			args = append(args, cfg.staticArgs[staticIdx])
			staticIdx++
		case redisArgKindSlot:
			args = append(args, ctx.ByteSlots[cfg.slotArgs[slotIdx]])
			slotIdx++
		}
	}

	// Prepare context with optional timeout.
	cmdCtx := ctx.Request.Context()
	var cancel context.CancelFunc
	if cfg.timeoutMs > 0 {
		cmdCtx, cancel = context.WithTimeout(cmdCtx, time.Duration(cfg.timeoutMs)*time.Millisecond)
		defer cancel()
	}

	// Record start time for observability.
	t0 := time.Now()

	// Execute the Redis command.
	result := cfg.redisClient.Do(cmdCtx, args...)
	val, err := result.Val(), result.Err()

	// Record timing if observability is enabled.
	if ctx.Obs != nil {
		elapsed := time.Since(t0).Nanoseconds()
		// Use a generic "redis_op" tag; could be enhanced with the command name.
		ctx.Obs.RecordCacheOp("redis_op", err == nil, elapsed)
	}

	// Return args to pool for reuse.
	(*argsp) = args
	redisArgPool.Put(argsp)

	// Handle errors.
	if err != nil {
		errMsg := fmt.Sprintf("redis_exec error: %v", err)
		if cfg.failOnError {
			cfg.failWith(ctx, errMsg)
			return engine.StopPlan
		}
		// failOnError=false: write error string to outputSlot and continue.
		if cfg.outputSlot >= 0 {
			ctx.ByteSlots[cfg.outputSlot] = []byte(errMsg)
		}
		return state.PC + 1
	}

	// Marshal result to JSON and store in outputSlot (if configured).
	if cfg.outputSlot >= 0 {
		resultBytes, err := marshalRedisResult(val, ctx)
		if err != nil {
			errMsg := fmt.Sprintf("redis_exec: failed to marshal result: %v", err)
			if cfg.failOnError {
				cfg.failWith(ctx, errMsg)
				return engine.StopPlan
			}
			ctx.ByteSlots[cfg.outputSlot] = []byte(errMsg)
			return state.PC + 1
		}
		ctx.ByteSlots[cfg.outputSlot] = resultBytes
	}

	return state.PC + 1
}

// marshalRedisResult converts a redis.Client.Do result to JSON bytes.
// Handles common Redis return types: nil, string, int, []string, etc.
// Uses ctx.Alloc to avoid heap allocation in the common case.
func marshalRedisResult(val interface{}, ctx *rctx.Context) ([]byte, error) {
	if val == nil {
		return []byte("null"), nil
	}

	// Try direct JSON marshalling for most types.
	// This covers int64, string, []interface{}, bool, etc.
	jsonBytes, err := json.Marshal(val)
	if err == nil {
		// Allocate from arena and copy.
		result := ctx.Alloc(len(jsonBytes))
		copy(result, jsonBytes)
		return result, nil
	}

	// Fallback: convert to string representation.
	strVal := fmt.Sprintf("%v", val)
	result := ctx.Alloc(len(strVal))
	copy(result, strVal)
	return result, nil
}

// failWith sets ctx.Failed = true and ctx.ErrorMsg to the given message.
// Used for non-recoverable errors.
func (cfg *RedisExecConfig) failWith(ctx *rctx.Context, msg string) {
	ctx.Failed = true
	b := ctx.Alloc(len(msg))
	copy(b, msg)
	ctx.ErrorMsg = b
}

// failFastInstruction returns an Instruction that fails immediately with the given message.
// Used when bake-time validation fails (e.g., cardinality mismatch).
func failFastInstruction(name, msg string) engine.Instruction {
	return engine.Instruction{
		Name: name,
		Action: func(ctx *rctx.Context, _ *engine.ExecutionState) int16 {
			ctx.Failed = true
			b := ctx.Alloc(len(msg))
			copy(b, msg)
			ctx.ErrorMsg = b
			return engine.StopPlan
		},
	}
}
