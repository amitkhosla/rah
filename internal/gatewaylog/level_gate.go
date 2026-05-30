package gatewaylog

// TenantLeveler provides per-tenant log level and debug state.
// *registry.RegistryManager satisfies this interface.
type TenantLeveler interface {
	// TenantLogLevel returns the configured log level string and debug flag for the tenant.
	// Returns ("", false) if the tenant has no override or is not found.
	TenantLogLevel(tenantID uint16) (level string, debugEnabled bool)
}

// Gate wraps the global Logger and provides per-request log decisions
// based on tenant-level overrides.
type Gate struct {
	base    *Logger
	tenants TenantLeveler // optional; nil = global only
}

// NewGate creates a Gate backed by base, with optional per-tenant level overrides.
// tenants may be nil, in which case all decisions fall through to the base logger.
func NewGate(base *Logger, tenants TenantLeveler) *Gate {
	return &Gate{base: base, tenants: tenants}
}

// ShouldLog returns true if a log at the given level should be emitted
// for the given tenant. tenantID=0 means global context.
// This is zero-allocation: it only does atomic reads and integer comparisons.
func (g *Gate) ShouldLog(level Level, tenantID uint16) bool {
	if tenantID != 0 && g.tenants != nil {
		lvlStr, debugEnabled := g.tenants.TenantLogLevel(tenantID)
		if debugEnabled {
			return true // debug mode: always log
		}
		if lvlStr != "" {
			return level >= ParseLevel(lvlStr)
		}
	}
	return g.base.ShouldLog(level)
}

// Log emits a structured log line if the level passes for the given tenant.
func (g *Gate) Log(level Level, tenantID uint16, msg string, fields ...Field) {
	if !g.ShouldLog(level, tenantID) {
		return
	}
	g.base.log(level, msg, fields...)
}

// Debug emits a DEBUG message using the base logger's global level check.
func (g *Gate) Debug(msg string, fields ...Field) { g.base.Debug(msg, fields...) }

// Info emits an INFO message using the base logger's global level check.
func (g *Gate) Info(msg string, fields ...Field) { g.base.Info(msg, fields...) }

// Warn emits a WARN message using the base logger's global level check.
func (g *Gate) Warn(msg string, fields ...Field) { g.base.Warn(msg, fields...) }

// Error emits an ERROR message using the base logger's global level check.
func (g *Gate) Error(msg string, fields ...Field) { g.base.Error(msg, fields...) }
