package control

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/datasource"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/engine/steps"
	"github.com/amitkhosla/rah/internal/gatewaylog"
	"github.com/amitkhosla/rah/internal/mcpreg"
	"github.com/amitkhosla/rah/internal/observability"
	"github.com/amitkhosla/rah/internal/redissource"
	registrypkg "github.com/amitkhosla/rah/internal/registry"
	"github.com/amitkhosla/rah/internal/router"
	"github.com/amitkhosla/rah/internal/scheduler"
	"github.com/amitkhosla/rah/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	apiConfigs  map[string]ApiUpdate // api name â†’ last upserted config

	// dataStore is optional. When set, every upsert/delete is persisted so the
	// gateway can restore its state on restart. Leave nil (or use SetDataStore)
	// when a central orchestrator owns persistence and RAH only reads on boot.
	dataStore *DataStoreManager

	// cfgMgr enables syncing of LLM models and MCP servers via the bundle pipeline.
	cfgMgr *config.Manager

	// mcpReg enables syncing of virtual MCP servers and API tools via the bundle pipeline.
	mcpReg *mcpreg.Registry

	// LLMProvider, if set, is called before each compile to refresh the
	// compiler's model catalog. Wire this to cfgMgr.LLM so that models
	// registered via the UI are visible to the compiler at sync time.
	LLMProvider func() config.LLMConfig

	// VarSchemaHook, if set, is called after each API is compiled.
	// It receives the API name, version hash, and all variable schema rows
	// exported from the compiler's slotMap. Wire this to obsWriter.UpsertVarSchema.
	VarSchemaHook func(apiName string, apiHash uint64, rows []observability.VarSchemaRow)

	// InstrSchemaHook, if set, is called after each API endpoint is baked.
	// It receives the api name, endpoint ID, version hash, and all instruction
	// schema rows derived from the compiled plan. Wire this to obsWriter.UpsertInstrSchema.
	InstrSchemaHook func(apiName string, endpointID uint8, apiHash uint64, rows []observability.InstrSchemaRow)

	// Schedule management callbacks - wired by the scheduler package (wave 3D).
	OnScheduleUpsert  func(s *scheduler.Schedule) error
	OnScheduleDelete  func(name string) error
	OnScheduleList    func() ([]*scheduler.Schedule, error)
	OnScheduleHistory func(name string) ([]scheduler.ExecutionRecord, error)

	// OnFlowRun is called to execute a named flow directly (MCP tool: run_flow).
	OnFlowRun func(flowName, tenantAlias string, constants map[string]string) error

	// DataSourcePool enables access to schema-isolated data sources for migrations.
	DataSourcePool    *datasource.DataSourcePool
	DataSourceConfigs []datasource.DataSourceConfig

	// RedisSourcePool enables access to customer Redis sources for redis_* steps.
	RedisSourcePool *redissource.RedisSourcePool

	WorkflowHandler *WorkflowHandler // optional; nil if workflows domain not configured

	StorageMgr *storage.StorageManager

	configVersion atomic.Uint32 // incremented on every live config apply; readable via ConfigVersion()
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

// ConfigVersion returns the current config version counter.
// Incremented on every ApplyUnifiedSync; set from DB version when InstanceSync is active.
func (s *ManagementServer) ConfigVersion() uint32 {
	return s.configVersion.Load()
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

	req := UnifiedSyncRequest{SyncUUID: "bootstrap"}

	for name, raw := range flowsSnapshot {
		var steps []StepConfig
		if err := json.Unmarshal(raw, &steps); err != nil {
			gatewaylog.Default.Warn("[Bootstrap] skipping unparseable flow", gatewaylog.F("name", name), gatewaylog.F("error", err.Error()))
			continue
		}
		req.Flows = append(req.Flows, FlowUpdate{Name: name, Instructions: steps, Action: "upsert"})
	}

	for name, raw := range apisSnapshot {
		var api ApiConfig
		if err := json.Unmarshal(raw, &api); err != nil {
			gatewaylog.Default.Warn("[Bootstrap] skipping unparseable api", gatewaylog.F("name", name), gatewaylog.F("error", err.Error()))
			continue
		}
		if api.ApiID == "" {
			api.ApiID = name
		}
		req.Apis = append(req.Apis, ApiUpdate{
			Name:              api.ApiID,
			Path:              api.Path,
			Method:            api.Method,
			FlowName:          api.FlowName,
			RateLimitName:     api.RateLimitName,
			EndpointConfigs:   api.EndpointConfigs,
			Async:             api.Async,
			RateLimitPolicies: api.RateLimitPolicies,
			SkipRateLimit:     api.SkipRateLimit,
			Action:            "upsert",
		})
	}

	// Read V2 rate limit configs.
	if rlv2Snapshot, serr := dsm.ReadRateLimitConfigsV2Snapshot(ctx); serr == nil {
		for name, raw := range rlv2Snapshot {
			var cfg registrypkg.RateLimitConfigV2
			if json.Unmarshal(raw, &cfg) == nil {
				if cfg.Name == "" {
					cfg.Name = name
				}
				req.RateLimitConfigsV2 = append(req.RateLimitConfigsV2, cfg)
			} else {
				gatewaylog.Default.Warn("[Bootstrap] skipping unparseable rate_limit_config_v2", gatewaylog.F("name", name))
			}
		}
	}

	// Read tier definitions.
	if tierSnapshot, serr := dsm.ReadTiersSnapshot(ctx); serr == nil {
		for name, raw := range tierSnapshot {
			var t registrypkg.TierDef
			if json.Unmarshal(raw, &t) == nil {
				if t.Name == "" {
					t.Name = name
				}
				req.Tiers = append(req.Tiers, t)
			} else {
				gatewaylog.Default.Warn("[Bootstrap] skipping unparseable tier", gatewaylog.F("name", name))
			}
		}
	}

	// Read upstream service definitions.
	if svcSnapshot, serr := dsm.ReadUpstreamServicesSnapshot(ctx); serr == nil {
		for name, raw := range svcSnapshot {
			var svc registrypkg.UpstreamServiceDef
			if json.Unmarshal(raw, &svc) == nil {
				if svc.Name == "" {
					svc.Name = name
				}
				req.UpstreamServices = append(req.UpstreamServices, svc)
			} else {
				gatewaylog.Default.Warn("[Bootstrap] skipping unparseable upstream_service", gatewaylog.F("name", name))
			}
		}
	}

	if len(req.Flows) == 0 && len(req.Apis) == 0 &&
		len(req.RateLimitConfigsV2) == 0 && len(req.Tiers) == 0 && len(req.UpstreamServices) == 0 {
		return nil
	}

	if err := s.ApplyUnifiedSync(req); err != nil {
		return fmt.Errorf("bootstrap: apply sync: %w", err)
	}
	gatewaylog.Default.Info("[Bootstrap] loaded",
		gatewaylog.Fint("flows", int64(len(req.Flows))),
		gatewaylog.Fint("apis", int64(len(req.Apis))),
		gatewaylog.Fint("rl_v2", int64(len(req.RateLimitConfigsV2))),
		gatewaylog.Fint("tiers", int64(len(req.Tiers))),
		gatewaylog.Fint("upstream_svcs", int64(len(req.UpstreamServices))))

	// Sync per-tenant rate-limit multipliers into the engine's fixed array.
	if s.RegMgr != nil {
		reg := registrypkg.State.Active.Load()
		registrypkg.BakeMultipliers(reg, s.RegMgr, engine.SetTenantMultiplier)
		log.Printf("[Bootstrap] BakeMultipliers complete")
	}

	return nil
}

