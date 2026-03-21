# API Creation Guide

This guide explains how to define and register APIs in Rah using the `/sync` control-plane endpoint.

---

## How It Works

Rah uses a **flow-based execution model**. You define reusable *flows* (sequences of steps), then attach them to *API routes*. Both are registered in a single `POST /sync` call, which atomically hot-swaps the engine state with zero downtime.

```
POST /sync  →  compile flows  →  register routes  →  atomic swap
```

---

## Sync Request Schema

```json
{
  "sync_uuid": "<unique-id>",
  "flows": [ <FlowUpdate>, ... ],
  "apis":  [ <ApiUpdate>,  ... ]
}
```

### FlowUpdate

```json
{
  "name":         "myFlow",
  "action":       "upsert",
  "instructions": [ <StepConfig>, ... ]
}
```

- `action`: `"upsert"` (create or replace) | `"delete"`
- `instructions`: ordered list of steps executed per request

### ApiUpdate

```json
{
  "name":      "my-api",
  "path":      "/v1/example",
  "flow_name": "myFlow",
  "action":    "upsert"
}
```

- `path`: base path, supports `{param}` segments (e.g. `/v1/users/{id}`)
- `flow_name`: must match a flow registered in the same or prior sync

---

## StepConfig Fields

Every step shares this shape (unused fields are omitted):

| Field            | Type              | Purpose                                                    |
|------------------|-------------------|------------------------------------------------------------|
| `action`         | string (required) | Which operation to run (see table below)                   |
| `key_identifier` | string            | Primary input — `header.X-Foo`, `query.q`, `path.id`, or slot name |
| `source`         | string            | Secondary input — same reference formats as `key_identifier` |
| `as`             | string            | Slot name to store result into                             |
| `value`          | string            | Literal string (e.g. separator for `concat`)               |
| `flow_name`      | string            | Target flow for `call`, `if`, `switch`                     |
| `input`          | map[string]string | Action-specific config (see per-action docs)               |

### Input Reference Syntax

Anywhere `key_identifier` or `source` accept a value, use:

```
header.<Name>   → request header, e.g. header.X-User-ID
query.<name>    → URL query param, e.g. query.tenant
path.<name>     → path segment, e.g. path.id  (from /v1/users/{id})
<slotName>      → previously stored slot
```

---

## Available Actions

| Action                | What it does                                                       |
|-----------------------|--------------------------------------------------------------------|
| `concat`              | Join two values with a separator → store in slot                   |
| `set_response_header` | Write a slot value as a response header                            |
| `set_response_body`   | Write a slot value as the response body                            |
| `set_response_status` | Set HTTP response status code                                      |
| `echo_request`        | Return all request headers + query params as JSON body             |
| `http_call`           | Proxy to upstream URL                                              |
| `registry_lookup`     | Tenant/config lookup by key                                        |
| `token_validation`    | JWT validation (see `TOKEN_VALIDATION.md`)                         |
| `to_lower`            | Lowercase a slot value                                             |
| `to_upper`            | Uppercase a slot value                                             |
| `substring`           | Extract substring from a slot                                      |
| `to_int`              | Parse slot string as integer                                       |
| `add` / `sub` / `mul` / `div` | Integer arithmetic on int slots                       |
| `call`                | Jump into another named flow                                       |
| `if`                  | Conditional branch to flow                                         |
| `switch`              | Multi-value branch                                                 |
| `foreach`             | Iterate a collection slot                                          |

---

## Example 1 — Header Concatenator

**Goal**: Read `X-User-ID` and `X-Tenant-ID` from the request, concatenate them with `::`, and return the result as the `X-Context` response header.

```json
{
  "sync_uuid": "sync-header-concat-001",
  "flows": [
    {
      "name": "headerConcatFlow",
      "action": "upsert",
      "instructions": [
        {
          "action": "concat",
          "key_identifier": "header.X-User-ID",
          "source": "header.X-Tenant-ID",
          "as": "user_context",
          "value": "::"
        },
        {
          "action": "set_response_header",
          "key": "X-Context",
          "source": "user_context"
        },
        {
          "action": "set_response_status",
          "value": "200"
        }
      ]
    }
  ],
  "apis": [
    {
      "name": "header-concat-api",
      "path": "/v1/context",
      "flow_name": "headerConcatFlow",
      "action": "upsert"
    }
  ]
}
```

**What happens on a request to `GET /v1/context`:**

1. `concat`: reads `X-User-ID` + `X-Tenant-ID`, joins with `::`, stores in slot `user_context`
2. `set_response_header`: copies `user_context` into response header `X-Context`
3. `set_response_status`: sends `200`

```
Request:   X-User-ID: alice   X-Tenant-ID: acme
Response:  X-Context: alice::acme   (status 200, empty body)
```

---

## Example 2 — Header Echo with Injected Header

**Goal**: Add a computed `X-Request-Summary` header (concatenation of method path and a query param), then echo all request headers and query params as a JSON body.

```json
{
  "sync_uuid": "sync-echo-001",
  "flows": [
    {
      "name": "echoFlow",
      "action": "upsert",
      "instructions": [
        {
          "action": "concat",
          "key_identifier": "header.X-Forwarded-For",
          "source": "query.trace_id",
          "as": "request_summary",
          "value": "|"
        },
        {
          "action": "set_response_header",
          "key": "X-Request-Summary",
          "source": "request_summary"
        },
        {
          "action": "echo_request"
        }
      ]
    }
  ],
  "apis": [
    {
      "name": "echo-api",
      "path": "/v1/echo",
      "flow_name": "echoFlow",
      "action": "upsert"
    }
  ]
}
```

**What happens on a request to `GET /v1/echo?trace_id=abc123`:**

1. `concat`: joins `X-Forwarded-For` header + `trace_id` query param, stores as `request_summary`
2. `set_response_header`: writes `X-Request-Summary` with that value
3. `echo_request`: returns all incoming request headers and query params as a JSON body

```
Request:
  X-Forwarded-For: 10.0.0.1
  X-Custom: hello
  ?trace_id=abc123

Response headers:
  X-Request-Summary: 10.0.0.1|abc123

Response body (JSON):
  {
    "headers": { "X-Forwarded-For": "10.0.0.1", "X-Custom": "hello", ... },
    "query":   { "trace_id": "abc123" }
  }
```

---

## Deleting an API or Flow

Set `"action": "delete"` and provide only `name`:

```json
{
  "sync_uuid": "sync-delete-001",
  "apis":  [{ "name": "echo-api",    "action": "delete" }],
  "flows": [{ "name": "echoFlow",    "action": "delete" }]
}
```

Delete the API before the flow — the flow may still be referenced by other APIs.

---

## Chaining Flows

Flows can call other flows with `call`:

```json
{
  "name": "authThenEcho",
  "action": "upsert",
  "instructions": [
    { "action": "call", "flow_name": "authFlow" },
    { "action": "echo_request" }
  ]
}
```

---

## See Also

- `internal/control/UNIFIED_SYNC_FLOW.md` — sequence diagram of the sync pipeline
- `internal/control/TOKEN_VALIDATION.md` — JWT validation step reference
- `internal/control/CACHE_API.md` — caching step reference
- `docs/packages/control.md` — compiler internals
- `docs/packages/engine.md` — execution engine internals
