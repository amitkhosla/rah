package registry

import (
	"testing"
)

// setupBenchRegistry builds a realistic TenantRegistry with N tenants and
// several URL/ID/Meta keys, installs it as the active snapshot, and returns
// a TenantID and pre-resolved KeyIDs for use in benchmarks.
func setupBenchRegistry(b *testing.B, numTenants int) (tenantID uint16, urlKeyID, idKeyID, metaKeyID uint16) {
	b.Helper()
	mgr := NewRegistryManager()

	aliases := make([]string, numTenants)
	for i := range aliases {
		aliases[i] = string(rune('a'+i%26)) + "tenant"
	}

	// Upsert all tenants with URL, identifier, and meta keys.
	for i := 0; i < numTenants; i++ {
		mgr.UpsertTenantState(
			[]string{aliases[i]},
			map[string]string{"primary": "https://api.example.com/svc", "secondary": "https://api2.example.com/svc"},
			map[string]string{"api_key": "sk-test-key-1234567890"},
			map[string]string{"plan": "enterprise", "region": "us-east-1"},
		)
	}

	// Resolve KeyIDs (bake-time cost, not measured in bench).
	urlKeyID = mgr.EnsureURLKeyID("primary")
	idKeyID = mgr.EnsureIDKeyID("api_key")
	metaKeyID = mgr.EnsureMetaKeyID("plan")

	// Resolve tenantID for the first alias.
	reg := State.Active.Load()
	tid, _ := reg.Aliases.Lookup(aliases[0])
	tenantID = tid

	return tenantID, urlKeyID, idKeyID, metaKeyID
}

// BenchmarkAliasLookup measures the hot-path alias→TenantID resolution.
// This is the cost of REG_LOOKUP minus the string() allocation in the step wrapper.
func BenchmarkAliasLookup(b *testing.B) {
	_, _, _, _ = setupBenchRegistry(b, 64)
	reg := State.Active.Load()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = reg.Aliases.Lookup("atenant")
	}
}

func BenchmarkAliasLookup_Parallel(b *testing.B) {
	_, _, _, _ = setupBenchRegistry(b, 64)
	reg := State.Active.Load()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = reg.Aliases.Lookup("atenant")
		}
	})
}

// BenchmarkAliasLookupViaState measures the full path including atomic snapshot load —
// matches exactly what RegistryLookup does after the string() call.
func BenchmarkAliasLookupViaState(b *testing.B) {
	_, _, _, _ = setupBenchRegistry(b, 64)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		reg := State.Active.Load()
		_, _ = reg.Aliases.Lookup("atenant")
	}
}

func BenchmarkAliasLookupViaState_Parallel(b *testing.B) {
	_, _, _, _ = setupBenchRegistry(b, 64)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			reg := State.Active.Load()
			_, _ = reg.Aliases.Lookup("atenant")
		}
	})
}

// BenchmarkGetURLByKeyID measures LOAD_SERVICE_URL hot path: atomic load + matrix read.
func BenchmarkGetURLByKeyID(b *testing.B) {
	tenantID, urlKeyID, _, _ := setupBenchRegistry(b, 64)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = GetURLByKeyID(tenantID, urlKeyID)
	}
}

func BenchmarkGetURLByKeyID_Parallel(b *testing.B) {
	tenantID, urlKeyID, _, _ := setupBenchRegistry(b, 64)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = GetURLByKeyID(tenantID, urlKeyID)
		}
	})
}

// BenchmarkGetMetaByKeyID measures LOAD_META hot path.
func BenchmarkGetMetaByKeyID(b *testing.B) {
	tenantID, _, _, metaKeyID := setupBenchRegistry(b, 64)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = GetMetaByKeyID(tenantID, metaKeyID)
	}
}

func BenchmarkGetMetaByKeyID_Parallel(b *testing.B) {
	tenantID, _, _, metaKeyID := setupBenchRegistry(b, 64)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = GetMetaByKeyID(tenantID, metaKeyID)
		}
	})
}

// BenchmarkGetIDByKeyID measures LOAD_IDENTIFIER hot path.
func BenchmarkGetIDByKeyID(b *testing.B) {
	tenantID, _, idKeyID, _ := setupBenchRegistry(b, 64)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = GetIDByKeyID(tenantID, idKeyID)
	}
}

// BenchmarkAliasLookup_ManyTenants checks if tenant count affects lookup time.
func BenchmarkAliasLookup_ManyTenants(b *testing.B) {
	for _, n := range []int{4, 64, 256, 1024} {
		n := n
		b.Run("tenants="+string(rune('0'+n/100%10))+string(rune('0'+n/10%10))+string(rune('0'+n%10)), func(b *testing.B) {
			_, _, _, _ = setupBenchRegistry(b, n)
			reg := State.Active.Load()
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = reg.Aliases.Lookup("atenant")
			}
		})
	}
}

// BenchmarkFullRegistryReadPath benchmarks the complete sequence for a
// request that does alias lookup + URL read + meta read — matching flow-scenario-a.
func BenchmarkFullRegistryReadPath(b *testing.B) {
	tenantID, urlKeyID, _, metaKeyID := setupBenchRegistry(b, 64)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		reg := State.Active.Load()
		tID, _ := reg.Aliases.Lookup("atenant")
		_ = tID
		_, _ = GetURLByKeyID(tenantID, urlKeyID)
		_, _ = GetMetaByKeyID(tenantID, metaKeyID)
	}
}

func BenchmarkFullRegistryReadPath_Parallel(b *testing.B) {
	tenantID, urlKeyID, _, metaKeyID := setupBenchRegistry(b, 64)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			reg := State.Active.Load()
			tID, _ := reg.Aliases.Lookup("atenant")
			_ = tID
			_, _ = GetURLByKeyID(tenantID, urlKeyID)
			_, _ = GetMetaByKeyID(tenantID, metaKeyID)
		}
	})
}
