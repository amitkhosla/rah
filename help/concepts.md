# Key Concepts

This guide explains the core concepts you'll encounter when managing APIs with RAH.

## Flow

A **Flow** is the sequence of logic that runs when a request arrives at the gateway. Think of it as a recipe or checklist that the gateway follows.

Flows are defined in YAML and can include instructions like:
- Validate a request header or token
- Look up tenant information
- Check rate limits
- Call an upstream service
- Transform the response
- Return the result to the client

Flows can also call other flows as sub-steps, allowing you to build modular, reusable logic.

**Example:**
```yaml
name: get_user_flow
steps:
  - validate_auth_token
  - lookup_tenant
  - check_rate_limit
  - call_upstream: https://api.example.com/users/{id}
  - return: 200
```

---

## API

An **API** is a route binding that connects a specific HTTP method and path to a flow.

When you define an API, you're saying: "When a client makes a `GET` request to `/users/{id}`, run the `get_user_flow` flow."

**Example:**
```yaml
routes:
  - method: GET
    path: /users/{id}
    flow: get_user_flow
  
  - method: POST
    path: /users
    flow: create_user_flow
```

The gateway matches incoming requests against these routes and executes the corresponding flow.

---

## Instruction (Step)

An **Instruction** is a single operation within a flow. Each instruction performs one task:

- **validate_token** — Check that an API key or JWT is valid
- **http_call** — Make a request to an upstream service
- **rate_limit** — Check whether the client has exceeded their request quota
- **registry_lookup** — Retrieve tenant configuration (URLs, credentials)
- **ai_call** — Send a prompt to an AI model via MCP
- **check_cache** — Look up a previous response in the cache
- **return** — Send a response back to the client

Behind the scenes, the gateway compiles flows into a flat list of instructions and executes them in order. This flat design keeps latency extremely low.

---

## Tenant

A **Tenant** is a logical customer or account. Each tenant is identified by an alias (e.g., `acme-corp`, `bigbank-prod`) and has their own:

- **Upstream URLs** — Where the gateway forwards requests (e.g., their backend API)
- **Credentials** — API keys, OAuth tokens, or other secrets
- **Metadata** — Custom key-value pairs for authorization or routing logic
- **Rate limit policies** — How many requests they can make per second/minute

This isolation ensures that one tenant's configuration and behavior doesn't affect another.

**Example:**
```yaml
tenants:
  - alias: acme-corp
    upstream_url: https://api.acme.com
    credentials:
      api_key: sk-acme-12345
    metadata:
      tier: enterprise
      region: us-west
```

---

## Studio

**Studio** is the browser-based management interface for RAH. From Studio, you can:

- **Create and edit APIs** — Define routes, flows, and instructions without writing YAML
- **Manage tenants** — Add/update tenant URLs, credentials, and metadata
- **Deploy changes** — Push your API definitions to the live gateway
- **Monitor traffic** — View request volume, latency, and errors
- **Configure rate limits** — Define and assign rate limit policies
- **Test flows** — Run test requests against your APIs before deploying

Studio runs as a separate service and connects to the gateway via a REST management API.

---

## Sync

**Sync** is the process of pushing your API definitions (flows, routes, and tenant configuration) from Studio to the live gateway. When you sync:

1. Your flow definitions and routes are compiled and loaded into memory
2. Tenant configuration is stored in the registry
3. All running gateway instances are notified of the changes
4. New requests use the updated configuration immediately (no restart required)

You can sync via:
- **Studio UI** — Click "Deploy"
- **REST API** — `POST /v1/api-definitions/sync`
- **CI/CD** — Automated deployment from your version control system

---

## DSL (Domain-Specific Language)

The **DSL** is the human-readable text format for writing flows. It's designed to be understandable without programming experience:

```yaml
flow: authenticate_and_forward
  steps:
    - validate_bearer_token
    - registry_lookup(tenant_id)
    - rate_limit_check(policy: enterprise)
    - http_call(method: GET, url: $tenant.upstream_url)
    - return(status: 200, body: $response)
```

The DSL is embedded in YAML files and can be edited in Studio or any text editor.

---

## Instruction Table

Under the hood, flows are compiled to a flat, ordered list of instructions. This design is what makes RAH extremely fast.

