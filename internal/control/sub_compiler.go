package control

import (
	"rah/internal/engine"
	"strings"
)

func (c *Compiler) BakeSubRouter(def *engine.ApiDefinition, path string, method string, plan []engine.Instruction, isStrict bool) uint32 {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if path == "" || path == "/" {
		segments = []string{""}
	}

	// Register the endpoint in the API Definition
	def.Endpoints = append(def.Endpoints, engine.Endpoint{Plan: plan})
	epIdx := uint32(len(def.Endpoints) - 1)

	// Every SubArena starts with a root node at index 0
	if len(def.SubArena) == 0 {
		def.SubArena = append(def.SubArena, engine.SubRouteNode{})
	}

	currIdx := uint32(0)

	for i, seg := range segments {
		isLast := i == len(segments)-1

		// Identify Parameter: e.g., "{project}"
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			paramName := seg[1 : len(seg)-1]
			slot := c.getSlot(paramName) // Dynamic slot allocation (No more hardcoding)

			newNode := engine.SubRouteNode{
				PrefixLen:   uint16(len(seg)),
				IsTerminal:  isLast,
				IsStrict:    isStrict,
				EndpointIdx: epIdx,
			}

			def.SubArena = append(def.SubArena, newNode)
			newIdx := uint32(len(def.SubArena) - 1)

			// Set the Fallback Marker on the current node
			def.SubArena[currIdx].HasParamChild = true
			def.SubArena[currIdx].ParamChildIdx = newIdx
			def.SubArena[currIdx].ParamSlot = uint8(slot)

			currIdx = newIdx
		} else {
			// Static segment
			currIdx = compileStaticSegment(def, currIdx, seg, isLast, epIdx, isStrict)
		}
	}

	return 0 // The root is always 0 for the SubArena
}

// compileStaticSegment builds a static branch in the SubArena.
func compileStaticSegment(def *engine.ApiDefinition, parentIdx uint32, seg string, isLast bool, epIdx uint32, isStrict bool) uint32 {
	if seg == "" {
		// Handle trailing slash or empty segment by marking parent as terminal
		def.SubArena[parentIdx].IsTerminal = true
		def.SubArena[parentIdx].IsStrict = isStrict
		def.SubArena[parentIdx].EndpointIdx = epIdx
		return parentIdx
	}

	char := seg[0]

	// 1. Check if child already exists
	existingIdx := def.SubArena[parentIdx].FindChildIdx(char, def.SubArena)
	if existingIdx != 0 {
		// If it exists, we just move to it (or update if it's the terminal segment)
		if isLast {
			def.SubArena[existingIdx].IsTerminal = true
			def.SubArena[existingIdx].IsStrict = isStrict
			def.SubArena[existingIdx].EndpointIdx = epIdx
		}
		return existingIdx
	}

	// 2. Create New Node
	newNode := engine.SubRouteNode{
		PrefixLen:   uint16(len(seg)),
		IsTerminal:  isLast,
		IsStrict:    isStrict,
		EndpointIdx: epIdx,
	}

	def.SubArena = append(def.SubArena, newNode)
	newIdx := uint32(len(def.SubArena) - 1)

	// 3. Update Parent's Bitmask
	parent := &def.SubArena[parentIdx]
	isHi := uint64(char >> 6)
	bit := uint64(1) << (char & 63)

	if isHi == 0 {
		parent.MaskLo |= bit
	} else {
		parent.MaskHi |= bit
	}

	// Note: In a true succinct Radix tree, childIdx points to the START of a block.
	// Since we are appending one by one during Bake, we are simplifying the
	// indexing for the SubArena to keep it manageable.
	if parent.ChildIdx == 0 {
		parent.ChildIdx = newIdx
	}
	parent.NumChildren++

	return newIdx
}
