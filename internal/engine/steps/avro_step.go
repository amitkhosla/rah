package steps

import (
	"fmt"
	"sync"

	"github.com/tidwall/gjson"
	"github.com/amitkhosla/rah/internal/avro"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
	"github.com/amitkhosla/rah/internal/transcode"
	"github.com/amitkhosla/rah/internal/xml"
)

// AvroToJSONConfig holds the state for avro_to_json instruction.
// Decodes Avro binary to JSON with zero allocations using ScratchPool.
type AvroToJSONConfig struct {
	SrcSlot     int               // source ByteSlot (Avro binary)
	DstSlot     int               // destination ByteSlot (JSON)
	DecProg     *avro.AvroProgram // compiled decoder program
	ScratchPool *sync.Pool        // *[]byte pool for intermediate decode buffer
}

// Action decodes ByteSlots[SrcSlot] (Avro binary) to JSON in ByteSlots[DstSlot].
func (cfg *AvroToJSONConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]
	buf := cfg.ScratchPool.Get().(*[]byte)
	defer func() {
		*buf = (*buf)[:0] // reset length; no pointer fields in []byte
		cfg.ScratchPool.Put(buf)
	}()

	out, err := avro.AppendAvroToJSON(*buf, src, cfg.DecProg)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Copy result to arena
	result := ctx.Alloc(len(out))
	copy(result, out)
	ctx.ByteSlots[cfg.DstSlot] = result
	return state.PC + 1
}

// JSONToAvroConfig holds the state for json_to_avro instruction.
// Encodes JSON to Avro binary with zero allocations using ScratchPool.
type JSONToAvroConfig struct {
	SrcSlot     int               // source ByteSlot (JSON)
	DstSlot     int               // destination ByteSlot (Avro binary)
	EncProg     *avro.AvroProgram // compiled encoder program
	ScratchPool *sync.Pool        // *[]byte pool for intermediate encode buffer
}

// Action encodes ByteSlots[SrcSlot] (JSON) to Avro binary in ByteSlots[DstSlot].
func (cfg *JSONToAvroConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]
	buf := cfg.ScratchPool.Get().(*[]byte)
	defer func() {
		*buf = (*buf)[:0]
		cfg.ScratchPool.Put(buf)
	}()

	out, err := avro.AppendJSONToAvro(*buf, src, cfg.EncProg)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Copy result to arena
	result := ctx.Alloc(len(out))
	copy(result, out)
	ctx.ByteSlots[cfg.DstSlot] = result
	return state.PC + 1
}

// AvroGetConfig holds the state for avro_get instruction.
// Extracts a field from Avro binary using either static partial-decode or dynamic full-decode + gjson.
type AvroGetConfig struct {
	SrcSlot     int               // source ByteSlot (Avro binary)
	DstSlot     int               // destination ByteSlot (extracted value)
	StaticPath  string            // dot-path string; empty if PathSlot is used
	PathSlot    int               // -1 if static; otherwise ByteSlot index for dynamic path
	PartialProg *avro.AvroProgram // nil if PathSlot >= 0 (dynamic); used for StaticPath
	FullDecProg *avro.AvroProgram // always set; used for dynamic paths
	JSONBufPool *sync.Pool        // *[]byte pool for dynamic path JSON buffer
}

// Action extracts a field from Avro binary.
// Static path: uses PartialProg to decode only the target field (zero alloc).
// Dynamic path: full decode to JSON then gjson.GetBytes (JSONBufPool alloc for intermediate JSON).
func (cfg *AvroGetConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]

	if cfg.PathSlot >= 0 {
		// Dynamic path: decode full JSON then extract via gjson
		jsonBuf := cfg.JSONBufPool.Get().(*[]byte)
		defer func() {
			*jsonBuf = (*jsonBuf)[:0]
			cfg.JSONBufPool.Put(jsonBuf)
		}()

		// Full decode to JSON
		out, err := avro.AppendAvroToJSON(*jsonBuf, src, cfg.FullDecProg)
		if err != nil {
			ctx.Failed = true
			ctx.ByteSlots[cfg.DstSlot] = nil
			return state.PC + 1
		}

		// Get path from dynamic slot
		pathBytes := ctx.ByteSlots[cfg.PathSlot]
		path := string(pathBytes)

		// Extract via gjson
		result := gjson.GetBytes(out, path)
		if !result.Exists() {
			ctx.Failed = true
			ctx.ByteSlots[cfg.DstSlot] = nil
			return state.PC + 1
		}

		// Copy raw JSON value to arena
		rawVal := []byte(result.Raw)
		dst := ctx.Alloc(len(rawVal))
		copy(dst, rawVal)
		ctx.ByteSlots[cfg.DstSlot] = dst
		return state.PC + 1
	}

	// Static path: partial decode using PartialProg
	out, err := avro.AppendAvroToJSON(nil, src, cfg.PartialProg)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// The partial program already decoded only the target field into a slot.
	// Extract from the JSON output (should be a single-field object).
	result := gjson.GetBytes(out, cfg.StaticPath)
	if !result.Exists() {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Copy raw JSON value to arena
	rawVal := []byte(result.Raw)
	dst := ctx.Alloc(len(rawVal))
	copy(dst, rawVal)
	ctx.ByteSlots[cfg.DstSlot] = dst
	return state.PC + 1
}

