# RAH — Reconfigurable API & AI Handler

**A high-performance API execution platform for Go.**

Write flows in YAML. RAH compiles them to zero-allocation bytecode and executes at
sub-5µs gateway overhead. API proxying, JWT auth, LLM orchestration, gRPC transcoding,
multi-tenant rate limiting, RAG pipelines, and AI agent workflows are all first-class
instructions in one compiled execution plan. Deploy one binary. No plugins. No Lua
scripts. No sidecars.

---

## Why RAH

API gateways route requests. RAH executes them.

Traditional gateways are configuration files with limited logic. When you need auth +
rate limiting + LLM routing + response transformation + cost tracking in a single
request path, you end up with a chain of plugins, sidecars, and custom middleware that
nobody can reason about. RAH replaces that stack with a single compiled flow — a
sequence of typed instructions that run in a tight loop with per-instruction timing,
zero allocations on the hot path, and an atomic state swap that makes new flows live
without restarts or in-flight disruption.

---

## Core Capabilities

| Domain | What RAH Does |
|--------|--------------|
| **API Execution** | Compile YAML flows to zero-allocation instruction plans; sub-5µs gateway overhead |
| **LLM Orchestration** | Route, call, classify, embed, cache, and cost-control across Anthropic, OpenAI, Google, Bedrock |
| **Protocol Translation** | HTTP ↔ gRPC ↔ GraphQL ↔ SOAP ↔ MQTT ↔ MCP in a single flow |
| **Format Conversion** | JSON ↔ XML ↔ Avro ↔ Protobuf — compiled programs, zero runtime allocation |
| **Multi-Tenant** | Lock-free tenant registry, per-tenant rate limits, cost quotas, credentials, and observability |
| **Security & Auth** | JWT/JWKS, DPoP (RFC 9449), token introspection, API keys with rotation |
| **Rate Limiting** | Multi-window (per-sec, per-min, per-hour, per-day), token bucket, per-tenant scale |
| **Caching** | Zero-GC slab cache (L1) + Redis/PostgreSQL/disk (L2), semantic cache via vector embeddings |
| **Vector & RAG** | Upsert, search, and semantic cache across Qdrant, Chroma, Weaviate, Redis, PgVector |
| **Secrets** | Google Secret Manager, AWS Secrets Manager, HashiCorp Vault, AES-256-GCM at-rest |
| **Observability** | Per-instruction timing, traces, access logs, metrics — Prometheus / OTEL export |
| **Scheduled Flows** | Cron-triggered flows via hashed timing wheel; distributed claiming across instances |
| **WebSocket** | Manage client sessions and upstream WS pools; push to session or broadcast to channel |
| **Customer Data** | Connect flows to customer-owned Postgres (schema/RLS isolation) and Redis (tenant namespacing) |
| **MCP** | Serve and call Model Context Protocol servers from flows; expose RAH APIs as MCP tools |

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

### String & Arithmetic
`concat`, `substring`, `trim`, `to_upper`, `to_lower`, `replace`, `split`, `contains`,
`starts_with`, `ends_with`, `index_of`, `byte_length`, `to_int`, `add`, `sub`, `mul`,
`div`, `current_timestamp` (unix_s / unix_ms / rfc3339)

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

### Control Flow
`if`, `switch`, `foreach`, `while`, `parallel`, `return`, `fail`, `early_return`,
`capture_error`, `call` (fragment call/return with link stack), `execute_plan`
(run LLM-generated instruction sequence)

### Secrets & Credentials
| Instruction | Purpose |
|-------------|---------|
| `load_secret` | Resolve credential reference into ByteSlot |
| `load_credential` | Resolve named credential with per-tenant override |

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
- action: enforce_cost_budget   # blocks if tenant quota exceeded
- action: llm_call
    model: my-llm
