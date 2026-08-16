# Event Context Injection & Pre-Pipeline

## Overview

This section defines how inbound events from Kafka, SQS, Pub/Sub, RabbitMQ, and other message brokers are injected into the request context (`rctx.Context`) and optionally pre-processed before the main flow executes. The architecture ensures **zero-copy semantics** for payloads in the data plane (ByteSlots), **pre-allocated slot allocation** at bake time, and **opt-in pre-processing** with configurable failure handling.

Key properties:
- **Well-known slot names** are reserved and pre-allocated by the compiler.
- **Header mappings** bind inbound message headers to flow variables at listener configuration time.
- **Pre-pipeline** is an optional list of flow steps compiled and executed before the main flow.
- **Large payload handling** uses inline arena for small payloads; pool overflow or heap fallback for larger data.
- **Batch mode** collects multiple deduplicated events into a JSON array for bulk processing.

---

## InboundMessage Type

**Location:** `internal/eventlistener/types.go`

```go
// InboundMessage represents a single event received from a message broker.
// All fields are immutable from the EventExecutor's perspective.
type InboundMessage struct {
	// Topic/Queue name (Kafka topic, SQS queue, Pub/Sub subscription, etc.)
	Topic string

	// Partition index (Kafka, for deterministic routing / idempotency tracking)
	Partition int32

	// Offset or sequence number (Kafka offset, SQS receive handle, etc.)
	// Used for idempotency and restart logic.
	Offset int64

	// Message routing key (Kafka key, event ID, or correlation ID)
	// May be empty for unkeyed topics/queues.
	Key []byte

	// Event payload bytes (JSON, Avro, Protobuf, raw binary)
	Payload []byte

	// Header map: broker-specific metadata (Content-Type, Encoding, etc.)
	// Populated by listener adapter (KafkaAdapter, SQSAdapter, etc.).
	// Empty map is safe; no nil check required.
	Headers map[string]string

	// Timestamp from the message broker (when available).
	// Set to zero time if broker does not provide timestamps.
	Timestamp time.Time
}
```

---

## Well-Known Slot Names

These slot names are **reserved and compiler-pre-allocated** for all EventListeners. The EventExecutor writes values to these slots before flow execution.

| Slot Name | Type | Size Expectation | Purpose | Written By |
|-----------|------|------------------|---------|------------|
| `__event__` | ByteSlot | Small–Large | Primary event payload (single event mode) | EventExecutor after context reset |
| `__batch__` | ByteSlot | Medium–Large | JSON array of event payloads `[{...}, {...}]` | EventExecutor (batch mode only) |
| `__event_key__` | ByteSlot | Small | Message routing key (Kafka key, etc.) | EventExecutor; empty if no key |
| `__event_topic__` | ByteSlot | Tiny | Source topic/queue name as bytes | EventExecutor from InboundMessage.Topic |
| `__event_partition__` | ByteSlot | Tiny | Kafka partition as string bytes | EventExecutor (Kafka only); empty otherwise |
| `__event_offset__` | ByteSlot | Tiny | Kafka offset as string bytes | EventExecutor (Kafka only); empty otherwise |

**Notes:**
- All slots are always allocated, even if unused by the flow.
- `__batch__` is only populated in batch mode; single event mode leaves it empty.
- Partition and offset are formatted as byte strings (`"3"`, `"42"`), suitable for logging or conditional logic.
- Empty slots are initialized to `[]byte{}` (non-nil, zero-length slices).

### Slot Index Resolution

At **compile time** (bake), the `Compiler` reserves fixed indices for these well-known slots:

```go
// In internal/control/compiler.go (pseudo-code)
const (
	SlotEventPayload    = 0  // __event__
	SlotBatch           = 1  // __batch__
	SlotEventKey        = 2  // __event_key__
	SlotEventTopic      = 3  // __event_topic__
	SlotEventPartition  = 4  // __event_partition__
	SlotEventOffset     = 5  // __event_offset__
	// User variables start at index 6
)
```

When a step references `__event__`, the compiler resolves it to slot index 0 (no lookup overhead).

---

## Header Mappings Configuration

**Location:** Flow YAML / EventListener config (`internal/eventlistener/config.go`)

Header mappings declaratively bind inbound message headers to flow variables:

```yaml
event_listeners:
  - name: order_events
    topic: orders
    flow: process_order
    
    # Header → slot mappings (optional)
    header_mappings:
      - header: X-Signature         # inbound header name
        as: event_signature          # flow variable name (slot index resolved at bake time)
      - header: X-Correlation-ID
        as: correlation_id
      - header: Content-Encoding
        as: event_encoding
      - header: X-Request-ID
        as: request_id
```

### Header Mapping Compilation

At **bake time**:

1. Compiler scans all `header_mappings` entries.
2. For each `as: <name>`, allocates a slot index.
3. Emits a **header_inject instruction** into the pre-pipeline:
   ```go
   // Pseudo-instruction
   struct {
       HeaderName  string  // "X-Signature"
       SlotIdx     int     // resolved at bake time (e.g., 10)
       Default     []byte  // empty byte slice if header missing
   }
   ```

At **request time**:
- EventExecutor looks up `InboundMessage.Headers["X-Signature"]`.
- If found, writes to ByteSlots[10].
- If missing, ByteSlots[10] remains empty or stores `Default` value.

---

## EventExecutor Injection Sequence

**Location:** `internal/eventlistener/executor.go`

This pseudocode describes the exact sequence before flow execution:

```pseudocode
func EventExecutor.Process(listener *CompiledEventListener, msg InboundMessage) error {
    
    // Step 1: Allocate and reset context
    ctx := pool.Get()
    defer pool.Put(ctx)
    ctx.Reset(noopWriter)  // NoopResponseWriter for async events
    
    // Step 2: Pre-allocate all slot indices
    // (Already done at bake time via Compiler.allocateSlots; 
    //  ByteSlots, IntSlots, BoolSlots are ready.)
    
    // Step 3: Write well-known slots
    ctx.ByteSlots[SlotEventPayload] = msg.Payload
    ctx.ByteSlots[SlotEventKey] = msg.Key
    ctx.ByteSlots[SlotEventTopic] = []byte(msg.Topic)
    
    // Convert Partition and Offset to string bytes
    if listener.Protocol == "kafka" {
        ctx.ByteSlots[SlotEventPartition] = []byte(strconv.FormatInt(int64(msg.Partition), 10))
        ctx.ByteSlots[SlotEventOffset] = []byte(strconv.FormatInt(msg.Offset, 10))
    } else {
        // Non-Kafka brokers leave these empty
        ctx.ByteSlots[SlotEventPartition] = []byte{}
        ctx.ByteSlots[SlotEventOffset] = []byte{}
    }
    
    // Step 4: Write listener constants + AppName (if set)
    for key, val := range listener.Constants {
        slotIdx := listener.SlotMap[key]  // resolved at bake time
        ctx.ByteSlots[slotIdx] = []byte(val)
    }
    if listener.AppName != "" {
        appSlotIdx := listener.SlotMap["__app__"]
        ctx.ByteSlots[appSlotIdx] = []byte(listener.AppName)
    }
    
    // Step 5: Write header mappings
    for mapping := range listener.HeaderMappings {
        headerVal, found := msg.Headers[mapping.HeaderName]
        slotIdx := mapping.SlotIdx  // pre-resolved at bake time
        if found {
            ctx.ByteSlots[slotIdx] = []byte(headerVal)
        } else {
            ctx.ByteSlots[slotIdx] = []byte{}  // empty on miss
        }
    }
    
    // Step 6: Execute pre-pipeline (if configured)
    if len(listener.PrePipelineInstructions) > 0 {
        engine.Execute(ctx, listener.PrePipelineInstructions, 0)
        
        // Check pre-pipeline result
        if ctx.ResponseStatus >= 400 {
            return listener.HandlePrePipelineFailure(ctx, msg)
        }
    }
    
    // Step 7: Execute main flow
    flowErr := engine.Execute(ctx, listener.FlowInstructions, 0)
    if flowErr != nil {
        // Handle flow error (retry, dead letter, etc.)
        listener.HandleFlowFailure(ctx, msg, flowErr)
        return flowErr
    }
    
    // Step 8: Emit telemetry (trace, logging)
    listener.EmitTelemetry(ctx, msg)
    
    return nil
}
```

### Injection Order Rationale