// SetDataStore enables write-through persistence. If the relevant domains
// (DomainFlows, DomainAPIDefinitions) are not bound in the store config,
// persistence calls are silently skipped.
func (s *ManagementServer) SetDataStore(dsm *DataStoreManager) {
	s.dataStore = dsm
}

// SetCfgMgr wires the config manager so LLM models and MCP servers in sync
// bundles are applied to the live config and persisted.
func (s *ManagementServer) SetCfgMgr(cfgMgr *config.Manager) {
	s.cfgMgr = cfgMgr
}

// SetMCPReg wires the MCP registry so virtual MCP servers and API tools in
// sync bundles are applied to the live registry and persisted.
func (s *ManagementServer) SetMCPReg(reg *mcpreg.Registry) {
	s.mcpReg = reg
}

// UnifiedSyncHandler is the primary entry point for configuration updates.
func (s *ManagementServer) UnifiedSyncHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UnifiedSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		gatewaylog.Default.Warn("[Management] failed to decode sync request", gatewaylog.F("error", err.Error()))
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if r.URL.Query().Get("draft") == "true" {
		if err := s.applyDraftSync(req); err != nil {
			gatewaylog.Default.Warn("[Management] draft sync failed", gatewaylog.F("error", err.Error()))
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
		return
	}

	if err := s.ApplyUnifiedSync(req); err != nil {
		gatewaylog.Default.Warn("[Management] sync failed", gatewaylog.F("error", err.Error()))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Run the RL validation pass after a successful sync so the Studio can
	// display per-row warnings without blocking the deploy.
	warnings := s.buildSyncWarnings(req)
	if len(warnings) > 0 {
		log.Printf("[Management] sync completed with %d rate limit warning(s)", len(warnings))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":              "success",
		"rate_limit_warnings": warnings,
	})
}

// buildSyncWarnings runs the rate limit validation pass over every API upsert
// in req and returns aggregated advisory warnings. It reads the current
// flowConfigs (post-sync) under the read lock so it is safe to call immediately
// after ApplyUnifiedSync completes.
func (s *ManagementServer) buildSyncWarnings(req UnifiedSyncRequest) []RateLimitWarning {
	s.mu.RLock()
	flowCfgs := make(map[string][]StepConfig, len(s.flowConfigs))
	for k, v := range s.flowConfigs {
		flowCfgs[k] = v
	}
	s.mu.RUnlock()

	var all []RateLimitWarning
	for _, a := range req.Apis {
		if a.Action == "delete" {
			continue
		}
		warns := validateRLPolicies(
			a.Name,
			a.FlowName,
			a.RateLimitPolicies,
			a.SkipRateLimit,
			flowCfgs,
			s.Compiler.RegMgr,
		)
		all = append(all, warns...)
	}
	return all
}