- action: calculate_cost
- action: record_cost           # deducts from rolling window
```

Pricing is sourced from explicit config → per-model config → LiteLLM community catalog
(auto-refreshed hourly) → hardcoded defaults. A daily learning job tracks estimated vs.
actual token counts and adjusts future estimates automatically.

**Resilient routing & automatic fallback** — configure an ordered fallback chain per
model; the gateway promotes to the next provider automatically on 429 rate limits,
provider errors, or timeouts:

```yaml
llm:
  models:
    - alias: primary-claude
      adapter: anthropic
      model_id: claude-opus-4-5
      api_key_ref: env:ANTHROPIC_API_KEY
      fallback_chain:
        - model: claude-haiku         # promoted to on 429 or any failure
        - model: my-gpt4o             # cross-provider final fallback
```

`circuit_breaker` bypasses a degraded model for a configurable recovery window;
`check_upstream_rate_limit` honours provider `Retry-After` headers automatically.
No code changes required — routing adjusts at runtime based on live provider health.

**Intelligent model selection** — `route_llm` evaluates each request at runtime against
your rules (token count, tenant tier, cost budget, content classification) and selects
the best registered model. Define the selection logic once in YAML; every flow that uses
`route_llm` benefits automatically without explicit branching per flow.

---

## Protocol & Format Translation

RAH handles the full protocol matrix in a single flow — no separate sidecar or adapter
service required.

**Protocols:**

| Inbound | Outbound |
|---------|---------|
| HTTP/1.1, HTTP/2, h2c | HTTP/1.1, HTTP/2, h2c |
| REST (any method) | gRPC (JSON ↔ Protobuf via descriptor registry) |
| SSE (streaming) | SOAP 1.1 / 1.2 |
| MCP (JSON-RPC 2.0) | MQTT publish / request-reply |
| GraphQL | GraphQL |

**Formats:** JSON ↔ XML ↔ Avro ↔ Protobuf. All conversions use compiled programs
(baked at deploy time) with zero runtime allocation. A pivot buffer pool allows
multi-hop chains (e.g., XML → JSON → Avro) in a single request path.

**gRPC:** Upload a `FileDescriptorSet` once; RAH transcodes JSON bodies to protobuf
messages, invokes the method, and transcodes the response back — no `.proto` files or
codegen needed at runtime. Dynamic message reflection, connection pooling with
keepalive, and mTLS are all supported.

---

## Multi-Tenant Platform

Tenancy is the primary axis of the entire data model, not an afterthought.

- **Alias resolution** — resolve tenant from header, query param, path segment, or JWT
  claim in ~40–70 ns via lock-free open-addressing hash table
- **Property matrix** — per-tenant service URLs, identifiers, and metadata accessed in
  ~2–5 ns via flat `[TenantID × KeyID]` array lookup
- **Per-tenant rate limits** — scale by percentage or set absolute overrides per
  rate-limit config
- **Per-tenant cost quotas** — daily, monthly, and flexible rolling windows (1h, 7d,
  30d, any duration)
- **Per-tenant credentials** — override global secrets at the tenant level via
  `CredentialRegistry`
- **Per-tenant observability** — per-tenant trace sample rate overrides and log level
  controls
- **Per-tenant API keys** — keys scoped to tenant and scope list; key rotation preserves
  `AppID` so rate limits survive rotation
- **Test isolation** — tenant ID range `0xF000–0xFFFF` reserved for ephemeral test
  tenants; never persisted

The tenant registry is an immutable snapshot published via `atomic.Pointer`. All
hot-path reads are lock-free. Management-plane writes are serialised under a single
mutex and publish a new snapshot atomically.

---

## Security & Authentication

### JWT / JWKS
- RS256, ES256 signature validation
- JWKS endpoint fetch with 12-hour cache and retry backoff
- Configurable issuer, audience, scopes, and clock-skew leeway
- Optional JTI replay prevention via datastore

### DPoP (RFC 9449)
- Embedded JWK parsing (RSA / EC / EdDSA)
- `htm` (HTTP method) + `htu` (URI) binding
- Access token hash (`ath`) verification
- `cnf.jkt` confirmation key matching
- JTI replay check via datastore

### Token Introspection (RFC 7662)
- Configurable introspection endpoint with result caching

### API Keys
- Format: `rah_<base64url(32 random bytes)>` (~47 chars)
- SHA-256 hash stored; raw key shown once on creation, never again
- Scope-based authorization, per-tenant restriction, expiry support
- Key rotation preserves `AppID`; rate limit continuity guaranteed

### OWASP & Bot Detection
- User-Agent pattern matching (built-in bot list + custom patterns)
- IP CIDR restriction and country-level geo-blocking (MaxMind GeoLite2)
- OWASP request validation patterns

---

## Rate Limiting

RAH's V2 rate limiting engine supports any combination of windows, enforcement modes,
and counting dimensions.

```yaml
rate_limit_configs_v2:
  - name: ai-tier-limits
    enforcement: approximate       # or "strict" (requires Redis)
    windows:
      - period: 1s
        limit: 10
        burst_factor: 2
      - period: 1m
        limit: 200
      - period: 1h
        limit: 5000
    count_by: tenant               # ip | key | tenant | global
    divide_by_nodes: true          # auto-scale limit by instance count
    exceeded_status: 429
    exceeded_body: '{"error":"rate limit exceeded"}'
    emit_headers: true             # X-RateLimit-Limit / Remaining / Reset
