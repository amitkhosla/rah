package control

import (
	"encoding/json"
	"log"
	"net/http"
	"rah/internal/engine"
	"rah/internal/router"
	"strings"
	"sync"
)

// ManagementServer coordinates the Control Plane. It translates high-level
// JSON configurations into low-level Instruction Tables and swaps the
// Engine's state atomically.
type ManagementServer struct {
	FlowManager *engine.FlowManager
	Compiler    *Compiler
	Registry    *NameRegistry
	mu          sync.RWMutex
	flowConfigs map[string][]StepConfig
}

// UnifiedSyncHandler is the primary entry point for configuration updates.
// It follows a "Copy-on-Write" pattern to ensure that the Data Plane (engine)
// is never in an inconsistent state during updates.
func (s *ManagementServer) UnifiedSyncHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UnifiedSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[Management] Failed to decode sync request: %v", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if err := s.ApplyUnifiedSync(req); err != nil {
		log.Printf("[Management] sync failed: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// ApplyUnifiedSync applies a sync payload to the active engine state.
func (s *ManagementServer) ApplyUnifiedSync(req UnifiedSyncRequest) error {
	oldState := s.FlowManager.State.Load()

	s.mu.RLock()
	newFlowConfigs := make(map[string][]StepConfig, len(s.flowConfigs))
	for k, v := range s.flowConfigs {
		newFlowConfigs[k] = v
	}
	s.mu.RUnlock()

	// 1. Clone Current State (Library & Definitions)
	// We use maps and slices to prepare the new state while the old state
	// continues to serve traffic on other CPU cores.
	newLibrary := make(map[string][]engine.Instruction)
	for k, v := range oldState.FlowLibrary {
		newLibrary[k] = v
	}

	newDefs := make([]*engine.ApiDefinition, len(oldState.Definitions))
	copy(newDefs, oldState.Definitions)

	// 2. Update Shared Flows (The Instruction Library)
	// We compile Shared Flows first so that APIs can reference them immediately.
	for _, f := range req.Flows {
		if f.Action == "delete" {
			delete(newLibrary, f.Name)
			delete(newFlowConfigs, f.Name)
			log.Printf("[Management] Deleted Flow: %s", f.Name)
		} else {
			newFlowConfigs[f.Name] = f.Instructions
			newLibrary[f.Name] = s.Compiler.Compile(f.Instructions)
			log.Printf("[Management] Compiled Flow: %s (%d instructions)", f.Name, len(newLibrary[f.Name]))
		}
	}

	// 3. Update API Routing & Linking
	// Here we link logical paths to the instructions compiled in step 2.
	routerChanged := false
	for _, a := range req.Apis {
		// Ensure the API has a unique, stable ID across reloads
		id := s.Registry.GetOrAssignId(a.Name)
		s.Compiler.ResetLocalScope()

		if a.Action == "delete" {
			if int(id) < len(newDefs) && newDefs[id] != nil {
				newDefs[id] = nil
				routerChanged = true
			}
		} else {
			// Check if the referenced flow exists in our updated library
			flowCfg, exists := newFlowConfigs[a.FlowName]
			if !exists {
				log.Printf("[Management] Error: API %s references missing flow %s", a.Name, a.FlowName)
				continue
			}

			instructions := s.Compiler.CompileExecutable(flowCfg, newFlowConfigs)

			// Normalize path for Radix Tree lookup (trailing slash optional)
			cleanPath := strings.TrimSuffix(a.Path, "/")

			// Bake the definition: This sets up the metadata for the specific route
			def := engine.BakeDefinition(id, cleanPath)

			// This stage is relative to the API's base path, so root sub-route is "/".
			// 'ANY' implies this flow handles all HTTP methods unless sub-routed.
			s.Compiler.BakeSubRouter(def, "/", "ANY", instructions, true)

			// Grow newDefs if the Registry assigned an ID outside current bounds
			if int(id) >= len(newDefs) {
				expanded := make([]*engine.ApiDefinition, id+1)
				copy(expanded, newDefs)
				newDefs = expanded
			}

			newDefs[id] = def
			routerChanged = true
			log.Printf("[Management] Linked API %s -> Flow %s", cleanPath, a.FlowName)
		}
	}

	// 4. Atomic Router Rebuild
	// If paths were added or removed, we must rebuild the high-performance
	// Radix Tree. If only logic changed, we reuse the old tree.
	var finalRouter *router.RahRouter
	if routerChanged {
		finalRouter = router.New()
		for _, d := range newDefs {
			if d != nil {
				// Add absolute path to the Radix Tree
				finalRouter.Add(d.BaseRawPath, d.Id)
			}
		}
	} else {
		finalRouter = oldState.Router
	}

	// 5. The Atomic Swap
	// This single pointer update switches the entire gateway logic.
	// Zero-allocation, zero-downtime.
	s.FlowManager.SetState(&engine.EngineState{
		Router:      finalRouter,
		Definitions: newDefs,
		FlowLibrary: newLibrary,
	})

	s.mu.Lock()
	s.flowConfigs = newFlowConfigs
	s.mu.Unlock()

	log.Printf("[Management] Sync Complete. RouterChanged=%v", routerChanged)
	return nil
}

// NewManagementServer initializes the server with the required compiler and manager.
func NewManagementServer(fm *engine.FlowManager, c *Compiler, reg *NameRegistry) *ManagementServer {
	return &ManagementServer{
		FlowManager: fm,
		Compiler:    c,
		Registry:    reg,
		flowConfigs: make(map[string][]StepConfig),
	}
}