// applyDraftSync compiles req into DraftState without touching the live State.
// The draft is never routed to â€” only /test/execute uses it.
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
	newRouteUpstreamUrls := make(map[uint64]*engine.UpstreamUrlInfo)
	for k, v := range oldState.RouteUpstreamUrls {
		newRouteUpstreamUrls[k] = v
	}

	// Pre-pass: expand DSL code â†’ Instructions
	{
		var dslExtras []FlowUpdate
		for i := range req.Flows {
			f := &req.Flows[i]
			if f.Code != "" && f.Action != "delete" {
				result, err := ParseDSL(f.Code)
				if err != nil {
					return fmt.Errorf("flow %q DSL: %w", f.Name, err)
				}
				f.Instructions = result.Steps
				for anonName, anonSteps := range result.ExtraFlows {
					dslExtras = append(dslExtras, FlowUpdate{Name: anonName, Instructions: anonSteps, Action: "upsert"})
				}
			}
		}
		if len(dslExtras) > 0 {
			req.Flows = append(dslExtras, req.Flows...)
		}
	}

	// 2. Update Shared Flows (The Instruction Library)
	// Populate the Compiler's QueryLibrary with named queries from the sync request.
	s.Compiler.QueryLibrary = req.Queries
	// Wire RedisSourcePool into compiler for redis_* step compilation.
	s.Compiler.RedisSourcePool = s.RedisSourcePool

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
					delete(newRouteUpstreamUrls, uint64(id)<<8|ep)
				}
			}
			delete(newApiConfigs, a.Name)
		} else {
			flowCfg, exists := newFlowConfigs[a.FlowName]
			if !exists {
				log.Printf("[Draft] Error: API %s references missing flow %s", a.Name, a.FlowName)
				continue
			}

			// Resolve and register multi-entry RL policies at API level (must happen
			// before CompileExecutable so the compiler can find anonymous fixed configs).
			if len(a.RateLimitPolicies) > 0 {
				a.RateLimitPolicies = resolveAndRegisterRLPolicies(a.Name, a.RateLimitPolicies)
			}

			// Wire API-level RL policies into the compiler for marker + auto-inject.
			s.Compiler.currentAPIPolicies = a.RateLimitPolicies
			s.Compiler.currentAPISkipRL = a.SkipRateLimit

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

			// Clear stale constant slots and upstream URLs for this API before registering new ones.
			for ep := uint64(0); ep < 256; ep++ {
				delete(newRouteConstants, uint64(id)<<8|ep)
				delete(newRouteUpstreamUrls, uint64(id)<<8|ep)
			}

			if len(a.EndpointConfigs) == 0 {
				epID := uint8(len(def.Endpoints))
				routeKey := uint64(id)<<8 | uint64(epID)
				if len(a.Constants) > 0 {
					if constSlots, cerr := s.Compiler.AllocConstantSlots(a.Constants); cerr == nil && len(constSlots) > 0 {
						newRouteConstants[routeKey] = constSlots
					}
				}
				if a.UpstreamUrl != nil {
					if slotIdx, serr := s.Compiler.AllocUpstreamUrlSlot(); serr == nil {
						newRouteUpstreamUrls[routeKey] = buildUpstreamUrlInfo(a.UpstreamUrl, slotIdx, s.RegMgr)
					}
				}
				s.Compiler.BakeSubRouter(def, "/", "ANY", instructions, true, apiRLId, 0, asyncMode)
			} else {
				for ecIdx, ec := range a.EndpointConfigs {
					// Resolve and register multi-entry RL policies at endpoint level.
					if len(ec.RateLimitPolicies) > 0 {
						ec.RateLimitPolicies = resolveAndRegisterRLPolicies(
							fmt.Sprintf("%s_ep%d", a.Name, ecIdx), ec.RateLimitPolicies)
						a.EndpointConfigs[ecIdx].RateLimitPolicies = ec.RateLimitPolicies
					}

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
					routeKey := uint64(id)<<8 | uint64(epID)
					s.Compiler.RestoreSlots(defaultSlotSnap, defaultNextSlot)
					mergedConsts := mergeConstants(a.Constants, ec.Constants)
					if len(mergedConsts) > 0 {
						if constSlots, cerr := s.Compiler.AllocConstantSlots(mergedConsts); cerr == nil && len(constSlots) > 0 {
							newRouteConstants[routeKey] = constSlots
						}
					}
					effectiveUpstream := a.UpstreamUrl
					if ec.UpstreamUrl != nil {
						effectiveUpstream = ec.UpstreamUrl
					}
					if effectiveUpstream != nil {
						if slotIdx, serr := s.Compiler.AllocUpstreamUrlSlot(); serr == nil {
							newRouteUpstreamUrls[routeKey] = buildUpstreamUrlInfo(effectiveUpstream, slotIdx, s.RegMgr)
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
				return fmt.Errorf("cannot delete flow %s: still referenced by API %s - delete the API first", flowName, apiName)
			}
		}
	}

	// 5. Build a draft router (internal use only â€” never registered with the live router).
	draftRouter := router.New()
	for _, d := range newDefs {
		if d != nil {
			draftRouter.Add(d.BaseRawPath, d.Id)
		}
	}

	// 6. Store in DraftState â€” does NOT touch FlowManager.State.
	s.FlowManager.DraftState.Store(&engine.EngineState{
		Router:            draftRouter,
		Definitions:       newDefs,
		FlowLibrary:       newLibrary,
		RouteConstants:    newRouteConstants,
		RouteUpstreamUrls: newRouteUpstreamUrls,
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
		_ = json.NewEncoder(w).Encode(map[string]any{"draft_loaded": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
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

	// Process LLM models before flows so aliases resolve at compile time.
	if len(req.LLMModels) > 0 && s.cfgMgr != nil {
		for _, m := range req.LLMModels {
			s.cfgMgr.UpsertLLMModel(m)
		}
		if s.dataStore != nil {
			persistAIModels(s.cfgMgr, s.dataStore)
		}
	}

	// Process external MCP servers before flows.
	if len(req.MCPServers) > 0 && s.cfgMgr != nil {
		for _, srv := range req.MCPServers {
			s.cfgMgr.UpsertMCPServer(srv)
		}
		if s.dataStore != nil {
			persistAIMCPServers(s.cfgMgr, s.dataStore)
		}
	}

	// Process virtual MCP servers.
	if len(req.VirtualMCPServers) > 0 && s.mcpReg != nil {
		for _, def := range req.VirtualMCPServers {
			s.mcpReg.UpsertServer(def)
		}
		if s.dataStore != nil {
			persistMCPTools(s.mcpReg, s.dataStore)
		}
	}

	// Process API tools.
	if len(req.APITools) > 0 && s.mcpReg != nil {
		for _, tool := range req.APITools {
			s.mcpReg.UpsertAPITool(tool)
		}
		if s.dataStore != nil {
			persistMCPTools(s.mcpReg, s.dataStore)
		}
	}

	// Process event listeners before flows.
	if len(req.EventListeners) > 0 && s.cfgMgr != nil {
		for _, el := range req.EventListeners {
			// EventListeners are matched by Name for updates/deletes
			if el.Name == "" {
				continue
			}
			// Upsert the event listener into the live config
			s.cfgMgr.UpsertEventListener(el)
		}
	}

	// Apply V2 rate limit configs â€” store config + assign stable integer ID +
	// register counter arenas so the compiled CheckRateLimitV2 step can count.
	for _, cfg := range req.RateLimitConfigsV2 {
		// Auto-populate PeriodSecs from the human-readable Period string when absent.
		for i := range cfg.Windows {
			if cfg.Windows[i].PeriodSecs == 0 && cfg.Windows[i].Period != "" {
				if secs, err := steps.ParseWindowDuration(cfg.Windows[i].Period); err == nil {
					cfg.Windows[i].PeriodSecs = secs
				}
			}
		}
		registrypkg.UpsertRateLimitConfigV2(cfg)
		id := s.RegMgr.EnsureRateLimitV2ID(cfg.Name)
		numWindows := len(cfg.Windows)
		if numWindows == 0 {
			numWindows = 1
		}
		reg := engine.ActiveCounterRegistry()
		reg.RegisterSlotConfig(id, numWindows, 65536)  // IP, slot, static, global, composite
		reg.RegisterTenantConfig(id, numWindows, 1024) // tenant-based counting
	}
	// Apply tier definitions.
	for _, t := range req.Tiers {
		registrypkg.UpsertTier(t)
	}
	// Apply upstream service definitions.
	for _, svc := range req.UpstreamServices {
		registrypkg.UpsertUpstreamService(svc)
	}

	// Register tenants declared in this sync bundle.
	// Processed before flows/APIs so that any flow compiled in this batch that
	// calls set_service_url / set_identifier / set_meta can immediately resolve
	// the tenant IDs for the aliases declared here.
	for _, td := range req.Tenants {
		if len(td.Aliases) == 0 {
			continue
		}
		var urls, ids, meta map[string]string
		if m := td.Properties["urls"]; len(m) > 0 {
			urls = m
		}
		if m := td.Properties["ids"]; len(m) > 0 {
			ids = m
		}
		if m := td.Properties["meta"]; len(m) > 0 {
			meta = m
		}
		switch td.Action {
		case "delete":
			s.RegMgr.DeleteTenant(td.Aliases[0])
		default: // "upsert" or empty
			s.RegMgr.UpsertTenantState(td.Aliases, urls, ids, meta)
		}
	}

	oldState := s.FlowManager.State.Load()

	// Capture the version that will be live after this sync completes.
	// configVersion.Add(1) fires at the end of this function; predict it here
	// so all endpoints compiled in this batch share the same VersionID.
	thisVersion := s.configVersion.Load() + 1

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
	newRouteUpstreamUrls := make(map[uint64]*engine.UpstreamUrlInfo)
	for k, v := range oldState.RouteUpstreamUrls {
		newRouteUpstreamUrls[k] = v
	}

	// Track changes for persistence after the atomic swap.
	type persistOp struct {
		kind    string // "flow_upsert", "flow_delete", "api_upsert", "api_delete", "rlv2_upsert", "tier_upsert", "upstreamsvc_upsert"
		name    string
		payload []byte // nil for deletes
	}
	var pendingPersist []persistOp

	// Queue V2 rate limit configs for persistence.
	for _, cfg := range req.RateLimitConfigsV2 {
		if data, merr := json.Marshal(cfg); merr == nil {
			pendingPersist = append(pendingPersist, persistOp{kind: "rlv2_upsert", name: cfg.Name, payload: data})
		}
	}
	// Queue tier definitions for persistence.
	for _, t := range req.Tiers {
		if data, merr := json.Marshal(t); merr == nil {
			pendingPersist = append(pendingPersist, persistOp{kind: "tier_upsert", name: t.Name, payload: data})
		}
	}
	// Queue upstream service definitions for persistence.
	for _, svc := range req.UpstreamServices {
		if data, merr := json.Marshal(svc); merr == nil {
			pendingPersist = append(pendingPersist, persistOp{kind: "upstreamsvc_upsert", name: svc.Name, payload: data})
		}
	}

	// Pre-pass: expand DSL code â†’ Instructions
	{
		var dslExtras []FlowUpdate
		for i := range req.Flows {
			f := &req.Flows[i]
			if f.Code != "" && f.Action != "delete" {
				result, err := ParseDSL(f.Code)
				if err != nil {
					return fmt.Errorf("flow %q DSL: %w", f.Name, err)
				}
				f.Instructions = result.Steps
				for anonName, anonSteps := range result.ExtraFlows {
					dslExtras = append(dslExtras, FlowUpdate{Name: anonName, Instructions: anonSteps, Action: "upsert"})
				}
			}
		}
		if len(dslExtras) > 0 {
			req.Flows = append(dslExtras, req.Flows...)
		}
	}

	// 2. Update Shared Flows (The Instruction Library)
	// Populate the Compiler's QueryLibrary with named queries from the sync request.
	s.Compiler.QueryLibrary = req.Queries
	// Wire RedisSourcePool into compiler for redis_* step compilation.
	s.Compiler.RedisSourcePool = s.RedisSourcePool

	var deletedFlows []string
	var compileErrors []string
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
				compileErrors = append(compileErrors, fmt.Sprintf("flow %q: %s", f.Name, err.Error()))
				continue
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
	if len(compileErrors) > 0 {
		return fmt.Errorf("compile errors:\n%s", strings.Join(compileErrors, "\n"))
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
				// Clear all constant slots and upstream URLs for this API (up to 256 endpoints).
				for ep := uint64(0); ep < 256; ep++ {
					delete(newRouteConstants, uint64(id)<<8|ep)
					delete(newRouteUpstreamUrls, uint64(id)<<8|ep)
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

			// Resolve and register multi-entry RL policies at API level (must happen
			// before CompileExecutable so the compiler can find anonymous fixed configs).
			if len(a.RateLimitPolicies) > 0 {
				a.RateLimitPolicies = resolveAndRegisterRLPolicies(a.Name, a.RateLimitPolicies)
				log.Printf("[Management] API %s: resolved %d RL policy entry/entries", a.Name, len(a.RateLimitPolicies))
			}

			// Wire API-level RL policies into the compiler so CompileExecutable can
			// handle "api_rate_limits" marker steps and perform auto-injection.
			s.Compiler.currentAPIPolicies = a.RateLimitPolicies
			s.Compiler.currentAPISkipRL = a.SkipRateLimit

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

			// Clear stale constant slots and upstream URLs for this API before registering new ones.
			for ep := uint64(0); ep < 256; ep++ {
				delete(newRouteConstants, uint64(id)<<8|ep)
				delete(newRouteUpstreamUrls, uint64(id)<<8|ep)
			}

			if len(a.EndpointConfigs) == 0 {
				// No sub-route config â€” register root for all methods.
				epID := uint8(len(def.Endpoints)) // = 0 before BakeSubRouter
				routeKey := uint64(id)<<8 | uint64(epID)
				mergedConsts := a.Constants
				if len(mergedConsts) > 0 {
					if constSlots, cerr := s.Compiler.AllocConstantSlots(mergedConsts); cerr == nil && len(constSlots) > 0 {
						newRouteConstants[routeKey] = constSlots
					}
				}
				// Bake gateway-native upstream URL (API-level config, no endpoint override).
				if a.UpstreamUrl != nil {
					if slotIdx, serr := s.Compiler.AllocUpstreamUrlSlot(); serr == nil {
						newRouteUpstreamUrls[routeKey] = buildUpstreamUrlInfo(a.UpstreamUrl, slotIdx, s.RegMgr)
					} else {
						log.Printf("[Management] Warning: cannot allocate upstream_url slot for API %s: %v", a.Name, serr)
					}
				}
				s.Compiler.BakeSubRouter(def, "/", "ANY", instructions, true, apiRLId, 0, asyncMode)
				bakeEndpointSchema(def)
				if s.InstrSchemaHook != nil {
					ep := &def.Endpoints[len(def.Endpoints)-1]
					rows := buildObsInstrSchema(a.Name, ep.EndpointId, uint64(thisVersion), ep.InstrSchema)
					s.InstrSchemaHook(a.Name, ep.EndpointId, uint64(thisVersion), rows)
				}
			} else {
				for ecIdx, ec := range a.EndpointConfigs {
					// Resolve and register multi-entry RL policies at endpoint level.
					if len(ec.RateLimitPolicies) > 0 {
						ec.RateLimitPolicies = resolveAndRegisterRLPolicies(
							fmt.Sprintf("%s_ep%d", a.Name, ecIdx), ec.RateLimitPolicies)
						a.EndpointConfigs[ecIdx].RateLimitPolicies = ec.RateLimitPolicies
					}

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
					routeKey := uint64(id)<<8 | uint64(epID)

					// Resolve endpoint-level flow override.
					epInstructions := instructions
					overrideCompiled := false
					if ec.FlowName != "" && ec.FlowName != a.FlowName {
						if epFlowCfg, ok := newFlowConfigs[ec.FlowName]; ok {
							// Endpoint-level RL policies take precedence for the override flow.
							if len(ec.RateLimitPolicies) > 0 || ec.SkipRateLimit {
								s.Compiler.currentAPIPolicies = ec.RateLimitPolicies
								s.Compiler.currentAPISkipRL = ec.SkipRateLimit
							}
							if compiled, cerr := s.Compiler.CompileExecutable(epFlowCfg, newFlowConfigs); cerr == nil {
								epInstructions = compiled
								overrideCompiled = true
							} else {
								log.Printf("[Management] Error compiling endpoint flow %s for API %s: %v", ec.FlowName, a.Name, cerr)
							}
							// Restore API-level policies for subsequent endpoints.
							s.Compiler.currentAPIPolicies = a.RateLimitPolicies
							s.Compiler.currentAPISkipRL = a.SkipRateLimit
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
							newRouteConstants[routeKey] = constSlots
						}
					}

					// Bake gateway-native upstream URL.
					// Endpoint-level config takes priority over API-level config.
					effectiveUpstream := a.UpstreamUrl
					if ec.UpstreamUrl != nil {
						effectiveUpstream = ec.UpstreamUrl
					}
					if effectiveUpstream != nil {
						if slotIdx, serr := s.Compiler.AllocUpstreamUrlSlot(); serr == nil {
							newRouteUpstreamUrls[routeKey] = buildUpstreamUrlInfo(effectiveUpstream, slotIdx, s.RegMgr)
						} else {
							log.Printf("[Management] Warning: cannot allocate upstream_url slot for API %s endpoint %s: %v", a.Name, ec.Path, serr)
						}
					}

					s.Compiler.BakeSubRouter(def, epPath, method, epInstructions, isStrict, apiRLId, epRLId, asyncMode)
					bakeEndpointSchema(def)
					if s.InstrSchemaHook != nil {
						ep := &def.Endpoints[len(def.Endpoints)-1]
						rows := buildObsInstrSchema(a.Name, ep.EndpointId, uint64(thisVersion), ep.InstrSchema)
						s.InstrSchemaHook(a.Name, ep.EndpointId, uint64(thisVersion), rows)
					}
				}
			}

			if int(id) >= len(newDefs) {
				expanded := make([]*engine.ApiDefinition, id+1)
				copy(expanded, newDefs)
				newDefs = expanded
			}
			def.VersionID = thisVersion
			newDefs[id] = def
			newApiConfigs[a.Name] = a
			routerChanged = true
			log.Printf("[Management] Linked API %s -> Flow %s", cleanPath, a.FlowName)

			// Export variable schema and fire the hook (e.g. persisting to obs store).
			if s.VarSchemaHook != nil {
				varRows := s.Compiler.ExportVarSchema(a.Name, uint64(thisVersion))
				if len(varRows) > 0 {
					s.VarSchemaHook(a.Name, uint64(thisVersion), varRows)
				}
			}

			apiCfg := ApiConfig{
				ApiID:             a.Name,
				Path:              a.Path,
				Method:            a.Method,
				FlowName:          a.FlowName,
				AppName:           a.AppName,
				RateLimitName:     a.RateLimitName,
				EndpointConfigs:   a.EndpointConfigs,
				Async:             a.Async,
				AliasPaths:        a.AliasPaths,
				RateLimitPolicies: a.RateLimitPolicies,
				SkipRateLimit:     a.SkipRateLimit,
				UpstreamDefaults:  a.UpstreamDefaults,
				WebSocket:         a.WebSocket,
			}
			if data, err := json.Marshal(apiCfg); err == nil {
				pendingPersist = append(pendingPersist, persistOp{kind: "api_upsert", name: a.Name, payload: data})
			}
		}
	}

	for idx := range req.Schedules {
		sc := req.Schedules[idx]
		if len(sc.Action) > 0 {
			if sc.Action[0] == byte('u') {
				if s.OnScheduleUpsert != nil {
					sched := &scheduler.Schedule{
						Name:        sc.Name,
						Cron:        sc.Cron,
						FlowName:    sc.FlowName,
						TenantAlias: sc.TenantAlias,
						Enabled:     sc.Enabled,
						TimeoutSec:  sc.TimeoutSec,
						Constants:   sc.Constants,
					}
					if err := s.OnScheduleUpsert(sched); err != nil {
						gatewaylog.Default.Warn("[Management] schedule upsert failed",
							gatewaylog.F("name", sc.Name), gatewaylog.F("error", err.Error()))
					}
				}
			} else if sc.Action[0] == byte('d') {
				if s.OnScheduleDelete != nil {
					if err := s.OnScheduleDelete(sc.Name); err != nil {
						gatewaylog.Default.Warn("[Management] schedule delete failed",
							gatewaylog.F("name", sc.Name), gatewaylog.F("error", err.Error()))
					}
				}
			}
		}
	}

	// 5. Flow reference safety: a flow may only be deleted if no remaining API uses it.
	// We check against newApiConfigs (which already reflects API deletions in this request),
	// so deleting both an API and its flow in a single sync payload is allowed.
	for _, flowName := range deletedFlows {
		for apiName, apiCfg := range newApiConfigs {
			if apiCfg.FlowName == flowName {
				return fmt.Errorf(`cannot delete flow %s: still referenced by API %s - delete the API first`, flowName, apiName)
			}
		}
	}

	// 6. Atomic Router Rebuild
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

	// 7. Atomic Swap â€” live traffic sees new state immediately after this line.
	s.FlowManager.SetState(&engine.EngineState{
		Router:            finalRouter,
		Definitions:       newDefs,
		FlowLibrary:       newLibrary,
		RouteConstants:    newRouteConstants,
		RouteUpstreamUrls: newRouteUpstreamUrls,
	})

	s.mu.Lock()
	s.flowConfigs = newFlowConfigs
	s.apiConfigs = newApiConfigs
	s.mu.Unlock()

	// 8. Persist changes to datastore (optional, best-effort).
	// Runs after the atomic swap so routing is never blocked by I/O.
	// Errors are logged but do not roll back the in-memory state â€” the
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
			case "rlv2_upsert":
				if s.dataStore.IsConfigured(config.DomainRateLimitConfigsV2) {
					err = s.dataStore.PutGlobal(ctx, config.DomainRateLimitConfigsV2, op.name, op.payload)
				}
			case "tier_upsert":
				if s.dataStore.IsConfigured(config.DomainTiers) {
					err = s.dataStore.PutGlobal(ctx, config.DomainTiers, op.name, op.payload)
				}
			case "upstreamsvc_upsert":
				if s.dataStore.IsConfigured(config.DomainUpstreamServices) {
					err = s.dataStore.PutGlobal(ctx, config.DomainUpstreamServices, op.name, op.payload)
				}
			}
			if err != nil {
				gatewaylog.Default.Warn("[Management] failed to persist",
					gatewaylog.F("kind", op.kind),
					gatewaylog.F("name", op.name),
					gatewaylog.F("error", err.Error()))
			}
		}
	}

	// 9. Apply migrations to data sources after state is live.
	if err := s.applyMigrations(context.Background(), req.Migrations); err != nil {
		gatewaylog.Default.Warn("[Management] migration processing failed",
			gatewaylog.F("error", err.Error()))
	}

	log.Printf("[Management] Sync Complete. RouterChanged=%v", routerChanged)
	s.configVersion.Add(1)
	return nil
}

// applyMigrations applies any pending migrations from the sync bundle to all
// schema-isolated data sources for each tenant alias extracted from requests.
func (s *ManagementServer) applyMigrations(ctx context.Context, migrations []MigrationDef) error {
	if len(migrations) == 0 {
		return nil
	}
	if s.DataSourcePool == nil {
		return nil
	}

	// Validate: no duplicate versions, ascending order enforced.
	seen := make(map[int]bool, len(migrations))
	for _, m := range migrations {
		if seen[m.Version] {
			return fmt.Errorf("migration: duplicate version %d", m.Version)
		}
		seen[m.Version] = true
	}
	// Sort by version ascending.
	sorted := make([]MigrationDef, len(migrations))
	copy(sorted, migrations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Version < sorted[j].Version })

	// Apply to each data source that has schema isolation.
	for _, cfg := range s.DataSourceConfigs {
		if cfg.TenantIsolation != "schema" {
			continue
		}
		pool, ok := s.DataSourcePool.Get(cfg.Name)
		if !ok {
			continue
		}
		// For now, apply migrations to public schema (not tenant-specific).
		// In the future, may need to iterate over known tenant aliases.
		if err := runMigrationsOnPool(ctx, pool, "", sorted, cfg.TenantKey); err != nil {
			return fmt.Errorf("datasource %q: %w", cfg.Name, err)
		}
	}
	return nil
}

// runMigrationsOnPool applies pending migrations to a single connection pool.
// tenantAlias is optional; if empty, applies to public schema only.
func runMigrationsOnPool(ctx context.Context, pool *pgxpool.Pool, tenantAlias string, migrations []MigrationDef, tenantKey string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	var schemaName string
	if tenantAlias != "" {
		schemaName = "tenant_" + tenantAlias
	} else {
		schemaName = "public"
	}

	// Ensure schema exists (for tenant-specific runs).
	if tenantAlias != "" {
		if _, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schemaName); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
	}

	// SET search_path for this session if in a tenant schema.
	if tenantAlias != "" {
		if _, err := conn.Exec(ctx, "SET search_path = "+schemaName+",public"); err != nil {
			return fmt.Errorf("set search_path: %w", err)
		}
	}

	// Ensure migrations tracking table.
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS rah_migrations (
		version    INT PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("create rah_migrations: %w", err)
	}

	// Load already-applied versions.
	rows, err := conn.Query(ctx, "SELECT version FROM rah_migrations")
	if err != nil {
		return err
	}
	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err == nil {
			applied[v] = true
		}
	}
	rows.Close()

	// Apply pending migrations in version order.
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for v%d: %w", m.Version, err)
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration v%d %q: %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO rah_migrations (version, name) VALUES ($1, $2)", m.Version, m.Name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration v%d: %w", m.Version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration v%d: %w", m.Version, err)
		}
	}
	return nil
}