```

**Additional controls:**
- **Spike arrest** — smoothed limiting (at most one request per interval per key)
- **Circuit breaker** — three-state (closed → open → half-open) with configurable
  failure threshold, success threshold, open duration, and fallback flow
- **Per-tenant scale** — multiply or divide any tenant's effective limit without
  creating a separate config
- **Token bucket** — configurable refill rate and burst capacity as alternative to
  fixed windows

---

## Caching

**L1 — Zero-GC slab cache** built into the gateway process:
- Fixed memory budget (configurable `mem_budget_mb`), per-tenant quota
- Circular-buffer regions per `(sizeClass, TTLTier)` pair — oldest entry evicted on
  overflow, no GC pressure
- Two index lanes: tiny (keys ≤ 6 bytes) and hash (larger keys) with Swiss-style H2
  filter for fast miss detection
- Coarse clock for TTL checks (~1 ns, no syscall)

**L2 — Persistent backend** (optional):
- Redis / Dragonfly (pipelined batch writes)
- Disk (local development)

**Semantic cache** — store and retrieve LLM responses by embedding similarity rather
than exact key match. Embed the incoming prompt, search the vector store, return cached
response if similarity exceeds threshold. Skips the LLM call entirely on cache hit.

**Cross-instance invalidation** — cache write events published to Redis Pub/Sub;
peer instances update their L1 cache via subscriber goroutines.

---

## Secrets Management

RAH resolves credentials at runtime from any of the following sources via a uniform
reference string:

| Scheme | Example |
|--------|---------|
| `env:` | `env:ANTHROPIC_API_KEY` |
| `file://` | `file:///run/secrets/db_password` |
| `enc:` | `enc:k1:base64ciphertext` (AES-256-GCM, at-rest) |
| `gsm://` | `gsm://projects/my-project/secrets/api-key/versions/latest` |
| `vault://` | `vault://secret/data/myapp#api_key` |
| `awssm://` | `awssm://us-east-1/my-secret#field` |

**Features:**
- Singleflight coalescing — concurrent requests for the same ref make one provider call
- In-memory cache with 30-minute TTL; secure zeroing (`clear()`) on eviction
- Background rotation goroutine evicts expired entries every 5 minutes
- Short-lived token support — OAuth2 / Google ID tokens refresh before expiry
- `CredentialRegistry` — map logical names to secret refs with per-tenant overrides
- AES-256-GCM at-rest encryption via `enc:` scheme; key derived via Argon2id from
  passphrase + salt

---

## Observability

