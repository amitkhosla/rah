# Template Pattern Plan: `validatePattern` + `extract`

## Feature Overview

Two new DSL functions for the RAH flow DSL that enable dynamic pattern matching against ByteSlot values at runtime, using a template syntax that supports literal segments, slot references, captures, and wildcards.

```js
// Validation: {braces} = slot reference (read existing var), * = wildcard
isValid = validatePattern(header.Custom_HEADER, 'internal_*_{tenantId}')

// Extraction: (parens) = capture group (write new var), {braces} = slot ref (read)
extract(header.Custom_HEADER, 'internal_(service)_(env)_{tenantId}')
// → 'service' and 'env' are new ByteSlot variables
```

### Delimiter Convention

| Token     | Meaning                                                         |
|-----------|-----------------------------------------------------------------|
| `(name)`  | Capture group — writes result bytes to ByteSlot `name` at runtime |
| `{name}`  | Slot reference — reads ByteSlot `name` at runtime (must already be declared) |
| `*`       | Wildcard — match any bytes, discard result                      |
| everything else | Literal bytes to match exactly                          |

---

## Architecture Decisions

1. **No regex at runtime.** Template patterns compile to pure byte operations (`HasPrefix`, `HasSuffix`, `bytes.Equal`, sequential scan). The compiler auto-detects the optimal strategy at bake time from the parsed segment structure.

2. **`validate_pattern` result.** Writes `[]byte{1}` (truthy) or `nil` (falsy) to a ByteSlot named by `as:`. This is compatible with the existing `NewComplexLogicGate` RPN evaluator (`len(ctx.ByteSlots[val]) > 0 == true`) without any changes to logic.go.

3. **`extract_pattern` result.** Named captures in `(name)` are the output variables. No `as:` field. On no-match, all capture slots are set to `nil`.

4. **Slot usage.** Captures call `getSlot(name)` (allocate or reuse). Slot references call `getSlotReadOnly(name)` (compile error if not declared before this step).

5. **Strategy auto-detection at compile time** (during `ParseTemplatePattern`, before emitting any instruction):

| Segment structure | Strategy |
|---|---|
| All literals, no captures, no refs | `StrategyExact` |
| `literal_prefix + *` | `StrategyPrefix` |
| `* + literal_suffix` | `StrategySuffix` |
| `literal_prefix + (capture) + literal_suffix` (all static) | `StrategyPrefixSuffixExtract` |
| `literal_prefix + (capture) + {slotRef}` | `StrategyPrefixSlotRefExtract` |
| Multiple slot refs or multiple captures | `StrategySequential` |
| Wildcard mid-pattern | `StrategySequential` |

---

## Session Parallelism

The five sessions split into two independent chains that can run in parallel:

```
Go chain:    [S1] ──► [S2] ──► [S3]
TS chain:    [S4] ──► [S5]

S1 and S4 can start simultaneously.
S2 starts only after S1 completes.
S5 starts only after S4 completes.
S3 starts only after S2 completes.
S3 and S5 can run in parallel with each other.
```

### Model Recommendations

| Session | Description | Recommended Model | Rationale |
|---------|-------------|-------------------|-----------|
| S1 | Go parser & data structures | **Sonnet** | Needs to understand existing slot/compiler patterns |
| S2 | Go runtime, zero-alloc matching | **Sonnet** | Performance-critical byte matching logic |
| S3 | Go compiler integration | **Sonnet** | Needs deep understanding of `compilePatternMatch` pattern |
| S4 | TypeScript DSL parse/serialize | **Haiku** | Mechanical: follow existing `actionSteps` switch patterns |
| S5 | TypeScript Studio UI component | **Haiku** | React component, follow `PatternConditionBuilder.tsx` style |

---

## Global Constraints (ALL Sessions)

1. Do NOT touch any file not listed in the session's "Files touched" section.
2. Each session MUST run its specified verification command before declaring done.
3. Skip pre-existing failing tests — do not attempt to fix them:
   - `observability`: `StartRequest` signature mismatch in `telemetry_test.go`
   - `control`: `TestUnifiedSyncRegistersApiAndRuntimeConsumesCompiledFlow` returns 401 instead of 200
   - `studio`: `TestSchemaAndUIServed` — UI not served in test environment
   - `cache`: `TestHighTPSWithEviction` — deadlock in `timedMutexMap` test helper
   - `inlcache`: `TestVsModels` timeout, `TestMemoryVsOldIndex` post-compact entry drop bug