// StepsMetaHandler handles GET /meta/steps.
// Returns the full step catalog â€” the single source of truth for every action
// buildUpstreamUrlInfo converts an UpstreamUrlConfig into a baked UpstreamUrlInfo.
// slotIdx is the resolved ByteSlot index for the "upstream_url" slot.
// regMgr is optional; when nil, registry source is left with KeyID=0 (resolved at runtime).
func buildUpstreamUrlInfo(cfg *UpstreamUrlConfig, slotIdx int, regMgr *registrypkg.RegistryManager) *engine.UpstreamUrlInfo {
	if cfg == nil {
		return nil
	}
	info := &engine.UpstreamUrlInfo{
		SlotIdx: slotIdx,
		Value:   []byte(cfg.Value),
	}
	switch cfg.Source {
	case "static":
		info.Source = engine.UpstreamUrlSourceStatic
	case "registry":
		info.Source = engine.UpstreamUrlSourceRegistry
		if regMgr != nil {
			info.RegistryKeyID = regMgr.EnsureURLKeyID(cfg.Value)
		}
	case "cache":
		info.Source = engine.UpstreamUrlSourceCache
	case "header":
		info.Source = engine.UpstreamUrlSourceHeader
	case "queryparam":
		info.Source = engine.UpstreamUrlSourceQueryParam
	default:
		// Unknown source â€” treat as static to avoid silent no-ops.
		info.Source = engine.UpstreamUrlSourceStatic
	}
	return info
}

