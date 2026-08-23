# RAH — Reconfigurable API & AI Handler

**One binary. Every role your architecture needs.**

RAH is a high-performance execution engine that adapts to the role you need it to play.
Write flows in YAML — RAH compiles them to zero-allocation bytecode and executes at
sub-5µs overhead. No plugins. No Lua scripts. No sidecars.

---

## Pick Your Role

RAH is not a fixed-purpose tool. The same binary, same DSL, and same deployment model
serve every one of these roles — often simultaneously.

### AI / Agentic Gateway
Route and orchestrate across LLM providers. Enforce cost budgets, rate limits, and
prompt injection safety. Run multi-step agent flows with tool use, MCP integration,
semantic caching, and RAG — all as compiled instructions with nanosecond-precision
observability.

```yaml
- action: enforce_cost_budget          # block if tenant quota exceeded
- action: sanitize_prompt              # strip injection patterns
- action: route_llm                    # pick model by token count + tenant tier
- action: semantic_cache_get           # skip LLM if similar prompt cached
- action: llm_call
    model: primary-claude
    fallback_chain: [claude-haiku, my-gpt4o]
- action: calculate_cost
- action: record_cost
- action: semantic_cache_put
```

### API Gateway
Proxy, auth, rate-limit, and transform upstream APIs in one compiled flow — no plugin
chain, no middleware ordering confusion.

```yaml
- action: validate_token               # JWT / DPoP / introspection
- action: rate_limit_v2               # multi-window, per-tenant
- action: registry_lookup             # resolve tenant → upstream URL
- action: http_call                   # call upstream with mTLS, retry, circuit breaker
- action: json_to_xml                 # format conversion — zero alloc
- action: set_response_body
```

### App Host
Deploy REST APIs, BFFs, OAuth2 web apps, webhook receivers, and event processors
directly from YAML bundles. No server framework. No Dockerfile. No Kubernetes.

```yaml
# app.yaml
name: school-mgmt
type: api-service
```

```bash
rah-sync publish ./school-mgmt --studio http://localhost:8092 --tag v1.0.0
# live in < 1 second. atomic hot-reload. no restarts.
```

### BFF (Backend for Frontend)
Aggregate multiple upstream services, shape the response per client, and enforce
per-tenant auth and rate limits — all in one flow, one deployment.

```yaml
- action: http_call
    url: "https://users.internal/api/users/{user_id}"
    as: profile
- action: http_call
    url: "https://settings.internal/api/{user_id}/settings"
    as: settings
- action: render_template
    template: '{"id":"{user_id}","name":"{profile.name}","theme":"{settings.theme}"}'
```

### Event Processor
Consume from Kafka, RabbitMQ, SQS, Redis Streams, or Google Pub/Sub. Transform,
persist, emit downstream events — RAH manages consumer groups, retries, and
dead-letter routing.

```yaml
name: order-processor
type: event-processor
# flows/handler.yaml runs for every inbound message
```

---

## Why RAH

Traditional stacks separate concerns across many layers: an API gateway for routing, a
server framework for business logic, an auth library for tokens, a message broker for
async work, a cron runner for scheduled jobs. Each layer has its own config, deployment,
and failure mode. When you need auth + rate limiting + LLM routing + database calls +
format conversion in a single request path, you end up with a plugin chain that nobody
can reason about.

RAH replaces that stack with a **single compiled execution plan**. Every capability —
auth, rate limiting, database, LLM, messaging, caching — is a typed instruction in the
same flat loop. One binary. One config file. One deploy step. The role (gateway, app
host, BFF, agent, event processor) is defined by which instructions you use, not by
which product you deploy.

| | RAH | API Gateway + Microservices | Low-Code Platform |
|--|-----|---|---|
| Latency overhead | < 5 µs | 0.5–5 ms (plugin chain) | 50–500 ms (interpreted) |
| Allocations on hot path | Zero | Plugin-dependent | Runtime GC |
| Expressibility | Full (240+ instructions) | Config only | Medium |
| Roles | All of the above | One per product | App only |
| Deploy | `rah-sync publish` | CI pipeline per service | UI drag-and-drop |
| Single binary | Yes | No | No |

---

