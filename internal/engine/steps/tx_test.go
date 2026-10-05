package steps

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/amitkhosla/rah/internal/datasource"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// mockTxForTesting implements pgx.Tx for testing tx.go functions.
// It tracks method calls and allows injection of errors.
type mockTxForTesting struct {
	commitErr      error
	rollbackErr    error
	execErr        error
	execCalls      []string         // track Exec calls
	commitCalled   bool
	rollbackCalled bool
}

// Begin satisfies pgx.Tx.Begin.
func (m *mockTxForTesting) Begin(ctx context.Context) (pgx.Tx, error) {
	return nil, nil
}

// Commit satisfies pgx.Tx.Commit.
func (m *mockTxForTesting) Commit(ctx context.Context) error {
	m.commitCalled = true
	return m.commitErr
}

// Rollback satisfies pgx.Tx.Rollback.
func (m *mockTxForTesting) Rollback(ctx context.Context) error {
	m.rollbackCalled = true
	return m.rollbackErr
}

// Exec satisfies pgx.Tx.Exec.
func (m *mockTxForTesting) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	m.execCalls = append(m.execCalls, sql)
	return pgconn.CommandTag{}, m.execErr
}

// Query satisfies pgx.Tx.Query.
func (m *mockTxForTesting) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

// QueryRow satisfies pgx.Tx.QueryRow.
func (m *mockTxForTesting) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}

// Prepare satisfies pgx.Tx.Prepare.
func (m *mockTxForTesting) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return nil, nil
}

// CopyFrom satisfies pgx.Tx.CopyFrom.
func (m *mockTxForTesting) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

// SendBatch satisfies pgx.Tx.SendBatch.
func (m *mockTxForTesting) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return nil
}

// LargeObjects satisfies pgx.Tx.LargeObjects.
func (m *mockTxForTesting) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

// Conn satisfies pgx.Tx.Conn.
func (m *mockTxForTesting) Conn() *pgx.Conn {
	return nil
}

// TestBeginTx_AtDepthZero_UnknownSource tests BeginTx when the data source is unknown.
// Expected: ctx.Failed=true, ctx.ActiveTx==nil, response status 500, returns StopPlan.
func TestBeginTx_AtDepthZero_UnknownSource(t *testing.T) {
	pool := datasource.NewAtomicPool()
	ctx := &rctx.Context{}
	state := &engine.ExecutionState{PC: 10}

	fn := BeginTx(pool, "unknown_source", 0)
	result := fn(ctx, state)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed=true")
	}
	if ctx.ActiveTx != nil {
		t.Errorf("expected ctx.ActiveTx==nil")
	}
	if ctx.ResponseStatus != 500 {
		t.Errorf("expected status 500, got %d", ctx.ResponseStatus)
	}
	if ctx.TxDepth != 0 {
		t.Errorf("expected TxDepth=0, got %d", ctx.TxDepth)
	}
}

// TestBeginTx_AtDepthOne_CreatesSavepoint tests BeginTx at depth 1 (creates savepoint).
// Expected: Exec called with "SAVEPOINT rah_sp_2", TxDepth==2, returns PC+1.
func TestBeginTx_AtDepthOne_CreatesSavepoint(t *testing.T) {
	pool := datasource.NewAtomicPool()
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  1,
	}
	state := &engine.ExecutionState{PC: 5}

	fn := BeginTx(pool, "ignored", 0)
	result := fn(ctx, state)

	if result != 6 {
		t.Errorf("expected PC+1=6, got %d", result)
	}
	if ctx.TxDepth != 2 {
		t.Errorf("expected TxDepth=2, got %d", ctx.TxDepth)
	}
	if len(mockTx.execCalls) != 1 {
		t.Errorf("expected 1 Exec call, got %d", len(mockTx.execCalls))
	} else if mockTx.execCalls[0] != "SAVEPOINT rah_sp_2" {
		t.Errorf("expected Exec(SAVEPOINT rah_sp_2), got %s", mockTx.execCalls[0])
	}
	if ctx.Failed {
		t.Errorf("expected ctx.Failed=false")
	}
	if ctx.ActiveTx == nil {
		t.Errorf("expected ctx.ActiveTx!=nil")
	}
}

