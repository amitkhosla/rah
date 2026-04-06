package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"rah/internal/cache"
	"rah/internal/config"
	"rah/internal/control"
	"rah/internal/datastore"
	"rah/internal/engine"
	"rah/internal/ingest"
	"rah/internal/mcpreg"
	"rah/internal/observability"
	"rah/internal/pricing"
	"rah/internal/quota"
	"rah/internal/rctx"
	tenantregistry "rah/internal/registry"
	"rah/internal/router"
	"rah/internal/secrets"
	"rah/internal/vectorstore"
	"sync/atomic"
	"time"
)

// domainScopedKV adapts DataStoreManager to the RegistryStoreBackend interface,
// pre-scoped to a single storage domain. Serialisation lives in TenantRegistryStore;
// this adapter only bridges the domain parameter.
type domainScopedKV struct {
	mgr    *control.DataStoreManager
	domain config.DataDomain
}

func (d *domainScopedKV) Put(ctx context.Context, key string, value []byte) error {
	return d.mgr.PutGlobal(ctx, d.domain, key, value)
}
func (d *domainScopedKV) MultiPut(ctx context.Context, kvs map[string][]byte) error {
	return d.mgr.MultiPutGlobal(ctx, d.domain, kvs)
}
func (d *domainScopedKV) Get(ctx context.Context, key string) ([]byte, bool, error) {
	return d.mgr.GetGlobal(ctx, d.domain, key)
}
func (d *domainScopedKV) Delete(ctx context.Context, key string) error {
	return d.mgr.DeleteGlobal(ctx, d.domain, key)
}
func (d *domainScopedKV) ListKeys(ctx context.Context, prefix string) ([]string, error) {
	return d.mgr.ListGlobalKeys(ctx, d.domain, prefix)
}

