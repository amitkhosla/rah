package steps

import (
	"bytes"
	"sync"

	"rah/internal/engine"
	"rah/internal/rctx"
	"rah/internal/transcode"
	"rah/internal/xml"
)

// ─── xml_to_json ─────────────────────────────────────────────────────────────

// XMLToJSONConfig holds state for the xml_to_json instruction.
// Converts XML bytes from SrcSlot to JSON bytes in DstSlot using XMLScanProgram.
type XMLToJSONConfig struct {
	SrcSlot int
	DstSlot int
	Prog    xml.XMLScanProgram
}

func (cfg *XMLToJSONConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]
	pivot := transcode.GetPivot()
	defer transcode.PutPivot(pivot)

	out, err := xml.AppendXMLToJSON(pivot.Bytes(), src, cfg.Prog)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}
	result := ctx.Alloc(len(out))
	copy(result, out)
	ctx.ByteSlots[cfg.DstSlot] = result
	return state.PC + 1
}

// NewXMLToJSONStep creates an xml_to_json instruction.
func NewXMLToJSONStep(srcSlot, dstSlot int, prog xml.XMLScanProgram) engine.Instruction {
	cfg := &XMLToJSONConfig{SrcSlot: srcSlot, DstSlot: dstSlot, Prog: prog}
	return engine.Instruction{Name: "XML_TO_JSON", Action: cfg.Action}
}

// ─── json_to_xml ─────────────────────────────────────────────────────────────

// JSONToXMLConfig holds state for the json_to_xml instruction.
// Converts JSON bytes from SrcSlot to XML bytes in DstSlot using XMLBuildProgram.
type JSONToXMLConfig struct {
	SrcSlot int
	DstSlot int
	Prog    xml.XMLBuildProgram
}

func (cfg *JSONToXMLConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]
	pivot := transcode.GetPivot()
	defer transcode.PutPivot(pivot)

	out, err := xml.AppendJSONToXML(pivot.Bytes(), src, cfg.Prog)
	if err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}
	result := ctx.Alloc(len(out))
	copy(result, out)
	ctx.ByteSlots[cfg.DstSlot] = result
	return state.PC + 1
}

// NewJSONToXMLStep creates a json_to_xml instruction.
func NewJSONToXMLStep(srcSlot, dstSlot int, prog xml.XMLBuildProgram) engine.Instruction {
	cfg := &JSONToXMLConfig{SrcSlot: srcSlot, DstSlot: dstSlot, Prog: prog}
	return engine.Instruction{Name: "JSON_TO_XML", Action: cfg.Action}
}

// ─── parse_xml ───────────────────────────────────────────────────────────────

// ParseXMLConfig holds state for the parse_xml instruction.
// Validates XML structure by scanning through it; if valid, stores the source
// bytes (unchanged) in DstSlot. Sets ctx.Failed if the XML is malformed.
type ParseXMLConfig struct {
	SrcSlot int
	DstSlot int
}

func (cfg *ParseXMLConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]
	s := xml.GetXMLScanner(src)
	defer xml.PutXMLScanner(s)

	if err := s.ValidateAndSkipProlog(); err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}
	// Minimal structural check: must contain at least one element start '<'.
	if bytes.IndexByte(src, '<') < 0 {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}
	ctx.ByteSlots[cfg.DstSlot] = src
	return state.PC + 1
}

// NewParseXMLStep creates a parse_xml instruction.
func NewParseXMLStep(srcSlot, dstSlot int) engine.Instruction {
	cfg := &ParseXMLConfig{SrcSlot: srcSlot, DstSlot: dstSlot}
	return engine.Instruction{Name: "PARSE_XML", Action: cfg.Action}
}

// ─── xml_get ─────────────────────────────────────────────────────────────────

// XMLGetConfig holds state for the xml_get instruction.
// Extracts the text content of the first element matching ElementName
// from the XML in SrcSlot and stores it in DstSlot.
type XMLGetConfig struct {
	SrcSlot     int
	DstSlot     int
	ElementName []byte // pre-computed at bake time; no runtime alloc
}

