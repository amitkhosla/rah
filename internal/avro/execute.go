package avro

import (
	"encoding/binary"
	"math"

	"github.com/tidwall/gjson"
)

// execute.go — zero-GC Avro binary ↔ JSON conversion.
// No hamba calls at runtime; all schema knowledge is already compiled into AvroProgram.

// AppendAvroToJSON decodes Avro binary src into JSON, appending to dst.
// prog must be compiled for the exact schema that produced src.
// The returned slice is always a valid JSON object.
func AppendAvroToJSON(dst, src []byte, prog *AvroProgram) ([]byte, error) {
	var inlineStack [8]loopState
	stackDepth := 0
	var extStack *loopStack

	out, _, err := decodeRecord(dst, src, prog, prog.Ops, &inlineStack, &stackDepth, &extStack)
	if extStack != nil {
		extStack.frames = extStack.frames[:0]
		loopStackPool.Put(extStack)
	}
	return out, err
}

// decodeRecord decodes a sequence of ops (record fields) into a JSON object.
// Returns (appended dst, remaining src, error).
func decodeRecord(dst, src []byte, prog *AvroProgram, ops []AvroOp,
	inlineStack *[8]loopState, stackDepth *int, extStack **loopStack) ([]byte, []byte, error) {

	dst = append(dst, '{')
	first := true
	for i, op := range ops {
		skip := op.Flags&AvroFlagSkip != 0

		if !skip {
			if !first {
				dst = append(dst, ',')
			}
			first = false
			dst = appendJSONKey(dst, prog.fieldName(op))
		}

		var err error
		dst, src, err = decodeValue(dst, src, prog, i, op, skip, inlineStack, stackDepth, extStack)
		if err != nil {
			return dst, src, err
		}
	}
	dst = append(dst, '}')
	return dst, src, nil
}

// decodeValue decodes one Avro value from src and (unless skip) appends its JSON to dst.
// When skip=true, dst is unchanged and bytes are still consumed from src.
func decodeValue(dst, src []byte, prog *AvroProgram, opIdx int, op AvroOp, skip bool,
	inlineStack *[8]loopState, stackDepth *int, extStack **loopStack) ([]byte, []byte, error) {

	switch op.Kind {
	case AvroKindNull:
		// 0 bytes; JSON: null
		if !skip {
			dst = append(dst, "null"...)
		}
		return dst, src, nil

	case AvroKindBool:
		if len(src) < 1 {
			return dst, src, ErrBadWireData
		}
		b := src[0]
		src = src[1:]
		if !skip {
			if b != 0 {
				dst = append(dst, "true"...)
			} else {
				dst = append(dst, "false"...)
			}
		}
		return dst, src, nil

	case AvroKindInt, AvroKindLong:
		n, nc, ok := readLong(src)
		if !ok {
			return dst, src, ErrBadWireData
		}
		src = src[nc:]
		if !skip {
			dst = appendInt64(dst, n)
		}
		return dst, src, nil

	case AvroKindFloat:
		if len(src) < 4 {
			return dst, src, ErrBadWireData
		}
		bits := binary.LittleEndian.Uint32(src[:4])
		src = src[4:]
		if !skip {
			f := math.Float32frombits(bits)
			dst = appendFloat64(dst, float64(f), 32)
		}
		return dst, src, nil

	case AvroKindDouble:
		if len(src) < 8 {
			return dst, src, ErrBadWireData
		}
		bits := binary.LittleEndian.Uint64(src[:8])
		src = src[8:]
		if !skip {
			f := math.Float64frombits(bits)
			dst = appendFloat64(dst, f, 64)
		}
		return dst, src, nil

	case AvroKindBytes:
		n, nc, ok := readLong(src)
		if !ok {
			return dst, src, ErrBadWireData
		}
		src = src[nc:]
		if n < 0 || int(n) > len(src) {
			return dst, src, ErrBadWireData
		}
		val := src[:n]
		src = src[n:]
		if !skip {
			dst = appendJSONBytes(dst, val)
		}
		return dst, src, nil

	case AvroKindString:
		n, nc, ok := readLong(src)
		if !ok {
			return dst, src, ErrBadWireData
		}
		src = src[nc:]
		if n < 0 || int(n) > len(src) {
			return dst, src, ErrBadWireData
		}
		val := src[:n]
		src = src[n:]
		if !skip {
			dst = appendJSONString(dst, val)
		}
		return dst, src, nil

	case AvroKindEnum:
		idx, nc, ok := readLong(src)
		if !ok {
			return dst, src, ErrBadWireData
		}
		src = src[nc:]
		if !skip {
			if idx >= 0 && int(idx) < len(prog.Symbols) {
				dst = appendJSONString(dst, prog.Symbols[idx])
			} else {
				dst = appendInt64(dst, idx)
			}
		}
		return dst, src, nil

	case AvroKindFixed:
		size := int(op.Param)
		if len(src) < size {
			return dst, src, ErrBadWireData
		}
		val := src[:size]
		src = src[size:]
		if !skip {
			dst = appendJSONBytes(dst, val)
		}
		return dst, src, nil

	case AvroKindRecord:
		children := prog.childOpsFor(opIdx)
		childOps := opsFromIndices(prog, children)
		if skip {
			tmp := make([]byte, 0, 32)
			var err error
			_, src, err = decodeRecord(tmp, src, prog, childOps, inlineStack, stackDepth, extStack)
			return dst, src, err
		}
		return decodeRecord(dst, src, prog, childOps, inlineStack, stackDepth, extStack)

	case AvroKindArray:
		return decodeArray(dst, src, prog, opIdx, skip, inlineStack, stackDepth, extStack)

	case AvroKindMap:
		return decodeMap(dst, src, prog, opIdx, skip, inlineStack, stackDepth, extStack)

	case AvroKindUnion:
		armIdx, nc, ok := readLong(src)
		if !ok {
			return dst, src, ErrBadWireData
		}
		src = src[nc:]
		children := prog.childOpsFor(opIdx)
		if armIdx < 0 || int(armIdx) >= len(children) {
			return dst, src, ErrBadWireData
		}
		childOpIdx := children[armIdx]
		childOp := prog.Ops[childOpIdx]
		return decodeValue(dst, src, prog, childOpIdx, childOp, skip, inlineStack, stackDepth, extStack)
	}

	return dst, src, ErrBadWireData
}

