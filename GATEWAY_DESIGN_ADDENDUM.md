# RAH Gateway — Design Addendum

> Supplement to GATEWAY_DESIGN_COMPLETE.md  
> Generated: 2026-05-18

---

## 1. Condition: `== null` / `!= null` Support

### Problem

The UI has no way to express "slot was not set" or "slot is empty". Users need to write:

```yaml
condition: auth_token == null
condition: response_body != null
condition: retry_count == null
```

### Design

**New token kind:** The `CompileCondition` parser recognises three synonyms as null literals: `null`, `nil`, `""` — all produce `TokenKindNullLit`. This means UI code may emit whichever is most natural.

**`slotKindMap` added to `CompileCondition` signature:**

```go
func CompileCondition(
    expr        string,
    slotMap     map[string]int,
    slotKindMap map[string]SlotKind,  // NEW: Byte | Int | Bool
) (CondFunc, error)
```

**Closure generator per slot type:**

```go
// ByteSlot == null  →  len(ctx.ByteSlots[idx]) == 0
func byteIsNull(idx int) CondFunc {
    return func(ctx *rctx.Context) bool { return idx >= len(ctx.ByteSlots) || len(ctx.ByteSlots[idx]) == 0 }
}
// ByteSlot != null  →  len(ctx.ByteSlots[idx]) > 0
func byteNotNull(idx int) CondFunc {
    return func(ctx *rctx.Context) bool { return idx < len(ctx.ByteSlots) && len(ctx.ByteSlots[idx]) > 0 }
}

// IntSlot == null  →  ctx.IntSlots[idx] == 0   (zero is the "not set" convention)
func intIsNull(idx int) CondFunc {
    return func(ctx *rctx.Context) bool { return idx >= len(ctx.IntSlots) || ctx.IntSlots[idx] == 0 }
}

// BoolSlot == null  →  ctx.BoolSlots[idx] == false
// NOTE: false is both "not set" and the value false — document this limitation to users.
// Use a ByteSlot presence flag when distinguishing "not set" from "false" matters.
func boolIsNull(idx int) CondFunc {
    return func(ctx *rctx.Context) bool { return idx >= len(ctx.BoolSlots) || !ctx.BoolSlots[idx] }
}
```

**Grammar addition (backward-compatible):**
```
term ::= slot_ref ("==" | "!=") null_lit   ← new null-check term
       | slot_ref                           ← existing truthiness term (unchanged)
null_lit ::= "null" | "nil" | `""`
```

**YAML examples:**
```yaml
# null check
- action: if
  condition: auth_token == null
  then: return_401

# not-null check
- action: if
  condition: response_body != null
  then: parse_body

# compound
- action: if
  condition: auth_token != null && user_role != null
  then: check_permissions
```

---

## 2. Caller Disconnect / Timeout — Stop Processing

### Problem

When an HTTP client disconnects (mobile closes app, proxy times out), the gateway continues executing — wasting CPU, hitting upstreams unnecessarily, and delaying context release. Currently `HttpAction` also uses `context.Background()` so it ignores client disconnects even during active network calls.

### Design

**New field on `rctx.Context`:**
```go
// Place after the Failed bool field, in the same cache line as other control flags.
Cancelled int32  // atomic: 0 = active, 1 = client disconnected
```
Reset in `ctx.Reset()`: `ctx.Cancelled = 0`

**New sentinel in `executor.go`:**
```go
const StopPlan      int16 = -1  // existing
const StopCancelled int16 = -2  // NEW: client disconnected
```
The `Execute()` loop condition `pc >= 0` already handles both — no loop change needed.

**Two helpers (new file `internal/engine/steps/cancel.go`):**
```go
// detectAndMarkCancelled: non-blocking select, ~2-5ns. Use at IO checkpoints.
func detectAndMarkCancelled(ctx *rctx.Context) bool {
    if ctx.Request == nil { return false }
    select {
    case <-ctx.Request.Context().Done():
        atomic.StoreInt32(&ctx.Cancelled, 1)
        return true
    default:
        return false
    }
}

// checkCancelled: fast int32 read, ~1ns. Use inside retry loops.
func checkCancelled(ctx *rctx.Context) bool {
    return atomic.LoadInt32(&ctx.Cancelled) != 0
}
```

