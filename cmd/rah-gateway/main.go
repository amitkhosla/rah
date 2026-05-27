package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"rah/internal/apikey"
	"rah/internal/cache"
	"rah/internal/config"
	"rah/internal/control"
	"rah/internal/datastore"
	"rah/internal/egress"
	"rah/internal/engine"
	enginesteps "rah/internal/engine/steps"
	grpcutil "rah/internal/grpc"
	"rah/internal/ingest"
	"rah/internal/mcpreg"
	"rah/internal/observability"
	"rah/internal/pricing"
	"rah/internal/quota"
	"rah/internal/rctx"
	tenantregistry "rah/internal/registry"
	"rah/internal/secrets"
	"rah/internal/vectorstore"
	"strconv"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"go.opentelemetry.io/otel"
	otlpmetricgrpc "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otlptracegrpc "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"google.golang.org/grpc"
	insecurecreds "google.golang.org/grpc/credentials/insecure"
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

// connAcceptKey is used to store the TCP connection accept time in the request context
// via http.Server.ConnContext. This enables per-request connection setup timing.
type connAcceptKey struct{}

func main() {
	port := flag.Int("port", 8080, "Gateway Port")
	mPort := flag.Int("mport", 8081, "Management Port")
	configPath := flag.String("config", "", "Path to gateway config file (.json or .yaml)")
	healthCheck := flag.Bool("health", false, "Probe the management /health endpoint and exit 0/1")
	flag.Parse()

	// Health-check mode: used by Docker HEALTHCHECK. Probes management plane and exits.
	if *healthCheck {
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/health", *mPort))
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

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

	adminUserStore := control.NewAdminUserStore(gatewayCtx, dataStoreMgr, cfgMgr.Gateway().Admin)
	log.Printf("admin user store initialised with %d user(s)", len(adminUserStore.List()))

	// Build observability store from config.
	obsCfg := cfgMgr.Gateway().Observability
	obsStoreParams := observability.ObsStoreParams{
		Type:         obsCfg.Store.Type,
		MaxAccessLog: obsCfg.Store.MaxAccessLog,
		MaxTraces:    obsCfg.Store.MaxTraces,
	}
	// For postgres/redis: resolve connection from observability domain bindings.
	// Try traces first, then access log as a fallback.
	if storeCfg, err := cfgMgr.Gateway().DataStore.ResolveStore(config.DomainObsTraces); err == nil {
		obsStoreParams.DSN = storeCfg.Connection.Address
		obsStoreParams.Password = storeCfg.Connection.Password
		obsStoreParams.PoolSize = storeCfg.Connection.PoolSize
		// If type is not explicitly set but a binding exists, infer type from store kind.
		if obsStoreParams.Type == "" {
			obsStoreParams.Type = string(storeCfg.Kind)
		}
	} else if storeCfg, err := cfgMgr.Gateway().DataStore.ResolveStore(config.DomainObsAccessLog); err == nil {
		obsStoreParams.DSN = storeCfg.Connection.Address
		obsStoreParams.Password = storeCfg.Connection.Password
		obsStoreParams.PoolSize = storeCfg.Connection.PoolSize
		// If type is not explicitly set but a binding exists, infer type from store kind.
		if obsStoreParams.Type == "" {
			obsStoreParams.Type = string(storeCfg.Kind)
		}
	}
	obsStore := observability.NewObsStoreFromParams(gatewayCtx, obsStoreParams)
	obsWriter := observability.NewObsWriter(obsStore, 200, 2*time.Second)
	obsWriter.Start(gatewayCtx)

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
	fm := engine.NewFlowManager(12000, cfg)
	obs := observability.NewFromEnv()
	// Prefer explicit gateway config for observability trace controls.
	// This overrides env defaults from NewFromEnv and keeps runtime behavior
	// aligned with gateway.yaml observability.traces settings.
	traceMode := obsCfg.Traces.Enabled
	traceSampleRate := obsCfg.Traces.SampleRate
	if traceSampleRate < 0 {
		traceSampleRate = 0
	}
	if traceSampleRate > 1 {
		traceSampleRate = 1
	}
	instructionTiming := obsCfg.Traces.InstructionTiming
	obs.UpdateConfig(&traceMode, &traceSampleRate, &instructionTiming, nil, nil, nil, nil)
	alwaysTrace5xx := obsCfg.Traces.AlwaysTrace5xx
	accessLog := observability.NewAccessLogger(8192)
	if obsCfg.AccessLog.SigningKeyRef != "" {
		if sigKey, sigErr := secretsMgr.Resolve(gatewayCtx, obsCfg.AccessLog.SigningKeyRef); sigErr != nil {
			log.Printf("[warn] access log signing key resolve failed: %v", sigErr)
		} else if len(sigKey) >= 16 {
			accessLog.SetSigningKey(sigKey)
			clear(sigKey)
		} else {
			log.Printf("[warn] access log signing key is too short (%d bytes, need ≥16); signing disabled", len(sigKey))
		}
	}
	registry := control.NewNameRegistry()

	// ── S8: OpenTelemetry SDK init ──────────────────────────────────────────────
	// Enabled via obs.export.otel.enabled in gateway config. No-op when disabled.
	if obsCfg.Export.OTEL.Enabled {
		otelCfg := obsCfg.Export.OTEL
		svcName := otelCfg.ServiceName
		if svcName == "" {
			svcName = "rah-gateway"
		}
		res, resErr := resource.Merge(
			resource.Default(),
			resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(svcName)),
		)
		if resErr != nil {
			log.Printf("[otel] resource merge warning: %v", resErr)
			res = resource.Default()
		}

		var grpcDialOpts []grpc.DialOption
		if otelCfg.Insecure {
			grpcDialOpts = append(grpcDialOpts, grpc.WithTransportCredentials(insecurecreds.NewCredentials()))
		}

		traceExp, traceErr := otlptracegrpc.New(gatewayCtx,
			otlptracegrpc.WithEndpoint(otelCfg.Endpoint),
			otlptracegrpc.WithDialOption(grpcDialOpts...),
		)
		if traceErr != nil {
			log.Fatalf("[otel] trace exporter init failed: %v", traceErr)
		}
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExp),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
		defer func() {
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tp.Shutdown(shutCtx)
		}()

		metricExp, metricErr := otlpmetricgrpc.New(gatewayCtx,
			otlpmetricgrpc.WithEndpoint(otelCfg.Endpoint),
			otlpmetricgrpc.WithDialOption(grpcDialOpts...),
		)
		if metricErr != nil {
			log.Fatalf("[otel] metric exporter init failed: %v", metricErr)
		}
		mp := sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
			sdkmetric.WithResource(res),
		)
		otel.SetMeterProvider(mp)
		defer func() {
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = mp.Shutdown(shutCtx)
		}()

		log.Printf("[otel] SDK initialized: endpoint=%s insecure=%v service=%s",
			otelCfg.Endpoint, otelCfg.Insecure, svcName)
	}

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
			// Redirect log output through the pipeline so gateway logs are
			// written to the configured log sink (e.g. a rolling file).
			// Falls back to stderr automatically if KindLog has no sinks.
			log.SetOutput(ingest.NewLogWriter(ingestPipeline))
		} else {
			log.Printf("[ingest] pipeline disabled (ingest.enabled=false)")
		}
	}

	// 2c. Pricing Manager — initialize before compiler
	// Loads pricing from config, with fallback to hardcoded defaults
	// 2d. Cost pricing & metrics
	// Note: Cost events are emitted via ingest pipeline to external analytics service.
	// Pricing data is optional; if not configured, cost calculation defaults to zero-cost.
	// Token metrics tracking (for learning) is handled by analytics service, not gateway.

	// 2e. Cost Quotas
	// Load tenant quotas from config
	log.Printf("Loading cost quotas...")
	quotasConfig := cfgMgr.Gateway().Quotas
	quotaCount := 0
	for _, tenantQuota := range quotasConfig.Tenants {
		tenantID := tenantQuota.TenantID
		if tenantID == "" {
			continue
		}

		// Build QuotaConfig from TenantQuotaConfig
		quotaCfg := quota.CostQuotaConfig{
			DailyCostLimit:   tenantQuota.DailyCostLimit,
			MonthlyCostLimit: tenantQuota.MonthlyCostLimit,
		}

		// Parse flexible windows if provided
		// Windows format: [{"duration": "1h", "limit": 100.0}, ...]
		for _, window := range tenantQuota.Windows {
			if durationStr, ok := window["duration"].(string); ok {
				duration, err := time.ParseDuration(durationStr)
				if err != nil {
					log.Printf("Warning: invalid window duration for tenant %s: %s", tenantID, durationStr)
					continue
				}

				limitVal := window["limit"]
				limit := 0.0
				switch v := limitVal.(type) {
				case float64:
					limit = v
				case int:
					limit = float64(v)
				}

				windowName, _ := window["name"].(string)
				if windowName == "" {
					windowName = durationStr
				}

				quotaCfg.Windows = append(quotaCfg.Windows, quota.QuotaWindow{
					Duration: duration,
					Name:     windowName,
					Limit:    limit,
				})
			}
		}

		// Register the quota with the manager
		fm.CostQuotaManager.RegisterQuota(tenantID, quotaCfg)
		quotaCount++

		if quotaCfg.DailyCostLimit > 0 || quotaCfg.MonthlyCostLimit > 0 || len(quotaCfg.Windows) > 0 {
			log.Printf("Loaded quota for tenant %s: daily=%.2f, monthly=%.2f, windows=%d",
				tenantID, quotaCfg.DailyCostLimit, quotaCfg.MonthlyCostLimit, len(quotaCfg.Windows))
		}
	}
	if quotaCount == 0 {
		log.Printf("No tenant quotas configured (all tenants unlimited)")
	} else {
		log.Printf("Cost quotas initialized: %d tenants", quotaCount)
	}
	// 2c. Wire ingest eventing into all datastore domains.
	// Every successful Put/Delete/MultiPut emits a KindDBPut or KindDBDelete event.
	// The instance fingerprint is stamped as Model so consumers can skip events
	// emitted by this instance (prevents write-emit-consume-write cascade loops).
	instanceFingerprint := fm.TxIDGen.Fingerprint()
	if ingestPipeline != nil {
		dataStoreMgr.SetStoreWrapper(func(domain config.DataDomain, store datastore.KeyValueStore) datastore.KeyValueStore {
			return datastore.WrapWithEventing(store, ingestPipeline, string(domain), instanceFingerprint)
		})
		log.Printf("[ingest] datastore eventing enabled for all domains")
	}

	// 3. Setup compiler and routes
	log.Printf("rah-gateway started | instance=%s port=%d", fm.TxIDGen.Fingerprint(), *port)
	compiler := control.NewCompiler(fm)
	compiler.SecretsMgr = secretsMgr

	// gRPC — create registry and conn pool before Bootstrap so that grpc_call
	// steps can resolve method descriptors at bake time (compiler.GrpcRegistry)
	// and make live calls at runtime (GlobalGrpcConnPool).
	grpcRegistry := grpcutil.NewDescriptorRegistry()
	enginesteps.GlobalGrpcRegistry = grpcRegistry
	compiler.GrpcRegistry = grpcRegistry

	grpcPool := grpcutil.NewConnPool()
	if grpcCfg := cfgMgr.Gateway().Grpc; grpcCfg != nil {
		if grpcCfg.KeepaliveTimeSec > 0 {
			grpcPool.KeepaliveTime = time.Duration(grpcCfg.KeepaliveTimeSec) * time.Second
		}
		if grpcCfg.KeepaliveTimeoutSec > 0 {
			grpcPool.KeepaliveTimeout = time.Duration(grpcCfg.KeepaliveTimeoutSec) * time.Second
		}
	}
	enginesteps.GlobalGrpcConnPool = grpcPool
	defer grpcPool.Close()
	log.Printf("[grpc] registry and connection pool initialized")

	// Pricing manager — bootstraps from hardcoded defaults, then merges config overrides.
	// Enables calculate_cost steps in flows. Runs a background hourly TTL refresh.
	pricingMgr := pricing.NewPricingManager()
	if err := pricingMgr.LoadFromConfig(cfgMgr.Gateway().Pricing); err != nil {
		log.Printf("Warning: pricing config load error: %v (using defaults)", err)
	}
	if err := pricingMgr.LoadFromLLMConfig(cfgMgr.Gateway().LLM); err != nil {
		log.Printf("Warning: pricing from llm config error: %v", err)
	}
	compiler.PricingManager = pricingMgr
	log.Printf("Pricing manager ready: %d models in catalog", len(pricingMgr.GetPricing()))

	// EgressManager — optional; enables egress profile resolution at bake time.
	egressMgr := egress.NewEgressManager()
	compiler.EgressMgr = egressMgr
	log.Printf("[egress] manager initialized")

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

		// Wire cache invalidation via the ingest eventing pipeline.
		// Emit side A — Put: every successful in-memory Put emits KindCacheInvalidate
		// so other instances tombstone their stale L1 slab entries.
		// Emit side B — Invalidate: explicit cache deletes also propagate so that
		// instances that never held the key don't need to act, but those that do
		// will remove it.
		// Self-invalidation is prevented by stamping instanceFingerprint as Model
		// and skipping events where Model == this instance's fingerprint on consume.
		if ingestPipeline != nil {
			emitInvalidate := func(tenantID uint16, key []byte) {
				n := ingestPipeline.NumSinksForKind(ingest.KindCacheInvalidate)
				if n == 0 {
					return
				}
				keyCopy := make([]byte, len(key))
				copy(keyCopy, key)
				e := ingest.Event{
					Kind:        ingest.KindCacheInvalidate,
					TenantID:    tenantID,
					TxID:        string(keyCopy),
					Model:       instanceFingerprint,
					TimestampNs: time.Now().UnixNano(),
				}
				ingestPipeline.Emit(e)
			}

			// Subscribe to in-memory Puts.
			cacheMgr.Subscribe(func(ev cache.WriteEvent) {
				emitInvalidate(ev.TenantID, ev.Key)
			})

			// Subscribe to explicit Invalidate calls (e.g. management API cache flush).
			cacheMgr.OnInvalidate = func(tenantID uint16, key []byte) {
				emitInvalidate(tenantID, key)
			}

			// Consume side: read KindCacheInvalidate events from configured sources
			// and remove the entry from this instance's L1 slab.
			// Use InvalidateLocal to avoid re-emitting (which would create a cascade).
			ingest.StartConsumers(gatewayCtx, cfgMgr.Gateway().Ingest, func(e ingest.Event) {
				if e.Kind != ingest.KindCacheInvalidate {
					return
				}
				if e.Model == instanceFingerprint {
					return // skip our own events — we already hold the new value
				}
				_ = cacheMgr.InvalidateLocal(e.TenantID, []byte(e.TxID))
			})
		}
	} else {
		log.Printf("Cache disabled (cache.disabled=true in config)")
	}

	// Distributed rate limiting via Redis — optional; requires DomainRateLimitSync binding.
	// Fails gracefully: if Redis is not configured or unavailable at startup, the gateway
	// falls back to local in-memory counters with no impact on request processing.
	if rlCfg, err := cfgMgr.Gateway().DataStore.ResolveStore(config.DomainRateLimitSync); err == nil {
		rlClient := goredis.NewClient(&goredis.Options{
			Addr:     rlCfg.Connection.Address,
			Password: rlCfg.Connection.Password,
			PoolSize: rlCfg.Connection.PoolSize,
		})
		if rlProvider, err := engine.NewRedisRateLimitProvider(gatewayCtx, rlClient); err == nil {
			fm.RemoteRL = rlProvider
			fm.DistRLPolicy = 2 // STRICT by default; cross-pod enforcement via Redis
			log.Printf("[rate-limit] distributed rate limiting enabled via Redis (%s)", rlCfg.Connection.Address)
			defer rlProvider.Stop()
		} else {
			log.Printf("[rate-limit] Redis rate limit provider init failed: %v — using local counters", err)
			_ = rlClient.Close()
		}
	} else {
		log.Printf("[rate-limit] distributed rate limiting not configured (add 'rate_limit_sync' binding to datastore config) — using local counters")
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

	// 4. The Unified Hot-Path Handler
	go func() {
		handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Always capture start time — needed for access log regardless of obs config.
			reqStart := time.Now()

			// Extract TCP connection accept time injected by ConnContext.
			// For fresh connections: reqStart - acceptTime ≈ TLS + header parse time.
			// For keep-alive connections: acceptTime is old → we suppress the value
			// (any setup time > 3 s is almost certainly a reused connection, not a slow handshake).
			var connSetupMs float64
			if acceptTime, ok := req.Context().Value(connAcceptKey{}).(time.Time); ok && !acceptTime.IsZero() {
				connSetupNs := reqStart.Sub(acceptTime).Nanoseconds()
				if connSetupNs > 0 && connSetupNs < 3_000_000_000 {
					connSetupMs = float64(connSetupNs) / 1e6
				}
			}

			// A. Router Lookup — load current atomic state so dynamically
			// registered APIs (via /sync) are always visible.
			currentState := fm.State.Load()
			apiId := currentState.Router.Lookup(req.URL.Path)
			if apiId == 0 {
				http.NotFound(w, req)
				// No API name or tenant for 404 — pass empty/zero values.
				accessLog.Snapshot(
					"", 0, "", 0,
					"", 0,
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

			isSampledTrace := obs.ShouldTrace()
			shouldStartTrace := isSampledTrace || alwaysTrace5xx
			if shouldStartTrace {
				trace := obs.StartRequest(apiId, ctx.TenantID, req.Method, req.URL.Path)
				ctx.Trace = &trace
				obs.CaptureRequestHeaders(ctx.Trace, req, req.URL.RawQuery)
			}

			ctx.ApiId = apiId
			ctx.SnapshotMetadata(req.Method, req.URL.Path, req.URL.RawQuery)

			// C. Delegate Execution to FlowManager
			processStarted := time.Now()
			fm.ProcessRequest(ctx, req)
			processDuration := time.Since(processStarted)

			// D. Finalize: flush buffered response — client receives data here.
			// If the client disconnected during flow execution, respond with 499
			// (nginx convention: client closed request) and skip normal finalization.
			finalizeStarted := time.Now()
			if atomic.LoadInt32(&ctx.Cancelled) != 0 {
				ctx.ResponseStatus = 499
				ctx.Finalize()
				// Skip AfterResponse hooks — client is gone.
				finalizeDuration := time.Since(finalizeStarted)
				_ = finalizeDuration
				return
			}
			ctx.Finalize()
			finalizeDuration := time.Since(finalizeStarted)

			// clientTotal: captured immediately after the last byte is written —
			// before any post-response work — so it correctly spans:
			//   routing overhead (router lookup, pool.Get, trace init) +
			//   flow execution (fm.ProcessRequest) +
			//   response flush (ctx.Finalize)
			// This is the true client-visible latency and matches the trace timeline total.
			clientTotal := time.Since(reqStart)

			// Compute response transfer time: from first byte to last byte sent to client.
			// Non-zero only when the response was actually written (not buffered-but-not-flushed).
			var transferMs float64
			lastByteSentNs := ctx.Timing.LastByteSentNs
			if lastByteSentNs > 0 && ctx.Timing.FirstByteSentNs > 0 && lastByteSentNs > ctx.Timing.FirstByteSentNs {
				transferMs = float64(lastByteSentNs-ctx.Timing.FirstByteSentNs) / 1e6
			}

			// D2. After-response hooks: run deferred ingest events now that
			// the response is committed. These are non-blocking channel sends
			// so they complete in nanoseconds; no latency impact on the caller.
			afterHooksStarted := time.Now()
			for _, fn := range ctx.AfterResponse {
				fn()
			}
			afterHooksDuration := time.Since(afterHooksStarted)

			// E. Post-response: snapshot for async access log and observability.
			// Client has already received the response — none of this adds latency.
			total := time.Since(reqStart) // full wall time incl. post-response; for internal gateway accounting only
			upstreamNs := atomic.LoadInt64(&ctx.Timing.UpstreamTimeNs)
			upstream := time.Duration(upstreamNs)
			gateway := max(clientTotal-upstream, 0)

			ttfbNs := max(ctx.Timing.FirstByteSentNs-ctx.Timing.StartNs, 0)

			// API name: resolved from registry (populated at sync time).
			// TenantKey: set during request by registry_lookup step; empty for tenant-agnostic APIs.
			apiName := registry.GetNameByID(ctx.ApiId)
			// Build runtime extra KV pairs from log_field steps — allocated post-response,
			// outside the hot path, so the small allocation here is acceptable.
			var runtimeLogFields []observability.KV
			if ctx.ExtraLogCount > 0 {
				runtimeLogFields = make([]observability.KV, 0, ctx.ExtraLogCount)
				for i := 0; i < int(ctx.ExtraLogCount); i++ {
					e := ctx.ExtraLogFields[i]
					if e.Slot < len(ctx.ByteSlots) && len(ctx.ByteSlots[e.Slot]) > 0 {
						runtimeLogFields = append(runtimeLogFields, observability.KV{
							K: e.Name,
							V: string(ctx.ByteSlots[e.Slot]),
						})
					}
				}
			}
			if ctx.CallerKey != "" {
				runtimeLogFields = append(runtimeLogFields,
					observability.KV{K: "caller_key", V: ctx.CallerKey},
					observability.KV{K: "caller_id", V: strconv.FormatUint(uint64(ctx.CallerID), 10)},
				)
			}
			accessLogSnapshotStarted := time.Now()
			accessLog.Snapshot(
				apiName,
				ctx.ApiId,
				ctx.TenantKey,
				ctx.TenantID,
				ctx.CallerKey,
				ctx.CallerID,
				req.Method, req.URL.Path,
				ctx.ResponseStatus,
				clientTotal.Nanoseconds(), gateway.Nanoseconds(), upstreamNs, ttfbNs,
				req.ContentLength, ctx.Timing.ClientBytesSent,
				req,
				runtimeLogFields...,
			)
			accessLogSnapshotDuration := time.Since(accessLogSnapshotStarted)

			shouldPersistTrace := ctx.Trace != nil && (isSampledTrace || (alwaysTrace5xx && ctx.ResponseStatus >= 500))
			var traceForTelemetry *observability.RequestTrace
			if shouldPersistTrace {
				traceForTelemetry = ctx.Trace
			}

			obsFinishStarted := time.Now()
			obs.FinishRequest(traceForTelemetry, ctx.ResponseStatus, total, gateway, upstream,
				int(atomic.LoadInt32(&ctx.Timing.UpstreamCalls)),
				ctx.Timing.ClientBytesSent,
				atomic.LoadInt64(&ctx.Timing.UpstreamBytesTx),
				atomic.LoadInt64(&ctx.Timing.UpstreamBytesRx),
			)
			obsFinishDuration := time.Since(obsFinishStarted)

			// Record per-API stats for the in-memory API Performance fallback.
			// Post-response: client already has the response, not on the critical path.
			if ctx.ApiId != 0 {
				obs.RecordRequest(ctx.ApiId, total.Nanoseconds(), req.ContentLength, ctx.Timing.ClientBytesSent)
			}

			// Write to persistent observability store (async, non-blocking via ObsWriter buffer).
			accessLogEnqueueStarted := time.Now()
			obsWriter.WriteAccessLog(observability.AccessLogRecord{
				TimestampNs: time.Now().UnixNano(),
				ApiName:     apiName,
				TenantID:    ctx.TenantID,
				TenantKey:   ctx.TenantKey,
				Method:      req.Method,
				Path:        req.URL.Path,
				Status:      ctx.ResponseStatus,
				TotalMs:     float64(clientTotal.Nanoseconds()) / 1e6,
				GatewayMs:   float64(gateway.Nanoseconds()) / 1e6,
				UpstreamMs:  float64(upstreamNs) / 1e6,
				TTFBMs:      float64(ttfbNs) / 1e6,
				ConnSetupMs: connSetupMs,
				TransferMs:  transferMs,
				ReqBytes:    req.ContentLength,
				ResBytes:    ctx.Timing.ClientBytesSent,
			})
			accessLogEnqueueDuration := time.Since(accessLogEnqueueStarted)

			if shouldPersistTrace {
				appendGatewayPhase := func(name string, d time.Duration, note string) {
					if d <= 0 {
						return
					}
					ev := observability.InstructionEvent{Name: name, PC: -1, DurationNs: d.Nanoseconds()}
					if note != "" {
						ev.Output = []observability.KV{{K: "note", V: note}}
					}
					ctx.Obs.AppendInstructionEvent(ctx.Trace, ev)
				}

				// Connection setup: TCP+TLS time before this request started (fresh connections only).
				if connSetupMs > 0 {
					connSetupDuration := time.Duration(connSetupMs * float64(time.Millisecond))
					appendGatewayPhase("GATEWAY_PHASE_CONN_SETUP", connSetupDuration, "TCP+TLS connection setup (fresh connections only; 0 for keep-alive)")
				}

				// Pre-execution setup: router lookup + context pool.Get + trace init.
				// Represents the gateway overhead before the first flow instruction ran.
				routingDuration := processStarted.Sub(reqStart)
				appendGatewayPhase("GATEWAY_PHASE_ROUTING", routingDuration, "router lookup + context setup before first flow step")

				appendGatewayPhase("GATEWAY_PHASE_PROCESS_REQUEST", processDuration, "time in flow manager request execution")
				appendGatewayPhase("GATEWAY_PHASE_FINALIZE_RESPONSE", finalizeDuration, "time flushing buffered response to client")
				if transferMs > 0 {
					transferDuration := time.Duration(transferMs * float64(time.Millisecond))
					appendGatewayPhase("GATEWAY_PHASE_RESPONSE_TRANSFER", transferDuration, "time from first→last byte written to client")
				}
				appendGatewayPhase("GATEWAY_PHASE_AFTER_RESPONSE_HOOKS", afterHooksDuration, "time running deferred hooks after client response")
				appendGatewayPhase("GATEWAY_PHASE_ACCESS_LOG_SNAPSHOT", accessLogSnapshotDuration, "time building in-memory access log snapshot")
				appendGatewayPhase("GATEWAY_PHASE_TELEMETRY_FINISH", obsFinishDuration, "time updating telemetry counters/export queue")
				appendGatewayPhase("GATEWAY_PHASE_ACCESS_LOG_ENQUEUE", accessLogEnqueueDuration, "time enqueueing persistent access log write")

				// RESIDUAL: flow manager time not attributed to any individual instruction.
				// Scoped to processDuration (not clientTotal) so routing and finalize don't
				// inflate or deflate it. Gateway phase events use PC=-1; real instructions
				// use PC>=0 — use that to distinguish without importing "strings".
				var flowAttributedNs int64
				for _, e := range ctx.Trace.Instructions {
					if e.DurationNs > 0 && e.PC >= 0 {
						flowAttributedNs += e.DurationNs
					}
				}
				if residualNs := processDuration.Nanoseconds() - flowAttributedNs; residualNs > 0 {
					ctx.Trace.Instructions = append(ctx.Trace.Instructions, observability.InstructionEvent{
						Name:       "GATEWAY_PHASE_RESIDUAL",
						PC:         -1,
						DurationNs: residualNs,
						Output:     []observability.KV{{K: "note", V: "flow manager overhead not captured by individual instruction events"}},
					})
				}

				payload, err := json.Marshal(ctx.Trace)
				if err != nil {
					log.Printf("[obs] marshal trace payload failed trace_id=%d: %v", ctx.Trace.Summary.TraceID, err)
				} else {
					obsWriter.WriteTrace(gatewayCtx, observability.TraceRecord{
						TraceID:   ctx.Trace.Summary.TraceID,
						Timestamp: time.Now().Unix(),
						ApiName:   apiName,
						TenantID:  ctx.TenantID,
						Status:    ctx.ResponseStatus,
						TotalMs:   float64(clientTotal.Nanoseconds()) / 1e6,
						Payload:   payload,
					})
				}
			}

			if ctx.ShouldReturnToPool() {
				// ReturnContext releases borrowed arena blocks and slot extensions
				// back to their pools before returning the context itself.
				fm.ReturnContext(ctx)
			}
		})

		addr := fmt.Sprintf(":%d", *port)
		log.Printf("Rah Gateway listening on %s\n", addr)
		var gwHandler http.Handler = handler
		if cfgMgr.Gateway().Admin.RequireGatewayAuth {
			gwHandler = adminUserStore.Middleware(handler)
		}
		// Use http.Server with ConnContext to capture TCP accept time for connection
		// setup latency tracking. Zero overhead on the hot path — runs once per TCP
		// connection (not per request) and stores one time.Time in the context.
		srv := &http.Server{
			Addr:           addr,
			Handler:        gwHandler,
			MaxHeaderBytes: cfgMgr.Layout().DefaultLimits.MaxHeaderSize,
			ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
				return context.WithValue(ctx, connAcceptKey{}, time.Now())
			},
		}
		// Optional TLS listener — started only when gateway.yaml has a tls: section
		// with cert_file + key_file. Plain HTTP listener above is never disabled;
		// customer opts in by configuring tls: and routing traffic accordingly.
		if tlsCfg := cfgMgr.Gateway().TLS; tlsCfg != nil && tlsCfg.CertFile != "" && tlsCfg.KeyFile != "" {
			go func() {
				tlsPort := tlsCfg.Port
				if tlsPort == 0 {
					tlsPort = 8443
				}
				tlsAddr := fmt.Sprintf(":%d", tlsPort)
				log.Printf("Rah Gateway (TLS) listening on %s\n", tlsAddr)
				var tlsHandler http.Handler = handler
				if cfgMgr.Gateway().Admin.RequireGatewayAuth {
					tlsHandler = adminUserStore.Middleware(handler)
				}
				tlsSrv := &http.Server{
					Addr:           tlsAddr,
					Handler:        tlsHandler,
					MaxHeaderBytes: cfgMgr.Layout().DefaultLimits.MaxHeaderSize,
					ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
						return context.WithValue(ctx, connAcceptKey{}, time.Now())
					},
				}
				log.Fatal(tlsSrv.ListenAndServeTLS(tlsCfg.CertFile, tlsCfg.KeyFile))
			}()
		}

		log.Fatal(srv.ListenAndServe())
	}()

	ts := tenantregistry.NewTenantServer(regMgr)
	aks := apikey.NewServer(dataStoreMgr)

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

		// Wire cross-instance registry sync via the ingest consumer.
		// When another instance writes a tenant or rate-limit config, it emits
		// KindDBPut/KindDBDelete with SessionID == "tenant_registry". We apply
		// those changes to the local in-memory registry WITHOUT re-persisting
		// (which would emit another event and create a cascade loop).
		// The self-guard is handled by the instance fingerprint in Model — we
		// skip events that we emitted ourselves.
		if ingestPipeline != nil {
			const registryDomain = string(config.DomainTenantRegistry)
			// Scoped key prefix produced by EventingStore/BuildScopedKey.
			// Format: tenant:__global__:tenant_registry:{rawKey}
			scopedPrefix := "tenant:" + string(control.GlobalTenant) + ":" + registryDomain + ":"
			ingest.StartConsumers(gatewayCtx, cfgMgr.Gateway().Ingest, func(e ingest.Event) {
				if e.Model == instanceFingerprint {
					return // skip events we emitted
				}
				rawKey := e.TxID
				if len(rawKey) <= len(scopedPrefix) || rawKey[:len(scopedPrefix)] != scopedPrefix {
					return // not a registry domain event
				}
				rawKey = rawKey[len(scopedPrefix):]
				switch e.Kind {
				case ingest.KindDBPut:
					if e.SessionID != registryDomain {
						return
					}
					regMgr.ApplyStorePut(rawKey, e.Payload())
				case ingest.KindDBDelete:
					if e.SessionID != registryDomain {
						return
					}
					regMgr.ApplyStoreDelete(rawKey)
				}
			})
			log.Printf("[Registry] cross-instance sync consumer started")
		}
	}

	if err := apikey.RestoreFromStore(dataStoreMgr); err != nil {
		log.Printf("[apikey] warn: restore failed: %v", err)
	}

	ms := control.NewManagementServer(fm, compiler, registry, regMgr)
	// Wire the LLM catalog provider so the compiler always sees models registered
	// via the UI (stored in Postgres) rather than only the gateway.yaml snapshot.
	ms.LLMProvider = func() config.LLMConfig { return cfgMgr.LLM() }

	// Load persisted LLM models (and MCP servers) into cfgMgr BEFORE bootstrap
	// so that flows referencing UI-registered models (e.g. classify_llm) compile
	// successfully. Without this, bootstrap sees an empty LLM catalog and fails
	// on any flow that uses a model added via the Studio UI.
	if err := control.LoadPersistedAIConfig(cfgMgr, dataStoreMgr); err != nil {
		log.Printf("[AI] failed to pre-load persisted ai_config before bootstrap: %v", err)
	}

	// Load stored gRPC FileDescriptorSets into the registry BEFORE Bootstrap so
	// that grpc_call steps in existing flows can resolve method descriptors at
	// bake time. A missing or corrupt .pb file is logged but does not abort startup.
	if err := control.BootstrapGrpcDescriptors(dataStoreMgr, grpcRegistry); err != nil {
		log.Printf("[grpc] descriptor bootstrap: %v", err)
	}

	// Bootstrap BEFORE SetDataStore so the bootstrap reads do not trigger
	// redundant writes back to the store. Registry must be restored first (above)
	// so named rate limit configs are available when Bootstrap bakes flows.
	if err := ms.Bootstrap(bootstrapCtx, dataStoreMgr); err != nil {
		log.Printf("failed control-plane bootstrap from datastore: %v", err)
	}

	// Bootstrap egress profiles and rules from datastore (if configured).
	if err := control.BootstrapEgress(bootstrapCtx, dataStoreMgr, egressMgr); err != nil {
		log.Printf("[egress] bootstrap failed: %v", err)
	}

	// Fallback: load from gateway.yaml if egress is defined there.
	if gatewayCfg := cfgMgr.Gateway(); gatewayCfg.Egress != nil {
		if err := egressMgr.Update(gatewayCfg.Egress); err != nil {
			log.Printf("[egress] config load failed: %v", err)
		} else {
			log.Printf("[egress] loaded from gateway config")
		}
	}

	ms.SetDataStore(dataStoreMgr)

	// Cross-instance flow/API sync: when another gateway instance writes to
	// DomainFlows or DomainAPIDefinitions, re-bootstrap this instance from
	// the datastore so all pods converge to the same compiled state.
	if ingestPipeline != nil {
		flowsDomain := string(config.DomainFlows)
		apisDomain := string(config.DomainAPIDefinitions)
		ingest.StartConsumers(gatewayCtx, cfgMgr.Gateway().Ingest, func(e ingest.Event) {
			if e.Model == instanceFingerprint {
				return // skip our own writes
			}
			if e.Kind != ingest.KindDBPut {
				return
			}
			if e.SessionID != flowsDomain && e.SessionID != apisDomain {
				return
			}
			log.Printf("[sync] cross-instance config change detected (domain=%s), re-bootstrapping", e.SessionID)
			if err := ms.Bootstrap(gatewayCtx, dataStoreMgr); err != nil {
				log.Printf("[sync] re-bootstrap failed: %v", err)
			}
		})
	}

	// Start instance heartbeat and config-poll goroutines.
	instanceSync := control.NewInstanceSync(
		instanceFingerprint,
		cfgMgr.Gateway().Instance,
		dataStoreMgr,
		ms,
	)
	instanceSync.Start(gatewayCtx)
	// Sweep stale test tenants (safety net for crash-interrupted test runs).
	// Any test tenant older than 10 minutes that wasn't cleaned up by defer is removed.
	regMgr.StartTestTenantSweep(gatewayCtx, 600)

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
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/sync", ms.UnifiedSyncHandler)
	mux.HandleFunc("/sync/draft/status", ms.DraftStatusHandler)
	mux.HandleFunc("/getAllApis", ms.GetAllApisHandler)
	mux.HandleFunc("/meta/steps", ms.StepsMetaHandler)
	mux.HandleFunc("/flows/", ms.FlowProfileHandler)
	ts.RegisterHandlers(mux)
	aks.RegisterHandlers(mux)
	if cacheMgr != nil {
		control.RegisterCacheRoutes(mux, cacheMgr)
	}
	mux.HandleFunc("/debug/observability", obs.DebugHandler)
	observability.RegisterObsRoutes(mux, obsWriter, obs, registry.GetNameByID)
	if obsCfg.Export.Prometheus.Enabled {
		mux.Handle("/metrics", observability.PrometheusHandler(obs))
	}
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
	mux.HandleFunc("/admin/jwks/flush", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		issuer := r.URL.Query().Get("issuer")
		// trim leading/trailing whitespace inline — no strings import needed
		for len(issuer) > 0 && (issuer[0] == ' ' || issuer[0] == '\t') {
			issuer = issuer[1:]
		}
		for len(issuer) > 0 && (issuer[len(issuer)-1] == ' ' || issuer[len(issuer)-1] == '\t') {
			issuer = issuer[:len(issuer)-1]
		}
		if issuer != "" {
			enginesteps.FlushJWKSCache(issuer)
			fmt.Fprintf(w, `{"flushed":%q}`, issuer)
		} else {
			enginesteps.FlushAllJWKSCaches()
			fmt.Fprint(w, `{"flushed":"all"}`)
		}
	})
	if credReg != nil {
		control.NewCredentialHandler(credReg).RegisterHandlers(mux)
	}

	// Egress profile and rule management endpoints.
	control.RegisterEgressRoutes(mux, dataStoreMgr, egressMgr)

	// Validation schema CRUD endpoints.
	control.RegisterSchemaRoutes(mux, dataStoreMgr)

	// gRPC FileDescriptorSet CRUD endpoints.
	control.RegisterGrpcRoutes(mux, grpcRegistry, dataStoreMgr)

	// ─── Cost Tracking API Routes ───────────────────────────────────────
	// Requires admin token in X-Admin-Token header or Authorization: Bearer
	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		log.Printf("Warning: ADMIN_TOKEN environment variable not set. Cost tracking endpoints will deny all access.")
	}
	RegisterCostRoutes(mux, fm.CostQuotaManager, adminToken)
	log.Printf("Cost tracking endpoints registered at /api/v1/costs*")

	pricing.RegisterPricingRoutes(mux, pricingMgr)
	log.Printf("Pricing endpoints registered at /pricing and /pricing/refresh")

	control.RegisterEnvironmentRoutes(mux, dataStoreMgr)
	control.RegisterReleaseRoutes(mux, dataStoreMgr, instanceSync)
	control.RegisterTestRoutes(mux, dataStoreMgr, ms, fm, cacheMgr)

	mcpReg := mcpreg.NewRegistry()
	compiler.MCPRegistry = mcpReg
	compiler.GatewayBase = fmt.Sprintf("http://localhost:%d", *port)
	control.RegisterAIRoutes(mux, cfgMgr, func() {
		go func() {
			if err := ms.Bootstrap(bootstrapCtx, dataStoreMgr); err != nil {
				log.Printf("[AI] rebake failed: %v", err)
			}
		}()
	}, dataStoreMgr, mcpReg, secretsMgr)
	log.Printf("MCPReg initialized; virtual MCP server routes available at /ai/mcp/virtual")

	control.RegisterAdminUserRoutes(mux, adminUserStore)
	log.Printf("Admin user endpoints registered at /admin/users")

	control.RegisterAnthropicAdapter(mux, fmt.Sprintf("http://localhost:%d", *port))
	log.Printf("Anthropic adapter registered at /ai/v1/messages (set ANTHROPIC_BASE_URL=http://localhost:%d/ai)", *mPort)

	log.Printf("Management API running on %d", *mPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *mPort), adminUserStore.Middleware(mux)))
}

