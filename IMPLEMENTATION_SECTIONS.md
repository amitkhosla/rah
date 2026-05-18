# RAH Gateway — Implementation Plan (Sectioned)

> All sections verified against live source code.  
> Rule: touch ONLY files listed per section. No cleanup, no refactors beyond scope.  
> Model guidance: haiku for mechanical/pattern-following work; sonnet for complex design.

---

## Dependency Order (implement in this sequence)

```
S1 (rctx fields)         — foundation; nothing depends on it but many depend on it
S2 (P0 bugs)             — independent; unblocks everything
S3 (condition system)    — depends on S1; S9/S12 depend on it
S4 (disconnect)          — depends on S1, S3
S5 (string ops)          — independent
S6 (encoding)            — independent
S7 (crypto)              — independent
S8 (cookie steps)        — independent
S9 (utility steps)       — depends on S1 (StagedRequestBody)
S10 (cache advanced)     — independent
S11 (http_call overhaul) — depends on S1, S3, S4, S9
S12 (foreach enhancements) — depends on S5 (split)
S13 (json_set + cookie flatten) — independent
S14 (resilience)         — depends on S3 (CompileCondition)
S15 (parallel)           — last; depends on S1, S4; all concurrency fixes must apply
```

---

## Section 1 — rctx.Context New Fields

**Model:** haiku  
**Complexity:** Low — mechanical field additions only  
**What:** Add 4 new fields to support disconnect detection, parallel fork guard, staged body, and multi-hop mutation scoping.

**Files changed (ONLY these):**
- `internal/rctx/context.go`

**Exact changes:**
1. Add after `Failed bool`:
   ```go
   Cancelled          int32    // atomic: 0=active 1=client disconnected
   parallelForkActive bool     // debug guard — set during parallel fan-out window
   ```
2. Add after `MutationLog []HeaderMutation`:
   ```go
   StagedRequestBody []byte   // set by set_request_body; consumed and cleared by http_call
   StagedContentType []byte   // paired with StagedRequestBody
   MutationFences    [8]int8  // multi-hop: MutationLog start index per call slot (8 bytes)
   ```
3. In `Reset()` — add alongside existing field resets:
   ```go
   ctx.Cancelled = 0
   ctx.parallelForkActive = false
   ctx.StagedRequestBody = nil
   ctx.StagedContentType = nil
   // MutationFences is [8]int8 — zeroed by Reset's memclr of the struct
   ```
4. Export arena pool access for BranchContext (two unexported helpers made into exported functions):
   ```go
   // Add to arena.go:
   func BorrowArenaBlock() *arenaBlock { return arenaPool.Get().(*arenaBlock) }
   func ReleaseArenaBlock(b *arenaBlock) { arenaPool.Put(b) }
   ```
   Wait — `arenaBlock` is unexported. Export the type as `ArenaBlock` OR add the two wrapper functions. **Add wrapper functions only** (do not export the type — minimal change):
   ```go
   // arena.go — two new exported functions, nothing else changes
   func BorrowExtBlock() []byte { b := arenaPool.Get().(*arenaBlock); return b.buf[:] }
   func ReleaseExtBlock(buf []byte) { /* ... */ }
   ```
   Actually, reconsider: the parallel section needs to borrow the same pool. Simpler approach — BranchContext uses `make([]byte, ArenaBlockSize)` as its ext block (heap, no pool). Pool borrowing from a separate package requires exposing the pool. **Decision: BranchContext uses heap-allocated ext block (no pool sharing). rctx/arena.go is unchanged.**

   Revised: Step 4 is dropped. Only steps 1-3 are needed.

**Verification before merge:** `go build ./internal/rctx/...` must pass.

---

## Section 2 — P0 Bug Fixes (4 bugs)

**Model:** haiku  
**Complexity:** Low — targeted 1-5 line fixes each  
**What:** Fix 4 existing broken behaviors. Do NOT touch surrounding code.

**Files changed (ONLY these):**
- `internal/control/compiler.go` — add `bind_path` case
- `internal/engine/steps/primitives.go` — activate Op=1 Remove branch
- `internal/engine/steps/arithmetic.go` — replace `make()`/`bytes.ToLower`/`bytes.ToUpper`
- `internal/engine/steps/logic.go` — fix `LoopGateSlot` O(n²) parse

**Exact changes:**