**Checkpoints (where to add the check):**

| Step | Check type | Location |
|---|---|---|
| `HttpAction` entry | `detectAndMarkCancelled` | Before URL resolution |
| `HttpAction` per-retry | `checkCancelled` | Top of retry loop (1ns) |
| `HttpAction` after `client.Do` error | `errors.Is(err, context.Canceled)` | Set Cancelled=1, return StopCancelled |
| `CacheGet` / `CacheGetGlobal` | `detectAndMarkCancelled` | Before `store.Get()` |
| `parallel` fan-out | `detectAndMarkCancelled` | Before goroutine launch |
| LLM call steps | `detectAndMarkCancelled` | Before HTTP client construction |

**Critical fix for `HttpAction` (http.go line ~419):**
```go
// BEFORE (ignores client disconnect during upstream call):
reqCtx := context.Background()

// AFTER (client disconnect cancels in-flight upstream call):
reqCtx := ctx.Request.Context()
```
With this change, `client.Do()` returns `context.Canceled` immediately when the client disconnects. Then:
```go
if errors.Is(err, context.Canceled) {
    atomic.StoreInt32(&ctx.Cancelled, 1)
    return engine.StopCancelled
}
```

**`main.go` handler changes:**
```go
fm.ProcessRequest(ctx, req)

if atomic.LoadInt32(&ctx.Cancelled) != 0 {
    ctx.ResponseStatus = 499  // nginx convention for "client closed request"
    // Skip ctx.Finalize() — writing to a dead connection wastes a syscall
    // Skip AfterResponse hooks — connection is gone
} else {
    ctx.Finalize()
    for _, fn := range ctx.AfterResponse { fn() }
}
// Access log and telemetry still run regardless — disconnect IS a loggable event
```

**Steps that do NOT need a check:** `bind_*`, `set_header`, arithmetic, `if`/`switch`/`foreach` gates — all are pure in-memory operations (<100ns) where a cancellation check costs more than the step itself.

---

## 3. Step Naming: `bind_request_url` Confirmed + Inconsistency Found

### `bind_request_url`

**Status:** Not yet implemented — only in GATEWAY_DESIGN_COMPLETE.md as a design. The step name follows the existing convention correctly (`bind_` prefix for read operations, snake_case).

Existing URL-related bind steps for comparison:
- `bind_header` — "Read Header"
- `bind_query` — "Read Query Param"
- `bind_path` — "Read Path Param"
- `bind_client_ip` — "Bind Client IP"

So `bind_request_url` → UI title: **"Read Request URL"** is consistent.

### Existing Bug: `bind_query` vs `bind_query_param`

The step descriptor uses `Type: "bind_query"` but the compiler case expects `"bind_query_param"` (and `flow_profile.go` recognises both). This is an incomplete refactoring. Fix: normalise to `bind_query` everywhere and remove the `bind_query_param` alias.

### UI Naming Convention

All step types use **lowercase `snake_case`**. The `Title` field in the descriptor provides the human-readable UI label and should be used in the Studio UI — not the raw `Type`. Examples:

| Type (internal) | Title (UI display) |
|---|---|
| `bind_header` | Read Header |
| `bind_query` | Read Query Param |
| `bind_path` | Read Path Param |
| `http_call` | HTTP Call |
| `spike_arrest` | Spike Arrest |
| `circuit_breaker` | Circuit Breaker |
| `cache_exists` | Cache Exists |
| `hmac_sha256` | HMAC SHA-256 |
| `extract_cookie` | Extract Cookie |
| `set_response_cookie` | Set Response Cookie |

---

## 4. Header Forwarding to Upstreams

### Verified Current Behavior

| | ProxyStep | HttpAction |
|---|---|---|
| Forwards ALL incoming headers? | **YES** — `req.Header = ctx.Request.Header` (direct assignment) | **NO** — fresh request, no headers |
| Applies MutationLog headers? | YES | **NO** (bug — fixed in http_call overhaul) |
| Strips hop-by-hop headers? | **NO** — sends Connection, Keep-Alive, Upgrade etc. | N/A |

