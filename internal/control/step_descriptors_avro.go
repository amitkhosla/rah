package control

// AvroStepDescriptors returns descriptors for all Avro-related step types.
// These are merged into AllStepDescriptors() to appear in the Studio palette
// and provide documentation for each avro_* action.
func AvroStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type: "avro_to_json", Title: "Avro to JSON", Category: "encoding", Capability: "transcode",
			Description: "Decode Avro binary to JSON using a compiled schema.",
			Defaults:    map[string]string{"src_slot": "avro_data", "dst_slot": "json_output"},
			Fields: []StepField{
				sf("src_slot", "Source slot", "ByteSlot containing Avro binary data", "avro_data"),
				sf("schema", "Schema", "Avro JSON schema string or reference", ""),
				sf("dst_slot", "Destination slot", "ByteSlot to store decoded JSON", "json_output"),
			},
		},
		{
			Type: "json_to_avro", Title: "JSON to Avro", Category: "encoding", Capability: "transcode",
			Description: "Encode JSON to Avro binary using a compiled schema.",
			Defaults:    map[string]string{"src_slot": "json_data", "dst_slot": "avro_output"},
			Fields: []StepField{
				sf("src_slot", "Source slot", "ByteSlot containing JSON data", "json_data"),
				sf("schema", "Schema", "Avro JSON schema string or reference", ""),
				sf("dst_slot", "Destination slot", "ByteSlot to store encoded Avro binary", "avro_output"),
			},
		},
		{
			Type: "avro_get", Title: "Avro Get Field", Category: "extraction", Capability: "extract",
			Description: "Extract a single field from Avro binary using a static path or dynamic path_slot.",
			Defaults:    map[string]string{"src_slot": "avro_data", "path": "user.name", "dst_slot": "field_value"},
			Fields: []StepField{
				sf("src_slot", "Source slot", "ByteSlot containing Avro binary", "avro_data"),
				sf("schema", "Schema", "Avro JSON schema string or reference", ""),
				sf("path", "Field path", "Static dot-path to extract (e.g. 'user.address.city'); omit if using path_slot", "user.name"),
				sf("path_slot", "Path slot", "ByteSlot containing the field path at runtime; use if path is dynamic", ""),
				sf("dst_slot", "Destination slot", "ByteSlot to store extracted value", "field_value"),
			},
		},
		{
			Type: "avro_to_xml", Title: "Avro to XML", Category: "conversion", Capability: "transcode",
			Description: "Convert Avro binary to XML via JSON intermediate (multi-hop).",
			Defaults:    map[string]string{"src_slot": "avro_data", "dst_slot": "xml_output"},
			Fields: []StepField{
				sf("src_slot", "Source slot", "ByteSlot containing Avro binary", "avro_data"),
				sf("avro_schema", "Avro schema", "Avro JSON schema string", ""),
				sf("dst_slot", "Destination slot", "ByteSlot to store XML output", "xml_output"),
			},
		},
		{
			Type: "xml_to_avro", Title: "XML to Avro", Category: "conversion", Capability: "transcode",
			Description: "Convert XML to Avro binary via JSON intermediate (multi-hop).",
			Defaults:    map[string]string{"src_slot": "xml_data", "dst_slot": "avro_output"},
			Fields: []StepField{
				sf("src_slot", "Source slot", "ByteSlot containing XML data", "xml_data"),
				sf("avro_schema", "Avro schema", "Avro JSON schema string", ""),
				sf("dst_slot", "Destination slot", "ByteSlot to store Avro binary", "avro_output"),
			},
		},
		{
			Type: "avro_to_proto", Title: "Avro to Proto", Category: "conversion", Capability: "transcode",
			Description: "Convert Avro binary to Protobuf message (S8 — not yet implemented).",
			Defaults:    map[string]string{},
			Fields:      []StepField{},
		},
		{
			Type: "proto_to_avro", Title: "Proto to Avro", Category: "conversion", Capability: "transcode",
			Description: "Convert Protobuf message to Avro binary (S8 — not yet implemented).",
			Defaults:    map[string]string{},
			Fields:      []StepField{},
		},
	}
}
