package control

import (
	"rah/internal/engine"
	"rah/internal/engine/steps"
	"strings"
)

/*
BakeSubRouter builds radix tree entries for a specific path + method.

ANY handling:
- Compile-time expansion into all methods.
- No runtime special handling.

Duplicate path+method results in panic.
*/

func (c *Compiler) BakeSubRouter(
	def *engine.ApiDefinition,
	path string,
	method string,
	plan []engine.Instruction,
	isStrict bool,
	apiRateLimitId uint16,
	endpointRateLimitId uint16,
	asyncMode engine.AsyncMode,
) uint32 {

	segments := strings.Split(strings.Trim(path, "/"), "/")
	if path == "" || path == "/" {
		segments = []string{""}
	}

	methodIdx := engine.MethodStringToIdx(method)
	isAny := method == "ANY"

	if len(def.Endpoints) > 255 {
		panic("endpoint limit exceeded: an ApiDefinition supports at most 256 endpoints")
	}
	def.Endpoints = append(def.Endpoints, engine.Endpoint{
		EndpointId:          uint8(len(def.Endpoints)),
		AsyncMode:           asyncMode,
		APIRateLimitId:      apiRateLimitId,
		EndpointRateLimitId: endpointRateLimitId,
		Plan:                plan,
	})
	epIdx := uint32(len(def.Endpoints) - 1)

	if len(def.SubArena) == 0 {
		def.SubArena = append(def.SubArena, engine.SubRouteNode{})
	}

	currIdx := uint32(0)

	// Collect BindPath instructions for each {param} in URL order.
	// These are prepended to the plan so they run before the flow steps,
	// exactly like BindHeader/BindQuery. paramIdx matches the order
	// resolveSubPath encounters params during radix traversal.
	var pathBindings []engine.Instruction
	paramIdx := 0

	for i, seg := range segments {
		isLast := i == len(segments)-1

		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {

			paramName := seg[1 : len(seg)-1]
			slot, _ := c.getSlot("path." + paramName)

			// Emit a BindPath instruction: reads ctx.Match.Params[paramIdx] (populated
			// by resolveSubPath) and writes to ctx.ByteSlots[slot].
			pathBindings = append(pathBindings, steps.BindPath(paramIdx, slot))
			paramIdx++

			newNode := engine.SubRouteNode{
				PrefixLen: uint16(len(seg)),
			}

			def.SubArena = append(def.SubArena, newNode)
			newIdx := uint32(len(def.SubArena) - 1)

			def.SubArena[currIdx].HasParamChild = true
			def.SubArena[currIdx].ParamChildIdx = newIdx

			currIdx = newIdx

			if isLast {
				registerTerminal(def, currIdx, epIdx, methodIdx, isAny, isStrict)
			}

		} else {

			currIdx = compileStaticSegment(
				def,
				currIdx,
				seg,
				isLast,
				epIdx,
				methodIdx,
				isAny,
				isStrict,
			)
		}
	}

	// Prepend BindPath instructions to the endpoint plan so they run before
	// any flow step, matching the header/query binding pattern.
	if len(pathBindings) > 0 {
		def.Endpoints[epIdx].Plan = append(pathBindings, def.Endpoints[epIdx].Plan...)
	}

	return 0
}

/*
registerTerminal marks terminal node for one or all methods.
*/
func registerTerminal(
	def *engine.ApiDefinition,
	nodeIdx uint32,
	epIdx uint32,
	methodIdx int,
	isAny bool,
	isStrict bool,
) {

	node := &def.SubArena[nodeIdx]

	if isAny {
		for m := range 5 {
			if node.AllowedMethods&(1<<m) != 0 {
				panic("Duplicate route definition")
			}
			node.AllowedMethods |= 1 << m
			node.StrictMethods |= boolToMask(isStrict, m)
			node.EndpointIdx[m] = epIdx
		}
		return
	}

	if node.AllowedMethods&(1<<methodIdx) != 0 {
		panic("Duplicate route definition")
	}

	node.AllowedMethods |= 1 << methodIdx
	node.StrictMethods |= boolToMask(isStrict, methodIdx)
	node.EndpointIdx[methodIdx] = epIdx
}

/*
compileStaticSegment builds or reuses static branch.
*/
func compileStaticSegment(
	def *engine.ApiDefinition,
	parentIdx uint32,
	seg string,
	isLast bool,
	epIdx uint32,
	methodIdx int,
	isAny bool,
	isStrict bool,
) uint32 {

	if seg == "" {
		if isLast {
			registerTerminal(def, parentIdx, epIdx, methodIdx, isAny, isStrict)
		}
		return parentIdx
	}

	char := seg[0]

	existingIdx := def.SubArena[parentIdx].FindChildIdx(char, def.SubArena)
	if existingIdx != 0 {
		if isLast {
			registerTerminal(def, existingIdx, epIdx, methodIdx, isAny, isStrict)
		}
		return existingIdx
	}

	newNode := engine.SubRouteNode{
		PrefixLen: uint16(len(seg)),
	}

	def.SubArena = append(def.SubArena, newNode)
	newIdx := uint32(len(def.SubArena) - 1)

	parent := &def.SubArena[parentIdx]
	if char < 64 {
		parent.MaskLo |= 1 << char
	} else {
		parent.MaskHi |= 1 << (char - 64)
	}
	if parent.ChildIdx == 0 {
		parent.ChildIdx = newIdx
	}
	parent.NumChildren++

	if isLast {
		registerTerminal(def, newIdx, epIdx, methodIdx, isAny, isStrict)
	}

	return newIdx
}

func boolToMask(val bool, idx int) uint8 {
	if val {
		return 1 << idx
	}
	return 0
}
