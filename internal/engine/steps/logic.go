package steps

import (
	"encoding/binary"
	"rah/internal/engine"
	"rah/internal/rctx"
	"strings"

	"github.com/tidwall/gjson"
)

// NewComplexLogicGate compiles condition into a ConditionFunc closure at bake
// time and wraps it in an IF_GATE instruction. Falls back to the legacy RPN
// evaluator when CompileCondition returns an error (e.g. unknown slot names
// used in old-style conditions).
func NewComplexLogicGate(condition string, thenID, elseID int16, slotMap map[string]int) engine.Instruction {
	condFn, err := CompileCondition(condition, slotMap)
	if err != nil {
		// Fallback: legacy RPN path for old-style bare-slot conditions.
		rpnStack := parseToRPN(condition, slotMap)
		condFn = func(ctx *rctx.Context) bool { return evaluateRPN(ctx, rpnStack) }
	}

	return engine.Instruction{
		Name: "IF_GATE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if condFn(ctx) {
				return thenID
			}
			return elseID
		},
	}
}

const (
	OpAnd = -1
	OpOr  = -2
)

func evaluateRPN(ctx *rctx.Context, stack []int) bool {
	if len(stack) == 0 {
		return false
	}
	var results []bool
	for _, val := range stack {
		switch val {
		case OpAnd:
			if len(results) < 2 {
				return false
			}
			l, r := results[len(results)-2], results[len(results)-1]
			results = results[:len(results)-2]
			results = append(results, l && r)
		case OpOr:
			if len(results) < 2 {
				return false
			}
			l, r := results[len(results)-2], results[len(results)-1]
			results = results[:len(results)-2]
			results = append(results, l || r)
		default:
			// Treat non-empty byte slot as "true"; out-of-range slot = false
			if val >= 0 && val < len(ctx.ByteSlots) {
				results = append(results, len(ctx.ByteSlots[val]) > 0)
			} else {
				results = append(results, false)
			}
		}
	}
	if len(results) == 0 {
		return false
	}
	return results[0]
}

// parseToRPN converts a simple boolean condition string into a postfix (RPN)
// integer stack. Variable names are replaced with their slot IDs from slotMap.
// Supports: single variables, && (AND), || (OR), left-to-right evaluation.
// Parentheses are stripped (no precedence grouping beyond left-to-right).
//
// Examples:
//
//	"header.X-Admin"           → [slotID]
//	"header.X-Admin && query.role" → [slotA, slotB, OpAnd]
func parseToRPN(cond string, slotMap map[string]int) []int {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return nil
	}
	tokens := strings.Fields(cond)
	var result []int
	var pendingOp *int
	for _, tok := range tokens {
		tok = strings.Trim(tok, "()")
		switch tok {
		case "&&":
			op := OpAnd
			pendingOp = &op
		case "||":
			op := OpOr
			pendingOp = &op
		default:
			idx, ok := slotMap[tok]
			if !ok {
				continue // unknown variable — skip
			}
			result = append(result, idx)
			if pendingOp != nil {
				result = append(result, *pendingOp)
				pendingOp = nil
			}
		}
	}
	return result
}

// NewComplexLogicGate creates an instruction that evaluates boolean logic
// against ByteSlots using a pre-compiled RPN stack.

func executeLogicPlan(ctx *rctx.Context, plan []int) bool {
	// Logic implementation:
	// Positive numbers = Slot IDs (Check if non-empty)
	// -1 = AND, -2 = OR
	var stack []bool
	for _, op := range plan {
		if op >= 0 {
			stack = append(stack, len(ctx.ByteSlots[op]) > 0)
		} else if op == -1 { // AND
			l, r := stack[len(stack)-2], stack[len(stack)-1]
			stack = stack[:len(stack)-2]
			stack = append(stack, l && r)
		} // ... handle OR (-2) etc.
	}
	return stack[0]
}

func executeLogic(ctx *rctx.Context, plan []int) bool {
	var stack []bool
	for _, op := range plan {
		if op >= 0 {
			// Check if the slot has data (exists/true)
			stack = append(stack, len(ctx.ByteSlots[op]) > 0)
		} else {
			// Handle Operators: -1 (AND), -2 (OR)
			b := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			a := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if op == -1 {
				stack = append(stack, a && b)
			}
			if op == -2 {
				stack = append(stack, a || b)
			}
		}
	}
	return stack[0]
}

