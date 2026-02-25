package cache

import "testing"

func TestPutOverwriteExpiredUpdatesTenantUsage(t *testing.T) {
	cm, err := NewCacheManager(
		64,
		[]uint32{64},
		[]uint32{0},
		16,
		0,
	)
	if err != nil {
		t.Fatalf("NewCacheManager() error = %v", err)
	}

	if _, ok := cm.Put(1, []byte("k1"), []byte("1234567890"), 0); !ok {
		t.Fatalf("first Put failed")
	}

	if got := cm.getTenantCounter(1).used.Load(); got != 42 {
		t.Fatalf("tenant 1 usage after first put = %d, want 42", got)
	}
	if got := cm.Stats(); got != 42 {
		t.Fatalf("global usage after first put = %d, want 42", got)
	}

	if _, ok := cm.Put(2, []byte("k2"), []byte("12345678901234567890"), 0); !ok {
		t.Fatalf("second Put failed")
	}

	if got := cm.getTenantCounter(1).used.Load(); got != 0 {
		t.Fatalf("tenant 1 usage after overwrite = %d, want 0", got)
	}
	if got := cm.getTenantCounter(2).used.Load(); got != 52 {
		t.Fatalf("tenant 2 usage after second put = %d, want 52", got)
	}
	if got := cm.Stats(); got != 52 {
		t.Fatalf("global usage after overwrite = %d, want 52", got)
	}

	if _, ok := cm.Put(3, []byte("k3"), []byte("1234567890123456"), 0); !ok {
		t.Fatalf("third Put failed")
	}

	if got := cm.getTenantCounter(2).used.Load(); got != 0 {
		t.Fatalf("tenant 2 usage after second overwrite = %d, want 0", got)
	}
	if got := cm.getTenantCounter(3).used.Load(); got != 48 {
		t.Fatalf("tenant 3 usage after third put = %d, want 48", got)
	}
	if got := cm.Stats(); got != 48 {
		t.Fatalf("global usage after second overwrite = %d, want 48", got)
	}
}

func TestPutOverwriteExpiredSkipsSecondReductionWhenIndexAlreadyDeleted(t *testing.T) {
	cm, err := NewCacheManager(
		64,
		[]uint32{64},
		[]uint32{0},
		16,
		0,
	)
	if err != nil {
		t.Fatalf("NewCacheManager() error = %v", err)
	}

	key := []byte("k1")
	if _, ok := cm.Put(1, key, []byte("1234567890"), 0); !ok {
		t.Fatalf("first Put failed")
	}

	if !cm.index.Delete(Hash128(1, key)) {
		t.Fatalf("expected pre-delete to succeed")
	}

	if _, ok := cm.Put(2, []byte("k2"), []byte("12345678901234567890"), 0); !ok {
		t.Fatalf("second Put failed")
	}

	if got := cm.getTenantCounter(1).used.Load(); got != 42 {
		t.Fatalf("tenant 1 usage = %d, want 42 (must not subtract twice)", got)
	}
	if got := cm.getTenantCounter(2).used.Load(); got != 52 {
		t.Fatalf("tenant 2 usage = %d, want 52", got)
	}
	if got := cm.Stats(); got != 94 {
		t.Fatalf("global usage = %d, want 94", got)
	}
}