1. **Reset first** — ensures all slots start clean (no stale data from prior request).
2. **Well-known slots early** — most flows reference `__event__`, so these are filled immediately.
3. **Constants and AppName** — listener-level metadata available to the flow.
4. **Header mappings** — optional message metadata; missing headers don't block flow.
5. **Pre-pipeline execution** — can inspect/transform the context before main flow.
6. **Main flow execution** — all data is ready; flow never blocks on slow I/O.

---

## Pre-Pipeline Architecture

**Location:** `internal/eventlistener/prepipeline.go`

The **pre-pipeline** is an optional sequence of flow steps compiled at listener startup, executed before the main flow.

### Configuration

```yaml
event_listeners:
  - name: encrypted_orders
    topic: secure_orders
    flow: process_order
    
    pre_pipeline:
      - action: decrypt
        source_slot: __event__
        as: __event__
        algorithm: aes-256-gcm
        key_ref: payload-key
        encoding: base64
        on_failure: dead_letter
      
      - action: verify_hmac
        data_slot: __event__
        signature_slot: event_signature
        secret_ref: signing-secret
        algorithm: sha256
        on_failure: dead_letter
      
      - action: json_validate
        source_slot: __event__
        schema_ref: order-schema
        on_failure: reject
```

### Pre-Pipeline Compilation

At **bake time**:

1. Parse `pre_pipeline: []StepConfig` from the EventListener config.
2. Pass to `Compiler.CompileExecutable(flow, fragments)` → produces `[]engine.Instruction`.
3. Store compiled instructions on `listener.PrePipelineInstructions` (once, not per request).
4. **Zero startup latency:** compilation happens once at server startup.

```go
// In internal/control/compiler.go (new method)
func (c *Compiler) compilePrePipeline(
    listener *EventListenerConfig,
) ([]engine.Instruction, error) {
    // Compile the pre-pipeline steps using the same compiler
    // that compiles main flows.
    return c.CompileExecutable(listener.PrePipeline, c.FlowLibrary)
}
```

### Pre-Pipeline Execution

EventExecutor calls `engine.Execute(ctx, preInstructions, 0)`:

- Uses the **same** `rctx.Context` as the main flow (all slot data shared).
- Runs on the **same goroutine** (no concurrency; deterministic order).
- Can **read and modify** any slot (e.g., decrypt `__event__`, validate signature).
- If it **stops with error** (ctx.ResponseStatus >= 400), the main flow is skipped.

### Pre-Pipeline Failure Handling

Each pre-pipeline step can define `on_failure`:

| Mode | Behavior |
|------|----------|
| `stop` (default) | Stop pre-pipeline; skip main flow; emit error to dead letter (if configured); commit offset. |
| `dead_letter` | Stop pre-pipeline; move event to dead-letter queue; commit offset. |
| `reject` | Stop pre-pipeline; drop event silently (no dead letter); commit offset. Useful for known invalid format. |
| `reject_with_retry` | Stop pre-pipeline; **do not commit offset**; broker will retry (redelivery semantics). |

**Implementation:**

```go
// In event execution loop
if ctx.ResponseStatus >= 400 {
    switch listener.PrePipelineFailureMode {
    case "dead_letter":
        return broker.SendToDeadLetter(msg, ctx.ErrorMsg)
    case "reject_with_retry":
        return fmt.Errorf("pre-pipeline failed: %s", ctx.ErrorMsg)  // NACKs the message
    case "reject":
        return nil  // ACK silently; message is dropped
    default:  // "stop"
        if listener.DeadLetterEnabled {
            broker.SendToDeadLetter(msg, ctx.ErrorMsg)
        }
        return nil  // ACK after dead letter
    }
}
```

---

## Large Payload Handling

### Size Expectations

Rah's slot arena is designed to handle payloads from **kilobytes to tens of megabytes** with predictable latency:

| Size | Strategy | Peak Memory | GC Cost |
|------|----------|-------------|---------|
| < 4 KB | Inline arena (1024-byte pre-allocated) | ~1 KB | None |
| 4 KB – 64 KB | Single pool-borrowed 4 KB ext block + heap fallback | ~4 KB + overage | Low (fallback only) |
| 64 KB – 1 MB | Heap allocation | Payload size | Low (predictable) |
| > 1 MB | Reference pattern (S3/GCS key, not payload) | ~100 B | None |

### Inline Payload (< 4 KB)

Most events fit in the inline arena and incur **zero pool overhead**:

