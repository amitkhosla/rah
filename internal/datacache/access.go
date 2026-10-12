package datacache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// AppendJSON parses a JSON object and appends it as one record to the named collection.
// Field values are matched by name from the schema; missing fields get zero values.
func (m *DataCacheManager) AppendJSON(name string, jsonObj []byte) error {
	meta, err := m.Get(name)
	if err != nil {
		return err
	}
	if meta.Access == AccessReadOnly {
		return ErrReadOnly
	}
	slot := &m.pool.slots[meta.PoolIdx]

	var obj map[string]any
	if err := json.Unmarshal(jsonObj, &obj); err != nil {
		return err
	}

	// Build values slice aligned to schema fields
	vals := make([]any, len(meta.Schema.Fields))
	for i, f := range meta.Schema.Fields {
		v, ok := obj[f.Name]
		if !ok {
			// zero value per field kind
			switch f.Kind {
			case FieldFloat64:
				vals[i] = float64(0)
			case FieldInt64, FieldEnum:
				vals[i] = int64(0)
			case FieldStringN:
				vals[i] = []byte{}
			}
			continue
		}
		vals[i] = v
	}
	// Evaluate computed fields before eviction: if eval fails, no row is lost.
	if err := evalComputed(meta.ComputedFields, vals); err != nil {
		return err
	}
	// Ring buffer: evict oldest live row when at window capacity.
	if meta.WindowSize > 0 && int(slot.arena.LiveCount.Load()) >= meta.WindowSize {
		evictIdx := int(slot.ringHead.Load()) % meta.WindowSize
		slot.arena.Delete(evictIdx)
		slot.ringHead.Add(1)
	}
	appendErr := slot.arena.Append(vals)
	if appendErr == ErrArenaFull && meta.Growth == GrowthAuto {
		if growErr := m.growCollection(name, meta); growErr == nil {
			appendErr = slot.arena.Append(vals)
		}
	}
	return appendErr
}

// AppendVals appends one record given values in schema field order.
func (m *DataCacheManager) AppendVals(name string, vals []any) error {
	meta, err := m.Get(name)
	if err != nil {
		return err
	}
	if meta.Access == AccessReadOnly {
		return ErrReadOnly
	}
	slot := &m.pool.slots[meta.PoolIdx]
	// Evaluate computed fields before eviction: if eval fails, no row is lost.
	if evalErr := evalComputed(meta.ComputedFields, vals); evalErr != nil {
		return evalErr
	}
	// Ring buffer: evict oldest live row when at window capacity.
	if meta.WindowSize > 0 && int(slot.arena.LiveCount.Load()) >= meta.WindowSize {
		evictIdx := int(slot.ringHead.Load()) % meta.WindowSize
		slot.arena.Delete(evictIdx)
		slot.ringHead.Add(1)
	}
	appendErr := slot.arena.Append(vals)
	if appendErr == ErrArenaFull && meta.Growth == GrowthAuto {
		if growErr := m.growCollection(name, meta); growErr == nil {
			appendErr = slot.arena.Append(vals)
		}
	}
	return appendErr
}

// QueryAll serializes all live (non-deleted) records to a JSON array.
// Returns []byte via ctx.Alloc callback to avoid heap allocation.
func (m *DataCacheManager) QueryAll(name string, alloc func(int) []byte) ([]byte, error) {
	meta, err := m.Get(name)
	if err != nil {
		return nil, err
	}
	slot := &m.pool.slots[meta.PoolIdx]
	arena := &slot.arena

	// Use sort index if a sort has been applied, else scan naturally
	liveCount := int(arena.LiveCount.Load())
	var liveIndices []uint32
	if arena.Schema.SortByIdx >= 0 {
		// SortBy has populated slot.indices[:liveCount] with sorted live rows
		if liveCount > len(slot.indices) {
			return nil, fmt.Errorf("datacache: liveCount %d exceeds indices capacity %d", liveCount, len(slot.indices))
		}
		liveIndices = make([]uint32, liveCount)
		copy(liveIndices, slot.indices[:liveCount])
	} else {
		size := int(arena.Size.Load())
		liveIndices = make([]uint32, 0, liveCount)
		for i := range size {
			if arena.IsLive(i) {
				liveIndices = append(liveIndices, uint32(i))
			}
		}
	}

	// Serialize via encoding/json into a pre-allocated buffer
	result := serializeArenaJSON(arena, liveIndices, meta.Schema.Fields, alloc)
	return result, nil
}

