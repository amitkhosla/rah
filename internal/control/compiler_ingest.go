package control

import (
	"fmt"

	"rah/internal/engine/steps"
	"rah/internal/gatewaylog"
	"rah/internal/ingest"
)

// compileEmitEvent handles the "emit_event" step type.
//
// Step input keys:
//
//	kind           — event kind string (required; e.g. "llm_request", "prompt_in")
//	payload_slot   — var name whose slot holds the event payload bytes
//	model_slot     — var name whose slot holds the model name string (optional)
//	session_slot   — var name whose slot holds the session ID string (optional)
//	deferred       — "true" to emit after HTTP response is committed (default false)
func (c *Compiler) compileEmitEvent(step StepConfig) error {
	if c.IngestPipeline == nil {
		// Pipeline disabled — compile a no-op instruction so flow structure is preserved.
		c.GlobalTable = append(c.GlobalTable, steps.EmitEvent(steps.EmitEventConfig{
			Pipeline: nil,
		}))
		return nil
	}

	kind := ingest.EventKind(step.Input["kind"])
	if kind == "" {
		return fmt.Errorf("emit_event: 'kind' is required")
	}

	payloadSlot := -1
	if v, ok := step.Input["payload_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("emit_event payload_slot: %w", err)
		}
		payloadSlot = s
	}

	modelSlot := -1
	if v, ok := step.Input["model_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("emit_event model_slot: %w", err)
		}
		modelSlot = s
	}

	sessionSlot := -1
	if v, ok := step.Input["session_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("emit_event session_slot: %w", err)
		}
		sessionSlot = s
	}

	deferred := step.Input["deferred"] == "true"

	c.GlobalTable = append(c.GlobalTable, steps.EmitEvent(steps.EmitEventConfig{
		Pipeline:    c.IngestPipeline,
		Kind:        kind,
		PayloadSlot: payloadSlot,
		ModelSlot:   modelSlot,
		SessionSlot: sessionSlot,
		Deferred:    deferred,
	}))
	return nil
}

// compileFlowLog handles the "log" step type.
//
// Step input keys:
//
//	level   — log level string: debug|info|warn|error (default: info)
//	message — log message text (required)
//	field.* — any input key prefixed with "field." is treated as a static field;
//	           the key suffix becomes the field name (e.g. "field.request_id" → key="request_id")
func (c *Compiler) compileFlowLog(step StepConfig) error {
	msg := step.Input["message"]
	if msg == "" {
		return fmt.Errorf("log: 'message' is required")
	}

	lvl := gatewaylog.ParseLevel(step.Input["level"])

	var fields []gatewaylog.Field
	for k, v := range step.Input {
		if len(k) > 6 && k[:6] == "field." {
			fields = append(fields, gatewaylog.F(k[6:], v))
		}
	}

	c.GlobalTable = append(c.GlobalTable, steps.FlowLog(steps.FlowLogConfig{
		Level:   lvl,
		Message: msg,
		Fields:  fields,
	}))
	return nil
}
