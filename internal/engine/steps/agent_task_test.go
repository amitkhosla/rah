package steps

import (
	"encoding/json"
	"testing"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

func newTaskCtx(numSlots int) (*rctx.Context, *engine.ExecutionState) {
	ctx := &rctx.Context{
		ByteSlots: make([][]byte, numSlots),
	}
	state := &engine.ExecutionState{PC: 0}
	return ctx, state
}

func TestTaskCreate_CreatesPendingTask(t *testing.T) {
	store := newMemStore("agent_tasks")
	ctx, state := newTaskCtx(3)

	cfg := AgentTaskConfig{
		Store:       store,
		TenantAlias: "acme",
		AgentName:   "bot",
		SessionSlot: -1,
		SessionLit:  "sess1",
		OutputSlot:  0,
	}

	next := TaskCreate(cfg).Action(ctx, state)
	if next != 1 {
		t.Fatalf("expected PC 1, got %d", next)
	}

	taskID := string(ctx.ByteSlots[0])
	if taskID == "" {
		t.Fatal("expected task ID in output slot")
	}

	key := agentTaskKey("acme", taskID)
	raw, found, err := store.Get(nil, agentGlobalTenant, key)
	if err != nil || !found {
		t.Fatalf("task not found in store: err=%v found=%v", err, found)
	}
	var task AgentTask
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if task.Status != AgentTaskPending {
		t.Errorf("expected status pending, got %s", task.Status)
	}
	if task.ID != taskID {
		t.Errorf("task ID mismatch: %s != %s", task.ID, taskID)
	}
	if task.TenantAlias != "acme" {
		t.Errorf("expected tenant acme, got %s", task.TenantAlias)
	}
}

func TestTaskUpdate_ChangesStatus(t *testing.T) {
	store := newMemStore("agent_tasks")
	ctx, state := newTaskCtx(3)

	createCfg := AgentTaskConfig{
		Store:       store,
		TenantAlias: "acme",
		AgentName:   "bot",
		SessionSlot: -1,
		SessionLit:  "sess1",
		OutputSlot:  0,
	}
	TaskCreate(createCfg).Action(ctx, state)
	taskID := string(ctx.ByteSlots[0])

	ctx.ByteSlots[1] = []byte(taskID)
	updateCfg := AgentTaskConfig{
		Store:       store,
		TenantAlias: "acme",
		TaskIDSlot:  1,
		StatusLit:   "done",
	}
	next := TaskUpdate(updateCfg).Action(ctx, state)
	if next != 1 {
		t.Fatalf("expected PC 1, got %d", next)
	}

	raw, found, _ := store.Get(nil, agentGlobalTenant, agentTaskKey("acme", taskID))
	if !found {
		t.Fatal("task not found after update")
	}
	var task AgentTask
	_ = json.Unmarshal(raw, &task)
	if task.Status != AgentTaskDone {
		t.Errorf("expected status done, got %s", task.Status)
	}
}
