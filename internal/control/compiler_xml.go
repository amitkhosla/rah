package control

import (
	"fmt"
	"strconv"
	"strings"

	"rah/internal/engine/steps"
	"rah/internal/xml"
)

// compileXMLToJSON handles the "xml_to_json" step type.
//
// Step input keys:
//
//	src_slot — var name whose ByteSlot contains XML bytes (required)
//	fields   — comma-separated list of element names to extract; optional.
//	           Each entry is "elementName" or "elementName:jsonKey" or
//	           "elementName:jsonKey:N" (N = 0-based occurrence index).
//	           If empty, a single-op wildcard scan is emitted using the
//	           element name "item" (passthrough for simple documents).
//	dst_slot — var name whose ByteSlot will receive JSON output (required)
func (c *Compiler) compileXMLToJSON(step StepConfig) error {
	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("xml_to_json: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("xml_to_json: src_slot: %w", err)
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("xml_to_json: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("xml_to_json: dst_slot: %w", err)
	}

	prog, err := buildXMLScanProgram(step.Input["fields"])
	if err != nil {
		return fmt.Errorf("xml_to_json: fields: %w", err)
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewXMLToJSONStep(srcSlot, dstSlot, prog))
	return nil
}

// compileJSONToXML handles the "json_to_xml" step type.
//
// Step input keys:
//
//	src_slot — var name whose ByteSlot contains JSON bytes (required)
//	fields   — comma-separated list of "jsonPath:openTag:closeTag" entries
//	           defining the XML structure. Example: "name:<name>:</name>,age:<age>:</age>".
//	           If empty, a minimal wrapper op "<root>" is used.
//	dst_slot — var name whose ByteSlot will receive XML output (required)
func (c *Compiler) compileJSONToXML(step StepConfig) error {
	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("json_to_xml: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("json_to_xml: src_slot: %w", err)
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("json_to_xml: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("json_to_xml: dst_slot: %w", err)
	}

	prog, err := buildXMLBuildProgramFromFields(step.Input["fields"])
	if err != nil {
		return fmt.Errorf("json_to_xml: fields: %w", err)
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewJSONToXMLStep(srcSlot, dstSlot, prog))
	return nil
}

// compileParseXML handles the "parse_xml" step type.
//
// Step input keys:
//
//	src_slot — var name whose ByteSlot contains XML bytes (required)
//	dst_slot — var name whose ByteSlot will receive validated XML (required;
//	           same bytes as src on success, nil on failure)
func (c *Compiler) compileParseXML(step StepConfig) error {
	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("parse_xml: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("parse_xml: src_slot: %w", err)
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("parse_xml: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("parse_xml: dst_slot: %w", err)
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewParseXMLStep(srcSlot, dstSlot))
	return nil
}

// compileXMLGet handles the "xml_get" step type.
//
// Step input keys:
//
//	src_slot — var name whose ByteSlot contains XML bytes (required)
//	path     — XML element name to extract (required; local name, case-sensitive)
//	dst_slot — var name whose ByteSlot will receive the extracted text content (required)
func (c *Compiler) compileXMLGet(step StepConfig) error {
	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("xml_get: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("xml_get: src_slot: %w", err)
	}

	path := step.Input["path"]
	if path == "" {
		return fmt.Errorf("xml_get: 'path' is required")
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("xml_get: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("xml_get: dst_slot: %w", err)
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewXMLGetStep(srcSlot, dstSlot, path))
	return nil
}

// compileSetXMLResponse handles the "set_xml_response" step type.
//
// Step input keys:
//
//	src_slot — var name whose ByteSlot contains XML bytes to send (required)
//	status   — HTTP status code as decimal string (optional; default 200)
func (c *Compiler) compileSetXMLResponse(step StepConfig) error {
	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("set_xml_response: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("set_xml_response: src_slot: %w", err)
	}

	defaultStatus := 200
	if statusStr := step.Input["status"]; statusStr != "" {
		code, err := strconv.Atoi(statusStr)
		if err != nil {
			return fmt.Errorf("set_xml_response: invalid status %q: %w", statusStr, err)
		}
		defaultStatus = code
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewSetXMLResponseStep(srcSlot, defaultStatus))
	return nil
}

// compileBuildXML handles the "build_xml" step type.
//
// Step input keys:
//
//	fields   — comma-separated list of "slotName:openTag:closeTag" triples.
//	           slotName is looked up as a ByteSlot; the slot index is baked
//	           into the XMLBuildOp. Example:
//	           "order_id:<orderId>:</orderId>,amount:<amount>:</amount>"
//	           A root wrapper can be added via root_open/root_close keys.
//	root_open  — open tag for root element (optional; e.g. "<Order>")
//	root_close — close tag for root element (optional; e.g. "</Order>")
//	dst_slot — var name whose ByteSlot will receive built XML (required)
func (c *Compiler) compileBuildXML(step StepConfig) error {
	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("build_xml: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("build_xml: dst_slot: %w", err)
	}

	fieldsStr := step.Input["fields"]
	if fieldsStr == "" {
		return fmt.Errorf("build_xml: 'fields' is required")
	}

	rootOpen := step.Input["root_open"]
	rootClose := step.Input["root_close"]

	prog, err := c.buildXMLBuildProgramFromSlotFields(fieldsStr, rootOpen, rootClose)
	if err != nil {
		return fmt.Errorf("build_xml: %w", err)
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewBuildXMLStep(dstSlot, prog))
	return nil
}

// compileXMLSet handles the "xml_set" step type.
//
// Step input keys:
//
//	src_slot   — var name whose ByteSlot contains the source XML (required)
//	path       — XML element name whose content is to be replaced (required)
//	value_slot — var name whose ByteSlot contains the new content bytes (required)
//	dst_slot   — var name whose ByteSlot will receive the modified XML (required)
func (c *Compiler) compileXMLSet(step StepConfig) error {
	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("xml_set: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("xml_set: src_slot: %w", err)
	}

	path := step.Input["path"]
	if path == "" {
		return fmt.Errorf("xml_set: 'path' is required")
	}

	valueSlotName := step.Input["value_slot"]
	if valueSlotName == "" {
		return fmt.Errorf("xml_set: 'value_slot' is required")
	}
	valueSlot, err := c.getSlot(valueSlotName)
	if err != nil {
		return fmt.Errorf("xml_set: value_slot: %w", err)
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("xml_set: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("xml_set: dst_slot: %w", err)
	}

	c.GlobalTable = append(c.GlobalTable, steps.NewXMLSetStep(srcSlot, valueSlot, dstSlot, path))
	return nil
}

// ─── program builder helpers ──────────────────────────────────────────────────

// buildXMLScanProgram builds an XMLScanProgram from a comma-separated fields string.
//
// Each field entry has one of these forms:
//
//	"name"             → scan element "name", emit JSON key "name":
//	"name:jsonKey"     → scan element "name", emit JSON key "jsonKey":
//	"name:jsonKey:N"   → scan element "name", Nth occurrence (0-based)
//	"name:jsonKey:N:array" → emit all matching elements as JSON array
//	"name:jsonKey:N:optional" → mark field as optional (omit vs. null)
//
// If fields is empty, an empty program is returned (AppendXMLToJSON returns "{}").
func buildXMLScanProgram(fields string) (xml.XMLScanProgram, error) {
	var b xml.XMLScanProgramBuilder
	if fields == "" {
		return b.Build(), nil
	}

	entries := strings.Split(fields, ",")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ":")
		tagName := parts[0]
		if tagName == "" {
			return xml.XMLScanProgram{}, fmt.Errorf("empty element name in entry %q", entry)
		}

		jsonKey := ""
		if len(parts) >= 2 && parts[1] != "" {
			jsonKey = `"` + parts[1] + `":`
		}

		index := 0
		if len(parts) >= 3 && parts[2] != "" {
			n, err := strconv.Atoi(parts[2])
			if err != nil {
				return xml.XMLScanProgram{}, fmt.Errorf("invalid index %q in entry %q: %w", parts[2], entry, err)
			}
			index = n
		}

		var flags uint8
		if len(parts) >= 4 {
			switch strings.ToLower(strings.TrimSpace(parts[3])) {
			case "array":
				flags |= xml.XMLFlagIsArray
			case "optional":
				flags |= xml.XMLFlagIsOptional
			case "attr":
				flags |= xml.XMLFlagIsAttr
			}
		}

		b.AddOp(tagName, jsonKey, index, flags)
	}
	return b.Build(), nil
}

// buildXMLBuildProgramFromFields builds an XMLBuildProgram from a comma-separated
// fields string where each entry is "jsonPath:openTag:closeTag".
// Used by the json_to_xml compiler method (all slots are irrelevant here;
// AppendJSONToXML reads values from the JSON source via gjson path).
//
// Example: "name:<name>:</name>,age:<age>:</age>"
//
// An optional root wrapper is not added here — add it via root_open/root_close
// in compileBuildXML. For json_to_xml the root comes from the JSON structure.
func buildXMLBuildProgramFromFields(fields string) (xml.XMLBuildProgram, error) {
	var b xml.XMLBuildProgramBuilder
	if fields == "" {
		// Minimal passthrough: a root element with slot 0.
		b.AddOp("<root>", "</root>", 0, 0)
		return b.Build(), nil
	}

	entries := strings.Split(fields, ",")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// Format: "jsonPath:openTag:closeTag"
		parts := strings.SplitN(entry, ":", 3)
		if len(parts) != 3 {
			return xml.XMLBuildProgram{}, fmt.Errorf("invalid field entry %q: expected jsonPath:openTag:closeTag", entry)
		}
		openTag := parts[1]
		closeTag := parts[2]
		if openTag == "" {
			return xml.XMLBuildProgram{}, fmt.Errorf("empty open tag in entry %q", entry)
		}
		// slotIdx 0 is unused for json_to_xml (gjson reads from src JSON).
		b.AddOp(openTag, closeTag, 0, 0)
	}
	return b.Build(), nil
}

// buildXMLBuildProgramFromSlotFields builds an XMLBuildProgram where leaf nodes
// read their values from ByteSlots. Used by the build_xml compiler method.
//
// fields format: "slotName:openTag:closeTag[,...]"
// rootOpen/rootClose: if non-empty, a parent op wraps all leaf ops.
func (c *Compiler) buildXMLBuildProgramFromSlotFields(fields, rootOpen, rootClose string) (xml.XMLBuildProgram, error) {
	var b xml.XMLBuildProgramBuilder

	entries := strings.Split(fields, ",")
	leafIndices := make([]int, 0, len(entries))

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 3)
		if len(parts) != 3 {
			return xml.XMLBuildProgram{}, fmt.Errorf("invalid field entry %q: expected slotName:openTag:closeTag", entry)
		}
		slotName := parts[0]
		openTag := parts[1]
		closeTag := parts[2]

		if slotName == "" {
			return xml.XMLBuildProgram{}, fmt.Errorf("empty slot name in entry %q", entry)
		}
		if openTag == "" {
			return xml.XMLBuildProgram{}, fmt.Errorf("empty open tag in entry %q", entry)
		}

		slotIdx, err := c.getSlot(slotName)
		if err != nil {
			return xml.XMLBuildProgram{}, fmt.Errorf("slot %q: %w", slotName, err)
		}

		opIdx := b.AddOp(openTag, closeTag, slotIdx, 0)
		leafIndices = append(leafIndices, opIdx)
	}

	if len(leafIndices) == 0 {
		return xml.XMLBuildProgram{}, fmt.Errorf("at least one field entry is required")
	}

	// If a root wrapper is specified, add a parent op and attach all leaves.
	if rootOpen != "" {
		parentIdx := b.AddOp(rootOpen, rootClose, 0, xml.XMLBuildFlagHasChildren)
		for _, leafIdx := range leafIndices {
			b.AddChild(parentIdx, leafIdx)
		}
	}

	return b.Build(), nil
}
