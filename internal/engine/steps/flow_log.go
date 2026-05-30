package steps

import (
	"encoding/json"
	"sync/atomic"

	"rah/internal/engine"
	"rah/internal/gatewaylog"
	"rah/internal/ingest"
	"rah/internal/rctx"
)

// flowLogPipeline is the optional ingest pipeline for KindFlowLog events.
// Set once at startup via SetFlowLogPipeline; nil = ingest disabled.
var flowLogPipeline atomic.Pointer[ingest.Pipeline]

// SetFlowLogPipeline wires the ingest pipeline for flow log events.
func SetFlowLogPipeline(p *ingest.Pipeline) {
	flowLogPipeline.Store(p)
}

// FlowLogConfig is baked at compile time and captured in the instruction closure.
type FlowLogConfig struct {
	Level   gatewaylog.Level
	Message string
	// Fields are static key=value pairs set at compile time.
	// Dynamic values (from slots) are a future extension.
	Fields []gatewaylog.Field
}

// flowLogPayload is the JSON structure emitted as KindFlowLog.
type flowLogPayload struct {
	Level    string            `json:"level"`
	Message  string            `json:"message"`
	TenantID uint16            `json:"tenant_id,omitempty"`
	APIID    uint32            `json:"api_id,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
}

// FlowLog returns an engine.Instruction that emits a structured log message from
// within a flow. It writes to gatewaylog.Default at the configured level and
// optionally to the ingest pipeline as a KindFlowLog event.
// The instruction is fast when ingest is not wired: one atomic load returning nil.
func FlowLog(cfg FlowLogConfig) engine.Instruction {
	return engine.Instruction{
		Name: "log[" + cfg.Level.String() + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Emit to gatewaylog.Default using the named level methods.
			switch cfg.Level {
			case gatewaylog.DEBUG:
				gatewaylog.Default.Debug(cfg.Message, cfg.Fields...)
			case gatewaylog.WARN:
				gatewaylog.Default.Warn(cfg.Message, cfg.Fields...)
			case gatewaylog.ERROR:
				gatewaylog.Default.Error(cfg.Message, cfg.Fields...)
			default: // INFO and anything else
				gatewaylog.Default.Info(cfg.Message, cfg.Fields...)
			}

			// Emit to ingest pipeline if wired and has sinks.
			if p := flowLogPipeline.Load(); p != nil {
				n := p.NumSinksForKind(ingest.KindFlowLog)
				if n > 0 {
					payload := flowLogPayload{
						Level:    cfg.Level.String(),
						Message:  cfg.Message,
						TenantID: ctx.TenantID,
						APIID:    uint32(ctx.ApiId),
					}
					if len(cfg.Fields) > 0 {
						payload.Fields = make(map[string]string, len(cfg.Fields))
						for _, f := range cfg.Fields {
							payload.Fields[f.Key] = f.Value
						}
					}
					b, err := json.Marshal(payload)
					if err == nil {
						e := ingest.Event{
							Kind:     ingest.KindFlowLog,
							TenantID: ctx.TenantID,
							APIID:    uint32(ctx.ApiId),
							Level:    cfg.Level.String(),
						}
						e.SetPayload(b, n)
						p.Emit(e)
					}
				}
			}

			return state.PC + 1
		},
	}
}
