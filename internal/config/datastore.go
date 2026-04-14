package config

import (
	"context"
	"fmt"
	"slices"
)

// CredentialResolver resolves secret references at startup.
// *secrets.Manager satisfies this interface.
type CredentialResolver interface {
	ResolveString(ctx context.Context, ref string) (string, error)
}

// StoreKind identifies the backend technology selected by customer.
type StoreKind string

const (
	StoreDisk       StoreKind = "disk"
	StoreRedis      StoreKind = "redis"
	StoreMongoDB    StoreKind = "mongodb"
	StoreDragonFly  StoreKind = "dragonfly"
	StorePostgreSQL StoreKind = "postgresql"
	StoreCassandra  StoreKind = "cassandra"
)

var supportedStoreKinds = map[StoreKind]struct{}{
	StoreDisk:       {},
	StoreRedis:      {},
	StoreMongoDB:    {},
	StoreDragonFly:  {},
	StorePostgreSQL: {},
	StoreCassandra:  {},
}

// DataDomain identifies logical data categories in gateway.
type DataDomain string

const (
	// Immutable / control-plane data — typically disk or object storage
	DomainAPIDefinitions DataDomain = "api_definitions" // compiled API + path definitions
	DomainFlows          DataDomain = "flows"           // instruction sets (compiled flows)

	// Mutable / management-plane data — typically fast KV or relational
	DomainTenantRegistry DataDomain = "tenant_data"   // aliases, service URLs, identifiers, metadata
	DomainRateLimit      DataDomain = "rate_limit"     // named rate limit configs + counters
	DomainCustomerData   DataDomain = "customer_data"  // arbitrary per-tenant custom data

	// Hot-path / ephemeral — typically in-memory or Redis
	DomainCache DataDomain = "cache" // request-level response cache

	// Infrastructure / operational
	DomainInstances    DataDomain = "instances"    // live gateway instance registry
	DomainCredentials  DataDomain = "credentials"  // named credential mappings (CredentialRegistry)
	DomainAsyncJobs    DataDomain = "async_jobs"   // async job state (status + results)
	DomainAIConfig     DataDomain = "ai_config"    // runtime LLM model catalog + MCP server registry
	DomainMCPTools     DataDomain = "mcp_tools"   // virtual MCP server definitions + API tool catalog

	// Deployment infrastructure domains
	DomainEnvironments     DataDomain = "environments"      // environment definitions (dev/qa/prod)
	DomainReleases         DataDomain = "releases"          // release bundles (named sets of API+flow versions)
	DomainConfigVersions   DataDomain = "config_versions"   // versioned compiled configs per environment
	DomainGatewayInstances DataDomain = "gateway_instances" // live instance heartbeats + current version
	DomainDeployHistory    DataDomain = "deploy_history"    // deployment audit log

	// Test execution domains
	DomainTestCases DataDomain = "test_cases" // customer-defined API test cases
	DomainTestRuns  DataDomain = "test_runs"  // test execution results

	// Observability domains — optional; skip gracefully if not bound
	DomainObsAccessLog DataDomain = "obs_access_log" // per-request access log entries
	DomainObsMetrics   DataDomain = "obs_metrics"     // pre-aggregated metric snapshots
	DomainObsTraces    DataDomain = "obs_traces"       // sampled/error request traces

	// Distributed rate limiting — optional; skip gracefully if not bound
	DomainRateLimitSync DataDomain = "rate_limit_sync" // Redis-backed cross-pod rate limit counters

	// Admin users and roles — optional; skip gracefully if not bound
	DomainAdminUsers DataDomain = "admin_users" // admin user records (bcrypt hashes + roles)
	DomainAdminRoles DataDomain = "admin_roles" // admin role definitions (name + permissions)
)

var requiredDomains = []DataDomain{
	DomainAPIDefinitions,
	DomainFlows,
	DomainTenantRegistry,
	DomainCache,
}