// opsFromIndices builds a []AvroOp slice from a list of op indices.
func opsFromIndices(prog *AvroProgram, indices []int) []AvroOp {
	ops := make([]AvroOp, len(indices))
	for i, idx := range indices {
		ops[i] = prog.Ops[idx]
	}
	return ops
}

// decodeArray decodes an Avro array (block-encoded) into a JSON array.
func decodeArray(dst, src []byte, prog *AvroProgram, arrayOpIdx int, skip bool,
	inlineStack *[8]loopState, stackDepth *int, extStack **loopStack) ([]byte, []byte, error) {

	if !skip {
		dst = append(dst, '[')
	}
	first := true

	children := prog.childOpsFor(arrayOpIdx)

	for {
		count, nc, ok := readLong(src)
		if !ok {
			return dst, src, ErrBadWireData
		}
		src = src[nc:]
		if count == 0 {
			break
		}
		absCount := count
		if count < 0 {
			// negative block: next long is block byte size (skip if no item ops needed)
			_, sc, ok2 := readLong(src)
			if !ok2 {
				return dst, src, ErrBadWireData
			}
			src = src[sc:]
			absCount = -count
		}

		if len(children) == 0 {
			// No item op; can't consume individual items without schema. Error.
			return dst, src, ErrBadWireData
		}
		itemOpIdx := children[0]
		itemOp := prog.Ops[itemOpIdx]

		for j := int64(0); j < absCount; j++ {
			if !skip && !first {
				dst = append(dst, ',')
			}
			first = false
			var err error
			dst, src, err = decodeValue(dst, src, prog, itemOpIdx, itemOp, skip, inlineStack, stackDepth, extStack)
			if err != nil {
				return dst, src, err
			}
		}
	}

	if !skip {
		dst = append(dst, ']')
	}
	return dst, src, nil
}

