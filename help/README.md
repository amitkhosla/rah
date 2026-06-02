# RAH API Gateway

Welcome to RAH, the intelligent API gateway built for modern cloud-native applications. RAH powers high-performance API routing, intelligent request transformation, AI-native workflows, and multi-tenant SaaS platforms—all with sub-microsecond latency and zero operational overhead.

## Why RAH?

- **Ultra-Low Latency**: <5 microseconds end-to-end (without upstream calls). No GC pauses, no contention.
- **AI-Native**: Built-in support for LLM chains, agentic workflows, and intelligent routing decisions.
- **Multi-Tenant by Default**: Isolated API configurations, cache, and metrics per tenant. Designed for SaaS.
- **Intelligent Routing**: Route based on headers, query parameters, tenant identity, ML predictions, or business logic.
- **Declarative Configuration**: Define APIs and business logic in YAML or via the visual Studio interface.
- **Zero-Config Scaling**: Horizontal scaling without centralized coordination. Each instance is independent.
- **Enterprise-Grade**: Rate limiting, authentication, observability, audit logs, and compliance features built in.

## Getting Started

Choose your path based on your role:

| I'm...                                    | Start here                                                     |
|-------------------------------------------|--------------------------------------------------------------|
| New to RAH                                | [Getting Started](./getting-started.md) — Run RAH in 10 min |
| Setting up the gateway                    | [Setup: Standalone](./setup-standalone.md) or [Distributed](./setup-distributed.md) |
| Using the Studio UI                       | [Studio Guide](./studio-guide.md)                            |
| Defining APIs in code/YAML                | [DSL Guide](./dsl-guide.md)                                 |
| Managing deployments & CI/CD              | [Sync Utility](./sync-utility.md) or [CI/CD Guide](./cicd-guide.md) |
| Building AI products or agents            | [AI Gateway](./ai-gateway.md) or [Agentic Workflows](./agentic-workflows.md) |
| Running a multi-tenant SaaS platform      | [Tenancy & API Keys](./tenancy-and-api-keys.md)            |

## All Guides

- **[Concepts](./concepts.md)** — Understand APIs, Flows, Instructions, Tenants, and more
- **[Getting Started](./getting-started.md)** — Run RAH in 10 minutes with Docker
- **[Setup: Standalone](./setup-standalone.md)** — Single-instance deployment
- **[Setup: Distributed](./setup-distributed.md)** — Multi-instance deployment with shared datastore
- **[Studio Guide](./studio-guide.md)** — Visual API builder and configuration interface
- **[DSL Guide](./dsl-guide.md)** — Define APIs, flows, and logic in YAML
- **[Sync Utility](./sync-utility.md)** — Synchronize configuration across instances
- **[CI/CD Integration](./cicd-guide.md)** — GitOps, automated deployments, environment management
- **[Tenancy & API Keys](./tenancy-and-api-keys.md)** — Multi-tenant isolation, tenant routing, key management
- **[Rate Limiting](./rate-limiting.md)** — Token bucket, fixed window, per-tenant quotas
- **[AI Gateway](./ai-gateway.md)** — LLM integrations, prompt templates, model selection
- **[Agentic Workflows](./agentic-workflows.md)** — Multi-step AI orchestration, function calling, tool use
- **[Security](./security.md)** — Authentication, encryption, compliance, audit trails
- **[Observability](./observability.md)** — Metrics, traces, logs, dashboards, debugging

## Key Concepts at a Glance

**Flow**: A sequence of instructions that processes a request. Think of it as a serverless function—it can return a response, call upstream services, transform data, or orchestrate multiple steps.

**API**: A route binding that maps incoming HTTP requests to a Flow. Define the path, method, and which Flow should handle it.

**Tenant**: A logically isolated customer or environment. Each tenant has its own APIs, configuration, cache, and rate limits. Perfect for SaaS.

**Instruction**: A single operation within a Flow—return a response, call HTTP upstream, rate limit, cache, validate auth, or run AI logic.

**Studio**: The visual configuration interface. Build APIs, flows, and logic without writing code. Uses an intuitive form-based builder.

**Sync**: The deployment mechanism. Push configuration from your local machine or CI/CD pipeline to running RAH instances. Zero downtime.

## What RAH Is Not

- **Not a simple reverse proxy.** RAH is intelligent—it makes decisions, transforms requests, and orchestrates workflows.
- **Not limited to REST.** While REST is a first-class citizen, RAH handles gRPC, WebSockets, and custom protocols.
- **Not a full application server.** Use RAH at the edge or as a gateway; pair it with your backend services.
- **Not a message queue.** RAH is request-driven and stateless (though it can integrate with event pipelines).

## Quick Stats

- **Latency**: <5µs per request (no upstream)
- **Throughput**: Millions of requests/sec per instance
- **Memory**: Minimal footprint per tenant, zero garbage collection pauses
- **Tenants**: Unlimited isolation and scaling
- **Storage**: Built-in multi-backend support (Redis, PostgreSQL, MongoDB, disk, more)

## Support & Contributions

For questions, issues, or contributions, visit the project repository or contact the RAH team.

---

**Next Step**: [Get RAH running in 10 minutes →](./getting-started.md)
