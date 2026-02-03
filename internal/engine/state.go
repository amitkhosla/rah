package engine

import (
	"rah/internal/api"
	"rah/internal/router"
)

// EngineState represents a single, immutable version of the gateway logic.
type EngineState struct {
	Router      *router.RahRouter
	Definitions []*api.ApiDefinition // Now using the slice you already have
}