func LoopGate(source string, valueSlot int, iterSlot int, bodyStart int16, exitID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		items := ctx.GetCollection(source)
		idx := ctx.GetInt(iterSlot)

		if idx >= int64(len(items)) {
			ctx.SetInt(iterSlot, 0) // Reset for future calls
			return exitID           // JUMP OUT
		}

		ctx.SetSlot(valueSlot, items[idx])
		ctx.SetInt(iterSlot, idx+1)
		return bodyStart // JUMP INTO BODY
	}
}

func LoopRepeat(gateID int16, iterSlot int) engine.InstructionFunc {
	return func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
		ctx.IntSlots[iterSlot]++ // Increment the iterator
		return gateID            // Jump back to the LoopGate check
	}
}

// LoopGateSlot iterates over a JSON array stored in ctx.ByteSlots[sourceSlot].
// Each iteration writes the raw JSON element to ctx.ByteSlots[valueSlot].
// Uses iterSlot (IntSlot) as the loop counter and indexSlot (ByteSlot) as a
// packed (start,end uint32) index built once at iteration 0 — O(1) per step.
func LoopGateSlot(sourceSlot, valueSlot, indexSlot, iterSlot int, bodyStart, exitID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		raw := ctx.ByteSlots[sourceSlot]
		if len(raw) == 0 {
			return exitID
		}
		idx := ctx.GetInt(iterSlot)

		// Build the packed index on the first iteration (idx == 0).
		if idx == 0 {
			arr := gjson.ParseBytes(raw)
			if !arr.IsArray() {
				return exitID
			}
			count := 0
			arr.ForEach(func(_, _ gjson.Result) bool { count++; return true })
			if count == 0 {
				return exitID
			}
			// Pack (uint32 start, uint32 end) per element — 8 bytes each.
			buf := ctx.Alloc(count * 8)
			i := 0
			arr.ForEach(func(_, v gjson.Result) bool {
				start := uint32(v.Index)
				end := uint32(v.Index + len(v.Raw))
				binary.LittleEndian.PutUint32(buf[i*8:], start)
				binary.LittleEndian.PutUint32(buf[i*8+4:], end)
				i++
				return true
			})
			ctx.ByteSlots[indexSlot] = buf
		}

		index := ctx.ByteSlots[indexSlot]
		count := int64(len(index) / 8)
		if idx >= count {
			ctx.SetInt(iterSlot, 0)
			ctx.ByteSlots[indexSlot] = nil // release cached index
			return exitID
		}

		start := binary.LittleEndian.Uint32(index[idx*8:])
		end := binary.LittleEndian.Uint32(index[idx*8+4:])
		ctx.ByteSlots[valueSlot] = raw[start:end] // zero-copy slice
		ctx.SetInt(iterSlot, idx+1)
		return bodyStart
	}
}

// WhileGate loops while condFn(ctx) is true, up to maxIter times.
// iterSlot (IntSlot) tracks the iteration count; reset to 0 on exit.
// maxIter <= 0 defaults to 100 to prevent infinite loops.
func WhileGate(condFn ConditionFunc, iterSlot int, maxIter int, bodyStart int16, exitID int16) engine.InstructionFunc {
	limit := int64(maxIter)
	if limit <= 0 {
		limit = 100
	}
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		iter := ctx.GetInt(iterSlot)
		if !condFn(ctx) || iter >= limit {
			ctx.SetInt(iterSlot, 0)
			return exitID
		}
		ctx.SetInt(iterSlot, iter+1)
		return bodyStart
	}
}

// WhileRepeat jumps back to the WhileGate check without incrementing the counter
// (WhileGate itself handles counting).
func WhileRepeat(gateID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
		return gateID
	}
}

func CallFragment(entryID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		// Push the NEXT instruction after this one onto the return stack
		state.LinkStack[state.StackPtr] = state.PC + 1
		state.StackPtr++
		return entryID // JUMP to shared flow
	}
}

func Return() engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		state.StackPtr--
		return state.LinkStack[state.StackPtr] // JUMP BACK
	}
}

// internal/engine/steps/logic.go

func InternalJump(targetID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		return targetID // Simple absolute jump back to the LoopGate
	}
}