// TestBeginTx_AtMaxDepth_Fails tests BeginTx at maximum nesting depth.
// Expected: ctx.Failed=true, TxDepth unchanged, returns StopPlan.
func TestBeginTx_AtMaxDepth_Fails(t *testing.T) {
	pool := datasource.NewAtomicPool()
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  8, // maxTxDepth
	}
	state := &engine.ExecutionState{PC: 20}

	fn := BeginTx(pool, "ignored", 0)
	result := fn(ctx, state)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed=true")
	}
	if ctx.TxDepth != 8 {
		t.Errorf("expected TxDepth unchanged at 8, got %d", ctx.TxDepth)
	}
	if ctx.ResponseStatus != 500 {
		t.Errorf("expected status 500, got %d", ctx.ResponseStatus)
	}
	if len(mockTx.execCalls) != 0 {
		t.Errorf("expected no Exec calls at max depth, got %d", len(mockTx.execCalls))
	}
}

// TestCommitTx_AtDepthOne_Commits tests CommitTx at depth 1 (commits transaction).
// Expected: Commit called, ctx.ActiveTx==nil, ctx.TxDepth==0, returns PC+1.
func TestCommitTx_AtDepthOne_Commits(t *testing.T) {
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  1,
	}
	state := &engine.ExecutionState{PC: 15}

	fn := CommitTx()
	result := fn(ctx, state)

	if result != 16 {
		t.Errorf("expected PC+1=16, got %d", result)
	}
	if !mockTx.commitCalled {
		t.Errorf("expected Commit to be called")
	}
	if ctx.ActiveTx != nil {
		t.Errorf("expected ctx.ActiveTx==nil after commit")
	}
	if ctx.TxDepth != 0 {
		t.Errorf("expected TxDepth=0, got %d", ctx.TxDepth)
	}
	if ctx.Failed {
		t.Errorf("expected ctx.Failed=false")
	}
}

// TestCommitTx_AtDepthTwo_ReleasesSavepoint tests CommitTx at depth 2 (releases savepoint).
// Expected: Exec called with "RELEASE SAVEPOINT rah_sp_2" (txReleaseSQL[2]), TxDepth==1, ctx.ActiveTx!=nil, returns PC+1.
func TestCommitTx_AtDepthTwo_ReleasesSavepoint(t *testing.T) {
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  2,
	}
	state := &engine.ExecutionState{PC: 8}

	fn := CommitTx()
	result := fn(ctx, state)

	if result != 9 {
		t.Errorf("expected PC+1=9, got %d", result)
	}
	if len(mockTx.execCalls) != 1 {
		t.Errorf("expected 1 Exec call, got %d", len(mockTx.execCalls))
	} else if mockTx.execCalls[0] != "RELEASE SAVEPOINT rah_sp_2" {
		t.Errorf("expected Exec with RELEASE SAVEPOINT rah_sp_2, got %q", mockTx.execCalls[0])
	}
	if ctx.TxDepth != 1 {
		t.Errorf("expected TxDepth=1, got %d", ctx.TxDepth)
	}
	if ctx.ActiveTx == nil {
		t.Errorf("expected ctx.ActiveTx!=nil (still in transaction)")
	}
	if ctx.Failed {
		t.Errorf("expected ctx.Failed=false")
	}
	if mockTx.commitCalled {
		t.Errorf("expected Commit NOT to be called at depth>1")
	}
}

// TestCommitTx_NilTx_Noop tests CommitTx when ctx.ActiveTx is nil.
// Expected: no-op, returns PC+1, no panic.
func TestCommitTx_NilTx_Noop(t *testing.T) {
	ctx := &rctx.Context{
		ActiveTx: nil,
		TxDepth:  0,
	}
	state := &engine.ExecutionState{PC: 25}

	fn := CommitTx()
	result := fn(ctx, state)

	if result != 26 {
		t.Errorf("expected PC+1=26, got %d", result)
	}
	if ctx.ActiveTx != nil {
		t.Errorf("expected ctx.ActiveTx to remain nil")
	}
	if ctx.Failed {
		t.Errorf("expected ctx.Failed=false")
	}
}

// TestRollbackTx_AtDepthOne_Rollbacks tests RollbackTx at depth 1 (rolls back transaction).
// Expected: Rollback called, ctx.ActiveTx==nil, ctx.TxDepth==0, returns PC+1.
func TestRollbackTx_AtDepthOne_Rollbacks(t *testing.T) {
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  1,
	}
	state := &engine.ExecutionState{PC: 30}

	fn := RollbackTx()
	result := fn(ctx, state)

	if result != 31 {
		t.Errorf("expected PC+1=31, got %d", result)
	}
	if !mockTx.rollbackCalled {
		t.Errorf("expected Rollback to be called")
	}
	if ctx.ActiveTx != nil {
		t.Errorf("expected ctx.ActiveTx==nil after rollback")
	}
	if ctx.TxDepth != 0 {
		t.Errorf("expected TxDepth=0, got %d", ctx.TxDepth)
	}
}