// decodeMap decodes an Avro map (block-encoded) into a JSON object.
func decodeMap(dst, src []byte, prog *AvroProgram, mapOpIdx int, skip bool,
	inlineStack *[8]loopState, stackDepth *int, extStack **loopStack) ([]byte, []byte, error) {

	if !skip {
		dst = append(dst, '{')
	}
	first := true

	children := prog.childOpsFor(mapOpIdx)

	for {
		count, nc, ok := readLong(src)
		if !ok {
			return dst, src, ErrBadWireData
		}
		src = src[nc:]
		if count == 0 {
			break
		}
		absCount := count
		if count < 0 {
			_, sc, ok2 := readLong(src)
			if !ok2 {
				return dst, src, ErrBadWireData
			}
			src = src[sc:]
			absCount = -count
		}

		if len(children) == 0 {
			return dst, src, ErrBadWireData
		}
		valOpIdx := children[0]
		valOp := prog.Ops[valOpIdx]

		for j := int64(0); j < absCount; j++ {
			// Read key string
			kLen, kc, ok2 := readLong(src)
			if !ok2 {
				return dst, src, ErrBadWireData
			}
			src = src[kc:]
			if kLen < 0 || int(kLen) > len(src) {
				return dst, src, ErrBadWireData
			}
			key := src[:kLen]
			src = src[kLen:]

			if !skip {
				if !first {
					dst = append(dst, ',')
				}
				first = false
				dst = appendJSONString(dst, key)
				dst = append(dst, ':')
			}

			var err error
			dst, src, err = decodeValue(dst, src, prog, valOpIdx, valOp, skip, inlineStack, stackDepth, extStack)
			if err != nil {
				return dst, src, err
			}
		}
	}

	if !skip {
		dst = append(dst, '}')
	}
	return dst, src, nil
}

// childOpsFor returns the child op indices for the op at opIdx in prog.ChildOps.
func (p *AvroProgram) childOpsFor(opIdx int) []int {
	if opIdx >= len(p.ChildOps) {
		return nil
	}
	return p.ChildOps[opIdx]
}

// ---------------------------------------------------------------------------
// AppendJSONToAvro
// ---------------------------------------------------------------------------

// AppendJSONToAvro encodes JSON src into Avro binary, appending to dst.
// prog must be compiled for the target schema.
// Uses gjson for field lookup — no encoding/json at runtime.
func AppendJSONToAvro(dst, src []byte, prog *AvroProgram) ([]byte, error) {
	return encodeRecord(dst, src, prog, prog.Ops)
}

// encodeRecord encodes a JSON object (src) into Avro binary for a sequence of ops.
func encodeRecord(dst, src []byte, prog *AvroProgram, ops []AvroOp) ([]byte, error) {
	for i, op := range ops {
		name := string(prog.fieldName(op))
		// For top-level ops we look up by field name; the opIdx here is relative
		// to the ops slice — we need the absolute opIdx for ChildOps.
		// encodeRecord is called with prog.Ops directly so index matches.
		result := gjson.GetBytes(src, name)
		var err error
		dst, err = encodeValue(dst, src, prog, i, op, result)
		if err != nil {
			return dst, err
		}
	}
	return dst, nil
}

// encodeValue encodes a single gjson.Result as Avro binary for the given op.
func encodeValue(dst, src []byte, prog *AvroProgram, opIdx int, op AvroOp, result gjson.Result) ([]byte, error) {
	switch op.Kind {
	case AvroKindNull:
		// 0 bytes
		return dst, nil

	case AvroKindBool:
		if result.Bool() {
			return append(dst, 1), nil
		}
		return append(dst, 0), nil

	case AvroKindInt, AvroKindLong:
		return appendLong(dst, result.Int()), nil

	case AvroKindFloat:
		f := float32(result.Float())
		bits := math.Float32bits(f)
		return append(dst, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24)), nil

	case AvroKindDouble:
		f := result.Float()
		bits := math.Float64bits(f)
		return append(dst,
			byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24),
			byte(bits>>32), byte(bits>>40), byte(bits>>48), byte(bits>>56)), nil

	case AvroKindBytes:
		// Accept hex-encoded string (our JSON output format) or raw string bytes
		b := hexDecodeOrBytes(result.String())
		dst = appendLong(dst, int64(len(b)))
		return append(dst, b...), nil

	case AvroKindString:
		s := result.String()
		dst = appendLong(dst, int64(len(s)))
		return append(dst, s...), nil

	case AvroKindEnum:
		sym := result.String()
		for idx, s := range prog.Symbols {
			if string(s) == sym {
				return appendLong(dst, int64(idx)), nil
			}
		}
		// Fallback: treat as integer index
		return appendLong(dst, result.Int()), nil

	case AvroKindFixed:
		size := int(op.Param)
		b := hexDecodeOrBytes(result.String())
		if len(b) >= size {
			return append(dst, b[:size]...), nil
		}
		// Zero-pad
		dst = append(dst, b...)
		for i := len(b); i < size; i++ {
			dst = append(dst, 0)
		}
		return dst, nil

	case AvroKindRecord:
		children := prog.childOpsFor(opIdx)
		childOps := opsFromIndices(prog, children)
		raw := []byte(result.Raw)
		if len(raw) == 0 {
			raw = []byte("{}")
		}
		return encodeRecord(dst, raw, prog, childOps)

	case AvroKindArray:
		children := prog.childOpsFor(opIdx)
		if len(children) == 0 {
			return appendLong(dst, 0), nil
		}
		itemOpIdx := children[0]
		itemOp := prog.Ops[itemOpIdx]

		var items []gjson.Result
		result.ForEach(func(_, v gjson.Result) bool {
			items = append(items, v)
			return true
		})
		if len(items) > 0 {
			dst = appendLong(dst, int64(len(items)))
			for _, item := range items {
				var err error
				dst, err = encodeValue(dst, src, prog, itemOpIdx, itemOp, item)
				if err != nil {
					return dst, err
				}
			}
		}
		return appendLong(dst, 0), nil

	case AvroKindMap:
		children := prog.childOpsFor(opIdx)
		if len(children) == 0 {
			return appendLong(dst, 0), nil
		}
		valOpIdx := children[0]
		valOp := prog.Ops[valOpIdx]

		var count int64
		var pairs []byte
		var encErr error
		result.ForEach(func(k, v gjson.Result) bool {
			count++
			key := k.String()
			pairs = appendLong(pairs, int64(len(key)))
			pairs = append(pairs, key...)
			pairs, encErr = encodeValue(pairs, src, prog, valOpIdx, valOp, v)
			return encErr == nil
		})
		if encErr != nil {
			return dst, encErr
		}
		if count > 0 {
			dst = appendLong(dst, count)
			dst = append(dst, pairs...)
		}
		return appendLong(dst, 0), nil

	case AvroKindUnion:
		children := prog.childOpsFor(opIdx)
		// Null value
		if !result.Exists() || result.Type == gjson.Null {
			for ai, ci := range children {
				if prog.Ops[ci].Kind == AvroKindNull {
					return appendLong(dst, int64(ai)), nil
				}
			}
			// Default: arm 0
			return appendLong(dst, 0), nil
		}
		// Non-null: pick first non-null arm
		for ai, ci := range children {
			childOp := prog.Ops[ci]
			if childOp.Kind == AvroKindNull {
				continue
			}
			dst = appendLong(dst, int64(ai))
			return encodeValue(dst, src, prog, ci, childOp, result)
		}
		return appendLong(dst, 0), nil
	}

	return dst, nil
}