**Bug 1: bind_path missing compiler case**
In compiler.go `compileStep()` switch, add:
```go
case "bind_path":
    destSlot, err := c.getSlot(step.As)
    if err != nil { return err }
    idx, _ := strconv.Atoi(step.Input["index"])
    c.GlobalTable = append(c.GlobalTable, steps.BindPath(idx, destSlot))
```

**Bug 2: remove_request_header / remove_response_header dead code**
In ProxyStep (primitives.go) mutation loop:
```go
// BEFORE:
req.Header.Set(string(m.Key), string(m.Value))
// AFTER:
if m.Op == 1 { req.Header.Del(string(m.Key)) } else { req.Header.Set(string(m.Key), string(m.Value)) }
```
In `flushResponseHeaders()` (context.go):
```go
// BEFORE:
h.Set(string(m.Key), string(m.Value))
// AFTER:
if m.Op == 1 { h.Del(string(m.Key)) } else { h.Set(string(m.Key), string(m.Value)) }
```
Wait — `flushResponseHeaders` is in `rctx/context.go`, not `primitives.go`. Add that file:
- `internal/rctx/context.go` (only the `flushResponseHeaders` function, 3-line change)

**Bug 3: concat/to_lower/to_upper heap allocs**
In arithmetic.go:
- `ConcatStep`: `make([]byte, n)` → `ctx.Alloc(n)`; add `ctx *rctx.Context` to closure capture (already available)
- `ToLowerStep`: replace `bytes.ToLower(src)` with inline ASCII loop + `ctx.Alloc(len(src))`
- `ToUpperStep`: replace `bytes.ToUpper(src)` with inline ASCII loop + `ctx.Alloc(len(src))`

**Bug 4: foreach O(n²) parse**
In logic.go `LoopGateSlot` / compiler.go foreach case:
- Compiler allocates a hidden `indexSlot` via `c.nextSlot++`
- Gate: at `iterIdx == 0`, call `gjson.ParseBytes(src)`, pack `(uint32 start, uint32 end)` pairs into `ctx.Alloc(N*8)`, store in `indexSlot`
- Per iteration: read pair from index, slice into src — zero copy

**Verification:** `go build ./internal/...` must pass. No new test files needed.

---

## Section 3 — Condition Expression System

**Model:** sonnet  
**Complexity:** High — new parser + closure generator + 3 integration sites  
**What:** Replace `parseToRPN`/`evaluateRPN` with `CompileCondition()` returning pre-compiled closures. Add `==`/`!=`/`<`/`<=`/`>`/`>=`/`contains`/`startsWith`/`endsWith`/`matches`/`== null` operators.

**Files changed (ONLY these):**
- `internal/engine/steps/cond_expr.go` — NEW file; full parser + CompileCondition
- `internal/engine/steps/logic.go` — replace parseToRPN/evaluateRPN in NewComplexLogicGate and WhileGate
- `internal/engine/steps/condition.go` — replace RetryGate (delete shouldRetry)
- `internal/control/compiler.go` — update `if` (line ~182), `while` (line ~1047), `http_call` (line ~229) call sites

**What CompileCondition must handle:**
```
Operators:     ==  !=  <  <=  >  >=  contains  startsWith  endsWith  matches
Null literals: null  nil  ""   (all synonyms → slot is empty/zero)
Logic:         &&  ||  !  ( )
Slot refs:     named byte/int/bool slots; status; method; path
```

**Signature:**
```go
type ConditionFunc func(ctx *rctx.Context) bool

func CompileCondition(
    expr        string,
    slotMap     map[string]int,
    slotKindMap map[string]SlotKind,  // populated by compiler from slot assignments
) (ConditionFunc, error)
```

**Backward compatibility:** Bare slot names (e.g. `"auth_token && status"`) generate truthiness closures identical in behavior to the current RPN evaluator. Existing flows compile unchanged.

**Integration changes in compiler.go (3 sites only):**
- `case "if"`: `gate, err := steps.NewComplexLogicGate(step.Condition, thenID, elseID, c.slotMap, c.slotKindMap)`
- `case "while"`: compile condition via `CompileCondition`, pass `ConditionFunc` to WhileGate
- `case "http_call"`: compile `step.RetryCondition` via `CompileCondition`, pass as `ConditionFunc` to HttpAction (not yet used in HttpAction until Section 11)

