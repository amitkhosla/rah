package datacache

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"unsafe"
)

var (
	ErrArenaFull = errors.New("datacache: arena full")
	ErrBadType   = errors.New("datacache: unsupported value type for field")
)

// Arena is a columnar, fixed-capacity in-memory data structure.
// All data is stored in a single contiguous buf []byte (the only GC root).
// Columns are laid out sequentially per the Schema.
type Arena struct {
	buf       []byte        // single GC root — all data lives here
	Schema    Schema        // exported for pool to read
	Size      atomic.Int32  // current record count (exported)
	Cap       int           // max records (exported)
	LiveCount atomic.Int32  // live (non-tombstoned) record count
	freeRows  []uint32      // deleted row indices available for reuse
	freeMu    sync.RWMutex
}

// NewArena creates a new Arena with the given schema and capacity.
func NewArena(schema Schema, capacity int) *Arena {
	return &Arena{
		buf:    make([]byte, schema.BufSize),
		Schema: schema,
		Cap:    capacity,
	}
}

// Float64 returns the float64 value at column col, row i.
// Zero-allocation: pure pointer arithmetic.
func (a *Arena) Float64(col, i int) float64 {
	addr := unsafe.Pointer(&a.buf[a.Schema.ColRegions[col].Offset+i*8])
	return *(*float64)(addr)
}

// SetFloat64 sets the float64 value at column col, row i.
func (a *Arena) SetFloat64(col, i int, v float64) {
	addr := unsafe.Pointer(&a.buf[a.Schema.ColRegions[col].Offset+i*8])
	*(*float64)(addr) = v
}

// Int64 returns the int64 value at column col, row i.
// Zero-allocation: pure pointer arithmetic.
func (a *Arena) Int64(col, i int) int64 {
	addr := unsafe.Pointer(&a.buf[a.Schema.ColRegions[col].Offset+i*8])
	return *(*int64)(addr)
}

// SetInt64 sets the int64 value at column col, row i.
func (a *Arena) SetInt64(col, i int, v int64) {
	addr := unsafe.Pointer(&a.buf[a.Schema.ColRegions[col].Offset+i*8])
	*(*int64)(addr) = v
}

// Enum returns the uint8 enum value at column col, row i.
// Zero-allocation: direct byte access.
func (a *Arena) Enum(col, i int) uint8 {
	return a.buf[a.Schema.ColRegions[col].Offset+i]
}

// SetEnum sets the uint8 enum value at column col, row i.
func (a *Arena) SetEnum(col, i int, v uint8) {
	a.buf[a.Schema.ColRegions[col].Offset+i] = v
}

// StringAt returns a zero-copy sub-slice of the fixed-width string at column col, row i.
// The slice length is exactly the stride of this column.
func (a *Arena) StringAt(col, i int) []byte {
	r := a.Schema.ColRegions[col]
	start := r.Offset + i*r.Stride
	return a.buf[start : start+r.Stride]
}

// SetStringAt copies the value v into the fixed-width string slot at column col, row i.
// If v is shorter than the stride, the remaining bytes are zero-padded.
// If v is longer than the stride, it is truncated.
func (a *Arena) SetStringAt(col, i int, v []byte) {
	r := a.Schema.ColRegions[col]
	start := r.Offset + i*r.Stride
	slot := a.buf[start : start+r.Stride]

	// Zero the slot first
	for j := range slot {
		slot[j] = 0
	}

	// Copy as much as fits
	copyLen := min(len(v), r.Stride)
	copy(slot[:copyLen], v)
}

// IsLive returns true if row i has live (non-deleted) data.
func (a *Arena) IsLive(i int) bool {
	if i < 0 || i >= int(a.Size.Load()) {
		return false
	}
	a.freeMu.RLock()
	defer a.freeMu.RUnlock()
	return !slices.Contains(a.freeRows, uint32(i))
}

// Delete marks row i as deleted, adds it to the free list for reuse, and decrements LiveCount.
// No-op if i is already deleted.
func (a *Arena) Delete(i int) {
	if i < 0 || i >= int(a.Size.Load()) {
		return
	}
	a.freeMu.Lock()
	if slices.Contains(a.freeRows, uint32(i)) {
		a.freeMu.Unlock()
		return
	}
	a.freeRows = append(a.freeRows, uint32(i))
	a.freeMu.Unlock()
	a.LiveCount.Add(-1)
}

