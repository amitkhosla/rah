package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"rah/internal/config"
	"rah/internal/control"
	"rah/internal/datastore"
	"rah/internal/engine"
	"rah/internal/observability"
	"rah/internal/rctx"
	"rah/internal/router"
	"sync/atomic"
	"time"
)

func main() {
	port := flag.Int("port", 8080, "Gateway Port")
	mPort := flag.Int("mport", 8081, "Management Port")
	flag.Parse()

	// 1. Initial Configuration
	cfg := config.GlobalLayout{
		MaxBytesSlots: 32,
		MaxIntsSlots:  16,
		DefaultLimits: config.ResourceLimit{
			MaxBodySize: 1024 * 1024, // 1MB
		},
	}

	dataStores := config.DataStoreConfig{
		Stores: map[string]config.StoreConfig{
			"local_disk": {
				Name:    "local_disk",
				Kind:    config.StoreDisk,
				Enabled: true,
				Connection: config.StoreConnection{
					Path: "/var/lib/rah",
				},
			},
		},
		Bindings: map[config.DataDomain]string{
			config.DomainAPIDefinitions: "local_disk",
			config.DomainFlows:          "local_disk",
			config.DomainTenantRegistry: "local_disk",
			config.DomainCache:          "local_disk",
			config.DomainRateLimit:      "local_disk",
			config.DomainCustomerData:   "local_disk",
			config.DomainInstances:      "local_disk",
		},
	}

	dataStoreMgr, err := control.NewDataStoreManager(dataStores)
	if err != nil {
		log.Fatalf("invalid data store config: %v", err)
	}

	bootstrapCtx := context.Background()

	if dataStoreMgr.IsConfigured(config.DomainAPIDefinitions) {
		apisSnapshot, err := dataStoreMgr.ReadAPIDefinitionsSnapshot(bootstrapCtx)
		if err != nil {
			log.Printf("failed to read api_definitions snapshot: %v", err)
		} else {
			log.Printf("api_definitions snapshot loaded: %d", len(apisSnapshot))
		}
	}

	if dataStoreMgr.IsConfigured(config.DomainFlows) {
		flowsSnapshot, err := dataStoreMgr.ReadFlowsSnapshot(bootstrapCtx)
		if err != nil {
			log.Printf("failed to read flows snapshot: %v", err)
		} else {
			log.Printf("flows snapshot loaded: %d", len(flowsSnapshot))
		}
	}

	if dataStoreMgr.IsConfigured(config.DomainRegistryStore) {
		registrySnapshot, err := dataStoreMgr.ReadRegistryStoreSnapshot(bootstrapCtx, datastore.Tenant("bootstrap"))
		if err != nil {
			log.Printf("failed to read registrystore snapshot: %v", err)
		} else {
			log.Printf("registrystore bootstrap records loaded: %d", len(registrySnapshot))
		}
	}

	if dataStoreMgr.IsConfigured(config.DomainInstances) {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "rah-unknown"
		}
		if err := dataStoreMgr.RegisterInstance(bootstrapCtx, hostname, []byte("online")); err != nil {
			log.Printf("failed to register instance: %v", err)
		}
	}

	// 2. Component Initialization
	r := router.New()
	fm := engine.NewFlowManager(12000, cfg)
	obs := observability.NewFromEnv()

	// 3. Register Headers and Setup APIs
	// This maps "Authorization" header to ByteSlots[0] globally
	fm.HeaderRegistry.RegisterHeader("Authorization")
	compiler := control.NewCompiler(fm)
	setupRoutes(r, fm, compiler)

	// 4. The Unified Hot-Path Handler
	go func() {
		handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			var reqStart time.Time
			if obs.ShouldTimeRequests() {
				reqStart = time.Now()
			}
			// A. Router Lookup (Returns uint32)
			apiId := r.Lookup(req.URL.Path)
			if apiId == 0 {
				http.NotFound(w, req)
				if !reqStart.IsZero() {
					obs.FinishRequest(nil, http.StatusNotFound, time.Since(reqStart), time.Since(reqStart), 0, 0, 0, 0, 0)
				}
				return
			}

			// B. Lifecycle: Get Context and Reset with ResponseWriter Interface
			ctx := fm.Pool.Get().(*rctx.Context)
			ctx.Reset(w)
			ctx.Obs = obs
			if !reqStart.IsZero() {
				ctx.RequestStartNs = reqStart.UnixNano()
			}
			if obs.ShouldTrace() {
				trace := obs.StartRequest(apiId, ctx.TenantID)
				ctx.Trace = &trace
			}

			ctx.ApiId = apiId

			ctx.SnapshotMetadata(req.Method, req.URL.Path, req.URL.RawQuery)

			// C. Delegate Execution to FlowManager
			fm.ProcessRequest(ctx, req)

			// D. Finalize: Flush buffered data or commit status code
			ctx.Finalize()
			if !reqStart.IsZero() {
				total := time.Since(reqStart)
				upstream := time.Duration(atomic.LoadInt64(&ctx.UpstreamTimeNs))
				gateway := total - upstream
				if gateway < 0 {
					gateway = 0
				}
				obs.FinishRequest(ctx.Trace, ctx.ResponseStatus, total, gateway, upstream, int(atomic.LoadInt32(&ctx.UpstreamCalls)), ctx.ClientBytesSent, atomic.LoadInt64(&ctx.UpstreamBytesTx), atomic.LoadInt64(&ctx.UpstreamBytesRx))
			}

			if ctx.ShouldReturnToPool() {
				fm.Pool.Put(ctx)
			}
		})

		addr := fmt.Sprintf(":%d", *port)
		log.Printf("Rah Gateway listening on %s\n", addr)
		log.Fatal(http.ListenAndServe(addr, handler))
	}()

	registry := control.NewNameRegistry()
	ms := &control.ManagementServer{
		FlowManager: fm,
		Compiler:    compiler,
		Registry:    registry,
	}

	if err := bootstrapControlPlaneFromDataStore(bootstrapCtx, dataStoreMgr, ms); err != nil {
		log.Printf("failed control-plane bootstrap from datastore: %v", err)
	}

	//Control Plane (Management)
	mux := http.NewServeMux()
	mux.HandleFunc("/sync", ms.UnifiedSyncHandler)
	mux.HandleFunc("/debug/observability", obs.DebugHandler)
	mux.HandleFunc("/config/datastores", dataStoreMgr.DataStoreConfigHandler)
	log.Printf("Management API running on %d", *mPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *mPort), mux))
}