## Quick Start

### Zero dependencies (disk store)

```bash
git clone https://github.com/amitkhosla/rah
cd rah
go mod download
make run-local
# Gateway:    http://localhost:8080
# Management: http://localhost:8081
```

### Full stack (Redis + PostgreSQL)

```bash
make docker-up
```

### With Studio (visual flow editor + release management)

```bash
make docker-up-studio
# Studio: http://localhost:8092
```

### Minimal config (`gateway.yaml`)

```yaml
datastore:
  stores:
    local:
      kind: disk
      connection:
        path: ./data
  bindings:
    api_definitions: local
    flows: local
    tenant_data: local
    cache: local

llm:
  models:
    - alias: my-llm
      provider: anthropic
      model_id: claude-haiku-4-5-20251001
      api_key_ref: env:ANTHROPIC_API_KEY
```

---

## App Hosting

RAH treats every deployed bundle as an **app** — a named collection of APIs and flows
with a type that determines lifecycle, routing, and scaffolding.

### App types

| Type | Description |
|------|-------------|
| `api-service` | Stateless REST API backed by upstreams or databases |
| `web` | OAuth2-protected web app with login/callback/logout flows |
| `webhook` | Inbound webhook receiver with signature verification |
| `event-processor` | Async consumer of messaging topics (Kafka, SQS, etc.) |

### Defining an app

```yaml
# app.yaml
name: school-mgmt
type: api-service
tenant_mode: tenant_aware
```

Flows live alongside the app definition in a `flows/` directory. Deploy with:

```bash
rah-sync publish ./school-mgmt --studio http://localhost:8092 --tag v1.0.0
rah-sync promote <release-id> --env prod
```

Releases are versioned and diffable:

```bash
rah-sync diff release-42 release-43
```

### Blueprint scaffolding

Studio generates starter flows for any app type via `POST /apps/{name}/blueprint`.
A `web` app scaffolds login, callback, and logout flows. A `webhook` app scaffolds
signature verification and processing flows. Start from a working template, not a
blank file.

### Atomic hot reload

New flows are compiled and applied via an atomic state swap. In-flight requests
complete on the old plan; all new requests immediately see the new one — no restarts,
no dropped connections.

---

## Document Connectors

Connect flows directly to document and relational databases — separate from RAH's
internal stores.

| Connector | Kind |
|-----------|------|
| MongoDB | `mongodb` |
| PostgreSQL | `postgresql` |
| MySQL | `mysql` |
| gRPC document service | `grpc` |

```yaml
document_connectors:
  - name: orders_db
    kind: mongodb
    uri_ref: env:MONGODB_URI
    database: orders
    max_pool_size: 20
```

**Flow steps:** `doc_find`, `doc_find_one`, `doc_insert`, `doc_update`, `doc_delete`,
`doc_count`, `doc_aggregate` — with tenant isolation and parameterized query safety
built in.

---

## Messaging Connectors

Publish to and consume from message brokers without any broker-specific client code.

| Broker | Kind |
|--------|------|
| Apache Kafka | `kafka` |
| Google Pub/Sub | `pubsub` |
| RabbitMQ / AMQP | `rabbitmq` |
| Amazon SQS | `sqs` |
| Redis Streams | `redis_streams` |

### Publishing

```yaml
messaging_publishers:
  - name: order-events
    kind: kafka
    brokers: ["kafka.internal:9092"]
    topic: orders
    auth_ref: env:KAFKA_SASL_PASSWORD
```

```yaml
- action: msg_publish
  publisher: order-events
  slot: event_payload
```

### Consuming

Define a consumer app (`type: event-processor`) and bind it to a topic. RAH manages
consumer group membership, offset commits, dead-letter routing, and retry logic.

```yaml
# app.yaml
name: order-processor
type: event-processor

# flows/handler.yaml
name: handler
instructions:
  - action: bind_body
    path: order_id
    as: oid
  - action: db_exec
    key: orders_db
    value: "UPDATE orders SET status='processed' WHERE id = $1"
    vars: ["{oid}"]
  - action: return
    status: 200
```

---

## SFTP Connector

Transfer files to and from SFTP servers directly from flows — no client code, no shell
commands.