// ForeachHeader iterates over HTTP request headers.
// On iteration 0: builds a packed index of (nameOff, nameLen, valOff, valLen) tuples
// with actual name/value data appended, stored in indexSlot.
// On iteration N: reads pair N from the packed index and extracts into nameSlot, valueSlot.
// When exhausted, jumps to exitID.
func ForeachHeader(nameSlot, valueSlot, indexSlot, iterSlot int, bodyStart, exitID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		idx := int(ctx.IntSlots[iterSlot])
		if idx == 0 {
			// Build flat list of (name, value) pairs from ctx.Request.Header (map[string][]string)
			count := 0
			for _, vals := range ctx.Request.Header {
				count += len(vals)
			}
			if count == 0 {
				return exitID
			}

			// Calculate total data size
			dataSize := 0
			for k, vals := range ctx.Request.Header {
				for _, v := range vals {
					dataSize += len(k) + len(v)
				}
			}

			// Allocate: [count uint32][16*count index bytes][data]
			buf := ctx.Alloc(4 + 16*count + dataSize)
			binary.LittleEndian.PutUint32(buf[0:4], uint32(count))
			indexBuf := buf[4 : 4+16*count]
			dataBuf := buf[4+16*count:]

			entryIdx := 0
			dataOff := 0
			for k, vals := range ctx.Request.Header {
				for _, v := range vals {
					// Store (nameOff, nameLen, valOff, valLen)
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16:], uint32(dataOff))
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16+4:], uint32(len(k)))
					dataOff2 := dataOff + len(k)
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16+8:], uint32(dataOff2))
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16+12:], uint32(len(v)))
					// Copy name and value data
					copy(dataBuf[dataOff:], k)
					copy(dataBuf[dataOff2:], v)
					dataOff += len(k) + len(v)
					entryIdx++
				}
			}
			ctx.ByteSlots[indexSlot] = buf
		}

		// Read from the packed index
		buf := ctx.ByteSlots[indexSlot]
		if len(buf) < 4 {
			return exitID
		}
		total := int(binary.LittleEndian.Uint32(buf[0:4]))
		if idx >= total {
			ctx.ByteSlots[indexSlot] = nil
			ctx.IntSlots[iterSlot] = 0
			return exitID
		}

		indexBuf := buf[4 : 4+16*total]
		dataBuf := buf[4+16*total:]
		nameOff := binary.LittleEndian.Uint32(indexBuf[idx*16:])
		nameLen := binary.LittleEndian.Uint32(indexBuf[idx*16+4:])
		valOff := binary.LittleEndian.Uint32(indexBuf[idx*16+8:])
		valLen := binary.LittleEndian.Uint32(indexBuf[idx*16+12:])
		ctx.ByteSlots[nameSlot] = dataBuf[nameOff : nameOff+nameLen]
		ctx.ByteSlots[valueSlot] = dataBuf[valOff : valOff+valLen]
		ctx.IntSlots[iterSlot]++
		return bodyStart
	}
}

// ForeachParam iterates over URL query parameters.
// Parses ctx.Request.URL.RawQuery without allocation, building a packed index
// of (keyOff, keyLen, valOff, valLen) tuples with decoded key/value data.
func ForeachParam(nameSlot, valueSlot, indexSlot, iterSlot int, bodyStart, exitID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		idx := int(ctx.IntSlots[iterSlot])
		if idx == 0 {
			rawQuery := ctx.Request.URL.RawQuery
			if rawQuery == "" {
				return exitID
			}

			// Count parameters
			count := 0
			for i := 0; i < len(rawQuery); i++ {
				if rawQuery[i] == '&' {
					count++
				}
			}
			count++ // at least one param if non-empty

			// First pass: calculate decoded data size
			dataSize := 0
			segments := strings.Split(rawQuery, "&")
			for _, seg := range segments {
				if eqIdx := strings.IndexByte(seg, '='); eqIdx >= 0 {
					key := seg[:eqIdx]
					val := seg[eqIdx+1:]
					// URL decode sizes (worst case: no decoding needed)
					dataSize += len(key) + len(val)
				}
			}

			if dataSize == 0 {
				return exitID
			}

			// Allocate: [count uint32][16*count index bytes][data]
			buf := ctx.Alloc(4 + 16*count + dataSize)
			binary.LittleEndian.PutUint32(buf[0:4], uint32(len(segments)))
			indexBuf := buf[4 : 4+16*len(segments)]
			dataBuf := buf[4+16*len(segments):]

			entryIdx := 0
			dataOff := 0
			for _, seg := range segments {
				if eqIdx := strings.IndexByte(seg, '='); eqIdx >= 0 {
					key := seg[:eqIdx]
					val := seg[eqIdx+1:]
					// Store (keyOff, keyLen, valOff, valLen)
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16:], uint32(dataOff))
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16+4:], uint32(len(key)))
					dataOff2 := dataOff + len(key)
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16+8:], uint32(dataOff2))
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16+12:], uint32(len(val)))
					// Copy data
					copy(dataBuf[dataOff:], key)
					copy(dataBuf[dataOff2:], val)
					dataOff += len(key) + len(val)
					entryIdx++
				}
			}
			ctx.ByteSlots[indexSlot] = buf
		}

		// Read from the packed index
		buf := ctx.ByteSlots[indexSlot]
		if len(buf) < 4 {
			return exitID
		}
		total := int(binary.LittleEndian.Uint32(buf[0:4]))
		if idx >= total {
			ctx.ByteSlots[indexSlot] = nil
			ctx.IntSlots[iterSlot] = 0
			return exitID
		}

		indexBuf := buf[4 : 4+16*total]
		dataBuf := buf[4+16*total:]
		keyOff := binary.LittleEndian.Uint32(indexBuf[idx*16:])
		keyLen := binary.LittleEndian.Uint32(indexBuf[idx*16+4:])
		valOff := binary.LittleEndian.Uint32(indexBuf[idx*16+8:])
		valLen := binary.LittleEndian.Uint32(indexBuf[idx*16+12:])
		ctx.ByteSlots[nameSlot] = dataBuf[keyOff : keyOff+keyLen]
		ctx.ByteSlots[valueSlot] = dataBuf[valOff : valOff+valLen]
		ctx.IntSlots[iterSlot]++
		return bodyStart
	}
}

