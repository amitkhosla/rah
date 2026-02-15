# Router, Subrouter, Engine, and Control Gap Review

This document captures architecture and correctness gaps that should be prioritized **before** performance tuning.

## Scope reviewed
- `internal/router/*`
- `internal/engine/*` (including subrouter runtime in `manager.go` and `sub_router.go`)
- `internal/control/*`

## High-priority gaps

1. **Router startup panic risk due to split atomic state**
   - `RahRouter` keeps arena and prefix table in two independent `atomic.Value` fields.
   - `New()` initializes `arena` but not `prefixTable`; `Lookup()` unconditionally loads both.
   - A lookup before the first route bake can panic (`Load` on unset atomic value).
   - Recommendation: atomically publish a single immutable snapshot struct `{arena, table}`.

2. **Subrouter static matching is only first-byte based for segments**
   - `compileStaticSegment()` indexes by first byte and stores only `PrefixLen` in subroute nodes.
   - Runtime `resolveSubPath()` advances by `PrefixLen` but does not validate full segment bytes.
   - This can falsely match different segments sharing the same first byte and length.
   - Recommendation: store segment bytes (or prefix-table offsets) and compare actual bytes during traversal.

3. **Subrouter child indexing assumes contiguity that builder does not guarantee**
   - `SubRouteNode.FindChildIdx()` computes `base + popcount(mask-before-char)` (contiguous ranking).
   - `compileStaticSegment()` appends new children over time without preserving sorted contiguous layout per parent.
   - Later insertions can break rank-to-index mapping and route lookups become non-deterministic.
   - Recommendation: finalize subrouter with a bake phase that compacts/sorts child ranges, or maintain explicit per-char index maps.

4. **Control compiler mutates API entry points on range-copy values**
   - In `BakeAll`, loop uses `for _, api := range cfg.Apis` and assigns `api.EntryPoint = ...`.
   - This modifies the loop variable copy, not the original config slice entry.
   - Result: computed entrypoints are lost unless used immediately in loop.
   - Recommendation: iterate by index (`for i := range cfg.Apis`) and mutate `cfg.Apis[i]`.

5. **`linkBreaks()` patches all breaks globally, not switch-local**
   - Compiler resolves `BREAK` by scanning full `GlobalTable` and replacing any instruction named `BREAK`.
   - Nested/multiple switch blocks can have earlier breaks overwritten to the latest `exitID`.
   - Recommendation: track break placeholder indices per switch scope and patch only that scope.

6. **Management plane likely has data races (shared compiler reused without locking)**
   - `ManagementServer` uses a shared `Compiler` containing mutable fields (`GlobalTable`, slot map, etc.).
   - `UnifiedSyncHandler` mutates compiler state; concurrent sync requests would race.
   - Recommendation: serialize sync operations with mutex or instantiate a fresh compiler per request.

## Medium-priority gaps

1. **Route normalization is inconsistent for root path**
   - `TrimSuffix(path, "/")` turns `/` into empty string.
   - This may create ambiguous routing behavior between empty path and root path.
   - Recommendation: normalize with explicit root preservation (`/` stays `/`).

2. **HTTP step has multiple global clients and ignores timeout parameter**
   - Multiple clients (`GlobalTransport`, `clientPool`, `poolClient`) exist but only one is used.
   - `HttpAction(timeout)` accepts timeout but does not apply it to request context/client.
   - Recommendation: unify to one tuned client/transport and enforce per-step timeout with context.

3. **String conversion helper uses unsafe aliasing**
   - `ByteToString` uses unsafe pointer conversion.
   - If backing byte slice is reused/mutated, string alias can observe corrupted data.
   - Recommendation: use safe conversion on boundary-sensitive keys (or document ownership constraints strictly).

4. **Engine test suite is stale against current APIs**
   - `internal/engine/engine_test.go` references missing `RequestContext`/`Run` signatures.
   - This prevents package-level CI signal for core engine behavior.
   - Recommendation: rewrite tests to current `rctx.Context` + `Execute`/`FlowManager` interfaces.

## Structural gaps affecting maintainability

1. **Duplicate router implementations with drift risk**
   - There are multiple router variants (`router.go`, `routerSmall.go`, `routerWithoutPadding.go`, string router variants).
   - Behavior and bug fixes can diverge across copies.
   - Recommendation: define a shared interface and shared correctness test matrix, then keep one primary implementation.

2. **Control compile path mixes planning and emission in one mutable object**
   - Makes thread-safety and incremental compilation difficult.
   - Recommendation: split into immutable plan analysis + instruction emission stages.

3. **MethodRoots appears unused in `ApiDefinition`**
   - Adds conceptual overhead and potential confusion about active routing strategy.
   - Recommendation: remove or wire it into runtime dispatch explicitly.

## Suggested next sequence (when you choose to fix)

1. Router atomic snapshot + root normalization.
2. Subrouter correctness (full-segment validation + deterministic child layout).
3. Compiler control-flow scoping (`BREAK`) and concurrent-safe compile session model.
4. Refresh engine tests, then profile.