```yaml
sftp_connectors:
  - name: reports-sftp
    host: sftp.example.com
    port: 22
    username: deployer
    private_key_ref: env:SFTP_PRIVATE_KEY
    max_connections: 5
```

**Flow steps:** `sftp_get`, `sftp_put`, `sftp_list`, `sftp_delete` — with per-tenant
credential isolation and connection pooling managed by RAH.

---

## Object Storage

Serve and manage app static assets from any object storage backend.

| Backend | Provider |
|---------|----------|
| Amazon S3 | `s3` |
| Google Cloud Storage | `gcs` |
| Local filesystem | `local` |

Configure per-app in Studio or via `assets_store` in the Studio config. Switch backends
without changing app code.

---

## Customer Data Sources

Connect flows to your own Postgres and Redis instances with tenant isolation built in.

### Postgres

```yaml
data_sources:
  - name: orders_db
    driver: postgres
    dsn_ref: env:ORDERS_DATABASE_URL
    max_connections: 20
    tenant_isolation: schema   # "schema" | "rls" | "" (none)
```

| Step | Description |
|------|-------------|
| `db_query` | SELECT — returns JSON array |
| `db_query_one` | SELECT one row — 404 if empty |
| `db_exec` | INSERT / UPDATE / DELETE / DDL |

**Named queries with automatic batching** — concurrent requests within a configurable
window (default 500µs) collapse into a single `WHERE id = ANY($1)` query:

```yaml
queries:
  get_orders_by_ids:
    sql: "SELECT id, amount FROM orders WHERE id = ANY($1::bigint[])"
    batch_by: "$1"
    batch_window: 500us
    batch_max: 100
```

### Redis

```yaml
redis_sources:
  - name: sessions
    addr: "redis.example.com:6379"
    password_ref: env:REDIS_PASSWORD
    tenant_prefix: alias
```

45+ operations: strings, hashes, lists, sets, sorted sets, distributed locks, pub/sub —
all automatically namespaced per tenant.

---

## Core Capabilities

| Domain | What RAH Does |
|--------|--------------|
| **App Hosting** | Deploy REST APIs, BFFs, auth flows, webhooks, event processors from YAML |
| **API Execution** | Compile flows to zero-allocation instruction plans; sub-5µs gateway overhead |
| **Document DBs** | Connect to MongoDB, PostgreSQL, MySQL, gRPC document services |
| **Messaging** | Publish/consume Kafka, Pub/Sub, RabbitMQ, SQS, Redis Streams |
| **SFTP** | Read, write, list, delete files on SFTP servers from flow steps |
| **Object Storage** | Serve app assets from S3, GCS, or local filesystem — switchable per app |
| **LLM Orchestration** | Route, call, classify, embed, cache, and cost-control across all major providers |
| **Protocol Translation** | HTTP ↔ gRPC ↔ GraphQL ↔ SOAP ↔ MQTT ↔ MCP in a single flow |
| **Format Conversion** | JSON ↔ XML ↔ Avro ↔ Protobuf — compiled programs, zero runtime allocation |
| **Multi-Tenant** | Lock-free tenant registry, per-tenant rate limits, cost quotas, credentials |
| **Security & Auth** | JWT/JWKS, DPoP (RFC 9449), token introspection, API keys with rotation |
| **Rate Limiting** | Multi-window, token bucket, spike arrest, circuit breaker, per-tenant scale |
| **Caching** | Zero-GC slab (L1) + Redis/PostgreSQL (L2), semantic cache via vector embeddings |
| **Vector & RAG** | Upsert, search, semantic cache — Qdrant, Chroma, Weaviate, Redis, PgVector |
| **Secrets** | Google Secret Manager, AWS Secrets Manager, HashiCorp Vault, AES-256-GCM |
| **Observability** | Per-instruction timing, traces, access logs, metrics — Prometheus / OTEL |
| **Scheduled Flows** | Cron via hashed timing wheel; distributed claiming across instances |
| **WebSocket** | Manage client sessions and upstream WS pools; push or broadcast |
| **MCP** | Serve and call Model Context Protocol servers; expose RAH APIs as MCP tools |

---

## How It Works