// ForeachCookie iterates over HTTP request cookies.
// Parses the Cookie header value (format: "name=value; name2=value2")
// and builds a packed index of (nameOff, nameLen, valOff, valLen) tuples.
func ForeachCookie(nameSlot, valueSlot, indexSlot, iterSlot int, bodyStart, exitID int16) engine.InstructionFunc {
	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
		idx := int(ctx.IntSlots[iterSlot])
		if idx == 0 {
			cookieHeader := ctx.Request.Header.Get("Cookie")
			if cookieHeader == "" {
				return exitID
			}

			// Count semicolon-delimited segments
			count := 0
			for i := 0; i < len(cookieHeader); i++ {
				if cookieHeader[i] == ';' {
					count++
				}
			}
			count++ // at least one cookie if non-empty

			// Calculate data size
			dataSize := 0
			segments := strings.Split(cookieHeader, ";")
			for _, seg := range segments {
				seg = strings.TrimSpace(seg)
				if eqIdx := strings.IndexByte(seg, '='); eqIdx >= 0 {
					dataSize += len(seg[:eqIdx]) + len(seg[eqIdx+1:])
				}
			}

			if dataSize == 0 {
				return exitID
			}

			// Allocate: [count uint32][16*count index bytes][data]
			buf := ctx.Alloc(4 + 16*count + dataSize)
			binary.LittleEndian.PutUint32(buf[0:4], uint32(len(segments)))
			indexBuf := buf[4 : 4+16*len(segments)]
			dataBuf := buf[4+16*len(segments):]

			entryIdx := 0
			dataOff := 0
			for _, seg := range segments {
				seg = strings.TrimSpace(seg)
				if eqIdx := strings.IndexByte(seg, '='); eqIdx >= 0 {
					name := seg[:eqIdx]
					val := seg[eqIdx+1:]
					// Store (nameOff, nameLen, valOff, valLen)
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16:], uint32(dataOff))
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16+4:], uint32(len(name)))
					dataOff2 := dataOff + len(name)
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16+8:], uint32(dataOff2))
					binary.LittleEndian.PutUint32(indexBuf[entryIdx*16+12:], uint32(len(val)))
					// Copy data
					copy(dataBuf[dataOff:], name)
					copy(dataBuf[dataOff2:], val)
					dataOff += len(name) + len(val)
					entryIdx++
				}
			}
			ctx.ByteSlots[indexSlot] = buf
		}

		// Read from the packed index
		buf := ctx.ByteSlots[indexSlot]
		if len(buf) < 4 {
			return exitID
		}
		total := int(binary.LittleEndian.Uint32(buf[0:4]))
		if idx >= total {
			ctx.ByteSlots[indexSlot] = nil
			ctx.IntSlots[iterSlot] = 0
			return exitID
		}

		indexBuf := buf[4 : 4+16*total]
		dataBuf := buf[4+16*total:]
		nameOff := binary.LittleEndian.Uint32(indexBuf[idx*16:])
		nameLen := binary.LittleEndian.Uint32(indexBuf[idx*16+4:])
		valOff := binary.LittleEndian.Uint32(indexBuf[idx*16+8:])
		valLen := binary.LittleEndian.Uint32(indexBuf[idx*16+12:])
		ctx.ByteSlots[nameSlot] = dataBuf[nameOff : nameOff+nameLen]
		ctx.ByteSlots[valueSlot] = dataBuf[valOff : valOff+valLen]
		ctx.IntSlots[iterSlot]++
		return bodyStart
	}
}
