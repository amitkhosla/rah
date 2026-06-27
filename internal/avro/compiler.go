package avro

import (
	"fmt"

	hamba "github.com/hamba/avro/v2"
)

// compiler.go — bake-time schema compiler.
// Parses Avro JSON schema via hamba and builds an immutable AvroProgram.
// hamba is ONLY used here; never imported in execute.go or other runtime files.

// Compile parses an Avro JSON schema string and returns a compiled AvroProgram.
// Called once at gateway startup (bake time). Returns error on invalid schema.
// Recursive schemas (self-referencing records) return ErrRecursiveSchema.
//
// slotMap maps Avro field name → ByteSlots index.
// Fields not in slotMap receive FieldIndex[keyIdx] = -1 (skip/discard).
func Compile(schemaJSON string, slotMap map[string]int) (*AvroProgram, error) {
	schema, err := hamba.Parse(schemaJSON)
	if err != nil {
		return nil, fmt.Errorf("avro: schema parse error: %w", err)
	}

	c := &compileCtx{
		slotMap: slotMap,
		seen:    make(map[string]struct{}),
	}

	if err := c.walk(schema, ""); err != nil {
		return nil, err
	}

	// Build FieldIndex: one entry per unique KeyIdx used
	maxKey := -1
	for _, op := range c.prog.Ops {
		if op.KeyIdx != extSentinel && int(op.KeyIdx) > maxKey {
			maxKey = int(op.KeyIdx)
		}
	}
	if maxKey >= 0 {
		c.prog.FieldIndex = make([]int8, maxKey+1)
		for i := range c.prog.FieldIndex {
			c.prog.FieldIndex[i] = -1
		}
		for i, op := range c.prog.Ops {
			if op.KeyIdx == extSentinel {
				continue
			}
			name := string(c.prog.Slab[op.NameOff : int(op.NameOff)+int(op.NameLen)])
			if slotIdx, ok := slotMap[name]; ok {
				c.prog.FieldIndex[op.KeyIdx] = int8(slotIdx)
				_ = i
			}
		}
	}

	return &c.prog, nil
}

// compileCtx holds mutable state during compilation.
type compileCtx struct {
	prog    AvroProgram
	slotMap map[string]int
	seen    map[string]struct{} // record names seen (for recursion detection)
	keySeq  int                 // monotonically increasing KeyIdx allocator
}

// internName appends name to the slab and returns (offset, length).
func (c *compileCtx) internName(name string) (uint16, uint8) {
	off := len(c.prog.Slab)
	c.prog.Slab = append(c.prog.Slab, name...)
	return uint16(off), uint8(len(name))
}

// allocKeyIdx returns the next KeyIdx and advances the sequence.
// If the sequence reaches 255 the op gets extSentinel and an ExtTable entry.
func (c *compileCtx) allocKeyIdx(opIdx int, fieldName string) uint8 {
	idx := c.keySeq
	c.keySeq++
	if idx >= int(extSentinel) {
		// Overflow: use ExtTable
		slotID := int32(-1)
		if si, ok := c.slotMap[fieldName]; ok {
			slotID = int32(si)
		}
		c.prog.ExtTable = append(c.prog.ExtTable, AvroOpExt{
			OpIdx:  int32(opIdx),
			SlotID: slotID,
		})
		return extSentinel
	}
	return uint8(idx)
}

