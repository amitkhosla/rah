package steps

import (
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/gatewaylog"
	"github.com/amitkhosla/rah/internal/ingest"
	"github.com/amitkhosla/rah/internal/rctx"
)

// flowLogPipeline is the optional ingest pipeline for KindFlowLog events.
// Set once at startup via SetFlowLogPipeline; nil = ingest disabled.
var flowLogPipeline atomic.Pointer[ingest.Pipeline]

// SetFlowLogPipeline wires the ingest pipeline for flow log events.
func SetFlowLogPipeline(p *ingest.Pipeline) {
	flowLogPipeline.Store(p)
}

// DynamicLogField is a field whose value comes from a ByteSlot at runtime.
type DynamicLogField struct {
	JSONKey []byte // pre-built JSON fragment e.g. `,"user_id":"`
	SlotIdx int
}

// FlowLogConfig is baked at compile time. All static content is pre-serialised
// into PayloadPrefix/Mid/Suffix so the hot path never calls json.Marshal.
type FlowLogConfig struct {
	Level gatewaylog.Level

	// Pre-built static JSON fragments. At runtime the instruction assembles:
	//   PayloadPrefix + strconv.AppendUint(tenantID) + PayloadMid +
	//   strconv.AppendUint(apiID) + dynamic fields + PayloadSuffix
	PayloadPrefix []byte
	PayloadMid    []byte
	PayloadSuffix []byte

	// DynamicFields holds slot-backed fields whose values are resolved at runtime.
	DynamicFields []DynamicLogField

	// BufPool is a per-step pool of []byte pre-sized to hold the max payload.
	// One pool per compiled log step â€” size is exact for this step's shape.
	BufPool *sync.Pool

	// GatewayMsg is the plain message string used for gatewaylog.Default
	// warn/error entries. Only used at warn/error frequency so no alloc concern.
	GatewayMsg string
}

// FlowLog returns an engine.Instruction that emits a structured log message from
// within a flow. Routing:
//   - debug/info â†’ ingest pipeline only (not gatewaylog.Default, which would
//     silently drop them when the gateway log level is INFO or higher).
//   - warn/error â†’ ingest pipeline AND gatewaylog.Default (low frequency,
//     allocations acceptable).
//
// The hot path is zero-allocation: JSON payload is assembled from pre-built
// static fragments (PayloadPrefix/Mid/Suffix) plus runtime slot values.
func FlowLog(cfg FlowLogConfig) engine.Instruction {
	return engine.Instruction{
		Name: "log[" + cfg.Level.String() + "]",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Routing fix: warn/error also go to gateway operational log.
			if cfg.Level == gatewaylog.WARN {
				gatewaylog.Default.Warn(cfg.GatewayMsg)
			} else if cfg.Level == gatewaylog.ERROR {
				gatewaylog.Default.Error(cfg.GatewayMsg)
			}
			// debug/info intentionally not sent to gatewaylog.Default â€”
			// they would be silently dropped when the gateway level is INFO+.

			// Emit to ingest pipeline (always â€” not conditional on level).
			p := flowLogPipeline.Load()
			if p == nil {
				return state.PC + 1
			}
			n := p.NumSinksForKind(ingest.KindFlowLog)
			if n == 0 {
				return state.PC + 1
			}

			// Zero-alloc hot path: assemble payload from pre-built fragments.
			buf := cfg.BufPool.Get().([]byte)
			buf = buf[:0]
			buf = append(buf, cfg.PayloadPrefix...)
			buf = strconv.AppendUint(buf, uint64(ctx.TenantID), 10)
			buf = append(buf, cfg.PayloadMid...)
			buf = strconv.AppendUint(buf, uint64(ctx.ApiId), 10)

			// Dynamic slot fields (resolved at runtime).
			for i := range cfg.DynamicFields {
				df := &cfg.DynamicFields[i]
				buf = append(buf, df.JSONKey...)
				val := ctx.ByteSlots[df.SlotIdx]
				buf = appendJSONBytes(buf, val)
				buf = append(buf, '"')
			}

			buf = append(buf, cfg.PayloadSuffix...)

			e := ingest.Event{
				Kind:     ingest.KindFlowLog,
				TenantID: ctx.TenantID,
				APIID:    uint32(ctx.ApiId),
				Level:    cfg.Level.String(),
			}
			e.SetPayload(buf, n)
			p.Emit(e)

			cfg.BufPool.Put(buf)
			return state.PC + 1
		},
	}
}

// appendJSONBytes appends b as a JSON string value (without surrounding quotes â€”
// caller writes the opening quote via JSONKey and closing quote after this call).
func appendJSONBytes(dst []byte, b []byte) []byte {
	for _, c := range b {
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			dst = append(dst, c)
		}
	}
	return dst
}