4. Do NOT modify `go.mod` or `go.sum`.
5. All new Go types must reside in the package specified per session (`steps` or `control`).

---

## Session 1 — Go: Template Pattern Parser & Data Structures

**Depends on:** nothing (start immediately)
**Go chain position:** first

### Files Touched

| File | Status |
|------|--------|
| `internal/engine/steps/template_pattern.go` | NEW |
| `internal/engine/steps/template_pattern_test.go` | NEW |

### Do NOT touch

`internal/engine/steps/pattern.go`, `internal/control/compiler.go`, or any other existing file.

### New Types

Define the following in `internal/engine/steps/template_pattern.go` (package `steps`):

```go
type SegKind int

const (
    SegLiteral  SegKind = iota // static bytes to match
    SegSlotRef                 // {name} — read existing ByteSlot at runtime
    SegCapture                 // (name) — write result to ByteSlot at runtime
    SegWildcard                // * — match any bytes, discard
)

type Segment struct {
    Kind    SegKind
    Literal []byte // SegLiteral: bytes to match; unused for other kinds
    SlotIdx int    // SegSlotRef: slot index to read; SegCapture: slot index to write
    Name    string // human name for error messages and debug output
}

type MatchStrategy int

const (
    StrategyExact                MatchStrategy = iota
    StrategyPrefix
    StrategySuffix
    StrategyContains
    StrategyPrefixSuffixExtract  // literal prefix + single capture + literal suffix (all static)
    StrategyPrefixSlotRefExtract // literal prefix + single capture + slotref suffix
    StrategySequential           // general case
)

type CompiledTemplatePattern struct {
    Segments     []Segment
    Strategy     MatchStrategy
    CaptureSlots []int // ordered slot indices that are captures (cleared on no-match)
    SlotRefSlots []int // ordered slot indices that are slot refs (for debug only)
}
```

### New Function

```go
func ParseTemplatePattern(
    pattern  string,
    slotMap  map[string]int,
    allocSlot func(name string) (int, error),
) (*CompiledTemplatePattern, error)
```

Behaviour:
- Iterates through the pattern string, recognising `(name)`, `{name}`, `*`, and literal runs.
- For `(name)`: calls `allocSlot(name)` to obtain (or allocate) a slot index.
- For `{name}`: looks up `name` in `slotMap`; returns an error if not found (maps to `getSlotReadOnly` semantics in the compiler).
- Nested parentheses or braces are not supported — return an error.
- Empty pattern string is an error.
- After parsing all segments, auto-detects `Strategy` from the segment structure using the rules in the Architecture Decisions table above.
- Populates `CaptureSlots` and `SlotRefSlots` ordered lists.
- Does NOT emit any instructions.

### Tests in `template_pattern_test.go`

| Test name | What it asserts |
|-----------|-----------------|
| `TestParseTemplatePattern_PureLiteral` | `"exact_value"` → `StrategyExact`, single `SegLiteral` segment |
| `TestParseTemplatePattern_PrefixWildcard` | `"prefix_*"` → `StrategyPrefix` |
| `TestParseTemplatePattern_WildcardSuffix` | `"*_suffix"` → `StrategySuffix` |
| `TestParseTemplatePattern_PrefixCaptureStaticSuffix` | `"pre_(cap)_suf"` → `StrategyPrefixSuffixExtract`, one capture slot |
| `TestParseTemplatePattern_PrefixCaptureSlotRef` | `"pre_(cap)_{ref}"` with `ref` in slotMap → `StrategyPrefixSlotRefExtract` |
| `TestParseTemplatePattern_TwoCaptures` | `"(a)_(b)"` → `StrategySequential`, two entries in `CaptureSlots` |
| `TestParseTemplatePattern_UnknownSlotRef` | `"{undeclared}"` with empty slotMap → error |
| `TestParseTemplatePattern_EmptyPattern` | `""` → error |
| `TestParseTemplatePattern_NestedParens` | `"((nested))"` → error |
| `TestParseTemplatePattern_MidWildcard` | `"pre_*_suf"` → `StrategySequential` |

### Verification

```bash
go test ./internal/engine/steps/ -run TestParseTemplatePattern -v
```

