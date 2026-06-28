package control

// SOAPStepDescriptors returns the step descriptors for SOAP-related steps.
func SOAPStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:           "soap_call",
			Title:          "SOAP Call",
			Category:       "protocol",
			Capability:     "soap",
			Description:    "Wraps an XML body in a SOAP envelope (1.1 or 1.2), calls the upstream HTTP endpoint, and unwraps the response body. On a SOAP fault response the step sets ctx.Failed and parses fault details.",
			SupportsNested: false,
			Defaults: map[string]string{
				"url":                  "",
				"content_type":         "text/xml",
				"soap_version":         "1",
				"timeout_ms":           "5000",
				"max_retries":          "0",
				"response_body_slot":   "",
				"response_status_slot": "",
			},
			Fields: []StepField{
				sf("url", "Endpoint URL", "Static SOAP endpoint URL (leave empty to use a slot via url_slot)", "http://soap-service/endpoint"),
				sf("url_slot", "URL Slot", "Variable name whose slot holds the endpoint URL (use instead of url for dynamic values)", "soap_url_var"),
				sf("content_type", "Content-Type", "HTTP Content-Type header value. Defaults to text/xml for SOAP 1.1 and application/soap+xml for SOAP 1.2.", "text/xml"),
				sf("soap_version", "SOAP Version", "SOAP envelope version to use: 1 (SOAP 1.1, default) or 2 (SOAP 1.2).", "1"),
				sf("timeout_ms", "Timeout (ms)", "Request timeout in milliseconds (default: 5000).", "5000"),
				sf("max_retries", "Max Retries", "Number of retry attempts on transport error (default: 0 = no retry).", "0"),
				sf("response_body_slot", "Response Body Slot", "Variable name whose slot will store the unwrapped SOAP body from the response.", "soap_response_var"),
				sf("response_status_slot", "Response Status Slot", "Variable name whose int-slot will store the HTTP response status code.", "soap_status_var"),
			},
		},
		{
			Type:           "parse_soap_fault",
			Title:          "Parse SOAP Fault",
			Category:       "protocol",
			Capability:     "soap",
			Description:    "Extracts the fault code and fault message from a SOAP fault envelope and stores them (combined as code:message) in a slot. Supports both SOAP 1.1 (faultcode/faultstring) and SOAP 1.2 (Code/Reason).",
			SupportsNested: false,
			Defaults: map[string]string{
				"soap_version": "1",
			},
			Fields: []StepField{
				sf("response_slot", "Response Slot", "Variable name whose slot holds the raw SOAP fault envelope bytes (required).", "soap_fault_body_var"),
				sf("fault_slot", "Fault Slot", "Variable name whose slot will store the extracted fault as code:message (required).", "soap_fault_var"),
				sf("soap_version", "SOAP Version", "SOAP fault format: 1 for SOAP 1.1 faultcode/faultstring, 2 for SOAP 1.2 Code/Reason (default: 1).", "1"),
			},
		},
		{
			Type:           "build_soap_envelope",
			Title:          "Build SOAP Envelope",
			Category:       "protocol",
			Capability:     "soap",
			Description:    "Wraps an XML body in a SOAP 1.1 or 1.2 envelope at flow time. Useful when you need to construct the envelope separately before sending it via an http_call or soap_call step.",
			SupportsNested: false,
			Defaults: map[string]string{
				"soap_version": "1",
			},
			Fields: []StepField{
				sf("body_slot", "Body Slot", "Variable name whose slot holds the XML body to wrap (required).", "xml_body_var"),
				sf("output_slot", "Output Slot", "Variable name whose slot will store the resulting SOAP envelope bytes (required).", "soap_envelope_var"),
				sf("soap_version", "SOAP Version", "Envelope format: 1 for SOAP 1.1 (default) or 2 for SOAP 1.2.", "1"),
			},
		},
	}
}