// TestRollbackTx_AtDepthTwo_RollsbackToSavepoint tests RollbackTx at depth 2 (rolls back to savepoint).
// Expected: Exec called with "ROLLBACK TO SAVEPOINT rah_sp_2" (txRollbackToSQL[2]), TxDepth==1, ctx.ActiveTx!=nil, returns PC+1.
func TestRollbackTx_AtDepthTwo_RollsbackToSavepoint(t *testing.T) {
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  2,
	}
	state := &engine.ExecutionState{PC: 12}

	fn := RollbackTx()
	result := fn(ctx, state)

	if result != 13 {
		t.Errorf("expected PC+1=13, got %d", result)
	}
	if len(mockTx.execCalls) != 1 {
		t.Errorf("expected 1 Exec call, got %d", len(mockTx.execCalls))
	} else if mockTx.execCalls[0] != "ROLLBACK TO SAVEPOINT rah_sp_2" {
		t.Errorf("expected Exec with ROLLBACK TO SAVEPOINT rah_sp_2, got %q", mockTx.execCalls[0])
	}
	if ctx.TxDepth != 1 {
		t.Errorf("expected TxDepth=1, got %d", ctx.TxDepth)
	}
	if ctx.ActiveTx == nil {
		t.Errorf("expected ctx.ActiveTx!=nil (still in transaction)")
	}
	if mockTx.rollbackCalled {
		t.Errorf("expected Rollback NOT to be called at depth>1")
	}
}

// TestRollbackTx_NilTx_Noop tests RollbackTx when ctx.ActiveTx is nil.
// Expected: no-op, returns PC+1, no panic.
func TestRollbackTx_NilTx_Noop(t *testing.T) {
	ctx := &rctx.Context{
		ActiveTx: nil,
		TxDepth:  0,
	}
	state := &engine.ExecutionState{PC: 40}

	fn := RollbackTx()
	result := fn(ctx, state)

	if result != 41 {
		t.Errorf("expected PC+1=41, got %d", result)
	}
	if ctx.ActiveTx != nil {
		t.Errorf("expected ctx.ActiveTx to remain nil")
	}
}

// TestReset_WithActiveTx_Rollbacks tests rctx.Context.Reset when ActiveTx is set.
// Expected: Rollback called, ctx.ActiveTx==nil, ctx.TxDepth==0.
func TestReset_WithActiveTx_Rollbacks(t *testing.T) {
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  1,
	}
	noopWriter := &rctx.NoopResponseWriter{}

	ctx.Reset(noopWriter)

	if !mockTx.rollbackCalled {
		t.Errorf("expected Rollback to be called during Reset")
	}
	if ctx.ActiveTx != nil {
		t.Errorf("expected ctx.ActiveTx==nil after Reset")
	}
	if ctx.TxDepth != 0 {
		t.Errorf("expected ctx.TxDepth==0 after Reset, got %d", ctx.TxDepth)
	}
}

// TestCommitTx_CommitFails_RollsbackAndSets500 tests CommitTx when Commit fails.
// Expected: Rollback called, ctx.ActiveTx==nil, ctx.TxDepth==0, ctx.Failed=true, status 500, returns StopPlan.
func TestCommitTx_CommitFails_RollsbackAndSets500(t *testing.T) {
	mockTx := &mockTxForTesting{commitErr: context.DeadlineExceeded}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  1,
	}
	state := &engine.ExecutionState{PC: 5}

	fn := CommitTx()
	result := fn(ctx, state)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed=true after commit error")
	}
	if ctx.ResponseStatus != 500 {
		t.Errorf("expected status 500, got %d", ctx.ResponseStatus)
	}
	if ctx.ActiveTx != nil {
		t.Errorf("expected ctx.ActiveTx==nil after rollback")
	}
	if ctx.TxDepth != 0 {
		t.Errorf("expected TxDepth=0, got %d", ctx.TxDepth)
	}
	if !mockTx.rollbackCalled {
		t.Errorf("expected Rollback to be called after Commit failure")
	}
}