func main() {
	port := flag.Int("port", 8080, "Gateway Port")
	mPort := flag.Int("mport", 8081, "Management Port")
	configPath := flag.String("config", "", "Path to gateway config file (.json or .yaml)")
	flag.Parse()

	// 1. Load Configuration
	var cfgMgr *config.Manager
	if *configPath != "" {
		var err error
		cfgMgr, err = config.Load(*configPath)
		if err != nil {
			log.Fatalf("failed to load config: %v", err)
		}
		log.Printf("config loaded from %s", *configPath)
	} else {
		cfgMgr = config.Default()
		log.Printf("no config file specified (-config), using built-in defaults")
	}

	cfg := cfgMgr.Layout()

	gatewayCtx, gatewayCancel := context.WithCancel(context.Background())
	defer gatewayCancel()

	// 1a. Secrets Manager — must be initialised before the datastore so that
	// credential references in store configs are resolved at startup.
	secretsMgr, err := secrets.New(gatewayCtx, cfgMgr.Secrets())
	if err != nil {
		log.Fatalf("failed to initialise secrets manager: %v", err)
	}
	defer secretsMgr.Close()

	dataStoreMgr, err := control.NewDataStoreManager(gatewayCtx, cfgMgr.DataStore(), secretsMgr)
	if err != nil {
		log.Fatalf("invalid data store config: %v", err)
	}

	bootstrapCtx := gatewayCtx

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

	if dataStoreMgr.IsConfigured(config.DomainTenantRegistry) {
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

	// 2b. Ingestion pipeline — started before the compiler so that
	// IngestPipeline can be wired into baked instruction closures.
	var ingestPipeline *ingest.Pipeline
	{
		pipeline, err := ingest.NewPipelineFromConfig(cfgMgr.Gateway().Ingest)
		if err != nil {
			log.Fatalf("[ingest] configuration error: %v", err)
		}
		if pipeline != nil {
			log.Printf("[ingest] pipeline started")
			defer pipeline.Stop()
			ingestPipeline = pipeline
		} else {
			log.Printf("[ingest] pipeline disabled (ingest.enabled=false)")
		}
	}

	// 2c. Pricing Manager — initialize before compiler
	// Loads pricing from config, with fallback to hardcoded defaults
	log.Printf("Initializing pricing manager...")
	pricingMgr := pricing.NewPricingManager()
	if err := pricingMgr.LoadFromConfig(cfgMgr.Gateway().Pricing); err != nil {
		log.Printf("Warning: failed to load pricing from config: %v (using defaults)", err)
	}
	if err := pricingMgr.LoadFromLLMConfig(cfgMgr.Gateway().LLM); err != nil {
		log.Printf("Warning: failed to load pricing from llm.models[]: %v", err)
	}
	pricingModels := pricingMgr.GetPricing()
	log.Printf("Pricing initialized: %d models cached", len(pricingModels))

	// 2d. Metrics Collector & Daily Learning Job
	// Track token estimation accuracy for next day's estimates
	log.Printf("Initializing metrics collection...")
	metricsCollector := pricing.NewMetricsCollector()
	learningJobTime := time.Date(2000, 1, 1, 2, 0, 0, 0, time.UTC) // 2 AM UTC
	learningJob := pricing.NewDailyLearningJob(metricsCollector, learningJobTime)
	defer learningJob.Stop()
	log.Printf("Daily learning job scheduled")

	// 2e. Cost Quotas
	// TODO: Load tenant quotas from config or database.
	// For now, quotaManager is ready but no quotas are configured (all tenants unlimited).
	// Example of registering a quota:
	//   fm.CostQuotaManager.RegisterQuota("acme-corp", quota.CostQuotaConfig{
	//       DailyCostLimit: 1000.0,
	//       MonthlyCostLimit: 20000.0,
	//       Windows: []quota.QuotaWindow{...},
	//   })
	// Quotas can be registered per-tenant as needed via:
	// - Config file (to be implemented)
	// - Management API (to be implemented)
	// - Direct API calls at startup

	// 3. Setup compiler and routes
	log.Printf("rah-gateway started | instance=%s port=%d", fm.TxIDGen.Fingerprint(), *port)
	compiler := control.NewCompiler(fm)
	compiler.SecretsMgr = secretsMgr
	compiler.PricingManager = pricingMgr

	// CredentialRegistry — optional; requires DomainCredentials in datastore config.
	var credReg *secrets.CredentialRegistry
	if credStore := control.NewCredentialStore(dataStoreMgr); credStore != nil {
		credReg = secrets.NewCredentialRegistry(credStore, secretsMgr)
		compiler.CredMgr = credReg
		log.Printf("CredentialRegistry enabled (credentials domain configured)")
	} else {
		log.Printf("CredentialRegistry disabled (no credentials domain configured — add 'credentials' binding to datastore config)")
	}

	// Cache — optional; controlled by [cache] section in config.
	// When enabled, cache_get/cache_put/cache_get_global/cache_put_global steps are available in flows.
	var cacheMgr *cache.CacheManager
	cacheCfg := cfgMgr.Cache()
	if !cacheCfg.Disabled {
		memBudget := uint64(cacheCfg.MemBudgetMB) * 1024 * 1024
		if memBudget == 0 {
			memBudget = 256 * 1024 * 1024 // default 256 MB
		}
		tenantLimit := uint64(cacheCfg.TenantLimitMB) * 1024 * 1024

		sizeClasses := cacheCfg.SizeClasses
		if len(sizeClasses) == 0 {
			sizeClasses = []uint32{256, 1024, 4096, 16384}
		}
		ttlTiers := cacheCfg.TTLTiers
		if len(ttlTiers) == 0 {
			ttlTiers = []uint32{60, 300, 3600}
		}

		var cacheBackend cache.CacheBackend
		switch cacheCfg.Backend.Kind {
		case "memory":
			cacheBackend = cache.NoopBackend
		case config.StoreRedis, config.StoreDragonFly:
			b, err := cache.NewRedisBackend(gatewayCtx, cacheCfg.Backend)
			if err != nil {
				log.Fatalf("cache: failed to connect Redis backend: %v", err)
			}
			cacheBackend = b
		default:
			// "" or "disk" — use disk backend
			if cacheCfg.Backend.Connection.Path != "" {
				b, err := cache.NewDiskBackend(cacheCfg.Backend.Connection.Path)
				if err != nil {
					log.Fatalf("cache: failed to init disk backend at %q: %v", cacheCfg.Backend.Connection.Path, err)
				}
				cacheBackend = b
			}
			// nil → NewCacheManager uses DefaultDiskCachePath ("./icache2")
		}

		var err error
		cacheMgr, err = cache.NewCacheManager(memBudget, sizeClasses, ttlTiers, 0, tenantLimit, cacheBackend)
		if err != nil {
			log.Fatalf("cache: failed to initialise CacheManager: %v", err)
		}
		defer cacheMgr.Stop()
		compiler.CacheMgr = cacheMgr
		log.Printf("Cache enabled: mem=%dMB sizeClasses=%v ttlTiers=%v backend=%s",
			cacheCfg.MemBudgetMB, sizeClasses, ttlTiers, cacheCfg.Backend.Kind)

		// CacheManager IS the OpFlusher — it decides L1 vs backend based on config.
		fm.CacheExec = cacheMgr
	} else {
		log.Printf("Cache disabled (cache.disabled=true in config)")
	}

	// Initialize vector stores — optional; controlled by [vector_stores] in config.
	// Each store's API key is resolved through the secrets manager at startup.
	vectorStores := make(map[string]vectorstore.VectorStore)
	for _, vsCfg := range cfgMgr.Gateway().VectorStores {
		resolvedKey, err := secretsMgr.ResolveString(gatewayCtx, vsCfg.APIKeyRef)
		if err != nil {
			log.Fatalf("vector store %q: failed to resolve api_key_ref %q: %v", vsCfg.Name, vsCfg.APIKeyRef, err)
		}
		vs, err := vectorstore.NewVectorStore(vsCfg, resolvedKey)
		if err != nil {
			log.Fatalf("vector store %q: %v", vsCfg.Name, err)
		}
		vectorStores[vsCfg.Name] = vs
		log.Printf("vector store %q (%s) initialized", vsCfg.Name, vsCfg.Kind)
	}
	defer func() {
		for name, vs := range vectorStores {
			if err := vs.Close(); err != nil {
				log.Printf("vector store %q close: %v", name, err)
			}
		}
	}()
	compiler.VectorStores = vectorStores

	// Registry manager — created here (before the gateway goroutine) so that
	// RegistryExec can be wired to fm before the first request arrives.
	regMgr := tenantregistry.NewRegistryManager()
	// Wire RegistryExec: routes buffered registry PUT ops to RegistryManager.
	fm.RegistryExec = engine.NewRegistryExecutor(regMgr)

	// Wire regMgr into compiler so load_service_url / load_identifier can
	// pre-resolve KeyIDs at bake time (avoids radix walk on every request).
	compiler.RegMgr = regMgr

	// Wire ingestion pipeline into compiler so emit_event steps capture it
	// in their instruction closures at bake time.
	compiler.IngestPipeline = ingestPipeline

	// Load default rate limit presets from config into the registry.
	for _, preset := range cfg.DefaultRateLimits {
		if preset.Name == "" {
			continue
		}
		regMgr.UpsertNamedRateLimitConfig(preset.Name, tenantregistry.RateLimitConfig{
			PerSec:      preset.RatePerSec,
			PerMin:      preset.RatePerMin,
			BurstFactor: preset.BurstFactor,
		})
	}

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
			ctx.Timing.StartNs = reqStart.UnixNano()

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

			// D2. After-response hooks: run deferred ingest events now that
			// the response is committed. These are non-blocking channel sends
			// so they complete in nanoseconds; no latency impact on the caller.
			for _, fn := range ctx.AfterResponse {
				fn()
			}

			// E. Post-response: snapshot for async access log and observability.
			// Client has already received the response — none of this adds latency.
			// req remains valid until this goroutine returns, so req.Header reads
			// in Snapshot() are safe.
			total := time.Since(reqStart)
			upstreamNs := atomic.LoadInt64(&ctx.Timing.UpstreamTimeNs)
			upstream := time.Duration(upstreamNs)
			gateway := max(total-upstream, 0)

			ttfbNs := max(ctx.Timing.FirstByteSentNs-ctx.Timing.StartNs, 0)

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
				req.ContentLength, ctx.Timing.ClientBytesSent,
				req,
			)

			obs.FinishRequest(ctx.Trace, ctx.ResponseStatus, total, gateway, upstream,
				int(atomic.LoadInt32(&ctx.Timing.UpstreamCalls)),
				ctx.Timing.ClientBytesSent,
				atomic.LoadInt64(&ctx.Timing.UpstreamBytesTx),
				atomic.LoadInt64(&ctx.Timing.UpstreamBytesRx),
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

	ts := tenantregistry.NewTenantServer(regMgr)

	// Restore registry (tenants + rate limit configs) BEFORE bootstrapping
	// flows/APIs so that named rate limit references resolve correctly when
	// Bootstrap bakes the instruction tables.
	if dataStoreMgr.IsConfigured(config.DomainTenantRegistry) {
		regStore := tenantregistry.NewTenantRegistryStore(&domainScopedKV{
			mgr:    dataStoreMgr,
			domain: config.DomainTenantRegistry,
		})
		snap, err := regStore.LoadAll(bootstrapCtx)
		if err != nil {
			log.Printf("[Registry] failed to load from datastore: %v", err)
		} else {
			regMgr.RestoreFromSnapshot(snap)
			log.Printf("[Registry] Restored %d tenant(s) and %d rate limit config(s)",
				len(snap.Tenants), len(snap.RateLimits))
		}
		// Wire AFTER restore so startup reads don't write back what was just read.
		regMgr.SetStore(regStore)
	}

	ms := control.NewManagementServer(fm, compiler, registry, regMgr)
	// Bootstrap BEFORE SetDataStore so the bootstrap reads do not trigger
	// redundant writes back to the store. Registry must be restored first (above)
	// so named rate limit configs are available when Bootstrap bakes flows.
	if err := ms.Bootstrap(bootstrapCtx, dataStoreMgr); err != nil {
		log.Printf("failed control-plane bootstrap from datastore: %v", err)
	}
	ms.SetDataStore(dataStoreMgr)

	// Load master key for encrypting credential values stored via the per-tenant API.
	// Returns NoopEncryptor (passthrough) if no master key is configured — safe to use regardless.
	encCfg := cfgMgr.Secrets().Encrypted
	masterKeyEnc, err := secrets.LoadMasterKey(secrets.MasterKeyConfig{
		KeyEnv:        encCfg.MasterKeyEnv,
		KeyPath:       encCfg.MasterKeyPath,
		PassphraseEnv: encCfg.MasterKeyPassphraseEnv,
		Salt:          encCfg.MasterKeySalt,
		KeyVersion:    encCfg.MasterKeyVersion,
	})
	if err != nil {
		log.Fatalf("failed to load master key for credential encryption: %v", err)
	}

	// Wire per-tenant credential routes into the tenant sub-handler BEFORE
	// calling ts.RegisterHandlers so the ExtraSubHandler is set when /tenants/ is registered.
	if credReg != nil {
		ts.ExtraSubHandler = control.NewTenantCredentialHandler(credReg, masterKeyEnc)
	}

	//Control Plane (Management)
	mux := http.NewServeMux()
	mux.HandleFunc("/sync", ms.UnifiedSyncHandler)
	mux.HandleFunc("/getAllApis", ms.GetAllApisHandler)
	mux.HandleFunc("/meta/steps", ms.StepsMetaHandler)
	ts.RegisterHandlers(mux)
	mux.HandleFunc("/debug/observability", obs.DebugHandler)
	mux.HandleFunc("/debug/arena", func(w http.ResponseWriter, _ *http.Request) {
		// Reports cumulative overflow counts since process start.
		// Non-zero ArenaOverflows indicates ArenaInlineSize needs tuning.
		fmt.Fprintf(w,
			`{"arena_overflows":%d,"arena_inline_size":%d,"arena_block_size":%d,"base_byte_slots":%d,"slot_value_threshold":%d}`,
			fm.Metrics.ArenaOverflows.Load(),
			rctx.ArenaInlineSize,
			rctx.ArenaBlockSize,
			rctx.BaseByteSlots,
			rctx.SlotValueThreshold,
		)
	})
	mux.HandleFunc("/config/datastores", dataStoreMgr.DataStoreConfigHandler)
	mux.HandleFunc("/config/log", accessLog.ConfigHandler)
	if credReg != nil {
		control.NewCredentialHandler(credReg).RegisterHandlers(mux)
	}

	// ─── Cost Tracking API Routes ───────────────────────────────────────
	// Requires admin token in X-Admin-Token header or Authorization: Bearer
	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		log.Printf("Warning: ADMIN_TOKEN environment variable not set. Cost tracking endpoints will deny all access.")
	}
	RegisterCostRoutes(mux, fm.CostQuotaManager, adminToken)
	log.Printf("Cost tracking endpoints registered at /api/v1/costs*")

	mcpReg := mcpreg.NewRegistry()
	compiler.MCPRegistry = mcpReg
	compiler.GatewayBase = fmt.Sprintf("http://localhost:%d", *port)
	control.RegisterAIRoutes(mux, cfgMgr, func() {
		go func() {
			if err := ms.Bootstrap(bootstrapCtx, dataStoreMgr); err != nil {
				log.Printf("[AI] rebake failed: %v", err)
			}
		}()
	}, dataStoreMgr, mcpReg)
	log.Printf("MCPReg initialized; virtual MCP server routes available at /ai/mcp/virtual")

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
	compiler.BakeSubRouter(def1, "/", "GET", p1Instructions, true, 0, 0, engine.AsyncDisabled)

	state.Definitions[1] = def1
	state.Router.Add(def1.BaseRawPath, 1)

	// --- API 2: Secure User Data with Path Params ---
	p2Instructions := []engine.Instruction{
		{
			Name: "ShowProfile",
			Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
				userId := ctx.ByteSlots[0]
				ctx.Write([]byte("User Profile for ID: "))
				ctx.Write(userId)
				return s.PC + 1
			},
		},
	}

	def2 := engine.BakeDefinition(2, "/v1/user")
	compiler.BakeSubRouter(def2, "/{id}/profile", "GET", p2Instructions, true, 0, 0, engine.AsyncDisabled)

	state.Definitions[2] = def2
	state.Router.Add(def2.BaseRawPath, 2)
}
