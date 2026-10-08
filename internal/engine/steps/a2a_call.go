package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/amitkhosla/rah/internal/a2a"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/google/uuid"
)

// A2ACallConfig holds bake-time resolved parameters for calling a remote A2A agent.
type A2ACallConfig struct {
	URL        string // base URL of the remote A2A agent
	SkillID    string // optional skill ID hint
	InputSlot  int    // ByteSlots index holding the JSON message to send
	OutputSlot int    // ByteSlots index where the result JSON is written
	TimeoutSec int    // HTTP timeout in seconds; default 30
}

var a2aHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// A2ACall returns an Instruction that sends a tasks/send JSON-RPC request
// to a remote A2A agent and writes the result JSON into OutputSlot.
// On any remote error the step sets OutputSlot to null and logs a warning (fail-open).
func A2ACall(cfg A2ACallConfig) engine.Instruction {
	timeoutSec := cfg.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}

	return engine.Instruction{
		Name: "a2a_call",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			var msgBytes []byte
			if cfg.InputSlot >= 0 && cfg.InputSlot < len(ctx.ByteSlots) {
				msgBytes = ctx.ByteSlots[cfg.InputSlot]
			}

			taskID := uuid.New().String()
			params := a2a.TaskSendParams{
				ID:      taskID,
				Message: json.RawMessage(msgBytes),
			}
			paramsJSON, err := json.Marshal(params)
			if err != nil {
				writeA2ANullResult(ctx, state, cfg.OutputSlot)
				return state.PC + 1
			}

			rpcReq := a2a.JSONRPCRequest{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "tasks/send",
				Params:  json.RawMessage(paramsJSON),
			}
			body, err := json.Marshal(rpcReq)
			if err != nil {
				writeA2ANullResult(ctx, state, cfg.OutputSlot)
				return state.PC + 1
			}

			targetURL := cfg.URL + "/"
			reqCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
			defer cancel()

			httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, targetURL, bytes.NewReader(body))
			if err != nil {
				writeA2ANullResult(ctx, state, cfg.OutputSlot)
				return state.PC + 1
			}
			httpReq.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(httpReq)
			if err != nil {
				RecordStepError(ctx, "a2a_call", fmt.Sprintf("remote call to %s failed", cfg.URL), err)
				writeA2ANullResult(ctx, state, cfg.OutputSlot)
				return state.PC + 1
			}
			defer resp.Body.Close()
			respBody, err := io.ReadAll(resp.Body)
			if err != nil {
				writeA2ANullResult(ctx, state, cfg.OutputSlot)
				return state.PC + 1
			}

			var rpcResp a2a.JSONRPCResponse
			if err := json.Unmarshal(respBody, &rpcResp); err != nil {
				writeA2ANullResult(ctx, state, cfg.OutputSlot)
				return state.PC + 1
			}
			if rpcResp.Error != nil {
				RecordStepError(ctx, "a2a_call",
					fmt.Sprintf("remote error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message),
					fmt.Errorf("rpc error %d", rpcResp.Error.Code))
				writeA2ANullResult(ctx, state, cfg.OutputSlot)
				return state.PC + 1
			}

			resultJSON, err := json.Marshal(rpcResp.Result)
			if err != nil {
				writeA2ANullResult(ctx, state, cfg.OutputSlot)
				return state.PC + 1
			}
			state.WriteSlot(ctx, cfg.OutputSlot, resultJSON)
			return state.PC + 1
		},
	}
}

func writeA2ANullResult(ctx *rctx.Context, state *engine.ExecutionState, outputSlot int) {
	if outputSlot >= 0 {
		state.WriteSlot(ctx, outputSlot, []byte("null"))
	}
}
