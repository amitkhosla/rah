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

			if len(a.EndpointConfigs) == 0 {
				// No sub-route config — register root for all methods.
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
			}
		}
	} else {
		finalRouter = oldState.Router
	}

	// 6. Atomic Swap — live traffic sees new state immediately after this line.
	s.FlowManager.SetState(&engine.EngineState{
		Router:      finalRouter,
		Definitions: newDefs,
		FlowLibrary: newLibrary,
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
