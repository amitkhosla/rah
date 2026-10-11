package datacache

import (
	"errors"
	"sync"
	"sync/atomic"
)

var ErrQuotaExceeded = errors.New("datacache: quota exceeded")

// quotaKey identifies a tenant+app combination
type quotaKey struct {
	TenantID uint16
	App      [32]byte // app name, null-padded
}

func makeQuotaKey(tenantID uint16, app string) quotaKey {
	var k quotaKey
	k.TenantID = tenantID
	copy(k.App[:], app)
	return k
}

type tenantDataQuota struct {
	usedBytes atomic.Int64
	usedSlots atomic.Int32
	maxBytes  int64
	maxSlots  int32
}

// QuotaRegistry manages per-tenant data quotas across all apps.
// All operations are safe to call concurrently.
type QuotaRegistry struct {
	mu               sync.RWMutex
	entries          map[quotaKey]*tenantDataQuota
	platformMaxBytes int64
	platformMaxSlots int32
}

// NewQuotaRegistry creates a new quota registry with platform-wide defaults.
// platformMaxBytes and platformMaxSlots are used as defaults for newly created
// tenant quotas.
func NewQuotaRegistry(platformMaxBytes int64, platformMaxSlots int32) *QuotaRegistry {
	return &QuotaRegistry{
		entries:          make(map[quotaKey]*tenantDataQuota),
		platformMaxBytes: platformMaxBytes,
		platformMaxSlots: platformMaxSlots,
	}
}

// Reserve atomically reserves bytes and slots for a tenant+app combination.
// If either quota would be exceeded, no reservation is made and ErrQuotaExceeded
// is returned. Otherwise, nil is returned.
func (qr *QuotaRegistry) Reserve(tenantID uint16, app string, bytes int64, slots int) error {
	key := makeQuotaKey(tenantID, app)

	qr.mu.RLock()
	q, exists := qr.entries[key]
	qr.mu.RUnlock()

	if !exists {
		// Create a new tenant quota with platform defaults.
		qr.mu.Lock()
		// Double-check after acquiring write lock.
		q, exists = qr.entries[key]
		if !exists {
			q = &tenantDataQuota{
				maxBytes: qr.platformMaxBytes,
				maxSlots: qr.platformMaxSlots,
			}
			qr.entries[key] = q
		}
		qr.mu.Unlock()
	}

	// Atomically try to reserve bytes.
	newBytes := q.usedBytes.Add(bytes)
	if newBytes > q.maxBytes {
		// Rollback bytes.
		q.usedBytes.Add(-bytes)
		return ErrQuotaExceeded
	}

	// Atomically try to reserve slots.
	newSlots := q.usedSlots.Add(int32(slots))
	if newSlots > q.maxSlots {
		// Rollback both bytes and slots.
		q.usedBytes.Add(-bytes)
		q.usedSlots.Add(-int32(slots))
		return ErrQuotaExceeded
	}

	return nil
}

// Release atomically releases bytes and slots for a tenant+app combination.
func (qr *QuotaRegistry) Release(tenantID uint16, app string, bytes int64, slots int) {
	key := makeQuotaKey(tenantID, app)

	qr.mu.RLock()
	q, exists := qr.entries[key]
	qr.mu.RUnlock()

	if !exists {
		// No entry: nothing to release. This can occur if a slot expired but
		// the entry was never created (edge case, but safe to ignore).
		return
	}

	q.usedBytes.Add(-bytes)
	q.usedSlots.Add(-int32(slots))
}

// SetTenantLimit updates the quota limits for a specific tenant+app combination.
// Creates the entry if it does not exist.
func (qr *QuotaRegistry) SetTenantLimit(tenantID uint16, app string, maxBytes int64, maxSlots int32) {
	key := makeQuotaKey(tenantID, app)

	qr.mu.Lock()
	defer qr.mu.Unlock()

	q, exists := qr.entries[key]
	if !exists {
		q = &tenantDataQuota{}
		qr.entries[key] = q
	}

	q.maxBytes = maxBytes
	q.maxSlots = maxSlots
}

// UsageBytes returns the current byte usage for a tenant+app combination.
// Returns 0 if no entry exists.
func (qr *QuotaRegistry) UsageBytes(tenantID uint16, app string) int64 {
	key := makeQuotaKey(tenantID, app)

	qr.mu.RLock()
	q, exists := qr.entries[key]
	qr.mu.RUnlock()

	if !exists {
		return 0
	}

	return q.usedBytes.Load()
}

// UsageSlots returns the current slot usage for a tenant+app combination.
// Returns 0 if no entry exists.
func (qr *QuotaRegistry) UsageSlots(tenantID uint16, app string) int32 {
	key := makeQuotaKey(tenantID, app)

	qr.mu.RLock()
	q, exists := qr.entries[key]
	qr.mu.RUnlock()

	if !exists {
		return 0
	}

	return q.usedSlots.Load()
}