All listed tests must pass. No other tests should regress.

---

## Session 2 — Go: Runtime Matching Instructions

**Depends on:** Session 1
**Go chain position:** second

### Files Touched

| File | Status |
|------|--------|
| `internal/engine/steps/template_pattern.go` | MODIFIED (add functions) |
| `internal/engine/steps/template_pattern_exec_test.go` | NEW |

### Do NOT touch

`internal/control/compiler.go` or any other existing file.

### New Exported Functions (add to `template_pattern.go`)

```go
func ValidateTemplatePattern(
    srcSlot     int,
    pattern     *CompiledTemplatePattern,
    resultSlot  int,
) engine.Instruction
```

- Instruction name: `"VALIDATE_TEMPLATE_PATTERN"`
- Reads `ctx.ByteSlots[srcSlot]`.
- Dispatches to the appropriate private strategy helper based on `pattern.Strategy`.
- On match: `ctx.ByteSlots[resultSlot] = []byte{1}` (truthy for RPN gate).
- On no-match: `ctx.ByteSlots[resultSlot] = nil`.
- Returns `state.PC + 1` always. This is NOT a branching instruction — let the existing `if` step handle branching.

```go
func ExtractTemplatePattern(
    srcSlot int,
    pattern *CompiledTemplatePattern,
) engine.Instruction
```

- Instruction name: `"EXTRACT_TEMPLATE_PATTERN"`
- Reads `ctx.ByteSlots[srcSlot]`.
- On match: populates each slot in `pattern.CaptureSlots` with the captured byte slice.
- On no-match: sets every slot in `pattern.CaptureSlots` to `nil`.
- Returns `state.PC + 1`.
- Zero-allocation hot path: use `bytes.HasPrefix`, `bytes.HasSuffix`, and direct slice views into the source byte slice. Capture slices are views into header memory that is alive for the full request lifetime, matching the `BindHeader` zero-copy contract.

### Private Strategy Helpers (inside `template_pattern.go`)

| Helper signature | Purpose |
|---|---|
| `matchExact(val []byte, segs []Segment) bool` | Bytes-equal comparison for `StrategyExact` |
| `matchPrefix(val []byte, segs []Segment) bool` | `bytes.HasPrefix` for `StrategyPrefix` |
| `matchSuffix(val []byte, segs []Segment) bool` | `bytes.HasSuffix` for `StrategySuffix` |
| `matchPrefixSuffixExtract(val []byte, segs []Segment) ([][]byte, bool)` | HasPrefix + HasSuffix + middle slice for `StrategyPrefixSuffixExtract` |
| `matchPrefixSlotRefExtract(val []byte, segs []Segment, ctx *rctx.Context) ([][]byte, bool)` | HasPrefix + runtime slot comparison from end for `StrategyPrefixSlotRefExtract` |
| `matchSequential(val []byte, segs []Segment, ctx *rctx.Context) ([][]byte, bool)` | General left-to-right scan for `StrategySequential` |

### Tests in `template_pattern_exec_test.go`

| Test name | What it asserts |
|-----------|-----------------|
| `TestValidateTemplatePattern_ExactMatch` | `StrategyExact`, input matches → resultSlot = `[]byte{1}` |
| `TestValidateTemplatePattern_ExactNoMatch` | `StrategyExact`, input differs → resultSlot = nil |
| `TestValidateTemplatePattern_PrefixMatch` | `StrategyPrefix` correct match |
| `TestValidateTemplatePattern_PrefixNoMatch` | `StrategyPrefix` no match |
| `TestValidateTemplatePattern_SuffixMatch` | `StrategySuffix` correct match |
| `TestValidateTemplatePattern_PrefixSuffixExtract_Match` | Correct capture value extracted |
| `TestValidateTemplatePattern_PrefixSuffixExtract_NoMatch` | Capture slot nil on no-match |
| `TestValidateTemplatePattern_PrefixSlotRefExtract_Match` | Slot ref read correctly at runtime |
| `TestValidateTemplatePattern_Sequential_TwoCaptures` | Both capture slots populated |
| `TestExtractTemplatePattern_CapturesNilOnMismatch` | All capture slots nil on no-match |
| `TestExtractTemplatePattern_EmptySrcSlot` | Nil srcSlot treated as no-match, all captures nil |
| `TestExtractTemplatePattern_PartialMatchNoCapture` | Prefix matches but suffix doesn't → no-match |
| `TestValidateTemplatePattern_ZeroAllocs` | `testing.AllocsPerRun` for `StrategyPrefixSuffixExtract` hot path = 0 |

