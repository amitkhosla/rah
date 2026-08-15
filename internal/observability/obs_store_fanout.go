package observability

import (
	"context"

	"github.com/amitkhosla/rah/internal/gatewaylog"
)

const maxFanOutStores = 4

// FanOutObsStore fans writes to multiple stores while delegating reads to the primary.
// It is safe for concurrent use; stores[0] is the primary store.
type FanOutObsStore struct {
	stores [maxFanOutStores]ObsStore
	n      int // number of active stores (set once at construction, never modified)
}

// NewFanOutObsStore creates a store that fans writes to all provided stores.
// The first store (primary) is used for all read/query methods.
// Write methods fan to all stores, returning the error from the primary.
// Secondary store errors are logged but do not cause the write to fail.
// Panics if len(stores) == 0 or len(stores) > maxFanOutStores.
func NewFanOutObsStore(primary ObsStore, secondary ...ObsStore) *FanOutObsStore {
	if primary == nil {
		panic("primary store is required")
	}
	n := 1 + len(secondary)
	if n > maxFanOutStores {
		panic("too many stores: exceeds maxFanOutStores")
	}

	f := &FanOutObsStore{n: n}
	f.stores[0] = primary
	for i, s := range secondary {
		f.stores[i+1] = s
	}
	return f
}

// WriteAccessLog persists access log records to all stores.
// Returns the error from the primary store; secondary errors are logged.
func (f *FanOutObsStore) WriteAccessLog(ctx context.Context, records []AccessLogRecord) error {
	var primaryErr error
	for i := 0; i < f.n; i++ {
		err := f.stores[i].WriteAccessLog(ctx, records)
		if i == 0 {
			primaryErr = err
		} else if err != nil {
			gatewaylog.Default.Warn("fanout secondary store WriteAccessLog failed", gatewaylog.Fint("store_index", int64(i)), gatewaylog.F("error", err.Error()))
		}
	}
	return primaryErr
}

// WriteMetricSnapshot persists a metric snapshot to all stores.
// Returns the error from the primary store; secondary errors are logged.
func (f *FanOutObsStore) WriteMetricSnapshot(ctx context.Context, snap MetricSnapshot) error {
	var primaryErr error
	for i := 0; i < f.n; i++ {
		err := f.stores[i].WriteMetricSnapshot(ctx, snap)
		if i == 0 {
			primaryErr = err
		} else if err != nil {
			gatewaylog.Default.Warn("fanout secondary store WriteMetricSnapshot failed", gatewaylog.Fint("store_index", int64(i)), gatewaylog.F("error", err.Error()))
		}
	}
	return primaryErr
}

// WriteTraceBatch persists trace records to all stores.
// Returns the error from the primary store; secondary errors are logged.
func (f *FanOutObsStore) WriteTraceBatch(ctx context.Context, records []TraceRecord) error {
	var primaryErr error
	for i := 0; i < f.n; i++ {
		err := f.stores[i].WriteTraceBatch(ctx, records)
		if i == 0 {
			primaryErr = err
		} else if err != nil {
			gatewaylog.Default.Warn("fanout secondary store WriteTraceBatch failed", gatewaylog.Fint("store_index", int64(i)), gatewaylog.F("error", err.Error()))
		}
	}
	return primaryErr
}

// UpsertInstrSchema writes instruction schema rows to all stores.
// Returns the error from the primary store; secondary errors are logged.
func (f *FanOutObsStore) UpsertInstrSchema(ctx context.Context, rows []InstrSchemaRow) error {
	var primaryErr error
	for i := 0; i < f.n; i++ {
		err := f.stores[i].UpsertInstrSchema(ctx, rows)
		if i == 0 {
			primaryErr = err
		} else if err != nil {
			gatewaylog.Default.Warn("fanout secondary store UpsertInstrSchema failed", gatewaylog.Fint("store_index", int64(i)), gatewaylog.F("error", err.Error()))
		}
	}
	return primaryErr
}

// UpsertVarSchema writes variable schema rows to all stores.
// Returns the error from the primary store; secondary errors are logged.
func (f *FanOutObsStore) UpsertVarSchema(ctx context.Context, rows []VarSchemaRow) error {
	var primaryErr error
	for i := 0; i < f.n; i++ {
		err := f.stores[i].UpsertVarSchema(ctx, rows)
		if i == 0 {
			primaryErr = err
		} else if err != nil {
			gatewaylog.Default.Warn("fanout secondary store UpsertVarSchema failed", gatewaylog.Fint("store_index", int64(i)), gatewaylog.F("error", err.Error()))
		}
	}
	return primaryErr
}

// WritePayloadBatch persists payload records to all stores.
// Returns the error from the primary store; secondary errors are logged.
func (f *FanOutObsStore) WritePayloadBatch(ctx context.Context, payloads []PayloadRecord) error {
	var primaryErr error
	for i := 0; i < f.n; i++ {
		err := f.stores[i].WritePayloadBatch(ctx, payloads)
		if i == 0 {
			primaryErr = err
		} else if err != nil {
			gatewaylog.Default.Warn("fanout secondary store WritePayloadBatch failed", gatewaylog.Fint("store_index", int64(i)), gatewaylog.F("error", err.Error()))
		}
	}
	return primaryErr
}

// QueryAccessLog returns access log entries from the primary store.
func (f *FanOutObsStore) QueryAccessLog(ctx context.Context, filter AccessLogFilter) ([]AccessLogRecord, error) {
	return f.stores[0].QueryAccessLog(ctx, filter)
}

// QueryMetrics returns metric snapshots from the primary store.
func (f *FanOutObsStore) QueryMetrics(ctx context.Context, filter MetricsFilter) ([]MetricSnapshot, error) {
	return f.stores[0].QueryMetrics(ctx, filter)
}

// QueryTraces returns trace records from the primary store.
func (f *FanOutObsStore) QueryTraces(ctx context.Context, filter TraceFilter) ([]TraceRecord, error) {
	return f.stores[0].QueryTraces(ctx, filter)
}

// QueryInstrSchema returns instruction schema rows from the primary store.
func (f *FanOutObsStore) QueryInstrSchema(ctx context.Context, apiName string) ([]InstrSchemaRow, error) {
	return f.stores[0].QueryInstrSchema(ctx, apiName)
}

// QueryVarSchema returns variable schema rows from the primary store.
func (f *FanOutObsStore) QueryVarSchema(ctx context.Context, apiName string) ([]VarSchemaRow, error) {
	return f.stores[0].QueryVarSchema(ctx, apiName)
}

// QueryPayloads returns payload records from the primary store.
func (f *FanOutObsStore) QueryPayloads(ctx context.Context, traceID uint64) ([]PayloadRecord, error) {
	return f.stores[0].QueryPayloads(ctx, traceID)
}

// GetDistinctApps returns distinct app names from the primary store.
func (f *FanOutObsStore) GetDistinctApps(ctx context.Context, fromUnixS int64) ([]string, error) {
	return f.stores[0].GetDistinctApps(ctx, fromUnixS)
}

// Close closes all stores and returns the first error encountered.
func (f *FanOutObsStore) Close() error {
	var firstErr error
	for i := 0; i < f.n; i++ {
		err := f.stores[i].Close()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