```go
// EventExecutor writes directly
ctx.ByteSlots[SlotEventPayload] = msg.Payload  // pointer assignment, no copy
if cap(ctx.arenaInline) - ctx.arenaUsed >= len(msg.Payload) {
    // Fits in inline arena — no heap allocation
    copy(dest, msg.Payload)
    ctx.ByteSlots[SlotEventPayload] = dest
}
```

### Pool Overflow (4 KB – 64 KB)

When inline fills, Alloc borrows a single 4 KB ext block:

```go
if ctx.arenaExt == nil {
    b := arenaPool.Get().(*arenaBlock)  // borrow once per request
    ctx.arenaExt = b
    ctx.ArenaOverflowed = true
}
// Use arenaExt.buf for additional payload data
```

### Heap Fallback (> 64 KB)

Beyond the pool ext block, `Alloc` falls back to `make()`:

```go
// Rare path, triggered only for very large payloads
return make([]byte, n)  // GC is acceptable for event processing SLAs
```

### Large Payload Best Practice: Reference Pattern

For events > 1 MB, **do not embed the payload**. Instead, send a small envelope with a storage reference:

```json
{
  "order_id": "12345",
  "payload_ref": {
    "bucket": "event-storage",
    "key": "orders/2024-08/order-12345.json.gz"
  }
}
```

Flow:

```yaml
pre_pipeline:
  - action: storage_get
    bucket_var: __bucket__
    key_var: __payload_key__
    as: __event__
    
main_flow:
  - action: json_extract
    source: __event__
    path: $.order_items
    as: items
```

### Encrypted Large Payloads

For encrypted payloads > 1 MB, use `tink_streaming_decrypt`:

```yaml
pre_pipeline:
  - action: tink_streaming_decrypt
    source_slot: __event__
    as: __event__
    key_ref: encryption-key
    chunk_size_bytes: 4096  # 4 KB chunks, peak memory = ciphertext + 4 KB
    on_failure: dead_letter
```

Peak memory = ciphertext bytes + 4 KB, **not** plaintext size. Suitable for multi-MB payloads.

---

## Batch Mode Context

**Scenario:** Multiple deduplicated events collected in a `BatchAccumulator` before processing.

### __batch__ Slot Content

Instead of processing one event, `__batch__` contains a JSON array:

```json
[
  {"order_id": "1", "customer_id": "A", "amount": 100},
  {"order_id": "2", "customer_id": "B", "amount": 200},
  {"order_id": "3", "customer_id": "C", "amount": 150}
]
```

### Flow Processing (Pseudo-Code)

```yaml
flow: process_batch
  - action: json_parse
    source_slot: __batch__
    as: batch_array
  
  - action: foreach
    source: batch_array
    do:
      - action: json_extract
        source: current_item  # context var set by foreach
        path: $.order_id
        as: oid
      
      - action: http_call
        url: "https://api.example.com/orders/{order_id}"
        url_var: order_url  # interpolated with oid
        method: POST
        body_var: current_item
        as: response_body
      
      - action: cache_set
        key: "order:{oid}"
        value_var: response_body
        ttl: 3600
```

### Deduplication Tracking

The `BatchAccumulator` (handled by listener config) deduplicates events **before** they reach EventExecutor:

```yaml
event_listeners:
  - name: order_batch
    topic: orders
    flow: process_batch
    
    batch:
      window_ms: 5000          # collect for 5 seconds
      max_size: 100            # or up to 100 events
      dedup_key: order_id      # deduplicate by this JSON path
      dedup_window: 60000      # within 60 seconds
```

Once batch is committed (window expires or size reached), `__batch__` slot is populated and main flow executes.

---

## Complete Example: Multi-Step Crypto & Validation

### Scenario: Sign-then-Encrypt Message Flow

**Problem:** Events arrive encrypted, then signed. Decrypt first (to access signature), then verify signature.

### Configuration

