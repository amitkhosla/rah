package control

// ProtoCodecStepDescriptors returns the step descriptors for proto encoding/decoding:
// proto_to_json, json_to_proto, proto_get, xml_to_proto, proto_to_xml.
//
// These are merged into AllStepDescriptors via the append block at the bottom of step_descriptors.go.
func ProtoCodecStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "proto_to_json",
			Title:       "Proto to JSON",
			Description: "Convert proto binary (from a variable) to JSON. Requires a descriptor set and message type to be specified.",
			Category:    "encoding",
			Capability:  "transcode",
			Defaults: map[string]string{
				"source":          "var.proto_data",
				"descriptor_set":  "",
				"message":         "",
				"as":              "json_output",
			},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing proto binary data", "var.proto_data"),
				sf("descriptor_set", "Descriptor set name", "Name of the loaded gRPC descriptor set", ""),
				sf("message", "Message type", "Fully-qualified proto message name (e.g. com.example.User)", ""),
				sf("as", "Store as", "Variable to save the JSON output into", "json_output"),
			},
		},
		{
			Type:        "json_to_proto",
			Title:       "JSON to Proto",
			Description: "Convert JSON (from a variable) to proto binary. Unmarshals and marshals with proto message pooling for efficiency.",
			Category:    "encoding",
			Capability:  "transcode",
			Defaults: map[string]string{
				"source":          "var.json_data",
				"descriptor_set":  "",
				"message":         "",
				"as":              "proto_output",
			},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing JSON data", "var.json_data"),
				sf("descriptor_set", "Descriptor set name", "Name of the loaded gRPC descriptor set", ""),
				sf("message", "Message type", "Fully-qualified proto message name (e.g. com.example.User)", ""),
				sf("as", "Store as", "Variable to save the proto binary output into", "proto_output"),
			},
		},
		{
			Type:        "proto_get",
			Title:       "Proto Get Field",
			Description: "Extract a field from proto binary using a compile-time or runtime field path. Static paths are optimized; dynamic paths use full unmarshal.",
			Category:    "encoding",
			Capability:  "transcode",
			Defaults: map[string]string{
				"source":         "var.proto_data",
				"descriptor_set": "",
				"message":        "",
				"path":           "user.name",
				"as":             "field_value",
			},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing proto binary data", "var.proto_data"),
				sf("descriptor_set", "Descriptor set name", "Name of the loaded gRPC descriptor set", ""),
				sf("message", "Message type", "Fully-qualified proto message name (e.g. com.example.User)", ""),
				sf("path", "Field path (static)", "Compile-time field path (e.g. user.name). Leave empty if using path_var.", "user.name"),
				sf("path_var", "Field path (dynamic)", "Variable containing field path at runtime. Use if path changes per request.", ""),
				sf("as", "Store as", "Variable to save the extracted field value into", "field_value"),
			},
		},
		{
			Type:        "xml_to_proto",
			Title:       "XML to Proto",
			Description: "Convert XML to proto binary via JSON intermediate. Requires descriptor set and message type. Multi-hop conversion.",
			Category:    "encoding",
			Capability:  "transcode",
			Defaults: map[string]string{
				"source":          "var.xml_data",
				"descriptor_set":  "",
				"message":         "",
				"as":              "proto_output",
			},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing XML data", "var.xml_data"),
				sf("descriptor_set", "Descriptor set name", "Name of the loaded gRPC descriptor set", ""),
				sf("message", "Message type", "Fully-qualified proto message name (e.g. com.example.User)", ""),
				sf("as", "Store as", "Variable to save the proto binary output into", "proto_output"),
			},
		},
		{
			Type:        "proto_to_xml",
			Title:       "Proto to XML",
			Description: "Convert proto binary to XML via JSON intermediate. Requires descriptor set and message type. Multi-hop conversion.",
			Category:    "encoding",
			Capability:  "transcode",
			Defaults: map[string]string{
				"source":          "var.proto_data",
				"descriptor_set":  "",
				"message":         "",
				"as":              "xml_output",
			},
			Fields: []StepField{
				sf("source", "Source variable", "Variable containing proto binary data", "var.proto_data"),
				sf("descriptor_set", "Descriptor set name", "Name of the loaded gRPC descriptor set", ""),
				sf("message", "Message type", "Fully-qualified proto message name (e.g. com.example.User)", ""),
				sf("as", "Store as", "Variable to save the XML output into", "xml_output"),
			},
		},
	}
}
