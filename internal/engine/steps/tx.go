package steps

import (
	"context"

	"github.com/amitkhosla/rah/internal/datasource"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// Savepoint SQL literals — one set per nesting depth, zero per-request allocation.
// Index N corresponds to savepoint rah_sp_{N+1} created when TxDepth==N (about to become N+1).
// Supports up to 8 levels of nesting (outermost BEGIN + 7 savepoints).
var txSavepointSQL = [9]string{
	"",
	"SAVEPOINT rah_sp_2",
	"SAVEPOINT rah_sp_3",
	"SAVEPOINT rah_sp_4",
	"SAVEPOINT rah_sp_5",
	"SAVEPOINT rah_sp_6",
	"SAVEPOINT rah_sp_7",
	"SAVEPOINT rah_sp_8",
	"SAVEPOINT rah_sp_9",
}

// txReleaseSQL[N] releases savepoint rah_sp_{N} (used when committing at TxDepth==N, N>=2).
var txReleaseSQL = [9]string{
	"",
	"",
	"RELEASE SAVEPOINT rah_sp_2",
	"RELEASE SAVEPOINT rah_sp_3",
	"RELEASE SAVEPOINT rah_sp_4",
	"RELEASE SAVEPOINT rah_sp_5",
	"RELEASE SAVEPOINT rah_sp_6",
	"RELEASE SAVEPOINT rah_sp_7",
	"RELEASE SAVEPOINT rah_sp_8",
}

// txRollbackToSQL[N] rolls back to savepoint rah_sp_{N} (used when rolling back at TxDepth==N, N>=2).
var txRollbackToSQL = [9]string{
	"",
	"",
	"ROLLBACK TO SAVEPOINT rah_sp_2",
	"ROLLBACK TO SAVEPOINT rah_sp_3",
	"ROLLBACK TO SAVEPOINT rah_sp_4",
	"ROLLBACK TO SAVEPOINT rah_sp_5",
	"ROLLBACK TO SAVEPOINT rah_sp_6",
	"ROLLBACK TO SAVEPOINT rah_sp_7",
	"ROLLBACK TO SAVEPOINT rah_sp_8",
}

const maxTxDepth = int8(8)

// BeginTx returns an InstructionFunc that starts a database transaction on the named
// data source. If no transaction is active (TxDepth==0) it calls pool.Begin(); for
// nested calls it issues a SAVEPOINT instead. Sets ctx.Failed on any error.
// rollbackPC is the instruction address to jump to on transaction body failure.
func BeginTx(atomicPool *datasource.AtomicPool, sourceName string, rollbackPC int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		if ctx.TxDepth == 0 {
			pool, ok := atomicPool.Get(sourceName)
			if !ok {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}
			tx, err := pool.Begin(context.Background())
			if err != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}
			ctx.ActiveTx = tx
			ctx.TxDepth = 1
		} else if ctx.TxDepth < maxTxDepth {
			if _, err := ctx.ActiveTx.Exec(context.Background(), txSavepointSQL[ctx.TxDepth]); err != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}
			ctx.TxDepth++
		} else {
			ctx.ResponseStatus = 500
			ctx.Failed = true
			return engine.StopPlan
		}
		state.ErrorHandlerPC = rollbackPC
		return state.PC + 1
	}
}

// CommitTx returns an InstructionFunc that commits the current transaction scope.
// At depth 1 it issues COMMIT; at depth >1 it releases the current savepoint.
// If ctx.ActiveTx is nil it is a no-op (safe to call defensively).
func CommitTx() engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		state.ErrorHandlerPC = 0
		if ctx.ActiveTx == nil {
			return state.PC + 1
		}
		if ctx.TxDepth == 1 {
			if err := ctx.ActiveTx.Commit(context.Background()); err != nil {
				_ = ctx.ActiveTx.Rollback(context.Background())
				ctx.ActiveTx = nil
				ctx.TxDepth = 0
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}
			ctx.ActiveTx = nil
			ctx.TxDepth = 0
		} else if ctx.TxDepth > 1 {
			if _, err := ctx.ActiveTx.Exec(context.Background(), txReleaseSQL[ctx.TxDepth]); err != nil {
				_, _ = ctx.ActiveTx.Exec(context.Background(), txRollbackToSQL[ctx.TxDepth])
				ctx.TxDepth--
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return engine.StopPlan
			}
			ctx.TxDepth--
		}
		return state.PC + 1
	}
}

// RollbackTx returns an InstructionFunc that rolls back the current transaction scope.
// At depth 1 it issues ROLLBACK; at depth >1 it rolls back to the current savepoint.
// If ctx.ActiveTx is nil it is a no-op.
func RollbackTx() engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		state.ErrorHandlerPC = 0
		if ctx.ActiveTx == nil {
			return state.PC + 1
		}
		if ctx.TxDepth == 1 {
			_ = ctx.ActiveTx.Rollback(context.Background())
			ctx.ActiveTx = nil
			ctx.TxDepth = 0
		} else if ctx.TxDepth > 1 {
			_, _ = ctx.ActiveTx.Exec(context.Background(), txRollbackToSQL[ctx.TxDepth])
			ctx.TxDepth--
		}
		return state.PC + 1
	}
}
