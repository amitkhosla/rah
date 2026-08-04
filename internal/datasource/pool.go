package datasource

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// DataSourcePool holds named pgx connection pools.
type DataSourcePool struct {
	pools   map[string]*pgxpool.Pool
	configs map[string]DataSourceConfig
	loaders sync.Map // key: "sourceName:queryName" → *BatchLoader
}

// New creates and opens all configured data source pools.
func New(configs []DataSourceConfig) (*DataSourcePool, error) {
	p := &DataSourcePool{
		pools:   make(map[string]*pgxpool.Pool, len(configs)),
		configs: make(map[string]DataSourceConfig, len(configs)),
	}
	for _, cfg := range configs {
		dsn := resolveDSN(cfg.DSNRef)
		if dsn == "" {
			return nil, fmt.Errorf("datasource %q: empty DSN (ref: %s)", cfg.Name, cfg.DSNRef)
		}
		maxConns := cfg.MaxConnections
		if maxConns <= 0 {
			maxConns = 20
		}
		poolCfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			return nil, fmt.Errorf("datasource %q: parse config: %w", cfg.Name, err)
		}
		poolCfg.MaxConns = int32(maxConns)
		pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
		if err != nil {
			return nil, fmt.Errorf("datasource %q: open pool: %w", cfg.Name, err)
		}
		p.pools[cfg.Name] = pool
		p.configs[cfg.Name] = cfg
		gatewaylog.Default.Info("[DataSource] connected", gatewaylog.F("name", cfg.Name))
	}
	return p, nil
}

// Get returns the named pool. Returns nil, false if not found.
func (p *DataSourcePool) Get(name string) (*pgxpool.Pool, bool) {
	pool, ok := p.pools[name]
	return pool, ok
}

// Close closes all pools.
func (p *DataSourcePool) Close() {
	for _, pool := range p.pools {
		pool.Close()
	}
}

// GetBatchLoader returns (or lazily creates) a BatchLoader for a named query.
// cfg.BatchBy must be non-empty; otherwise returns nil.
func (p *DataSourcePool) GetBatchLoader(ctx context.Context, sourceName, queryName string, cfg NamedQueryConfig) *BatchLoader {
	if cfg.BatchBy == "" {
		return nil
	}
	pool, ok := p.pools[sourceName]
	if !ok {
		return nil
	}
	cacheKey := sourceName + ":" + queryName
	if v, loaded := p.loaders.Load(cacheKey); loaded {
		return v.(*BatchLoader)
	}
	bl := NewBatchLoader(ctx, pool, cfg)
	p.loaders.Store(cacheKey, bl)
	return bl
}

// resolveDSN resolves env:VAR references or returns the string as-is.
func resolveDSN(ref string) string {
	if strings.HasPrefix(ref, "env:") {
		return os.Getenv(strings.TrimPrefix(ref, "env:"))
	}
	return ref
}

// QueryTimeout returns the configured query timeout for a DataSourceConfig.
func QueryTimeout(cfg DataSourceConfig) time.Duration {
	if cfg.QueryTimeoutSec <= 0 {
		return 10 * time.Second
	}
	return time.Duration(cfg.QueryTimeoutSec) * time.Second
}

// AcquireIsolated acquires a connection and sets the tenant isolation context
// (search_path for schema mode, SET LOCAL for RLS mode) before calling fn.
// The connection is released after fn returns.
func (p *DataSourcePool) AcquireIsolated(ctx context.Context, name string, tenantID uint16, tenantKey string, fn func(*pgx.Conn) error) error {
	pool, ok := p.pools[name]
	if !ok {
		return fmt.Errorf("data source %q not found", name)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire %q: %w", name, err)
	}
	defer conn.Release()

	cfg := p.configs[name]
	if err := setTenantContext(ctx, conn.Conn(), cfg, tenantID, tenantKey); err != nil {
		return err
	}
	return fn(conn.Conn())
}

func setTenantContext(ctx context.Context, conn *pgx.Conn, cfg DataSourceConfig, tenantID uint16, tenantKey string) error {
	switch cfg.TenantIsolation {
	case "schema":
		var schemaName string
		if cfg.TenantKey == "alias" {
			schemaName = "tenant_" + tenantKey
		} else {
			schemaName = "tenant_" + strconv.FormatUint(uint64(tenantID), 10)
		}
		shared := "public"
		if len(cfg.SharedSchemas) > 0 {
			shared = strings.Join(cfg.SharedSchemas, ",") + ",public"
		}
		_, err := conn.Exec(ctx, "SET search_path = "+schemaName+","+shared)
		return err
	case "rls":
		rlsVar := cfg.RLSVariable
		if rlsVar == "" {
			rlsVar = "app.current_tenant"
		}
		var tenantVal string
		if cfg.TenantKey == "alias" {
			tenantVal = tenantKey
		} else {
			tenantVal = strconv.FormatUint(uint64(tenantID), 10)
		}
		_, err := conn.Exec(ctx, "SET LOCAL "+rlsVar+" = '"+tenantVal+"'")
		return err
	default:
		return nil
	}
}