// SortCollection sorts a collection by the given field name (ascending by default, descending if desc=true).
func (m *DataCacheManager) SortCollection(name string, fieldName string, desc bool) error {
	meta, err := m.Get(name)
	if err != nil {
		return err
	}
	// Find field index
	colIdx := findFieldIdx(meta, fieldName)
	if colIdx < 0 {
		return fmt.Errorf("datacache: field %q not found", fieldName)
	}
	m.pool.SortBy(meta.PoolIdx, colIdx)

	// If descending, reverse the indices
	if desc {
		slot := &m.pool.slots[meta.PoolIdx]
		n := int(slot.arena.LiveCount.Load())
		if n > len(slot.indices) {
			return fmt.Errorf("datacache: liveCount %d exceeds indices capacity %d", n, len(slot.indices))
		}
		indices := slot.indices[:n]
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			indices[i], indices[j] = indices[j], indices[i]
		}
	}

	return nil
}

// FilterJSON filters records by a field comparison and serializes matching records to JSON.
// Supported ops: "eq", "ne", "lt", "gt", "lte", "gte".
func (m *DataCacheManager) FilterJSON(name string, fieldName string, op string, compareVal string, alloc func(int) []byte) ([]byte, error) {
	meta, err := m.Get(name)
	if err != nil {
		return nil, err
	}

	colIdx := findFieldIdx(meta, fieldName)
	if colIdx < 0 {
		return nil, fmt.Errorf("datacache: field %q not found", fieldName)
	}

	slot := &m.pool.slots[meta.PoolIdx]
	arena := &slot.arena
	field := meta.Schema.Fields[colIdx]

	// Collect matching row indices
	var matchedIndices []uint32
	size := int(arena.Size.Load())
	for i := range size {
		if !arena.IsLive(i) {
			continue
		}

		matches := false
		switch field.Kind {
		case FieldFloat64:
			fval, _ := strconv.ParseFloat(compareVal, 64)
			actual := arena.Float64(colIdx, i)
			matches = compareFloat(actual, fval, op)
		case FieldInt64:
			ival, _ := strconv.ParseInt(compareVal, 10, 64)
			actual := arena.Int64(colIdx, i)
			matches = compareInt(actual, ival, op)
		case FieldEnum:
			ival, _ := strconv.ParseInt(compareVal, 10, 64)
			actual := int64(arena.Enum(colIdx, i))
			matches = compareInt(actual, ival, op)
		case FieldStringN:
			actual := arena.StringAt(colIdx, i)
			matches = compareString(actual, []byte(compareVal), op)
		}

		if matches {
			matchedIndices = append(matchedIndices, uint32(i))
		}
	}

	// Serialize matched records
	result := serializeArenaJSON(arena, matchedIndices, meta.Schema.Fields, alloc)
	return result, nil
}

// AggregateField computes an aggregate (sum, avg, min, max, count) over a field.
// Supported ops: "sum", "avg", "min", "max", "count".
func (m *DataCacheManager) AggregateField(name string, fieldName string, op string) (float64, error) {
	meta, err := m.Get(name)
	if err != nil {
		return 0, err
	}

	colIdx := findFieldIdx(meta, fieldName)
	if colIdx < 0 {
		return 0, fmt.Errorf("datacache: field %q not found", fieldName)
	}

	slot := &m.pool.slots[meta.PoolIdx]
	arena := &slot.arena
	field := meta.Schema.Fields[colIdx]

	// Iterate and aggregate
	var sum float64
	var min, max float64
	var count int64
	first := true

	size := int(arena.Size.Load())
	for i := range size {
		if !arena.IsLive(i) {
			continue
		}

		var val float64
		switch field.Kind {
		case FieldFloat64:
			val = arena.Float64(colIdx, i)
		case FieldInt64:
			val = float64(arena.Int64(colIdx, i))
		case FieldEnum:
			val = float64(arena.Enum(colIdx, i))
		default:
			return 0, fmt.Errorf("datacache: aggregate not supported for field kind")
		}

		count++
		sum += val

		if first {
			min, max = val, val
			first = false
		} else {
			if val < min {
				min = val
			}
			if val > max {
				max = val
			}
		}
	}

	if count == 0 {
		switch op {
		case "count":
			return 0, nil
		case "avg", "sum":
			return 0, nil
		default:
			return 0, fmt.Errorf("datacache: no data to aggregate")
		}
	}

	switch op {
	case "sum":
		return sum, nil
	case "avg":
		return sum / float64(count), nil
	case "min":
		return min, nil
	case "max":
		return max, nil
	case "count":
		return float64(count), nil
	default:
		return 0, fmt.Errorf("datacache: unsupported aggregate op %q", op)
	}
}