// StoreConnection captures connection details across all backend kinds.
// Redis/Dragonfly-specific fields (Topology, SentinelMaster, ClusterAddrs, PoolSize)
// are ignored by all other store implementations.
//
// Credential resolution order (highest precedence first):
//
//  1. UsernameRef / PasswordRef — resolved via the secrets manager at startup
//     (supports env:, enc:, vault:, awssm:, gsm: schemes and plain literals).
//  2. Username / Password — inline plaintext (fine for dev; avoid in production).
//
// Connection address resolution order:
//
//  1. Address — full DSN (postgres://…), host:port, or URI — used as-is.
//  2. Host + Port — if Address is empty, these are combined into host:port.
type StoreConnection struct {
	Address  string            `json:"address,omitempty"  yaml:"address,omitempty"`  // full DSN, host:port, or URI
	Host     string            `json:"host,omitempty"     yaml:"host,omitempty"`     // explicit host (used when Address is empty)
	Port     int               `json:"port,omitempty"     yaml:"port,omitempty"`     // explicit port (used when Address is empty)
	Path     string            `json:"path,omitempty"     yaml:"path,omitempty"`     // local path for disk-based stores
	Database string            `json:"database,omitempty" yaml:"database,omitempty"` // logical DB/keyspace name or index
	Username string            `json:"username,omitempty" yaml:"username,omitempty"` // inline plaintext username
	Password string            `json:"password,omitempty" yaml:"password,omitempty"` // inline plaintext password

	// UsernameRef and PasswordRef accept any reference supported by the secrets
	// manager: "env:MY_VAR", "$MY_VAR", "enc:base64...", "vault://...", etc.
	// When set, the resolved value overwrites Username/Password before the store
	// connection is opened. Plain literals (no scheme prefix) are passed through.
	UsernameRef string `json:"username_ref,omitempty" yaml:"username_ref,omitempty"`
	PasswordRef string `json:"password_ref,omitempty" yaml:"password_ref,omitempty"`

	Params map[string]string `json:"params,omitempty" yaml:"params,omitempty"` // backend-specific overflow options

	// Redis / Dragonfly topology
	Topology       string   `json:"topology,omitempty"        yaml:"topology,omitempty"`         // single (default) | sentinel | cluster
	SentinelMaster string   `json:"sentinel_master,omitempty" yaml:"sentinel_master,omitempty"`  // required for sentinel topology
	ClusterAddrs   []string `json:"cluster_addrs,omitempty"   yaml:"cluster_addrs,omitempty"`   // seed nodes for cluster/sentinel
	PoolSize       int      `json:"pool_size,omitempty"       yaml:"pool_size,omitempty"`        // connection pool size (default 32)

	// MaxCmdsPerPipeline caps the number of Redis commands batched in one pipeline exec.
	// 0 = use executor default (256).
	MaxCmdsPerPipeline int `json:"max_cmds_per_pipeline,omitempty" yaml:"max_cmds_per_pipeline,omitempty"`

	// MaxBytesPerPipeline caps the estimated payload size of one pipeline exec (bytes).
	// PUT ops: key+value bytes counted. GET ops: key bytes only.
	// 0 = use executor default (1MB).
	MaxBytesPerPipeline int64 `json:"max_bytes_per_pipeline,omitempty" yaml:"max_bytes_per_pipeline,omitempty"`

	// IODedupWindow is the in-process I/O deduplication TTL for Redis/Dragonfly stores.
	// It coalesces burst reads (same key, many goroutines) and suppresses identical writes
	// (same key+value already in Redis). Set "" to disable (default).
	// Use a Go duration string: "200ms", "500ms", "1s".
	// Recommended: 200ms–1s. Do not use "0" — that means infinite in Redis convention.
	IODedupWindow string `json:"io_dedup_window,omitempty" yaml:"io_dedup_window,omitempty"`

	// MaxRetries is the maximum number of retries on transient network/connection errors
	// (e.g. Redis momentarily unreachable). Applied per Redis command.
	// 0 = go-redis default (3 retries). -1 = disabled (no retries).
	MaxRetries int `json:"max_retries,omitempty" yaml:"max_retries,omitempty"`

	// MinRetryBackoff is the minimum sleep between retries.
	// "" or "0" = go-redis default (8ms). "-1" = no backoff (retry immediately).
	// Use a Go duration string: "8ms", "50ms".
	MinRetryBackoff string `json:"min_retry_backoff,omitempty" yaml:"min_retry_backoff,omitempty"`

	// MaxRetryBackoff is the maximum sleep between retries (exponential cap).
	// "" or "0" = go-redis default (512ms). "-1" = no backoff (retry immediately).
	// Use a Go duration string: "100ms", "512ms".
	MaxRetryBackoff string `json:"max_retry_backoff,omitempty" yaml:"max_retry_backoff,omitempty"`
}

