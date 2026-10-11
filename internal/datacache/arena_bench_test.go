package datacache

import (
	"fmt"
	"testing"
)

// BenchmarkArena_Append measures ns/op for appending records.
// Creates a schema with 3 float64 fields, capacity 1000, and measures
// throughput of Append([float64, float64, float64]) with Reset between iterations.
func BenchmarkArena_Append(b *testing.B) {
	schema := BakeSchema([]FieldDef{
		{Name: "f1", Kind: FieldFloat64},
		{Name: "f2", Kind: FieldFloat64},
		{Name: "f3", Kind: FieldFloat64},
	}, 1000)

	arena := NewArena(schema, 1000)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Reset between iterations to avoid hitting capacity limit
		arena.Reset()

		// Append a single record
		_ = arena.Append([]interface{}{42.5, 99.9, 0.01})
	}
}

// BenchmarkArena_Float64Read measures ns/op for reading float64 values.
// Pre-fills the arena with 100 records and measures Float64 read throughput.
func BenchmarkArena_Float64Read(b *testing.B) {
	schema := BakeSchema([]FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}, 100)

	arena := NewArena(schema, 100)

	// Pre-fill with 100 records
	for i := 0; i < 100; i++ {
		_ = arena.Append([]interface{}{float64(i)})
	}

	b.ReportAllocs()
	b.ResetTimer()

	// Measure read throughput
	for i := 0; i < b.N; i++ {
		// Cycle through all 100 records
		_ = arena.Float64(0, i%100)
	}
}

// BenchmarkArena_Int64Read measures ns/op for reading int64 values.
func BenchmarkArena_Int64Read(b *testing.B) {
	schema := BakeSchema([]FieldDef{
		{Name: "value", Kind: FieldInt64},
	}, 100)

	arena := NewArena(schema, 100)

	// Pre-fill with 100 records
	for i := 0; i < 100; i++ {
		_ = arena.Append([]interface{}{int64(i)})
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = arena.Int64(0, i%100)
	}
}

// BenchmarkArena_StringRead measures ns/op for reading string values.
func BenchmarkArena_StringRead(b *testing.B) {
	schema := BakeSchema([]FieldDef{
		{Name: "label", Kind: FieldStringN, Width: 16},
	}, 100)

	arena := NewArena(schema, 100)

	// Pre-fill with 100 records
	for i := 0; i < 100; i++ {
		arena.Append([]interface{}{"benchmark"})
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = arena.StringAt(0, i%100)
	}
}

// BenchmarkArena_EnumRead measures ns/op for reading enum values.
func BenchmarkArena_EnumRead(b *testing.B) {
	schema := BakeSchema([]FieldDef{
		{Name: "status", Kind: FieldEnum},
	}, 100)

	arena := NewArena(schema, 100)

	// Pre-fill with 100 records
	for i := 0; i < 100; i++ {
		arena.Append([]interface{}{uint8(i % 256)})
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = arena.Enum(0, i%100)
	}
}

// BenchmarkArena_MultiFieldAppend measures ns/op for appending multi-field records.
func BenchmarkArena_MultiFieldAppend(b *testing.B) {
	schema := BakeSchema([]FieldDef{
		{Name: "price", Kind: FieldFloat64},
		{Name: "count", Kind: FieldInt64},
		{Name: "label", Kind: FieldStringN, Width: 16},
		{Name: "status", Kind: FieldEnum},
	}, 500)

	arena := NewArena(schema, 500)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		arena.Reset()
		arena.Append([]interface{}{
			49.99,
			int64(42),
			"product",
			uint8(1),
		})
	}
}

// BenchmarkArena_Tombstone measures ns/op for marking records as deleted.
func BenchmarkArena_Tombstone(b *testing.B) {
	schema := BakeSchema([]FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}, 100)

	arena := NewArena(schema, 100)

	// Pre-fill with 100 records
	for i := 0; i < 100; i++ {
		arena.Append([]interface{}{float64(i)})
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		arena.Delete(i % 100)
	}
}

// BenchmarkArena_TombstoneCheck measures ns/op for checking if a record is live (not deleted).
func BenchmarkArena_TombstoneCheck(b *testing.B) {
	schema := BakeSchema([]FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}, 100)

	arena := NewArena(schema, 100)

	// Pre-fill with 100 records, delete half of them
	for i := 0; i < 100; i++ {
		arena.Append([]interface{}{float64(i)})
		if i%2 == 0 {
			arena.Delete(i)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = arena.IsLive(i % 100)
	}
}

