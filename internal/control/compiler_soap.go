package control

import (
	"fmt"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileSOAPCall compiles a soap_call step.
// Config:
//   soap_call:
//     url: "http://soap-service/endpoint"  # static or use url_slot
//     body_slot: 0                         # required
//     response_body_slot: 1                # where to store unwrapped response
//     response_status_slot: 2              # optional, where to store HTTP status
//     timeout_ms: 5000                     # optional, defaults to global http.request_timeout_ms
//     max_retries: 2                       # optional
//     content_type: "text/xml"             # optional, defaults to text/xml for SOAP 1.1
//     soap_version: 1 or 2                 # optional, defaults to 1
func (c *Compiler) compileSOAPCall(input StepConfig) (engine.Instruction, error) {
	if input.Input == nil {
		input.Input = make(map[string]string)
	}

	cfg := steps.SOAPCallConfig{
		MaxRetries: -1, // Use config default
	}

	// Static URL
	if url := input.Input["url"]; url != "" {
		cfg.StaticURL = url
		cfg.URLSlot = -1
	}

	// Body slot (required) — use As field
	if input.As != "" {
		var err error
		cfg.BodySlot, err = c.getSlot(input.As)
		if err != nil {
			return engine.Instruction{}, fmt.Errorf("soap_call: %w", err)
		}
	} else {
		return engine.Instruction{}, fmt.Errorf("soap_call: 'as' field is required")
	}

	// Response body slot
	if respBodySlot := input.Input["response_body_slot"]; respBodySlot != "" {
		var err error
		cfg.ResponseBodySlot, err = c.getSlot(respBodySlot)
		if err != nil {
			cfg.ResponseBodySlot = -1
		}
	} else {
		cfg.ResponseBodySlot = -1
	}

	// Response status slot
	if respStatusSlot := input.Input["response_status_slot"]; respStatusSlot != "" {
		var err error
		cfg.ResponseStatusSlot, err = c.getSlot(respStatusSlot)
		if err != nil {
			cfg.ResponseStatusSlot = -1
		}
	} else {
		cfg.ResponseStatusSlot = -1
	}

	// Content type
	if ct := input.Input["content_type"]; ct != "" {
		cfg.StaticContentType = ct
	} else {
		cfg.StaticContentType = "text/xml"
	}

	// Timeout (parse from string)
	if timeoutStr := input.Input["timeout_ms"]; timeoutStr != "" {
		var timeout int
		if _, err := fmt.Sscanf(timeoutStr, "%d", &timeout); err == nil && timeout > 0 {
			cfg.Timeout = uint32(timeout)
		}
	}

	// Max retries (parse from string)
	if retriesStr := input.Input["max_retries"]; retriesStr != "" {
		var retries int
		if _, err := fmt.Sscanf(retriesStr, "%d", &retries); err == nil {
			cfg.MaxRetries = retries
		}
	}

	return steps.SOAPCallFromConfig(cfg), nil
}

// compileBuildSOAPEnvelope compiles a build_soap_envelope step.
// Config:
//
//	build_soap_envelope:
//	  body_slot: xml_body_var    # required — slot holding XML body to wrap
//	  output_slot: envelope_var  # required — slot to receive resulting SOAP envelope
//	  soap_version: 1            # optional, 1 = SOAP 1.1 (default), 2 = SOAP 1.2
func (c *Compiler) compileBuildSOAPEnvelope(input StepConfig) (engine.Instruction, error) {
	if input.Input == nil {
		input.Input = make(map[string]string)
	}

	cfg := steps.BuildSOAPEnvelopeStepConfig{
		SoapVersion: 1,
	}

	// Body slot (required)
	if bodySlot := input.Input["body_slot"]; bodySlot != "" {
		var err error
		cfg.BodySlot, err = c.getSlot(bodySlot)
		if err != nil {
			return engine.Instruction{}, fmt.Errorf("build_soap_envelope: %w", err)
		}
	} else {
		return engine.Instruction{}, fmt.Errorf("build_soap_envelope: body_slot is required")
	}

	// Output slot (required)
	if outputSlot := input.Input["output_slot"]; outputSlot != "" {
		var err error
		cfg.OutputSlot, err = c.getSlot(outputSlot)
		if err != nil {
			return engine.Instruction{}, fmt.Errorf("build_soap_envelope: %w", err)
		}
	} else {
		return engine.Instruction{}, fmt.Errorf("build_soap_envelope: output_slot is required")
	}

	// SOAP version
	if versionStr := input.Input["soap_version"]; versionStr != "" {
		var version int
		if _, err := fmt.Sscanf(versionStr, "%d", &version); err == nil && (version == 1 || version == 2) {
			cfg.SoapVersion = uint8(version)
		}
	}

	return steps.BuildSOAPEnvelopeFromConfig(cfg), nil
}

// compileSOAPParseFault compiles a soap_parse_fault step.
// Extracts fault code and message from SOAP fault response.
// Config:
//   soap_parse_fault:
//     response_slot: 1                    # slot containing SOAP envelope
//     fault_code_slot: 3                  # where to store fault code
//     fault_message_slot: 4               # where to store fault message
//     soap_version: 1 or 2                # optional, defaults to 1
func (c *Compiler) compileSOAPParseFault(input StepConfig) (engine.Instruction, error) {
	cfg := steps.ParseSOAPFaultConfig{
		Version: 1,
	}

	// Response slot (required)
	if respSlot := input.Input["response_slot"]; respSlot != "" {
		var err error
		cfg.ResponseSlot, err = c.getSlot(respSlot)
		if err != nil {
			return engine.Instruction{}, fmt.Errorf("soap_parse_fault: %w", err)
		}
	} else {
		return engine.Instruction{}, fmt.Errorf("soap_parse_fault: response_slot is required")
	}

	// Fault slot (required)
	if faultSlot := input.Input["fault_slot"]; faultSlot != "" {
		var err error
		cfg.FaultSlot, err = c.getSlot(faultSlot)
		if err != nil {
			return engine.Instruction{}, fmt.Errorf("soap_parse_fault: %w", err)
		}
	} else {
		return engine.Instruction{}, fmt.Errorf("soap_parse_fault: fault_slot is required")
	}

	// SOAP version (parse from string)
	if versionStr := input.Input["soap_version"]; versionStr != "" {
		var version int
		if _, err := fmt.Sscanf(versionStr, "%d", &version); err == nil && (version == 1 || version == 2) {
			cfg.Version = uint8(version)
		}
	}

	return steps.ParseSOAPFaultFromConfig(cfg), nil
}
