package steps

import (
	"context"
	"encoding/json"
	"time"

	"rah/internal/datastore"
	"rah/internal/engine"
	"rah/internal/rctx"
)

// emptyHistory is the canonical JSON encoding of an empty message slice.
var emptyHistory = []byte("[]")

// decodeHistory decodes a JSON-encoded []CanonicalMessage from a byte slice.
// Returns an empty slice on nil, empty input, or a decode error.
func decodeHistory(raw []byte) []CanonicalMessage {
	if len(raw) == 0 {
		return nil
	}
	var msgs []CanonicalMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return nil
	}
	return msgs
}

// encodeHistory JSON-encodes a []CanonicalMessage. Falls back to emptyHistory
// on encode error (should never happen with well-formed data).
func encodeHistory(msgs []CanonicalMessage) []byte {
	if len(msgs) == 0 {
		return emptyHistory
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		return emptyHistory
	}
	return b
}

// trimTurns removes oldest messages from the front until at most maxTurns
// complete user/assistant turn pairs remain.
// If maxTurns <= 0 the slice is returned unchanged.
func trimTurns(msgs []CanonicalMessage, maxTurns int) []CanonicalMessage {
	if maxTurns <= 0 || len(msgs) == 0 {
		return msgs
	}
	pairs := 0
	cutAt := len(msgs) // keep msgs[cutAt:]
	i := len(msgs) - 1
	for i >= 0 {
		if msgs[i].Role == RoleAssistant {
			j := i - 1
			for j >= 0 && msgs[j].Role != RoleUser {
				j--
			}
			if j < 0 {
				break
			}
			pairs++
			if pairs <= maxTurns {
				cutAt = j
			}
			i = j - 1
		} else {
			i--
		}
	}
	return msgs[cutAt:]
}

// trimTokens removes oldest messages from the front until the total estimated
// token count is <= maxTokens. If maxTokens <= 0 the slice is unchanged.
func trimTokens(msgs []CanonicalMessage, maxTokens int) []CanonicalMessage {
	if maxTokens <= 0 || len(msgs) == 0 {
		return msgs
	}
	for len(msgs) > 0 {
		total := 0
		for _, m := range msgs {
			total += estimateTokens(m.Content) + 4
		}
		if total <= maxTokens {
			break
		}
		msgs = msgs[1:]
	}
	return msgs
}

// writeHistorySlot encodes msgs and writes the result into ByteSlots[historySlot].
func writeHistorySlot(ctx *rctx.Context, state *engine.ExecutionState, historySlot int, msgs []CanonicalMessage) {
	state.WriteSlot(ctx, historySlot, encodeHistory(msgs))
}

// ─── append_message ───────────────────────────────────────────────────────────

// AppendMessage appends a single message with the given role to the conversation
// history stored in ByteSlots[historySlot] (JSON-encoded []CanonicalMessage).
// The content is read from ByteSlots[contentSlot].
// If maxTurns > 0, the history is trimmed after append.
func AppendMessage(historySlot, contentSlot int, role MessageRole, maxTurns int) engine.Instruction {
	return engine.Instruction{
		Name: "append_message",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			content := ctx.ByteSlots[contentSlot]
			if len(content) == 0 {
				return state.PC + 1
			}
			msgs := decodeHistory(ctx.ByteSlots[historySlot])
			msgs = append(msgs, CanonicalMessage{
				Role:    role,
				Content: string(content),
			})
			if maxTurns > 0 {
				msgs = trimTurns(msgs, maxTurns)
			}
			writeHistorySlot(ctx, state, historySlot, msgs)
			return state.PC + 1
		},
	}
}

// ─── trim_history ─────────────────────────────────────────────────────────────

// TrimHistory enforces a turn and/or token budget on the history in
// ByteSlots[historySlot]. Both limits are optional (0 = unlimited).
// Turns are trimmed first, then tokens.
func TrimHistory(historySlot, maxTurns, maxTokens int) engine.Instruction {
	return engine.Instruction{
		Name: "trim_history",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			msgs := decodeHistory(ctx.ByteSlots[historySlot])
			if len(msgs) == 0 {
				return state.PC + 1
			}
			if maxTurns > 0 {
				msgs = trimTurns(msgs, maxTurns)
			}
			if maxTokens > 0 {
				msgs = trimTokens(msgs, maxTokens)
			}
			writeHistorySlot(ctx, state, historySlot, msgs)
			return state.PC + 1
		},
	}
}

// ─── load_history ─────────────────────────────────────────────────────────────

// LoadHistory loads conversation history from the datastore into
// ByteSlots[historySlot]. The key is read from ByteSlots[keySlot] at runtime.
// Tenant isolation is applied via ctx.TenantID.
// If store is nil, the key is empty, or the key is not found, writes "[]".
func LoadHistory(store datastore.KeyValueStore, domain string, historySlot, keySlot int) engine.Instruction {
	return engine.Instruction{
		Name: "load_history",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if store == nil {
				state.WriteSlot(ctx, historySlot, emptyHistory)
				return state.PC + 1
			}
			rawKey := ctx.ByteSlots[keySlot]
			if len(rawKey) == 0 {
				state.WriteSlot(ctx, historySlot, emptyHistory)
				return state.PC + 1
			}
			val, found, err := store.Get(context.Background(), datastore.Tenant(ctx.TenantKey), string(rawKey))
			if err != nil || !found {
				state.WriteSlot(ctx, historySlot, emptyHistory)
				return state.PC + 1
			}
			state.WriteSlot(ctx, historySlot, val)
			return state.PC + 1
		},
	}
}

// ─── save_history ─────────────────────────────────────────────────────────────

// SaveHistory persists ByteSlots[historySlot] to the datastore under the key
// in ByteSlots[keySlot]. If store is nil or historySlot is empty, it is a no-op.
// If ttlSecs > 0 and the store supports TTL, PutWithTTL is used; otherwise Put.
func SaveHistory(store datastore.KeyValueStore, domain string, historySlot, keySlot, ttlSecs int) engine.Instruction {
	return engine.Instruction{
		Name: "save_history",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			if store == nil {
				return state.PC + 1
			}
			val := ctx.ByteSlots[historySlot]
			if len(val) == 0 {
				return state.PC + 1
			}
			rawKey := ctx.ByteSlots[keySlot]
			if len(rawKey) == 0 {
				return state.PC + 1
			}
			tenant := datastore.Tenant(ctx.TenantKey)
			if ttlSecs > 0 {
				if exp, ok := store.(datastore.ExpiringStore); ok {
					_ = exp.PutWithTTL(context.Background(), tenant, string(rawKey), val,
						time.Duration(ttlSecs)*time.Second)
					return state.PC + 1
				}
			}
			_ = store.Put(context.Background(), tenant, string(rawKey), val)
			return state.PC + 1
		},
	}
}
