package control

import (
	"encoding/json"
)

// SOAPStepDescriptors returns the SOAP-related step descriptors.
func SOAPStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Name:      "soap_call",
			Docs:      "Wrap request XML in SOAP envelope and POST to upstream service",
			Compiler:  (*Compiler).compileSOAPCall,
			JSONSchema: mustSchema(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "Static upstream SOAP service URL (http/https)",
					},
					"url_slot": map[string]interface{}{
						"type":        "integer",
						"description": "Slot containing dynamic URL (-1 to use static url)",
					},
					"body_slot": map[string]interface{}{
						"type":        "integer",
						"description": "Required: slot containing XML request body",
					},
					"response_body_slot": map[string]interface{}{
						"type":        "integer",
						"description": "Slot to store unwrapped SOAP response body (-1 to discard)",
					},
					"response_status_slot": map[string]interface{}{
						"type":        "integer",
						"description": "Slot to store HTTP response status (-1 to discard)",
					},
					"content_type": map[string]interface{}{
						"type":        "string",
						"description": "HTTP Content-Type header (default: text/xml for SOAP 1.1)",
					},
					"timeout_ms": map[string]interface{}{
						"type":        "integer",
						"description": "Request timeout in milliseconds",
					},
					"max_retries": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of retries (-1 for default)",
					},
					"soap_version": map[string]interface{}{
						"type":        "integer",
						"enum":        []int{1, 2},
						"description": "SOAP version (1 or 2, default: 1)",
					},
					"egress_profile": map[string]interface{}{
						"type":        "string",
						"description": "Named egress profile for protocol selection",
					},
				},
				"required": []string{"body_slot"},
			}),
		},
		{
			Name:      "soap_parse_fault",
			Docs:      "Extract SOAP fault details (code + message) from response",
			Compiler:  (*Compiler).compileSOAPParseFault,
			JSONSchema: mustSchema(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"response_slot": map[string]interface{}{
						"type":        "integer",
						"description": "Slot containing SOAP envelope with fault",
					},
					"fault_slot": map[string]interface{}{
						"type":        "integer",
						"description": "Slot to store combined fault code:message",
					},
					"soap_version": map[string]interface{}{
						"type":        "integer",
						"enum":        []int{1, 2},
						"description": "SOAP version (1 or 2, default: 1)",
					},
				},
				"required": []string{"response_slot", "fault_slot"},
			}),
		},
	}
}

func mustSchema(v map[string]interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