```yaml
event_listeners:
  - name: signed_encrypted_events
    topic: secure_orders
    flow: process_order
    
    constants:
      app_version: "2.0"
    
    header_mappings:
      - header: X-HMAC-Signature
        as: event_signature
      - header: X-Timestamp
        as: event_ts
    
    pre_pipeline:
      # Step 1: Decrypt the payload (symmetric encryption)
      - action: decrypt
        source_slot: __event__
        as: __event__
        algorithm: aes-256-gcm
        key_ref: tink://orders-key
        encoding: base64
        on_failure: dead_letter
        log_as: "decrypt_status"
      
      # Step 2: Verify HMAC over decrypted payload
      - action: verify_hmac
        data_slot: __event__
        signature_slot: event_signature
        secret_ref: env://HMAC_SECRET
        algorithm: sha256
        on_failure: dead_letter
        log_as: "hmac_status"
      
      # Step 3: Validate JSON schema
      - action: json_validate
        source_slot: __event__
        schema_ref: file:///etc/schemas/order-v2.json
        on_failure: reject  # drop silently on schema fail
      
      # Step 4: Extract correlation ID for logging
      - action: json_extract
        source: __event__
        path: $.correlation_id
        as: correlation_id
```

### Execution Trace

```
[EventExecutor.Process]
1. Reset context
2. Write __event__ = <encrypted payload bytes>
3. Write __event_signature__ (from header)
4. Write app_version constant

[Pre-Pipeline: Decrypt]
5. AES-256-GCM decrypt __event__ → __event__ (now plaintext)
6. Log decrypt_status = "OK"

[Pre-Pipeline: Verify HMAC]
7. HMAC-SHA256(__event__, secret) == __event_signature__ ✓
8. Log hmac_status = "VERIFIED"

[Pre-Pipeline: Validate Schema]
9. Parse __event__ JSON; validate against schema → OK

[Pre-Pipeline: Extract Correlation ID]
10. Extract $.correlation_id → correlation_id slot

[Main Flow: process_order]
11. All slots ready; __event__ is decrypted, verified, validated
12. Flow can safely process the JSON without re-checking
```

### Dead-Letter Handling

If any pre-pipeline step fails with `on_failure: dead_letter`:

```go
// EventExecutor.Process continues:
if ctx.ResponseStatus >= 400 {
    deadLetterMsg := &Message{
        Topic:        msg.Topic + ".dead-letter",
        Payload:      msg.Payload,  // original encrypted bytes
        Headers: map[string]string{
            "X-Original-Topic": msg.Topic,
            "X-Error":          string(ctx.ErrorMsg),  // e.g., "decrypt: invalid ciphertext"
            "X-Timestamp":      time.Now().Format(time.RFC3339),
        },
    }
    broker.Send(deadLetterMsg)
    // Commit offset (event is moved, not retried)
}
```

---

## Compilation & Bake Time

### Slot Allocation

At **bake time**, the Compiler discovers all slot names (well-known + user-defined) and allocates indices:

```go
// In internal/control/compiler.go (pseudo-code)
type CompiledEventListener struct {
    Name                        string
    Topic                       string
    FlowName                    string
    
    // Slot mapping: user/system name → index
    SlotMap                     map[string]int
    
    // Constants to inject (e.g., app_version → "2.0")
    Constants                   map[string]string
    
    // Header → slot mappings
    HeaderMappings              []HeaderMapping
    
    // Compiled pre-pipeline instructions
    PrePipelineInstructions     []engine.Instruction
    
    // Compiled main flow instructions
    FlowInstructions            []engine.Instruction
    
    // Well-known protocol (kafka, sqs, pubsub, etc.)
    Protocol                    string
    
    // App name (used to populate __app__ slot)
    AppName                     string
}

type HeaderMapping struct {
    HeaderName  string  // "X-Signature"
    SlotIdx     int     // resolved at bake time
    Default     []byte  // optional default if header missing
}
```

### Pre-Pipeline Compilation

