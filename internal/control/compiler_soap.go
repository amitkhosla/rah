package control

import (
	"fmt"
	"rah/internal/engine"
	"rah/internal/engine/steps"
)

// compileSOAPCall compiles a soap_call step.
// Config:
//   soap_call:
//     url: "http://soap-service/endpoint"  # static or use url_slot
//     url_slot: 5                          # -1 or omitted = static
//     body_slot: 0                         # required
//     response_body_slot: 1                # where to store unwrapped response
//     response_status_slot: 2              # optional, where to store HTTP status
//     timeout_ms: 5000                     # optional, defaults to global http.request_timeout_ms
//     max_retries: 2                       # optional
//     content_type: "text/xml"             # optional, defaults to text/xml for SOAP 1.1
//     soap_version: 1 or 2                 # optional, defaults to 1
func (c *Compiler) compileSOAPCall(input StepConfig, flowInput map[string]string) (engine.Instruction, error) {
	cfg := steps.SOAPCallConfig{
		FlowInput:      flowInput,
		MaxRetries:     -1, // Use config default
		soapVersion:    1,  // Default to SOAP 1.1
	}

	// Static URL
	if url, ok := input.Input["url"].(string); ok && url != "" {
		cfg.StaticURL = url
		cfg.URLSlot = -1
	}

	// URL slot
	if slot, ok := input.Input["url_slot"].(float64); ok {
		cfg.URLSlot = int(slot)
		cfg.StaticURL = ""
	}

	// Body slot (required)
	if slot, ok := input.Input["body_slot"].(float64); ok {
		cfg.BodySlot = int(slot)
	} else {
		return engine.Instruction{}, fmt.Errorf("soap_call: body_slot is required")
	}

	// Response body slot
	if slot, ok := input.Input["response_body_slot"].(float64); ok {
		cfg.ResponseBodySlot = int(slot)
	} else {
		cfg.ResponseBodySlot = -1
	}

	// Response status slot
	if slot, ok := input.Input["response_status_slot"].(float64); ok {
		cfg.ResponseStatusSlot = int(slot)
	} else {
		cfg.ResponseStatusSlot = -1
	}

	// Content type
	if ct, ok := input.Input["content_type"].(string); ok && ct != "" {
		cfg.StaticContentType = ct
	} else {
		cfg.StaticContentType = "text/xml"
	}

	// Timeout
	if timeoutMs, ok := input.Input["timeout_ms"].(float64); ok && timeoutMs > 0 {
		cfg.Timeout = uint32(timeoutMs)
	}

	// Max retries
	if maxRetries, ok := input.Input["max_retries"].(float64); ok {
		cfg.MaxRetries = int(maxRetries)
	}

	// SOAP version
	if version, ok := input.Input["soap_version"].(float64); ok && (version == 1 || version == 2) {
		cfg.soapVersion = uint8(version)
	}

	// Egress profile resolution (if available)
	if c.EgressMgr != nil {
		// Check if a profile name is specified
		if profileName, ok := input.Input["egress_profile"].(string); ok && profileName != "" {
			profile, err := c.EgressMgr.GetProfile(profileName)
			if err != nil {
				return engine.Instruction{}, fmt.Errorf("soap_call: %w", err)
			}
			cfg.EgressProfile = profile
		}
	}

	// Compute envelope size hint
	cfg = steps.BuildSOAPEnvelopeConfig(cfg)

	return steps.SOAPCallFromConfig(cfg), nil
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
	if slot, ok := input.Input["response_slot"].(float64); ok {
		cfg.ResponseSlot = int(slot)
	} else {
		return engine.Instruction{}, fmt.Errorf("soap_parse_fault: response_slot is required")
	}

	// Fault slot (required)
	if slot, ok := input.Input["fault_slot"].(float64); ok {
		cfg.FaultSlot = int(slot)
	} else {
		return engine.Instruction{}, fmt.Errorf("soap_parse_fault: fault_slot is required")
	}

	// SOAP version
	if version, ok := input.Input["soap_version"].(float64); ok && (version == 1 || version == 2) {
		cfg.Version = uint8(version)
	}

	return steps.ParseSOAPFaultFromConfig(cfg), nil
}