### Verification

```bash
go test ./internal/engine/steps/ -run TestTemplatePattern -v
go test ./internal/engine/steps/ -run TestValidateTemplatePattern -v
go test ./internal/engine/steps/ -run TestExtractTemplatePattern -v
```

All tests from both `template_pattern_test.go` and `template_pattern_exec_test.go` must pass.

---

## Session 3 — Go: Compiler & Step Descriptor Integration

**Depends on:** Session 2
**Go chain position:** third

### Files Touched

| File | Status |
|------|--------|
| `internal/control/compiler.go` | MODIFIED |
| `internal/control/step_descriptors.go` | MODIFIED |
| `internal/control/template_pattern_compiler_test.go` | NEW |

### Do NOT touch

`internal/engine/steps/template_pattern.go`, `internal/engine/steps/pattern.go`, or any other file not listed above.

### Changes to `compiler.go`

In the main `bakeStep` switch statement (alongside the existing `case "pattern_match":` block), add:

```go
case "validate_pattern":
    return c.compileValidatePattern(step)
case "extract_pattern":
    return c.compileExtractPattern(step)
```

Add method `compileValidatePattern(step StepConfig) error`:
- Validates that `step.Source` is non-empty; return a descriptive error if missing.
- Validates that `step.Input["pattern"]` is non-empty; return a descriptive error if missing.
- Validates that `step.As` is non-empty; return a descriptive error if missing.
- Calls `c.getSlot(step.Source)` to obtain `srcSlot`.
- Calls `c.getSlot(step.As)` to obtain `resultSlot` (ByteSlot, not BoolSlot).
- Builds a `map[string]int` snapshot of `c.slotMap` to pass as the read-only `slotMap` argument.
- Calls `steps.ParseTemplatePattern(pattern, slotMap, c.getSlot)` — passes `c.getSlot` as the `allocSlot` callback so captures get real slot indices.
- On parse error, wraps and returns the error.
- Emits `steps.ValidateTemplatePattern(srcSlot, compiled, resultSlot)` via `c.emit(...)`.

Add method `compileExtractPattern(step StepConfig) error`:
- Validates that `step.Source` is non-empty.
- Validates that `step.Input["pattern"]` is non-empty.
- `step.As` is NOT used (captures are named inside the pattern string) — do not error if it is absent.
- Calls `c.getSlot(step.Source)` to obtain `srcSlot`.
- Builds `slotMap` snapshot.
- Calls `steps.ParseTemplatePattern(pattern, slotMap, c.getSlot)`.
- On parse error, wraps and returns the error.
- Emits `steps.ExtractTemplatePattern(srcSlot, compiled)`.

**Gotcha — `getSlot` vs `getSlotReadOnly`:** Inside `ParseTemplatePattern`, the `allocSlot` callback is `c.getSlot` (which allocates a new slot or returns an existing one). The `slotMap` snapshot passed for `{ref}` lookups contains only slots declared before this step, enforcing declaration order. Do NOT pass `c.getSlotReadOnly` as the `allocSlot` argument — captures must be allowed to create new slots.

### Changes to `step_descriptors.go`

Add two new descriptors following the pattern of existing entries:

```go
{
    Type:           "validate_pattern",
    Category:       "string",
    Capability:     "match",
    SupportsNested: false,
},
{
    Type:           "extract_pattern",
    Category:       "string",
    Capability:     "extract",
    SupportsNested: false,
},
```

Do NOT add either type to `isControlFlowAction` — these are data-manipulation steps, not control flow.

### Tests in `template_pattern_compiler_test.go`

Follow the style of `internal/control/pattern_match_compiler_test.go`.