```go
// In control/compiler.go
func (c *Compiler) compileEventListener(cfg *EventListenerConfig) (*CompiledEventListener, error) {
    compiled := &CompiledEventListener{
        Name: cfg.Name,
        Topic: cfg.Topic,
        FlowName: cfg.FlowName,
        SlotMap: make(map[string]int),
        Constants: cfg.Constants,
        Protocol: cfg.Protocol,
        AppName: cfg.AppName,
    }
    
    // Reserve well-known slots
    compiled.SlotMap["__event__"] = SlotEventPayload
    compiled.SlotMap["__batch__"] = SlotBatch
    compiled.SlotMap["__event_key__"] = SlotEventKey
    compiled.SlotMap["__event_topic__"] = SlotEventTopic
    compiled.SlotMap["__event_partition__"] = SlotEventPartition
    compiled.SlotMap["__event_offset__"] = SlotEventOffset
    nextSlot := 6
    
    // Allocate slots for user variables
    for key := range cfg.Constants {
        compiled.SlotMap[key] = nextSlot
        nextSlot++
    }
    if cfg.AppName != "" {
        compiled.SlotMap["__app__"] = nextSlot
        nextSlot++
    }
    
    // Allocate slots for header mappings
    compiled.HeaderMappings = make([]HeaderMapping, len(cfg.HeaderMappings))
    for i, hm := range cfg.HeaderMappings {
        slotIdx := nextSlot
        compiled.SlotMap[hm.As] = slotIdx
        compiled.HeaderMappings[i] = HeaderMapping{
            HeaderName: hm.Header,
            SlotIdx: slotIdx,
            Default: []byte(hm.DefaultValue),
        }
        nextSlot++
    }
    
    // Compile pre-pipeline
    if len(cfg.PrePipeline) > 0 {
        instructions, err := c.CompileExecutable(cfg.PrePipeline, c.FlowLibrary)
        if err != nil {
            return nil, fmt.Errorf("pre-pipeline compile: %w", err)
        }
        compiled.PrePipelineInstructions = instructions
    }
    
    // Compile main flow
    flowInstructions, err := c.CompileExecutable(cfg.Flow, c.FlowLibrary)
    if err != nil {
        return nil, fmt.Errorf("flow compile: %w", err)
    }
    compiled.FlowInstructions = flowInstructions
    
    return compiled, nil
}
```

---

## Request Flow Diagram

```
Broker Adapter (KafkaAdapter, SQSAdapter, etc.)
         ↓
    InboundMessage (Topic, Partition, Offset, Key, Payload, Headers, Timestamp)
         ↓
EventExecutor.Process(listener, msg)
         ↓
    [1] Reset context
    [2] Write well-known slots (__event__, __event_key__, __event_topic__, etc.)
    [3] Write listener constants + __app__
    [4] Write header mappings from InboundMessage.Headers
         ↓
    [5] Pre-Pipeline Instructions
        ├─ decrypt __event__
        ├─ verify_hmac
        ├─ json_validate
        └─ json_extract (populate more slots)
         ↓
    [6] Check pre-pipeline result
        └─ if ctx.ResponseStatus >= 400 → handle failure, skip main flow
         ↓
    [7] Main Flow Instructions
        ├─ http_call (reads slots, updates others)
        ├─ cache operations
        ├─ database operations
        └─ ... (standard flow execution)
         ↓
    [8] Telemetry (trace, logging)
         ↓
    [9] Commit offset / ACK message
```

---

## Summary Table: Slot Types & Injection Points

| Slot | Injected At | Value Source | Immutable? |
|------|-------------|--------------|-----------|
| `__event__` | Step 2 (well-known) | `InboundMessage.Payload` | No (pre-pipeline may transform) |
| `__batch__` | Step 2 (well-known) | BatchAccumulator output | No |
| `__event_key__` | Step 2 (well-known) | `InboundMessage.Key` | Yes |
| `__event_topic__` | Step 2 (well-known) | `InboundMessage.Topic` | Yes |
| `__event_partition__` | Step 2 (well-known) | `InboundMessage.Partition` (Kafka only) | Yes |
| `__event_offset__` | Step 2 (well-known) | `InboundMessage.Offset` (Kafka only) | Yes |
| User constants (e.g., `app_version`) | Step 3 | EventListener config | Yes |
| `__app__` | Step 3 | EventListener.AppName | Yes |
| Header mappings (e.g., `event_signature`) | Step 4 | `InboundMessage.Headers[key]` | No (pre-pipeline may use) |

---

## Notes on Performance & Constraints

1. **Zero allocation on small payloads** — inline arena handles < 4 KB with no pool overhead.
2. **Pre-pipeline is compiled once** — baked at server startup; execution cost is the same as main flow steps.
3. **Slot allocation is static** — resolved at bake time; no runtime lookup overhead.
4. **Context reuse** — same `rctx.Context` for pre-pipeline and main flow; no copy overhead.
5. **Large payloads** — > 1 MB should use reference pattern (S3/GCS key) rather than embedding.
6. **Batch deduplication** — happens in `BatchAccumulator` before EventExecutor, reducing redundant processing.
