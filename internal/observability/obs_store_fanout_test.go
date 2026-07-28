package observability

import (
	"context"
	"errors"
	"testing"
)

// stubObsStore is a minimal store implementation for testing.
// It counts calls and can optionally return a configured error.
type stubObsStore struct {
	NoopObsStore
	writeAccessLogCount int
	writeMetricCount    int
	writeTraceCount     int
	writePayloadCount   int
	queryTraceResult    []TraceRecord
	writeErr            error
	closeErr            error
	closeCalled         bool
}

func (s *stubObsStore) WriteAccessLog(_ context.Context, records []AccessLogRecord) error {
	s.writeAccessLogCount += len(records)
	return s.writeErr
}

func (s *stubObsStore) WriteMetricSnapshot(_ context.Context, _ MetricSnapshot) error {
	s.writeMetricCount++
	return s.writeErr
}

func (s *stubObsStore) WriteTraceBatch(_ context.Context, records []TraceRecord) error {
	s.writeTraceCount += len(records)
	return s.writeErr
}

func (s *stubObsStore) WritePayloadBatch(_ context.Context, payloads []PayloadRecord) error {
	s.writePayloadCount += len(payloads)
	return s.writeErr
}

func (s *stubObsStore) QueryTraces(_ context.Context, _ TraceFilter) ([]TraceRecord, error) {
	return s.queryTraceResult, nil
}

func (s *stubObsStore) Close() error {
	s.closeCalled = true
	return s.closeErr
}

func TestFanOutObsStore_WritesFanOut(t *testing.T) {
	primary := &stubObsStore{}
	secondary1 := &stubObsStore{}
	secondary2 := &stubObsStore{}

	f := NewFanOutObsStore(primary, secondary1, secondary2)

	records := []TraceRecord{
		{TraceID: 1, Timestamp: 100},
		{TraceID: 2, Timestamp: 200},
	}

	err := f.WriteTraceBatch(context.Background(), records)
	if err != nil {
		t.Fatalf("WriteTraceBatch failed: %v", err)
	}

	if primary.writeTraceCount != 2 {
		t.Errorf("primary writeTraceCount = %d, want 2", primary.writeTraceCount)
	}
	if secondary1.writeTraceCount != 2 {
		t.Errorf("secondary1 writeTraceCount = %d, want 2", secondary1.writeTraceCount)
	}
	if secondary2.writeTraceCount != 2 {
		t.Errorf("secondary2 writeTraceCount = %d, want 2", secondary2.writeTraceCount)
	}
}

