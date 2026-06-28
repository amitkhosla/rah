package control

// ConvertStepDescriptors returns Studio palette descriptors for the multi-hop
// format-conversion steps introduced in Session S8.
// These are registered by AllStepDescriptors() in step_descriptors.go.
func ConvertStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "avro_to_proto",
			Title:       "Avro to Proto",
			Description: "Convert Avro binary to Proto binary via a JSON pivot buffer. Requires an Avro schema and a registered proto descriptor set.",
			Category:    "format",
			Capability:  "convert",
			Defaults: map[string]string{
				"src_slot":       "avro_in",
				"dst_slot":       "proto_out",
				"avro_schema":    "",
				"descriptor_set": "",
				"message":        "",
			},
			Fields: []StepField{
				sf("src_slot", "Source slot", "Variable holding the Avro binary to convert", "avro_in"),
				sf("dst_slot", "Destination slot", "Variable to store the resulting Proto binary", "proto_out"),
				sf("avro_schema", "Avro schema", "Avro JSON schema for the source binary", `{"type":"record","name":"MyRecord","fields":[]}`),
				sf("descriptor_set", "Descriptor set", "Name of the registered FileDescriptorSet containing the target message", "my_service"),
				sf("message", "Message name", "Fully-qualified Proto message name (e.g. com.example.MyMessage)", "com.example.MyMessage"),
			},
		},
		{
			Type:        "proto_to_avro",
			Title:       "Proto to Avro",
			Description: "Convert Proto binary to Avro binary via a JSON pivot buffer. Requires a registered proto descriptor set and an Avro schema.",
			Category:    "format",
			Capability:  "convert",
			Defaults: map[string]string{
				"src_slot":       "proto_in",
				"dst_slot":       "avro_out",
				"descriptor_set": "",
				"message":        "",
				"avro_schema":    "",
			},
			Fields: []StepField{
				sf("src_slot", "Source slot", "Variable holding the Proto binary to convert", "proto_in"),
				sf("dst_slot", "Destination slot", "Variable to store the resulting Avro binary", "avro_out"),
				sf("descriptor_set", "Descriptor set", "Name of the registered FileDescriptorSet containing the source message", "my_service"),
				sf("message", "Message name", "Fully-qualified Proto message name (e.g. com.example.MyMessage)", "com.example.MyMessage"),
				sf("avro_schema", "Avro schema", "Avro JSON schema for the destination binary", `{"type":"record","name":"MyRecord","fields":[]}`),
			},
		},
	}
}