### Problems

1. **ProxyStep sends hop-by-hop headers** to upstreams — violates HTTP/1.1 spec (RFC 7230 §6.1). Upstreams that parse `Connection: keep-alive` directly get confused.
2. **HttpAction sends zero incoming headers** — every internal call starts clean. This is often correct (you don't want to forward a user's `Authorization` to every internal service) but should be configurable.
3. **No customer control** over which headers to forward or block.

### Design: Header Forwarding Control

#### ProxyStep — hop-by-hop filter (always applied, not configurable)

Add a package-level hop-by-hop header blocklist and apply it when copying headers:

```go
// Package-level in primitives.go
var hopByHopHeaders = map[string]struct{}{
    "Connection": {}, "Keep-Alive": {}, "Transfer-Encoding": {},
    "TE": {}, "Trailer": {}, "Upgrade": {},
    "Proxy-Authorization": {}, "Proxy-Authenticate": {},
}

// In ProxyStep, replace: req.Header = ctx.Request.Header
// With:
req.Header = make(http.Header, len(ctx.Request.Header))
for k, v := range ctx.Request.Header {
    if _, blocked := hopByHopHeaders[k]; !blocked {
        req.Header[k] = v
    }
}
// Then apply MutationLog overrides as before
```

This is a correctness fix, not optional. Cost: one `make` + map iteration (~200-500ns for typical request). Acceptable — ProxyStep is not on the <5µs no-upstream path.

#### HttpAction — configurable header forwarding

Add `forward_incoming_headers` to `StepConfig` and `HttpActionConfig`:

```go
// StepConfig addition:
ForwardIncomingHeaders bool     `json:"forward_incoming_headers,omitempty"` // default: false
BlockHeaders           []string `json:"block_headers,omitempty"`            // headers to strip even if forwarding

// HttpActionConfig addition:
ForwardIncomingHeaders bool
BlockHeadersMap        map[string]struct{} // pre-compiled from BlockHeaders at bake time
```

**Hot path in HttpAction (after `http.NewRequestWithContext`):**
```go
// Forward incoming headers if configured (default: off — sends clean request)
if cfg.ForwardIncomingHeaders {
    for k, v := range ctx.Request.Header {
        if _, isHopByHop := hopByHopHeaders[k]; isHopByHop { continue }
        if _, isBlocked := cfg.BlockHeadersMap[k]; isBlocked { continue }
        req.Header[k] = v
    }
}
// Then apply MutationLog (always — allows explicit overrides regardless of forward setting)
for i := 0; i < ctx.MutationCount; i++ {
    m := ctx.MutationLog[i]
    req.Header.Set(string(m.Key), string(m.Value))
}
```

**YAML:**
```yaml
- action: http_call
  url: "https://internal-service/api"
  method: POST
  forward_incoming_headers: true           # propagate incoming request headers
  block_headers:
    - Authorization                         # but strip the user's auth token
    - X-Internal-Secret                     # and any internal headers
  response_body_var: service_response
```

**Default behavior (no `forward_incoming_headers`):** HttpAction sends a clean request + only explicit `set_request_header` mutations. This is the safe default — no accidental credential forwarding.

**New `StepConfig` fields:**
```go
ForwardIncomingHeaders bool     `json:"forward_incoming_headers,omitempty"`
BlockHeaders           []string `json:"block_headers,omitempty"`
```

---

## 5. Concurrency Safety Fixes (Priority-Ordered)

All issues found in the proposed parallel + pool designs. **None of these bugs exist in currently deployed code** — they are in designs not yet implemented. Fix them during implementation, not after.

---

### Fix C1 (CRITICAL): BranchContext must own its slot backing array

**Risk:** DATA CORRUPTION at any TPS.  
**Scenario:** If `BranchContext.ByteSlots` is initialised as a re-slice of the parent's `byteSlotBase` (`bc.ByteSlots = ctx.ByteSlots`), then `bc.ByteSlots[N] = val` writes to the **parent's** `byteSlotBase[N]`. When the parent resumes after the join, it sees corrupted slots — wrong upstream URLs, wrong auth headers, wrong routing decisions — silently.