// resolveAndRegisterRLPolicies processes an APIRateLimitEntry slice at bake time:
//   - named:   validates the referenced RateLimitConfigV2 exists (logs warning if absent).
//   - fixed:   builds and registers an anonymous RateLimitConfigV2 with a deterministic
//              name "__fixed__{scopeID}_{rowIdx}" so the compiler can find it via name.
//              Sets entry.Config to the anonymous name on success.
//   - dynamic: validates each mapping target config exists (logs warnings for missing ones).
//
// Returns the (possibly mutated) slice. The input is not modified in place.
func resolveAndRegisterRLPolicies(scopeID string, policies []APIRateLimitEntry) []APIRateLimitEntry {
	if len(policies) == 0 {
		return policies
	}
	result := make([]APIRateLimitEntry, len(policies))
	copy(result, policies)
	for i := range result {
		entry := &result[i]
		switch entry.Kind {
		case RLEntryNamed:
			if entry.Config == "" {
				log.Printf("[Management] RL policy %s row %d (named): empty config name â€” skipped", scopeID, i)
				continue
			}
			if registrypkg.GetRateLimitConfigV2(entry.Config) == nil {
				log.Printf("[Management] RL policy %s row %d (named): config %q not found â€” will produce warning at compile time", scopeID, i, entry.Config)
			}
		case RLEntryFixed:
			if len(entry.Windows) == 0 {
				log.Printf("[Management] RL policy %s row %d (fixed): no windows defined â€” skipped", scopeID, i)
				continue
			}
			anonName := fmt.Sprintf("__fixed__%s_%d", scopeID, i)
			windows := make([]registrypkg.RateLimitWindow, 0, len(entry.Windows))
			for _, w := range entry.Windows {
				windows = append(windows, registrypkg.RateLimitWindow{
					PeriodSecs: w.EpochSec,
					Limit:      w.Limit,
				})
			}
			registrypkg.UpsertRateLimitConfigV2(registrypkg.RateLimitConfigV2{
				Name:        anonName,
				Enforcement: "approximate",
				Windows:     windows,
			})
			entry.Config = anonName
			log.Printf("[Management] RL policy %s row %d (fixed): registered anonymous config %q (%d window(s))", scopeID, i, anonName, len(windows))
		case RLEntryDynamic:
			if entry.Dynamic == nil || len(entry.Dynamic.Mappings) == 0 {
				log.Printf("[Management] RL policy %s row %d (dynamic): empty mapping â€” skipped", scopeID, i)
				continue
			}
			for runtimeVal, cfgName := range entry.Dynamic.Mappings {
				if registrypkg.GetRateLimitConfigV2(cfgName) == nil {
					log.Printf("[Management] RL policy %s row %d (dynamic): mapping[%q]=%q config not found", scopeID, i, runtimeVal, cfgName)
				}
			}
		}
	}
	return result
}

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

