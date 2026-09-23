package steps

import (
	"bytes"
	"context"
	"encoding/binary"
	"log"
	"sort"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// ColBinding maps a DB column name (resolved to a column index at runtime) to a
// destination ByteSlot index (resolved at bake time).
type ColBinding struct {
	Name    string // DB column name
	SlotIdx int    // destination ByteSlot index
}

// dbCursorStateExt holds all Phase 1 output for one db_foreach execution.
// Stored in ctx.Cursors[cursorIdx] — per-request, never shared between requests.
type dbCursorStateExt struct {
	buf         []byte  // packed binary rows, arena-allocated
	rowOffsets  []int32 // rowOffsets[i] = byte offset to row i in buf
	rowCount    int32
	positions   []int // positions[i] = column index in result for bound column i
	slotIndices []int // slotIndices[i] = ByteSlot index for bound column i
}

// rowBufPool pools *bytes.Buffer for Phase 1 row accumulation.
// Returned to pool after contents are copied to the arena.
var rowBufPool = sync.Pool{New: func() any { return &bytes.Buffer{} }}

// dbForeachArgsPool pools []any for pgx query args — avoids per-call allocation.
// Separate from control.pgxArgsPool to avoid a circular import.
var dbForeachArgsPool = sync.Pool{New: func() any { s := make([]any, 0, 8); return &s }}

// fieldDesc is a minimal column name carrier; avoids importing pgconn here.
type fieldDesc struct{ name string }

// buildPositions returns positions[i] = column index in fields for colBindings[i].Name.
// Returns -1 for unmatched names. Called once per query execution.
func buildPositions(colBindings []ColBinding, fields []fieldDesc) []int {
	positions := make([]int, len(colBindings))
	for i, cb := range colBindings {
		positions[i] = -1
		for j, f := range fields {
			if f.name == cb.Name {
				positions[i] = j
				break
			}
		}
	}
	return positions
}

// DbForeachGate returns an InstructionFunc that implements db_foreach in two phases.
//
// Phase 1 — first entry (ctx.Cursors[cursorIdx] == nil):
//   - Executes the SQL query.
//   - Reads every row via rows.RawValues() — zero-alloc; bytes are sub-slices of pgx's
//     internal network buffer, valid only until the next rows.Next() call.
//   - Packs all row data into a compact length-prefixed binary buffer (arena-allocated).
//   - Calls rows.Close() — DB connection returned to pool before any do-block executes.
//   - Stores *dbCursorStateExt in ctx.Cursors[cursorIdx].
//
// Phase 2 — subsequent entries (per-row, pure in-memory):
//   - Reads the next row from the binary buffer.
//   - Assigns bound columns as zero-copy sub-slices of the arena buffer into ByteSlots.
//   - Returns bodyStart; LoopRepeat increments iterSlot.
//   - When all rows consumed: resets iterSlot, nils the cursor, returns exitID.
//
// Binary row format (per column):
//
//	[val_len: uint16 LE][val_bytes]   val_len=0xFFFF → NULL → nil slot
func DbForeachGate(
	pool *pgxpool.Pool,
	cursorIdx int,
	sql string,
	paramSlots []int,
	colBindings []ColBinding,
	iterSlot int,
	bodyStart, exitID int16,
) engine.InstructionFunc {
	// Sort a bake-time copy by Name for deterministic behaviour regardless of
	// map-iteration order in the compiler. Phase 1 re-sorts by column position.
	sorted := make([]ColBinding, len(colBindings))
	copy(sorted, colBindings)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	return func(ctx *rctx.Context, state *engine.ExecutionState) int16 {

		// ── Phase 1: materialise ─────────────────────────────────────────────────
		if ctx.Cursors[cursorIdx] == nil {
			if pool == nil {
				log.Printf("[db_foreach] DataSourcePool is nil — no data_sources configured")
				return state.PC + 1
			}

			argsp := dbForeachArgsPool.Get().(*[]any)
			args := (*argsp)[:0]
			for _, idx := range paramSlots {
				if idx >= 0 && idx < len(ctx.ByteSlots) {
					args = append(args, ctx.ByteSlots[idx])
				}
			}
			rows, err := pool.Query(context.Background(), sql, args...)
			*argsp = args[:0]
			dbForeachArgsPool.Put(argsp)
			if err != nil {
				log.Printf("[db_foreach] query error: %v", err)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return -1
			}

			// Build column descriptor slice once — before any Next() call.
			pgFields := rows.FieldDescriptions()
			fields := make([]fieldDesc, len(pgFields))
			for i, f := range pgFields {
				fields[i] = fieldDesc{name: f.Name}
			}
			positions := buildPositions(sorted, fields)

			// Build bound-column list sorted by column position for left-to-right
			// single-pass decoding in Phase 2.
			type boundCol struct{ pos, slotIdx int }
			bound := make([]boundCol, 0, len(sorted))
			for i, cb := range sorted {
				if positions[i] >= 0 {
					bound = append(bound, boundCol{positions[i], cb.SlotIdx})
				}
			}
			sort.Slice(bound, func(i, j int) bool { return bound[i].pos < bound[j].pos })

			// Accumulate packed rows into pooled temp buffer.
			tmp := rowBufPool.Get().(*bytes.Buffer)
			tmp.Reset()
			var rowOffsets []int32
			var nrows int32

			for rows.Next() {
				rowOffsets = append(rowOffsets, int32(tmp.Len()))
				for _, rb := range rows.RawValues() { // zero-alloc pgx API
					if rb == nil {
						tmp.Write([]byte{0xFF, 0xFF})
					} else {
						var lbuf [2]byte
						binary.LittleEndian.PutUint16(lbuf[:], uint16(len(rb)))
						tmp.Write(lbuf[:])
						tmp.Write(rb)
					}
				}
				nrows++
			}
			rows.Close() // ← DB connection returned to pool HERE

			if err := rows.Err(); err != nil {
				rowBufPool.Put(tmp)
				log.Printf("[db_foreach] row scan error: %v", err)
				ctx.ResponseStatus = 500
				ctx.Failed = true
				return -1
			}

			// Copy into arena — no GC for results ≤ 5KB (inline + ext block).
			buf := ctx.Alloc(tmp.Len())
			copy(buf, tmp.Bytes())
			rowBufPool.Put(tmp)

			bpos := make([]int, len(bound))
			bslot := make([]int, len(bound))
			for i, bc := range bound {
				bpos[i] = bc.pos
				bslot[i] = bc.slotIdx
			}
			ctx.Cursors[cursorIdx] = &dbCursorStateExt{
				buf:         buf,
				rowOffsets:  rowOffsets,
				rowCount:    nrows,
				positions:   bpos,
				slotIndices: bslot,
			}
		}

		// ── Phase 2: iterate binary buffer ───────────────────────────────────────
		cs := ctx.Cursors[cursorIdx].(*dbCursorStateExt)
		rowIdx := ctx.GetInt(iterSlot)

		if rowIdx >= int64(cs.rowCount) {
			ctx.SetInt(iterSlot, 0)
			ctx.Cursors[cursorIdx] = nil
			return exitID
		}

		// Single forward pass through the packed row, extracting only bound columns.
		cur := int(cs.rowOffsets[rowIdx])
		prevPos := 0
		for i, pos := range cs.positions {
			for col := prevPos; col < pos; col++ {
				if cur+2 > len(cs.buf) {
					break
				}
				vlen := binary.LittleEndian.Uint16(cs.buf[cur:])
				cur += 2
				if vlen != 0xFFFF {
					cur += int(vlen)
				}
			}
			if cur+2 > len(cs.buf) {
				ctx.ByteSlots[cs.slotIndices[i]] = nil
				prevPos = pos + 1
				continue
			}
			vlen := binary.LittleEndian.Uint16(cs.buf[cur:])
			cur += 2
			if vlen == 0xFFFF {
				ctx.ByteSlots[cs.slotIndices[i]] = nil
			} else {
				ctx.ByteSlots[cs.slotIndices[i]] = cs.buf[cur : cur+int(vlen)] // zero-copy
				cur += int(vlen)
			}
			prevPos = pos + 1
		}

		return bodyStart
		// LoopRepeat will increment the iterSlot counter via ctx.SetInt.
	}
}
