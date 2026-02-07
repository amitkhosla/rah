package control

import (
	"encoding/json"
	"net/http"
	"rah/internal/engine"
	"rah/internal/router"
	"strings"
)

type ManagementServer struct {
	FlowManager *engine.FlowManager
	Compiler    *Compiler
	Registry    *NameRegistry
}

func (s *ManagementServer) UnifiedSyncHandler(w http.ResponseWriter, r *http.Request) {
	var req UnifiedSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	oldState := s.FlowManager.State.Load()

	// 1. Clone Library & Definitions
	newLibrary := make(map[string][]engine.Instruction)
	for k, v := range oldState.FlowLibrary {
		newLibrary[k] = v
	}
	newDefs := make([]*engine.ApiDefinition, len(oldState.Definitions))
	copy(newDefs, oldState.Definitions)

	// 2. Update Flows
	for _, f := range req.Flows {
		if f.Action == "delete" {
			delete(newLibrary, f.Name)
		} else {
			newLibrary[f.Name] = s.Compiler.Compile(ApiConfig{Flow: f.Instructions})
		}
	}

	// 3. Update APIs
	routerChanged := false
	for _, a := range req.Apis {
		id := s.Registry.GetOrAssignId(a.Name)
		s.Compiler.ResetLocalScope()
		if a.Action == "delete" {
			if newDefs[id] != nil {
				newDefs[id] = nil
				routerChanged = true
			}
		} else {
			if instructions, exists := newLibrary[a.FlowName]; exists {
				// Normalize path: Remove trailing slash to make it optional during lookup
				cleanPath := strings.TrimSuffix(a.Path, "/")

				def := engine.BakeDefinition(id, cleanPath)
				s.Compiler.BakeSubRouter(def, cleanPath, "ANY", instructions, true)

				for i := range def.MethodRoots {
					def.MethodRoots[i] = 0
				}

				newDefs[id] = def
				routerChanged = true
			}
		}
	}

	// 4. Atomic Swap (Only rebuild router if paths changed)
	var finalRouter *router.RahRouter
	if routerChanged {
		finalRouter = router.New()
		for _, d := range newDefs {
			if d != nil {
				finalRouter.Add(d.BaseRawPath, d.Id)
			}
		}
	} else {
		finalRouter = oldState.Router
	}

	s.FlowManager.SetState(&engine.EngineState{
		Router:      finalRouter,
		Definitions: newDefs,
		FlowLibrary: newLibrary,
	})

	w.WriteHeader(200)
}
