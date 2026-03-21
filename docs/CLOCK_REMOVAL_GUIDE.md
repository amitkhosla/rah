# Clock Package Removal Guide

## Executive Summary

The `internal/clock` package implements a **background goroutine that updates a global clock every 100ms**. This is an anti-pattern that should be removed in favor of:
1. **Direct `time.Now()` calls** for accurate timestamps
2. **Request-scoped timestamps** for consistency within a request

This guide explains the problem, the refactoring strategy, and the implementation steps.

---

## Problem Analysis

### The Current Anti-Pattern

The clock package maintains a global `CurrentClock` struct updated by a background goroutine:

```go
// internal/clock/clock.go
func StartClockHeartbeat() {
    go func() {
        ticker := time.NewTicker(100 * time.Millisecond)
        for range ticker.C {
            now := time.Now().Unix()
            elapsed := uint32(now - start)
            CurrentClock.ElapsedSec = elapsed
            CurrentClock.MinuteID = elapsed / 60
            CurrentClock.HourID = elapsed / 3600
            CurrentClock.DayID = elapsed / 86400
            CurrentClock.UnixCurTime = now
        }
    }()
}
```

This is used in `cache`, `engine`, and `control` packages to avoid calling `time.Now()` in hot paths.

### Why This Is an Anti-Pattern

| Issue | Impact | Severity |
|-------|--------|----------|
| **Background goroutine overhead** | Adds scheduler jitter, context switches, CPU usage | Medium |
| **Accuracy drift (±100ms)** | Clock readings are up to 100ms stale | High |
| **Global mutable state** | Difficult to test, synchronization issues | High |
| **Resource contention** | Goroutine + ticker + atomic updates | Low-Medium |
| **Unnecessary complexity** | Adds code that could be eliminated | Medium |

### Why `time.Now()` Cost is Negligible

The original rationale was avoiding the cost of `time.Now()` (~100-200ns):
- **Per-request overhead**: Single `time.Now()` call = 100-200ns
- **With 10 clock reads/request**: 1-2µs savings claimed
- **Actual benefit**: Negligible in most cases
- **Cost of anti-pattern**: Global state, testing complexity, accuracy loss

**Trade-off**: The 100-200ns saved per request is far outweighed by the complexity and accuracy loss.

---

## Dependency Analysis

### Packages Using Clock

From `docs/dependency-graph.md`:

| Package | Usage | Phase | Impact |
|---------|-------|-------|--------|
| **cache** | TTL checks on entry expiry | Runtime | High (every cache operation) |
| **engine** | Instruction timing, timeouts | Runtime | Medium (optional) |
| **control** | Initialization timing | Startup | Low |

### Incoming Dependencies (3 packages)

```
cache      → uses clock for TTL calculations
engine     → uses clock for instruction timing
control    → uses clock for startup timing
```

### Dependency Removal Order