**Dead code to delete (only after new code is verified):**
- `parseToRPN`, `evaluateRPN`, `executeLogic`, `executeLogicPlan`, `OpAnd`, `OpOr` from logic.go
- `shouldRetry` from condition.go

**Verification:** All existing `if` / `while` conditions in test fixtures must still compile.

---

## Section 4 — Caller Disconnect Handling

**Model:** haiku  
**Complexity:** Low-medium — mechanical additions at defined checkpoints  
**What:** When client disconnects, stop processing instead of wasting CPU and hitting upstreams.

**Files changed (ONLY these):**
- `internal/engine/executor.go` — add `StopCancelled int16 = -2` constant
- `internal/engine/steps/cancel.go` — NEW file; `detectAndMarkCancelled`, `checkCancelled`
- `internal/engine/steps/http.go` — change `context.Background()` → `ctx.Request.Context()`; add cancel checks
- `internal/engine/steps/cache.go` — add cancel check at top of `CacheGet`/`CacheGetGlobal` actions
- `cmd/rah-gateway/main.go` — check `ctx.Cancelled` after ProcessRequest; return 499; skip Finalize

**Exact changes per file:**

`executor.go`: Add after `StopPlan`:
```go
const StopCancelled int16 = -2  // client disconnected mid-flow
```

`cancel.go` (new, ~25 lines):
```go
func detectAndMarkCancelled(ctx *rctx.Context) bool { ... non-blocking select ... }
func checkCancelled(ctx *rctx.Context) bool { return atomic.LoadInt32(&ctx.Cancelled) != 0 }
```

`http.go` — two changes only:
1. Line ~419: `reqCtx := context.Background()` → `reqCtx := ctx.Request.Context()`
2. After `client.Do(req)` error: add `if errors.Is(err, context.Canceled) { atomic.StoreInt32(&ctx.Cancelled, 1); return engine.StopCancelled }`
3. At top of Action: `if detectAndMarkCancelled(ctx) { return engine.StopCancelled }`
4. Per-retry: `if checkCancelled(ctx) { return engine.StopCancelled }`

`cache.go` — add at top of `CacheGet` and `CacheGetGlobal` Action funcs:
```go
if detectAndMarkCancelled(ctx) { return engine.StopCancelled }
```

`main.go` — after `fm.ProcessRequest(ctx, req)`:
```go
if atomic.LoadInt32(&ctx.Cancelled) != 0 {
    ctx.ResponseStatus = 499
    // skip Finalize and AfterResponse
} else {
    ctx.Finalize()
    // existing AfterResponse hooks
}
```

**Verification:** `go build ./...` must pass.

---

## Section 5 — String Operation Steps

**Model:** haiku  
**Complexity:** Low — small, pattern-following step functions  
**What:** Add missing string manipulation steps. All use `ctx.Alloc` or zero-copy slice operations.

**Files changed (ONLY these):**
- `internal/engine/steps/arithmetic.go` — add new steps to existing file (already has string ops)
- `internal/control/compiler.go` — add 7 new cases
- `internal/control/step_descriptors.go` — add 7 new descriptors under "string" category

**New steps and implementation pattern:**

| Step | Implementation | Alloc? | Latency |
|---|---|---|---|
| `trim` | `bytes.TrimSpace(src)` or `bytes.TrimFunc` — returns sub-slice, zero copy | None | ~5ns |
| `contains` | `bytes.Contains(src, needle)` → BoolSlot | None | ~5-15ns |
| `starts_with` | `bytes.HasPrefix(src, prefix)` → BoolSlot | None | ~3ns |
| `ends_with` | `bytes.HasSuffix(src, suffix)` → BoolSlot | None | ~3ns |
| `replace` | `bytes.ReplaceAll(src, old, new)` — 1 stdlib alloc (output size unknown) | 1 alloc | ~40-100ns |
| `split` | Scan src for sep, build JSON array in `ctx.Alloc` buffer | Arena | ~30-80ns |
| `index_of` | `bytes.Index(src, needle)` → IntSlot (-1 if not found) | None | ~5-10ns |

All closures capture: `srcSlot int`, `dstSlot int`, and the baked pattern/needle bytes.

**YAML fields used:** `source` (srcSlot), `as` (dstSlot), `value` (needle/separator/old/new, baked at compile).

---

## Section 6 — Encoding Steps