RAH compiles your YAML flow definitions into a flat `[]Instruction` table at deploy
time. At request time, the engine acquires a pooled context (zero allocation), walks a
radix-tree router (~10–20 ns), and runs the instruction loop. Every data value lives in
typed slots (`ByteSlots`, `IntSlots`, `BoolSlots`) backed by a per-request arena
allocator. Nothing escapes to the heap on the hot path.

```
HTTP Request
     │
     ▼
 RahRouter          ← lock-free atomic snapshot, radix tree (~10–20 ns)
     │
     ▼
 rctx.Context       ← pool-allocated, 1 KB inline arena, typed slots
     │
     ▼
 engine.Execute()   ← flat instruction loop, per-instruction timing
     │
   ┌─┴──────────────────────────────────────────────────┐
   │  bind_header → validate_token → rate_limit_v2      │
   │  → registry_lookup → cache_get → llm_call          │
   │  → calculate_cost → record_cost → set_response_body│
   └────────────────────────────────────────────────────┘
     │
     ▼
 Flush Op Batch     ← cache writes, registry writes (async, fire-and-forget)
     │
     ▼
 HTTP Response
```

New flows are compiled and applied via an atomic state swap — in-flight requests
complete on the old plan; all new requests immediately see the new one.

---

## The Instruction Set

240+ built-in instructions across 12 categories. Every capability is an instruction —
there is no distinction between "built-in" and "plugin" behaviour.

### HTTP & Upstream
| Instruction | Purpose |
|-------------|---------|
| `http_call` | Upstream HTTP with retry, backoff, mTLS, streaming, per-shard connection pool |
| `grpc_call` | gRPC with JSON↔Protobuf transcoding, descriptor registry, connection pooling |
| `graphql_call` | GraphQL query/mutation with variable binding |
| `soap_call` | SOAP 1.1 / 1.2 with envelope wrapping and fault extraction |
| `mqtt_publish` | Publish to MQTT broker (QoS 0/1) |
| `mqtt_call` | Request-reply over MQTT with timeout |

### Database & Messaging
| Instruction | Purpose |
|-------------|---------|
| `db_query` | SQL SELECT on customer Postgres; returns JSON array |
| `db_query_one` | SQL SELECT one row; 404 if empty |
| `db_exec` | SQL INSERT / UPDATE / DELETE / DDL |
| `doc_find` | MongoDB / document connector query; returns JSON array |
| `doc_find_one` | Document connector single-document lookup |
| `doc_insert` / `doc_update` / `doc_delete` | Document write operations |
| `doc_aggregate` | Aggregation pipeline (MongoDB) |
| `msg_publish` | Publish to Kafka, Pub/Sub, RabbitMQ, SQS, or Redis Streams |
| `sftp_get` | Download a file from an SFTP server into a slot |
| `sftp_put` | Upload slot contents to an SFTP server |
| `sftp_list` | List files in a remote SFTP directory |
| `sftp_delete` | Delete a file on an SFTP server |

### LLM & AI
| Instruction | Purpose |
|-------------|---------|
| `llm_call` | Call Anthropic / OpenAI / Google / Bedrock / custom with retry and streaming |
| `route_llm` | Rule-based model selection (token count, tenant tier, complexity) |
| `classify_llm` | Classify request via LLM; result drives branching |
| `embed_text` | Generate embeddings for RAG or semantic cache |
| `estimate_tokens` | Count tokens before sending; reject if over context window |
| `check_context_fit` | Truncate or overflow message history to fit model context |
| `transform_messages` | Reshape message array for target model format |
| `parse_tool_calls` | Extract structured tool calls from LLM response |
| `call_mcp_tool` | Execute an MCP tool and inject result |
| `semantic_cache_get` | Vector-similarity cache lookup before calling LLM |
| `semantic_cache_put` | Store LLM response indexed by embedding |
| `load_history` / `save_history` | Multi-turn conversation state management |
| `calculate_cost` / `record_cost` | Token cost calculation and quota deduction |
| `enforce_cost_budget` | Block request if tenant cost quota is exceeded |
| `sanitize_prompt` | Strip injection patterns before sending to LLM |
| `chunk_text` | Split document for RAG ingestion |