| Test name | What it asserts |
|-----------|-----------------|
| `TestValidatePatternBasicMatch` | Compiles a flow with `validate_pattern`; at runtime, matching input sets isValid ByteSlot to non-nil |
| `TestValidatePatternNoMatch` | At runtime, non-matching input → isValid ByteSlot is nil |
| `TestExtractPatternCaptures` | Compiles a flow with `extract_pattern`; capture slots populated on match |
| `TestExtractPatternNilOnMismatch` | Capture slots are nil when input does not match |
| `TestValidatePatternUnknownSlotRef` | `{undeclaredVar}` in pattern → `compileValidatePattern` returns a compile error |
| `TestExtractPatternUnknownSlotRef` | Same check for `extract_pattern` |
| `TestExtractPatternMissingSource` | Empty `Source` field → compile error |
| `TestValidatePatternMissingAs` | Empty `As` field → compile error |
| `TestExtractPatternStepDescriptorRegistered` | Descriptor with type `"extract_pattern"` present in registry |
| `TestValidatePatternStepDescriptorRegistered` | Descriptor with type `"validate_pattern"` present in registry |

### Verification

```bash
go test ./internal/control/ -run TestValidatePattern -v
go test ./internal/control/ -run TestExtractPattern -v
```

All listed tests must pass. Pre-existing failing tests listed in the global constraints must be skipped, not fixed.

---

## Session 4 — TypeScript: DSL Parser & Serializer

**Depends on:** nothing (start immediately, parallel with Session 1)
**TS chain position:** first

### Files Touched

| File | Status |
|------|--------|
| `internal/studio/ui/src/utils/dsl_parse.ts` | MODIFIED |
| `internal/studio/ui/src/utils/dsl_serialize.ts` | MODIFIED |
| `internal/studio/ui/src/utils/dsl_serialize.test.ts` | MODIFIED |

### Do NOT touch

`internal/studio/ui/src/components/PatternConditionBuilder.tsx`, `types.ts`, `FlowDesigner.tsx`, or any other file not listed above.

### Changes to `dsl_parse.ts`

In the `actionSteps` switch statement, after the `'concat':` case (around line 339), add two new cases:

```typescript
case 'validate_pattern': {
  const pos = positional(2)  // arg0 = source slot, arg1 = quoted pattern string
  return [mk({
    action: 'validate_pattern',
    source: unquote(pos[0] ?? ''),
    input: JSON.stringify({ pattern: unquote(pos[1] ?? '') }),
    ...withAs,
  })]
}
case 'extract': {
  const pos = positional(2)  // arg0 = source slot, arg1 = quoted pattern string
  // No LHS assignment — capture variable names are embedded in the pattern
  return [mk({
    action: 'extract_pattern',
    source: unquote(pos[0] ?? ''),
    input: JSON.stringify({ pattern: unquote(pos[1] ?? '') }),
  })]
}
```

Note: `withAs` is populated from the `var = fn(...)` parser for `validate_pattern` (LHS present). For `extract`, the line starts with `extract(...)` with no LHS, so `withAs` / `as_` will be undefined — do not include it.

### Changes to `dsl_serialize.ts`

In the `serializeDSL` function, add two cases for the new action types. Insert before the final `else` / fallthrough, following existing patterns:

```typescript
if (a === 'validate_pattern') {
  const src = str(s['source'])
  const pat = str(JSON.parse(str(s['input'] || '{}')).pattern ?? '')
  const as_ = str(s['as'])
  const lhs = as_ ? `${as_} = ` : ''
  lines.push(`${I}${lhs}validatePattern(${src}, '${pat}')`)
  continue
}
if (a === 'extract_pattern') {
  const src = str(s['source'])
  const pat = str(JSON.parse(str(s['input'] || '{}')).pattern ?? '')
  lines.push(`${I}extract(${src}, '${pat}')`)
  continue
}
```

### Tests to Add to `dsl_serialize.test.ts`

| Test name | What it asserts |
|-----------|-----------------|
| `validatePattern round-trip` | Parse DSL line → serialize → parse again → same `FlowStep` object |
| `extract round-trip` | Same round-trip check for `extract(...)` |
| `validatePattern with dynamic slot ref` | Pattern `'internal_*_{tenantId}'` round-trips with `{tenantId}` preserved verbatim |
| `extract with multiple captures` | Pattern `'(service)_(env)'` round-trips correctly; both capture names preserved |
| `validatePattern serializes lhs` | `isValid = validatePattern(...)` serializes with the `isValid =` prefix |
| `extract serializes without lhs` | `extract(...)` serializes without any `=` assignment prefix |

### Verification

