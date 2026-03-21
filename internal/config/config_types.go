package config

type ResourceLimit struct {
	MaxHeaderSize  int   // e.g., 16KB
	MaxHeaderCount int   // e.g., 50
	MaxBodySize    int64 // e.g., 1MB (SME) vs 1GB (Enterprise)
}

// RateLimitPreset defines a named rate limit preset used as a system-wide default.
// IDs are assigned at sync time by the control plane and stored in
// registry.RateLimitConfigs. ID 0 is reserved for the system baseline.
type RateLimitPreset struct {
	ID          uint16 `json:"id"`
	Name        string `json:"name"`
	RatePerSec  uint32 `json:"rate_per_sec,omitempty"`  // max requests/second; 0 = unlimited
	RatePerMin  uint32 `json:"rate_per_min,omitempty"`  // max requests/minute; 0 = unlimited
	BurstFactor uint16 `json:"burst_factor,omitempty"`  // 100 = 1x, 150 = 1.5x, 200 = 2x
}

type GlobalLayout struct {
	MaxBytesSlots      int
	MaxIntsSlots       int
	MaxBoolsSlots      int
	MaxHeapBytes       int64
	DefaultLimits      ResourceLimit
	SlotValueThreshold int               // per-value arena limit in bytes; 0 means use rctx.SlotValueThreshold default (256)
	DefaultRateLimits  []RateLimitPreset // system-wide rate limit defaults loaded at startup
}