// walk compiles one schema node, appending ops to prog.Ops.
// fieldName is the parent record field name (empty for the root).
func (c *compileCtx) walk(schema hamba.Schema, fieldName string) error {
	opIdx := len(c.prog.Ops)

	// Reserve a slot in ChildOps parallel to Ops
	for len(c.prog.ChildOps) <= opIdx {
		c.prog.ChildOps = append(c.prog.ChildOps, nil)
	}

	switch s := schema.(type) {
	case *hamba.NullSchema:
		off, ln := c.internName(fieldName)
		c.prog.Ops = append(c.prog.Ops, AvroOp{
			NameOff: off,
			NameLen: ln,
			KeyIdx:  c.allocKeyIdx(opIdx, fieldName),
			Kind:    AvroKindNull,
		})

	case *hamba.PrimitiveSchema:
		off, ln := c.internName(fieldName)
		var kind uint8
		switch s.Type() {
		case hamba.Boolean:
			kind = AvroKindBool
		case hamba.Int:
			kind = AvroKindInt
		case hamba.Long:
			kind = AvroKindLong
		case hamba.Float:
			kind = AvroKindFloat
		case hamba.Double:
			kind = AvroKindDouble
		case hamba.Bytes:
			kind = AvroKindBytes
		case hamba.String:
			kind = AvroKindString
		default:
			return fmt.Errorf("avro: unsupported primitive type %q", s.Type())
		}
		c.prog.Ops = append(c.prog.Ops, AvroOp{
			NameOff: off,
			NameLen: ln,
			KeyIdx:  c.allocKeyIdx(opIdx, fieldName),
			Kind:    kind,
		})

	case *hamba.RecordSchema:
		name := s.Name()
		if _, already := c.seen[name]; already {
			return ErrRecursiveSchema
		}
		c.seen[name] = struct{}{}

		off, ln := c.internName(fieldName)
		keyIdx := c.allocKeyIdx(opIdx, fieldName)
		c.prog.Ops = append(c.prog.Ops, AvroOp{
			NameOff: off,
			NameLen: ln,
			KeyIdx:  keyIdx,
			Kind:    AvroKindRecord,
		})

		// Compile child fields
		for _, f := range s.Fields() {
			childIdx := len(c.prog.Ops)
			if err := c.walk(f.Type(), f.Name()); err != nil {
				return err
			}
			c.prog.ChildOps[opIdx] = append(c.prog.ChildOps[opIdx], childIdx)
		}

		delete(c.seen, name)

	case *hamba.EnumSchema:
		off, ln := c.internName(fieldName)
		syms := s.Symbols()
		// Intern symbol names
		symStart := len(c.prog.Symbols)
		for _, sym := range syms {
			c.prog.Symbols = append(c.prog.Symbols, []byte(sym))
		}
		_ = symStart
		c.prog.Ops = append(c.prog.Ops, AvroOp{
			NameOff: off,
			NameLen: ln,
			KeyIdx:  c.allocKeyIdx(opIdx, fieldName),
			Kind:    AvroKindEnum,
			Param:   uint16(len(syms)),
		})

	case *hamba.ArraySchema:
		off, ln := c.internName(fieldName)
		keyIdx := c.allocKeyIdx(opIdx, fieldName)
		c.prog.Ops = append(c.prog.Ops, AvroOp{
			NameOff: off,
			NameLen: ln,
			KeyIdx:  keyIdx,
			Kind:    AvroKindArray,
		})
		// Compile item schema
		childIdx := len(c.prog.Ops)
		if err := c.walk(s.Items(), ""); err != nil {
			return err
		}
		c.prog.ChildOps[opIdx] = []int{childIdx}

	case *hamba.MapSchema:
		off, ln := c.internName(fieldName)
		keyIdx := c.allocKeyIdx(opIdx, fieldName)
		c.prog.Ops = append(c.prog.Ops, AvroOp{
			NameOff: off,
			NameLen: ln,
			KeyIdx:  keyIdx,
			Kind:    AvroKindMap,
		})
		// Compile value schema
		childIdx := len(c.prog.Ops)
		if err := c.walk(s.Values(), ""); err != nil {
			return err
		}
		c.prog.ChildOps[opIdx] = []int{childIdx}

	case *hamba.UnionSchema:
		off, ln := c.internName(fieldName)
		types := s.Types()
		var flags uint8
		// Detect NullFirst pattern: ["null", "other"]
		if len(types) >= 1 {
			if _, isNull := types[0].(*hamba.NullSchema); isNull {
				flags |= AvroFlagNullFirst
			}
		}
		// Detect optional (exactly one null + one other)
		hasNull := false
		for _, t := range types {
			if _, isNull := t.(*hamba.NullSchema); isNull {
				hasNull = true
				break
			}
		}
		if hasNull {
			flags |= AvroFlagOptional
		}

		keyIdx := c.allocKeyIdx(opIdx, fieldName)
		c.prog.Ops = append(c.prog.Ops, AvroOp{
			NameOff: off,
			NameLen: ln,
			KeyIdx:  keyIdx,
			Kind:    AvroKindUnion,
			Flags:   flags,
			Param:   uint16(len(types)),
		})
		// Compile each arm
		for _, t := range types {
			childIdx := len(c.prog.Ops)
			if err := c.walk(t, ""); err != nil {
				return err
			}
			c.prog.ChildOps[opIdx] = append(c.prog.ChildOps[opIdx], childIdx)
		}

	case *hamba.FixedSchema:
		off, ln := c.internName(fieldName)
		c.prog.Ops = append(c.prog.Ops, AvroOp{
			NameOff: off,
			NameLen: ln,
			KeyIdx:  c.allocKeyIdx(opIdx, fieldName),
			Kind:    AvroKindFixed,
			Param:   uint16(s.Size()),
		})

	case *hamba.RefSchema:
		// Self-referencing schema — always an error in our design
		return ErrRecursiveSchema

	default:
		return fmt.Errorf("avro: unsupported schema type %T", schema)
	}

	return nil
}