### Authentication & Security
| Instruction | Purpose |
|-------------|---------|
| `validate_token` | JWT validation — RS256/ES256, JWKS fetch+cache, leeway, scopes |
| `validate_api_key` | SHA-256 hash lookup, scope check, tenant restriction |
| `validate_dpop` | DPoP proof (RFC 9449) — htm/htu binding, ath verification |
| `validate_token_introspection` | RFC 7662 token introspection with result cache |
| `cors` | Preflight handling, origin reflection, Vary header |
| `set_security_headers` | HSTS, X-Frame-Options, CSP, Referrer-Policy |
| `ip_restriction` | Allow/block by CIDR or geolocation |
| `geo_block` | Country-level access control (MaxMind GeoLite2) |
| `detect_bot` | User-Agent pattern matching; block or tag mode |
| `owasp_check` | Request validation against OWASP patterns |

### Rate Limiting & Traffic Control
| Instruction | Purpose |
|-------------|---------|
| `rate_limit_v2` | Multi-window fixed or token bucket; per-IP, key, tenant, or global |
| `spike_arrest` | Smoothed rate limiting (at most one request per interval per key) |
| `circuit_breaker` | Three-state breaker (closed → open → half-open) with fallback flow |
| `check_upstream_rate_limit` | Honour upstream 429 / Retry-After headers |
| `assign_quota_group` | Map tenant tier to quota group for per-tier limits |

### Caching
| Instruction | Purpose |
|-------------|---------|
| `cache_get` / `cache_put` | Tenant-scoped L1+L2 cache with TTL |
| `cache_get_global` / `cache_put_global` | Cross-tenant shared cache entries |
| `cache_exists` / `cache_delete` | Existence check and invalidation |
| `cache_incr` | Atomic integer increment (counters, sequence numbers) |
| `cache_touch` | Extend TTL without a write |

### Request & Response Binding
| Instruction | Purpose |
|-------------|---------|
| `bind_header` / `bind_query` / `bind_path` | Zero-copy extraction into ByteSlots |
| `bind_body` | gjson path extraction from request body |
| `bind_json` | gjson extraction from any ByteSlot |
| `set_request_header` / `remove_request_header` | Mutate upstream request headers |
| `set_response_header` / `remove_response_header` | Mutate response headers |
| `set_request_body` / `set_response_body` | Stage body for next upstream call |
| `set_response_status` | Set HTTP response code |
| `render_template` | Assemble response from literals and slot values (single allocation) |

### Data Transformation
| Instruction | Purpose |
|-------------|---------|
| `xml_to_json` / `json_to_xml` | Bidirectional XML↔JSON (compiled scan/build programs) |
| `proto_to_json` / `json_to_proto` | Protobuf↔JSON via dynamic descriptor (no codegen) |
| `avro_to_json` / `json_to_avro` | Avro binary↔JSON (zero-GC compiled programs) |
| `aes_encrypt` / `aes_decrypt` | AES-256-GCM with random nonce |
| `hmac_sha256` / `hmac_sha1` | HMAC with pooled hash objects |
| `sha256_hash` / `md5_hash` | Digest computation |
| `base64_encode` / `base64_decode` | Std, URL, raw encodings |
| `url_encode` / `url_decode` | RFC 3986 percent-encoding |
| `json_set` / `json_extract_emit` | gjson-path JSON field manipulation |

### String, Arithmetic & Flow
`concat`, `substring`, `trim`, `to_upper`, `to_lower`, `replace`, `split`, `contains`,
`starts_with`, `ends_with`, `index_of`, `byte_length`, `to_int`, `add`, `sub`, `mul`,
`div`, `sleep`, `current_timestamp` (unix_s / unix_ms / rfc3339)

### Control Flow
`if`, `switch`, `foreach`, `while`, `parallel`, `return`, `fail`, `early_return`,
`capture_error`, `call` (fragment call/return with link stack), `execute_plan`
(run LLM-generated instruction sequence)

