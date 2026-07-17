package steps

import (
	"strconv"
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// CurrentTimestampStep stores the current time into ByteSlots[slot] as ASCII bytes.
//
// Formats:
//   - "unix_s"   â€” seconds since epoch  (e.g. "1748477823")
//   - "unix_ms"  â€” milliseconds          (e.g. "1748477823451")
//   - "unix_ns"  â€” nanoseconds           (e.g. "1748477823451000000")
//   - "rfc3339"  â€” "2006-01-02T15:04:05Z" (UTC)
//   - ""         â€” defaults to "unix_ms"
//
// nowSec is optional: when non-nil it is called for "unix_s" instead of
// time.Now(), reusing the CacheManager's coarse clock (one atomic load, ~1 ns).
// For all other formats time.Now() is called once (~20 ns).
//
// Output bytes are allocated from the request arena â€” zero heap allocation.
func CurrentTimestampStep(slot int, format string, nowSec func() uint32) engine.Instruction {
	if format == "" {
		format = "unix_ms"
	}
	return engine.Instruction{
		Name: "CURRENT_TIMESTAMP",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			var buf []byte

			switch format {
			case "unix_s":
				var sec uint32
				if nowSec != nil {
					sec = nowSec()
				} else {
					sec = uint32(time.Now().Unix())
				}
				b := ctx.Alloc(12)
				n := strconv.AppendUint(b[:0], uint64(sec), 10)
				buf = n

			case "unix_ns":
				ns := time.Now().UnixNano()
				b := ctx.Alloc(22)
				buf = strconv.AppendInt(b[:0], ns, 10)

			case "rfc3339":
				t := time.Now().UTC()
				s := t.Format(time.RFC3339)
				b := ctx.Alloc(len(s))
				copy(b, s)
				buf = b

			default: // "unix_ms"
				ms := time.Now().UnixMilli()
				b := ctx.Alloc(16)
				buf = strconv.AppendInt(b[:0], ms, 10)
			}

			ctx.ByteSlots[slot] = buf
			return state.PC + 1
		},
	}
}
