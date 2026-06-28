package control

// XMLStepDescriptors returns descriptors for all XML-related step types.
// These are merged into AllStepDescriptors() to appear in the Studio palette
// and provide documentation for each xml_* action.
func XMLStepDescriptors() []StepDescriptor {
	return []StepDescriptor{
		{
			Type:        "xml_to_json",
			Title:       "XML to JSON",
			Category:    "format",
			Capability:  "xml",
			Description: "Convert XML bytes from a slot to JSON using a compiled field mapping. Fields are comma-separated element names (e.g. 'orderId,amount'). Optional per-field JSON key and occurrence index supported.",
			Defaults:    map[string]string{"src_slot": "xml_input", "dst_slot": "json_output"},
			Fields: []StepField{
				sf("src_slot", "Source slot", "ByteSlot containing XML bytes to convert", "xml_input"),
				sf("fields", "Field mappings", "Comma-separated list of element names to extract. Each entry: 'name' or 'name:jsonKey' or 'name:jsonKey:N' or 'name:jsonKey:N:array'. Leave empty for an empty JSON object.", "orderId,amount,status"),
				sf("dst_slot", "Destination slot", "ByteSlot to store the resulting JSON bytes", "json_output"),
			},
		},
		{
			Type:        "json_to_xml",
			Title:       "JSON to XML",
			Category:    "format",
			Capability:  "xml",
			Description: "Convert JSON bytes from a slot to XML using a compiled field mapping. Fields are comma-separated 'jsonPath:openTag:closeTag' triples (e.g. 'name:<name>:</name>').",
			Defaults:    map[string]string{"src_slot": "json_input", "dst_slot": "xml_output"},
			Fields: []StepField{
				sf("src_slot", "Source slot", "ByteSlot containing JSON bytes to convert", "json_input"),
				sf("fields", "Field mappings", "Comma-separated list of 'jsonPath:<openTag>:</closeTag>' triples defining the XML structure. Leave empty for a minimal <root> wrapper.", "name:<name>:</name>,age:<age>:</age>"),
				sf("dst_slot", "Destination slot", "ByteSlot to store the resulting XML bytes", "xml_output"),
			},
		},
		{
			Type:        "parse_xml",
			Title:       "Parse / Validate XML",
			Category:    "format",
			Capability:  "xml",
			Description: "Validate XML structure (prolog check, DTD rejection). On success, stores the original XML bytes in dst_slot. Sets ctx.Failed on malformed or DTD-containing XML.",
			Defaults:    map[string]string{"src_slot": "raw_xml", "dst_slot": "xml_doc"},
			Fields: []StepField{
				sf("src_slot", "Source slot", "ByteSlot containing raw XML bytes", "raw_xml"),
				sf("dst_slot", "Destination slot", "ByteSlot to store validated XML bytes (same as source on success)", "xml_doc"),
			},
		},
		{
			Type:        "xml_get",
			Title:       "XML Get Element",
			Category:    "format",
			Capability:  "xml",
			Description: "Extract the text content of the first matching element from XML bytes. Sets ctx.Failed if the element is not found.",
			Defaults:    map[string]string{"src_slot": "xml_doc", "path": "orderId", "dst_slot": "order_id"},
			Fields: []StepField{
				sf("src_slot", "Source slot", "ByteSlot containing XML bytes to scan", "xml_doc"),
				sf("path", "Element name", "Local element name to find (case-insensitive scan, e.g. 'orderId')", "orderId"),
				sf("dst_slot", "Destination slot", "ByteSlot to store the extracted text content", "order_id"),
			},
		},
		{
			Type:        "set_xml_response",
			Title:       "Set XML Response",
			Category:    "format",
			Capability:  "xml",
			Description: "Write XML bytes from a slot to the response body and set Content-Type: application/xml. Sets response status to 200 if not already set.",
			Defaults:    map[string]string{"src_slot": "xml_output", "status": "200"},
			Fields: []StepField{
				sf("src_slot", "Source slot", "ByteSlot containing XML bytes to send as the response body", "xml_output"),
				sf("status", "HTTP status", "HTTP response status code (default: 200)", "200"),
			},
		},
		{
			Type:        "build_xml",
			Title:       "Build XML",
			Category:    "format",
			Capability:  "xml",
			Description: "Build an XML document from slot values using a baked structural program. Fields are 'slotName:<openTag>:</closeTag>' triples. Optional root_open/root_close wrap all leaf elements.",
			Defaults:    map[string]string{"dst_slot": "xml_output"},
			Fields: []StepField{
				sf("fields", "Field mappings", "Comma-separated 'slotName:<openTag>:</closeTag>' triples. Each slot value becomes the text content of the corresponding element.", "order_id:<orderId>:</orderId>,amount:<amount>:</amount>"),
				sf("root_open", "Root open tag", "Optional wrapping root element open tag (e.g. '<Order>')", "<Order>"),
				sf("root_close", "Root close tag", "Optional wrapping root element close tag (e.g. '</Order>')", "</Order>"),
				sf("dst_slot", "Destination slot", "ByteSlot to store the built XML bytes", "xml_output"),
			},
		},
		{
			Type:        "xml_set",
			Title:       "XML Set Element",
			Category:    "format",
			Capability:  "xml",
			Description: "Replace the text content of the first matching element in an XML document. Searches for the element by exact name and replaces its content with the value_slot bytes.",
			Defaults:    map[string]string{"src_slot": "xml_doc", "path": "status", "value_slot": "new_status", "dst_slot": "xml_updated"},
			Fields: []StepField{
				sf("src_slot", "Source slot", "ByteSlot containing the source XML bytes", "xml_doc"),
				sf("path", "Element name", "Local element name whose content to replace (exact case match)", "status"),
				sf("value_slot", "Value slot", "ByteSlot containing the new text content to inject", "new_status"),
				sf("dst_slot", "Destination slot", "ByteSlot to store the modified XML bytes", "xml_updated"),
			},
		},
	}
}