**Per-request:**
- Instruction-level timing (nanosecond precision, PC-indexed, unlimited depth)
- Upstream call breakdown: DNS + connect + TLS + TTFB + body transfer
- Sampled traces with configurable rate; `Authorization` and `X-API-Key` headers
  are never captured
- Access log (signed NDJSON) with configurable extra slot fields and retention

**Aggregated:**
- Per-API request counts, latency percentiles, bytes
- Per-tenant error distribution
- Cache hit rate and latency per instruction
- Custom metrics with dimensions (`emit_event`)

**Export:**
- Prometheus scrape endpoint
- OpenTelemetry (OTLP/gRPC) traces and metrics
- Webhook push for request summaries

**Event pipeline — 18 event kinds** (prompt in/out, LLM request/response, tool calls,
cost records, cache hits/misses, access log, audit log, upstream log, flow log, etc.)
routed to configurable sinks:

| Sink | Notes |
|------|-------|
| HTTP | Batched NDJSON POST |
| Redis Stream | `XADD` with configurable max-length |
| File | Buffered append, 256 KB flush |
| Stdout | Development default |

**Observability API** (management port `:8081`):
- `GET /observability/metrics`
- `GET /observability/access-log?api=&tenant=&status=&from=&limit=`
- `GET /observability/traces?api=&tenant_id=&min_ms=&from=&limit=`
- `GET /observability/apis/{name}` — per-API traces + access logs

---

## Adaptive Concurrency

An AIMD (Additive Increase / Multiplicative Decrease) controller continuously adjusts
the in-flight request limit based on observed p99 latency.

```yaml
concurrency:
  enabled: true
  target_overhead_ms: 50      # p99 target
  initial_limit: 2000
  min_limit: 500
  max_limit: 8000
  add_step: 50                # increase when healthy
  cut_factor: 0.85            # cut on distress
  tick_sec: 2
```

All parameters are patchable at runtime via `PATCH /admin/concurrency` without restart.
The limiter is a lock-free CAS semaphore (~10–20 ns per acquire/release).

---

## Scheduled Flows (Cron)

Flows can be triggered on a cron schedule without an inbound HTTP request. The
scheduler uses a hashed timing wheel (3600 slots, 1-second resolution) and supports
distributed multi-instance deployments — only one instance claims and executes each
scheduled event.

```yaml
schedules:
  - name: daily-report
    cron: "0 6 * * *"        # standard 5-field cron; 6-field with seconds also supported
    flow: generate_report
    tenant_alias: acme
    timeout_sec: 120
    on_failure:
      retry_count: 3
      retry_interval_sec: 30
      dead_letter_flow: handle_report_failure
    max_concurrent: 1
```

Schedules can also be created at runtime by tenants via the management API
(`runtime_only: true`). Persistence backends: in-memory or Redis.

---

## WebSocket

RAH manages WebSocket connections to browser clients and to upstream services in a
single layer — no separate broker required.

**Client sessions** — incoming WebSocket connections are tracked per session. Flows can
push messages to individual sessions or broadcast to all subscribers of a channel:

```yaml
- action: ws_broadcast_channel
  channel: alerts
  slot: message_slot

- action: ws_push_session
  session_id: session_id_slot
  slot: message_slot
```

**Upstream pools** — RAH maintains persistent outbound WebSocket connections to
upstream services with auto-reconnect and configurable ping intervals:

```yaml
- action: ws_upstream_connect
  name: data-feed
  url: wss://feeds.example.com/stream
  ttl_sec: 300         # idle TTL; connection closed and removed after expiry

- action: ws_upstream_disconnect
  name: data-feed
```

Static upstream connections (always-on) are defined in gateway config; dynamic
connections are opened on-demand per flow execution and reused within their TTL window.

---

## Model Context Protocol (MCP)

RAH both **consumes** and **serves** MCP.

**As a client** — call tools on any registered MCP server from a flow:
```yaml
- action: mcp_list_tools
    server: my-mcp-server
    mode: brief
- action: call_mcp_tool
    server: my-mcp-server
    tool: search_documents
    params: query_slot
```