// bakeEndpointSchema sets InstrSchema and Counters on the most recently appended
// endpoint of def. Must be called immediately after BakeSubRouter so the Plan
// (including any prepended BindPath instructions) is final.
func bakeEndpointSchema(def *engine.ApiDefinition) {
	ep := &def.Endpoints[len(def.Endpoints)-1]
	ep.InstrSchema = buildInstrSchema(ep.Plan)
	ep.Counters = make([]engine.InstrCounter, len(ep.Plan))
}

// buildObsInstrSchema converts compiled InstrSchema into observability rows for persistence.
func buildObsInstrSchema(apiName string, endpointID uint8, apiHash uint64, schema []engine.InstrMeta) []observability.InstrSchemaRow {
	rows := make([]observability.InstrSchemaRow, len(schema))
	for i, meta := range schema {
		rows[i] = observability.InstrSchemaRow{
			ApiName:    apiName,
			ApiHash:    apiHash,
			EndpointID: endpointID,
			PC:         int16(i),
			StepType:   meta.StepType,
			StepName:   meta.Name,
		}
	}
	return rows
}

// buildInstrSchema derives a read-only []InstrMeta from a compiled plan.
// The result is parallel to plan: schema[i] describes plan[i].
func buildInstrSchema(plan []engine.Instruction) []engine.InstrMeta {
	schema := make([]engine.InstrMeta, len(plan))
	for i, instr := range plan {
		schema[i] = engine.InstrMeta{
			Name:     instr.Name,
			StepType: instrStepType(instr.Name),
		}
	}
	return schema
}

