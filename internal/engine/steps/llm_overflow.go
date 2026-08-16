package steps

import (
	"context"
	"time"

	"github.com/amitkhosla/rah/internal/datastore"
	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// â"€â"€â"€ overflow_history â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// OverflowHistoryConfig controls the overflow_history instruction.
type OverflowHistoryConfig struct {
	HistorySlot  int                   // ByteSlots index: JSON-encoded []CanonicalMessage, trimmed in place
	KeySlot      int                   // ByteSlots index: conversation key (e.g. session ID)
	OverflowSlot int                   // IntSlots index: token budget from check_context_fit; -1 = use MaxTokens/MaxTurns
	MaxTokens    int                   // fallback token budget when OverflowSlot < 0 (0 = no limit, noop)
	MaxTurns     int                   // fallback turn limit when OverflowSlot < 0 (0 = no limit)
	Domain       string                // datastore domain for overflow storage
	Store        datastore.KeyValueStore // resolved at bake time; nil = trim only, no store write
	TTLSecs      int                   // TTL for overflow entries (0 = no TTL)
}

// OverflowHistory moves the oldest messages that exceed the configured budget
// into persistent datastore overflow storage, keeping only the fitting tail in
// HistorySlot. A companion LoadOverflowHistory step can prepend them back when
// needed.
//
// Budget precedence:
//  1. OverflowSlot >= 0: read overflowTokens from ctx.IntSlots[OverflowSlot].
//     Trim oldest until remaining tokens fit (total - overflowTokens).
//  2. MaxTokens > 0: trim via token budget.
//  3. MaxTurns > 0: trim via turn-pair count.
//  4. Neither set: noop.
//
// If Store is nil, overflow messages are silently discarded (just trimmed).
// If the store write fails, the error is ignored — the history slot is still
// trimmed so the request can continue.
func OverflowHistory(cfg OverflowHistoryConfig) engine.Instruction {
	return engine.Instruction{
		Name: "overflow_history",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			msgs := decodeHistory(ctx.ByteSlots[cfg.HistorySlot])
			if len(msgs) == 0 {
				return state.PC + 1
			}

			var trimmed []CanonicalMessage

			if cfg.OverflowSlot >= 0 && cfg.OverflowSlot < len(ctx.IntSlots) {
				// Budget driven by a slot value (e.g. from check_context_fit).
				overflowTokens := int(ctx.IntSlots[cfg.OverflowSlot])
				if overflowTokens <= 0 {
					// Nothing overflows — history already fits.
					return state.PC + 1
				}
				// Trim oldest messages until total tokens drop by at least overflowTokens.
				// We remove from the front until the number of removed tokens >= overflowTokens.
				removed := 0
				cutPoint := 0
				for cutPoint < len(msgs) && removed < overflowTokens {
					removed += estimateTokens(msgs[cutPoint].Content) + 4
					cutPoint++
				}
				trimmed = msgs[:cutPoint]
				msgs = msgs[cutPoint:]
			} else if cfg.MaxTokens > 0 {
				after := trimTokens(msgs, cfg.MaxTokens)
				cutPoint := len(msgs) - len(after)
				trimmed = msgs[:cutPoint]
				msgs = after
			} else if cfg.MaxTurns > 0 {
				after := trimTurns(msgs, cfg.MaxTurns)
				cutPoint := len(msgs) - len(after)
				trimmed = msgs[:cutPoint]
				msgs = after
			} else {
				// No budget configured — noop.
				return state.PC + 1
			}

			// Persist overflow (oldest first) if there is something to store.
			if cfg.Store != nil && len(trimmed) > 0 {
				rawKey := ctx.ByteSlots[cfg.KeySlot]
				if len(rawKey) > 0 {
					overflowKey := string(rawKey) + ":overflow"
					tenant := datastore.Tenant(ctx.TenantKey)

					// Load any existing overflow so we can prepend the new prefix
					// after it (oldest entries stay oldest).
					var existing []CanonicalMessage
					if raw, found, err := cfg.Store.Get(context.Background(), tenant, overflowKey); err == nil && found {
						existing = decodeHistory(raw)
					}

					// Build combined: existing (oldest) + new overflow.
					combined := make([]CanonicalMessage, 0, len(existing)+len(trimmed))
					combined = append(combined, existing...)
					combined = append(combined, trimmed...)

					encoded := encodeHistory(combined)
					if cfg.TTLSecs > 0 {
						if exp, ok := cfg.Store.(datastore.ExpiringStore); ok {
							_ = exp.PutWithTTL(context.Background(), tenant, overflowKey, encoded,
								time.Duration(cfg.TTLSecs)*time.Second)
						} else {
							_ = cfg.Store.Put(context.Background(), tenant, overflowKey, encoded)
						}
					} else {
						_ = cfg.Store.Put(context.Background(), tenant, overflowKey, encoded)
					}
				}
			}

			// Write the trimmed (fitting) history back to the slot.
			writeHistorySlot(ctx, state, cfg.HistorySlot, msgs)
			return state.PC + 1
		},
	}
}

// â"€â"€â"€ load_overflow_history â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// LoadOverflowHistoryConfig controls the load_overflow_history instruction.
type LoadOverflowHistoryConfig struct {
	HistorySlot int                    // ByteSlots index: JSON-encoded []CanonicalMessage — overflow is prepended
	KeySlot     int                    // ByteSlots index: conversation key
	MaxTurns    int                    // if > 0, keep only the last MaxTurns pairs from overflow (most recent)
	Domain      string                 // datastore domain
	Store       datastore.KeyValueStore // resolved at bake time; nil = noop
}

// LoadOverflowHistory retrieves overflow history from the datastore and
// prepends it to the current history in HistorySlot.  If no overflow exists
// or the store is nil the slot is left unchanged.
//
// If MaxTurns > 0 only the most recent MaxTurns turn-pairs of the overflow
// are prepended (trimTurns applied to overflow before merging).
func LoadOverflowHistory(cfg LoadOverflowHistoryConfig) engine.Instruction {
	return engine.Instruction{
		Name: "load_overflow_history",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if cfg.Store == nil {
				return state.PC + 1
			}
			rawKey := ctx.ByteSlots[cfg.KeySlot]
			if len(rawKey) == 0 {
				return state.PC + 1
			}

			overflowKey := string(rawKey) + ":overflow"
			tenant := datastore.Tenant(ctx.TenantKey)

			raw, found, err := cfg.Store.Get(context.Background(), tenant, overflowKey)
			if err != nil || !found {
				return state.PC + 1
			}

			overflow := decodeHistory(raw)
			if len(overflow) == 0 {
				return state.PC + 1
			}

			// Optionally limit how much overflow we prepend to the most recent pairs.
			if cfg.MaxTurns > 0 {
				overflow = trimTurns(overflow, cfg.MaxTurns)
			}

			current := decodeHistory(ctx.ByteSlots[cfg.HistorySlot])

			merged := make([]CanonicalMessage, 0, len(overflow)+len(current))
			merged = append(merged, overflow...)
			merged = append(merged, current...)

			writeHistorySlot(ctx, state, cfg.HistorySlot, merged)
			return state.PC + 1
		},
	}
}
