package control

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/amitkhosla/rah/internal/config"
	"github.com/amitkhosla/rah/internal/datastore"
	tenantregistry "github.com/amitkhosla/rah/internal/registry"
	"github.com/jackc/pgx/v5/pgxpool"
)

// init loads the project-root .env file so that POSTGRES_PASSWORD (and any
// other secrets) are available to the PG tests without being hardcoded.
// Variables already set in the process environment take priority.
func init() {
	// Walk up from this file's directory to find the repo root .env.
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Dir(thisFile)
	for range 6 { // max 6 levels up
		candidate := filepath.Join(dir, ".env")
		if f, err := os.Open(candidate); err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				k, v, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				k = strings.TrimSpace(k)
				v = strings.TrimSpace(v)
				// Don't overwrite variables already set by the caller.
				if os.Getenv(k) == "" {
					_ = os.Setenv(k, v)
				}
			}
			f.Close()
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
}

// pgPassword returns the PostgreSQL password from the environment.
// Populated by init() from the project-root .env file.
func pgPassword() string {
	return os.Getenv("POSTGRES_PASSWORD")
}

// pgConnDSN returns a postgres:// URL for direct pgxpool connections (ping / cleanup).
func pgConnDSN() string {
	if v := os.Getenv("RAH_TEST_PG_DSN"); v != "" {
		return v
	}
	// pgx accepts key=value libpq format directly.
	return fmt.Sprintf("host=localhost port=5432 user=postgres password=%s dbname=gateway sslmode=disable", pgPassword())
}

// pgStoreConfig returns a DataStoreConfig that binds registry and cache domains
// to the shared PostgreSQL instance. Separate host/port/user/pass fields are
// used so buildConnString produces a valid libpq key-value string regardless
// of special characters in the password. Pool size is capped at 2 so that
// multiple parallel test runs don't exhaust the server's max_connections.
func pgStoreConfig() config.DataStoreConfig {
	pwd := pgPassword()
	// If RAH_TEST_PG_DSN is set (e.g., in CI), extract password from it
	if dsn := os.Getenv("RAH_TEST_PG_DSN"); dsn != "" {
		_, pass, found := strings.Cut(dsn, "password=")
		if found {
			if endIdx := strings.IndexAny(pass, " \t"); endIdx != -1 {
				pwd = pass[:endIdx]
			} else {
				pwd = pass
			}
		}
	}
	return config.DataStoreConfig{
		Stores: map[string]config.StoreConfig{
			"pg": {
				Name:    "pg",
				Kind:    config.StorePostgreSQL,
				Enabled: true,
				Connection: config.StoreConnection{
					Host:     "localhost",
					Port:     5432,
					Username: "postgres",
					Password: pwd,
					Database: "gateway",
					Params:   map[string]string{"pool_max_open": "2"},
				},
			},
		},
		Bindings: map[config.DataDomain]string{
			config.DomainAPIDefinitions:  "pg",
			config.DomainFlows:           "pg",
			config.DomainTenantRegistry:  "pg",
			config.DomainCache:           "pg",
		},
	}
}

// newPGDataStoreManager builds a DataStoreManager backed by Postgres and
// registers t.Cleanup to close the connection pool when the test ends.
func newPGDataStoreManager(t *testing.T) *DataStoreManager {
	t.Helper()
	dsm, err := NewDataStoreManager(context.Background(), pgStoreConfig(), nil)
	if err != nil {
		t.Fatalf("build pg datastore manager: %v", err)
	}
	t.Cleanup(func() { dsm.Close() })
	return dsm
}

// skipIfNoPG skips the test if postgres is unreachable.
func skipIfNoPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), pgConnDSN())
	if err != nil {
		t.Skipf("postgres not available (%v) — skipping", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("postgres ping failed (%v) — skipping", err)
	}
	return pool
}

