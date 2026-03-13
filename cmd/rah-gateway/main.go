package main

import (
	"context"
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
	// registry is created before the goroutine so the handler closure never
	// captures a nil pointer. Names are populated at sync time via
	// ApplyUnifiedSync → registry.GetOrAssignId, and resolved post-response via
	// registry.GetNameByID with no hot-path cost.
	r := router.New()
	fm := engine.NewFlowManager(12000, cfg)
	obs := observability.NewFromEnv()
	accessLog := observability.NewAccessLogger(8192)
	registry := control.NewNameRegistry()

	// 3. Register Headers and Setup APIs
	fm.HeaderRegistry.RegisterHeader("Authorization")
	compiler := control.NewCompiler(fm)
	setupRoutes(r, fm, compiler)

	// 4. The Unified Hot-Path Handler
	go func() {
		handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Always capture start time — needed for access log regardless of obs config.
			reqStart := time.Now()

			// A. Router Lookup — load current atomic state so dynamically
			// registered APIs (via /sync) are always visible.
			currentState := fm.State.Load()
			apiId := currentState.Router.Lookup(req.URL.Path)
			if apiId == 0 {
				http.NotFound(w, req)
				// No API name or tenant for 404 — pass empty/zero values.
				accessLog.Snapshot(
					"", 0, "", 0,
					req.Method, req.URL.Path,
					http.StatusNotFound,
					time.Since(reqStart).Nanoseconds(), 0, 0, 0,
					req.ContentLength, 0,
					req,
				)
				return
			}

			// B. Lifecycle: Get Context and Reset with ResponseWriter Interface
			ctx := fm.Pool.Get().(*rctx.Context)
			ctx.Reset(w) // set Writer before any processing — new pool contexts have Writer=nil

			ctx.Obs = obs
			ctx.RequestStartNs = reqStart.UnixNano()

			if obs.ShouldTrace() {
				trace := obs.StartRequest(apiId, ctx.TenantID, req.Method, req.URL.Path)
				ctx.Trace = &trace
			}

			ctx.ApiId = apiId
			ctx.SnapshotMetadata(req.Method, req.URL.Path, req.URL.RawQuery)

			// C. Delegate Execution to FlowManager
			fm.ProcessRequest(ctx, req)

			// D. Finalize: flush buffered response — client receives data here.
			ctx.Finalize()

			// E. Post-response: snapshot for async access log and observability.
			// Client has already received the response — none of this adds latency.
			// req remains valid until this goroutine returns, so req.Header reads
			// in Snapshot() are safe.
			total := time.Since(reqStart)
			upstreamNs := atomic.LoadInt64(&ctx.UpstreamTimeNs)
			upstream := time.Duration(upstreamNs)
			gateway := total - upstream
			if gateway < 0 {
				gateway = 0
			}

			ttfbNs := ctx.FirstByteSentNs - ctx.RequestStartNs
			if ttfbNs < 0 {
				ttfbNs = 0
			}

			// API name: resolved from registry (populated at sync time).
			// TenantKey: set during request by registry_lookup step; empty for tenant-agnostic APIs.
			accessLog.Snapshot(
				registry.GetNameByID(ctx.ApiId),
				ctx.ApiId,
				ctx.TenantKey,
				ctx.TenantID,
				req.Method, req.URL.Path,
				ctx.ResponseStatus,
				total.Nanoseconds(), gateway.Nanoseconds(), upstreamNs, ttfbNs,
				req.ContentLength, ctx.ClientBytesSent,
				req,
			)

			obs.FinishRequest(ctx.Trace, ctx.ResponseStatus, total, gateway, upstream,
				int(atomic.LoadInt32(&ctx.UpstreamCalls)),
				ctx.ClientBytesSent,
				atomic.LoadInt64(&ctx.UpstreamBytesTx),
				atomic.LoadInt64(&ctx.UpstreamBytesRx),
			)

			if ctx.ShouldReturnToPool() {
				// ReturnContext releases borrowed arena blocks and slot extensions
				// back to their pools before returning the context itself.
				fm.ReturnContext(ctx)
			}
		})

		addr := fmt.Sprintf(":%d", *port)
		log.Printf("Rah Gateway listening on %s\n", addr)
		log.Fatal(http.ListenAndServe(addr, handler))
	}()

	ms := control.NewManagementServer(fm, compiler, registry)
	// Bootstrap BEFORE SetDataStore so the bootstrap reads do not trigger
	// redundant writes back to the store.
	if err := ms.Bootstrap(bootstrapCtx, dataStoreMgr); err != nil {
		log.Printf("failed control-plane bootstrap from datastore: %v", err)
	}
	// Optional: enable write-through persistence for subsequent upserts/deletes.
	// Remove this line when a central orchestrator owns persistence and RAH only
	// reads on boot.
	ms.SetDataStore(dataStoreMgr)

	//Control Plane (Management)
	mux := http.NewServeMux()
	mux.HandleFunc("/sync", ms.UnifiedSyncHandler)
	mux.HandleFunc("/getAllApis", ms.GetAllApisHandler)
	mux.HandleFunc("/debug/observability", obs.DebugHandler)
	mux.HandleFunc("/debug/arena", func(w http.ResponseWriter, _ *http.Request) {
		// Reports cumulative overflow counts since process start.
		// Non-zero ArenaOverflows or SlotOverflows indicates default arena/slot
		// sizing needs tuning (increase rctx.ArenaBlockSize or BaseByteSlots).
		fmt.Fprintf(w,
			`{"arena_overflows":%d,"slot_overflows":%d,"arena_block_size":%d,"base_byte_slots":%d,"max_extra_arenas":%d}`,
			fm.Metrics.ArenaOverflows.Load(),
			fm.Metrics.SlotOverflows.Load(),
			rctx.ArenaBlockSize,
			rctx.BaseByteSlots,
			rctx.MaxExtraArenas,
		)
	})
	mux.HandleFunc("/config/datastores", dataStoreMgr.DataStoreConfigHandler)
	mux.HandleFunc("/config/log", accessLog.ConfigHandler)
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
			Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
				ctx.Write([]byte("Welcome to Rah Gateway"))
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
			Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
				userId := ctx.ByteSlots[10]
				ctx.Write([]byte("User Profile for ID: "))
				ctx.Write(userId)
				return s.PC + 1
			},
		},
	}

	def2 := engine.BakeDefinition(2, "/v1/user")
	compiler.BakeSubRouter(def2, "/{id}/profile", "GET", p2Instructions, true)

	state.Definitions[2] = def2
	state.Router.Add(def2.BaseRawPath, 2)
}