// instrStepType maps an instruction name to a broad step-type category.
// Used by the schema endpoint so collectors can label metrics without
// knowing every concrete instruction name.
func instrStepType(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "http_call"):
		return "http"
	case strings.HasPrefix(lower, "cache_"):
		return "cache"
	case strings.HasPrefix(lower, "bind_"):
		return "binding"
	case strings.HasPrefix(lower, "validate_token"), strings.HasPrefix(lower, "token_"):
		return "token_validation"
	case strings.HasPrefix(lower, "rate_limit"), strings.HasPrefix(lower, "fixed_window"), strings.HasPrefix(lower, "token_bucket"):
		return "rate_limit"
	case strings.HasPrefix(lower, "log"):
		return "log"
	case strings.HasPrefix(lower, "response"):
		return "response"
	case strings.HasPrefix(lower, "validate"):
		return "validation"
	case strings.HasPrefix(lower, "cors"):
		return "cors"
	case strings.HasPrefix(lower, "set_"), strings.HasPrefix(lower, "load_"),
		strings.HasPrefix(lower, "copy_"), strings.HasPrefix(lower, "remove_"),
		strings.HasPrefix(lower, "rename_"), strings.HasPrefix(lower, "to_"),
		strings.HasPrefix(lower, "concat"), strings.HasPrefix(lower, "trim"),
		strings.HasPrefix(lower, "contains"), strings.HasPrefix(lower, "replace"),
		strings.HasPrefix(lower, "split"), strings.HasPrefix(lower, "join"):
		return "transform"
	case strings.HasPrefix(lower, "extract_"), strings.HasPrefix(lower, "cookie"):
		return "cookie"
	case strings.HasPrefix(lower, "switch"), strings.HasPrefix(lower, "retry"),
		strings.HasPrefix(lower, "jump"), strings.HasPrefix(lower, "branch"),
		strings.HasPrefix(lower, "batch"):
		return "flow_control"
	default:
		return "system"
	}
}

// the compiler supports. Studio fetches this at load time to build its palette
// dynamically; no UI code changes are needed when new steps are added.
func (s *ManagementServer) StepsMetaHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(BuildStepCatalog())
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
	_ = json.NewEncoder(w).Encode(UnifiedSyncRequest{Flows: flows, Apis: apis})
}

// FlowProfileHandler handles GET /flows/{name}/profile and DELETE /flows/{name}.
//
//   - GET  /flows/{name}/profile â†’ returns the FlowProfile for the compiled flow
//   - DELETE /flows/{name}       â†’ removes an orphaned flow (one with no API pointing to it)
func (s *ManagementServer) FlowProfileHandler(w http.ResponseWriter, r *http.Request) {
	// Extract flow name from path: /flows/{name}[/profile|/run]
	path := strings.TrimPrefix(r.URL.Path, "/flows/")
	isProfile := strings.HasSuffix(path, "/profile")
	isRun := strings.HasSuffix(path, "/run")
	if isProfile {
		path = strings.TrimSuffix(path, "/profile")
	} else if isRun {
		path = strings.TrimSuffix(path, "/run")
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
		if !isProfile {
			http.Error(w, "GET /flows/{name} requires /profile suffix", http.StatusBadRequest)
			return
		}
		profile, ok := s.Compiler.GetFlowProfile(name)
		if !ok {
			http.Error(w, fmt.Sprintf("flow %q not found", name), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(profile)
	case http.MethodPost:
		if !isRun {
			http.Error(w, "POST /flows/{name} requires /run suffix", http.StatusBadRequest)
			return
		}
		s.flowRunDirectHandler(w, r, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// flowRunDirectHandler executes a flow directly and is called from FlowProfileHandler.
func (s *ManagementServer) flowRunDirectHandler(w http.ResponseWriter, r *http.Request, flowName string) {
	if s.OnFlowRun == nil {
		http.Error(w, "flow execution not available", http.StatusNotImplemented)
		return
	}

	var payload struct {
		TenantAlias string            `json:"tenant_alias,omitempty"`
		Constants   map[string]string `json:"constants,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.OnFlowRun(flowName, payload.TenantAlias, payload.Constants); err != nil {
		gatewaylog.Default.Warn("[Management] failed to run flow",
			gatewaylog.F("flow", flowName),
			gatewaylog.F("error", err.Error()))
		http.Error(w, "failed to run flow: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "flow": flowName})
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
		http.Error(w, fmt.Sprintf("flow %q is in use by API %q â€” remove the API first", name, inUseBy), http.StatusConflict)
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
	if _, err := fmt.Fprintf(w, `{"deleted":%q}`, name); err != nil {
		gatewaylog.Default.Error("management: failed to write delete response",
			gatewaylog.F("err", err.Error()),
			gatewaylog.F("name", name),
		)
	}
}

// ScheduleRuntimeHandler handles POST /schedules/runtime (create/update runtime schedule)
// and DELETE /schedules/runtime/{name} (delete runtime schedule).
func (s *ManagementServer) ScheduleRuntimeHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/schedules/runtime")
	path = strings.TrimSpace(path)

	switch r.Method {
	case http.MethodPost:
		s.createRuntimeSchedule(w, r)
	case http.MethodDelete:
		if path == "" || path == "/" {
			http.Error(w, "schedule name required", http.StatusBadRequest)
			return
		}
		name := strings.TrimPrefix(path, "/")
		s.deleteRuntimeSchedule(w, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// createRuntimeSchedule handles POST /schedules/runtime.
func (s *ManagementServer) createRuntimeSchedule(w http.ResponseWriter, r *http.Request) {
	var cfg ScheduleConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if cfg.Name == "" {
		http.Error(w, "schedule name required", http.StatusBadRequest)
		return
	}
	if cfg.Cron == "" {
		http.Error(w, "cron expression required", http.StatusBadRequest)
		return
	}
	if cfg.FlowName == "" {
		http.Error(w, "flow_name required", http.StatusBadRequest)
		return
	}

	// Create the schedule via the OnScheduleUpsert callback.
	if s.OnScheduleUpsert == nil {
		http.Error(w, "schedule management not available", http.StatusServiceUnavailable)
		return
	}

	sched := &scheduler.Schedule{
		Name:        cfg.Name,
		Cron:        cfg.Cron,
		FlowName:    cfg.FlowName,
		TenantAlias: cfg.TenantAlias,
		Enabled:     cfg.Enabled,
		TimeoutSec:  cfg.TimeoutSec,
		Constants:   cfg.Constants,
	}
	if err := s.OnScheduleUpsert(sched); err != nil {
		gatewaylog.Default.Warn("[Management] failed to create runtime schedule",
			gatewaylog.F("name", cfg.Name),
			gatewaylog.F("error", err.Error()))
		http.Error(w, "failed to create schedule: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "created", "name": cfg.Name})
}

// deleteRuntimeSchedule handles DELETE /schedules/runtime/{name}.
func (s *ManagementServer) deleteRuntimeSchedule(w http.ResponseWriter, name string) {
	if s.OnScheduleDelete == nil {
		http.Error(w, "schedule management not available", http.StatusServiceUnavailable)
		return
	}

	if err := s.OnScheduleDelete(name); err != nil {
		gatewaylog.Default.Warn("[Management] failed to delete runtime schedule",
			gatewaylog.F("name", name),
			gatewaylog.F("error", err.Error()))
		http.Error(w, "failed to delete schedule: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})
}

// SchedulesListHandler handles GET /schedules.
// Returns all schedules as a JSON array.
func (s *ManagementServer) SchedulesListHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.OnScheduleList == nil {
		http.Error(w, "schedule management not available", http.StatusServiceUnavailable)
		return
	}

	schedules, err := s.OnScheduleList()
	if err != nil {
		gatewaylog.Default.Warn("[Management] failed to list schedules",
			gatewaylog.F("error", err.Error()))
		http.Error(w, "failed to list schedules: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if schedules == nil {
		schedules = []*scheduler.Schedule{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(schedules)
}

// SchedulesUpsertHandler handles POST /schedules.
// Creates or updates a schedule.
func (s *ManagementServer) SchedulesUpsertHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var cfg ScheduleConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if cfg.Name == "" {
		http.Error(w, "schedule name required", http.StatusBadRequest)
		return
	}
	if cfg.Cron == "" {
		http.Error(w, "cron expression required", http.StatusBadRequest)
		return
	}
	if cfg.FlowName == "" {
		http.Error(w, "flow_name required", http.StatusBadRequest)
		return
	}

	if s.OnScheduleUpsert == nil {
		http.Error(w, "schedule management not available", http.StatusServiceUnavailable)
		return
	}

	sched := &scheduler.Schedule{
		Name:        cfg.Name,
		Cron:        cfg.Cron,
		FlowName:    cfg.FlowName,
		TenantAlias: cfg.TenantAlias,
		Enabled:     cfg.Enabled,
		TimeoutSec:  cfg.TimeoutSec,
		Constants:   cfg.Constants,
	}

	if err := s.OnScheduleUpsert(sched); err != nil {
		gatewaylog.Default.Warn("[Management] failed to upsert schedule",
			gatewaylog.F("name", cfg.Name),
			gatewaylog.F("error", err.Error()))
		http.Error(w, "failed to upsert schedule: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "upserted", "name": cfg.Name})
}

// SchedulesDeleteHandler handles DELETE /schedules/{name}.
// Deletes a schedule by name.
func (s *ManagementServer) SchedulesDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/schedules/")
	path = strings.TrimSpace(path)
	if path == "" {
		http.Error(w, "schedule name required", http.StatusBadRequest)
		return
	}

	if s.OnScheduleDelete == nil {
		http.Error(w, "schedule management not available", http.StatusServiceUnavailable)
		return
	}

	if err := s.OnScheduleDelete(path); err != nil {
		gatewaylog.Default.Warn("[Management] failed to delete schedule",
			gatewaylog.F("name", path),
			gatewaylog.F("error", err.Error()))
		http.Error(w, "failed to delete schedule: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": path})
}

// SchedulesHistoryHandler handles GET /schedules/{name}/history.
// Returns execution history for a schedule.
func (s *ManagementServer) SchedulesHistoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/schedules/")
	path = strings.TrimSpace(path)
	// Remove /history suffix
	path = strings.TrimSuffix(path, "/history")
	path = strings.TrimSpace(path)

	if path == "" {
		http.Error(w, "schedule name required", http.StatusBadRequest)
		return
	}

	// If OnScheduleHistory is not available, return empty array
	if s.OnScheduleHistory == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "[]")
		return
	}

	history, err := s.OnScheduleHistory(path)
	if err != nil {
		gatewaylog.Default.Warn("[Management] failed to get schedule history",
			gatewaylog.F("name", path),
			gatewaylog.F("error", err.Error()))
		http.Error(w, "failed to get history: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if history == nil {
		_, _ = fmt.Fprint(w, "[]")
	} else {
		_ = json.NewEncoder(w).Encode(history)
	}
}

// NamedQueriesHandler serves GET /named-queries and POST /named-queries.
func (s *ManagementServer) NamedQueriesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		// Return all named queries from the store.
		queries := s.getStoredQueries()
		_ = json.NewEncoder(w).Encode(map[string]any{"queries": queries})
	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			SQL         string `json:"sql"`
			BatchBy     string `json:"batch_by,omitempty"`
			BatchWindow string `json:"batch_window,omitempty"`
			BatchMax    int    `json:"batch_max,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, `{"error":"name and sql required"}`, http.StatusBadRequest)
			return
		}
		queries := s.getStoredQueries()
		queries[req.Name] = datasource.NamedQueryConfig{
			SQL: req.SQL, BatchBy: req.BatchBy,
			BatchWindow: req.BatchWindow, BatchMax: req.BatchMax,
		}
		s.storeQueries(queries)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// NamedQueryDeleteHandler serves DELETE /named-queries/{name}.
func (s *ManagementServer) NamedQueryDeleteHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Extract name from path — strip prefix "/named-queries/"
	name := strings.TrimPrefix(r.URL.Path, "/named-queries/")
	name = strings.Trim(name, "/")
	if name == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}
	queries := s.getStoredQueries()
	delete(queries, name)
	s.storeQueries(queries)
	w.WriteHeader(http.StatusNoContent)
}

// RedisSourcesHandler serves GET /redis-sources.
func (s *ManagementServer) RedisSourcesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var names []string
	if s.RedisSourcePool != nil {
		names = s.RedisSourcePool.Names()
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"sources": names})
}

// MigrationsHandler serves GET /migrations.
func (s *ManagementServer) MigrationsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"migrations": []any{}})
}