// cleanupPGPrefix deletes all kv_entries whose full_key contains any of the
// given tenant aliases in either the tenant_data or cache domains.
// Called via t.Cleanup so test data doesn't accumulate between runs.
func cleanupPGPrefix(pool *pgxpool.Pool, aliases ...string) {
	ctx := context.Background()
	for _, alias := range aliases {
		// Registry keys: tenant:__global__:tenant_data:{alias}...
		// Cache keys:    tenant:{alias}:{domain}:{key}
		// Broad match: delete anything scoped to this alias.
		_, _ = pool.Exec(ctx,
			`DELETE FROM kv_entries WHERE full_key LIKE $1`,
			fmt.Sprintf("%%:%s:%%", alias),
		)
		// Also clean global-tenant registry entries that embed this alias.
		_, _ = pool.Exec(ctx,
			`DELETE FROM kv_entries WHERE full_key LIKE $1`,
			fmt.Sprintf("%%__global__:tenant_data:%s:%%", alias),
		)
		_, _ = pool.Exec(ctx,
			`DELETE FROM kv_entries WHERE full_key LIKE $1`,
			fmt.Sprintf("%%__global__:tenant_data:tenant:%s", alias),
		)
	}
}

// ---------------------------------------------------------------------------
// PostgreSQL — same five cross-instance tests
// ---------------------------------------------------------------------------

func TestPG_RegistrySameInstancePersistAndLoad(t *testing.T) {
	pool := skipIfNoPG(t)
	defer pool.Close()

	const alias = "pg-acme"
	t.Cleanup(func() { cleanupPGPrefix(pool, alias) })

	dsm := newPGDataStoreManager(t)
	mgr, store := newTestRegMgr(t, dsm)
	mgr.UpsertTenantState(
		[]string{alias},
		map[string]string{"primary": "https://pg-acme.internal/v2"},
		map[string]string{"api_key": "sk-pg-acme"},
		nil,
	)

	// In-memory check — must be immediate.
	rec := tenantRecordByAlias(mgr, alias)
	if rec == nil {
		t.Fatal("tenant not found in memory after UpsertTenantState")
	}
	if got := rec.ServiceURLs["primary"]; got != "https://pg-acme.internal/v2" {
		t.Errorf("in-memory URL: want %q, got %q", "https://pg-acme.internal/v2", got)
	}
	if got := rec.Identifiers["api_key"]; got != "sk-pg-acme" {
		t.Errorf("in-memory identifier: want %q, got %q", "sk-pg-acme", got)
	}

	// Disk (postgres) check — wait for async persist goroutine.
	waitForTenantPersisted(t, store, alias)

	snap, err := store.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	var found *tenantregistry.TenantRecord
	for i := range snap.Tenants {
		for _, a := range snap.Tenants[i].Aliases {
			if a == alias {
				found = &snap.Tenants[i]
			}
		}
	}
	if found == nil {
		t.Fatal("tenant missing from postgres snapshot")
	}
	if got := found.ServiceURLs["primary"]; got != "https://pg-acme.internal/v2" {
		t.Errorf("postgres URL: want %q, got %q", "https://pg-acme.internal/v2", got)
	}
	if got := found.Identifiers["api_key"]; got != "sk-pg-acme" {
		t.Errorf("postgres identifier: want %q, got %q", "sk-pg-acme", got)
	}
}

func TestPG_RegistryMultiInstanceReflection(t *testing.T) {
	pool := skipIfNoPG(t)
	defer pool.Close()

	tenants := []struct{ alias, url, key string }{
		{"pg-org-alpha", "https://pg-alpha.svc/v1", "key-pg-alpha"},
		{"pg-org-beta", "https://pg-beta.svc/v1", "key-pg-beta"},
		{"pg-org-gamma", "https://pg-gamma.svc/v1", "key-pg-gamma"},
	}
	allAliases := make([]string, len(tenants))
	for i, tc := range tenants {
		allAliases[i] = tc.alias
	}
	t.Cleanup(func() { cleanupPGPrefix(pool, allAliases...) })

	// Instance 1.
	mgr1, store1 := newTestRegMgr(t, newPGDataStoreManager(t))
	for _, tc := range tenants {
		mgr1.UpsertTenantState(
			[]string{tc.alias},
			map[string]string{"endpoint": tc.url},
			map[string]string{"secret": tc.key},
			nil,
		)
	}
	for _, tc := range tenants {
		waitForTenantPersisted(t, store1, tc.alias)
	}

	// Instance 2: cold start from same postgres.
	mgr2, _ := newTestRegMgr(t, newPGDataStoreManager(t))

	for _, tc := range tenants {
		rec := tenantRecordByAlias(mgr2, tc.alias)
		if rec == nil {
			t.Errorf("instance 2 missing tenant %q", tc.alias)
			continue
		}
		if got := rec.ServiceURLs["endpoint"]; got != tc.url {
			t.Errorf("tenant %q endpoint: want %q, got %q", tc.alias, tc.url, got)
		}
		if got := rec.Identifiers["secret"]; got != tc.key {
			t.Errorf("tenant %q secret: want %q, got %q", tc.alias, tc.key, got)
		}
	}
}