// hexDecodeOrBytes attempts hex-decoding a string; returns raw UTF-8 bytes on failure.
func hexDecodeOrBytes(s string) []byte {
	if len(s)%2 != 0 {
		return []byte(s)
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		hi, ok1 := hexVal(s[i])
		lo, ok2 := hexVal(s[i+1])
		if !ok1 || !ok2 {
			return []byte(s)
		}
		out[i/2] = hi<<4 | lo
	}
	return out
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// JSON output helpers (no encoding/json)
// ---------------------------------------------------------------------------

// appendJSONKey appends `"name":` to dst.
func appendJSONKey(dst, name []byte) []byte {
	dst = appendJSONString(dst, name)
	return append(dst, ':')
}

// appendJSONString appends a JSON-encoded string (with quotes and escaping) to dst.
func appendJSONString(dst, s []byte) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			if c < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0',
					hexNibble(c>>4), hexNibble(c&0xf))
			} else {
				dst = append(dst, c)
			}
		}
	}
	return append(dst, '"')
}

// appendJSONBytes appends a JSON string containing the hex encoding of raw bytes.
func appendJSONBytes(dst, b []byte) []byte {
	dst = append(dst, '"')
	for _, c := range b {
		dst = append(dst, hexNibble(c>>4), hexNibble(c&0xf))
	}
	return append(dst, '"')
}

func hexNibble(n byte) byte {
	if n < 10 {
		return '0' + n
	}
	return 'a' + n - 10
}

// appendInt64 appends the decimal representation of n to dst.
func appendInt64(dst []byte, n int64) []byte {
	if n == 0 {
		return append(dst, '0')
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	u := uint64(n)
	if neg {
		u = uint64(-n)
	}
	for u > 0 {
		pos--
		buf[pos] = byte(u%10) + '0'
		u /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return append(dst, buf[pos:]...)
}

// appendFloat64 appends a float64 as JSON number to dst.
// bits is 32 for float32-origin, 64 for float64.
func appendFloat64(dst []byte, f float64, bits int) []byte {
	if math.IsNaN(f) {
		return append(dst, `"NaN"`...)
	}
	if math.IsInf(f, 1) {
		return append(dst, `"Infinity"`...)
	}
	if math.IsInf(f, -1) {
		return append(dst, `"-Infinity"`...)
	}
	return appendStrconvFloat(dst, f, bits)
}
