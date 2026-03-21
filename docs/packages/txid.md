# TxID — Transaction ID System

## Purpose
Provides **globally-unique transaction IDs** per request without OS calls on the hot path. One `TxIDGenerator` instance lives in `FlowManager`; each request gets a `[2]uint64` ID in ~7ns.

## Files
- **internal/rctx/txid.go**: `TxIDGenerator`, `FormatTxID`, `Fingerprint`
- **internal/engine/steps/txid.go**: `StoreInternalTxID`, `BindCorrelationID` instructions

---

## TxIDGenerator Design

```go
type TxIDGenerator struct {
    fingerprint [2]uint64    // seeded from crypto/rand at startup
    counter     atomic.Uint64
}
```

### ID Layout (`[2]uint64`)
| Word | Content |
|------|---------|
| Word0 | `fingerprint[0] XOR (epochMs<<32 \| counter_low32)` |
| Word1 | `fingerprint[1]` (constant per instance) |

Where `epochMs = uint64(requestStartNs) >> 20` ≈ 1ms granularity, derived from `ctx.RequestStartNs` — no syscall.

### Properties
- **Globally unique**: `crypto/rand` fingerprint at startup makes IDs unique across gateway instances
- **Time-ordered within instance**: epochMs from `RequestStartNs` (no extra `time.Now()`)
- **Counter overflow safe**: wraps at 2³² per millisecond — impossible to exhaust at realistic RPS
- **Cost**: one `atomic.Uint64.Add(1)` per request (~7ns)

### Startup
```go
// FlowManager.NewFlowManager:
fm.TxIDGen = rctx.NewTxIDGenerator()
log.Printf("instance fingerprint: %s", fm.TxIDGen.Fingerprint())
```
The fingerprint is logged once at startup for cross-instance log correlation.

### Per-request
```go
// FlowManager.ProcessRequest:
ctx.InternalTxID = fm.TxIDGen.Generate(ctx.RequestStartNs)
```

---

## Instructions

### StoreInternalTxID
Formats `ctx.InternalTxID` and stores the hex string in `ByteSlots[slotIdx]`.

Use when you want to **forward the gateway's internal TX ID** to the upstream or include it in a response header.

```yaml
# Flow definition (store_internal_tx_id)
- action: store_internal_tx_id
  as: tx_id

- action: set_response_header
  key: "X-Rah-Request-ID"
  source: tx_id
```

### BindCorrelationID
Reads an incoming header (`key`) into `ByteSlots[slotIdx]`. If the header is absent and `generate_if_missing: true`, falls back to a newly generated transaction ID.

Use when you want to **echo back a customer-provided correlation ID** (e.g. `X-Request-ID`) or generate one if absent.

```yaml
# Flow definition (bind_correlation_id)
- action: bind_correlation_id
  key: "X-Request-ID"
  as: correlation_id
  generate_if_missing: true

- action: set_response_header
  key: "X-Request-ID"
  source: correlation_id
```

**Zero-copy**: When the header is present, `ByteSlots[slotIdx]` references the `http.Request.Header` memory directly (`unsafe.Slice`) — no arena allocation.

---

## FormatTxID
```go
func FormatTxID(id [2]uint64) string
```
Produces a 32-character lowercase hex string (`%016x%016x`). **Allocates** — use only on non-hot paths (logging, error responses, header injection).

---

## Compiler Integration
Both instructions are registered in `control/compiler.go`:

```go
case "store_internal_tx_id":
    slot, _ := c.getSlot(step.As)
    c.GlobalTable = append(c.GlobalTable, steps.StoreInternalTxID(slot))

case "bind_correlation_id":
    slot, _ := c.getSlot(step.As)
    c.GlobalTable = append(c.GlobalTable,
        steps.BindCorrelationID(step.Key, step.GenerateIfMissing, c.fm.TxIDGen, slot))
```

---

## Latency Budget
| Operation | Cost |
|-----------|------|
| `TxIDGenerator.Generate()` | ~7ns (one atomic add) |
| `rctx.FormatTxID()` | ~150ns (fmt.Sprintf, allocates) |
| `BindCorrelationID` (header present) | ~5ns (unsafe.Slice, zero-copy) |
| `StoreInternalTxID` | ~150ns (FormatTxID + Alloc + copy) |

`Generate()` runs on every request. `FormatTxID()` runs only if the flow explicitly exposes the ID.
