package steps

import (
	"regexp"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// PatternMatchRegex creates an Instruction that matches a precompiled regex pattern
// against the content of a ByteSlot and jumps to different instruction pointers
// based on match result.
//
// Design Notes:
// - Regex must be precompiled at bake time (by compiler in SESSION-2)
// - Zero allocations in hot path: no string conversions, no copies
// - Returns absolute PC (next instruction index), not relative offset
// - onMatch and onNoMatch are absolute instruction pointers
// - slotIdx must be valid (compiler ensures bounds at bake time)
//
// Parameters:
//   - slotIdx: Index into ctx.ByteSlots to match against (0-47)
//   - pattern: Precompiled *regexp.Regexp (baked at compile time)
//   - onMatch: Absolute PC to jump to if pattern matches
//   - onNoMatch: Absolute PC to jump to if pattern does not match
//
// Example usage (after compiler support in SESSION-2):
//   ctx.ByteSlots[0] = []byte("api-service-prod")
//   instr := PatternMatchRegex(0, myCompiledRegex, 5, 10)
//   pc := instr.Action(ctx, state)
//   // If pattern matches: pc == 5
//   // If pattern doesn't match: pc == 10
func PatternMatchRegex(slotIdx int, pattern *regexp.Regexp, onMatch, onNoMatch int16) engine.Instruction {
	return engine.Instruction{
		Name: "PATTERN_MATCH_REGEX",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			// Zero-allocation: ByteSlots[slotIdx] is already a []byte, no conversion needed
			slotContent := ctx.ByteSlots[slotIdx]

			// Quick check: if slot is empty, no match possible
			if len(slotContent) == 0 {
				return onNoMatch
			}

			// Hot path: regexp.Match with zero allocations
			// MatchReader also doesn't allocate for the match check itself,
			// but we have bytes, so we use the bytes.Buffer pattern (via bytes.NewReader).
			// However, for maximum performance, we use regexp.Match directly on the bytes.
			if pattern.Match(slotContent) {
				return onMatch
			}
			return onNoMatch
		},
	}
}

// PatternMatchRegexWithCapture creates an Instruction that matches a regex pattern
// and optionally stores captured groups into additional ByteSlots.
//
// This is an enhanced version that supports capture groups. Useful when you need
// to extract parts of the matched string for further processing.
//
// Parameters:
//   - slotIdx: Index into ctx.ByteSlots to match against
//   - pattern: Precompiled *regexp.Regexp
//   - onMatch: Absolute PC to jump to if pattern matches
//   - onNoMatch: Absolute PC to jump to if pattern does not match
//   - captureSlots: Indices where matched groups should be stored (order matters)
//     Empty slice means no captures (equivalent to PatternMatchRegex)
//
// Captures are stored left-to-right:
// - captureSlots[0] gets the first capturing group (submatch[1])
// - captureSlots[1] gets the second capturing group (submatch[2])
// - etc.
// - If a capturing group doesn't participate in the match, its slot is set to nil
//
// Example:
//   pattern = regexp.MustCompile(`^([a-z]+)-([0-9]+)$`)
//   slotContent = "api-123"
//   captureSlots = []int{5, 6}
//   // After match: ctx.ByteSlots[5] = "api", ctx.ByteSlots[6] = "123"
func PatternMatchRegexWithCapture(slotIdx int, pattern *regexp.Regexp, onMatch, onNoMatch int16, captureSlots []int) engine.Instruction {
	return engine.Instruction{
		Name: "PATTERN_MATCH_REGEX_CAPTURE",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			slotContent := ctx.ByteSlots[slotIdx]

			if len(slotContent) == 0 {
				// Clear capture slots on no-match
				for _, idx := range captureSlots {
					if idx >= 0 && idx < len(ctx.ByteSlots) {
						ctx.ByteSlots[idx] = nil
					}
				}
				return onNoMatch
			}

			// FindSubmatch returns the full match at [0] and captured groups at [1], [2], etc.
			matches := pattern.FindSubmatch(slotContent)

			if matches == nil {
				// No match: clear capture slots
				for _, idx := range captureSlots {
					if idx >= 0 && idx < len(ctx.ByteSlots) {
						ctx.ByteSlots[idx] = nil
					}
				}
				return onNoMatch
			}

			// Match found: populate capture slots
			// matches[0] is the full match, matches[1:] are the capture groups
			for i, captureSlot := range captureSlots {
				// i+1 because matches[0] is the full match
				groupIdx := i + 1
				if captureSlot >= 0 && captureSlot < len(ctx.ByteSlots) {
					if groupIdx < len(matches) && matches[groupIdx] != nil {
						// Zero-copy: assign the submatch bytes directly (no arena alloc needed,
						// submatch is a view into the original slotContent which stays alive)
						ctx.ByteSlots[captureSlot] = matches[groupIdx]
					} else {
						// Group didn't participate in match
						ctx.ByteSlots[captureSlot] = nil
					}
				}
			}

			return onMatch
		},
	}
}

/*
USAGE EXAMPLES (for documentation purposes):

// Example 1: Simple pattern match (SESSION-1 complete)
import "regexp"

pattern := regexp.MustCompile(`^(api|data).*`)
instr := PatternMatchRegex(0, pattern, 5, 10)

ctx := &rctx.Context{
    ByteSlots: make([][]byte, 48),
}
ctx.ByteSlots[0] = []byte("api-service")
state := &engine.ExecutionState{PC: 0}

next := instr.Action(ctx, state)
// next == 5 (match found)

// Example 2: Pattern match with capture groups (bonus feature)
pattern := regexp.MustCompile(`^([a-z]+)-([0-9]+)$`)
instr := PatternMatchRegexWithCapture(0, pattern, 5, 10, []int{1, 2})

ctx.ByteSlots[0] = []byte("api-123")
next := instr.Action(ctx, state)
// next == 5
// ctx.ByteSlots[1] == "api"
// ctx.ByteSlots[2] == "123"

// Example 3: Compiler integration (SESSION-2 will implement this)
// In internal/control/compiler.go:
//
// func (c *Compiler) compilePatternMatch(config StepConfig, nextPC int16) int16 {
//     slotIdx := c.getSlot(config.Source) // Get ByteSlot to match against
//     pattern, err := regexp.Compile(config.Pattern)
//     if err != nil {
//         c.Errors = append(c.Errors, fmt.Sprintf("invalid regex: %v", err))
//         return nextPC
//     }
//     onMatch := c.resolveTarget(config.OnMatch)
//     onNoMatch := c.resolveTarget(config.OnNoMatch)
//     c.emit(PatternMatchRegex(slotIdx, pattern, onMatch, onNoMatch))
//     return nextPC + 1
// }

*/
