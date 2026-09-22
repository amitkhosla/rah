package steps

import (
	"bytes"
	"encoding/binary"
	"testing"
	"unsafe"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

func TestBuildPositions_AllFound(t *testing.T) {
	colBindings := []ColBinding{
		{Name: "a", SlotIdx: 10},
		{Name: "b", SlotIdx: 11},
		{Name: "c", SlotIdx: 12},
	}
	fields := []fieldDesc{{name: "a"}, {name: "b"}, {name: "c"}}

	positions := buildPositions(colBindings, fields)

	if len(positions) != 3 {
		t.Fatalf("expected 3 positions, got %d", len(positions))
	}
	if positions[0] != 0 || positions[1] != 1 || positions[2] != 2 {
		t.Errorf("positions: got %v, want [0 1 2]", positions)
	}
}

func TestBuildPositions_SubsetAndReorder(t *testing.T) {
	colBindings := []ColBinding{
		{Name: "c", SlotIdx: 20},
		{Name: "a", SlotIdx: 21},
	}
	fields := []fieldDesc{{name: "a"}, {name: "b"}, {name: "c"}}

	positions := buildPositions(colBindings, fields)

	if len(positions) != 2 {
		t.Fatalf("expected 2 positions, got %d", len(positions))
	}
	if positions[0] != 2 || positions[1] != 0 {
		t.Errorf("positions: got %v, want [2 0]", positions)
	}
}

func TestBuildPositions_ColumnNotFound(t *testing.T) {
	colBindings := []ColBinding{
		{Name: "a", SlotIdx: 30},
		{Name: "x", SlotIdx: 31},
		{Name: "b", SlotIdx: 32},
	}
	fields := []fieldDesc{{name: "a"}, {name: "b"}}

	positions := buildPositions(colBindings, fields)

	if len(positions) != 3 {
		t.Fatalf("expected 3 positions, got %d", len(positions))
	}
	if positions[0] != 0 || positions[1] != -1 || positions[2] != 1 {
		t.Errorf("positions: got %v, want [0 -1 1]", positions)
	}
}

func TestBuildPositions_EmptyBindings(t *testing.T) {
	positions := buildPositions([]ColBinding{}, []fieldDesc{{name: "a"}, {name: "b"}})
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func makeRow(vals []string) []byte {
	var buf bytes.Buffer
	for _, v := range vals {
		var lbuf [2]byte
		binary.LittleEndian.PutUint16(lbuf[:], uint16(len(v)))
		buf.Write(lbuf[:])
		buf.WriteString(v)
	}
	return buf.Bytes()
}

func makeRowWithNull(vals []any) []byte {
	var buf bytes.Buffer
	for _, v := range vals {
		if v == nil {
			buf.Write([]byte{0xFF, 0xFF})
		} else {
			s := v.(string)
			var lbuf [2]byte
			binary.LittleEndian.PutUint16(lbuf[:], uint16(len(s)))
			buf.Write(lbuf[:])
			buf.WriteString(s)
		}
	}
	return buf.Bytes()
}

func makeCursorState(rows [][]string, positions, slotIndices []int) *dbCursorStateExt {
	var buf bytes.Buffer
	offsets := make([]int32, len(rows))
	for i, row := range rows {
		offsets[i] = int32(buf.Len())
		buf.Write(makeRow(row))
	}
	bpos := make([]int, len(positions))
	copy(bpos, positions)
	bslot := make([]int, len(slotIndices))
	copy(bslot, slotIndices)
	return &dbCursorStateExt{
		buf:         buf.Bytes(),
		rowOffsets:  offsets,
		rowCount:    int32(len(rows)),
		positions:   bpos,
		slotIndices: bslot,
	}
}

func makeCursorStateWithNull(rows [][]any, positions, slotIndices []int) *dbCursorStateExt {
	var buf bytes.Buffer
	offsets := make([]int32, len(rows))
	for i, row := range rows {
		offsets[i] = int32(buf.Len())
		buf.Write(makeRowWithNull(row))
	}
	bpos := make([]int, len(positions))
	copy(bpos, positions)
	bslot := make([]int, len(slotIndices))
	copy(bslot, slotIndices)
	return &dbCursorStateExt{
		buf:         buf.Bytes(),
		rowOffsets:  offsets,
		rowCount:    int32(len(rows)),
		positions:   bpos,
		slotIndices: bslot,
	}
}

// ── Phase 2 tests (nil pool — cursor already populated) ──────────────────────

func TestDbForeachGate_Phase2_TwoRows(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	cs := makeCursorState([][]string{{"alice", "123"}, {"bob", "456"}}, []int{0, 1}, []int{2, 3})
	ctx.Cursors[0] = cs

	gate := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	state := &engine.ExecutionState{}

	if pc := gate(ctx, state); pc != 10 || string(ctx.ByteSlots[2]) != "alice" || string(ctx.ByteSlots[3]) != "123" {
		t.Errorf("row 0: pc=%d slot2=%q slot3=%q", pc, ctx.ByteSlots[2], ctx.ByteSlots[3])
	}
	ctx.IntSlots[0]++
	if pc := gate(ctx, state); pc != 10 || string(ctx.ByteSlots[2]) != "bob" || string(ctx.ByteSlots[3]) != "456" {
		t.Errorf("row 1: pc=%d slot2=%q slot3=%q", pc, ctx.ByteSlots[2], ctx.ByteSlots[3])
	}
	ctx.IntSlots[0]++
	if pc := gate(ctx, state); pc != 99 || ctx.IntSlots[0] != 0 || ctx.Cursors[0] != nil {
		t.Errorf("exit: pc=%d iterSlot=%d cursor=%v", pc, ctx.IntSlots[0], ctx.Cursors[0])
	}
}

func TestDbForeachGate_Phase2_NullColumn(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	cs := makeCursorStateWithNull([][]any{{nil, "value"}}, []int{0, 1}, []int{2, 3})
	ctx.Cursors[0] = cs

	gate := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	state := &engine.ExecutionState{}

	if pc := gate(ctx, state); pc != 10 || ctx.ByteSlots[2] != nil || string(ctx.ByteSlots[3]) != "value" {
		t.Errorf("pc=%d slot2=%v slot3=%q", pc, ctx.ByteSlots[2], ctx.ByteSlots[3])
	}
}

func TestDbForeachGate_Phase2_ZeroRows(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	ctx.Cursors[0] = makeCursorState(nil, []int{0}, []int{2})

	gate := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	state := &engine.ExecutionState{}

	if pc := gate(ctx, state); pc != 99 || ctx.Cursors[0] != nil || ctx.IntSlots[0] != 0 {
		t.Errorf("pc=%d cursor=%v iterSlot=%d", pc, ctx.Cursors[0], ctx.IntSlots[0])
	}
}

func TestDbForeachGate_Phase2_UnboundColumnSkipped(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	// 3-column row; only column 2 is bound
	cs := makeCursorState([][]string{{"col0", "col1", "target"}}, []int{2}, []int{2})
	ctx.Cursors[0] = cs

	gate := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	state := &engine.ExecutionState{}

	if pc := gate(ctx, state); pc != 10 || string(ctx.ByteSlots[2]) != "target" {
		t.Errorf("pc=%d slot2=%q", pc, ctx.ByteSlots[2])
	}
}

func TestDbForeachGate_Phase2_ZeroCopySlice(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	cs := makeCursorState([][]string{{"data"}}, []int{0}, []int{2})
	ctx.Cursors[0] = cs

	gate := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	gate(ctx, &engine.ExecutionState{})

	slot := ctx.ByteSlots[2]
	if len(slot) == 0 {
		t.Fatal("slot is empty")
	}
	slotPtr := uintptr(unsafe.Pointer(&slot[0]))
	bufPtr := uintptr(unsafe.Pointer(&cs.buf[0]))
	bufEnd := uintptr(unsafe.Pointer(&cs.buf[len(cs.buf)-1]))
	if slotPtr < bufPtr || slotPtr > bufEnd {
		t.Errorf("slot[0] (%x) not inside cs.buf [%x,%x]", slotPtr, bufPtr, bufEnd)
	}
}

func TestDbForeachGate_TwoCursors_Independent(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	ctx.Cursors[0] = makeCursorState([][]string{{"x"}}, []int{0}, []int{2})
	ctx.Cursors[1] = makeCursorState([][]string{{"y"}}, []int{0}, []int{3})

	gate0 := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	gate1 := DbForeachGate(nil, 1, "", nil, nil, 1, 20, 99)
	state := &engine.ExecutionState{}

	if pc := gate0(ctx, state); pc != 10 || string(ctx.ByteSlots[2]) != "x" || ctx.ByteSlots[3] != nil {
		t.Errorf("gate0: pc=%d s2=%q s3=%v", pc, ctx.ByteSlots[2], ctx.ByteSlots[3])
	}
	if pc := gate1(ctx, state); pc != 20 || string(ctx.ByteSlots[3]) != "y" || string(ctx.ByteSlots[2]) != "x" {
		t.Errorf("gate1: pc=%d s3=%q s2=%q", pc, ctx.ByteSlots[3], ctx.ByteSlots[2])
	}
}

func TestDbForeachGate_NilPool_Phase2Proceeds(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	ctx.Cursors[0] = makeCursorState([][]string{{"data1"}}, []int{0}, []int{2})

	gate := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	state := &engine.ExecutionState{}

	if pc := gate(ctx, state); pc != 10 || string(ctx.ByteSlots[2]) != "data1" {
		t.Errorf("pc=%d slot2=%q", pc, ctx.ByteSlots[2])
	}
}

func TestDbForeachGate_ComplexReorder(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	// 4-column row; bind columns 0→slot12, 1→slot11, 3→slot10 (positions already sorted)
	cs := makeCursorState([][]string{{"a", "b", "c", "d"}}, []int{0, 1, 3}, []int{12, 11, 10})
	ctx.Cursors[0] = cs

	gate := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	state := &engine.ExecutionState{}

	gate(ctx, state)
	if string(ctx.ByteSlots[12]) != "a" || string(ctx.ByteSlots[11]) != "b" || string(ctx.ByteSlots[10]) != "d" {
		t.Errorf("s12=%q s11=%q s10=%q", ctx.ByteSlots[12], ctx.ByteSlots[11], ctx.ByteSlots[10])
	}
}

func TestDbForeachGate_EmptyStrings(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	cs := makeCursorState([][]string{{"", "non-empty"}}, []int{0, 1}, []int{2, 3})
	ctx.Cursors[0] = cs

	gate := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	gate(ctx, &engine.ExecutionState{})

	if len(ctx.ByteSlots[2]) != 0 || string(ctx.ByteSlots[3]) != "non-empty" {
		t.Errorf("s2_len=%d s3=%q", len(ctx.ByteSlots[2]), ctx.ByteSlots[3])
	}
}

func TestDbForeachGate_ManyRows(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	rows := make([][]string, 10)
	for i := range rows {
		rows[i] = []string{"value" + string(rune('0'+i))}
	}
	ctx.Cursors[0] = makeCursorState(rows, []int{0}, []int{2})

	gate := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	state := &engine.ExecutionState{}

	for i := 0; i < 10; i++ {
		want := "value" + string(rune('0'+i))
		if pc := gate(ctx, state); pc != 10 || string(ctx.ByteSlots[2]) != want {
			t.Errorf("row %d: pc=%d slot=%q want %q", i, pc, ctx.ByteSlots[2], want)
		}
		ctx.IntSlots[0]++
	}
	if pc := gate(ctx, state); pc != 99 || ctx.Cursors[0] != nil {
		t.Errorf("exit: pc=%d cursor=%v", pc, ctx.Cursors[0])
	}
}

func TestDbForeachGate_LargeValues(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	largeStr := string(make([]byte, 1024))
	for i := range []byte(largeStr) {
		largeStr = largeStr[:i] + "x" + largeStr[i+1:]
	}

	ctx.Cursors[0] = makeCursorState([][]string{{largeStr}}, []int{0}, []int{2})

	gate := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	if pc := gate(ctx, &engine.ExecutionState{}); pc != 10 || string(ctx.ByteSlots[2]) != largeStr {
		t.Errorf("pc=%d large value mismatch (len=%d)", pc, len(ctx.ByteSlots[2]))
	}
}

func TestDbForeachGate_AllNull(t *testing.T) {
	ctx := &rctx.Context{}
	ctx.InitSlots()

	cs := makeCursorStateWithNull([][]any{{nil, nil, nil}}, []int{0, 1, 2}, []int{2, 3, 4})
	ctx.Cursors[0] = cs

	gate := DbForeachGate(nil, 0, "", nil, nil, 0, 10, 99)
	if pc := gate(ctx, &engine.ExecutionState{}); pc != 10 || ctx.ByteSlots[2] != nil || ctx.ByteSlots[3] != nil || ctx.ByteSlots[4] != nil {
		t.Errorf("pc=%d all slots should be nil", pc)
	}
}