**As a server** — expose RAH API endpoints as MCP tools. External LLM clients (Claude,
custom agents) discover and call your APIs via standard MCP JSON-RPC 2.0. Studio also
exposes 17 management tools over MCP.

Transports: HTTP, SSE, stdio.

---

## Data Stores

RAH binds logical data domains to named store instances. Each domain can point to a
different store — hot ephemeral data on Redis, audit records on PostgreSQL, API
definitions on disk.

### Supported Backends

| Backend | Kind | Use Case |
|---------|------|----------|
| Disk | `disk` | Development, single-node, zero dependencies |
| Redis / Dragonfly | `redis` / `dragonfly` | Distributed cache, rate limit counters, pub/sub, TTL |
| PostgreSQL | `postgresql` | Audit log, access log, observability, management-plane data |

Redis and Dragonfly share the same adapter (Dragonfly is wire-compatible with Redis).

**Smart coalescing** — both backends batch datastore operations to reduce round-trips
under load:
- **Redis / Dragonfly** — concurrent cache writes are pipelined; multiple operations in
  the same request window are sent as a single batch
- **PostgreSQL** — a batching wrapper coalesces concurrent reads and writes into
  `MultiGet` / `MultiPut` queries with a read-your-writes guarantee; high-concurrency
  observability workloads generate far fewer database round-trips than request volume
  would suggest

### Domain Bindings

Required domains (must be configured at startup):

| Domain | Purpose |
|--------|---------|
| `api_definitions` | Compiled API route definitions |
| `flows` | Compiled flow instruction plans |
| `tenant_data` | Tenant registry (aliases, URLs, rate limits) |
| `cache` | Response cache L2 backend |

Optional domains (skip gracefully if unbound):

`api_keys`, `apps`, `rate_limit`, `rl_configs_v2`, `tiers`, `credentials`,
`async_jobs`, `instances`, `obs_access_log`, `obs_traces`, `dpop_jti`,
`introspection_cache`, `releases`, `deploy_history`, and more.

### At-Rest Encryption

Any domain can be wrapped with transparent AES-256-GCM encryption. Per-tenant key
derivation (HKDF-SHA256) is supported. Key rotation is handled via versioned keys —
old and new keys are trusted simultaneously during rotation.

### Tenant-First Key Model

All keys are scoped by tenant:
```
tenant:{tenant_name}:{domain}:{key}
```
Control-plane data (API definitions, flows) lives under `__global__`.

---

## Customer Data Sources

RAH can connect flows directly to customer-owned Postgres and Redis instances. These
are **separate** from RAH's internal datastore domains — they are your databases, with
your schemas and your tables. RAH handles connection pooling, tenant isolation, and
parameterized query safety.

### Postgres data sources

```yaml
data_sources:
  - name: orders_db
    driver: postgres
    dsn_ref: env:ORDERS_DATABASE_URL   # or a literal DSN
    max_connections: 20
    query_timeout_sec: 30
    tenant_isolation: schema           # "schema" | "rls" | "" (none)
    tenant_key: id                     # "id" | "alias"
    shared_schemas: [public, shared]   # schemas visible to all tenants (schema mode)
    rls_variable: app.current_tenant   # session variable name (rls mode)
```

**Tenant isolation modes:**
- `schema` — sets `search_path = tenant_<id>, public` per connection; each tenant sees only its own schema
- `rls` — sets `SET LOCAL <rls_variable> = '<tenant>'`; your Postgres RLS policies enforce row-level filtering
- *(empty)* — no automatic isolation; manage it yourself in SQL

**Flow steps:**

| Step | Description |
|------|-------------|
| `db_query` | Execute a SQL SELECT; returns a JSON array |
| `db_query_one` | SELECT expecting one row; returns a JSON object (404 if empty) |
| `db_exec` | Execute INSERT / UPDATE / DELETE / DDL; returns affected row count |

