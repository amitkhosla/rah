package steps

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// ValidateRouteInstruction evaluates a list of CompiledRules against the current
// request/response. The first matching rule's OnMatch action is applied.
// If no rule matches, DefaultPC is returned.
type ValidateRouteInstruction struct {
	Rules     []CompiledRule
	DefaultPC int16
}

// NewValidateRoute wraps a ValidateRouteInstruction as an engine.Instruction.
func NewValidateRoute(rules []CompiledRule, defaultPC int16) engine.Instruction {
	v := &ValidateRouteInstruction{Rules: rules, DefaultPC: defaultPC}
	return engine.Instruction{
		Name: "validate_route",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			return v.Execute(ctx, state)
		},
	}
}

// Execute evaluates all rules and applies the first matching rule's dest action.
func (v *ValidateRouteInstruction) Execute(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	for i := range v.Rules {
		rule := &v.Rules[i]
		if evalNode(ctx, rule, 0, 0) {
			return applyDest(ctx, rule, state, v.DefaultPC)
		}
	}
	return v.DefaultPC
}

// evalNode evaluates one node (And/Or/Leaf) in the flat pre-order tree.
// depth is a safety limit; real flows are â‰¤4 deep so this is never hit.
//
// For composite nodes (And/Or), children are stored as subtrees in the flat array.
// ChildStart points to the first child's root node. To find siblings, we must
// compute each child's subtree size and skip past it to the next sibling's root.
func evalNode(ctx *rctx.Context, rule *CompiledRule, nodeIdx int, depth int) bool {
	if depth > 8 || nodeIdx >= len(rule.Nodes) {
		return false
	}
	node := &rule.Nodes[nodeIdx]
	switch node.Kind {
	case CondAnd:
		// For each direct child, evaluate its subtree and skip to the next sibling.
		childIdx := int(node.ChildStart)
		for c := 0; c < int(node.ChildCount); c++ {
			if childIdx >= len(rule.Nodes) {
				return false
			}
			if !evalNode(ctx, rule, childIdx, depth+1) {
				return false
			}
			// Skip to next sibling: compute current child's subtree size.
			childIdx = nextSiblingIdx(rule, childIdx)
		}
		return true
	case CondOr:
		// For each direct child, evaluate its subtree and skip to the next sibling.
		childIdx := int(node.ChildStart)
		for c := 0; c < int(node.ChildCount); c++ {
			if childIdx >= len(rule.Nodes) {
				return false
			}
			if evalNode(ctx, rule, childIdx, depth+1) {
				return true
			}
			// Skip to next sibling: compute current child's subtree size.
			childIdx = nextSiblingIdx(rule, childIdx)
		}
		return false
	default: // CondLeaf
		return evalLeaf(ctx, rule, node)
	}
}

// subtreeSize returns the number of nodes in the subtree rooted at nodeIdx.
// For a leaf, this is 1. For a composite, it's 1 + sum of children's subtree sizes.
func subtreeSize(rule *CompiledRule, nodeIdx int) int {
	if nodeIdx >= len(rule.Nodes) {
		return 0
	}
	node := &rule.Nodes[nodeIdx]
	if node.Kind == CondLeaf {
		return 1
	}
	// Composite node: sum the subtree sizes of all children.
	size := 1 // the composite node itself
	childIdx := int(node.ChildStart)
	for c := 0; c < int(node.ChildCount); c++ {
		childSize := subtreeSize(rule, childIdx)
		size += childSize
		childIdx += childSize // move to next sibling
	}
	return size
}

// nextSiblingIdx returns the index of the next sibling after the subtree rooted at nodeIdx.
func nextSiblingIdx(rule *CompiledRule, nodeIdx int) int {
	return nodeIdx + subtreeSize(rule, nodeIdx)
}

// evalLeaf extracts the value from the appropriate source and applies the check.
func evalLeaf(ctx *rctx.Context, rule *CompiledRule, node *CondNode) bool {
	switch node.SourceKind {
	case SrcReqBody:
		path := ""
		if int(node.PathKey) < len(rule.Paths) {
			path = rule.Paths[node.PathKey]
		}
		return evalBodyCheck(ctx.RequestBuffer, path, node, rule)

	case SrcRespBody:
		path := ""
		if int(node.PathKey) < len(rule.Paths) {
			path = rule.Paths[node.PathKey]
		}
		return evalBodyCheck(ctx.ResponseBuffer, path, node, rule)

	case SrcReqHeader:
		name := ""
		if int(node.PathKey) < len(rule.Paths) {
			name = rule.Paths[node.PathKey]
		}
		val := ""
		if ctx.Request != nil {
			val = ctx.Request.Header.Get(name)
		}
		return evalStringCheck(val, node, rule)

	case SrcRespHeader:
		name := ""
		if int(node.PathKey) < len(rule.Paths) {
			name = rule.Paths[node.PathKey]
		}
		val := findRespHeader(ctx, name)
		return evalStringCheck(val, node, rule)

	case SrcSlot:
		// PathKey holds the slot index directly (not an index into rule.Paths).
		slotIdx := int(node.PathKey)
		val := ""
		if slotIdx < len(ctx.ByteSlots) {
			val = string(ctx.ByteSlots[slotIdx])
		}
		return evalStringCheck(val, node, rule)

	default:
		return false
	}
}