// ResolveCredentials resolves UsernameRef and PasswordRef via the supplied
// secret resolver and writes the results into Username and Password.
// Call this at startup before opening any store connections.
// If r is nil, or the Ref fields are empty, this is a no-op.
func (c *StoreConnection) ResolveCredentials(ctx context.Context, r CredentialResolver) error {
	if r == nil {
		return nil
	}
	if c.UsernameRef != "" {
		val, err := r.ResolveString(ctx, c.UsernameRef)
		if err != nil {
			return fmt.Errorf("resolve username_ref %q: %w", c.UsernameRef, err)
		}
		c.Username = val
	}
	if c.PasswordRef != "" {
		val, err := r.ResolveString(ctx, c.PasswordRef)
		if err != nil {
			return fmt.Errorf("resolve password_ref %q: %w", c.PasswordRef, err)
		}
		c.Password = val
	}
	return nil
}

// EffectiveAddress returns the address to use for the store connection.
// If Address is set, it is returned unchanged.
// Otherwise Host and Port are combined as "host:port" (or just "host" if Port is 0).
func (c *StoreConnection) EffectiveAddress() string {
	if c.Address != "" {
		return c.Address
	}
	if c.Host != "" && c.Port > 0 {
		return fmt.Sprintf("%s:%d", c.Host, c.Port)
	}
	return c.Host
}

// StoreConfig defines a single named backend instance.
type StoreConfig struct {
	Name       string          `json:"name"       yaml:"name"`
	Kind       StoreKind       `json:"kind"       yaml:"kind"`
	Enabled    bool            `json:"enabled"    yaml:"enabled"`
	Connection StoreConnection `json:"connection" yaml:"connection"`
}

// DataStoreConfig wires logical data domains to concrete store instances.
type DataStoreConfig struct {
	Stores   map[string]StoreConfig `json:"stores"   yaml:"stores"`
	Bindings map[DataDomain]string  `json:"bindings" yaml:"bindings"`
}

func ErrDomainNotConfigured(domain DataDomain) error {
	return fmt.Errorf("domain %q is not configured", domain)
}

// Validate checks that every required domain resolves to an enabled and supported store.
func (c DataStoreConfig) Validate() error {
	if len(c.Stores) == 0 {
		return fmt.Errorf("at least one data store must be configured")
	}

	for name, cfg := range c.Stores {
		if cfg.Name == "" {
			cfg.Name = name
		}
		if cfg.Name != name {
			return fmt.Errorf("store key %q must match store name %q", name, cfg.Name)
		}
		if _, ok := supportedStoreKinds[cfg.Kind]; !ok {
			return fmt.Errorf("store %q has unsupported kind %q", name, cfg.Kind)
		}
	}

	for _, domain := range requiredDomains {
		if _, ok := c.Bindings[domain]; !ok {
			return fmt.Errorf("required binding %q is missing", domain)
		}
	}

	for domain, storeName := range c.Bindings {
		cfg, ok := c.Stores[storeName]
		if !ok {
			return fmt.Errorf("binding %q references unknown store %q", domain, storeName)
		}
		if !cfg.Enabled {
			return fmt.Errorf("binding %q references disabled store %q", domain, storeName)
		}
	}

	return nil
}

// ResolveStore returns the selected store for a logical domain.
func (c DataStoreConfig) ResolveStore(domain DataDomain) (StoreConfig, error) {
	name, ok := c.Bindings[domain]
	if !ok {
		return StoreConfig{}, fmt.Errorf("binding for %q not configured", domain)
	}
	cfg, ok := c.Stores[name]
	if !ok {
		return StoreConfig{}, fmt.Errorf("store %q for %q not found", name, domain)
	}
	if !cfg.Enabled {
		return StoreConfig{}, fmt.Errorf("store %q for %q is disabled", name, domain)
	}
	return cfg, nil
}

// SupportedStoreKinds returns all supported store types sorted for stable logs/APIs.
func SupportedStoreKinds() []StoreKind {
	kinds := make([]StoreKind, 0, len(supportedStoreKinds))
	for kind := range supportedStoreKinds {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}
