# Pattern Matching Feature - Implementation Plan

**One single plan file. Everything you need.**

---

## Why This Feature?

User needs to match patterns in flow conditions:
```
if header.xyz matches "internal*{tenantid}" then → upstream
if header.x-service matches "^(api|data).*" then → process
```

Currently: No pattern matching support in flow conditions.
**Goal**: Add regex + pattern matching to if/switch steps.

---

## What's the Overall Plan?

**Feature**: Regex pattern matching in flow conditions
**Scope**: Phase 1 = Regex only (simple patterns + variables → Phase 2+3)
**Cost**: ~$5 (190k tokens planned, 20k buffer, $20 allocated)
**Timeline**: 6-7 working days (9 sessions total)

---

## 9 Sessions: What, Where, Who

| # | Session Name | What We're Building | Where (Files) | Model | Time | Status |
|---|---|---|---|---|---|---|
| **1** | **Regex Instruction** | Runtime regex matching instruction for engine | `internal/engine/steps/pattern.go` (new) | Haiku | 45-60m | 🟢 READY |
| **2** | **Compiler Integration** | Compile regex at bake time, generate instruction bytecode | `internal/control/compiler.go` (edit) | Sonnet | 45-60m | 🔴 Blocked by S1 |
| **3** | **Engine Test** | Prove S1+S2 work: compile flow → execute → verify jump | `internal/engine/steps/pattern_test.go` (new) | Haiku | 30-45m | 🔴 Blocked by S1,S2 |
| **4** | **Studio Types** | TypeScript types for pattern conditions in flows | `internal/studio/ui/src/types.ts` (edit) + `dsl_parse.ts` (edit) | Haiku | 45-60m | 🟢 READY |
| **5** | **UI Component** | Visual condition builder for pattern matching in FlowDesigner | `internal/studio/ui/src/components/PatternConditionBuilder.tsx` (new) + `FlowDesigner.tsx` (edit) | Sonnet | 60-90m | 🔴 Blocked by S4 |
| **6** | **Serialization** | Save pattern conditions back to flow DSL | `internal/studio/ui/src/utils/dsl_serialize.ts` (edit) | Haiku | 30-45m | 🔴 Blocked by S4 |
| **7** | **Integration Test** | End-to-end: Studio UI → Compile → Execute → Verify | `internal/control/integration_test_pattern.go` (new) | Sonnet | 60-90m | 🔴 Blocked by ALL S1-6 |
| **8** | **Documentation** | Write feature docs + examples | `docs/packages/pattern_matching.md` (new) | Haiku | 30-45m | 🔴 Blocked by S7 |
| **9** | **Benchmarks** | Prove <1µs latency target met | `internal/engine/steps/pattern_bench_test.go` (new) | Haiku | 30-45m | 🔴 Blocked by S7 |

---

## Why This Many Files vs Fewer?

**Files created**: 4 markdown files to explain the plan

| File | Why | Can We Reduce? |
|------|-----|---|
| `plan_pattern_matching_feature.md` | Full detailed breakdown (dependencies, rollback, risks) | ✓ **Delete this** - use IMPLEMENTATION_PLAN.md instead |
| `SESSION_CURRENT.md` | Per-session reference (what to do this session) | ✓ **Delete this** - put in table below |
| `PLAN_REVIEW_CHECKLIST.md` | Verification before starting | ✓ **Delete this** - move essentials to IMPLEMENTATION_PLAN.md |
| `QUICK_REFERENCE.md` | Visual at-a-glance summary | ✓ **Delete this** - covered in table above |
| **IMPLEMENTATION_PLAN.md** (this file) | Single source of truth | ✓ **KEEP THIS ONLY** |

**You're right - too much.** I'll delete the 4 files and keep this one.

---

## Token Budget Breakdown