1. **cache/cache_manager.go**: Replace TTL checks
2. **engine/** and **engine/steps/**: Replace timing logic
3. **control/**: Remove clock dependency
4. **cmd/rah-gateway/main.go**: Remove `StartClockHeartbeat()` call
5. **internal/clock/**: Delete package

---

## Refactoring Strategy

### Option 1: Direct time.Now() Calls (Simplest)

Replace all `clock.CurrentClock` reads with `time.Now()` calls.

**Pros**:
- ✓ Simplest refactoring
- ✓ Perfectly accurate
- ✓ No global state
- ✓ Easy to test

**Cons**:
- ✗ Slightly higher latency (100-200ns per call)
- ✗ More syscalls

**Verdict**: **Recommended** - The latency trade-off is worth the simplicity and accuracy.

**Example**:
```go
// Before (anti-pattern)
age := clock.CurrentClock.ElapsedSec - createdAt

// After (direct call)
age := uint32(time.Now().Unix()) - createdAt
```

### Option 2: Request-Scoped Timestamp (Best for Consistency)

Cache the timestamp at request start, reuse throughout.

**Pros**:
- ✓ Single `time.Now()` call per request
- ✓ Consistent timestamp within request
- ✓ Minimal latency overhead
- ✓ No global state

**Cons**:
- ✗ Requires context threading
- ✗ Slightly more complex

**Verdict**: **Recommended for hot paths** - Provides consistency + minimal overhead.

**Example**:
```go
// In rctx.Context
type Context struct {
    StartedAtUnixNano int64  // Set at request start
    // ... other fields ...
}

// At request start
ctx.StartedAtUnixNano = time.Now().UnixNano()

// Later in instructions
ageMicros := (time.Now().UnixNano() - ctx.StartedAtUnixNano) / 1000
```

---

## Implementation Plan

### Phase 1: Replace Cache TTL Checks

**File**: `internal/cache/cache_manager.go`

**Current**:
```go
import "rah/internal/clock"

func (cm *CacheManager) IsExpired(entry *Entry) bool {
    age := clock.CurrentClock.ElapsedSec - entry.CreatedAt
    return age > entry.TTL
}
```

**Refactored (Option 1 - Direct Call)**:
```go
import "time"

func (cm *CacheManager) IsExpired(entry *Entry) bool {
    now := uint32(time.Now().Unix())
    age := now - entry.CreatedAt
    return age > entry.TTL
}
```

**Refactored (Option 2 - Request-Scoped)**:
```go
func (cm *CacheManager) IsExpired(entry *Entry, requestStartNano int64) bool {
    elapsed := (time.Now().UnixNano() - requestStartNano) / 1_000_000_000
    age := uint32(elapsed) - entry.CreatedAt
    return age > entry.TTL
}
```

### Phase 2: Replace Engine Instruction Timing

**File**: `internal/engine/executor.go`

**Current**:
```go
import "rah/internal/clock"

started := clock.CurrentClock.UnixCurTime
// ... execute instruction ...
duration := clock.CurrentClock.UnixCurTime - started
```

**Refactored**:
```go
import "time"

started := time.Now().UnixNano()
// ... execute instruction ...
duration := time.Now().UnixNano() - started
```

### Phase 3: Remove Control Dependencies

**File**: `internal/control/compiler.go` and other control files

Search for all `clock` imports and remove them.

### Phase 4: Remove Main Entry Point Call

**File**: `cmd/rah-gateway/main.go`

**Remove**:
```go
import "rah/internal/clock"

// Remove this line:
clock.StartClockHeartbeat()
```

### Phase 5: Delete Clock Package

1. Delete `internal/clock/clock.go`
2. Delete `internal/clock/` directory

### Phase 6: Update Documentation

Files to update:
- `docs/dependency-graph.md` - Remove clock package section
- `docs/latency-design.md` - Update clock section
- `docs/packages/cache.md` - Remove clock dependency
- `docs/packages/engine.md` - Remove clock dependency
- `docs/packages/control.md` - Remove clock dependency
- `docs/packages/clock.md` - Mark as removed

---

## Detailed Refactoring Steps

### Step 1: Find All Clock Usage

```bash
grep -r "clock\." internal/ cmd/
grep -r "import.*clock" internal/ cmd/
```

Expected output: ~20-30 lines across cache, engine, control packages

### Step 2: Replace Cache TTL Checks

**File: internal/cache/cache_manager.go**

```go
// Find all lines like:
// clock.CurrentClock.ElapsedSec
// clock.CurrentClock.UnixCurTime

// Replace with:
time.Now().Unix()
time.Now().UnixNano()
```

### Step 3: Replace Engine Timing

**File: internal/engine/executor.go**

```go
// Find instruction timing code
// Replace clock reads with direct time.Now() calls
```

### Step 4: Replace Control Timing

**Files: internal/control/*.go**

```go
// Find any clock imports or usage
// Replace or remove
```

### Step 5: Remove Main Call

**File: cmd/rah-gateway/main.go**

```go
// Remove: clock.StartClockHeartbeat()
// Remove: import "rah/internal/clock"
```

### Step 6: Clean Up

```bash
# Remove the clock package
rm -rf internal/clock/

# Update go.mod (if clock had external deps)
go mod tidy

# Verify no remaining references
grep -r "clock\." internal/ cmd/ || echo "All clock references removed"
```

---

## Testing Strategy

### Unit Tests for Affected Packages

```bash
# Test cache after refactoring
go test -v ./internal/cache/

# Test engine after refactoring
go test -v ./internal/engine/

# Test control after refactoring
go test -v ./internal/control/
```

### Performance Regression Testing

```bash
# Benchmark before (create baseline)
go test -bench=. -benchmem ./internal/cache/ > before.txt

# After refactoring
go test -bench=. -benchmem ./internal/cache/ > after.txt

# Compare
benchstat before.txt after.txt
```

**Expected**: Negligible difference or slight improvement (less overhead from background goroutine)

---

## Impact Assessment

### Risk Level: **LOW**

- Removing clock is low-risk
- All uses are for timing/TTL calculations
- Fallback to `time.Now()` is always safe
- No logic changes, just timestamp sources

### Latency Impact: **Negligible**

| Scenario | Before | After | Delta |
|----------|--------|-------|-------|
| Cache hit (no clock call) | ~500ns | ~500ns | +0ns |
| Cache hit (with clock call) | ~500ns | ~700ns | +200ns |
| Instruction exec | 100-500ns | 100-500ns | ±0ns |
| **Total request (no upstream)** | 1-4µs | 1-4µs | **Negligible** |

The added latency is **sub-microsecond** and **immeasurable** in real workloads.

### Maintenance Impact: **HIGH POSITIVE**

- ✓ Removes 100+ lines of code
- ✓ Eliminates global mutable state
- ✓ Improves testability
- ✓ Improves accuracy
- ✓ Reduces complexity

---

## Rollback Strategy

If issues arise:

1. **Revert commits** using `git revert`
2. **Re-add clock package** with `git checkout <commit> internal/clock/`
3. **Restore main.go** clock initialization
4. **Re-add imports** to cache, engine, control

**Note**: Since tests may be failing anyway (per project guidance), rollback is low-risk.

---

## Migration Checklist

- [ ] Find all clock usage: `grep -r "clock\." internal/ cmd/`
- [ ] Update cache TTL checks to use `time.Now()`
- [ ] Update engine instruction timing to use `time.Now()`
- [ ] Update control package to remove clock imports
- [ ] Remove `StartClockHeartbeat()` call from main.go
- [ ] Delete `internal/clock/` directory
- [ ] Run `go mod tidy`
- [ ] Verify no remaining clock references
- [ ] Run performance benchmarks (cache, engine)
- [ ] Update all documentation files
- [ ] Create commit: "Remove clock antipattern, use time.Now() instead"

---

## Related Documentation

- **docs/latency-design.md**: Performance implications
- **docs/dependency-graph.md**: Package dependencies
- **docs/packages/clock.md**: Current (antipattern) design
- **docs/packages/cache.md**: Cache TTL implementation
- **docs/packages/engine.md**: Instruction timing