func TestPG_TenantIsolationCrossInstance(t *testing.T) {
	pool := skipIfNoPG(t)
	defer pool.Close()

	const aliasA, aliasB = "pg-tenant-a", "pg-tenant-b"
	t.Cleanup(func() { cleanupPGPrefix(pool, aliasA, aliasB) })

	mgr1, store1 := newTestRegMgr(t, newPGDataStoreManager(t))
	mgr1.UpsertTenantState([]string{aliasA}, map[string]string{"svc": "https://pg-a.internal"}, map[string]string{"tok": "tok-pg-a"}, nil)
	mgr1.UpsertTenantState([]string{aliasB}, map[string]string{"svc": "https://pg-b.internal"}, map[string]string{"tok": "tok-pg-b"}, nil)
	waitForTenantPersisted(t, store1, aliasA)
	waitForTenantPersisted(t, store1, aliasB)

	// Same-instance isolation.
	recA := tenantRecordByAlias(mgr1, aliasA)
	recB := tenantRecordByAlias(mgr1, aliasB)
	if recA == nil || recB == nil {
		t.Fatal("tenant-a or tenant-b missing in instance 1")
	}
	if recA.ServiceURLs["svc"] == recB.ServiceURLs["svc"] {
		t.Error("same-instance: service URLs bleed across tenants")
	}
	if recA.Identifiers["tok"] == recB.Identifiers["tok"] {
		t.Error("same-instance: tokens bleed across tenants")
	}

	// Cross-instance isolation.
	mgr2, _ := newTestRegMgr(t, newPGDataStoreManager(t))
	recA2 := tenantRecordByAlias(mgr2, aliasA)
	recB2 := tenantRecordByAlias(mgr2, aliasB)
	if recA2 == nil || recB2 == nil {
		t.Fatal("tenant-a or tenant-b missing in instance 2")
	}
	if recA2.ServiceURLs["svc"] != "https://pg-a.internal" {
		t.Errorf("instance 2 tenant-a svc: want %q, got %q", "https://pg-a.internal", recA2.ServiceURLs["svc"])
	}
	if recB2.ServiceURLs["svc"] != "https://pg-b.internal" {
		t.Errorf("instance 2 tenant-b svc: want %q, got %q", "https://pg-b.internal", recB2.ServiceURLs["svc"])
	}
	if recA2.Identifiers["tok"] != "tok-pg-a" {
		t.Errorf("instance 2 tenant-a tok: want %q, got %q", "tok-pg-a", recA2.Identifiers["tok"])
	}
	if recB2.Identifiers["tok"] != "tok-pg-b" {
		t.Errorf("instance 2 tenant-b tok: want %q, got %q", "tok-pg-b", recB2.Identifiers["tok"])
	}
	if recA2.ServiceURLs["svc"] == recB2.ServiceURLs["svc"] {
		t.Error("cross-instance: service URLs bleed between tenants after restore")
	}
}