// TrimOldest marks the first n live (non-deleted) records as deleted.
// Returns the actual count of records deleted.
func (m *DataCacheManager) TrimOldest(name string, n int) error {
	meta, err := m.Get(name)
	if err != nil {
		return err
	}

	slot := &m.pool.slots[meta.PoolIdx]
	arena := &slot.arena

	// Walk through records in sort-index order, delete first n live records
	count := 0
	arenaSize := int(arena.Size.Load())
	for i := 0; i < arenaSize && count < n; i++ {
		if arena.IsLive(i) {
			arena.Delete(i)
			count++
		}
	}

	return nil
}

// findFieldIdx returns the column index of a field by name, or -1 if not found.
func findFieldIdx(meta *CollectionMeta, fieldName string) int {
	for i, f := range meta.Schema.Fields {
		if f.Name == fieldName {
			return i
		}
	}
	return -1
}

// serializeArenaJSON builds a JSON array from arena records at the given indices.
// Uses encoding/json.Marshal internally for correctness (not zero-alloc, but these are batch ops).
func serializeArenaJSON(arena *Arena, indices []uint32, fields []FieldDef, alloc func(int) []byte) []byte {
	// Estimate size: roughly 100 bytes per record + overhead
	estimated := 2 + len(indices)*100
	buf := alloc(estimated)
	pos := 0

	buf[pos] = '['
	pos++

	for idx, rowIdx := range indices {
		row := int(rowIdx)

		// Serialize one record as a JSON object
		obj := make(map[string]any)
		for colIdx, field := range fields {
			switch field.Kind {
			case FieldFloat64:
				obj[field.Name] = arena.Float64(colIdx, row)
			case FieldInt64:
				obj[field.Name] = arena.Int64(colIdx, row)
			case FieldEnum:
				obj[field.Name] = arena.Enum(colIdx, row)
			case FieldStringN:
				obj[field.Name] = string(bytes.TrimRight(arena.StringAt(colIdx, row), "\x00"))
			}
		}

		// Marshal to JSON
		jsonBytes, _ := json.Marshal(obj)

		// Ensure buffer has space for the JSON bytes plus comma and closing bracket.
		if pos+len(jsonBytes)+2 > len(buf) {
			needed := pos + len(jsonBytes) + 2
			newSize := max(len(buf)*2, needed)
			newBuf := alloc(newSize)
			copy(newBuf, buf[:pos])
			buf = newBuf
		}

		copy(buf[pos:], jsonBytes)
		pos += len(jsonBytes)

		if idx < len(indices)-1 {
			buf[pos] = ','
			pos++
		}
	}

	buf[pos] = ']'
	pos++

	return buf[:pos]
}

// compareFloat compares two float64 values using the given op.
func compareFloat(a, b float64, op string) bool {
	switch op {
	case "eq":
		return a == b
	case "ne":
		return a != b
	case "lt":
		return a < b
	case "gt":
		return a > b
	case "lte":
		return a <= b
	case "gte":
		return a >= b
	default:
		return false
	}
}

// compareInt compares two int64 values using the given op.
func compareInt(a, b int64, op string) bool {
	switch op {
	case "eq":
		return a == b
	case "ne":
		return a != b
	case "lt":
		return a < b
	case "gt":
		return a > b
	case "lte":
		return a <= b
	case "gte":
		return a >= b
	default:
		return false
	}
}

// compareString compares two byte slices using the given op.
func compareString(a, b []byte, op string) bool {
	cmp := bytes.Compare(a, b)
	switch op {
	case "eq":
		return cmp == 0
	case "ne":
		return cmp != 0
	case "lt":
		return cmp < 0
	case "gt":
		return cmp > 0
	case "lte":
		return cmp <= 0
	case "gte":
		return cmp >= 0
	default:
		return false
	}
}

