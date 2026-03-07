package datastore

import (
	"context"
	"testing"

	"rah/internal/config"
)

func TestBuildScopedKeyRequiresTenantName(t *testing.T) {
	if _, err := BuildScopedKey(Tenant(""), "cache", "k1"); err == nil {
		t.Fatalf("expected error when tenant name is empty")
	}
}

func TestMemoryStoreTenantScopedIsolation(t *testing.T) {
	store := newMemoryStore(config.StoreConfig{Name: "s1"}, "redis", "cache")
	ctx := context.Background()

	if err := store.Put(ctx, Tenant("tenant-a"), "profile", []byte("A")); err != nil {
		t.Fatalf("put tenant-a failed: %v", err)
	}
	if err := store.Put(ctx, Tenant("tenant-b"), "profile", []byte("B")); err != nil {
		t.Fatalf("put tenant-b failed: %v", err)
	}

	va, ok, err := store.Get(ctx, Tenant("tenant-a"), "profile")
	if err != nil || !ok || string(va) != "A" {
		t.Fatalf("tenant-a read mismatch ok=%v err=%v val=%q", ok, err, string(va))
	}

	vb, ok, err := store.Get(ctx, Tenant("tenant-b"), "profile")
	if err != nil || !ok || string(vb) != "B" {
		t.Fatalf("tenant-b read mismatch ok=%v err=%v val=%q", ok, err, string(vb))
	}
}

func TestMemoryStoreHasPoolStats(t *testing.T) {
	store := newMemoryStore(config.StoreConfig{Name: "s1"}, "redis", "cache")
	stats := store.PoolStats()
	if stats.MaxOpen <= 0 {
		t.Fatalf("expected max_open > 0")
	}
}

func TestMemoryStoreListKeysByTenant(t *testing.T) {
	store := newMemoryStore(config.StoreConfig{Name: "s1"}, "redis", "cache")
	ctx := context.Background()
	_ = store.Put(ctx, Tenant("tenant-a"), "k1", []byte("v1"))
	_ = store.Put(ctx, Tenant("tenant-a"), "k2", []byte("v2"))
	_ = store.Put(ctx, Tenant("tenant-b"), "k1", []byte("v3"))

	keys, err := store.ListKeys(ctx, Tenant("tenant-a"), "")
	if err != nil {
		t.Fatalf("list keys failed: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys for tenant-a, got %d", len(keys))
	}
}