| Session | Model | Tokens | Cost |
|---------|-------|--------|------|
| 1 | Haiku | 35k | $0.53 |
| 2 | Sonnet | 15k | $0.75 |
| 3 | Haiku | 25k | $0.38 |
| 4 | Haiku | 30k | $0.45 |
| 5 | Sonnet | 20k | $1.00 |
| 6 | Haiku | 20k | $0.30 |
| 7 | Sonnet | 20k | $1.00 |
| 8 | Haiku | 15k | $0.23 |
| 9 | Haiku | 10k | $0.15 |
| **Total** | | **190k** | **$4.79** |

**Budget**: $20 allocated
**Used**: $4.79
**Reserve**: $15.21 remaining ✓

---

## What Can Run in Parallel?

```
Track A (Engine):        Track B (Studio):
SESSION-1 ┐             SESSION-4 ┐
SESSION-2 ├─ Can start → SESSION-5 ├─ Can start →
SESSION-3 ┘             SESSION-6 ┘
      ↓                       ↓
      └─ SESSION-7 (sync point, needs both tracks)
            ↓
      SESSION-8 + 9 (optional cleanup)
```

**Optimal**: Do S1→S2→S3 in parallel with S4→S5→S6, then sync at S7.

---

## Session Details (What To Do)

### SESSION-1: Regex Instruction
**Model**: Haiku | **Time**: 45-60m | **Status**: Ready now
- **Goal**: Create instruction that matches regex at runtime
- **File**: Create `internal/engine/steps/pattern.go`
- **What**: Function `PatternMatchRegex()` that takes compiled regex + jumps on match/no-match
- **Why**: Engine needs way to execute pattern matching in flows
- **Next**: SESSION-2 compiles the regex

### SESSION-2: Compiler Integration
**Model**: Sonnet | **Time**: 45-60m | **Blocked by**: S1
- **Goal**: Compile regex at bake time (not runtime)
- **File**: Edit `internal/control/compiler.go`
- **What**: Function `compilePatternMatch()` that validates regex syntax + creates instruction bytecode
- **Why**: Regex compilation is expensive, do once at deploy time
- **Next**: SESSION-3 tests both S1+S2 together

### SESSION-3: Engine Test
**Model**: Haiku | **Time**: 30-45m | **Blocked by**: S1, S2
- **Goal**: Prove pattern matching works end-to-end in engine
- **File**: Create `internal/engine/steps/pattern_test.go`
- **What**: Flow with pattern_match step → compile → execute → verify jump
- **Why**: Verify S1+S2 integration works before Studio integration
- **Next**: Can proceed to SESSION-7 (or wait for S4-6)

### SESSION-4: Studio Types
**Model**: Haiku | **Time**: 45-60m | **Blocked by**: None (can start now)
- **Goal**: Define TypeScript types for pattern conditions
- **File**: Edit `internal/studio/ui/src/types.ts` + `dsl_parse.ts`
- **What**: `PatternCondition` interface + parser for pattern_match DSL
- **Why**: Studio needs to understand pattern conditions
- **Next**: SESSION-5 builds UI with these types

### SESSION-5: UI Component
**Model**: Sonnet | **Time**: 60-90m | **Blocked by**: S4
- **Goal**: Visual pattern condition builder in FlowDesigner
- **File**: Create `PatternConditionBuilder.tsx` + edit `FlowDesigner.tsx`
- **What**: React component with dropdowns/inputs for pattern, source, flags
- **Why**: Users need visual way to create patterns (not just DSL)
- **Next**: SESSION-6 saves patterns back to DSL

### SESSION-6: Serialization
**Model**: Haiku | **Time**: 30-45m | **Blocked by**: S4
- **Goal**: Save pattern conditions to flow DSL
- **File**: Edit `internal/studio/ui/src/utils/dsl_serialize.ts`
- **What**: Function `serializePatternCondition()` converts UI → DSL
- **Why**: Round-trip: parse DSL → edit in UI → save back to DSL ✓
- **Next**: SESSION-7 tests all layers together

### SESSION-7: Integration Test
**Model**: Sonnet | **Time**: 60-90m | **Blocked by**: S1-6 (all must complete)
- **Goal**: End-to-end: Studio UI → Compile → Execute → Verify
- **File**: Create `internal/control/integration_test_pattern.go`
- **What**: Full flow test: create pattern condition in UI, compile, deploy, execute, verify jump
- **Why**: Prove entire feature works across all layers
- **Next**: S8-9 for docs/benchmarks (optional)

