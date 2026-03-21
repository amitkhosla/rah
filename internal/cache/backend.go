package cache

// CacheBackend is a TTL-aware persistent store used as the spill/warm-up layer
// behind the in-memory slab.
//
// Expiry is a unix32 timestamp (seconds since epoch). Implementations must
// reject expired entries on Get. All methods must be safe for concurrent use.
type CacheBackend interface {
	// Get retrieves a value. Returns (value, expiry, true) on hit;
	// (nil, 0, false) on miss or if the stored entry has expired.
	Get(tenantID uint16, key []byte) (value []byte, expiry uint32, found bool)

	// Set stores value with the given expiry timestamp (unix32).
	// expiry == 0 means the entry never expires.
	Set(tenantID uint16, key []byte, value []byte, expiry uint32) error

	// Delete removes the entry for (tenantID, key). No-op if absent.
	Delete(tenantID uint16, key []byte) error

	// Sweep scans and removes expired entries. Returns the number deleted.
	// Called periodically by a background goroutine; may be slow.
	Sweep() int

	// Close releases any held resources (file handles, connections).
	Close() error
}

// NoopBackend is a CacheBackend that does nothing.
// Use it to disable persistence and run purely in-memory:
//
//	NewCacheManager(..., icache.NoopBackend)
var NoopBackend CacheBackend = noopBackend{}

type noopBackend struct{}

func (noopBackend) Get(_ uint16, _ []byte) ([]byte, uint32, bool) { return nil, 0, false }
func (noopBackend) Set(_ uint16, _ []byte, _ []byte, _ uint32) error { return nil }
func (noopBackend) Delete(_ uint16, _ []byte) error                   { return nil }
func (noopBackend) Sweep() int                                        { return 0 }
func (noopBackend) Close() error                                      { return nil }
