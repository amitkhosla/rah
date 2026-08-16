# Rah as a Lightweight App Host

Deploy backends, BFFs, and simple web/API applications without infrastructure overhead. Rah compiles your YAML-defined flows to zero-allocation bytecode and hosts them at sub-5µs gateway overhead — all in a single binary with no plugins, sidecars, or external servers.

---

## Table of Contents

1. [Overview](#overview)
2. [BFF Pattern](#bff-pattern)
3. [Simple App Hosting](#simple-app-hosting)
4. [The school-mgmt Example](#the-school-mgmt-example)
5. [LLM Integration](#llm-integration)
6. [Path Parameters & URL Routing](#path-parameters--url-routing)
7. [Testing](#testing)
8. [Quick Reference](#quick-reference)

---

## Overview

Rah is a no-code app host: define your APIs and flows in YAML, deploy via `rah-sync`, and your app is live. No Kubernetes. No Node.js servers. No Docker orchestration. A single Rah gateway binary executes all your request logic.

**Core strengths for app hosting:**

- **Fast** — sub-5µs request overhead, zero allocations on hot path
- **Stateless** — no middleware chain, no middleware order confusion
- **Multi-tenant** — per-tenant rate limits, credentials, and data isolation built-in
- **Auth-first** — JWT, API keys, DPoP, token introspection, scopes
- **Observable** — per-instruction timing, per-API access logs, sampled traces
- **No infra** — single binary (gateway), optional Redis/PostgreSQL for scale

**What you build in Rah:**

| App Type | Example |
|----------|---------|
| **REST API** | CRUD endpoints backed by database flows |
| **BFF** | Aggregate multiple upstream APIs; shape data for mobile/web |
| **Auth flow** | OAuth2 login/callback/logout with session management |
| **Webhook receiver** | HMAC-signed webhook listener with deduplication |
| **Event processor** | Consume Kafka/pub-sub topics, transform, and emit events |
| **GraphQL/gRPC gateway** | Protocol translation in a single flow |
| **LLM agent** | Prompt injection safe, cost-controlled LLM orchestration |

---

## BFF Pattern

A **Backend For Frontend** (BFF) is a server layer that:

1. **Aggregates** calls to multiple upstream services
2. **Shapes** responses for a specific client (mobile app, web UI, etc.)
3. **Enforces** auth, rate limits, and data access rules
4. **Transforms** response formats (e.g., REST → GraphQL schema)

### How Rah Does It

Define one flow per client endpoint. In the flow, call multiple upstreams, aggregate results, and return shaped data — all in a compiled instruction sequence.

**Example: Mobile app + web app served by the same Rah instance**

- Mobile client hits `/mobile/user/{id}` — gets profile + settings
- Web client hits `/web/user/{id}` — gets profile + activity feed
- Both routes live in the same Rah instance with no separate BFF deployment

### Aggregation Example

```yaml
flows:
  - name: mobile_user_detail
    action: upsert
    code: |
      user_id = path("id")
      profile = http(method: "GET", url: "https://users.internal/api/users/{user_id}")
      settings = http(method: "GET", url: "https://settings.internal/api/user/{user_id}/settings")
      return(200, '{"id":"{user_id}","name":"{profile.name}","theme":"{settings.theme}"}')
```

---

## Simple App Hosting

### Bundle Structure

A **bundle** is a directory containing your app's flows, APIs, and configuration. Deploy atomically via `rah-sync`.

```
my-app/
├── apis/
│   ├── users.yaml          # API route definitions
│   └── admin.yaml
├── flows/
│   ├── users.yaml          # Flow implementations
│   └── ai.yaml
├── tests/
│   └── users.yaml          # Test definitions
├── tenants.yaml
├── api-keys.yaml
├── llm.yaml
└── rate-limits.yaml
```

### Step 1: Define APIs

```yaml
# apis/users.yaml
apis:
  - name: users-list
    path: /users
    flow_name: user_list_flow
    skip_rate_limit: true
    action: upsert

  - name: user-detail
    path: /users
    flow_name: user_get_flow
    action: upsert
    endpoint_configs:
      - path: /{id}
        method: GET

  - name: user-create
    path: /users
    flow_name: user_create_flow
    action: upsert
    endpoint_configs:
      - path: /
        method: POST
```

### Step 2: Implement Flows

```yaml
# flows/users.yaml
flows:
  - name: user_list_flow
    action: upsert
    code: |
      return(200, '{"users":[{"id":"1","name":"Alice"},{"id":"2","name":"Bob"}]}')

  - name: user_get_flow
    action: upsert
    code: |
      id = path("id")
      return(200, '{"id":"u1","name":"Alice","email":"alice@example.com"}')

  - name: user_create_flow
    action: upsert
    code: |
      name = body("name")
      email = body("email")
      return(201, '{"id":"new","name":"' + name + '","status":"created"}')
```

### Step 3: Configure Tenants

```yaml
# tenants.yaml
tenants:
  - aliases: [acme-corp, acme]
    action: upsert
  - aliases: [widget-inc, widget]
    action: upsert
```

### Step 4: Configure API Keys

```yaml
# api-keys.yaml
api_keys:
  - alias: acme-admin-key
    app: my-app
    key_ref: acme-admin-sk-change-me
    allowed_tenants: [acme-corp]
    action: upsert

  - alias: widget-api-key
    app: my-app
    key_ref: widget-api-sk-change-me
    allowed_tenants: [widget-inc]
    action: upsert
```

### Step 5: Deploy

```bash
# Lint (5 tiers of validation)
rah-sync lint ./my-app

# Publish to Studio
rah-sync publish ./my-app --studio http://localhost:8092

# Deploy to gateway
rah-sync deploy <release-id> --studio http://localhost:8092
```

---

## The school-mgmt Example

A complete multi-tenant school management system demonstrating production patterns.

### What's Included

| Entity | Endpoints | Notes |
|--------|-----------|-------|
| Students | LIST, GET, CREATE, UPDATE | Per-tenant isolation |
| Teachers | LIST, GET, CREATE | Role-based access |
| Classes | LIST, GET, CREATE | Includes teacher assignment |
| Fees | LIST, RECORD, OUTSTANDING | Outstanding fee tracking |
| Homework | LIST, ASSIGN, SUBMIT | Due date tracking |
| Timetable | GET, SET | Weekly schedule |
| AI Tutor | ASK | LLM-powered answers |

### Multi-Tenant, Role-Based Keys

```yaml
# api-keys.yaml
api_keys:
  - alias: alpha-admin-key
    key_ref: alpha-admin-sk-change-me
    allowed_tenants: [alpha-school]
    action: upsert

  - alias: alpha-teacher-key
    key_ref: alpha-teacher-sk-change-me
    allowed_tenants: [alpha-school]
    action: upsert

  - alias: alpha-student-key
    key_ref: alpha-student-sk-change-me
    allowed_tenants: [alpha-school]
    action: upsert
```

### Class Detail with Teacher Info

```yaml
flows:
  - name: school_class_get
    action: upsert
    code: |
      id = path("id")
      return(200, '{"id":"c1","name":"10A Mathematics","subject":"Mathematics","teacher":{"id":"t1","name":"Ms. Johnson"},"students":15,"room":"101"}')
```

### Outstanding Fees

```yaml
flows:
  - name: school_fees_outstanding
    action: upsert
    code: |
      return(200, '{"student_id":"st2","name":"Bob Kumar","outstanding":[{"fee_type":"tuition","amount":5000,"due_date":"2026-09-01","status":"overdue"}],"total_outstanding":5000}')
```

---

## LLM Integration

### Register Models

```yaml
# llm.yaml
llm_models:
  - alias: school-llm
    provider: openai
    model_id: gpt-4o-mini
    use_completion_tokens: true   # required for gpt-4o-mini and newer OpenAI models
    action: upsert

  - alias: my-llm
    provider: anthropic
    model_id: claude-haiku-4-5-20251001
    action: upsert
```

**Providers:** `anthropic`, `openai`, `google`, `bedrock`, `ollama`, `custom` (OpenAI-compatible)

> **Note:** For OpenAI models released after late 2024 (gpt-4o-mini, o1, o3, gpt-4.1-*), set `use_completion_tokens: true`. This switches the request field from `max_tokens` to `max_completion_tokens` as required by the API.

### Call LLM in a Flow

```yaml
flows:
  - name: school_ai_ask
    action: upsert
    code: |
      question = body("question")
      subject = body("subject")
      answer = llm(
        question,
        model: "school-llm",
        system: "You are an AI tutor for school students. Answer clearly and educationally in 2-3 sentences.",
        max_tokens: 300
      )
      return(200, answer)
```

**llm() parameters:**

| Parameter | Type | Purpose |
|-----------|------|---------|
| prompt (arg 1) | string | User message |
| `model:` | string | Alias from llm.yaml |
| `system:` | string | System prompt (optional) |
| `max_tokens:` | int | Output token cap |
| `temperature:` | float | 0.0–2.0, default 1.0 |

---

## Path Parameters & URL Routing

Rah uses a prefix-match radix trie for routing. The correct pattern for parameterised routes is:

- **`path:`** — static base path (registered in the router)
- **`endpoint_configs:`** — param segment + method (handled by sub-router)

### Single Route with Path Param

```yaml
apis:
  - name: user-detail
    path: /users          # base path — registered in router
    flow_name: user_get
    action: upsert
    endpoint_configs:
      - path: /{id}       # sub-router handles /{id} segment
        method: GET
```

### Multiple Methods on Same Base Path

```yaml
apis:
  - name: users-api
    path: /users
    flow_name: user_list   # default flow (GET /)
    action: upsert
    endpoint_configs:
      - path: /
        method: GET
      - path: /
        method: POST
        flow_name: user_create
      - path: /{id}
        method: GET
        flow_name: user_get
      - path: /{id}
        method: PUT
        flow_name: user_update
      - path: /{id}
        method: DELETE
        flow_name: user_delete
```

### Accessing Params in Flows

```yaml
flows:
  - name: user_get
    action: upsert
    code: |
      id = path("id")            # path param
      format = query("format")   # ?format=json
      auth = header("X-API-Key") # request header
      name = body("name")        # JSON body field
      return(200, '{"id":"' + id + '"}')
```

> **Common mistake:** Using `/users/{id}` as the top-level `path:` stores `{id}` literally in the router trie and never matches real URLs. Always put `{id}` in `endpoint_configs[].path` instead.

---

## Testing

### Test File Structure

```yaml
# tests/users.yaml
tests:
  - name: "User list returns 200"
    flow_name: "user_list_flow"
    mode: "published"
    call_mode: "real"
    input:
      method: GET
      path: /users
      headers: {}
      body: ""
    assertions:
      - type: status
        expected: 200

  - name: "User detail returns 200"
    flow_name: "user_get_flow"
    mode: "published"
    call_mode: "real"
    input:
      method: GET
      path: /users/u1
      headers: {}
      body: ""
    assertions:
      - type: status
        expected: 200

  - name: "User create returns 201"
    flow_name: "user_create_flow"
    mode: "published"
    call_mode: "real"
    input:
      method: POST
      path: /users
      headers:
        Content-Type: application/json
      body: '{"name":"Dave","email":"dave@example.com"}'
    assertions:
      - type: status
        expected: 201
```

### Run Tests

```bash
rah-sync test ./my-app --studio http://localhost:8092
```

---

## Quick Reference

### Common DSL Patterns

| Pattern | Code |
|---------|------|
| Static JSON | `return(200, '{"key":"value"}')` |
| Path param | `id = path("id")` |
| Query param | `page = query("page")` |
| Body field | `name = body("name")` |
| Request header | `auth = header("Authorization")` |
| Call LLM | `answer = llm(prompt, model: "my-llm", max_tokens: 200)` |
| HTTP upstream | `resp = http(method: "GET", url: "https://api.example.com/data")` |
| Conditional | `if (role == "admin") return(200, ...) else return(403, ...)` |

### Deployment Checklist

- [ ] `rah-sync lint ./my-app` passes
- [ ] All flows tested (`rah-sync test`)
- [ ] Tenants defined in `tenants.yaml`
- [ ] API keys bound to tenants in `api-keys.yaml`
- [ ] LLM models configured with correct `use_completion_tokens` flag if using OpenAI
- [ ] Rate limits configured per role/tier
- [ ] `rah-sync publish` → `rah-sync deploy` completed

### Flow Progression

Start simple, add capability incrementally:

```
Static JSON → Template responses → Real DB query → LLM augmentation → Caching
```

Each step is a flow edit + redeploy — no infrastructure changes needed.
