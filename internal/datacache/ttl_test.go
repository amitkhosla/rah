package datacache

import (
	"testing"
	"time"
)

func TestTTLCleaner_Expiry(t *testing.T) {
	pool := NewArenaPool(4, 64<<10, 100)
	quotas := NewQuotaRegistry(1<<30, 10)
	mgr := NewDataCacheManager(pool, quotas)
	defer mgr.Close()

	fields := []FieldDef{{Name: "v", Kind: FieldFloat64, Width: 8}}
	opts := CreateOpts{
		Fields:   fields,
		Capacity: 10,
		TTLSec:   1, // expires in 1 second
	}

	if err := mgr.Create("expiring", opts); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify collection exists
	if _, err := mgr.Get("expiring"); err != nil {
		t.Fatalf("Get before expiry: %v", err)
	}

	// Wait for TTL to expire (TTL=1s, cleaner ticks every 1s, wait 2.5s to be safe)
	time.Sleep(2500 * time.Millisecond)

	// Collection should be gone
	if _, err := mgr.Get("expiring"); err != ErrCollectionNotFound {
		t.Errorf("after TTL expiry, expected ErrCollectionNotFound, got %v", err)
	}
}

func TestTTLCleaner_NoExpiryAtZero(t *testing.T) {
	pool := NewArenaPool(4, 64<<10, 100)
	quotas := NewQuotaRegistry(1<<30, 10)
	mgr := NewDataCacheManager(pool, quotas)
	defer mgr.Close()

	fields := []FieldDef{{Name: "v", Kind: FieldFloat64, Width: 8}}
	opts := CreateOpts{
		Fields:   fields,
		Capacity: 10,
		TTLSec:   0, // no TTL
	}

	if err := mgr.Create("permanent", opts); err != nil {
		t.Fatalf("Create: %v", err)
	}

	time.Sleep(1500 * time.Millisecond)

	// Collection should still exist
	if _, err := mgr.Get("permanent"); err != nil {
		t.Errorf("permanent collection should still exist, got %v", err)
	}
}
