# Changelog

All notable changes to RAH are documented here.

---

## [v0.2.0] — 2026-08-09

### Added

**Customer Data Sources**
- Connect flows to customer-owned Postgres instances via `data_sources` config. Supports schema-per-tenant (`search_path`) and row-level security (`SET LOCAL`) isolation modes.
- Connect flows to customer-owned Redis instances via `redis_sources` config. All key operations are automatically namespaced per tenant.
- 45+ Redis operations: strings, hashes, lists, sets, sorted sets, distributed locks, pub/sub.
- `db_query`, `db_query_one`, `db_exec` flow steps for SQL — SELECT, INSERT, UPDATE, DELETE, and DDL all supported.
- Named query library with automatic DataLoader-style batching: concurrent requests within a configurable window (default 500µs) are collapsed into a single `WHERE id = ANY($1)` query with singleflight deduplication.

**Scheduled Flows (Cron)**
- Trigger any flow on a cron schedule (5-field and 6-field with seconds supported).
- Hashed timing wheel (3600 slots, 1-second resolution) with distributed claiming — only one instance executes each event in a multi-gateway deployment.
- Per-schedule retry config, dead-letter flow on failure, and max concurrency limit.
- Tenants can create and manage their own schedules at runtime via management API.

**WebSocket**
- Manage browser client WebSocket sessions from flows: push to a specific session (`ws_push_session`) or broadcast to a channel (`ws_broadcast_channel`).
- Persistent upstream WebSocket connection pools with TTL management, auto-reconnect, and configurable ping intervals.
- Dynamic on-demand upstream connections (`ws_upstream_connect` / `ws_upstream_disconnect`) reused within TTL window.

**OpenTelemetry**
- Full OTEL trace and metric export via OTLP/gRPC.
- Per-instruction nanosecond timing, upstream call breakdowns (DNS + connect + TLS + TTFB + body), LLM call records, and custom metrics exportable to any OTEL-compatible backend.

**LLM Providers**
- Added Ollama (local models, no auth required).
- Added DeepSeek.

### Changed

- DSL: `+` and `-` can now be used directly in flow expressions instead of `sum` / `minus`.
- Improved upstream configuration handling.
- Studio: release management UI improvements, improved deployment pipeline.

### Fixed

- Multiple security vulnerability fixes.
- Removed deprecated method usage across the codebase.
- Lint pipeline: feature branches now block on new lint errors; main branch handles pre-existing lint issues gracefully.

---

## [v0.1.0] — 2026-07-20

Initial release.

- Core execution engine: YAML flows compiled to zero-allocation bytecode, sub-5µs gateway overhead.
- HTTP, gRPC, GraphQL, SOAP, MQTT protocol support.
- JSON ↔ XML ↔ Avro ↔ Protobuf format conversion.
- JWT / DPoP / token introspection authentication.
- Multi-window rate limiting (V2), spike arrest, circuit breaker.
- Zero-GC L1 slab cache + Redis/PostgreSQL L2 cache.
- Semantic cache via vector embeddings.
- Multi-tenant registry with lock-free alias resolution.
- LLM orchestration: Anthropic, OpenAI, Google Gemini, AWS Bedrock, custom endpoints.
- MCP client and server support.
- Per-tenant cost quotas, credential overrides, observability controls.
- Prometheus metrics export.
- `rah-gateway`, `rah-studio`, `rah-sync` binaries.