**Fix:** `BranchContext` MUST have its own independent backing array:
```go
type BranchContext struct {
    ByteSlots    [][]byte
    byteSlotBase [rctx.BaseByteSlots][]byte  // OWN array, NOT a slice of parent's
    // ...
}

// Init:
bc.ByteSlots = bc.byteSlotBase[:rctx.BaseByteSlots]

// Slot copy from parent (copies HEADER VALUES, writes into bc's own backing array):
for _, idx := range cfg.ParentSlotsToCopy {
    bc.byteSlotBase[idx] = ctx.ByteSlots[idx]  // copy of slice header — NOT a re-slice
}
```
The copied slice header `bc.byteSlotBase[idx]` points to the same underlying bytes as the parent (read-only access — safe), but any **assignment** `bc.ByteSlots[idx] = newSlice` only modifies `bc.byteSlotBase[idx]`, leaving the parent's `ctx.byteSlotBase[idx]` untouched.

**Test to add:** Fork 2 branches, both write to slot 0; assert parent's slot 0 unchanged after join.

---

### Fix C2 (CRITICAL): BranchContext must not return to pool until branch goroutine exits

**Risk:** DATA CORRUPTION when any upstream times out.  
**Scenario:** Fan-out times out → parent calls `drainResultChan` in background → drainer calls `branchCtxPool.Put(bc)` → another request gets same `bc` and calls `reset()` clearing `branchArena` → but the late branch goroutine is still executing and writing to `bc.branchArena` → two goroutines write to same memory simultaneously.

**Fix:** Use a `sync.WaitGroup` per parallel invocation:
```go
var wg sync.WaitGroup
wg.Add(N)  // one per branch

// Each branch goroutine (in worker):
defer wg.Done()  // after sending to resultChan

// drainResultChan signature change:
func drainResultChan(ch chan branchResult, remaining int, wg *sync.WaitGroup) {
    for i := 0; i < remaining; i++ {
        result := <-ch
        wg.Wait()  // wait for that specific branch goroutine to fully exit
        result.branchCtx.reset()
        branchCtxPool.Put(result.branchCtx)
    }
}
```
Alternative: pass the WaitGroup into each branchTask; the drainer calls `wg.Wait()` before any Put.

---

### Fix C3 (CRITICAL): cancelChan double-close panic

**Risk:** PANIC on any concurrent timeout + fan-out cancellation.  
**Scenario:** Fan-out timeout fires → timeout handler calls `close(cancelChan)` → simultaneously the first-failure handler also calls `close(cancelChan)` → panic: "close of closed channel".

**Fix (one line per close site):**
```go
var closeOnce sync.Once
cancelFn := func() { closeOnce.Do(func() { close(cancelChan) }) }
// All close() call sites replaced with cancelFn()
```

---

### Fix C4 (HIGH): Worker goroutine panic kills pool worker permanently

**Risk:** Pool exhaustion → goroutine leak → eventual deadlock on fan-out.  
**Scenario:** Any step inside a branch panics (nil pointer, slice bounds). Without recovery, the worker goroutine exits. Pool shrinks by 1 permanently. At 0.001% panic rate and 10k req/s, pool reaches 0 workers in ~2 minutes.

**Fix:** Wrap each task dispatch in a deferred recover:
```go
func (wp *workerPool) runWorker() {
    for task := range wp.taskChan {
        func() {
            defer func() {
                if r := recover(); r != nil {
                    // Send sentinel error so fan-out is never blocked
                    task.branchCtx.Failed = true
                    task.branchCtx.ErrorCode = 500
                    task.resultChan <- branchResult{branchCtx: task.branchCtx, failed: true}
                }
            }()
            result := executeBranch(task)
            task.resultChan <- result
        }()
    }
}
```

---

### Fix C5 (HIGH): responseBodyPool — always Reset on Get

**Risk:** DATA CORRUPTION — response from previous request prepended to current response.  
**Scenario:** `responseBodyPool.Get()` returns a buffer with stale bytes from a previous request. Without `Reset()`, `io.Copy` appends to the stale data. Client receives `{previous_response_body}{current_response_body}` concatenated.