// allocRows allocates n new rows from the arena.
// It reuses deleted rows from freeRows first, then extends Size if needed.
// Returns the row indices or ErrArenaFull if capacity is exceeded.
func (a *Arena) allocRows(n int) ([]uint32, error) {
	rows := make([]uint32, n)
	a.freeMu.Lock()

	reused := 0
	for reused < n && len(a.freeRows) > 0 {
		last := len(a.freeRows) - 1
		rows[reused] = a.freeRows[last]
		a.freeRows = a.freeRows[:last]
		reused++
	}

	newNeeded := n - reused
	if newNeeded > 0 {
		oldSize := int(a.Size.Load())
		if oldSize+newNeeded > a.Cap {
			a.freeRows = append(a.freeRows, rows[:reused]...)
			a.freeMu.Unlock()
			return nil, ErrArenaFull
		}
		for i := range newNeeded {
			rows[reused+i] = uint32(oldSize + i)
		}
		a.Size.Store(int32(oldSize + newNeeded))
	}

	a.freeMu.Unlock()
	a.LiveCount.Add(int32(n))
	return rows, nil
}

// writeRow writes the given values to row r without modifying Size, LiveCount, or freeRows.
func (a *Arena) writeRow(r int, vals []any) error {
	if len(vals) != len(a.Schema.Fields) {
		return fmt.Errorf("datacache: expected %d values, got %d", len(a.Schema.Fields), len(vals))
	}

	// Process each field value
	for i, v := range vals {
		switch a.Schema.Fields[i].Kind {
		case FieldFloat64:
			fv, ok := v.(float64)
			if !ok {
				return ErrBadType
			}
			a.SetFloat64(i, r, fv)

		case FieldInt64:
			switch vt := v.(type) {
			case int64:
				a.SetInt64(i, r, vt)
			case float64:
				a.SetInt64(i, r, int64(vt))
			default:
				return ErrBadType
			}

		case FieldEnum:
			switch vt := v.(type) {
			case uint8:
				a.SetEnum(i, r, vt)
			case float64:
				a.SetEnum(i, r, uint8(vt))
			case int64:
				a.SetEnum(i, r, uint8(vt))
			default:
				return ErrBadType
			}

		case FieldStringN:
			var sv []byte
			switch vt := v.(type) {
			case []byte:
				sv = vt
			case string:
				sv = []byte(vt)
			default:
				return ErrBadType
			}
			a.SetStringAt(i, r, sv)
		}
	}

	return nil
}

// Append adds a new record to the arena.
// vals should have len(vals) == len(Schema.Fields).
// Returns ErrArenaFull if Size >= Cap.
// Returns ErrBadType if a value has the wrong type for its field.
func (a *Arena) Append(vals []any) error {
	rows, err := a.allocRows(1)
	if err != nil {
		return err
	}
	if err := a.writeRow(int(rows[0]), vals); err != nil {
		// rollback allocation
		a.freeMu.Lock()
		a.freeRows = append(a.freeRows, rows[0])
		a.freeMu.Unlock()
		a.LiveCount.Add(-1)
		return err
	}
	return nil
}

// AppendBatch adds multiple records to the arena in a single batch.
// Returns ErrArenaFull if the arena cannot accommodate all records.
func (a *Arena) AppendBatch(allVals [][]any) error {
	if len(allVals) == 0 {
		return nil
	}
	// Pre-validate field counts before allocating rows.
	for i, vals := range allVals {
		if len(vals) != len(a.Schema.Fields) {
			return fmt.Errorf("datacache: batch row %d: expected %d values, got %d", i, len(a.Schema.Fields), len(vals))
		}
	}
	rows, err := a.allocRows(len(allVals))
	if err != nil {
		return err
	}
	for i, vals := range allVals {
		if err := a.writeRow(int(rows[i]), vals); err != nil {
			// Roll back all allocated rows.
			a.freeMu.Lock()
			a.freeRows = append(a.freeRows, rows...)
			a.freeMu.Unlock()
			a.LiveCount.Add(-int32(len(allVals)))
			return fmt.Errorf("datacache: batch row %d: %w", i, err)
		}
	}
	return nil
}

// Reset clears the record count and live count, but keeps the buffer and schema.
// Useful for reusing the arena for a new dataset.
func (a *Arena) Reset() {
	a.freeMu.Lock()
	a.freeRows = a.freeRows[:0]
	a.freeMu.Unlock()
	a.Size.Store(0)
	a.LiveCount.Store(0)
	a.Schema.SortByIdx = -1
}
