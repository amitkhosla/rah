package steps

import (
	"bytes"
	"fmt"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// trueResult is the canonical truthy byte slice written to a result slot on match.
// Using a package-level var avoids allocating a new []byte{1} on every match.
var trueResult = []byte{1}

// SegKind identifies the kind of a parsed template pattern segment.
type SegKind int

const (
	SegLiteral  SegKind = iota // static bytes to match
	SegSlotRef                 // {name} â€” read existing ByteSlot at runtime
	SegCapture                 // (name) â€” write result to ByteSlot at runtime
	SegWildcard                // * â€” match any bytes, discard
)

// Segment is one token in a parsed template pattern.
type Segment struct {
	Kind    SegKind
	Literal []byte // SegLiteral: bytes to match; unused for other kinds
	SlotIdx int    // SegSlotRef: slot index to read; SegCapture: slot index to write
	Name    string // human name for error messages and debug output
}

// MatchStrategy describes the matching algorithm selected at compile time.
type MatchStrategy int

const (
	StrategyExact                MatchStrategy = iota
	StrategyPrefix               MatchStrategy = iota
	StrategySuffix               MatchStrategy = iota
	StrategyContains             MatchStrategy = iota
	StrategyPrefixSuffixExtract  MatchStrategy = iota // literal prefix + single capture + literal suffix (all static)
	StrategyPrefixSlotRefExtract MatchStrategy = iota // literal prefix + single capture + slotref suffix
	StrategySequential           MatchStrategy = iota // general case
)

// CompiledTemplatePattern is the result of ParseTemplatePattern.
type CompiledTemplatePattern struct {
	Segments     []Segment
	Strategy     MatchStrategy
	CaptureSlots []int // ordered slot indices that are captures (cleared on no-match)
	SlotRefSlots []int // ordered slot indices that are slot refs (for debug only)
}

// ParseTemplatePattern parses a pattern string into a CompiledTemplatePattern.
//
// Token syntax:
//   - (name)  â€” capture group: calls allocSlot(name) to obtain a slot index for writing
//   - {name}  â€” slot reference: looks up name in slotMap; error if not found
//   - *       â€” wildcard: matches any bytes, result discarded
//   - everything else â€” literal bytes
//
// After parsing, the optimal MatchStrategy is auto-detected from the segment structure.
func ParseTemplatePattern(
	pattern string,
	slotMap map[string]int,
	allocSlot func(name string) (int, error),
) (*CompiledTemplatePattern, error) {
	if pattern == "" {
		return nil, fmt.Errorf("template pattern: empty pattern string")
	}

	var segments []Segment
	i := 0
	n := len(pattern)

	for i < n {
		ch := pattern[i]

		switch ch {
		case '(':
			// Capture group â€” find matching ')'
			if i+1 < n && pattern[i+1] == '(' {
				return nil, fmt.Errorf("template pattern: nested parentheses not supported at position %d", i)
			}
			j := i + 1
			for j < n && pattern[j] != ')' {
				if pattern[j] == '(' {
					return nil, fmt.Errorf("template pattern: nested parentheses not supported at position %d", j)
				}
				j++
			}
			if j >= n {
				return nil, fmt.Errorf("template pattern: unclosed '(' at position %d", i)
			}
			name := pattern[i+1 : j]
			if name == "" {
				return nil, fmt.Errorf("template pattern: empty capture group name at position %d", i)
			}
			slotIdx, err := allocSlot(name)
			if err != nil {
				return nil, fmt.Errorf("template pattern: allocating capture slot %q: %w", name, err)
			}
			segments = append(segments, Segment{Kind: SegCapture, SlotIdx: slotIdx, Name: name})
			i = j + 1

		case '{':
			// Slot reference â€” find matching '}'
			j := i + 1
			for j < n && pattern[j] != '}' {
				if pattern[j] == '{' {
					return nil, fmt.Errorf("template pattern: nested braces not supported at position %d", j)
				}
				j++
			}
			if j >= n {
				return nil, fmt.Errorf("template pattern: unclosed '{' at position %d", i)
			}
			name := pattern[i+1 : j]
			if name == "" {
				return nil, fmt.Errorf("template pattern: empty slot reference name at position %d", i)
			}
			slotIdx, ok := slotMap[name]
			if !ok {
				return nil, fmt.Errorf("template pattern: slot reference %q is not declared before this step", name)
			}
			segments = append(segments, Segment{Kind: SegSlotRef, SlotIdx: slotIdx, Name: name})
			i = j + 1

		case '*':
			segments = append(segments, Segment{Kind: SegWildcard, Name: "*"})
			i++

		default:
			// Accumulate literal bytes until the next special character
			j := i
			for j < n && pattern[j] != '(' && pattern[j] != '{' && pattern[j] != '*' {
				j++
			}
			lit := []byte(pattern[i:j])
			segments = append(segments, Segment{Kind: SegLiteral, Literal: lit})
			i = j
		}
	}

	if len(segments) == 0 {
		return nil, fmt.Errorf("template pattern: pattern produced no segments")
	}

	// Collect CaptureSlots and SlotRefSlots (ordered)
	var captureSlots []int
	var slotRefSlots []int
	for _, seg := range segments {
		switch seg.Kind {
		case SegCapture:
			captureSlots = append(captureSlots, seg.SlotIdx)
		case SegSlotRef:
			slotRefSlots = append(slotRefSlots, seg.SlotIdx)
		}
	}

	strategy := detectStrategy(segments)

	return &CompiledTemplatePattern{
		Segments:     segments,
		Strategy:     strategy,
		CaptureSlots: captureSlots,
		SlotRefSlots: slotRefSlots,
	}, nil
}

// detectStrategy auto-selects the optimal matching algorithm from the segment structure.
func detectStrategy(segs []Segment) MatchStrategy {
	nCaptures := 0
	nSlotRefs := 0
	nWildcards := 0
	for _, s := range segs {
		switch s.Kind {
		case SegCapture:
			nCaptures++
		case SegSlotRef:
			nSlotRefs++
		case SegWildcard:
			nWildcards++
		}
	}

	// Pure literal â€” no special tokens
	if nCaptures == 0 && nSlotRefs == 0 && nWildcards == 0 {
		return StrategyExact
	}

	if nWildcards > 0 {
		if nWildcards == 1 && nCaptures == 0 && nSlotRefs == 0 {
			first := segs[0]
			last := segs[len(segs)-1]
			if last.Kind == SegWildcard {
				// literal_prefix + * â†’ StrategyPrefix (all non-wildcard segs before must be literals)
				allLiterals := true
				for _, s := range segs[:len(segs)-1] {
					if s.Kind != SegLiteral {
						allLiterals = false
						break
					}
				}
				if allLiterals {
					return StrategyPrefix
				}
			}
			if first.Kind == SegWildcard {
				// * + literal_suffix â†’ StrategySuffix
				allLiterals := true
				for _, s := range segs[1:] {
					if s.Kind != SegLiteral {
						allLiterals = false
						break
					}
				}
				if allLiterals {
					return StrategySuffix
				}
			}
		}
		// Any other wildcard pattern (mid-wildcard, multiple wildcards, wildcards+captures, etc.)
		return StrategySequential
	}

	// No wildcards from here on.

	// Multiple captures or multiple slot refs â†’ sequential
	if nCaptures > 1 || nSlotRefs > 1 {
		return StrategySequential
	}

	// Exactly one capture + one slot ref: check for StrategyPrefixSlotRefExtract
	// Pattern: [literals]* + (capture) + [literals]* + {slotRef}
	// The slotRef must be the last segment; the capture must appear before it;
	// everything else must be literals.
	if nCaptures == 1 && nSlotRefs == 1 {
		last := segs[len(segs)-1]
		if last.Kind == SegSlotRef {
			// Verify exactly one capture somewhere before the last segment,
			// and everything else before the last segment is literal or that one capture.
			captureFound := false
			onlyLiteralsAndOneCapture := true
			for _, s := range segs[:len(segs)-1] {
				if s.Kind == SegCapture {
					captureFound = true
				} else if s.Kind != SegLiteral {
					onlyLiteralsAndOneCapture = false
					break
				}
			}
			if captureFound && onlyLiteralsAndOneCapture {
				return StrategyPrefixSlotRefExtract
			}
		}
		return StrategySequential
	}

	// Exactly one capture, no slot refs, no wildcards
	if nCaptures == 1 {
		// Check for StrategyPrefixSuffixExtract: literal_prefix + (capture) + literal_suffix
		captureIdx := -1
		for i, s := range segs {
			if s.Kind == SegCapture {
				captureIdx = i
				break
			}
		}
		allLiteralsBeforeAndAfter := true
		for i, s := range segs {
			if i == captureIdx {
				continue
			}
			if s.Kind != SegLiteral {
				allLiteralsBeforeAndAfter = false
				break
			}
		}
		if allLiteralsBeforeAndAfter {
			return StrategyPrefixSuffixExtract
		}
		return StrategySequential
	}

	// Only slot refs, no captures, no wildcards â†’ sequential
	return StrategySequential
}

// ---------------------------------------------------------------------------
// Compile-time helpers
// ---------------------------------------------------------------------------

// buildPrefixSuffix extracts and concatenates the static prefix and suffix bytes
// for StrategyPrefixSuffixExtract. Called once at instruction-build time.
func buildPrefixSuffix(segs []Segment) (prefix, suffix []byte) {
	captureIdx := -1
	for i, s := range segs {
		if s.Kind == SegCapture {
			captureIdx = i
			break
		}
	}
	if captureIdx < 0 {
		return nil, nil
	}
	for _, s := range segs[:captureIdx] {
		prefix = append(prefix, s.Literal...)
	}
	for _, s := range segs[captureIdx+1:] {
		suffix = append(suffix, s.Literal...)
	}
	return
}

// ---------------------------------------------------------------------------
// Private strategy helpers
// ---------------------------------------------------------------------------

// matchExact returns true when val equals the concatenation of all literal segments.
func matchExact(val []byte, segs []Segment) bool {
	pos := 0
	for _, s := range segs {
		if s.Kind != SegLiteral {
			continue
		}
		if pos+len(s.Literal) > len(val) {
			return false
		}
		if !bytes.Equal(val[pos:pos+len(s.Literal)], s.Literal) {
			return false
		}
		pos += len(s.Literal)
	}
	return pos == len(val)
}

// matchPrefix returns true when val begins with the literal prefix (ignoring the trailing wildcard).
func matchPrefix(val []byte, segs []Segment) bool {
	for _, s := range segs {
		if s.Kind == SegLiteral {
			if !bytes.HasPrefix(val, s.Literal) {
				return false
			}
			val = val[len(s.Literal):]
		}
		// SegWildcard: match anything â€” prefix check is satisfied
	}
	return true
}

// matchSuffix returns true when val ends with the literal suffix (ignoring the leading wildcard).
func matchSuffix(val []byte, segs []Segment) bool {
	for i := len(segs) - 1; i >= 0; i-- {
		s := segs[i]
		if s.Kind == SegLiteral {
			if !bytes.HasSuffix(val, s.Literal) {
				return false
			}
			val = val[:len(val)-len(s.Literal)]
		}
		// SegWildcard: leading wildcard â€” suffix check is satisfied
	}
	return true
}

// matchPrefixSuffixExtract handles StrategyPrefixSuffixExtract (literal prefix + single
// capture + literal suffix, all static). Returns the captured slices and whether it matched.
// The returned slices are views into val â€” zero copy.
func matchPrefixSuffixExtract(val []byte, prefix, suffix []byte) ([][]byte, bool) {
	if !bytes.HasPrefix(val, prefix) || !bytes.HasSuffix(val, suffix) {
		return nil, false
	}
	if len(prefix)+len(suffix) > len(val) {
		return nil, false
	}
	captured := val[len(prefix) : len(val)-len(suffix)]
	return [][]byte{captured}, true
}

// matchPrefixSlotRefExtract handles StrategyPrefixSlotRefExtract.
// Pattern: [literals...] + (capture) + [literals...] + {slotRef}
// The slot ref is always the last segment. Literals before the capture form the
// prefix; any literals between the capture and the slot ref are concatenated with
// the slot ref value to form the effective suffix.
// Returns captured slices (views into val) and match status.
func matchPrefixSlotRefExtract(val []byte, segs []Segment, ctx *rctx.Context) ([][]byte, bool) {
	last := segs[len(segs)-1] // always a SlotRef by strategy invariant
	slotRefVal := ctx.ByteSlots[last.SlotIdx]

	// Find the capture index (exactly one exists by strategy invariant).
	captureIdx := -1
	for i, s := range segs[:len(segs)-1] {
		if s.Kind == SegCapture {
			captureIdx = i
			break
		}
	}

	// Compute prefix: check all literal segs before the capture.
	prefixLen := 0
	for _, s := range segs[:captureIdx] {
		if !bytes.HasPrefix(val[prefixLen:], s.Literal) {
			return nil, false
		}
		prefixLen += len(s.Literal)
	}

	// Compute effective suffix: any literal segs between capture and slotRef, then slotRef value.
	// We append into a scratch slice (one allocation, not on the hot-path zero-alloc requirement).
	var suffix []byte
	for _, s := range segs[captureIdx+1 : len(segs)-1] {
		suffix = append(suffix, s.Literal...)
	}
	suffix = append(suffix, slotRefVal...)

	if !bytes.HasSuffix(val, suffix) {
		return nil, false
	}
	if prefixLen+len(suffix) > len(val) {
		return nil, false
	}
	captured := val[prefixLen : len(val)-len(suffix)]
	return [][]byte{captured}, true
}

// findEndOfFlexibleSeg returns the position in val (starting from pos) where the
// current Capture or Wildcard segment ends, by scanning ahead for the next
// anchoring segment (Literal or SlotRef) in remaining.
// Returns len(val) if no anchor exists, or -1 if a required anchor is absent.
func findEndOfFlexibleSeg(val []byte, pos int, remaining []Segment, ctx *rctx.Context) int {
	for _, s := range remaining {
		switch s.Kind {
		case SegLiteral:
			if len(s.Literal) == 0 {
				continue
			}
			idx := bytes.Index(val[pos:], s.Literal)
			if idx < 0 {
				return -1
			}
			return pos + idx
		case SegSlotRef:
			slotVal := ctx.ByteSlots[s.SlotIdx]
			if len(slotVal) == 0 {
				continue // empty slot â€” treat as transparent, look further
			}
			idx := bytes.Index(val[pos:], slotVal)
			if idx < 0 {
				return -1
			}
			return pos + idx
		// SegCapture, SegWildcard: flexible â€” continue scanning for next anchor
		}
	}
	return len(val)
}

// matchSequential handles the general StrategySequential case.
// Returns captured slices (views into val) in declaration order and match status.
func matchSequential(val []byte, segs []Segment, ctx *rctx.Context) ([][]byte, bool) {
	nCaptures := 0
	for _, s := range segs {
		if s.Kind == SegCapture {
			nCaptures++
		}
	}

	var captures [][]byte
	if nCaptures > 0 {
		captures = make([][]byte, 0, nCaptures)
	}

	pos := 0
	for i, seg := range segs {
		switch seg.Kind {
		case SegLiteral:
			if !bytes.HasPrefix(val[pos:], seg.Literal) {
				return nil, false
			}
			pos += len(seg.Literal)

		case SegSlotRef:
			slotVal := ctx.ByteSlots[seg.SlotIdx]
			if !bytes.HasPrefix(val[pos:], slotVal) {
				return nil, false
			}
			pos += len(slotVal)

		case SegCapture, SegWildcard:
			end := findEndOfFlexibleSeg(val, pos, segs[i+1:], ctx)
			if end < 0 {
				return nil, false
			}
			if seg.Kind == SegCapture {
				captures = append(captures, val[pos:end])
			}
			pos = end
		}
	}

	if pos != len(val) {
		return nil, false
	}
	return captures, true
}

// ---------------------------------------------------------------------------
// Exported instruction constructors
// ---------------------------------------------------------------------------

// ValidateTemplatePattern returns an Instruction that matches the value at srcSlot
// against the compiled template pattern and writes []byte{1} (truthy) or nil to
// resultSlot. It never branches â€” the caller uses the existing 'if' step for that.
func ValidateTemplatePattern(
	srcSlot int,
	pattern *CompiledTemplatePattern,
	resultSlot int,
) engine.Instruction {
	strategy := pattern.Strategy
	segs := pattern.Segments

	// Pre-compute static prefix/suffix for PrefixSuffixExtract (zero-alloc hot path).
	var precomputedPrefix, precomputedSuffix []byte
	if strategy == StrategyPrefixSuffixExtract {
		precomputedPrefix, precomputedSuffix = buildPrefixSuffix(segs)
	}

	return engine.Instruction{
		Name: "VALIDATE_TEMPLATE_PATTERN",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			val := ctx.ByteSlots[srcSlot]

			var matched bool
			switch strategy {
			case StrategyExact:
				matched = matchExact(val, segs)
			case StrategyPrefix:
				matched = matchPrefix(val, segs)
			case StrategySuffix:
				matched = matchSuffix(val, segs)
			case StrategyPrefixSuffixExtract:
				// Zero-alloc: use pre-computed prefix/suffix, inline bool check.
				matched = len(val) > 0 &&
					bytes.HasPrefix(val, precomputedPrefix) &&
					bytes.HasSuffix(val, precomputedSuffix) &&
					len(precomputedPrefix)+len(precomputedSuffix) <= len(val)
			case StrategyPrefixSlotRefExtract:
				_, matched = matchPrefixSlotRefExtract(val, segs, ctx)
			default: // StrategySequential, StrategyContains
				_, matched = matchSequential(val, segs, ctx)
			}

			if matched {
				ctx.ByteSlots[resultSlot] = trueResult
			} else {
				ctx.ByteSlots[resultSlot] = nil
			}
			return state.PC + 1
		},
	}
}

