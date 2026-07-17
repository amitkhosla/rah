package steps

import (
	"encoding/json"
	"unicode"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// ChunkTextConfig is resolved at bake time and captured in the instruction closure.
type ChunkTextConfig struct {
	// InputSlot: ByteSlot index holding the text to chunk
	InputSlot int
	// OutputSlot: ByteSlot index to write JSON []string of chunks
	OutputSlot int
	// CountSlot: IntSlot index to write chunk count (-1 = skip)
	CountSlot int
	// ChunkSize: target characters per chunk; estimated as tokens*4 (default 512 tokens = 2048 chars)
	ChunkSize int
	// Overlap: overlap characters between chunks (default 64 tokens = 256 chars)
	Overlap int
}

// ChunkText returns an engine.Instruction that splits text into overlapping chunks.
//
// At runtime:
//  1. Reads text from ctx.ByteSlots[cfg.InputSlot]; if empty, returns PC+1 (no-op)
//  2. Splits text into chunks of cfg.ChunkSize chars with cfg.Overlap char overlap
//  3. Works in rune slices for correctness; trims at word boundaries
//  4. If text fits in one chunk, output is ["<full text>"]
//  5. Marshals []string chunks to JSON â†’ ctx.ByteSlots[cfg.OutputSlot]
//  6. If CountSlot >= 0 and in range: writes int64(len(chunks)) to ctx.IntSlots[cfg.CountSlot]
//  7. Returns state.PC + 1
//
// On any error: sets ctx.ResponseStatus = 500, ctx.Failed = true, returns engine.StopPlan.
func ChunkText(cfg ChunkTextConfig) engine.Instruction {
	chunkSize := cfg.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 2048 // default 512 tokens = 2048 chars
	}
	overlap := cfg.Overlap
	if overlap < 0 {
		overlap = 0
	}

	return engine.Instruction{
		Name: "chunk_text",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// 1. Read input text
			if cfg.InputSlot < 0 || cfg.InputSlot >= len(ctx.ByteSlots) {
				return state.PC + 1
			}
			textBytes := ctx.ByteSlots[cfg.InputSlot]
			if len(textBytes) == 0 {
				return state.PC + 1
			}

			text := string(textBytes)

			// 2. Split into runes for correctness
			runes := []rune(text)
			if len(runes) == 0 {
				return state.PC + 1
			}

			// 3. Calculate chunks
			chunks := splitIntoChunks(runes, chunkSize, overlap)
			if len(chunks) == 0 {
				return state.PC + 1
			}

			// 4. Marshal to JSON
			jsonBytes, err := json.Marshal(chunks)
			if err != nil {
				ctx.ResponseStatus = 500
				ctx.Failed = true
				ctx.ErrorCode = 500
				msg := "chunk_text: marshal failed"
				ctx.ErrorMsg = ctx.Alloc(len(msg))
				copy(ctx.ErrorMsg, msg)
				return engine.StopPlan
			}

			// 5. Write to output slot
			if cfg.OutputSlot >= 0 && cfg.OutputSlot < len(ctx.ByteSlots) {
				dst := ctx.Alloc(len(jsonBytes))
				copy(dst, jsonBytes)
				ctx.ByteSlots[cfg.OutputSlot] = dst
			}

			// 6. Write count to IntSlot
			if cfg.CountSlot >= 0 && cfg.CountSlot < len(ctx.IntSlots) {
				ctx.IntSlots[cfg.CountSlot] = int64(len(chunks))
			}

			return state.PC + 1
		},
	}
}

// splitIntoChunks splits a rune slice into chunks with overlap.
// Each chunk is trimmed at word boundaries (last space before chunk end).
// If the entire text fits in one chunk, returns []string with single element.
func splitIntoChunks(runes []rune, chunkSize, overlap int) []string {
	if len(runes) <= chunkSize {
		return []string{string(runes)}
	}

	var chunks []string
	step := chunkSize - overlap
	if step <= 0 {
		step = 1
	}

	for start := 0; start < len(runes); {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}

		// Trim at word boundary (scan backward for last space)
		trimEnd := end
		if end < len(runes) {
			// Only trim if we're not at the end already
			for i := end - 1; i > start; i-- {
				if unicode.IsSpace(runes[i]) {
					trimEnd = i
					break
				}
			}
		}

		chunk := string(runes[start:trimEnd])
		if len(chunk) > 0 {
			chunks = append(chunks, chunk)
		}

		// Move to next chunk start
		start += step
		if start >= len(runes) {
			break
		}
	}

	if len(chunks) == 0 {
		return []string{string(runes)}
	}

	return chunks
}
