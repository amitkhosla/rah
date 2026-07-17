package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/amitkhosla/rah/internal/engine/steps"
	"github.com/amitkhosla/rah/internal/gatewaylog"
	"github.com/amitkhosla/rah/internal/ingest"
)

// compileEmitEvent handles the "emit_event" step type.
//
// Step input keys:
//
//	kind               â€” event kind string (required; e.g. "llm_request", "prompt_in")
//	payload_slot       â€” var name whose slot holds the event payload bytes
//	model_slot         â€” var name whose slot holds the model name string (optional)
//	session_slot       â€” var name whose slot holds the session ID string (optional)
//	input_tokens_slot  â€” var name whose IntSlot holds input token count (optional)
//	output_tokens_slot â€” var name whose IntSlot holds output token count (optional)
//	deferred           â€” "true" to emit after HTTP response is committed (default false)
func (c *Compiler) compileEmitEvent(step StepConfig) error {
	if c.IngestPipeline == nil {
		// Pipeline disabled â€” compile a no-op instruction so flow structure is preserved.
		c.GlobalTable = append(c.GlobalTable, steps.EmitEvent(steps.EmitEventConfig{
			Pipeline:         nil,
			InputTokensSlot:  -1,
			OutputTokensSlot: -1,
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

	inputTokensSlot := -1
	if v, ok := step.Input["input_tokens_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("emit_event input_tokens_slot: %w", err)
		}
		inputTokensSlot = s
	}

	outputTokensSlot := -1
	if v, ok := step.Input["output_tokens_slot"]; ok && v != "" {
		s, err := c.getSlot(v)
		if err != nil {
			return fmt.Errorf("emit_event output_tokens_slot: %w", err)
		}
		outputTokensSlot = s
	}

	deferred := step.Input["deferred"] == "true"

	c.GlobalTable = append(c.GlobalTable, steps.EmitEvent(steps.EmitEventConfig{
		Pipeline:         c.IngestPipeline,
		Kind:             kind,
		PayloadSlot:      payloadSlot,
		ModelSlot:        modelSlot,
		SessionSlot:      sessionSlot,
		InputTokensSlot:  inputTokensSlot,
		OutputTokensSlot: outputTokensSlot,
		Deferred:         deferred,
	}))
	return nil
}

// compileFlowLog handles the "log" step type.
//
// Step input keys:
//
//	level   â€” log level string: debug|info|warn|error (default: info)
//	message â€” log message text (required)
//	field.* â€” any input key prefixed with "field." is treated as a field.
//	           Static literal values are baked into the pre-built JSON fragments.
//	           Dynamic values prefixed with "var." resolve from a ByteSlot at runtime.
func (c *Compiler) compileFlowLog(step StepConfig) error {
	msg := step.Input["message"]
	if msg == "" {
		return fmt.Errorf("log: 'message' is required")
	}

	lvl := gatewaylog.ParseLevel(step.Input["level"])

	// Separate static fields from dynamic (var.) fields.
	var staticFields []gatewaylog.Field
	var dynamicFields []steps.DynamicLogField

	for k, v := range step.Input {
		if !strings.HasPrefix(k, "field.") {
			continue
		}
		fieldName := k[6:]
		if strings.HasPrefix(v, "var.") {
			slotIdx, err := c.getSlot(v)
			if err != nil {
				return fmt.Errorf("log field %q: %w", fieldName, err)
			}
			// Pre-build the JSON key fragment: ,"fieldName":"
			jsonKey := []byte(`,"` + jsonEscapeString(fieldName) + `":"`)
			dynamicFields = append(dynamicFields, steps.DynamicLogField{
				JSONKey: jsonKey,
				SlotIdx: slotIdx,
			})
		} else {
			staticFields = append(staticFields, gatewaylog.F(fieldName, v))
		}
	}

	// Build static JSON payload in three parts:
	//   PayloadPrefix = {"level":"...","message":"...","fields":{...static fields...},"tenant_id":
	//   PayloadMid    = ,"api_id":
	//   PayloadSuffix = }
	// Dynamic fields are inserted between PayloadMid+apiID and PayloadSuffix at runtime.

	// Build the fields object (static fields only).
	var fieldsBuf []byte
	if len(staticFields) > 0 {
		fieldsBuf = append(fieldsBuf, '{')
		for i, f := range staticFields {
			if i > 0 {
				fieldsBuf = append(fieldsBuf, ',')
			}
			fieldsBuf = append(fieldsBuf, '"')
			fieldsBuf = append(fieldsBuf, []byte(jsonEscapeString(f.Key))...)
			fieldsBuf = append(fieldsBuf, '"', ':', '"')
			fieldsBuf = append(fieldsBuf, []byte(jsonEscapeString(f.Value))...)
			fieldsBuf = append(fieldsBuf, '"')
		}
		fieldsBuf = append(fieldsBuf, '}')
	}

	var prefix []byte
	prefix = append(prefix, `{"level":"`...)
	prefix = append(prefix, []byte(lvl.String())...)
	prefix = append(prefix, `","message":"`...)
	prefix = append(prefix, []byte(jsonEscapeString(msg))...)
	prefix = append(prefix, '"')
	if len(fieldsBuf) > 0 {
		prefix = append(prefix, `,"fields":`...)
		prefix = append(prefix, fieldsBuf...)
	}
	prefix = append(prefix, `,"tenant_id":`...)

	mid := []byte(`,"api_id":`)
	suffix := []byte(`}`)

	// Compute max buffer size for the pool.
	// Static part is known exactly; each dynamic field adds key + up to 256 bytes
	// of value with JSON escape overhead (2x worst case = 512) + closing quote.
	const maxDynFieldValLen = 256
	const jsonEscapeOverhead = 2
	maxLen := len(prefix) + 5 + // tenant_id: uint16 max 5 digits
		len(mid) + 10 + // api_id: uint32 max 10 digits
		len(dynamicFields)*(64+maxDynFieldValLen*jsonEscapeOverhead+1) +
		len(suffix)

	pool := &sync.Pool{New: func() any {
		b := make([]byte, 0, maxLen)
		return b
	}}

	c.GlobalTable = append(c.GlobalTable, steps.FlowLog(steps.FlowLogConfig{
		Level:         lvl,
		GatewayMsg:    msg,
		PayloadPrefix: prefix,
		PayloadMid:    mid,
		PayloadSuffix: suffix,
		DynamicFields: dynamicFields,
		BufPool:       pool,
	}))
	return nil
}

// jsonEscapeString escapes a string for use inside a JSON string value
// (without surrounding quotes).
func jsonEscapeString(s string) string {
	b, _ := json.Marshal(s)
	// json.Marshal wraps in quotes; strip them.
	return string(b[1 : len(b)-1])
}
