package control

import (
	"fmt"
	"strings"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/engine/steps"
	"github.com/amitkhosla/rah/internal/rctx"
)

// compileValidateRoute compiles a "validate_route" step into a ValidateRouteInstruction.
// It:
//  1. Parses each RuleConfig into a CompiledRule (flat CondNode array).
//  2. Registers pendingValidateJumps for any DestJump rules (patched in second pass).
//  3. Appends the instruction to GlobalTable.
//
// The `default_next` input field names the step to jump to when no rule matches.
// If omitted, DefaultPC falls through to PC+1.
func (c *Compiler) compileValidateRoute(step StepConfig) error {
	instrIdx := len(c.GlobalTable)
	rules := make([]steps.CompiledRule, 0, len(step.Rules))

	for ruleIdx, rc := range step.Rules {
		rule, err := buildCompiledRule(rc)
		if err != nil {
			return fmt.Errorf("validate_route rule[%d] %q: %w", ruleIdx, rc.Label, err)
		}

		// Register pending validate jump for DestJump rules.
		if rule.DestKind == steps.DestJump && rc.OnMatch.TargetStep != "" {
			if targetPC, ok := c.FragmentMap[rc.OnMatch.TargetStep]; ok {
				// Already compiled â€” resolve immediately.
				rule.MatchPC = int(targetPC)
			} else {
				// Defer to second pass.
				c.pendingValidateJumps = append(c.pendingValidateJumps, validateRouteJump{
					instrIdx: instrIdx,
					ruleIdx:  ruleIdx,
					flowName: rc.OnMatch.TargetStep,
				})
			}
		}

		rules = append(rules, rule)
	}

	// defaultPC: fall through to next instruction by default.
	defaultPC := int16(instrIdx + 1)
	defaultNext := step.Input["default_next"]
	if defaultNext != "" {
		if targetPC, ok := c.FragmentMap[defaultNext]; ok {
			// Already compiled â€” resolve immediately.
			defaultPC = targetPC
		} else {
			// Defer to second pass; ruleIdx=-1 signals DefaultPC patch.
			c.pendingValidateJumps = append(c.pendingValidateJumps, validateRouteJump{
				instrIdx: instrIdx,
				ruleIdx:  -1,
				flowName: defaultNext,
			})
		}
	}

	// Build the instruction and store the pointer so second-pass can patch it.
	vr := &steps.ValidateRouteInstruction{Rules: rules, DefaultPC: defaultPC}
	c.validateRouteInstrs[instrIdx] = vr

	c.GlobalTable = append(c.GlobalTable, engine.Instruction{
		Name: "validate_route",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			return vr.Execute(ctx, state)
		},
	})
	return nil
}

// buildCompiledRule converts a RuleConfig into a CompiledRule with a flat CondNode tree.
func buildCompiledRule(rc RuleConfig) (steps.CompiledRule, error) {
	rule := steps.CompiledRule{
		Status:         rc.OnMatch.Status,
		TargetStepName: rc.OnMatch.TargetStep,
	}

	// Parse DestKind.
	switch strings.ToLower(rc.OnMatch.Dest) {
	case "jump":
		rule.DestKind = steps.DestJump
	case "fail":
		rule.DestKind = steps.DestFail
	case "continue":
		rule.DestKind = steps.DestContinue
	case "retry":
		rule.DestKind = steps.DestRetry
	default:
		rule.DestKind = steps.DestDefault
	}

	// Parse message template.
	if rc.OnMatch.Message != "" {
		rule.MsgParts = steps.ParseMsgTemplate(rc.OnMatch.Message)
	}

	// Build flat CondNode tree.
	paths := make([]string, 0, 4)
	strs := make([]string, 0, 4)
	nodes, err := buildCondNodes(rc.When, &paths, &strs)
	if err != nil {
		return rule, err
	}
	rule.Nodes = nodes
	rule.Paths = paths
	rule.Strings = strs

	return rule, nil
}