**Fix:** Wrap `Pool.Get()` in a helper that always resets:
```go
func getResponseBuf() *bytes.Buffer {
    buf := responseBodyPool.Get().(*bytes.Buffer)
    buf.Reset()  // mandatory — never skip
    return buf
}
```
And ensure `Put()` is called only **after** the caller has finished reading from the buffer — never inside a defer that fires before the write completes.

---

### Fix C6 (MEDIUM): CircuitState HALF_OPEN double-probe

**Risk:** More probes than intended during upstream flapping.  
**Scenario:** Probe succeeds → recorder increments `successCount` and CASes to CLOSED → between these two operations, `successCount` briefly returns to 0 → another goroutine claims the probe slot.

**Fix:** The HALF_OPEN → CLOSED transition must be a single atomic operation. Pack `(state, successCount)` into one `int64` and CAS the full word, or ensure the recorder transitions state to CLOSED before clearing the probe flag:
```go
// In recorder success path (HALF_OPEN):
// Step 1: CAS state HALF_OPEN → CLOSED (atomic transition)
if atomic.CompareAndSwapInt32(&cs.state, CBStateHalfOpen, CBStateClosed) {
    // Step 2: Only NOW reset counters — state is already CLOSED so no new probes possible
    atomic.StoreInt64(&cs.failureCount, 0)
    atomic.StoreInt64(&cs.successCount, 0)
}
```
Once state is CLOSED, the gate step sees `CBStateClosed` and passes all requests through — no CAS(0, -1) is attempted. The order matters: transition first, clear counters second.

---

### Fix C7 (LOW): arenaInline immutability guard for future safety

**Risk:** Latent — safe today, breaks silently if any future step calls `ctx.Alloc()` between fork and join.

**Fix:** Add a debug-mode guard to `Context`:
```go
parallelForkActive bool  // set true before dispatch, false after join
```
In `ctx.Alloc()` debug builds:
```go
if ctx.parallelForkActive {
    panic("ctx.Alloc() called while parallel branches are active — arena is frozen")
}
```
Cost in production: zero (compile-time constant `false` + `if false` is eliminated by compiler).

---

## 6. Summary of All Changes Required

### New fields on `rctx.Context`
```go
Cancelled          int32    // atomic; 0=active 1=disconnected
parallelForkActive bool     // debug guard
```

### New constant in `executor.go`
```go
const StopCancelled int16 = -2
```

### New file: `internal/engine/steps/cancel.go`
```go
func detectAndMarkCancelled(ctx *rctx.Context) bool
func checkCancelled(ctx *rctx.Context) bool
```

### Modified: `internal/engine/steps/http.go`
- Change `context.Background()` → `ctx.Request.Context()` at line ~419
- Add `detectAndMarkCancelled` at entry
- Add `errors.Is(err, context.Canceled)` → `StopCancelled` after `client.Do`
- Add hop-by-hop header copy for `ProxyStep`
- Add `forward_incoming_headers` / `block_headers` forwarding logic

### Modified: `internal/control/types.go`
```go
ForwardIncomingHeaders bool     `json:"forward_incoming_headers,omitempty"`
BlockHeaders           []string `json:"block_headers,omitempty"`
```

### Modified: `internal/control/compiler.go`
- Update `CompileCondition` call site to pass `slotKindMap`
- Add `bind_query` normalisation (remove `bind_query_param` alias)
- Add `forward_incoming_headers` + `block_headers` to `http_call` compiler case

### Modified: `cmd/rah-gateway/main.go`
- Check `ctx.Cancelled != 0` after `ProcessRequest`
- Return 499, skip `Finalize()` and `AfterResponse` hooks when cancelled

### Required implementation rules for parallel step
1. `BranchContext` struct must have own `byteSlotBase [48][]byte` — never re-slice parent
2. `sync.Once` for all `cancelChan` close sites
3. `WaitGroup` per parallel invocation — `Put` only after `wg.Done()`
4. `defer recover()` inside every worker goroutine task dispatch
5. `resultChan` buffered to N — guarantee branch can always send even after parent times out
6. `getResponseBuf()` helper always calls `Reset()` before returning buffer
