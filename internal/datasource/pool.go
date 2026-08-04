package datasource

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// DataSourcePool holds named pgx connection pools.
type DataSourcePool struct {
	pools map[string]*pgxpool.Pool
}

// New creates and opens all configured data source pools.
func New(configs []DataSourceConfig) (*DataSourcePool, error) {
	p := &DataSourcePool{pools: make(map[string]*pgxpool.Pool, len(configs))}
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