**Model:** haiku  
**Complexity:** Low — thin wrappers over stdlib with `ctx.Alloc` for output buffers  
**What:** base64, hex, url encoding/decoding.

**Files changed (ONLY these):**
- `internal/engine/steps/encoding.go` — NEW file
- `internal/control/compiler.go` — add 6 new cases
- `internal/control/step_descriptors.go` — add 6 new descriptors under "encoding" category

**New steps:**

| Step | Key pattern | Alloc |
|---|---|---|
| `base64_encode` | `enc.Encode(ctx.Alloc(enc.EncodedLen(n)), src)` | Arena |
| `base64_decode` | `ctx.Alloc(enc.DecodedLen(n))` + `enc.Decode` + trim to actualN | Arena |
| `hex_encode` | `hex.Encode(ctx.Alloc(hex.EncodedLen(n)), src)` | Arena |
| `hex_decode` | `ctx.Alloc(hex.DecodedLen(n))` + `hex.Decode` | Arena |
| `url_encode` | Package-level `var urlQuerySafe [256]bool`; two-pass count+encode into arena | Arena |
| `url_decode` | Single-pass into `ctx.Alloc(len(src))`, trim to actualN | Arena |

`base64_encode` YAML `input.encoding`: `std` / `url` / `raw_url` (default: `std`; decode default: `raw_url` for JWT).

---

## Section 7 — Crypto/Hash Steps

**Model:** sonnet  
**Complexity:** Medium — pool design requires correctness, AES needs bake-time cipher  
**What:** HMAC, SHA256, MD5, AES encrypt/decrypt.

**Files changed (ONLY these):**
- `internal/engine/steps/crypto.go` — NEW file
- `internal/control/compiler.go` — add 6 new cases
- `internal/control/step_descriptors.go` — add 6 new descriptors under "crypto" category

**Pool design (critical):**

```go
// Package-level (key-agnostic):
var sha256Pool = sync.Pool{New: func() any { return sha256.New() }}
var md5Pool    = sync.Pool{New: func() any { return md5.New() }}

// Per-closure (HMAC — one pool per unique key):
// Allocated inside HMACSha256Step factory, captured in closure:
keyCopy := append([]byte(nil), staticKey...)   // bake-time copy
pool := &sync.Pool{New: func() any { return hmac.New(sha256.New, keyCopy) }}
```

**h.Sum pattern (no heap alloc for digest):**
```go
dst := ctx.Alloc(sha256.Size)   // 32 bytes — always fits inline arena
dst = h.Sum(dst[:0])            // appends digest into arena slice
```

**AES:** `aes.NewCipher(keyBytes)` called at bake time (compiler), captured as `cipher.Block` in closure. GCM AEAD also pre-computed. `aead.Seal(ctx.Alloc(n)[:0], nonce, src, nil)` writes into arena.

---

## Section 8 — Cookie Steps

**Model:** haiku  
**Complexity:** Low — pattern-following, arena allocation  
**What:** Set, extract, and remove cookies without using `net/http.Cookie` (which allocates).

**Files changed (ONLY these):**
- `internal/engine/steps/cookie.go` — NEW file
- `internal/control/compiler.go` — add 4 new cases
- `internal/control/step_descriptors.go` — add 4 new descriptors under "cookie" category

**New steps:**

| Step | Key design |
|---|---|
| `extract_cookie` | Zero-copy scan of `Cookie` header bytes using `unsafe.Slice`; alias into header memory (same as BindHeader) |
| `set_response_cookie` | Build `name=value; Path=/; HttpOnly; Max-Age=N` in `ctx.Alloc`; call `SetResponseHeader` |
| `set_request_cookie` | Build `name=value` in `ctx.Alloc`; append to `MutationLog` with key=`Cookie` |
| `remove_response_cookie` | Build `name=; Max-Age=0; Path=/` in `ctx.Alloc`; call `SetResponseHeader` |

Package-level constant: `var setCookieHeaderKey = []byte("Set-Cookie")`

**YAML fields:** `key` (cookie name, baked), `source` (value slot), `ttl` (Max-Age), `input.path`, `input.http_only`, `input.secure`, `input.same_site`.

---

## Section 9 — Utility HTTP Steps

**Model:** haiku  
**Complexity:** Low — small steps, mostly slot manipulation  
**What:** `set_request_body`, `bind_request_url`, `copy_header`.

