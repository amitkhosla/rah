package control

import (
	"fmt"

	"github.com/amitkhosla/rah/internal/avro"
	"github.com/amitkhosla/rah/internal/engine/steps"
	"github.com/amitkhosla/rah/internal/xml"
)

// compileAvroToJSON handles the "avro_to_json" step type.
//
// Step input keys:
//
//	src_slot  — var name whose ByteSlot contains Avro binary (required)
//	schema    — Avro JSON schema string (required)
//	dst_slot  — var name whose ByteSlot will receive JSON output (required)
func (c *Compiler) compileAvroToJSON(step StepConfig) error {
	schemaJSON := step.Input["schema"]
	if schemaJSON == "" {
		return fmt.Errorf("avro_to_json: 'schema' is required")
	}

	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("avro_to_json: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("avro_to_json: src_slot: %w", err)
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("avro_to_json: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("avro_to_json: dst_slot: %w", err)
	}

	decProg, err := avro.Compile(schemaJSON, nil)
	if err != nil {
		return fmt.Errorf("avro_to_json: schema compile: %w", err)
	}

	instr, err := steps.NewAvroToJSONStep(srcSlot, dstSlot, decProg)
	if err != nil {
		return err
	}
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileJSONToAvro handles the "json_to_avro" step type.
//
// Step input keys:
//
//	src_slot  — var name whose ByteSlot contains JSON (required)
//	schema    — Avro JSON schema string (required)
//	dst_slot  — var name whose ByteSlot will receive Avro binary (required)
func (c *Compiler) compileJSONToAvro(step StepConfig) error {
	schemaJSON := step.Input["schema"]
	if schemaJSON == "" {
		return fmt.Errorf("json_to_avro: 'schema' is required")
	}

	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("json_to_avro: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("json_to_avro: src_slot: %w", err)
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("json_to_avro: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("json_to_avro: dst_slot: %w", err)
	}

	encProg, err := avro.Compile(schemaJSON, nil)
	if err != nil {
		return fmt.Errorf("json_to_avro: schema compile: %w", err)
	}

	instr, err := steps.NewJSONToAvroStep(srcSlot, dstSlot, encProg)
	if err != nil {
		return err
	}
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileAvroGet handles the "avro_get" step type.
//
// Step input keys:
//
//	src_slot   — var name whose ByteSlot contains Avro binary (required)
//	schema     — Avro JSON schema string (required)
//	path       — static dot-path to extract (use if path is known at bake time)
//	path_slot  — var name whose ByteSlot holds the path at runtime (use for dynamic paths)
//	dst_slot   — var name whose ByteSlot will receive the extracted value (required)
//
// Exactly one of 'path' or 'path_slot' must be set.
func (c *Compiler) compileAvroGet(step StepConfig) error {
	schemaJSON := step.Input["schema"]
	if schemaJSON == "" {
		return fmt.Errorf("avro_get: 'schema' is required")
	}

	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("avro_get: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("avro_get: src_slot: %w", err)
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("avro_get: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("avro_get: dst_slot: %w", err)
	}

	staticPath := step.Input["path"]
	pathSlotName := step.Input["path_slot"]

	if staticPath == "" && pathSlotName == "" {
		return fmt.Errorf("avro_get: either 'path' or 'path_slot' must be set")
	}
	if staticPath != "" && pathSlotName != "" {
		return fmt.Errorf("avro_get: 'path' and 'path_slot' are mutually exclusive")
	}

	// Always compile a full-decode program (needed for dynamic path and as fallback)
	fullDecProg, err := avro.Compile(schemaJSON, nil)
	if err != nil {
		return fmt.Errorf("avro_get: schema compile: %w", err)
	}

	var partialProg *avro.AvroProgram
	pathSlot := -1

	if pathSlotName != "" {
		// Dynamic path: resolve the slot; partialProg stays nil
		slot, err := c.getSlot(pathSlotName)
		if err != nil {
			return fmt.Errorf("avro_get: path_slot: %w", err)
		}
		pathSlot = slot
	} else {
		// Static path: compile a partial program targeting only that field
		slotMap := map[string]int{staticPath: 0}
		partialProg, err = avro.Compile(schemaJSON, slotMap)
		if err != nil {
			return fmt.Errorf("avro_get: partial schema compile: %w", err)
		}
		// pathSlot remains -1 to signal static mode
	}

	instr, err := steps.NewAvroGetStep(srcSlot, dstSlot, staticPath, pathSlot, partialProg, fullDecProg)
	if err != nil {
		return err
	}
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileAvroToXML handles the "avro_to_xml" step type.
//
// Step input keys:
//
//	src_slot     — var name whose ByteSlot contains Avro binary (required)
//	avro_schema  — Avro JSON schema string (required)
//	dst_slot     — var name whose ByteSlot will receive XML output (required)
//
// XML structure is derived from the JSON intermediate via the default build program.
func (c *Compiler) compileAvroToXML(step StepConfig) error {
	schemaJSON := step.Input["avro_schema"]
	if schemaJSON == "" {
		return fmt.Errorf("avro_to_xml: 'avro_schema' is required")
	}

	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("avro_to_xml: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("avro_to_xml: src_slot: %w", err)
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("avro_to_xml: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("avro_to_xml: dst_slot: %w", err)
	}

	decProg, err := avro.Compile(schemaJSON, nil)
	if err != nil {
		return fmt.Errorf("avro_to_xml: schema compile: %w", err)
	}

	// Use zero-value XMLBuildProgram; AppendJSONToXML handles empty prog gracefully
	// (emits the JSON as-is or wraps it). Full DSL compilation is deferred to S8.
	buildProg := xml.XMLBuildProgram{}

	instr, err := steps.NewAvroToXMLStep(srcSlot, dstSlot, decProg, buildProg)
	if err != nil {
		return err
	}
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileXMLToAvro handles the "xml_to_avro" step type.
//
// Step input keys:
//
//	src_slot     — var name whose ByteSlot contains XML (required)
//	avro_schema  — Avro JSON schema string (required)
//	dst_slot     — var name whose ByteSlot will receive Avro binary (required)
//
// XML is converted to JSON intermediate via the default scan program before encoding.
func (c *Compiler) compileXMLToAvro(step StepConfig) error {
	schemaJSON := step.Input["avro_schema"]
	if schemaJSON == "" {
		return fmt.Errorf("xml_to_avro: 'avro_schema' is required")
	}

	srcSlotName := step.Input["src_slot"]
	if srcSlotName == "" {
		return fmt.Errorf("xml_to_avro: 'src_slot' is required")
	}
	srcSlot, err := c.getSlot(srcSlotName)
	if err != nil {
		return fmt.Errorf("xml_to_avro: src_slot: %w", err)
	}

	dstSlotName := step.Input["dst_slot"]
	if dstSlotName == "" {
		return fmt.Errorf("xml_to_avro: 'dst_slot' is required")
	}
	dstSlot, err := c.getSlot(dstSlotName)
	if err != nil {
		return fmt.Errorf("xml_to_avro: dst_slot: %w", err)
	}

	encProg, err := avro.Compile(schemaJSON, nil)
	if err != nil {
		return fmt.Errorf("xml_to_avro: schema compile: %w", err)
	}

	// Use zero-value XMLScanProgram; AppendXMLToJSON handles empty prog gracefully.
	// Full DSL compilation is deferred to S8.
	scanProg := xml.XMLScanProgram{}

	instr, err := steps.NewXMLToAvroStep(srcSlot, dstSlot, scanProg, encProg)
	if err != nil {
		return err
	}
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}
