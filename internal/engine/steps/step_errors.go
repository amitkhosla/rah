package steps

import (
	"fmt"

	"github.com/amitkhosla/rah/internal/gatewaylog"
	"github.com/amitkhosla/rah/internal/observability"
	"github.com/amitkhosla/rah/internal/rctx"
)

// RecordStepError logs a step-level error to both the operational log and the
// request trace. Use for external failures (network errors, write failures)
// that occur within a step that has access to ctx.
func RecordStepError(ctx *rctx.Context, component, msg string, err error) {
	gatewaylog.Default.Warn(component,
		gatewaylog.F("msg", msg),
		gatewaylog.F("tx_id", rctx.FormatTxID(ctx.InternalTxID)),
		gatewaylog.Fint("tenant_id", int64(ctx.TenantID)),
		gatewaylog.Fint("api_id", int64(ctx.ApiId)),
		gatewaylog.F("error", err.Error()),
	)
	if ctx.Trace != nil && ctx.Obs != nil {
		ctx.Obs.AppendUpstreamEvent(ctx.Trace, observability.UpstreamEvent{
			Err: fmt.Sprintf("%s: %s: %s", component, msg, err.Error()),
		})
	}
}
