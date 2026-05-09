package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"rah/internal/config"
	"rah/internal/engine"
	"rah/internal/router"
	registrypkg "rah/internal/registry"
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
	RegMgr      *registrypkg.RegistryManager // optional; enables rate limit name resolution at bake time
	mu          sync.RWMutex
	flowConfigs map[string][]StepConfig
	apiConfigs  map[string]ApiUpdate // api name → last upserted config

	// dataStore is optional. When set, every upsert/delete is persisted so the
	// gateway can restore its state on restart. Leave nil (or use SetDataStore)
	// when a central orchestrator owns persistence and RAH only reads on boot.
	dataStore *DataStoreManager

	// LLMProvider, if set, is called before each compile to refresh the
	// compiler's model catalog. Wire this to cfgMgr.LLM so that models
	// registered via the UI are visible to the compiler at sync time.
	LLMProvider func() config.LLMConfig
}

// NewManagementServer initializes the server with the required compiler and manager.
func NewManagementServer(fm *engine.FlowManager, c *Compiler, reg *NameRegistry, regMgr *registrypkg.RegistryManager) *ManagementServer {
	return &ManagementServer{
		FlowManager: fm,
		Compiler:    c,
		Registry:    reg,
		RegMgr:      regMgr,
		flowConfigs: make(map[string][]StepConfig),
		apiConfigs:  make(map[string]ApiUpdate),
	}
}

// Bootstrap reads flows and APIs persisted in dsm and applies them via
// ApplyUnifiedSync. Call this before SetDataStore so the bootstrap reads
// do not trigger redundant writes back to the store.
func (s *ManagementServer) Bootstrap(ctx context.Context, dsm *DataStoreManager) error {
	flowsSnapshot, err := dsm.ReadFlowsSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: read flows: %w", err)
	}
	apisSnapshot, err := dsm.ReadAPIDefinitionsSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: read apis: %w", err)
	}

	if len(flowsSnapshot) == 0 && len(apisSnapshot) == 0 {
		return nil
	}

	req := UnifiedSyncRequest{SyncUUID: "bootstrap"}

	for name, raw := range flowsSnapshot {
		var steps []StepConfig
		if err := json.Unmarshal(raw, &steps); err != nil {
			log.Printf("[Bootstrap] Skipping unparseable flow %q: %v", name, err)
			continue
		}
		req.Flows = append(req.Flows, FlowUpdate{Name: name, Instructions: steps, Action: "upsert"})
	}

	for name, raw := range apisSnapshot {
		var api ApiConfig
		if err := json.Unmarshal(raw, &api); err != nil {
			log.Printf("[Bootstrap] Skipping unparseable api %q: %v", name, err)
			continue
		}
		if api.ApiID == "" {
			api.ApiID = name
		}
		req.Apis = append(req.Apis, ApiUpdate{
			Name:            api.ApiID,
			Path:            api.Path,
			Method:          api.Method,
			FlowName:        api.FlowName,
			RateLimitName:   api.RateLimitName,
			EndpointConfigs: api.EndpointConfigs,
			Async:           api.Async,
			Action:          "upsert",
		})
	}

	if len(req.Flows) == 0 && len(req.Apis) == 0 {
		return nil
	}

	if err := s.ApplyUnifiedSync(req); err != nil {
		return fmt.Errorf("bootstrap: apply sync: %w", err)
	}
	log.Printf("[Bootstrap] Loaded %d flow(s) and %d api(s)", len(req.Flows), len(req.Apis))
	return nil
}

// SetDataStore enables write-through persistence. If the relevant domains
// (DomainFlows, DomainAPIDefinitions) are not bound in the store config,
// persistence calls are silently skipped.
func (s *ManagementServer) SetDataStore(dsm *DataStoreManager) {
	s.dataStore = dsm
}

