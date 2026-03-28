package control

import (
	"fmt"
	"strconv"

	"rah/internal/engine/steps"
)

// compileChunkText resolves slots and appends the chunk_text instruction.
//
// Slot mapping:
//   - key_identifier → inputSlot (text to chunk)
//   - as             → outputSlot (JSON []string chunks)
//   - input          → config JSON with chunk_size, overlap, count_slot
func (c *Compiler) compileChunkText(step StepConfig) error {
	inputSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("chunk_text: input slot: %w", err)
	}

	outputSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("chunk_text: output slot: %w", err)
	}

	// Parse config from step.Input
	chunkSize := 512   // default 512 tokens = 2048 chars
	overlap := 64      // default 64 tokens = 256 chars
	countSlot := -1    // default: skip

	if len(step.Input) > 0 {
		if v, ok := step.Input["chunk_size"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				chunkSize = n * 4 // tokens to chars
			}
		}

		if v, ok := step.Input["overlap"]; ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				overlap = n * 4 // tokens to chars
			}
		}

		if v, ok := step.Input["count_slot"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				countSlot = n
			}
		}
	}

	cfg := steps.ChunkTextConfig{
		InputSlot:  inputSlot,
		OutputSlot: outputSlot,
		CountSlot:  countSlot,
		ChunkSize:  chunkSize,
		Overlap:    overlap,
	}
	c.GlobalTable = append(c.GlobalTable, steps.ChunkText(cfg))
	return nil
}