### Registry (Tenant Metadata)
| Instruction | Purpose |
|-------------|---------|
| `registry_lookup` | Resolve tenant from alias (~40–70 ns) |
| `load_service_url` / `set_service_url` | Per-tenant backend URL (O(1) matrix read) |
| `load_identifier` / `set_identifier` | Per-tenant identifier storage |
| `load_meta` / `set_meta` | Per-tenant metadata key-value |

### Vector Store
| Instruction | Purpose |
|-------------|---------|
| `vector_search` | KNN similarity search with optional metadata filter |
| `vector_upsert` | Insert or update vectors (content-addressed ID) |

### Observability & Events
| Instruction | Purpose |
|-------------|---------|
| `log_field` | Add slot value to access log record |
| `emit_event` | Emit billing/usage event to ingest pipeline |
| `flow_log` | Structured flow log (debug/info/warn/error) |
| `trace_capture` | Attach request data to sampled trace |
| `send_sse_event` | Server-sent event to streaming client |

---

## LLM & AI Orchestration

RAH is a full execution layer for AI pipelines, not a passthrough proxy.

**Supported providers:**
- Anthropic (all Claude models, Prompt Caching cost tracking)
- OpenAI (GPT-4o, o1-series with `max_completion_tokens`)
- Google (Gemini 2.0 / 1.5, v1 and v1beta)
- Amazon Bedrock (Claude on Bedrock via SigV4 signing)
- Ollama (local models, no auth)
- DeepSeek
- Any OpenAI-compatible endpoint via `adapter: custom` (HuggingFace TGI, vLLM, LM Studio, Groq, Together AI, Fireworks)

**Model routing example** — route by token count and tenant tier:

```yaml
flows:
  - name: smart_llm_router
    instructions:
      - action: bind_body
        path: messages
        as: messages_slot
      - action: estimate_tokens
        input: messages_slot
        as: token_count
      - action: if
        condition: token_count > 8000
        then:
          - action: llm_call
            model: my-claude-opus
            messages: messages_slot
        else:
          - action: llm_call
            model: my-claude-haiku
            messages: messages_slot
      - action: calculate_cost
        as: cost_usd
      - action: record_cost
        cost: cost_usd
      - action: set_response_body
        slot: llm_response
```

**Cost control** — enforce per-tenant spending windows:

```yaml
- action: enforce_cost_budget
- action: llm_call
    model: my-llm
- action: calculate_cost
- action: record_cost
```

**Resilient routing** — automatic fallback chain on 429s or provider errors:

```yaml
llm:
  models:
    - alias: primary-claude
      adapter: anthropic
      model_id: claude-opus-4-5
      api_key_ref: env:ANTHROPIC_API_KEY
      fallback_chain:
        - model: claude-haiku
        - model: my-gpt4o
```

---

## Multi-Tenant Platform

Tenancy is the primary axis of the entire data model, not an afterthought.

- **Alias resolution** — resolve tenant from header, query param, path segment, or JWT claim in ~40–70 ns via lock-free open-addressing hash table
- **Property matrix** — per-tenant service URLs, identifiers, and metadata accessed in ~2–5 ns via flat `[TenantID × KeyID]` array lookup
- **Per-tenant rate limits** — scale by percentage or set absolute overrides
- **Per-tenant cost quotas** — daily, monthly, and flexible rolling windows (1h, 7d, 30d)
- **Per-tenant credentials** — override global secrets at the tenant level
- **Per-tenant observability** — per-tenant trace sample rate and log level controls
- **Per-tenant API keys** — scoped to tenant and scope list; rotation preserves `AppID` so rate limits survive key rotation
- **Test isolation** — tenant ID range `0xF000–0xFFFF` reserved for ephemeral test tenants; never persisted

---

## Security & Authentication

### JWT / JWKS
- RS256, ES256 signature validation
- JWKS endpoint fetch with 12-hour cache and retry backoff
- Configurable issuer, audience, scopes, and clock-skew leeway
- Optional JTI replay prevention via datastore

### DPoP (RFC 9449)
- Embedded JWK parsing (RSA / EC / EdDSA)
- `htm` + `htu` binding, `ath` verification, JTI replay check

### Token Introspection (RFC 7662)
- Configurable introspection endpoint with result caching

### API Keys
- Format: `rah_<base64url(32 random bytes)>` (~47 chars)
- SHA-256 hash stored; raw key shown once, never again
- Scope-based authorization, per-tenant restriction, expiry support