// UnifiedSyncHandler is the primary entry point for configuration updates.
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

	if r.URL.Query().Get("draft") == "true" {
		if err := s.applyDraftSync(req); err != nil {
			log.Printf("[Management] draft sync failed: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		if err := s.ApplyUnifiedSync(req); err != nil {
			log.Printf("[Management] sync failed: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// applyDraftSync compiles req into DraftState without touching the live State.
// The draft is never routed to — only /test/execute uses it.
// No datastore persistence and no router registration occur.
func (s *ManagementServer) applyDraftSync(req UnifiedSyncRequest) error {
	oldState := s.FlowManager.State.Load()

	s.mu.RLock()
	newFlowConfigs := make(map[string][]StepConfig, len(s.flowConfigs))
	for k, v := range s.flowConfigs {
		newFlowConfigs[k] = v
	}
	newApiConfigs := make(map[string]ApiUpdate, len(s.apiConfigs))
	for k, v := range s.apiConfigs {
		newApiConfigs[k] = v
	}
	s.mu.RUnlock()

	// 1. Clone Current State (Library & Definitions & RouteConstants)
	newLibrary := make(map[string][]engine.Instruction)
	for k, v := range oldState.FlowLibrary {
		newLibrary[k] = v
	}
	newDefs := make([]*engine.ApiDefinition, len(oldState.Definitions))
	copy(newDefs, oldState.Definitions)
	newRouteConstants := make(map[uint64][]engine.ConstantSlot)
	for k, v := range oldState.RouteConstants {
		newRouteConstants[k] = v
	}

	// 2. Update Shared Flows (The Instruction Library)
	var deletedFlows []string
	for _, f := range req.Flows {
		if f.Action == "delete" {
			delete(newLibrary, f.Name)
			delete(newFlowConfigs, f.Name)
			deletedFlows = append(deletedFlows, f.Name)
			log.Printf("[Draft] Deleted Flow: %s", f.Name)
		} else {
			compiled, err := s.Compiler.Compile(f.Instructions)
			if err != nil {
				return fmt.Errorf("flow %q: %w", f.Name, err)
			}
			newFlowConfigs[f.Name] = f.Instructions
			newLibrary[f.Name] = compiled
			s.Compiler.FlowProfiles[f.Name] = buildFlowProfile(f.Name, f.Instructions)
			log.Printf("[Draft] Compiled Flow: %s (%d instructions)", f.Name, len(newLibrary[f.Name]))
		}
	}

	// 3. Update API Routing & Linking
	for _, a := range req.Apis {
		id := s.Registry.GetOrAssignId(a.Name)
		s.Compiler.ResetLocalScope()

		if a.Action == "delete" {
			if int(id) < len(newDefs) && newDefs[id] != nil {
				newDefs[id] = nil
				for ep := uint64(0); ep < 256; ep++ {
					delete(newRouteConstants, uint64(id)<<8|ep)
				}
			}
			delete(newApiConfigs, a.Name)
		} else {
			flowCfg, exists := newFlowConfigs[a.FlowName]
			if !exists {
				log.Printf("[Draft] Error: API %s references missing flow %s", a.Name, a.FlowName)
				continue
			}

			instructions, err := s.Compiler.CompileExecutable(flowCfg, newFlowConfigs)
			if err != nil {
				log.Printf("[Draft] Error compiling API %s: %v", a.Name, err)
				continue
			}
			defaultSlotSnap, defaultNextSlot := s.Compiler.SnapshotSlots()

			cleanPath := strings.TrimSuffix(a.Path, "/")
			def := engine.BakeDefinition(id, cleanPath)
			if len(a.AliasPaths) > 0 {
				cleaned := make([]string, 0, len(a.AliasPaths))
				for _, p := range a.AliasPaths {
					cleaned = append(cleaned, strings.TrimSuffix(p, "/"))
				}
				def.AliasPaths = cleaned
			}

			apiRLId := uint16(0)
			if a.RateLimitName != "" && s.RegMgr != nil {
				if rlid, ok := s.RegMgr.GetRateLimitConfigId(a.RateLimitName); ok {
					apiRLId = rlid
				}
			}
			asyncMode := engine.AsyncDisabled
			switch a.Async {
			case "allowed":
				asyncMode = engine.AsyncAllowed
			case "forced":
				asyncMode = engine.AsyncForced
			}

			// Clear stale constant slots for this API before registering new ones.
			for ep := uint64(0); ep < 256; ep++ {
				delete(newRouteConstants, uint64(id)<<8|ep)
			}

			if len(a.EndpointConfigs) == 0 {
				epID := uint8(len(def.Endpoints))
				if len(a.Constants) > 0 {
					if constSlots, cerr := s.Compiler.AllocConstantSlots(a.Constants); cerr == nil && len(constSlots) > 0 {
						newRouteConstants[uint64(id)<<8|uint64(epID)] = constSlots
					}
				}
				s.Compiler.BakeSubRouter(def, "/", "ANY", instructions, true, apiRLId, 0, asyncMode)
			} else {
				for _, ec := range a.EndpointConfigs {
					epRLId := uint16(0)
					if ec.RateLimitName != "" && s.RegMgr != nil {
						if rlid, ok := s.RegMgr.GetRateLimitConfigId(ec.RateLimitName); ok {
							epRLId = rlid
						}
					}
					method := ec.Method
					if method == "" {
						method = "ANY"
					}
					epPath := ec.Path
					if epPath == "" {
						epPath = "/"
					}
					isStrict := len(epPath) > 1 && !strings.HasSuffix(epPath, "/")
					epID := uint8(len(def.Endpoints))
					s.Compiler.RestoreSlots(defaultSlotSnap, defaultNextSlot)
					mergedConsts := mergeConstants(a.Constants, ec.Constants)
					if len(mergedConsts) > 0 {
						if constSlots, cerr := s.Compiler.AllocConstantSlots(mergedConsts); cerr == nil && len(constSlots) > 0 {
							newRouteConstants[uint64(id)<<8|uint64(epID)] = constSlots
						}
					}
					s.Compiler.BakeSubRouter(def, epPath, method, instructions, isStrict, apiRLId, epRLId, asyncMode)
				}
			}

			if int(id) >= len(newDefs) {
				expanded := make([]*engine.ApiDefinition, id+1)
				copy(expanded, newDefs)
				newDefs = expanded
			}
			newDefs[id] = def
			newApiConfigs[a.Name] = a
			log.Printf("[Draft] Linked API %s -> Flow %s", cleanPath, a.FlowName)
		}
	}

	// 4. Flow reference safety check
	for _, flowName := range deletedFlows {
		for apiName, apiCfg := range newApiConfigs {
			if apiCfg.FlowName == flowName {
				return fmt.Errorf("cannot delete flow %q: still referenced by API %q — delete the API first", flowName, apiName)
			}
		}
	}

	// 5. Build a draft router (internal use only — never registered with the live router).
	draftRouter := router.New()
	for _, d := range newDefs {
		if d != nil {
			draftRouter.Add(d.BaseRawPath, d.Id)
		}
	}

	// 6. Store in DraftState — does NOT touch FlowManager.State.
	s.FlowManager.DraftState.Store(&engine.EngineState{
		Router:         draftRouter,
		Definitions:    newDefs,
		FlowLibrary:    newLibrary,
		RouteConstants: newRouteConstants,
	})

	log.Printf("[Draft] Sync Complete. Stored in DraftState (not live).")
	return nil
}

// DraftStatusHandler handles GET /sync/draft/status.
// Returns whether a draft EngineState is currently loaded.
func (s *ManagementServer) DraftStatusHandler(w http.ResponseWriter, r *http.Request) {
	draft := s.FlowManager.DraftState.Load()
	w.Header().Set("Content-Type", "application/json")
	if draft == nil {
		json.NewEncoder(w).Encode(map[string]any{"draft_loaded": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"draft_loaded": true,
		"flow_count":   len(draft.FlowLibrary),
		"api_count":    len(draft.Definitions),
	})
}

// ApplyUnifiedSync applies a sync payload to the active engine state.
func (s *ManagementServer) ApplyUnifiedSync(req UnifiedSyncRequest) error {
	// Refresh the compiler's LLM catalog so models registered via the UI
	// (stored in Postgres, not gateway.yaml) are visible during bake.
	if s.LLMProvider != nil {
		s.Compiler.LLMCfg = s.LLMProvider()
	}

	oldState := s.FlowManager.State.Load()

	s.mu.RLock()
	newFlowConfigs := make(map[string][]StepConfig, len(s.flowConfigs))
	for k, v := range s.flowConfigs {
		newFlowConfigs[k] = v
	}
	newApiConfigs := make(map[string]ApiUpdate, len(s.apiConfigs))
	for k, v := range s.apiConfigs {
		newApiConfigs[k] = v
	}
	s.mu.RUnlock()

	// 1. Clone Current State (Library & Definitions)
	newLibrary := make(map[string][]engine.Instruction)
	for k, v := range oldState.FlowLibrary {
		newLibrary[k] = v
	}
	newDefs := make([]*engine.ApiDefinition, len(oldState.Definitions))
	copy(newDefs, oldState.Definitions)
	newRouteConstants := make(map[uint64][]engine.ConstantSlot)
	for k, v := range oldState.RouteConstants {
		newRouteConstants[k] = v
	}

	// Track changes for persistence after the atomic swap.
	type persistOp struct {
		kind    string // "flow_upsert", "flow_delete", "api_upsert", "api_delete"
		name    string
		payload []byte // nil for deletes
	}
	var pendingPersist []persistOp

	// 2. Update Shared Flows (The Instruction Library)
	var deletedFlows []string
	for _, f := range req.Flows {
		if f.Action == "delete" {
			delete(newLibrary, f.Name)
			delete(newFlowConfigs, f.Name)
			delete(s.Compiler.FlowProfiles, f.Name)
			deletedFlows = append(deletedFlows, f.Name)
			log.Printf("[Management] Deleted Flow: %s", f.Name)
			pendingPersist = append(pendingPersist, persistOp{kind: "flow_delete", name: f.Name})
		} else {
			compiled, err := s.Compiler.Compile(f.Instructions)
			if err != nil {
				return fmt.Errorf("flow %q: %w", f.Name, err)
			}
			newFlowConfigs[f.Name] = f.Instructions
			newLibrary[f.Name] = compiled
			s.Compiler.FlowProfiles[f.Name] = buildFlowProfile(f.Name, f.Instructions)
			log.Printf("[Management] Compiled Flow: %s (%d instructions)", f.Name, len(newLibrary[f.Name]))
			if data, err := json.Marshal(f.Instructions); err == nil {
				pendingPersist = append(pendingPersist, persistOp{kind: "flow_upsert", name: f.Name, payload: data})
			}
		}
	}

	// 3. Update API Routing & Linking
	routerChanged := false
	for _, a := range req.Apis {
		id := s.Registry.GetOrAssignId(a.Name)
		s.Compiler.ResetLocalScope()

		if a.Action == "delete" {
			if int(id) < len(newDefs) && newDefs[id] != nil {
				newDefs[id] = nil
				routerChanged = true
				// Clear all constant slots for this API (up to 256 endpoints).
				for ep := uint64(0); ep < 256; ep++ {
					delete(newRouteConstants, uint64(id)<<8|ep)
				}
			}
			delete(newApiConfigs, a.Name)
			pendingPersist = append(pendingPersist, persistOp{kind: "api_delete", name: a.Name})
		} else {
			flowCfg, exists := newFlowConfigs[a.FlowName]
			if !exists {
				log.Printf("[Management] Error: API %s references missing flow %s", a.Name, a.FlowName)
				continue
			}

			instructions, err := s.Compiler.CompileExecutable(flowCfg, newFlowConfigs)
			if err != nil {
				log.Printf("[Management] Error compiling API %s: %v", a.Name, err)
				continue
			}

			// Snapshot the default flow's slot assignments so we can restore them
			// when allocating constants for endpoints that share the default flow.
			defaultSlotSnap, defaultNextSlot := s.Compiler.SnapshotSlots()

			cleanPath := strings.TrimSuffix(a.Path, "/")
			def := engine.BakeDefinition(id, cleanPath)

			apiRLId := uint16(0)
			if a.RateLimitName != "" && s.RegMgr != nil {
				if rlid, ok := s.RegMgr.GetRateLimitConfigId(a.RateLimitName); ok {
					apiRLId = rlid
				}
			}
			// Parse async mode from the api update config
			asyncMode := engine.AsyncDisabled
			switch a.Async {
			case "allowed":
				asyncMode = engine.AsyncAllowed
			case "forced":
				asyncMode = engine.AsyncForced
			}

			// Clear stale constant slots for this API before registering new ones.
			for ep := uint64(0); ep < 256; ep++ {
				delete(newRouteConstants, uint64(id)<<8|ep)
			}

			if len(a.EndpointConfigs) == 0 {
				// No sub-route config — register root for all methods.
				epID := uint8(len(def.Endpoints)) // = 0 before BakeSubRouter
				mergedConsts := a.Constants
				if len(mergedConsts) > 0 {
					if constSlots, cerr := s.Compiler.AllocConstantSlots(mergedConsts); cerr == nil && len(constSlots) > 0 {
						newRouteConstants[uint64(id)<<8|uint64(epID)] = constSlots
					}
				}
				s.Compiler.BakeSubRouter(def, "/", "ANY", instructions, true, apiRLId, 0, asyncMode)
			} else {
				for _, ec := range a.EndpointConfigs {
					epRLId := uint16(0)
					if ec.RateLimitName != "" && s.RegMgr != nil {
						if rlid, ok := s.RegMgr.GetRateLimitConfigId(ec.RateLimitName); ok {
							epRLId = rlid
						}
					}
					method := ec.Method
					if method == "" {
						method = "ANY"
					}
					epPath := ec.Path
					if epPath == "" {
						epPath = "/"
					}
					isStrict := len(epPath) > 1 && !strings.HasSuffix(epPath, "/")

					// Capture endpointID before BakeSubRouter appends the new endpoint.
					epID := uint8(len(def.Endpoints))

					// Resolve endpoint-level flow override.
					epInstructions := instructions
					overrideCompiled := false
					if ec.FlowName != "" && ec.FlowName != a.FlowName {
						if epFlowCfg, ok := newFlowConfigs[ec.FlowName]; ok {
							if compiled, cerr := s.Compiler.CompileExecutable(epFlowCfg, newFlowConfigs); cerr == nil {
								epInstructions = compiled
								overrideCompiled = true
							} else {
								log.Printf("[Management] Error compiling endpoint flow %s for API %s: %v", ec.FlowName, a.Name, cerr)
							}
						} else {
							log.Printf("[Management] Warning: endpoint flow %s not found for API %s, using API default", ec.FlowName, a.Name)
						}
					}

					// Constant slot allocation must use the slot map from this endpoint's flow.
					// If no override was compiled, restore the default flow's slot state so
					// constant keys resolve to the correct indices for the default flow.
					if !overrideCompiled {
						s.Compiler.RestoreSlots(defaultSlotSnap, defaultNextSlot)
					}
					mergedConsts := mergeConstants(a.Constants, ec.Constants)
					if len(mergedConsts) > 0 {
						if constSlots, cerr := s.Compiler.AllocConstantSlots(mergedConsts); cerr == nil && len(constSlots) > 0 {
							newRouteConstants[uint64(id)<<8|uint64(epID)] = constSlots
						}
					}

					s.Compiler.BakeSubRouter(def, epPath, method, epInstructions, isStrict, apiRLId, epRLId, asyncMode)
				}
			}

			if int(id) >= len(newDefs) {
				expanded := make([]*engine.ApiDefinition, id+1)
				copy(expanded, newDefs)
				newDefs = expanded
			}
			newDefs[id] = def
			newApiConfigs[a.Name] = a
			routerChanged = true
			log.Printf("[Management] Linked API %s -> Flow %s", cleanPath, a.FlowName)

			apiCfg := ApiConfig{
				ApiID:           a.Name,
				Path:            a.Path,
				Method:          a.Method,
				FlowName:        a.FlowName,
				RateLimitName:   a.RateLimitName,
				EndpointConfigs: a.EndpointConfigs,
				Async:           a.Async,
				AliasPaths:      a.AliasPaths,
			}
			if data, err := json.Marshal(apiCfg); err == nil {
				pendingPersist = append(pendingPersist, persistOp{kind: "api_upsert", name: a.Name, payload: data})
			}
		}
	}

	// 4. Flow reference safety: a flow may only be deleted if no remaining API uses it.
	// We check against newApiConfigs (which already reflects API deletions in this request),
	// so deleting both an API and its flow in a single sync payload is allowed.
	for _, flowName := range deletedFlows {
		for apiName, apiCfg := range newApiConfigs {
			if apiCfg.FlowName == flowName {
				return fmt.Errorf("cannot delete flow %q: still referenced by API %q — delete the API first", flowName, apiName)
			}
		}
	}

	// 5. Atomic Router Rebuild
	var finalRouter *router.RahRouter
	if routerChanged {
		finalRouter = router.New()
		for _, d := range newDefs {
			if d != nil {
				finalRouter.Add(d.BaseRawPath, d.Id)
				for _, alias := range d.AliasPaths {
					finalRouter.Add(alias, d.Id)
				}
			}
		}
	} else {
		finalRouter = oldState.Router
	}

	// 6. Atomic Swap — live traffic sees new state immediately after this line.
	s.FlowManager.SetState(&engine.EngineState{
		Router:         finalRouter,
		Definitions:    newDefs,
		FlowLibrary:    newLibrary,
		RouteConstants: newRouteConstants,
	})

	s.mu.Lock()
	s.flowConfigs = newFlowConfigs
	s.apiConfigs = newApiConfigs
	s.mu.Unlock()

	// 7. Persist changes to datastore (optional, best-effort).
	// Runs after the atomic swap so routing is never blocked by I/O.
	// Errors are logged but do not roll back the in-memory state — the
	// central orchestrator is the source of truth if a datastore is shared.
	if s.dataStore != nil && len(pendingPersist) > 0 {
		ctx := context.Background()
		for _, op := range pendingPersist {
			var err error
			switch op.kind {
			case "flow_upsert":
				if s.dataStore.IsConfigured(config.DomainFlows) {
					err = s.dataStore.PutGlobal(ctx, config.DomainFlows, op.name, op.payload)
				}
			case "flow_delete":
				if s.dataStore.IsConfigured(config.DomainFlows) {
					err = s.dataStore.DeleteGlobal(ctx, config.DomainFlows, op.name)
				}
			case "api_upsert":
				if s.dataStore.IsConfigured(config.DomainAPIDefinitions) {
					err = s.dataStore.PutGlobal(ctx, config.DomainAPIDefinitions, op.name, op.payload)
				}
			case "api_delete":
				if s.dataStore.IsConfigured(config.DomainAPIDefinitions) {
					err = s.dataStore.DeleteGlobal(ctx, config.DomainAPIDefinitions, op.name)
				}
			}
			if err != nil {
				log.Printf("[Management] Failed to persist %s %q: %v", op.kind, op.name, err)
			}
		}
	}

	log.Printf("[Management] Sync Complete. RouterChanged=%v", routerChanged)
	return nil
}

// StepsMetaHandler handles GET /meta/steps.
// Returns the full step catalog — the single source of truth for every action
// mergeConstants returns a new map with apiConsts as the base, overridden by epConsts.
// Returns nil when both inputs are empty.
func mergeConstants(apiConsts, epConsts map[string]string) map[string]string {
	if len(apiConsts) == 0 && len(epConsts) == 0 {
		return nil
	}
	merged := make(map[string]string, len(apiConsts)+len(epConsts))
	for k, v := range apiConsts {
		merged[k] = v
	}
	for k, v := range epConsts {
		merged[k] = v
	}
	return merged
}

// the compiler supports. Studio fetches this at load time to build its palette
// dynamically; no UI code changes are needed when new steps are added.
func (s *ManagementServer) StepsMetaHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(BuildStepCatalog())
}

// GetAllApisHandler returns all registered flows and APIs in the same shape
// as the UnifiedSyncRequest used to create them.
func (s *ManagementServer) GetAllApisHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	flows := make([]FlowUpdate, 0, len(s.flowConfigs))
	for name, steps := range s.flowConfigs {
		flows = append(flows, FlowUpdate{Name: name, Instructions: steps, Action: "upsert"})
	}
	apis := make([]ApiUpdate, 0, len(s.apiConfigs))
	for _, a := range s.apiConfigs {
		apis = append(apis, a)
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(UnifiedSyncRequest{Flows: flows, Apis: apis})
}

// FlowProfileHandler handles GET /flows/{name}/profile and DELETE /flows/{name}.
//
//   - GET  /flows/{name}/profile → returns the FlowProfile for the compiled flow
//   - DELETE /flows/{name}       → removes an orphaned flow (one with no API pointing to it)
func (s *ManagementServer) FlowProfileHandler(w http.ResponseWriter, r *http.Request) {
	// Extract flow name from path: /flows/{name}[/profile]
	path := strings.TrimPrefix(r.URL.Path, "/flows/")
	isProfile := strings.HasSuffix(path, "/profile")
	if isProfile {
		path = strings.TrimSuffix(path, "/profile")
	}
	name := strings.TrimSpace(path)
	if name == "" {
		http.Error(w, "missing flow name", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		s.deleteFlowHandler(w, name)
	case http.MethodGet:
		profile, ok := s.Compiler.GetFlowProfile(name)
		if !ok {
			http.Error(w, fmt.Sprintf("flow %q not found", name), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(profile)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// deleteFlowHandler removes an orphaned flow (DELETE /flows/{name}).
// Refuses if any registered API references this flow as its flow_name.
func (s *ManagementServer) deleteFlowHandler(w http.ResponseWriter, name string) {
	s.mu.RLock()
	_, exists := s.flowConfigs[name]
	var inUseBy string
	for apiName, api := range s.apiConfigs {
		if api.FlowName == name {
			inUseBy = apiName
			break
		}
	}
	s.mu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("flow %q not found", name), http.StatusNotFound)
		return
	}
	if inUseBy != "" {
		http.Error(w, fmt.Sprintf("flow %q is in use by API %q — remove the API first", name, inUseBy), http.StatusConflict)
		return
	}

	// Build a sync request with action "delete" for the flow so ApplyUnifiedSync
	// removes it from the live runtime.
	req := UnifiedSyncRequest{
		Flows: []FlowUpdate{{Name: name, Action: "delete"}},
	}
	if err := s.ApplyUnifiedSync(req); err != nil {
		http.Error(w, "runtime sync failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"deleted":%q}`, name)
}