// AvroToXMLConfig holds the state for avro_to_xml instruction.
// Multi-hop: Avro binary â†’ JSON â†’ XML using pivot buffer.
type AvroToXMLConfig struct {
	SrcSlot     int                 // source ByteSlot (Avro binary)
	DstSlot     int                 // destination ByteSlot (XML)
	DecProg     *avro.AvroProgram   // decode Avro to JSON
	BuildProg   xml.XMLBuildProgram // build XML from JSON
	ScratchPool *sync.Pool          // *[]byte pool for intermediate buffers
}

// Action converts Avro binary to XML via JSON pivot.
func (cfg *AvroToXMLConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]
	pivot := transcode.GetPivot()
	defer transcode.PutPivot(pivot)

	// Decode Avro to JSON in pivot buffer
	jsonBytes, err := avro.AppendAvroToJSON(pivot.Bytes(), src, cfg.DecProg)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Allocate output buffer and convert JSON to XML
	// Estimate XML size: 1.5x JSON for element tags
	hint := len(jsonBytes) * 3 / 2
	if hint < 256 {
		hint = 256
	}
	out := ctx.Alloc(hint)

	xmlBytes, err := xml.AppendJSONToXML(out, jsonBytes, cfg.BuildProg)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	ctx.ByteSlots[cfg.DstSlot] = xmlBytes
	return state.PC + 1
}

// XMLToAvroConfig holds the state for xml_to_avro instruction.
// Multi-hop: XML â†’ JSON â†’ Avro binary using pivot buffer.
type XMLToAvroConfig struct {
	SrcSlot     int                // source ByteSlot (XML)
	DstSlot     int                // destination ByteSlot (Avro binary)
	ScanProg    xml.XMLScanProgram // scan XML to JSON
	EncProg     *avro.AvroProgram  // encode JSON to Avro
	ScratchPool *sync.Pool         // *[]byte pool for intermediate buffers
}

// Action converts XML to Avro binary via JSON pivot.
func (cfg *XMLToAvroConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]
	pivot := transcode.GetPivot()
	defer transcode.PutPivot(pivot)

	// Scan XML to JSON in pivot buffer
	jsonBytes, err := xml.AppendXMLToJSON(pivot.Bytes(), src, cfg.ScanProg)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Encode JSON to Avro
	buf := cfg.ScratchPool.Get().(*[]byte)
	defer func() {
		*buf = (*buf)[:0]
		cfg.ScratchPool.Put(buf)
	}()

	avroBin, err := avro.AppendJSONToAvro(*buf, jsonBytes, cfg.EncProg)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	// Copy to arena
	out := ctx.Alloc(len(avroBin))
	copy(out, avroBin)
	ctx.ByteSlots[cfg.DstSlot] = out
	return state.PC + 1
}

// AvroToProtoConfig holds the state for avro_to_proto instruction.
// Multi-hop: Avro binary â†’ JSON â†’ Proto message using pivot buffer.
// (Proto support in S8 — for now, this is a placeholder structure.)
type AvroToProtoConfig struct {
	SrcSlot int               // source ByteSlot (Avro binary)
	DstSlot int               // destination ByteSlot (Proto binary)
	DecProg *avro.AvroProgram // decode Avro to JSON
	// ProtoEncoderFunc will be set when gRPC/proto support is added (S6/S8)
	ScratchPool *sync.Pool
}