func evalBodyCheck(body []byte, path string, node *CondNode, rule *CompiledRule) bool {
	if len(body) == 0 {
		return node.Check == CheckMissing
	}
	result := gjson.GetBytes(body, path)
	switch node.Check {
	case CheckExists:
		return result.Exists()
	case CheckMissing:
		return !result.Exists()
	case CheckEq:
		if int(node.ValueStr) < len(rule.Strings) {
			return result.String() == rule.Strings[node.ValueStr]
		}
		return false
	case CheckNEq:
		if int(node.ValueStr) < len(rule.Strings) {
			return result.String() != rule.Strings[node.ValueStr]
		}
		return false
	case CheckLt:
		return result.Float() < node.ValueFloat
	case CheckGt:
		return result.Float() > node.ValueFloat
	case CheckRegex:
		if node.CompiledPattern != nil {
			return node.CompiledPattern.MatchString(result.String())
		}
		return false
	case CheckIn:
		// Values are comma-joined in rule.Strings[node.ValueStr].
		if int(node.ValueStr) < len(rule.Strings) {
			s := result.String()
			for _, v := range strings.Split(rule.Strings[node.ValueStr], ",") {
				if v == s {
					return true
				}
			}
		}
		return false
	}
	return false
}

func evalStringCheck(val string, node *CondNode, rule *CompiledRule) bool {
	switch node.Check {
	case CheckExists:
		return val != ""
	case CheckMissing:
		return val == ""
	case CheckEq:
		if int(node.ValueStr) < len(rule.Strings) {
			return val == rule.Strings[node.ValueStr]
		}
		return false
	case CheckNEq:
		if int(node.ValueStr) < len(rule.Strings) {
			return val != rule.Strings[node.ValueStr]
		}
		return false
	case CheckLt:
		f := parseFloat64(val)
		return f < node.ValueFloat
	case CheckGt:
		f := parseFloat64(val)
		return f > node.ValueFloat
	case CheckRegex:
		if node.CompiledPattern != nil {
			return node.CompiledPattern.MatchString(val)
		}
		return false
	case CheckIn:
		if int(node.ValueStr) < len(rule.Strings) {
			for _, v := range strings.Split(rule.Strings[node.ValueStr], ",") {
				if v == val {
					return true
				}
			}
		}
		return false
	}
	return false
}

// findRespHeader scans ctx.ResponseHeaders for the first matching key (case-sensitive).
func findRespHeader(ctx *rctx.Context, name string) string {
	for i := 0; i < ctx.ResHeaderCount && i < len(ctx.ResponseHeaders); i++ {
		if string(ctx.ResponseHeaders[i].Key) == name {
			return string(ctx.ResponseHeaders[i].Value)
		}
	}
	return ""
}

// parseFloat64 converts a decimal string to float64 without using strconv.
// Handles negative numbers and one decimal point. Sufficient for numeric comparisons.
func parseFloat64(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	var intPart float64
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		intPart = intPart*10 + float64(s[i]-'0')
		i++
	}
	var fracPart float64
	if i < len(s) && s[i] == '.' {
		i++
		dec := 0.1
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			fracPart += float64(s[i]-'0') * dec
			dec *= 0.1
			i++
		}
	}
	result := intPart + fracPart
	if neg {
		return -result
	}
	return result
}

func applyDest(ctx *rctx.Context, rule *CompiledRule, state *engine.ExecutionState, defaultPC int16) int16 {
	switch rule.DestKind {
	case DestFail:
		msg := renderMsg(ctx, rule)
		ctx.ResponseStatus = rule.Status
		ctx.Failed = true
		if ctx.Writer != nil {
			ctx.Writer.WriteHeader(rule.Status)
			ctx.Writer.Write(msg) //nolint:errcheck
		}
		return engine.StopPlan

	case DestJump:
		return int16(rule.MatchPC)

	case DestContinue:
		return state.PC + 1

	case DestRetry:
		return int16(rule.MatchPC)

	case DestDefault:
		return defaultPC
	}
	return state.PC + 1
}

// renderMsg builds the failure message from pre-split MsgParts into ctx.ScratchBuffer.
// Reuses the scratch buffer — zero heap allocation in the common case.
func renderMsg(ctx *rctx.Context, rule *CompiledRule) []byte {
	if len(rule.MsgParts) == 0 {
		return nil
	}
	// Measure total length first to avoid reallocations.
	total := 0
	for _, p := range rule.MsgParts {
		if p.kind == msgPartLiteral {
			total += len(p.literal)
		} else if p.slotIdx < len(ctx.ByteSlots) {
			total += len(ctx.ByteSlots[p.slotIdx])
		}
	}
	// Grow scratch buffer only when needed; reuse existing capacity.
	if cap(ctx.ScratchBuffer) >= total {
		ctx.ScratchBuffer = ctx.ScratchBuffer[:total]
	} else {
		ctx.ScratchBuffer = make([]byte, total)
	}
	i := 0
	for _, p := range rule.MsgParts {
		if p.kind == msgPartLiteral {
			i += copy(ctx.ScratchBuffer[i:], p.literal)
		} else if p.slotIdx < len(ctx.ByteSlots) {
			i += copy(ctx.ScratchBuffer[i:], ctx.ByteSlots[p.slotIdx])
		}
	}
	return ctx.ScratchBuffer[:i]
}