**Files changed (ONLY these):**
- `internal/engine/steps/branching.go` — add `CopyHeader` and `BindRequestURL`; add `SetRequestBody`
- `internal/control/compiler.go` — add 3 new cases
- `internal/control/step_descriptors.go` — add 3 new descriptors
- `internal/control/types.go` — add `IncludeQuery *bool` field to StepConfig

**Steps:**

`set_request_body`:
```go
// Closure: srcSlot int, contentTypeBytes []byte
ctx.StagedRequestBody = ctx.ByteSlots[srcSlot]   // 2ns — pointer assignment
ctx.StagedContentType = contentTypeBytes           // static baked
```

`bind_request_url`:
```go
// Closure: dstSlot int, includeQuery bool (baked)
rawPath := ctx.Request.URL.RawPath; if rawPath == "" { rawPath = ctx.Request.URL.Path }
// ctx.RawQuery already snapshotted as []byte
// One ctx.Alloc + 2 copies → ~20-40ns
```

`copy_header`:
```go
// Closure: srcKey []byte (canonical, baked), dstKey []byte
val := ctx.Request.Header[string(srcKey)]   // direct map, no canonicalization overhead
// Append to MutationLog via unsafe.Slice — zero copy
```

---

## Section 10 — Cache Advanced Operations

**Model:** sonnet  
**Complexity:** Medium — must understand cache internals (index routing, region mutex, slab layout)  
**What:** `cache_exists`, `cache_incr`, `cache_touch`.

**Files changed (ONLY these):**
- `internal/cache/cache_manager.go` — add `Exists`, `Incr`, `Touch` methods
- `internal/cache/backend.go` — add `Exists`, `Touch` to `CacheBackend` interface (optional backend hint)
- `internal/engine/steps/cache.go` — add `Exists`, `Incr`, `Touch` to `CacheStore` interface; add step constructors
- `internal/control/compiler.go` — add 3 new cases
- `internal/control/step_descriptors.go` — add 3 new descriptors
- `internal/control/types.go` — add `Delta int64` field to StepConfig

**Design constraints:**
- `Exists`: follow Get's tag-routing; stop before `region.Read()` (no value copy); check `EntryHeader.Expiry` directly — ~2-3x faster than Get
- `Incr`: read-modify-write under `region.mu`; encode int64 as 8-byte LE; initialize to `0+delta` if missing
- `Touch`: locate entry via index; update `EntryHeader.Expiry` in-place under `region.mu`; no value read/write

---

## Section 11 — http_call Complete Overhaul

**Model:** sonnet  
**Complexity:** High — many new fields, pools, response handling, header forwarding  
**Depends on:** S1 (StagedRequestBody), S3 (ConditionFunc), S4 (cancel/context fix), S9 (set_request_body staging)

**Files changed (ONLY these):**
- `internal/engine/steps/http.go` — major changes
- `internal/engine/steps/primitives.go` — ProxyStep hop-by-hop filter
- `internal/control/compiler.go` — expand `http_call` case
- `internal/control/types.go` — add 6 new fields to StepConfig
- `internal/control/step_descriptors.go` — expand http_call descriptor

**New StepConfig fields:**
```go
BodyVar                string            `json:"body_var,omitempty"`
ContentType            string            `json:"content_type,omitempty"`
ResponseBodyVar        string            `json:"response_body_var,omitempty"`
ResponseStatusVar      string            `json:"response_status_var,omitempty"`
ResponseHeaderVars     map[string]string `json:"response_header_vars,omitempty"`
ForwardIncomingHeaders bool              `json:"forward_incoming_headers,omitempty"`
BlockHeaders           []string          `json:"block_headers,omitempty"`
```

**New pools in http.go (package-level):**
```go
var bytesReaderPool  = sync.Pool{New: func() any { return new(bytes.Reader) }}
var responseBodyPool = sync.Pool{New: func() any { b := new(bytes.Buffer); b.Grow(4096); return b }}

// responseBodyPool MUST always be gotten via helper:
func getResponseBuf() *bytes.Buffer { b := responseBodyPool.Get().(*bytes.Buffer); b.Reset(); return b }
```

