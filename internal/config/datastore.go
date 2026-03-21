package config

import (
	"fmt"
	"sort"
)

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
	DomainInstances DataDomain = "instances" // live gateway instance registry
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
type StoreConnection struct {
	Address  string            `json:"address,omitempty"  yaml:"address,omitempty"`  // host:port, URI, or DSN endpoint
	Path     string            `json:"path,omitempty"     yaml:"path,omitempty"`     // local path for disk-based stores
	Database string            `json:"database,omitempty" yaml:"database,omitempty"` // logical DB/keyspace name or index
	Username string            `json:"username,omitempty" yaml:"username,omitempty"`
	Password string            `json:"password,omitempty" yaml:"password,omitempty"`
	Params   map[string]string `json:"params,omitempty"   yaml:"params,omitempty"` // backend-specific overflow options

	// Redis / Dragonfly topology
	Topology       string   `json:"topology,omitempty"        yaml:"topology,omitempty"`         // single (default) | sentinel | cluster
	SentinelMaster string   `json:"sentinel_master,omitempty" yaml:"sentinel_master,omitempty"`  // required for sentinel topology
	ClusterAddrs   []string `json:"cluster_addrs,omitempty"   yaml:"cluster_addrs,omitempty"`   // seed nodes for cluster/sentinel
	PoolSize       int      `json:"pool_size,omitempty"       yaml:"pool_size,omitempty"`        // connection pool size (default 32)
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
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}
