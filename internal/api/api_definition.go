package api

import "strings"

type ParamLocation struct {
	SlotIdx         int
	StaticPrefixLen int
}

type ApiDefinition struct {
	Id             uint32
	RawPath        string
	ParamPositions []ParamLocation
	Plan           any // Cast to *engine.Plan in the engine
}

// BakeDefinition parses the path (e.g., "/v1/user/:id") to find where variables start.
func BakeDefinition(id uint32, rawPath string, plan any) *ApiDefinition {
	def := &ApiDefinition{
		Id:      id,
		RawPath: rawPath,
		Plan:    plan,
	}

	segments := strings.Split(rawPath, "/")
	currentOffset := 0
	slotCounter := 0

	for _, seg := range segments {
		if seg == "" {
			currentOffset += 1 // The leading or trailing slash
			continue
		}

		if strings.HasPrefix(seg, ":") {
			def.ParamPositions = append(def.ParamPositions, ParamLocation{
				SlotIdx:         slotCounter,
				StaticPrefixLen: currentOffset,
			})
			slotCounter++
		}

		// Move the offset forward: length of segment + the slash
		currentOffset += len(seg) + 1
	}

	return def
}