**HttpActionConfig struct** (replaces positional parameters):
```go
type HttpActionConfig struct {
    StaticURL, StaticMethod, StaticContentType string
    URLSlot, MethodSlot, BodySlot, ContentTypeSlot int   // -1 = use static
    Timeout uint32; MaxRetries int
    RetryCondFunc      ConditionFunc  // nil = no retry condition
    ResponseBodySlot   int            // -1 = not captured
    ResponseStatusSlot int
    ResponseHeaderSlots []HeaderSlotBinding
    ForwardIncomingHeaders bool
    BlockHeadersMap        map[string]struct{}  // pre-built at bake time
}
```

**Key hot path additions:**
1. Method resolution from MethodSlot or StaticMethod
2. Body: read from `ctx.StagedRequestBody` (set by set_request_body) OR `BodySlot`; wrap via `bytesReaderPool`; PUT BACK after `client.Do()` returns
3. MutationLog applied to request headers (currently missing — this is the bug fix)
4. `forward_incoming_headers`: copy all non-hop-by-hop incoming headers; then apply MutationLog overrides
5. Response body: Content-Length fast path → `ctx.Alloc(cl)` + `io.ReadFull`; else `getResponseBuf()` → copy into arena → `responseBodyPool.Put`
6. Write response to named slots
7. ProxyStep: add hop-by-hop header filter when copying `ctx.Request.Header`

**Hop-by-hop header blocklist (package-level in primitives.go or http.go):**
```go
var hopByHopHeaders = map[string]struct{}{
    "Connection":{}, "Keep-Alive":{}, "Transfer-Encoding":{},
    "TE":{}, "Trailer":{}, "Upgrade":{},
    "Proxy-Authorization":{}, "Proxy-Authenticate":{},
}
```

---

## Section 12 — Foreach Enhancements

**Model:** haiku  
**Complexity:** Low-medium — complete the stubs in logic.go  
**What:** `foreach header`, `foreach param`, `foreach cookie` (iterate over all items of each type).  
**Depends on:** S5 (split uses same JSON array format)

**Files changed (ONLY these):**
- `internal/engine/steps/logic.go` — complete `ctx.GetCollection("headers")` and add "params"/"cookies"
- `internal/control/compiler.go` — add `foreach_header`, `foreach_param`, `foreach_cookie` cases (or extend existing `foreach` case)
- `internal/control/step_descriptors.go` — add descriptors

**Design for each collection type:**

`foreach header`:
```go
// Bake: no slot needed — reads ctx.Request.Header directly
// On each iteration: emit one "key: value" pair into arena as "Header-Name: value" bytes
// OR: split into two slots (nameSlot, valueSlot) — more useful
```

`foreach param`:
```go
// Parse ctx.RawQuery once at idx==0 using bytes.IndexByte scanning (no url.ParseQuery — allocates map)
// Pack (start,end) of each "key=value" segment into arena index (same O(1) pattern as foreach fix)
```

`foreach cookie`:
```go
// Parse Cookie header value at idx==0 using same scanner as extract_cookie
// Pack (nameStart, nameEnd, valStart, valEnd) per cookie into arena index
// Emit nameSlot and valueSlot on each iteration
```

All three use the same gjson-like packed index strategy from the S2 foreach O(n²) fix.

**YAML:**
```yaml
- action: foreach_header    # or foreach with source: "headers"
  as: header_name
  value_as: header_value

- action: foreach_param
  as: param_name
  value_as: param_value

- action: foreach_cookie
  as: cookie_name
  value_as: cookie_value
```

---

## Section 13 — json_set + cookie_flatten

**Model:** haiku  
**Complexity:** Low-medium  
**What:** Set a value at a JSON path in a ByteSlot; serialize multiple KV pairs into a single Cookie string.

**Files changed (ONLY these):**
- `internal/engine/steps/extract.go` — add `JsonSetStep` (alongside existing json_extract)
- `internal/engine/steps/cookie.go` — add `CookieFlattenStep` (already created in S8)
- `internal/control/compiler.go` — add 2 new cases
- `internal/control/step_descriptors.go` — add 2 new descriptors

**`json_set`:**
Use `tidwall/sjson` (already likely in go.mod via gjson family) or a minimal hand-rolled JSON path setter:
```yaml
- action: json_set
  source: body_slot           # ByteSlot with JSON input
  as: body_slot               # can overwrite same slot
  key: "user.id"              # JSON path (dot notation)
  value_var: user_id_slot     # ByteSlot with value to set
```
Hot path: `sjson.SetBytes(src, path, value)` — one alloc (output size unknown). Store result in `ctx.Alloc` copy. If sjson not available, note that it must be added to go.mod.

