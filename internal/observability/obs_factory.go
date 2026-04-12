package observability

import (
	"context"
	"log"
	"strings"
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
}

// NewObsStoreFromParams creates an ObsStore according to params.
// Always returns a valid store (falls back to MemObsStore on error).
func NewObsStoreFromParams(ctx context.Context, p ObsStoreParams) ObsStore {
	storeType := strings.ToLower(strings.TrimSpace(p.Type))
	if storeType == "" {
		storeType = "memory"
	}

	switch storeType {
	case "postgres", "postgresql":
		if p.DSN == "" {
			log.Printf("[obs] postgres type requested but DSN is empty — falling back to memory store")
			break
		}
		s, err := NewPostgresObsStore(p.DSN)
		if err != nil {
			log.Printf("[obs] postgres store failed: %v — falling back to memory store", err)
			break
		}
		log.Printf("[obs] using PostgreSQL store")
		return s

	case "redis", "dragonfly":
		if p.DSN == "" {
			log.Printf("[obs] redis type requested but address is empty — falling back to memory store")
			break
		}
		poolSize := p.PoolSize
		if poolSize <= 0 {
			poolSize = 10
		}
		s, err := NewRedisObsStore(ctx, p.DSN, p.Password, poolSize)
		if err != nil {
			log.Printf("[obs] redis store failed: %v — falling back to memory store", err)
			break
		}
		log.Printf("[obs] using Redis store")
		return s

	case "memory":
		// fall through to default below
	default:
		log.Printf("[obs] unknown store type %q — using memory store", p.Type)
	}

	maxAL := p.MaxAccessLog
	if maxAL <= 0 {
		maxAL = 10000
	}
	maxT := p.MaxTraces
	if maxT <= 0 {
		maxT = 500
	}
	log.Printf("[obs] using in-memory store (max_access_log=%d, max_traces=%d)", maxAL, maxT)
	return NewMemObsStore(maxAL, maxT)
}
