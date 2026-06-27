package avro

import (
	"fmt"
	"strings"
)

// partial.go — static-path partial decode compilation.
// Given a dot-path string (e.g. "user.address.city"), generates a minimal
// AvroProgram that skips all fields not needed to reach the target.

// CompilePartial compiles a partial decode program for the given gjson dot-path.
// The returned AvroProgram skips all fields not needed to reach the target field.
// slotIdx is the ByteSlots index where the extracted value will be stored.
//
// The fullProg must have been compiled with Compile() first.
// For array paths using [N] syntax (e.g. "items[0].name"), only the target
// index element will be stored; others are skipped.
func CompilePartial(fullProg *AvroProgram, dotPath string, slotIdx int) (*AvroProgram, error) {
	if dotPath == "" {
		return nil, fmt.Errorf("avro: empty dot-path")
	}
	segments := strings.Split(dotPath, ".")
	if len(segments) == 0 {
		return nil, fmt.Errorf("avro: empty dot-path")
	}

	// Deep-clone the program
	partial := cloneProgram(fullProg)

	// Walk the ops tree marking off-path fields as skip
	err := markSkips(partial, partial.Ops, segments, slotIdx)
	if err != nil {
		return nil, err
	}

	return partial, nil
}

// markSkips recursively marks ops as skip if they are not on the path to the target.
// ops is the current record's ops (absolute indices resolved from partial.Ops).
// segments is the remaining path to the target.
// slotIdx is stored for the terminal field.
func markSkips(prog *AvroProgram, ops []AvroOp, segments []string, slotIdx int) error {
	if len(segments) == 0 {
		return nil
	}
	target := segments[0]
	rest := segments[1:]

	found := false
	for i, op := range ops {
		name := string(prog.fieldName(op))

		// Find the op's absolute index in prog.Ops by name match
		absIdx := findOpByName(prog, name)
		if absIdx < 0 {
			continue
		}

		if name != target {
			// Mark as skip — off the path
			prog.Ops[absIdx].Flags |= AvroFlagSkip
			continue
		}

		// This is the target segment
		found = true
		if len(rest) == 0 {
			// Terminal: assign slot
			prog.Ops[absIdx].Flags &^= AvroFlagSkip
			prog.Ops[absIdx].Flags |= AvroFlagIsSlot
			// Update FieldIndex for this op
			if op.KeyIdx != extSentinel && int(op.KeyIdx) < len(prog.FieldIndex) {
				prog.FieldIndex[op.KeyIdx] = int8(slotIdx)
			}
		} else {
			// Intermediate: recurse into children
			children := prog.childOpsFor(absIdx)
			if len(children) == 0 {
				return fmt.Errorf("avro: path segment %q has no children in schema", target)
			}
			childOps := make([]AvroOp, len(children))
			for ci, idx := range children {
				childOps[ci] = prog.Ops[idx]
			}
			if err := markSkips(prog, childOps, rest, slotIdx); err != nil {
				return err
			}
		}
		_ = i
	}

	if !found {
		return fmt.Errorf("avro: path segment %q not found in schema", target)
	}
	return nil
}

// findOpByName finds the first op in prog.Ops with the given field name.
// Returns -1 if not found.
func findOpByName(prog *AvroProgram, name string) int {
	for i, op := range prog.Ops {
		n := string(prog.Slab[op.NameOff : int(op.NameOff)+int(op.NameLen)])
		if n == name {
			return i
		}
	}
	return -1
}

// cloneProgram performs a deep copy of an AvroProgram.
// The clone is independent — modifying it does not affect the original.
func cloneProgram(src *AvroProgram) *AvroProgram {
	dst := &AvroProgram{}

	dst.Ops = make([]AvroOp, len(src.Ops))
	copy(dst.Ops, src.Ops)

	dst.Slab = make([]byte, len(src.Slab))
	copy(dst.Slab, src.Slab)

	dst.FieldIndex = make([]int8, len(src.FieldIndex))
	copy(dst.FieldIndex, src.FieldIndex)

	if src.ExtTable != nil {
		dst.ExtTable = make([]AvroOpExt, len(src.ExtTable))
		copy(dst.ExtTable, src.ExtTable)
	}

	if src.Symbols != nil {
		dst.Symbols = make([][]byte, len(src.Symbols))
		for i, s := range src.Symbols {
			sc := make([]byte, len(s))
			copy(sc, s)
			dst.Symbols[i] = sc
		}
	}

	if src.ChildOps != nil {
		dst.ChildOps = make([][]int, len(src.ChildOps))
		for i, children := range src.ChildOps {
			if children != nil {
				cc := make([]int, len(children))
				copy(cc, children)
				dst.ChildOps[i] = cc
			}
		}
	}

	return dst
}