// buildCondNodes recursively converts a CondConfig into a flat pre-order []CondNode.
// Returns [compositeNode, child0_subtree..., child1_subtree..., ...].
// For nested composites, this produces a correct absolute index layout for evaluation.
//
// IMPORTANT: ChildStart is the absolute index of the first child's root node in the
// flat returned slice (which becomes part of CompiledRule.Nodes). For nested composites,
// direct children are NOT consecutive in the flat array (grandchildren are interspersed),
// so the evaluator must recurse to find each child's subtree.
func buildCondNodes(cc CondConfig, paths *[]string, strs *[]string) ([]steps.CondNode, error) {
	op := strings.ToLower(cc.Op)

	// Composite node (and / or).
	if op == "and" || op == "or" || (op == "" && len(cc.Children) > 1) {
		kind := steps.CondAnd
		if op == "or" {
			kind = steps.CondOr
		}

		// Build children and collect their absolute start indices.
		// The layout is [compositeNode, child0_root, child0_grandchildren..., child1_root, ...]
		allNodes := make([]steps.CondNode, 1) // reserve space for the composite node
		childRootIndices := make([]int, 0, len(cc.Children))

		for _, child := range cc.Children {
			childRootIdx := len(allNodes) // absolute index where this child's subtree begins
			childRootIndices = append(childRootIndices, childRootIdx)
			cn, err := buildCondNodes(child, paths, strs)
			if err != nil {
				return nil, err
			}
			allNodes = append(allNodes, cn...)
		}

		// Set the composite node with ChildStart pointing to the first child's root absolute index.
		compositeNode := steps.CondNode{
			Kind:       kind,
			ChildStart: uint16(childRootIndices[0]), // absolute index of first child's root
			ChildCount: uint8(len(cc.Children)),
		}
		allNodes[0] = compositeNode
		return allNodes, nil
	}

	// DIRECT or single leaf: treat as a leaf node.
	node := steps.CondNode{Kind: steps.CondLeaf}

	// SourceKind.
	switch strings.ToLower(cc.Source) {
	case "req_body", "":
		node.SourceKind = steps.SrcReqBody
	case "resp_body":
		node.SourceKind = steps.SrcRespBody
	case "req_header":
		node.SourceKind = steps.SrcReqHeader
	case "resp_header":
		node.SourceKind = steps.SrcRespHeader
	case "slot":
		node.SourceKind = steps.SrcSlot
	default:
		return nil, fmt.Errorf("unknown source %q", cc.Source)
	}

	// PathKey: for slots, store the index parsed from cc.Path; for others, intern the path string.
	if node.SourceKind == steps.SrcSlot {
		idx := 0
		for _, ch := range cc.Path {
			if ch >= '0' && ch <= '9' {
				idx = idx*10 + int(ch-'0')
			}
		}
		node.PathKey = uint16(idx)
	} else {
		node.PathKey = uint16(internString(paths, cc.Path))
	}

	// Check.
	switch strings.ToLower(cc.Check) {
	case "exists", "":
		node.Check = steps.CheckExists
	case "missing":
		node.Check = steps.CheckMissing
	case "eq":
		node.Check = steps.CheckEq
		node.ValueStr = uint16(internString(strs, cc.Value))
	case "neq":
		node.Check = steps.CheckNEq
		node.ValueStr = uint16(internString(strs, cc.Value))
	case "lt":
		node.Check = steps.CheckLt
		node.ValueFloat = cc.ValueNum
	case "gt":
		node.Check = steps.CheckGt
		node.ValueFloat = cc.ValueNum
	case "regex":
		node.Check = steps.CheckRegex
		pat, err := steps.CompileCondPattern(cc.Value)
		if err != nil {
			return nil, fmt.Errorf("regex compile: %w", err)
		}
		node.CompiledPattern = pat
		node.ValueStr = uint16(internString(strs, cc.Value))
	case "in":
		node.Check = steps.CheckIn
		joined := strings.Join(cc.InValues, ",")
		node.ValueStr = uint16(internString(strs, joined))
	default:
		return nil, fmt.Errorf("unknown check %q", cc.Check)
	}

	return []steps.CondNode{node}, nil
}

// internString appends s to *pool if not already present, returning its index.
func internString(pool *[]string, s string) int {
	for i, v := range *pool {
		if v == s {
			return i
		}
	}
	*pool = append(*pool, s)
	return len(*pool) - 1
}
