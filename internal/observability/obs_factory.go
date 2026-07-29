package observability

import (
	"context"
	"fmt"
	"strings"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

// ObsStoreParams holds the resolved parameters for creating an ObsStore.
// main.go reads ObservabilityConfig and DataStoreConfig, extracts these
// params, and passes them here. No config package import needed.
type ObsStoreParams struct {
	// Type selects the implementation: "memory" | "postgres" | "redis" | "dragonfly"
	// Empty string defaults to "memory".
	Type string

	// Connection params (used for postgres and redis types)
	DSN      string // postgres: full DSN. redis: "host:port"
	Password string // redis only
	PoolSize int    // redis only; default 10

	// Memory store sizing
	MaxAccessLog int // default 10000
	MaxTraces    int // default 500

	// EnableOTEL wraps the primary store in a FanOutObsStore with an OTELObsStore
	// when true. The OTELObsStore uses the global OTEL TracerProvider (set by main.go
	// after S8 OTEL SDK init). No-op when false.
	EnableOTEL bool
}

// NewObsStoreFromParams creates an ObsStore according to params.
// Always returns a valid store (falls back to MemObsStore on error).
// If EnableOTEL is true, wraps the primary store in a FanOutObsStore with an OTELObsStore.
func NewObsStoreFromParams(ctx context.Context, p ObsStoreParams) ObsStore {
	primary := newPrimaryObsStore(ctx, p)
	if p.EnableOTEL {
		otelStore := newOTELObsStore()
		gatewaylog.Default.Info("obs store otel export enabled")
		return NewFanOutObsStore(primary, otelStore)
	}
	return primary
}

// newPrimaryObsStore creates the primary ObsStore based on params.
func newPrimaryObsStore(ctx context.Context, p ObsStoreParams) ObsStore {
	storeType := strings.ToLower(strings.TrimSpace(p.Type))
	if storeType == "" {
		storeType = "memory"
	}

	switch storeType {
	case "postgres", "postgresql":
		if p.DSN == "" {
			gatewaylog.Default.Warn("obs store fallback", gatewaylog.F("reason", "postgres DSN is empty"), gatewaylog.F("fallback", "memory"))
			break
		}
		s, err := NewPostgresObsStore(p.DSN)
		if err != nil {
			gatewaylog.Default.Warn("obs store fallback", gatewaylog.F("reason", fmt.Sprintf("postgres store failed: %v", err)), gatewaylog.F("fallback", "memory"))
			break
		}
		gatewaylog.Default.Info("obs store", gatewaylog.F("type", "postgres"))
		return s

	case "redis", "dragonfly":
		if p.DSN == "" {
			gatewaylog.Default.Warn("obs store fallback", gatewaylog.F("reason", "redis address is empty"), gatewaylog.F("fallback", "memory"))
			break
		}
		poolSize := p.PoolSize
		if poolSize <= 0 {
			poolSize = 10
		}
		s, err := NewRedisObsStore(ctx, p.DSN, p.Password, poolSize)
		if err != nil {
			gatewaylog.Default.Warn("obs store fallback", gatewaylog.F("reason", fmt.Sprintf("redis store failed: %v", err)), gatewaylog.F("fallback", "memory"))
			break
		}
		gatewaylog.Default.Info("obs store", gatewaylog.F("type", "redis"))
		return s

	case "memory":
		// fall through to default below
	default:
		gatewaylog.Default.Warn("obs store unknown type", gatewaylog.F("type", p.Type), gatewaylog.F("fallback", "memory"))
	}

	maxAL := p.MaxAccessLog
	if maxAL <= 0 {
		maxAL = 10000
	}
	maxT := p.MaxTraces
	if maxT <= 0 {
		maxT = 500
	}
	gatewaylog.Default.Info("obs store", gatewaylog.F("type", "memory"), gatewaylog.Fint("max_access_log", int64(maxAL)), gatewaylog.Fint("max_traces", int64(maxT)))
	return NewMemObsStore(maxAL, maxT)
}