func setupRoutes(r *router.RahRouter, fm *engine.FlowManager, compiler *control.Compiler) {
	state := fm.State.Load()
	state.Router = r

	// --- API 1: Public Hello ---
	p1Instructions := []engine.Instruction{
		{
			Name: "HelloStep",
			// ADDED: *engine.ExecutionState parameter
			Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
				ctx.Write([]byte("Welcome to Rah Gateway"))
				// Return PC + 1 to move to the next instruction
				return s.PC + 1
			},
		},
	}

	def1 := engine.BakeDefinition(1, "/v1/hello")
	compiler.BakeSubRouter(def1, "/", "GET", p1Instructions, true)

	state.Definitions[1] = def1
	state.Router.Add(def1.BaseRawPath, 1)

	// --- API 2: Secure User Data with Path Params ---
	p2Instructions := []engine.Instruction{
		{
			Name: "ShowProfile",
			// ADDED: *engine.ExecutionState parameter
			Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
				// In your compiler setup, you assigned nextSlot starting at 10.
				// Ensure the parameter extraction actually maps to this slot.
				userId := ctx.ByteSlots[10]
				ctx.Write([]byte("User Profile for ID: "))
				ctx.Write(userId)
				return s.PC + 1
			},
		},
	}

	// Base path is /v1/user
	def2 := engine.BakeDefinition(2, "/v1/user")
	// Sub-path contains the dynamic segment {id}
	compiler.BakeSubRouter(def2, "/{id}/profile", "GET", p2Instructions, true)

	state.Definitions[2] = def2
	state.Router.Add(def2.BaseRawPath, 2)
}

func bootstrapControlPlaneFromDataStore(ctx context.Context, dsm *control.DataStoreManager, ms *control.ManagementServer) error {
	flowsSnapshot, err := dsm.ReadFlowsSnapshot(ctx)
	if err != nil {
		return err
	}
	apisSnapshot, err := dsm.ReadAPIDefinitionsSnapshot(ctx)
	if err != nil {
		return err
	}

	if len(flowsSnapshot) == 0 && len(apisSnapshot) == 0 {
		return nil
	}

	req := control.UnifiedSyncRequest{SyncUUID: "bootstrap"}

	for name, raw := range flowsSnapshot {
		var steps []control.StepConfig
		if err := json.Unmarshal(raw, &steps); err != nil {
			continue
		}
		req.Flows = append(req.Flows, control.FlowUpdate{
			Name:         name,
			Instructions: steps,
			Action:       "upsert",
		})
	}

	for name, raw := range apisSnapshot {
		var api control.ApiConfig
		if err := json.Unmarshal(raw, &api); err != nil {
			continue
		}
		if api.ApiID == "" {
			api.ApiID = name
		}
		req.Apis = append(req.Apis, control.ApiUpdate{
			Name:     api.ApiID,
			Path:     api.Path,
			FlowName: api.FlowName,
			Action:   "upsert",
		})
	}

	if len(req.Flows) == 0 && len(req.Apis) == 0 {
		return nil
	}

	return ms.ApplyUnifiedSync(req)
}