// ExtractTemplatePattern returns an Instruction that matches the value at srcSlot
// against the compiled template pattern and populates capture slots on match.
// On no-match, all capture slots are set to nil. Captured byte slices are zero-copy
// views into the source header memory (same lifetime guarantee as BindHeader).
func ExtractTemplatePattern(
	srcSlot int,
	pattern *CompiledTemplatePattern,
) engine.Instruction {
	strategy := pattern.Strategy
	segs := pattern.Segments
	captureSlots := pattern.CaptureSlots

	// Pre-compute static prefix/suffix for the zero-alloc PrefixSuffixExtract path.
	var precomputedPrefix, precomputedSuffix []byte
	var firstCaptureSlot int
	if strategy == StrategyPrefixSuffixExtract && len(captureSlots) > 0 {
		precomputedPrefix, precomputedSuffix = buildPrefixSuffix(segs)
		firstCaptureSlot = captureSlots[0]
	}

	return engine.Instruction{
		Name: "EXTRACT_TEMPLATE_PATTERN",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			val := ctx.ByteSlots[srcSlot]

			switch strategy {
			case StrategyPrefixSuffixExtract:
				// Zero-alloc hot path: direct slice view into val.
				if len(val) == 0 ||
					!bytes.HasPrefix(val, precomputedPrefix) ||
					!bytes.HasSuffix(val, precomputedSuffix) ||
					len(precomputedPrefix)+len(precomputedSuffix) > len(val) {
					ctx.ByteSlots[firstCaptureSlot] = nil
				} else {
					ctx.ByteSlots[firstCaptureSlot] = val[len(precomputedPrefix) : len(val)-len(precomputedSuffix)]
				}

			case StrategyPrefixSlotRefExtract:
				captures, ok := matchPrefixSlotRefExtract(val, segs, ctx)
				writeCaptures(ctx, captureSlots, captures, ok)

			default: // StrategySequential (and Exact/Prefix/Suffix with captures, though unusual)
				captures, ok := matchSequential(val, segs, ctx)
				writeCaptures(ctx, captureSlots, captures, ok)
			}

			return state.PC + 1
		},
	}
}

// writeCaptures copies captures into the designated ByteSlots, or clears them on no-match.
func writeCaptures(ctx *rctx.Context, slots []int, captures [][]byte, ok bool) {
	if !ok {
		for _, s := range slots {
			ctx.ByteSlots[s] = nil
		}
		return
	}
	for i, s := range slots {
		if i < len(captures) {
			ctx.ByteSlots[s] = captures[i]
		} else {
			ctx.ByteSlots[s] = nil
		}
	}
}
