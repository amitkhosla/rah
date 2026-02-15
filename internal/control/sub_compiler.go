package control

import (
	"rah/internal/engine"
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
) uint32 {

	segments := strings.Split(strings.Trim(path, "/"), "/")
	if path == "" || path == "/" {
		segments = []string{""}
	}

	methodIdx := engine.MethodStringToIdx(method)
	isAny := method == "ANY"

	def.Endpoints = append(def.Endpoints, engine.Endpoint{
		Id:   uint32(len(def.Endpoints)),
		Plan: plan,
	})
	epIdx := uint32(len(def.Endpoints) - 1)

	if len(def.SubArena) == 0 {
		def.SubArena = append(def.SubArena, engine.SubRouteNode{})
	}

	currIdx := uint32(0)

	for i, seg := range segments {
		isLast := i == len(segments)-1

		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {

			paramName := seg[1 : len(seg)-1]
			slot := c.getSlot("path." + paramName)

			newNode := engine.SubRouteNode{
				PrefixLen: uint16(len(seg)),
			}

			def.SubArena = append(def.SubArena, newNode)
			newIdx := uint32(len(def.SubArena) - 1)

			def.SubArena[currIdx].HasParamChild = true
			def.SubArena[currIdx].ParamChildIdx = newIdx
			def.SubArena[currIdx].ParamSlot = uint8(slot)

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
		for m := 0; m < 5; m++ {
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