Instead of a tree of nested calls (which requires jumping around memory), all instructions are stored sequentially in a table. The gateway reads the table from top to bottom, executing each instruction in order. Each instruction knows the address of the next instruction to execute.

This flat layout has two benefits:
- **CPU cache efficiency** — Instructions are accessed sequentially, so the CPU cache is always warm
- **Predictable latency** — No branching, no function calls, no surprises

You don't need to think about instruction tables—the compiler handles this automatically—but it's good to know why RAH is so fast.

---

## Rate Limit Config

A **Rate Limit Config** is a named policy that defines how many requests a tenant can make within a specific time window.

**Example:**
```yaml
rate_limits:
  - name: free_tier
    window: 60s
    limit: 100     # 100 requests per 60 seconds

  - name: enterprise
    window: 60s
    limit: 10000   # 10,000 requests per 60 seconds
```

You assign rate limit configs to tenants. When a request arrives, the gateway checks the tenant's current request count against their assigned policy. If they've exceeded the limit, the gateway returns a `429 Too Many Requests` response.

Rate limiting is distributed across all gateway instances when using a shared cache like Redis.

---

## Registry

The **Registry** is the runtime store of tenant information. When the gateway processes a request:

1. It identifies the tenant (from a header, path parameter, or token)
2. It looks up the tenant in the registry
3. It retrieves the tenant's upstream URLs, credentials, and metadata
4. It uses this information to process the request

The registry is updated whenever you sync API definitions from Studio. It can be stored in memory (for a single gateway instance) or in a shared store like Redis (for multiple instances).

---

## MCP (Model Context Protocol)

**MCP** is an open standard protocol for connecting AI models to tools. RAH supports MCP in two ways:

1. **Call external MCP servers** — Your flows can invoke tools exposed by external MCP servers (e.g., a weather API, a database query tool)
2. **Serve as an MCP server** — Other systems can call RAH's flows as if they were tools

This is useful for:
- **AI-powered APIs** — An AI agent can use RAH's flows as tools to answer user questions
- **Composable services** — One RAH instance can call flows from another
- **Integration with AI platforms** — Your AI orchestration platform can invoke RAH flows

---

## Ingest Pipeline

The **Ingest Pipeline** is a background event bus that logs and tracks activity in the gateway. It captures:

- **Request/response events** — Every API call, status code, latency
- **Metrics** — Request counts, error rates, cache hit rates
- **Traces** — Detailed timing of each instruction within a flow
- **AI prompt tracking** — Which prompts were sent, what responses were received, costs
- **Custom events** — Application-specific events you emit from flows

The ingest pipeline is designed to have zero impact on request latency. Events are collected asynchronously and sent to configured sinks (log files, Redis, PostgreSQL, etc.).

---

## How a Request Flows Through RAH

Here's the typical journey of an API request:

```
[Client Request]
      ↓
[Gateway Port 8080]
      ↓
[Router: Match HTTP method + path to a route]
      ↓
[Flow Execution: Run instructions in sequence]
  ├─ validate_token
  ├─ registry_lookup (identify tenant, get their config)
  ├─ rate_limit_v2 (check if tenant exceeded quota)
  ├─ cache_check (is this response already cached?)
  ├─ http_call (forward to upstream service)
  ├─ cache_store (save response for future requests)
  └─ return(200, response)
      ↓
[Response back to Client]
      ↓
[Ingest Pipeline: Log event, record metrics, trace timing]
```

Each instruction executes in sequence. If an instruction fails or returns an error, the flow stops and an error response is returned to the client.

---

## Summary

| Concept | What It Is |
|---------|-----------|
| **Flow** | The sequence of logic that runs for an API |
| **API** | A route binding (method + path → flow) |
| **Instruction** | A single operation within a flow |
| **Tenant** | A customer/account with isolated config |
| **Studio** | Browser-based management UI |
| **Sync** | Deploy API definitions to the gateway |
| **DSL** | Human-readable flow definition language |
| **Instruction Table** | Compiled flat list for maximum speed |
| **Rate Limit Config** | Named policy defining request quotas |
| **Registry** | Runtime store of tenant data |
| **MCP** | Protocol for AI tools and integrations |
| **Ingest Pipeline** | Background event logging and metrics |

---

## Next Steps

- **Getting started:** See [Setting Up a Standalone Gateway](setup-standalone.md)
- **Scaling up:** See [Setting Up a Distributed Gateway](setup-distributed.md)
