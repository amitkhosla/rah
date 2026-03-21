package config

// GatewayConfig is the top-level configuration read from a JSON or YAML file.
// It is the single struct passed to config.Manager and distributed to all
// components via Manager accessors.
type GatewayConfig struct {
	Layout    GlobalLayout    `json:"layout"    yaml:"layout"`
	DataStore DataStoreConfig `json:"datastore" yaml:"datastore"`
}

type ResourceLimit struct {
	MaxHeaderSize  int   `json:"max_header_size,omitempty"  yaml:"max_header_size,omitempty"`  // e.g., 16KB
	MaxHeaderCount int   `json:"max_header_count,omitempty" yaml:"max_header_count,omitempty"` // e.g., 50
	MaxBodySize    int64 `json:"max_body_size,omitempty"    yaml:"max_body_size,omitempty"`    // e.g., 1MB (SME) vs 1GB (Enterprise)
}

// RateLimitPreset defines a named rate limit preset used as a system-wide default.
// IDs are assigned at sync time by the control plane and stored in
// registry.RateLimitConfigs. ID 0 is reserved for the system baseline.
type RateLimitPreset struct {
	ID          uint16 `json:"id"                     yaml:"id"`
	Name        string `json:"name"                   yaml:"name"`
	RatePerSec  uint32 `json:"rate_per_sec,omitempty" yaml:"rate_per_sec,omitempty"` // max requests/second; 0 = unlimited
	RatePerMin  uint32 `json:"rate_per_min,omitempty" yaml:"rate_per_min,omitempty"` // max requests/minute; 0 = unlimited
	BurstFactor uint16 `json:"burst_factor,omitempty" yaml:"burst_factor,omitempty"` // 100 = 1x, 150 = 1.5x, 200 = 2x
}

type GlobalLayout struct {
	MaxBytesSlots      int               `json:"max_bytes_slots,omitempty"      yaml:"max_bytes_slots,omitempty"`
	MaxIntsSlots       int               `json:"max_ints_slots,omitempty"       yaml:"max_ints_slots,omitempty"`
	MaxBoolsSlots      int               `json:"max_bools_slots,omitempty"      yaml:"max_bools_slots,omitempty"`
	MaxHeapBytes       int64             `json:"max_heap_bytes,omitempty"       yaml:"max_heap_bytes,omitempty"`
	DefaultLimits      ResourceLimit     `json:"default_limits,omitempty"       yaml:"default_limits,omitempty"`
	SlotValueThreshold int               `json:"slot_value_threshold,omitempty" yaml:"slot_value_threshold,omitempty"` // per-value arena limit; 0 = rctx default (256)
	DefaultRateLimits  []RateLimitPreset `json:"default_rate_limits,omitempty"  yaml:"default_rate_limits,omitempty"`  // system-wide defaults loaded at startup
}