// BenchmarkManager_CreateDrop measures ns/op for Create + Drop cycle.
func BenchmarkManager_CreateDrop(b *testing.B) {
	pool := NewArenaPool(10, 4096, 50)
	quotas := NewQuotaRegistry(1000000, 100)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	opts := CreateOpts{
		TenantID: 1,
		App:      "bench",
		Fields: []FieldDef{
			{Name: "value", Kind: FieldFloat64},
		},
		Capacity: 10,
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		name := fmt.Sprintf("coll%d", i)
		_ = manager.Create(name, opts)
		_ = manager.Drop(name, 1, "bench")
	}
}

// BenchmarkManager_AppendJSON measures ns/op for appending JSON records.
func BenchmarkManager_AppendJSON(b *testing.B) {
	pool := NewArenaPool(4, 8192, 1000)
	quotas := NewQuotaRegistry(1000000, 100)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	err := manager.Create("test", CreateOpts{
		TenantID: 1,
		App:      "bench",
		Fields: []FieldDef{
			{Name: "price", Kind: FieldFloat64},
			{Name: "count", Kind: FieldInt64},
		},
		Capacity: 1000,
	})
	if err != nil {
		b.Fatalf("Create failed: %v", err)
	}

	jsonData := []byte(`{"price": 42.5, "count": 100}`)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = manager.AppendJSON("test", jsonData)
	}
}

// BenchmarkManager_QueryAll measures ns/op for querying all records.
func BenchmarkManager_QueryAll(b *testing.B) {
	pool := NewArenaPool(4, 8192, 100)
	quotas := NewQuotaRegistry(1000000, 100)
	manager := NewDataCacheManager(pool, quotas)
	defer manager.Close()

	err := manager.Create("test", CreateOpts{
		TenantID: 1,
		App:      "bench",
		Fields: []FieldDef{
			{Name: "price", Kind: FieldFloat64},
		},
		Capacity: 100,
	})
	if err != nil {
		b.Fatalf("Create failed: %v", err)
	}

	// Pre-populate with 100 records
	for i := 0; i < 100; i++ {
		_ = manager.AppendJSON("test", []byte(`{"price": 42.5}`))
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = manager.QueryAll("test", func(size int) []byte {
			return make([]byte, 0, size)
		})
	}
}

// BenchmarkPool_Claim measures ns/op for claiming a pool slot.
func BenchmarkPool_Claim(b *testing.B) {
	pool := NewArenaPool(64, 4096, 50)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		idx, _ := pool.Claim(0)
		pool.Release(idx)
	}
}

// BenchmarkPool_SortBy measures ns/op for sorting by a column.
func BenchmarkPool_SortBy(b *testing.B) {
	pool := NewArenaPool(2, 4096, 100)

	idx, _ := pool.Claim(0)
	schema := BakeSchema([]FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}, 100)

	pool.InitSlot(idx, 100, schema)
	arena := &pool.slots[idx].arena

	// Pre-fill with 100 records in reverse order
	for i := 99; i >= 0; i-- {
		arena.Append([]interface{}{float64(i)})
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		pool.SortBy(idx, 0)
	}
}

// BenchmarkQuota_Reserve measures ns/op for quota reservation.
func BenchmarkQuota_Reserve(b *testing.B) {
	qr := NewQuotaRegistry(1000000, 10000)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tenantID := uint16(i % 100)
		_ = qr.Reserve(tenantID, "app", 100, 1)
		qr.Release(tenantID, "app", 100, 1)
	}
}

// BenchmarkArena_Parallel measures parallel Append throughput.
func BenchmarkArena_Parallel(b *testing.B) {
	schema := BakeSchema([]FieldDef{
		{Name: "f1", Kind: FieldFloat64},
		{Name: "f2", Kind: FieldFloat64},
		{Name: "f3", Kind: FieldFloat64},
	}, 10000)

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		arena := NewArena(schema, 10000)
		for pb.Next() {
			if int(arena.Size.Load()) >= arena.Cap {
				arena.Reset()
			}
			arena.Append([]interface{}{42.5, 99.9, 0.01})
		}
	})
}

// BenchmarkArena_ParallelRead measures parallel Float64 read throughput.
func BenchmarkArena_ParallelRead(b *testing.B) {
	schema := BakeSchema([]FieldDef{
		{Name: "value", Kind: FieldFloat64},
	}, 100)

	arena := NewArena(schema, 100)

	// Pre-fill
	for i := 0; i < 100; i++ {
		arena.Append([]interface{}{float64(i)})
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = arena.Float64(0, i%100)
			i++
		}
	})
}