func TestFanOutObsStore_ReadFromPrimary(t *testing.T) {
	primary := &stubObsStore{
		queryTraceResult: []TraceRecord{
			{TraceID: 1, Timestamp: 100},
			{TraceID: 2, Timestamp: 200},
		},
	}
	secondary := &stubObsStore{}

	f := NewFanOutObsStore(primary, secondary)

	result, err := f.QueryTraces(context.Background(), TraceFilter{})
	if err != nil {
		t.Fatalf("QueryTraces failed: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("QueryTraces returned %d records, want 2", len(result))
	}
	if result[0].TraceID != 1 || result[1].TraceID != 2 {
		t.Errorf("QueryTraces returned unexpected records: %v", result)
	}
}

func TestFanOutObsStore_SecondaryErrorNotFatal(t *testing.T) {
	primary := &stubObsStore{}
	secondary := &stubObsStore{writeErr: errors.New("secondary failed")}

	f := NewFanOutObsStore(primary, secondary)

	records := []TraceRecord{
		{TraceID: 1, Timestamp: 100},
	}

	err := f.WriteTraceBatch(context.Background(), records)
	if err != nil {
		t.Fatalf("WriteTraceBatch should not return secondary error, got: %v", err)
	}

	if primary.writeTraceCount != 1 {
		t.Errorf("primary writeTraceCount = %d, want 1", primary.writeTraceCount)
	}
	if secondary.writeTraceCount != 1 {
		t.Errorf("secondary writeTraceCount = %d, want 1", secondary.writeTraceCount)
	}
}

func TestFanOutObsStore_PrimaryErrorFatal(t *testing.T) {
	primaryErr := errors.New("primary failed")
	primary := &stubObsStore{writeErr: primaryErr}
	secondary := &stubObsStore{}

	f := NewFanOutObsStore(primary, secondary)

	records := []TraceRecord{
		{TraceID: 1, Timestamp: 100},
	}

	err := f.WriteTraceBatch(context.Background(), records)
	if err != primaryErr {
		t.Fatalf("WriteTraceBatch should return primary error, got: %v", err)
	}
}

func TestFanOutObsStore_Close(t *testing.T) {
	primary := &stubObsStore{}
	secondary1 := &stubObsStore{}
	secondary2 := &stubObsStore{}

	f := NewFanOutObsStore(primary, secondary1, secondary2)

	err := f.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if !primary.closeCalled {
		t.Error("primary Close not called")
	}
	if !secondary1.closeCalled {
		t.Error("secondary1 Close not called")
	}
	if !secondary2.closeCalled {
		t.Error("secondary2 Close not called")
	}
}

func TestFanOutObsStore_CloseReturnsFirstError(t *testing.T) {
	primary := &stubObsStore{}
	secondaryErr1 := errors.New("secondary1 failed")
	secondary1 := &stubObsStore{closeErr: secondaryErr1}
	secondaryErr2 := errors.New("secondary2 failed")
	secondary2 := &stubObsStore{closeErr: secondaryErr2}

	f := NewFanOutObsStore(primary, secondary1, secondary2)

	err := f.Close()
	if err != secondaryErr1 {
		t.Fatalf("Close should return first error, got: %v, want: %v", err, secondaryErr1)
	}

	if !primary.closeCalled {
		t.Error("primary Close not called")
	}
	if !secondary1.closeCalled {
		t.Error("secondary1 Close not called")
	}
	if !secondary2.closeCalled {
		t.Error("secondary2 Close not called")
	}
}

func TestFanOutObsStore_PanicOnNilPrimary(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil primary, but did not panic")
		}
	}()
	_ = NewFanOutObsStore(nil)
}

func TestFanOutObsStore_PanicOnTooManyStores(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for too many stores, but did not panic")
		}
	}()
	primary := &stubObsStore{}
	stores := make([]ObsStore, maxFanOutStores)
	for i := range stores {
		stores[i] = &stubObsStore{}
	}
	_ = NewFanOutObsStore(primary, stores...)
}

func TestFanOutObsStore_WriteAccessLogFansOut(t *testing.T) {
	primary := &stubObsStore{}
	secondary := &stubObsStore{}

	f := NewFanOutObsStore(primary, secondary)

	records := []AccessLogRecord{
		{ApiName: "test", TenantID: 1},
		{ApiName: "test", TenantID: 2},
	}

	err := f.WriteAccessLog(context.Background(), records)
	if err != nil {
		t.Fatalf("WriteAccessLog failed: %v", err)
	}

	if primary.writeAccessLogCount != 2 {
		t.Errorf("primary writeAccessLogCount = %d, want 2", primary.writeAccessLogCount)
	}
	if secondary.writeAccessLogCount != 2 {
		t.Errorf("secondary writeAccessLogCount = %d, want 2", secondary.writeAccessLogCount)
	}
}

func TestFanOutObsStore_WritePayloadBatchFansOut(t *testing.T) {
	primary := &stubObsStore{}
	secondary := &stubObsStore{}

	f := NewFanOutObsStore(primary, secondary)

	payloads := []PayloadRecord{
		{TraceID: 1, Kind: 1},
		{TraceID: 1, Kind: 2},
	}

	err := f.WritePayloadBatch(context.Background(), payloads)
	if err != nil {
		t.Fatalf("WritePayloadBatch failed: %v", err)
	}

	if primary.writePayloadCount != 2 {
		t.Errorf("primary writePayloadCount = %d, want 2", primary.writePayloadCount)
	}
	if secondary.writePayloadCount != 2 {
		t.Errorf("secondary writePayloadCount = %d, want 2", secondary.writePayloadCount)
	}
}
