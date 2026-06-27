package xml

// AppendXML builds an XML document from prog and slot values, appending into dst.
// All tag bytes come from prog.Slab (referenced, not copied).
// Slot values are appended from slots[op.SlotIdx].
// Children are emitted recursively in DFS order.
// Zero heap allocations: all data from slab or slot bytes.
func AppendXML(dst []byte, prog XMLBuildProgram, slots [][]byte) []byte {
	if len(prog.Ops) == 0 {
		return dst
	}
	// Emit root op and its children
	return appendXMLOp(dst, prog, slots, 0)
}

func appendXMLOp(dst []byte, prog XMLBuildProgram, slots [][]byte, opIdx int) []byte {
	op := prog.Ops[opIdx]
	open := prog.Slab[op.OpenOff : op.OpenOff+uint16(op.OpenLen)]
	close_ := prog.Slab[op.CloseOff : op.CloseOff+uint16(op.CloseLen)]

	dst = append(dst, open...)

	children := prog.Children[opIdx]
	if len(children) > 0 {
		for _, childIdx := range children {
			dst = appendXMLOp(dst, prog, slots, childIdx)
		}
		dst = append(dst, close_...)
		return dst
	}

	// Leaf node: emit slot value
	val := slots[op.SlotIdx]
	if len(val) == 0 {
		if op.Flags&XMLBuildFlagSelfClose != 0 {
			// Already emitted as self-closing in open tag
			return dst
		}
		// Empty element: rewrite as self-closing
		// open was e.g. "<name>", strip trailing '>' and add '/>'
		if len(dst) > 0 && dst[len(dst)-1] == '>' {
			dst[len(dst)-1] = '/'
			dst = append(dst, '>')
		}
		return dst
	}
	dst = append(dst, val...)
	dst = append(dst, close_...)
	return dst
}