```yaml
- action: db_query
  key: orders_db          # data source name
  value: "SELECT id, amount FROM orders WHERE customer_id = $1"
  vars: ["{customer_id}"]
  as: orders

- action: db_exec
  key: orders_db
  value: "INSERT INTO events (type, payload) VALUES ($1, $2)"
  vars: ["{event_type}", "{event_body}"]
```

DDL statements (`CREATE TABLE`, `ALTER TABLE`, etc.) are also accepted via `db_exec` —
there is no statement restriction. This lets flows provision schemas or run migrations
as part of a deployment flow.

**Named queries with automatic batching:**

```yaml
# Define at sync time
queries:
  get_orders_by_ids:
    sql: "SELECT id, customer_id, amount FROM orders WHERE id = ANY($1::bigint[])"
    batch_by: "$1"
    batch_window: 500us   # collect requests for up to 500 µs
    batch_max: 100        # max keys per batch

# Reference in a flow
- action: db_query
  key: orders_db
  value: "query:get_orders_by_ids"
  vars: ["{order_id}"]
  as: order
```

Concurrent flow executions requesting different keys within the batch window are
collapsed into a single `WHERE id = ANY($1)` query. A singleflight group additionally
deduplicates identical concurrent keys.

### Redis data sources

```yaml
redis_sources:
  - name: sessions
    addr: "redis.example.com:6379"   # single node; use "addrs" for cluster
    password: env:REDIS_PASSWORD
    db: 0
    tls: true
    tenant_prefix: alias             # "id" | "alias" | "none"
    key_sep: ":"
```

All key operations are automatically namespaced per tenant (`{tenant_alias}:{key}`).
The reserved prefix `_rah:` is blocked and cannot be used by flows.

**Available operations:** `redis_get`, `redis_put`, `redis_mget`, `redis_mput`,
`redis_del`, `redis_exists`, `redis_incr`, `redis_decr`, `redis_expire`, `redis_ttl`,
`redis_persist`, `redis_publish`, `redis_lock`, `redis_unlock` — plus sorted sets
(`redis_zadd`, `redis_zrange`, `redis_zrangebyscore`, `redis_zrank`, `redis_zscore`,
`redis_zrem`, `redis_zpopmin`, `redis_zpopmax`), hashes (`redis_hset`, `redis_hmset`,
`redis_hget`, `redis_hgetall`, `redis_hdel`, `redis_hincrby`), and lists
(`redis_lpush`, `redis_rpush`, `redis_lpop`, `redis_rpop`, `redis_lrange`, `redis_llen`),
and sets (`redis_sadd`, `redis_srem`, `redis_sismember`, `redis_smembers`, `redis_scard`).

---

## Vector Store Backends

Used by `vector_search`, `vector_upsert`, `semantic_cache_get`, and
`semantic_cache_put` instructions.

| Backend | Kind |
|---------|------|
| Qdrant | `qdrant` |
| Chroma | `chroma` |
| Weaviate | `weaviate` |
| Redis Stack | `redis` |
| PgVector (via PostgREST) | `pgvector` |
| Generic HTTP | `http` |

---

## The Three Binaries

### `rah-gateway`
The execution runtime. Data plane on `:8080`, management plane on `:8081`.

```
-port    8080    Data plane port
-mport   8081    Management plane port
-config  path    Path to gateway YAML / JSON config
-health         Health-check mode (probe /health, exit 0/1)
```

### `rah-studio`
Web UI for flow authoring, release management, observability dashboards, and
AI-assisted configuration.

```
-port                  8092    Studio UI port
-gateway-management-url       http://127.0.0.1:8081
-store-kind            memory  Release store: memory | file
-obs-store-type               memory | postgres | redis
-auth-enabled                 Require login
```

### `rah-sync`
CI/CD CLI for bundle management.