// TestBeginTx_AtDepthTwo_CreatesSavepoint3 tests BeginTx at depth 2 (creates savepoint 3).
// Expected: Exec called with "SAVEPOINT rah_sp_3", TxDepth==3, returns PC+1.
func TestBeginTx_AtDepthTwo_CreatesSavepoint3(t *testing.T) {
	pool := datasource.NewAtomicPool()
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  2,
	}
	state := &engine.ExecutionState{PC: 10}

	fn := BeginTx(pool, "ignored", 0)
	result := fn(ctx, state)

	if result != 11 {
		t.Errorf("expected PC+1=11, got %d", result)
	}
	if ctx.TxDepth != 3 {
		t.Errorf("expected TxDepth=3, got %d", ctx.TxDepth)
	}
	if len(mockTx.execCalls) != 1 {
		t.Errorf("expected 1 Exec call, got %d", len(mockTx.execCalls))
	} else if mockTx.execCalls[0] != "SAVEPOINT rah_sp_3" {
		t.Errorf("expected Exec(SAVEPOINT rah_sp_3), got %s", mockTx.execCalls[0])
	}
}

// TestCommitTx_AtDepthThree_ReleasesSavepoint3 tests CommitTx at depth 3.
// Expected: Exec called with "RELEASE SAVEPOINT rah_sp_3" (txReleaseSQL[3]), TxDepth==2, returns PC+1.
func TestCommitTx_AtDepthThree_ReleasesSavepoint3(t *testing.T) {
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  3,
	}
	state := &engine.ExecutionState{PC: 20}

	fn := CommitTx()
	result := fn(ctx, state)

	if result != 21 {
		t.Errorf("expected PC+1=21, got %d", result)
	}
	if len(mockTx.execCalls) != 1 {
		t.Errorf("expected 1 Exec call, got %d", len(mockTx.execCalls))
	} else if mockTx.execCalls[0] != "RELEASE SAVEPOINT rah_sp_3" {
		t.Errorf("expected Exec(RELEASE SAVEPOINT rah_sp_3 via index 3), got %s", mockTx.execCalls[0])
	}
	if ctx.TxDepth != 2 {
		t.Errorf("expected TxDepth=2, got %d", ctx.TxDepth)
	}
}

// TestRollbackTx_AtDepthThree_RollsbackToSavepoint3 tests RollbackTx at depth 3.
// Expected: Exec called with "ROLLBACK TO SAVEPOINT rah_sp_3" (txRollbackToSQL[3]), TxDepth==2, returns PC+1.
func TestRollbackTx_AtDepthThree_RollsbackToSavepoint3(t *testing.T) {
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  3,
	}
	state := &engine.ExecutionState{PC: 50}

	fn := RollbackTx()
	result := fn(ctx, state)

	if result != 51 {
		t.Errorf("expected PC+1=51, got %d", result)
	}
	if len(mockTx.execCalls) != 1 {
		t.Errorf("expected 1 Exec call, got %d", len(mockTx.execCalls))
	} else if mockTx.execCalls[0] != "ROLLBACK TO SAVEPOINT rah_sp_3" {
		t.Errorf("expected Exec(ROLLBACK TO SAVEPOINT rah_sp_3 via index 3), got %s", mockTx.execCalls[0])
	}
	if ctx.TxDepth != 2 {
		t.Errorf("expected TxDepth=2, got %d", ctx.TxDepth)
	}
}

// TestErrorHandlerPC_SetAfterSuccessfulBegin tests that BeginTx sets ErrorHandlerPC at nested depth.
// Expected: state.ErrorHandlerPC is set to rollbackPC after a successful nested BeginTx call.
// (Tests the happy path of ErrorHandlerPC setting at depth 1.)
func TestErrorHandlerPC_SetAfterSuccessfulBegin(t *testing.T) {
	pool := datasource.NewAtomicPool()
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  1, // Already at depth 1, so BeginTx will create a savepoint
	}
	state := &engine.ExecutionState{PC: 5}
	rollbackPC := int16(99)

	fn := BeginTx(pool, "ignored", rollbackPC)
	result := fn(ctx, state)

	if result != 6 {
		t.Errorf("expected PC+1=6, got %d", result)
	}
	if state.ErrorHandlerPC != 99 {
		t.Errorf("expected ErrorHandlerPC=99, got %d", state.ErrorHandlerPC)
	}
	if ctx.TxDepth != 2 {
		t.Errorf("expected TxDepth=2, got %d", ctx.TxDepth)
	}
	if ctx.Failed {
		t.Errorf("expected ctx.Failed=false")
	}
}

