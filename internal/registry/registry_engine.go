package registry

import "bytes"

// GetValue retrieves a configuration value for a given tenant alias and key.
// It performs three fast hops: Alias -> tID, Key -> kID, (tID, kID) -> Value.
func GetValue(alias string, key string) ([]byte, bool) {
	reg := State.Active.Load()
	if reg == nil {
		return nil, false
	}

	// 1. Resolve TenantID from Alias (e.g., "pepsi-uuid" -> 5)
	tID, found := walkRadix(reg.Identity, reg.StringPool, alias)
	if !found {
		return nil, false
	}

	// 2. Resolve KeyID from Key Name (e.g., "service-url" -> 10)
	kID, found := walkRadix(reg.Properties, reg.StringPool, key)
	if !found {
		return nil, false
	}

	// 3. Matrix Jump: O(1) coordinate lookup
	// Index = (Row * Width) + Column
	matrixIdx := (tID * reg.Stride) + kID
	if matrixIdx >= uint32(len(reg.Matrix)) {
		return nil, false
	}

	vID := reg.Matrix[matrixIdx]
	if vID == 0 { // 0 is reserved for "No Value"
		return nil, false
	}

	// 4. Return the actual data from the pool
	return reg.ValuePool[vID], true
}

// walkRadix performs a pointer-free, iterative search through the node arena.
func walkRadix(nodes []RegistryNode, pool []byte, input string) (uint32, bool) {
	if len(nodes) == 0 {
		return 0, false
	}

	currIdx := uint32(0) // Start at the root node (always index 0)
	inputBytes := []byte(input)
	inputPos := 0

	for {
		node := nodes[currIdx]
		prefix := pool[node.PrefixOffset : node.PrefixOffset+uint32(node.PrefixLen)]

		// 1. Check if the current node's prefix matches the input at the current position
		if !bytes.HasPrefix(inputBytes[inputPos:], prefix) {
			return 0, false
		}
		inputPos += len(prefix)

		// 2. If we've consumed the whole input, we found our leaf
		if inputPos == len(inputBytes) {
			return node.Value, true
		}

		// 3. Otherwise, search for a child that matches the next byte of input
		nextChar := inputBytes[inputPos]
		foundChild := false

		// Linear scan through children (usually very few branches, so this is fast)
		for i := uint32(0); i < uint32(node.ChildCount); i++ {
			childIdx := node.ChildBase + i
			childNode := nodes[childIdx]

			// Peek at the first character of the child prefix
			if pool[childNode.PrefixOffset] == nextChar {
				currIdx = childIdx
				foundChild = true
				break
			}
		}

		if !foundChild {
			return 0, false
		}
	}
}
