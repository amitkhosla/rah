package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/amitkhosla/rah/internal/datastore"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// AgentTaskStatus represents the lifecycle state of an agent task.
type AgentTaskStatus string

const (
	AgentTaskPending AgentTaskStatus = "pending"
	AgentTaskRunning AgentTaskStatus = "running"
	AgentTaskDone    AgentTaskStatus = "done"
	AgentTaskFailed  AgentTaskStatus = "failed"
)

// AgentTask is the persisted task record.
type AgentTask struct {
	ID          string          `json:"id"`
	TenantAlias string          `json:"tenant_alias"`
	AgentName   string          `json:"agent_name"`
	SessionID   string          `json:"session_id"`
	Status      AgentTaskStatus `json:"status"`
	Input       json.RawMessage `json:"input,omitempty"`
	Output      json.RawMessage `json:"output,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// AgentTaskConfig holds bake-time resolved parameters for agent task steps.
type AgentTaskConfig struct {
	Store       datastore.KeyValueStore
	TenantAlias string
	AgentName   string
	SessionSlot int    // >= 0: read session ID from ByteSlots; -1: use SessionLit
	SessionLit  string
	TaskIDSlot  int    // >= 0: read task ID from ByteSlots; -1: use TaskIDLit (for update/get)
	TaskIDLit   string
	StatusLit   string // literal status for update
	OutputSlot  int    // >= 0: write result JSON into ByteSlots[OutputSlot]; -1: skip
}

// agentTaskKey builds the storage key for a task.
func agentTaskKey(tenantAlias, taskID string) string {
	return fmt.Sprintf("agent_task:%s:%s", tenantAlias, taskID)
}

// resolveTaskID reads the task ID from a slot or falls back to the literal.
func resolveTaskID(ctx *rctx.Context, slot int, lit string) string {
	if slot >= 0 && slot < len(ctx.ByteSlots) && len(ctx.ByteSlots[slot]) > 0 {
		return string(ctx.ByteSlots[slot])
	}
	return lit
}

// TaskCreate creates a new agent task with pending status, generates a UUID for the ID,
// stores it, and writes the task ID into OutputSlot.
func TaskCreate(cfg AgentTaskConfig) engine.Instruction {
	return engine.Instruction{
		Name: "task_create",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.Store == nil {
				return state.PC + 1
			}
			sessionID := resolveSession(ctx, cfg.SessionSlot, cfg.SessionLit)
			taskID := uuid.New().String()
			now := time.Now().UTC()
			task := AgentTask{
				ID:          taskID,
				TenantAlias: cfg.TenantAlias,
				AgentName:   cfg.AgentName,
				SessionID:   sessionID,
				Status:      AgentTaskPending,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			data, err := json.Marshal(task)
			if err != nil {
				return state.PC + 1
			}
			storeKey := agentTaskKey(cfg.TenantAlias, taskID)
			_ = cfg.Store.Put(context.Background(), agentGlobalTenant, storeKey, data)
			if cfg.OutputSlot >= 0 {
				idBytes := []byte(taskID)
				state.WriteSlot(ctx, cfg.OutputSlot, idBytes)
			}
			return state.PC + 1
		},
	}
}

// TaskUpdate updates the status (and optionally error/output) of an existing task.
// The task ID is read from TaskIDSlot or TaskIDLit at runtime.
func TaskUpdate(cfg AgentTaskConfig) engine.Instruction {
	return engine.Instruction{
		Name: "task_update",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.Store == nil {
				return state.PC + 1
			}
			taskID := resolveTaskID(ctx, cfg.TaskIDSlot, cfg.TaskIDLit)
			if taskID == "" {
				return state.PC + 1
			}
			storeKey := agentTaskKey(cfg.TenantAlias, taskID)
			raw, found, err := cfg.Store.Get(context.Background(), agentGlobalTenant, storeKey)
			if err != nil || !found {
				return state.PC + 1
			}
			var task AgentTask
			if err := json.Unmarshal(raw, &task); err != nil {
				return state.PC + 1
			}
			if cfg.StatusLit != "" {
				task.Status = AgentTaskStatus(cfg.StatusLit)
			}
			task.UpdatedAt = time.Now().UTC()
			updated, err := json.Marshal(task)
			if err != nil {
				return state.PC + 1
			}
			_ = cfg.Store.Put(context.Background(), agentGlobalTenant, storeKey, updated)
			return state.PC + 1
		},
	}
}

// TaskGet reads a task by ID and writes its JSON into OutputSlot.
func TaskGet(cfg AgentTaskConfig) engine.Instruction {
	return engine.Instruction{
		Name: "task_get",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.Store == nil || cfg.OutputSlot < 0 {
				return state.PC + 1
			}
			taskID := resolveTaskID(ctx, cfg.TaskIDSlot, cfg.TaskIDLit)
			if taskID == "" {
				return state.PC + 1
			}
			storeKey := agentTaskKey(cfg.TenantAlias, taskID)
			raw, found, err := cfg.Store.Get(context.Background(), agentGlobalTenant, storeKey)
			if err != nil || !found {
				return state.PC + 1
			}
			state.WriteSlot(ctx, cfg.OutputSlot, raw)
			return state.PC + 1
		},
	}
}
