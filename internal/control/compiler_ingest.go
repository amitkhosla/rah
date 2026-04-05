package control

import (
	"fmt"

	"rah/internal/engine/steps"
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