### SESSION-8: Documentation (OPTIONAL)
**Model**: Haiku | **Time**: 30-45m | **Blocked by**: S7
- **Goal**: Document pattern matching feature
- **File**: Create `docs/packages/pattern_matching.md`
- **What**: Usage guide, examples, performance notes
- **Why**: Future developers need to understand the feature
- **Next**: S9 for benchmarks (optional)

### SESSION-9: Benchmarks (OPTIONAL)
**Model**: Haiku | **Time**: 30-45m | **Blocked by**: S7
- **Goal**: Verify <1µs latency target
- **File**: Create `internal/engine/steps/pattern_bench_test.go`
- **What**: Go benchmarks for regex matching
- **Why**: Prove performance meets RAH's latency targets
- **Next**: Feature complete ✓

---

## Architecture Context (Why Pattern Matching Here?)

```
Request Flow:
  HTTP Request → Router.Lookup(path) → API ID
                                         ↓
                    resolveSubPath(method+path) → Endpoint
                                                     ↓
                             Execute(endpoint.Plan) ← the flow instructions
                                         ↓
                    [Pattern matching lives HERE in flow conditions]
```

**Pattern matching is NOT for endpoint routing** (that's already done by router + resolveSubPath).
**Pattern matching IS for conditions within the flow** (if this header matches X, then do Y).

See `memory/endpoint_flow_architecture.md` for full details.

---

## Before Starting SESSION-1

**Read these 2 files**:
1. `memory/endpoint_flow_architecture.md` (5 min) - understand flow architecture
2. `internal/engine/steps/condition.go` (10 min) - see how existing conditions work

Then start SESSION-1.

---

## Success Criteria

After all 9 sessions:
- ✓ User can create pattern_match conditions in Studio UI
- ✓ Pattern conditions compile at deploy time (not runtime)
- ✓ Regex executes in <1µs in request flow
- ✓ Full e2e test passes: UI → Compiler → Engine → Execution
- ✓ Feature documented
- ✓ Latency verified with benchmarks
- ✓ No breaking changes to existing flows

---

## Known Limitations (Phase 1)

What's **NOT** in Phase 1:
- ❌ Simple patterns (prefix/suffix/contains) → Phase 2
- ❌ Variable interpolation (abc*{tenantid}) → Phase 3
- ❌ Boolean composition (AND/OR patterns) → Phase 4
- ❌ Pattern matching for endpoint routing → not needed (router already does this)

What **IS** in Phase 1:
- ✅ Regex pattern matching
- ✅ Regex flags (i, m, s, x)
- ✅ Within flow conditions only
- ✅ <1µs latency

---

## If Something Breaks

**Rollback is safe**:
- After S1: Delete pattern.go (no impact)
- After S2: Undo compiler edits (no impact)
- After S3: Feature isolated, can skip to S7
- After S7+: Feature complete, no rollback needed

---

## Summary: 9 Sessions in 2 Rows

| Phase | Sessions | What | Cost | Model Split |
|---|---|---|---|---|
| **Foundation (Engine)** | S1, S2, S3 | Instruction, Compiler, Test | $1.66 | 2× Haiku, 1× Sonnet |
| **Integration (Studio)** | S4, S5, S6 | Types, UI, Serialization | $2.75 | 2× Haiku, 1× Sonnet |
| **Verification** | S7 | Full e2e integration test | $1.00 | 1× Sonnet |
| **Polish** | S8, S9 | Docs, Benchmarks (optional) | $0.38 | 2× Haiku |
| **TOTAL** | **9** | **Regex pattern matching in flows** | **$4.79** | **5× Haiku, 4× Sonnet** |

---

## Ready to Start?

**Next action**: Begin SESSION-1
- Read `memory/endpoint_flow_architecture.md`
- Read `internal/engine/steps/condition.go`
- Create `internal/engine/steps/pattern.go` with regex instruction

**Questions before starting?** Ask now.

