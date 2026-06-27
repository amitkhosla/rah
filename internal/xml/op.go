package xml

// XMLScanOp is one operation in a compiled XMLScanProgram.
// 8 bytes. All string data (tag names, JSON keys) lives in XMLScanProgram.Slab.
// Set once at bake time; never mutated at runtime.
type XMLScanOp struct {
	TagOff uint16 // element name offset in Slab (e.g. "items")
	TagLen uint8  // element name length
	KeyOff uint16 // JSON output key offset in Slab (e.g. `"items":`)
	KeyLen uint8  // JSON key length
	Index  uint8  // 0–254: nth occurrence to match (0=first)
	// 255: sentinel → look up XMLScanProgram.ExtTable for actual index
	Flags uint8 // IsAttr(1) | IsArray(2) | IsOptional(4) | IsWildcard(8)
}

const (
	XMLFlagIsAttr     uint8 = 1 << 0
	XMLFlagIsArray    uint8 = 1 << 1
	XMLFlagIsOptional uint8 = 1 << 2
	XMLFlagIsWildcard uint8 = 1 << 3
)

// XMLScanOpExt holds overflow values for ops where Index > 254.
type XMLScanOpExt struct {
	OpIdx int32 // index into XMLScanProgram.Ops
	Index int32 // actual element index (no limit)
}

// XMLScanProgram is an immutable, compiled program for scanning XML documents.
// Built once at bake time. Safe for concurrent execution.
type XMLScanProgram struct {
	Ops      []XMLScanOp
	Slab     []byte        // all tag names and JSON keys interned here
	ExtTable []XMLScanOpExt // nil unless any op has Index > 254
}

// lookupExt returns the ExtTable entry for opIdx. Binary search on OpIdx.
// Only called when op.Index == 255. Returns zero-value if not found (shouldn't happen).
func (p *XMLScanProgram) lookupExt(opIdx int) XMLScanOpExt {
	lo, hi := 0, len(p.ExtTable)
	for lo < hi {
		mid := (lo + hi) / 2
		if p.ExtTable[mid].OpIdx < int32(opIdx) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(p.ExtTable) && p.ExtTable[lo].OpIdx == int32(opIdx) {
		return p.ExtTable[lo]
	}
	return XMLScanOpExt{}
}

// XMLBuildOp is one operation in a compiled XMLBuildProgram.
// 8 bytes. All tag bytes live in XMLBuildProgram.Slab.
type XMLBuildOp struct {
	OpenOff  uint16 // open tag offset in Slab e.g. `<name>`
	OpenLen  uint8
	CloseOff uint16 // close tag offset in Slab e.g. `</name>`
	CloseLen uint8
	SlotIdx  uint8 // ByteSlots index to read value from
	Flags    uint8 // XMLBuildFlagIsAttr(1) | XMLBuildFlagHasChildren(2) | XMLBuildFlagSelfClose(4)
}

const (
	XMLBuildFlagIsAttr      uint8 = 1 << 0
	XMLBuildFlagHasChildren uint8 = 1 << 1
	XMLBuildFlagSelfClose   uint8 = 1 << 2
)

// XMLBuildProgram is an immutable, compiled program for building XML documents.
type XMLBuildProgram struct {
	Ops      []XMLBuildOp
	Slab     []byte   // all open/close tag bytes interned here
	Children [][]int  // children[i] = slice of child op indices for op i (nil if leaf)
}

// --- Builders (used by compiler at bake time) ---

// XMLScanProgramBuilder constructs an XMLScanProgram.
type XMLScanProgramBuilder struct {
	ops  []XMLScanOp
	slab []byte
	exts []XMLScanOpExt
}

// AddOp adds a scan op and returns its index.
// tagName: XML element name to match (e.g. "order")
// jsonKey: JSON key to emit including colon (e.g. `"orderId":`) — empty = use tagName
// index: which occurrence to match (0-based); handles > 254 via ExtTable
// flags: XMLFlag* constants
func (b *XMLScanProgramBuilder) AddOp(tagName, jsonKey string, index int, flags uint8) int {
	opIdx := len(b.ops)
	op := XMLScanOp{Flags: flags}

	// Intern tagName into slab
	op.TagOff = uint16(len(b.slab))
	op.TagLen = uint8(len(tagName))
	b.slab = append(b.slab, tagName...)

	// Intern jsonKey into slab (default to `"tagName":` if empty)
	if jsonKey == "" {
		jsonKey = `"` + tagName + `":`
	}
	op.KeyOff = uint16(len(b.slab))
	op.KeyLen = uint8(len(jsonKey))
	b.slab = append(b.slab, jsonKey...)

	// Handle index overflow
	if index <= 254 {
		op.Index = uint8(index)
	} else {
		op.Index = 255
		b.exts = append(b.exts, XMLScanOpExt{OpIdx: int32(opIdx), Index: int32(index)})
	}

	b.ops = append(b.ops, op)
	return opIdx
}

// Build returns the immutable XMLScanProgram.
func (b *XMLScanProgramBuilder) Build() XMLScanProgram {
	return XMLScanProgram{Ops: b.ops, Slab: b.slab, ExtTable: b.exts}
}

// XMLBuildProgramBuilder constructs an XMLBuildProgram.
type XMLBuildProgramBuilder struct {
	ops      []XMLBuildOp
	slab     []byte
	children [][]int
}

// AddOp adds a build op and returns its index.
// openTag: e.g. "<name>" or "<user id=\"1\">"
// closeTag: e.g. "</name>"
// slotIdx: ByteSlots index for value
// flags: XMLBuildFlag* constants
func (b *XMLBuildProgramBuilder) AddOp(openTag, closeTag string, slotIdx int, flags uint8) int {
	opIdx := len(b.ops)
	op := XMLBuildOp{SlotIdx: uint8(slotIdx), Flags: flags}
	op.OpenOff = uint16(len(b.slab))
	op.OpenLen = uint8(len(openTag))
	b.slab = append(b.slab, openTag...)
	op.CloseOff = uint16(len(b.slab))
	op.CloseLen = uint8(len(closeTag))
	b.slab = append(b.slab, closeTag...)
	b.ops = append(b.ops, op)
	b.children = append(b.children, nil)
	return opIdx
}

// AddChild registers childIdx as a child of parentIdx.
func (b *XMLBuildProgramBuilder) AddChild(parentIdx, childIdx int) {
	b.children[parentIdx] = append(b.children[parentIdx], childIdx)
}

// Build returns the immutable XMLBuildProgram.
func (b *XMLBuildProgramBuilder) Build() XMLBuildProgram {
	return XMLBuildProgram{Ops: b.ops, Slab: b.slab, Children: b.children}
}