---

## Rate Limiting

```yaml
rate_limit_configs_v2:
  - name: ai-tier-limits
    enforcement: approximate
    windows:
      - period: 1s
        limit: 10
        burst_factor: 2
      - period: 1m
        limit: 200
      - period: 1h
        limit: 5000
    count_by: tenant
    divide_by_nodes: true
    exceeded_status: 429
    emit_headers: true
```

Additional controls: **spike arrest**, **circuit breaker** (three-state), **per-tenant scale**, **token bucket**.

---

## Caching

**L1 — Zero-GC slab cache** built into the gateway process:
- Fixed memory budget, per-tenant quota
- Circular-buffer regions per `(sizeClass, TTLTier)` — no GC pressure
- Swiss-style H2 filter for fast miss detection

**L2 — Persistent backend** (optional): Redis / Dragonfly, disk

**Semantic cache** — store and retrieve LLM responses by embedding similarity. Skips
the LLM call entirely on cache hit.

---

## Observability

**Per-request:** instruction-level nanosecond timing, upstream call breakdown (DNS +
connect + TLS + TTFB + body), sampled traces, access log.

**Aggregated:** per-API latency percentiles, per-tenant error distribution, cache hit
rates, custom metrics with dimensions.

**Export:** Prometheus scrape endpoint, OpenTelemetry (OTLP/gRPC) traces and metrics,
webhook push.

**Observability API** (management port `:8081`):
- `GET /observability/metrics`
- `GET /observability/access-log?api=&tenant=&status=&from=&limit=`
- `GET /observability/traces?api=&tenant_id=&min_ms=&from=&limit=`

---

## Scheduled Flows (Cron)

```yaml
schedules:
  - name: daily-report
    cron: "0 6 * * *"
    flow: generate_report
    tenant_alias: acme
    timeout_sec: 120
    on_failure:
      retry_count: 3
      dead_letter_flow: handle_report_failure
    max_concurrent: 1
```

Hashed timing wheel (3600 slots, 1-second resolution). Distributed claiming — only one
instance executes each event in a multi-gateway deployment.

---

## WebSocket

**Client sessions** — push to individual sessions or broadcast to channels:

```yaml
- action: ws_broadcast_channel
  channel: alerts
  slot: message_slot
```

**Upstream pools** — persistent outbound connections with auto-reconnect and TTL:

```yaml
- action: ws_upstream_connect
  name: data-feed
  url: wss://feeds.example.com/stream
  ttl_sec: 300
```

---

## Model Context Protocol (MCP)

**As a client** — call tools on any MCP server from a flow.

**As a server** — expose RAH APIs as MCP tools. Studio exposes 17 management tools
over MCP. Transports: HTTP, SSE, stdio.

---

## The Three Binaries

### `rah-gateway`
Execution runtime. Data plane `:8080`, management plane `:8081`.

### `rah-studio`
Web UI for flow authoring, app management, release pipelines, connector configuration,
and observability. Includes live API testing — send requests to any deployed app API
directly from the Studio with inline response display.

### `rah-sync`
CI/CD CLI for bundle management.

```bash
rah-sync lint    <dir>
rah-sync publish <dir> --studio <url> --tag v1
rah-sync promote <release-id> --env prod
rah-sync diff    <release-a> <release-b>
```

---

## Performance

All numbers are gateway overhead only, excluding upstream latency.

| Metric | Value |
|--------|-------|
| Request processing overhead | < 5 µs |
| Tenant alias lookup | ~40–70 ns |
| Tenant property read | ~2–5 ns |
| Router lookup | ~10–20 ns |
| Rate limit check (local) | ~20–50 ns |
| Cache L1 hit | ~100–300 ns |
| Arena allocation (inline) | ~5 ns |
| Context pool acquire | ~10–15 ns |
| Concurrency gate | ~10–20 ns |

---

## Build

```bash
make build          # gateway + studio + sync for current platform
make build-all      # cross-compile linux/darwin/windows × amd64/arm64
make test
make test-race
make bench
make lint
```

---

## License

See [LICENSE](LICENSE).