// ProtoToAvroConfig holds the state for proto_to_avro instruction.
// Multi-hop: Proto message â†’ JSON â†’ Avro binary using pivot buffer.
// (Proto support in S8 — for now, this is a placeholder structure.)
type ProtoToAvroConfig struct {
	SrcSlot int               // source ByteSlot (Proto binary)
	DstSlot int               // destination ByteSlot (Avro binary)
	EncProg *avro.AvroProgram // encode JSON to Avro
	// ProtoDecoderFunc will be set when gRPC/proto support is added (S6/S8)
	ScratchPool *sync.Pool
}

// NewAvroToJSONStep creates an avro_to_json instruction.
func NewAvroToJSONStep(srcSlot, dstSlot int, decProg *avro.AvroProgram) (engine.Instruction, error) {
	if decProg == nil {
		return engine.Instruction{}, fmt.Errorf("avro_to_json: decProg is nil")
	}
	cfg := &AvroToJSONConfig{
		SrcSlot:     srcSlot,
		DstSlot:     dstSlot,
		DecProg:     decProg,
		ScratchPool: &sync.Pool{New: func() any { return &([]byte{}) }},
	}
	return engine.Instruction{
		Name:   "AVRO_TO_JSON",
		Action: cfg.Action,
	}, nil
}

// NewJSONToAvroStep creates a json_to_avro instruction.
func NewJSONToAvroStep(srcSlot, dstSlot int, encProg *avro.AvroProgram) (engine.Instruction, error) {
	if encProg == nil {
		return engine.Instruction{}, fmt.Errorf("json_to_avro: encProg is nil")
	}
	cfg := &JSONToAvroConfig{
		SrcSlot:     srcSlot,
		DstSlot:     dstSlot,
		EncProg:     encProg,
		ScratchPool: &sync.Pool{New: func() any { return &([]byte{}) }},
	}
	return engine.Instruction{
		Name:   "JSON_TO_AVRO",
		Action: cfg.Action,
	}, nil
}

// NewAvroGetStep creates an avro_get instruction.
func NewAvroGetStep(srcSlot, dstSlot int, staticPath string, pathSlot int,
	partialProg *avro.AvroProgram, fullDecProg *avro.AvroProgram) (engine.Instruction, error) {
	if fullDecProg == nil {
		return engine.Instruction{}, fmt.Errorf("avro_get: fullDecProg is nil")
	}
	if pathSlot < 0 && partialProg == nil {
		return engine.Instruction{}, fmt.Errorf("avro_get: static path but partialProg is nil")
	}
	cfg := &AvroGetConfig{
		SrcSlot:     srcSlot,
		DstSlot:     dstSlot,
		StaticPath:  staticPath,
		PathSlot:    pathSlot,
		PartialProg: partialProg,
		FullDecProg: fullDecProg,
		JSONBufPool: &sync.Pool{New: func() any { return &([]byte{}) }},
	}
	return engine.Instruction{
		Name:   "AVRO_GET",
		Action: cfg.Action,
	}, nil
}

// NewAvroToXMLStep creates an avro_to_xml instruction.
func NewAvroToXMLStep(srcSlot, dstSlot int, decProg *avro.AvroProgram,
	buildProg xml.XMLBuildProgram) (engine.Instruction, error) {
	if decProg == nil {
		return engine.Instruction{}, fmt.Errorf("avro_to_xml: decProg is nil")
	}
	cfg := &AvroToXMLConfig{
		SrcSlot:     srcSlot,
		DstSlot:     dstSlot,
		DecProg:     decProg,
		BuildProg:   buildProg,
		ScratchPool: &sync.Pool{New: func() any { return &([]byte{}) }},
	}
	return engine.Instruction{
		Name:   "AVRO_TO_XML",
		Action: cfg.Action,
	}, nil
}

// NewXMLToAvroStep creates an xml_to_avro instruction.
func NewXMLToAvroStep(srcSlot, dstSlot int, scanProg xml.XMLScanProgram,
	encProg *avro.AvroProgram) (engine.Instruction, error) {
	if encProg == nil {
		return engine.Instruction{}, fmt.Errorf("xml_to_avro: encProg is nil")
	}
	cfg := &XMLToAvroConfig{
		SrcSlot:     srcSlot,
		DstSlot:     dstSlot,
		ScanProg:    scanProg,
		EncProg:     encProg,
		ScratchPool: &sync.Pool{New: func() any { return &([]byte{}) }},
	}
	return engine.Instruction{
		Name:   "XML_TO_AVRO",
		Action: cfg.Action,
	}, nil
}