func TestPG_CacheMultiInstanceReflection(t *testing.T) {
	pool := skipIfNoPG(t)
	defer pool.Close()

	const tA, tB = "pg-cache-tenant-a", "pg-cache-tenant-b"
	t.Cleanup(func() { cleanupPGPrefix(pool, tA, tB) })

	dsm1 := newPGDataStoreManager(t)
	ctx := context.Background()

	if err := dsm1.Put(ctx, config.DomainCache, datastore.Tenant(tA), "session", []byte("pg-val-a")); err != nil {
		t.Fatalf("put cache tenant-a: %v", err)
	}
	if err := dsm1.Put(ctx, config.DomainCache, datastore.Tenant(tB), "session", []byte("pg-val-b")); err != nil {
		t.Fatalf("put cache tenant-b: %v", err)
	}
	if err := dsm1.Put(ctx, config.DomainCache, datastore.Tenant(tA), "profile", []byte("pg-profile-a")); err != nil {
		t.Fatalf("put cache tenant-a profile: %v", err)
	}

	// Instance 2: separate manager, same postgres table.
	dsm2 := newPGDataStoreManager(t)

	gotA, ok, err := dsm2.Get(ctx, config.DomainCache, datastore.Tenant(tA), "session")
	if err != nil || !ok || string(gotA) != "pg-val-a" {
		t.Errorf("instance-2 tenant-a session: ok=%v err=%v got=%q", ok, err, string(gotA))
	}
	gotAP, ok, err := dsm2.Get(ctx, config.DomainCache, datastore.Tenant(tA), "profile")
	if err != nil || !ok || string(gotAP) != "pg-profile-a" {
		t.Errorf("instance-2 tenant-a profile: ok=%v err=%v got=%q", ok, err, string(gotAP))
	}
	gotB, ok, err := dsm2.Get(ctx, config.DomainCache, datastore.Tenant(tB), "session")
	if err != nil || !ok || string(gotB) != "pg-val-b" {
		t.Errorf("instance-2 tenant-b session: ok=%v err=%v got=%q", ok, err, string(gotB))
	}

	// No bleed: tenant-b must NOT see tenant-a's profile key.
	_, found, err := dsm2.Get(ctx, config.DomainCache, datastore.Tenant(tB), "profile")
	if err != nil {
		t.Fatalf("tenant-b profile lookup error: %v", err)
	}
	if found {
		t.Error("cache bleed: tenant-b can read tenant-a's profile key")
	}
	if string(gotA) == string(gotB) {
		t.Error("cache bleed: tenant-a and tenant-b returned identical session values")
	}
}

func TestPG_HighConcurrencyCrossInstanceConsistency(t *testing.T) {
	pool := skipIfNoPG(t)
	defer pool.Close()

	const numTenants = 30
	aliases := make([]string, numTenants)
	for i := range numTenants {
		aliases[i] = fmt.Sprintf("pg-conc-tenant-%02d", i)
	}
	t.Cleanup(func() { cleanupPGPrefix(pool, aliases...) })

	mgr1, store1 := newTestRegMgr(t, newPGDataStoreManager(t))

	var wg sync.WaitGroup
	for i := range numTenants {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			mgr1.UpsertTenantState(
				[]string{aliases[n]},
				map[string]string{"endpoint": fmt.Sprintf("https://pg-svc-%02d.internal", n)},
				map[string]string{"tok": fmt.Sprintf("pg-secret-%02d", n)},
				nil,
			)
		}(i)
	}
	wg.Wait()

	for _, alias := range aliases {
		waitForTenantPersisted(t, store1, alias)
	}

	// Instance 2: cold start from same postgres.
	mgr2, _ := newTestRegMgr(t, newPGDataStoreManager(t))

	summaries, _ := mgr2.ListTenants(0, 1000)
	// Filter to only our test tenants (postgres may have tenants from other tests).
	var pgCount int
	for _, s := range summaries {
		for _, a := range s.Aliases {
			for _, want := range aliases {
				if a == want {
					pgCount++
				}
			}
		}
	}
	if pgCount != numTenants {
		t.Errorf("instance 2: expected %d test tenants, found %d", numTenants, pgCount)
	}

	for i, alias := range aliases {
		wantURL := fmt.Sprintf("https://pg-svc-%02d.internal", i)
		wantKey := fmt.Sprintf("pg-secret-%02d", i)
		rec := tenantRecordByAlias(mgr2, alias)
		if rec == nil {
			t.Errorf("instance 2 missing tenant %q", alias)
			continue
		}
		if got := rec.ServiceURLs["endpoint"]; got != wantURL {
			t.Errorf("tenant %q endpoint: want %q, got %q", alias, wantURL, got)
		}
		if got := rec.Identifiers["tok"]; got != wantKey {
			t.Errorf("tenant %q tok: want %q, got %q", alias, wantKey, got)
		}
	}
}