func (cfg *XMLGetConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]
	s := xml.GetXMLScanner(src)
	defer xml.PutXMLScanner(s)

	if err := s.ValidateAndSkipProlog(); err != nil {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	if !s.FindElement(cfg.ElementName) {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	content, ok := s.AppendContent(nil)
	if !ok {
		ctx.Failed = true
		ctx.ByteSlots[cfg.DstSlot] = nil
		return state.PC + 1
	}

	result := ctx.Alloc(len(content))
	copy(result, content)
	ctx.ByteSlots[cfg.DstSlot] = result
	return state.PC + 1
}

// NewXMLGetStep creates an xml_get instruction.
func NewXMLGetStep(srcSlot, dstSlot int, elementName string) engine.Instruction {
	cfg := &XMLGetConfig{
		SrcSlot:     srcSlot,
		DstSlot:     dstSlot,
		ElementName: []byte(elementName),
	}
	return engine.Instruction{Name: "XML_GET", Action: cfg.Action}
}

// ─── set_xml_response ────────────────────────────────────────────────────────

// SetXMLResponseConfig holds state for the set_xml_response instruction.
// Writes the XML bytes from SrcSlot to ctx.ResponseBuffer and sets
// ctx.ResponseStatus to DefaultStatus (typically 200) if not yet set.
// Also adds a Content-Type: application/xml response header.
type SetXMLResponseConfig struct {
	SrcSlot       int
	DefaultStatus int
}

func (cfg *SetXMLResponseConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	body := ctx.ByteSlots[cfg.SrcSlot]
	if !ctx.StreamResponseBody {
		ctx.ResponseBuffer = body
		ctx.IsBuffered = true
	}
	if ctx.ResponseStatus == 0 {
		ctx.ResponseStatus = cfg.DefaultStatus
	}
	// Set Content-Type header if not already set.
	ctx.ResponseHeaders = append(ctx.ResponseHeaders, rctx.HeaderMutation{
		Key:   []byte("Content-Type"),
		Value: []byte("application/xml; charset=utf-8"),
		Op:    0, // Set
	})
	ctx.ResHeaderCount++
	return state.PC + 1
}

// NewSetXMLResponseStep creates a set_xml_response instruction.
func NewSetXMLResponseStep(srcSlot, defaultStatus int) engine.Instruction {
	if defaultStatus == 0 {
		defaultStatus = 200
	}
	cfg := &SetXMLResponseConfig{SrcSlot: srcSlot, DefaultStatus: defaultStatus}
	return engine.Instruction{Name: "SET_XML_RESPONSE", Action: cfg.Action}
}

// ─── build_xml ───────────────────────────────────────────────────────────────

// BuildXMLConfig holds state for the build_xml instruction.
// Builds an XML document from prog using ByteSlot values as leaf content.
// The SlotIndices slice maps prog op indices → ByteSlots positions.
type BuildXMLConfig struct {
	DstSlot int
	Prog    xml.XMLBuildProgram
	// Slots is a scratch pool for [][]byte to gather slot values without alloc.
	ScratchPool *sync.Pool
}

func (cfg *BuildXMLConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	// Borrow scratch slice for the slots view.
	slotsPtr := cfg.ScratchPool.Get().(*[][]byte)
	slots := (*slotsPtr)[:0]

	// Grow to match prog.Ops length.
	for len(slots) < len(cfg.Prog.Ops) {
		slots = append(slots, nil)
	}

	// Each op's SlotIdx references ctx.ByteSlots directly.
	for i, op := range cfg.Prog.Ops {
		if int(op.SlotIdx) < len(ctx.ByteSlots) {
			slots[i] = ctx.ByteSlots[op.SlotIdx]
		} else {
			slots[i] = nil
		}
	}

	pivot := transcode.GetPivot()
	defer transcode.PutPivot(pivot)

	out := xml.AppendXML(pivot.Bytes(), cfg.Prog, slots)

	// Return scratch slice.
	*slotsPtr = slots
	cfg.ScratchPool.Put(slotsPtr)

	result := ctx.Alloc(len(out))
	copy(result, out)
	ctx.ByteSlots[cfg.DstSlot] = result
	return state.PC + 1
}

// NewBuildXMLStep creates a build_xml instruction.
func NewBuildXMLStep(dstSlot int, prog xml.XMLBuildProgram) engine.Instruction {
	cfg := &BuildXMLConfig{
		DstSlot: dstSlot,
		Prog:    prog,
		ScratchPool: &sync.Pool{New: func() any {
			s := make([][]byte, 0, 16)
			return &s
		}},
	}
	return engine.Instruction{Name: "BUILD_XML", Action: cfg.Action}
}

// ─── xml_set ─────────────────────────────────────────────────────────────────

// XMLSetConfig holds state for the xml_set instruction.
// Replaces the text content of the first element matching ElementName in the
// XML from SrcSlot with the bytes from ValueSlot. The result is written to DstSlot.
// Strategy: copy bytes up to element content start, inject new value, then copy
// from original content end to end of document. All done in a pool-borrowed buffer.
//
// OpenPrefix and CloseTag are pre-computed at bake time to avoid runtime allocs.
type XMLSetConfig struct {
	SrcSlot     int
	ValueSlot   int
	DstSlot     int
	OpenPrefix  []byte // "<ElementName" — baked at compile time
	CloseTag    []byte // "</ElementName>" — baked at compile time
	ScratchPool *sync.Pool
}

func (cfg *XMLSetConfig) Action(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	src := ctx.ByteSlots[cfg.SrcSlot]
	newVal := ctx.ByteSlots[cfg.ValueSlot]

	// Validate the source XML is well-formed before modifying.
	{
		s := xml.GetXMLScanner(src)
		err := s.ValidateAndSkipProlog()
		xml.PutXMLScanner(s)
		if err != nil {
			ctx.Failed = true
			ctx.ByteSlots[cfg.DstSlot] = nil
			return state.PC + 1
		}
	}

	// Find the open tag boundary using pre-computed prefix (no runtime alloc).
	openTagStart := bytes.Index(src, cfg.OpenPrefix)
	if openTagStart < 0 {
		// Element not found — copy src unchanged.
		ctx.ByteSlots[cfg.DstSlot] = src
		return state.PC + 1
	}

	// Find the '>' ending the open tag.
	openTagEnd := bytes.IndexByte(src[openTagStart:], '>')
	if openTagEnd < 0 {
		ctx.ByteSlots[cfg.DstSlot] = src
		return state.PC + 1
	}
	contentStart := openTagStart + openTagEnd + 1

	closeTagStart := bytes.Index(src[contentStart:], cfg.CloseTag)
	if closeTagStart < 0 {
		ctx.ByteSlots[cfg.DstSlot] = src
		return state.PC + 1
	}
	contentEnd := contentStart + closeTagStart

	// Build new document: src[:contentStart] + newVal + src[contentEnd:].
	prefix := src[:contentStart]
	suffix := src[contentEnd:]
	totalLen := len(prefix) + len(newVal) + len(suffix)

	bufPtr := cfg.ScratchPool.Get().(*[]byte)
	buf := (*bufPtr)[:0]
	buf = append(buf, prefix...)
	buf = append(buf, newVal...)
	buf = append(buf, suffix...)

	result := ctx.Alloc(totalLen)
	copy(result, buf)
	ctx.ByteSlots[cfg.DstSlot] = result

	*bufPtr = buf
	cfg.ScratchPool.Put(bufPtr)
	return state.PC + 1
}

// NewXMLSetStep creates an xml_set instruction.
// elementName must match the exact case used in the XML document.
func NewXMLSetStep(srcSlot, valueSlot, dstSlot int, elementName string) engine.Instruction {
	nameBytes := []byte(elementName)
	openPrefix := make([]byte, 0, len(nameBytes)+1)
	openPrefix = append(openPrefix, '<')
	openPrefix = append(openPrefix, nameBytes...)
	closeTag := make([]byte, 0, len(nameBytes)+3)
	closeTag = append(closeTag, '<', '/')
	closeTag = append(closeTag, nameBytes...)
	closeTag = append(closeTag, '>')

	cfg := &XMLSetConfig{
		SrcSlot:    srcSlot,
		ValueSlot:  valueSlot,
		DstSlot:    dstSlot,
		OpenPrefix: openPrefix,
		CloseTag:   closeTag,
		ScratchPool: &sync.Pool{New: func() any {
			b := make([]byte, 0, 4096)
			return &b
		}},
	}
	return engine.Instruction{Name: "XML_SET", Action: cfg.Action}
}