// DocumentConnectorsHandler serves GET /document-connectors.
func (s *ManagementServer) DocumentConnectorsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type item struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	var out []item
	if s.cfgMgr != nil {
		for _, c := range s.cfgMgr.Gateway().DocumentConnectors {
			out = append(out, item{Name: c.Name, Type: c.Kind})
		}
	}
	if out == nil {
		out = []item{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"connectors": out})
}

// StorageConnectorsHandler serves GET /storage-connectors.
func (s *ManagementServer) StorageConnectorsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type item struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	var out []item
	if s.cfgMgr != nil {
		for _, c := range s.cfgMgr.Gateway().StorageProviders {
			out = append(out, item{Name: c.Name, Type: c.Type})
		}
	}
	if out == nil {
		out = []item{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"connectors": out})
}

// MessagingPublishersHandler serves GET /messaging-publishers.
func (s *ManagementServer) MessagingPublishersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type item struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	var out []item
	if s.cfgMgr != nil {
		for _, c := range s.cfgMgr.Gateway().MessagingPublishers {
			out = append(out, item{Name: c.Name, Type: c.Kind})
		}
	}
	if out == nil {
		out = []item{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"publishers": out})
}

// EventListenersHandler serves GET /event-listeners.
func (s *ManagementServer) EventListenersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type item struct {
		Name      string `json:"name"`
		Publisher string `json:"publisher"`
	}
	var out []item
	if s.cfgMgr != nil {
		for _, c := range s.cfgMgr.Gateway().EventListeners {
			out = append(out, item{Name: c.Name, Publisher: c.Publisher})
		}
	}
	if out == nil {
		out = []item{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"listeners": out})
}

// getStoredQueries retrieves named queries from the registry store.
func (s *ManagementServer) getStoredQueries() map[string]datasource.NamedQueryConfig {
	const key = "_all"
	if s.dataStore == nil {
		return map[string]datasource.NamedQueryConfig{}
	}
	data, ok, err := s.dataStore.GetGlobal(context.Background(), config.DomainNamedQueries, key)
	if err != nil || !ok || len(data) == 0 {
		return map[string]datasource.NamedQueryConfig{}
	}
	var m map[string]datasource.NamedQueryConfig
	_ = json.Unmarshal(data, &m)
	if m == nil {
		return map[string]datasource.NamedQueryConfig{}
	}
	return m
}

// storeQueries persists named queries to the registry store.
func (s *ManagementServer) storeQueries(queries map[string]datasource.NamedQueryConfig) {
	const key = "_all"
	if s.dataStore == nil {
		return
	}
	data, _ := json.Marshal(queries)
	_ = s.dataStore.PutGlobal(context.Background(), config.DomainNamedQueries, key, data)
}