```bash
rah-sync lint   <dir>                          # validate flows and APIs
rah-sync publish <dir> --studio <url> --tag v1 # upload release
rah-sync promote <release-id> --env prod       # deploy to environment
rah-sync diff   <release-a> <release-b>        # compare releases
```

Lint runs five tiers of validation: structural → schema → reference integrity →
semantic → advanced. All findings include file path and line number.

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

### Full stack (Redis + PostgreSQL via Docker)

```bash
make docker-up
# Starts Redis, PostgreSQL, and rah-gateway
```

### With Studio

```bash
make docker-up-studio
# Adds rah-studio at http://localhost:8092
```

### Minimal configuration (`gateway.yaml`)

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

### Deploy a flow

```bash
# Define your API and flow in a bundle directory, then:
rah-sync lint    ./my-bundle
rah-sync publish ./my-bundle --studio http://localhost:8092
```

Or POST directly to the management plane:

```bash
curl -X POST http://localhost:8081/sync \
  -H 'Content-Type: application/json' \
  -d @bundle.json
```

---

## Configuration Reference

The gateway config is a single YAML (or JSON) file. Environment variables are expanded
automatically. Key top-level sections:

| Section | Purpose |
|---------|---------|
| `layout` | Slot sizes, max APIs, default rate limits |
| `datastore` | RAH internal store backends and domain bindings |
| `data_sources` | Customer-owned Postgres connections (with tenant isolation) |
| `redis_sources` | Customer-owned Redis connections (with tenant key namespacing) |
| `secrets` | Credential provider config |
| `cache` | L1 slab cache sizing and L2 backend |
| `llm` | Model catalog and MCP server registrations |
| `pricing` | Per-model cost overrides |
| `quotas` | Per-tenant cost quota definitions |
| `observability` | Traces, access log, metrics, export sinks |
| `concurrency` | AIMD controller parameters |
| `scheduler` | Cron schedule definitions and store backend |
| `websocket` | Static upstream WS connections and session config |
| `egress` | Outbound call profiles (TLS, timeouts, connection pool) |
| `ingest` | Event pipeline sources, kinds, and sinks |
| `mqtt` | MQTT broker connections |
| `grpc` | gRPC message size limits and keepalive |
| `tls` | HTTPS listener (cert/key paths) |
| `admin` | Management plane auth (Basic Auth, roles) |
| `vector_stores` | Vector database backends |
| `instance` | Config poll interval, heartbeat |

---

## Performance

All numbers are gateway overhead only, excluding upstream latency.

| Metric | Value |
|--------|-------|
| Request processing overhead | < 5 µs |
| Tenant alias lookup | ~40–70 ns (lock-free hash table) |
| Tenant property read | ~2–5 ns (flat matrix) |
| Router lookup | ~10–20 ns (radix tree, zero alloc) |
| Rate limit check (local) | ~20–50 ns (CAS epoch counter) |
| Cache L1 hit | ~100–300 ns |
| Arena allocation (inline) | ~5 ns |
| Context pool acquire | ~10–15 ns |
| Concurrency gate | ~10–20 ns (CAS semaphore) |

**Architecture choices that make these numbers possible:**
- All hot-path reads via `atomic.Pointer` snapshots — no mutex on request path
- Per-request arena: 1 KB inline, 4 KB pool-borrowed ext block, heap only on overflow
- Instruction table compiled at deploy time — zero parsing at request time
- Flat typed slot arrays (`ByteSlots [32]`, `IntSlots [16]`) — cache-line resident
- Two-generation rolling latency ring for AIMD — atomic adds only on hot path
- Lock-free MPMC ring (Vyukov algorithm) for event pipeline and logging

---

## Build

```bash
make build          # gateway + studio + sync for current platform
make build-all      # cross-compile linux/darwin/windows × amd64/arm64
make test           # unit tests
make test-race      # race detector (180s timeout)
make bench          # in-process Go benchmarks
make lint           # golangci-lint
make helm-install-local  # deploy to local Kubernetes
```

---

## License

See [LICENSE](LICENSE).
