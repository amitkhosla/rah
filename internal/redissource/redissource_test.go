package redissource

import (
	"testing"
)

// ============================================================================
// validateKey tests
// ============================================================================

func TestValidateKey_RahPrefix(t *testing.T) {
	c := &TenantScopedClient{}
	err := c.validateKey("_rah:something")
	if err == nil {
		t.Fatal("expected error for _rah: prefix")
	}
}

func TestValidateKey_Normal(t *testing.T) {
	c := &TenantScopedClient{}
	for _, k := range []string{"mykey", "tenant:key", "orders:123", "a"} {
		if err := c.validateKey(k); err != nil {
			t.Errorf("validateKey(%q) = %v, want nil", k, err)
		}
	}
}

func TestValidateKey_EdgeCases(t *testing.T) {
	c := &TenantScopedClient{}
	// Exactly the reserved prefix — should fail
	if err := c.validateKey("_rah:"); err == nil {
		t.Error("expected error for exact reserved prefix")
	}
	// Empty key — should pass (no _rah: prefix)
	if err := c.validateKey(""); err != nil {
		t.Errorf("empty key: expected nil, got %v", err)
	}
}

// ============================================================================
// fullKey tests
// ============================================================================

func TestFullKey_WithPrefix(t *testing.T) {
	c := &TenantScopedClient{prefix: "42:"}
	if got := c.fullKey("orders"); got != "42:orders" {
		t.Errorf("got %q, want %q", got, "42:orders")
	}
}

func TestFullKey_EmptyPrefix(t *testing.T) {
	c := &TenantScopedClient{prefix: ""}
	if got := c.fullKey("orders"); got != "orders" {
		t.Errorf("got %q, want %q", got, "orders")
	}
}

// ============================================================================
// newTenantScopedClient prefix computation
// ============================================================================

func TestNewTenantScopedClient_IDMode(t *testing.T) {
	cfg := RedisSourceConfig{TenantPrefix: PrefixID, KeySep: ":"}
	c := newTenantScopedClient(nil, cfg, 42, "acme")
	if got := c.fullKey("k"); got != "42:k" {
		t.Errorf("got %q, want %q", got, "42:k")
	}
}

func TestNewTenantScopedClient_AliasMode(t *testing.T) {
	cfg := RedisSourceConfig{TenantPrefix: PrefixAlias, KeySep: ":"}
	c := newTenantScopedClient(nil, cfg, 42, "acme")
	if got := c.fullKey("k"); got != "acme:k" {
		t.Errorf("got %q, want %q", got, "acme:k")
	}
}

func TestNewTenantScopedClient_NoneMode(t *testing.T) {
	cfg := RedisSourceConfig{TenantPrefix: PrefixNone}
	c := newTenantScopedClient(nil, cfg, 42, "acme")
	if got := c.fullKey("k"); got != "k" {
		t.Errorf("got %q, want %q", got, "k")
	}
}

func TestNewTenantScopedClient_DefaultSep(t *testing.T) {
	// Empty KeySep should default to ":"
	cfg := RedisSourceConfig{TenantPrefix: PrefixID}
	c := newTenantScopedClient(nil, cfg, 7, "t")
	if got := c.fullKey("x"); got != "7:x" {
		t.Errorf("got %q, want %q", got, "7:x")
	}
}

func TestNewTenantScopedClient_CustomSep(t *testing.T) {
	cfg := RedisSourceConfig{TenantPrefix: PrefixAlias, KeySep: "/"}
	c := newTenantScopedClient(nil, cfg, 1, "org")
	if got := c.fullKey("k"); got != "org/k" {
		t.Errorf("got %q, want %q", got, "org/k")
	}
}

// ============================================================================
// Pool caching tests
// ============================================================================

func TestPoolGetForTenant_UnknownSource(t *testing.T) {
	p := &RedisSourcePool{
		coalescers: make(map[string]*RedisCoalescer),
		configs:    make(map[string]RedisSourceConfig),
	}
	_, ok := p.GetForTenant("nonexistent", 1, "t")
	if ok {
		t.Fatal("expected ok=false for unknown source")
	}
}

func TestPoolGetForTenant_CachesClient(t *testing.T) {
	// nil coalescer is fine — GetForTenant only does caching, doesn't call Redis
	p := &RedisSourcePool{
		coalescers: map[string]*RedisCoalescer{"src": nil},
		configs:    map[string]RedisSourceConfig{"src": {TenantPrefix: PrefixID}},
	}
	c1, ok1 := p.GetForTenant("src", 5, "t5")
	c2, ok2 := p.GetForTenant("src", 5, "t5")
	if !ok1 || !ok2 {
		t.Fatal("expected ok=true")
	}
	if c1 != c2 {
		t.Error("expected same *TenantScopedClient on repeated call (cache hit)")
	}
}

func TestPoolGetForTenant_DifferentTenants(t *testing.T) {
	p := &RedisSourcePool{
		coalescers: map[string]*RedisCoalescer{"src": nil},
		configs:    map[string]RedisSourceConfig{"src": {TenantPrefix: PrefixID}},
	}
	c1, _ := p.GetForTenant("src", 1, "t1")
	c2, _ := p.GetForTenant("src", 2, "t2")
	if c1 == c2 {
		t.Error("expected different clients for different tenants")
	}
	// Prefixes should be different
	if c1.prefix == c2.prefix {
		t.Errorf("tenant 1 and 2 have same prefix %q", c1.prefix)
	}
}

// ============================================================================
// Constants / type tests
// ============================================================================

func TestRahReservedPrefix(t *testing.T) {
	if RahReservedPrefix != "_rah:" {
		t.Errorf("RahReservedPrefix = %q, want _rah:", RahReservedPrefix)
	}
}

func TestPrefixModeValues(t *testing.T) {
	if string(PrefixNone) != "none" {
		t.Errorf("PrefixNone = %q, want none", PrefixNone)
	}
	if string(PrefixID) != "id" {
		t.Errorf("PrefixID = %q, want id", PrefixID)
	}
	if string(PrefixAlias) != "alias" {
		t.Errorf("PrefixAlias = %q, want alias", PrefixAlias)
	}
}

// ============================================================================
// Benchmarks
// ============================================================================

func BenchmarkFullKey_WithPrefix(b *testing.B) {
	c := &TenantScopedClient{prefix: "42:"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = c.fullKey("orders")
	}
}

func BenchmarkValidateKey_Normal(b *testing.B) {
	c := &TenantScopedClient{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = c.validateKey("normal:key:here")
	}
}