```bash
cd internal/studio/ui && npm test -- --testPathPattern=dsl_serialize
```

All new tests must pass. No existing tests should regress.

---

## Session 5 — TypeScript: Studio UI Component

**Depends on:** Session 4
**TS chain position:** second

### Files Touched

| File | Status |
|------|--------|
| `internal/studio/ui/src/types.ts` | MODIFIED |
| `internal/studio/ui/src/components/TemplatePatternBuilder.tsx` | NEW |
| `internal/studio/ui/src/components/FlowDesigner.tsx` | MODIFIED |

### Do NOT touch

`internal/studio/ui/src/components/PatternConditionBuilder.tsx`, `dsl_parse.ts`, `dsl_serialize.ts`, or any other file not listed above.

### Changes to `types.ts`

Add after the existing pattern-related types:

```typescript
export interface TemplatePatternStep extends FlowStep {
  action: 'validate_pattern' | 'extract_pattern'
  source: string          // slot name e.g. 'header.X-My-Header'
  input: { pattern: string }
  as?: string             // only for validate_pattern; undefined for extract_pattern
}
```

### New Component `TemplatePatternBuilder.tsx`

Props interface:

```typescript
interface TemplatePatternBuilderProps {
  action: 'validate_pattern' | 'extract_pattern'
  step: TemplatePatternStep
  onChange: (s: TemplatePatternStep) => void
}
```

The component must render:

1. **Source slot input field** — text input bound to `step.source`, with placeholder hint `"e.g. header.X-My-Header or any variable name"`.

2. **Pattern textarea** — bound to `step.input.pattern`, with a live segment preview below it. The preview parses the pattern string and renders colored inline spans:
   - Literals → white / default text
   - `(name)` captures → green, with a label `writes to variable 'name'`
   - `{name}` slot refs → blue, with a label `reads from 'name'`
   - `*` wildcards → yellow

3. **Result variable field** (only when `action === 'validate_pattern'`) — text input bound to `step.as`, labelled `"Result variable name"`.

4. **Code preview** — a read-only `<code>` block showing the equivalent DSL line, e.g.:
   - `isValid = validatePattern(header.X-Custom, 'internal_*_{tenantId}')`
   - `extract(header.X-Custom, 'internal_(service)_(env)_{tenantId}')`

Model the live segment preview parser after the logic in `ParseTemplatePattern` from Session 1, but implemented in TypeScript purely for display: iterate through the pattern string, tokenise `(...)`, `{...}`, `*`, and literal runs.

### Changes to `FlowDesigner.tsx`

Add rendering for `validate_pattern` and `extract_pattern` step cards alongside existing step type handlers. When a step's `action` is `'validate_pattern'` or `'extract_pattern'`:

- Render a card using the `TemplatePatternBuilder` component.
- Pass `step` typed as `TemplatePatternStep` and wire `onChange` to update the flow step in the designer state.
- Follow the same card layout pattern used for existing action steps in the file.

### Verification

```bash
cd internal/studio/ui && npm run build
```

The build must complete with **zero TypeScript errors**. No runtime testing is required for this session beyond a clean compile.

---

## File Change Summary

| File | Session | New / Modified |
|------|---------|----------------|
| `internal/engine/steps/template_pattern.go` | S1, S2 | NEW (S1 creates; S2 extends) |
| `internal/engine/steps/template_pattern_test.go` | S1 | NEW |
| `internal/engine/steps/template_pattern_exec_test.go` | S2 | NEW |
| `internal/control/compiler.go` | S3 | MODIFIED |
| `internal/control/step_descriptors.go` | S3 | MODIFIED |
| `internal/control/template_pattern_compiler_test.go` | S3 | NEW |
| `internal/studio/ui/src/utils/dsl_parse.ts` | S4 | MODIFIED |
| `internal/studio/ui/src/utils/dsl_serialize.ts` | S4 | MODIFIED |
| `internal/studio/ui/src/utils/dsl_serialize.test.ts` | S4 | MODIFIED |
| `internal/studio/ui/src/types.ts` | S5 | MODIFIED |
| `internal/studio/ui/src/components/TemplatePatternBuilder.tsx` | S5 | NEW |
| `internal/studio/ui/src/components/FlowDesigner.tsx` | S5 | MODIFIED |

**Total:** 6 new files, 6 modified files across 5 sessions.
