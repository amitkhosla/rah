package avro

import "sync"

// op.go — compiled AvroOp instruction and AvroProgram types.
// All instances are immutable after Build/Compile; safe for concurrent use.

// AvroOp is one operation in a compiled AvroProgram. Exactly 8 bytes.
// All field name bytes live in AvroProgram.Slab.
type AvroOp struct {
	NameOff uint16 // field name offset in Slab
	NameLen uint8  // field name length
	KeyIdx  uint8  // 0–254: index into AvroProgram.FieldIndex for slot mapping
	// 255: sentinel → look up AvroProgram.ExtTable
	Kind  uint8  // AvroKind* constant
	Flags uint8  // AvroFlag* constants
	Param uint16 // overloaded: enum symbol count, union arm count, fixed size, array-item opIdx
}

// AvroKind constants — identify the Avro wire type for each op.
const (
	AvroKindNull    uint8 = 0
	AvroKindBool    uint8 = 1
	AvroKindInt     uint8 = 2
	AvroKindLong    uint8 = 3
	AvroKindFloat   uint8 = 4
	AvroKindDouble  uint8 = 5
	AvroKindBytes   uint8 = 6
	AvroKindString  uint8 = 7
	AvroKindRecord  uint8 = 8
	AvroKindEnum    uint8 = 9
	AvroKindArray   uint8 = 10
	AvroKindMap     uint8 = 11
	AvroKindUnion   uint8 = 12
	AvroKindFixed   uint8 = 13
)

// AvroFlag constants — modifier bits stored in AvroOp.Flags.
const (
	AvroFlagOptional  uint8 = 1 << 0 // field is optional (null union)
	AvroFlagIsSlot    uint8 = 1 << 1 // decoded value stored in ByteSlots[slot]
	AvroFlagSkip      uint8 = 1 << 2 // field present in schema but not needed — decode and discard
	AvroFlagNullFirst uint8 = 1 << 3 // union: null is arm 0 (["null","string"] pattern)
)

// extSentinel is the KeyIdx value that signals an ExtTable lookup.
const extSentinel uint8 = 255

// AvroOpExt holds overflow slot mapping for ops where KeyIdx > 254.
// Stored in AvroProgram.ExtTable sorted by OpIdx for binary search.
type AvroOpExt struct {
	OpIdx  int32
	SlotID int32
}

// AvroProgram is an immutable compiled program for a single Avro record schema.
// Built once at bake time. All fields are read-only after Compile().
type AvroProgram struct {
	Ops        []AvroOp
	Slab       []byte      // all field name bytes interned here
	FieldIndex []int8      // maps KeyIdx → ByteSlots index (-1 = skip/discard)
	ExtTable   []AvroOpExt // nil unless any op has KeyIdx == extSentinel
	Symbols    [][]byte    // enum symbol names; indexed by symbol index for Enum ops
	ChildOps   [][]int     // for Record/Array/Map/Union ops: child op indices (parallel to Ops)
}

// lookupExtSlot returns the SlotID for opIdx when KeyIdx==extSentinel.
// Uses binary search on the sorted ExtTable. Returns -1 if not found.
func (p *AvroProgram) lookupExtSlot(opIdx int) int32 {
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
		return p.ExtTable[lo].SlotID
	}
	return -1
}

// fieldName returns the field name bytes for op o from the program slab.
func (p *AvroProgram) fieldName(o AvroOp) []byte {
	return p.Slab[o.NameOff : int(o.NameOff)+int(o.NameLen)]
}

// slotFor returns the ByteSlots index for op at opIdx, or -1 if skip/no slot.
func (p *AvroProgram) slotFor(opIdx int) int {
	op := p.Ops[opIdx]
	if op.Flags&AvroFlagSkip != 0 {
		return -1
	}
	if op.KeyIdx == extSentinel {
		return int(p.lookupExtSlot(opIdx))
	}
	if int(op.KeyIdx) >= len(p.FieldIndex) {
		return -1
	}
	return int(p.FieldIndex[op.KeyIdx])
}

// loopState holds state for one level of Array/Map iteration.
type loopState struct {
	remaining int64 // items left in current block
	opIdx     int   // which op to execute for each item
}

// loopStack is a borrowed heap stack for nesting depth > 8.
type loopStack struct{ frames []loopState }

var loopStackPool = sync.Pool{New: func() any {
	return &loopStack{frames: make([]loopState, 0, 16)}
}}