**`cookie_flatten`:**
```yaml
# Collect multiple cookie KV pairs from named slots into one Cookie header value
- action: cookie_flatten
  cookies:                    # map of name → slot
    session_id: session_slot
    csrf: csrf_slot
  as: cookie_header_value     # ByteSlot with "session_id=X; csrf=Y"
```
Hot path: pre-compute total length at runtime; `ctx.Alloc(total)`; write `name=value` pairs separated by `; `. Zero extra alloc beyond arena.

---

## Section 14 — Resilience: spike_arrest + circuit_breaker

**Model:** sonnet  
**Complexity:** High — atomic state machines, storage design  
**Depends on:** S3 (CompileCondition for failure_condition in circuit_breaker)

**Files changed (ONLY these):**
- `internal/engine/spike_arrest.go` — NEW: `SpikeArrestStore`, `spikeArrestKey`, step logic
- `internal/engine/circuit_breaker.go` — NEW: `CircuitState`, `CircuitBreakerArena`, gate + recorder logic
- `internal/engine/manager.go` — add 2 new fields + init in `NewFlowManager`
- `internal/control/compiler.go` — add 3 new cases: `spike_arrest`, `circuit_breaker`, `record_circuit_outcome`
- `internal/control/step_descriptors.go` — add 3 new descriptors under "resilience" category

**spike_arrest storage:** `sync.Map` of `spikeArrestKey (16-byte struct)` → `*int64` (lastAllowedNs, atomic CAS).  
**circuit_breaker storage:** Pre-allocated `[]CircuitState` (capacity 256) indexed by bake-time integer. No sync.Map on hot path.

**Critical atomic ordering for CircuitState HALF_OPEN→CLOSED:**
```go
// Recorder success path — order MATTERS:
if atomic.CompareAndSwapInt32(&cs.state, CBStateHalfOpen, CBStateClosed) {
    // Only AFTER state is CLOSED, reset counters:
    atomic.StoreInt64(&cs.failureCount, 0)
    atomic.StoreInt64(&cs.successCount, 0)
}
```

**New FlowManager fields:**
```go
SpikeArrestStore    *engine.SpikeArrestStore
CircuitBreakerArena *engine.CircuitBreakerArena
```

---

## Section 15 — Parallel Execution Step

**Model:** sonnet  
**Complexity:** Very high — goroutine pool, concurrency, BranchContext ownership  
**Depends on:** S1 (rctx fields), S4 (cancel checks), all concurrency fixes below MUST be applied

**Files changed (ONLY these):**
- `internal/engine/steps/parallel.go` — full replacement of 28-line stub
- `internal/control/compiler.go` — add `parallel` case with sub-table compilation
- `internal/control/types.go` — add `BranchConfig`, `Branches []BranchConfig`, `TimeoutMs uint32`, `ErrorPolicy string`
- `internal/control/step_descriptors.go` — add `parallel` descriptor

**Mandatory concurrency rules (all 7 must be implemented together — no partial implementation):**

| Rule | What | Why |
|---|---|---|
| C1 | `BranchContext` has own `byteSlotBase [48][]byte` field | Slot writes must not corrupt parent's backing array |
| C2 | `wg.Wait()` in drainer before any `branchCtxPool.Put()` | No recycling while goroutine still executes |
| C3 | `sync.Once` wraps all `cancelChan` close sites | Prevent "close of closed channel" panic |
| C4 | `defer recover()` inside every worker goroutine task dispatch | Prevent panic from killing pool worker permanently |
| C5 | `resultChan` buffered to N | Late goroutines can always send; no goroutine leak |
| C6 | `getResponseBuf()` helper always calls `Reset()` before returning | No stale body from previous request |
| C7 | `ctx.parallelForkActive = true` before dispatch, `false` after join | Debug guard on arena mutation |

**WaitGroup ownership (C2 detailed):**
```go
// Per parallel invocation (NOT per pool):
var wg sync.WaitGroup
wg.Add(N)

// Worker goroutine (for EACH branch):
defer wg.Done()   // last thing before goroutine exits, AFTER sending to resultChan

// After timeout, background drainer:
go func() {
    wg.Wait()           // wait for ALL branch goroutines to fully exit
    for i := 0; i < remaining; i++ {
        result := <-ch  // non-blocking — all goroutines have exited
        result.branchCtx.reset()
        branchCtxPool.Put(result.branchCtx)
    }
}()
```