// TestErrorHandlerPC_NotSetAfterFailedBegin tests that BeginTx does not set ErrorHandlerPC on failure.
// Expected: state.ErrorHandlerPC remains 0 when BeginTx fails (unknown source).
func TestErrorHandlerPC_NotSetAfterFailedBegin(t *testing.T) {
	pool := datasource.NewAtomicPool()
	ctx := &rctx.Context{}
	state := &engine.ExecutionState{PC: 10}
	rollbackPC := int16(50)

	fn := BeginTx(pool, "unknown_source", rollbackPC)
	result := fn(ctx, state)

	if result != engine.StopPlan {
		t.Errorf("expected StopPlan, got %d", result)
	}
	if state.ErrorHandlerPC != 0 {
		t.Errorf("expected ErrorHandlerPC=0 after failed BeginTx, got %d", state.ErrorHandlerPC)
	}
	if !ctx.Failed {
		t.Errorf("expected ctx.Failed=true")
	}
}

// TestErrorHandlerPC_ClearedByCommitTx tests that CommitTx clears ErrorHandlerPC.
// Expected: state.ErrorHandlerPC is set to 0 after CommitTx executes.
func TestErrorHandlerPC_ClearedByCommitTx(t *testing.T) {
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  1,
	}
	state := &engine.ExecutionState{PC: 15, ErrorHandlerPC: 50}

	fn := CommitTx()
	result := fn(ctx, state)

	if result != 16 {
		t.Errorf("expected PC+1=16, got %d", result)
	}
	if state.ErrorHandlerPC != 0 {
		t.Errorf("expected ErrorHandlerPC=0 after CommitTx, got %d", state.ErrorHandlerPC)
	}
	if !mockTx.commitCalled {
		t.Errorf("expected Commit to be called")
	}
	if ctx.TxDepth != 0 {
		t.Errorf("expected TxDepth=0, got %d", ctx.TxDepth)
	}
}

// TestErrorHandlerPC_ClearedByRollbackTx tests that RollbackTx clears ErrorHandlerPC.
// Expected: state.ErrorHandlerPC is set to 0 after RollbackTx executes.
func TestErrorHandlerPC_ClearedByRollbackTx(t *testing.T) {
	mockTx := &mockTxForTesting{}
	ctx := &rctx.Context{
		ActiveTx: mockTx,
		TxDepth:  1,
	}
	state := &engine.ExecutionState{PC: 20, ErrorHandlerPC: 75}

	fn := RollbackTx()
	result := fn(ctx, state)

	if result != 21 {
		t.Errorf("expected PC+1=21, got %d", result)
	}
	if state.ErrorHandlerPC != 0 {
		t.Errorf("expected ErrorHandlerPC=0 after RollbackTx, got %d", state.ErrorHandlerPC)
	}
	if !mockTx.rollbackCalled {
		t.Errorf("expected Rollback to be called")
	}
	if ctx.TxDepth != 0 {
		t.Errorf("expected TxDepth=0, got %d", ctx.TxDepth)
	}
}

// TestExecute_TxBodyFail_RedirectsToRollback tests the Execute loop's ErrorHandlerPC redirection.
// Expected: When a step fails (returns StopPlan with ctx.Failed=true) and ErrorHandlerPC is set,
// the execution redirects to the error handler instead of stopping.
// Step 0 simulates a successful BeginTx by setting ErrorHandlerPC and returning next PC.
// Step 1 is a transaction body that fails.
// Step 2 is the rollback handler (accessed via ErrorHandlerPC redirect).
func TestExecute_TxBodyFail_RedirectsToRollback(t *testing.T) {
	mockTx := &mockTxForTesting{}

	// Step 0: Simulates BeginTx (sets ErrorHandlerPC=2 and returns PC+1)
	step0 := func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		state.ErrorHandlerPC = 2
		ctx.ActiveTx = mockTx
		ctx.TxDepth = 1
		return state.PC + 1 // Return next PC
	}

	// Step 1: Body step that fails with StopPlan and ctx.Failed=true
	step1 := func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		ctx.Failed = true
		return engine.StopPlan
	}

	// Step 2: RollbackTx (the error handler, reached via ErrorHandlerPC redirect)
	step2 := RollbackTx()

	table := []engine.Instruction{
		{Name: "begin_step", Action: step0, StepIdx: 0},
		{Name: "body_step", Action: step1, StepIdx: 1},
		{Name: "rollback_step", Action: step2, StepIdx: -1},
	}

	ctx := &rctx.Context{}
	engine.Execute(ctx, table, 0, 0)

	// Verify rollback was called (transaction was rolled back)
	if !mockTx.rollbackCalled {
		t.Errorf("expected Rollback to be called")
	}
	// Verify transaction depth was reset
	if ctx.TxDepth != 0 {
		t.Errorf("expected TxDepth=0 after rollback, got %d", ctx.TxDepth)
	}
	// Verify ActiveTx was cleared
	if ctx.ActiveTx != nil {
		t.Errorf("expected ActiveTx==nil after rollback")
	}
}