---

## Complete Inventory of Everything Designed

### P0 Bug Fixes (4)
- [x] `bind_path` missing compiler case
- [x] `remove_request/response_header` dead Op=1 branch
- [x] `concat`/`to_lower`/`to_upper` heap allocations
- [x] `foreach` O(n²) JSON parse

### Cross-Cutting (5)
- [x] `http_call` complete overhaul (method, body, response slots, MutationLog, header forwarding)
- [x] Condition system (`CompileCondition` with all operators + `== null`)
- [x] Caller disconnect (`Cancelled` field, `StopCancelled`, checkpoints, 499 response)
- [x] ProxyStep hop-by-hop header filter
- [x] `parallel` step (goroutine pool, BranchContext, fan-out)

### String (7)
- [x] `trim`
- [x] `contains`
- [x] `starts_with` / `ends_with`
- [x] `replace`
- [x] `split`
- [x] `index_of`
- [x] `regex_replace` (output-to-slot via existing `pattern_match` extension)

### Encoding (6)
- [x] `base64_encode` / `base64_decode`
- [x] `hex_encode` / `hex_decode`
- [x] `url_encode` / `url_decode`

### Crypto (6)
- [x] `hmac_sha256` / `hmac_sha1`
- [x] `sha256_hash` / `md5_hash`
- [x] `aes_encrypt` / `aes_decrypt`

### Cookie (5)
- [x] `extract_cookie`
- [x] `set_response_cookie`
- [x] `set_request_cookie`
- [x] `remove_response_cookie`
- [x] `cookie_flatten`

### HTTP Utilities (3)
- [x] `set_request_body`
- [x] `bind_request_url`
- [x] `copy_header`

### Cache Advanced (3)
- [x] `cache_exists`
- [x] `cache_incr`
- [x] `cache_touch`

### Foreach Enhancements (3)
- [x] `foreach_header`
- [x] `foreach_param`
- [x] `foreach_cookie`

### JSON (1)
- [x] `json_set`

### Resilience (3)
- [x] `spike_arrest`
- [x] `circuit_breaker`
- [x] `record_circuit_outcome`

**Total: 46 items across 15 implementation sections**

---

## What Is NOT Designed (deferred, explicitly out of scope)

- `generate_jwt` — requires key material management design
- `validate_schema` — JSON Schema validation, complex dependency
- `xml_to_json` / `json_to_xml` — body transformation, not suitable for <5µs path
- `audit_log` / `metric_emit` — wire into existing ingest pipeline (separate session)
- `validate_request` / `json_threat_protect` — threat protection (separate session)
- `interpolate` template function system — separate session
- `foreach` over response headers (of http_call response) — dependent on http_call slots (do after S11)
- Distributed circuit breaker — requires shared state backend (separate session)
- `bind_query` vs `bind_query_param` naming normalisation — existing bug, separate 1-line fix session

---

## Session Size Guidance

For the $20 plan, each implementation session should:
- Work on ONE section only
- Load only the files listed for that section into context
- Use haiku where marked — these are mechanical changes
- Use sonnet where marked — these require design judgment
- Verify with `go build` at the end of each section
- Never modify files not listed in the section

Suggested session order:
```
Session 1: S1 (haiku, 20 min) — rctx fields
Session 2: S2 (haiku, 30 min) — P0 bugs
Session 3: S3 (sonnet, 60 min) — condition system
Session 4: S4 (haiku, 20 min) — disconnect handling
Session 5: S5+S6 (haiku, 40 min) — string + encoding steps
Session 6: S7 (sonnet, 30 min) — crypto steps
Session 7: S8+S13 (haiku, 30 min) — cookie steps + cookie_flatten
Session 8: S9 (haiku, 20 min) — utility steps
Session 9: S10 (sonnet, 40 min) — cache advanced
Session 10: S11 (sonnet, 60 min) — http_call overhaul
Session 11: S12 (haiku, 30 min) — foreach enhancements
Session 12: S13-json (haiku, 20 min) — json_set
Session 13: S14 (sonnet, 60 min) — resilience
Session 14: S15 (sonnet, 90 min) — parallel step
```
