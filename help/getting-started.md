# Getting Started with RAH

Get your first API running in less than 10 minutes. This guide walks you through two approaches: using Docker (easiest) or YAML files (for developers).

---

## Prerequisites

### Option A: Docker (Recommended)
- Docker and Docker Compose installed ([get them here](https://www.docker.com/products/docker-desktop))
- A terminal or command prompt
- A web browser

### Option B: YAML + Sync Utility
- Go 1.22+ ([install here](https://golang.org/doc/install)) OR access to a RAH instance already running
- A text editor (VS Code, vim, nano, etc.)
- A terminal
- Optional: git for version control

---

## Option A: Docker (5 Minutes)

### Step 1: Start RAH with Docker

The easiest way is to use the official Docker Compose configuration. Clone or download the RAH repository, then run:

```bash
docker compose up -d
```

This starts three services:
- **Gateway** (port 8080) — The API endpoint that receives incoming requests
- **Management API** (port 8081) — The control plane for deploying APIs and managing configuration
- **Studio** (port 8082) — The web UI for visual API configuration

The stack also includes Redis and PostgreSQL for data persistence.

Wait a moment for all services to start. You'll see logs confirming each service is listening.

### Step 2: Open Studio

Open your browser to **http://localhost:8082**

You'll see the Studio dashboard (no login required by default).

**Note**: If you prefer to use curl instead of the UI, skip to Option B below.

### Step 3: Create Your First API in 3 Steps

**Step 3.1: Use the Studio UI (Recommended)**

In the Studio dashboard:
1. Click **"Create API"** (or navigate to the APIs section)
2. Fill in:
   - **Name:** `hello-world`
   - **Path:** `/hello`
   - **Method:** `GET`
3. Click **"Create"**

A flow has been created automatically. Now add a step:
1. Click **"Add Step"** in the flow builder
2. Select **"Return Response"** (or **"Static Response"**)
3. Set:
   - **Status Code:** `200`
   - **Body:** `{"message": "Hello, World!"}`
4. Click **"Save"** and then **"Deploy"**

**Step 3.2: Or use curl to the Management API**

If you prefer the command line, create a file `hello-api.yaml`:

```yaml
flows:
  - name: hello_world
    code: |
      return(200, "Hello, World!")

apis:
  - name: hello-api
    path: /hello
    method: GET
    flow_name: hello_world
```

Then sync it:
```bash
curl -X POST http://localhost:8081/sync \
  -H "Content-Type: application/json" \
  -d @hello-api.yaml
```

**Step 3.3: Test Your API**

Open a terminal and run:

```bash
curl http://localhost:8080/hello
```

You should see:
```
Hello, World!
```

**Congratulations!** Your first API is live.

---

## What are APIs and Flows?

In RAH, an **API** is a route binding (path + method) that receives HTTP requests. A **Flow** is a sequence of instructions that processes that request. Think of a Flow like a serverless function—it can return responses, call upstream services, transform data, or run AI logic.

For your first API, you used a simple Flow with a single "Return Response" instruction. Next, you can add more instructions to build complex workflows.

---

## Next Steps

Now that you have a working gateway, explore:

- **[Concepts Guide](./concepts.md)** — Learn about Flows, Instructions, Tenants, and Caching
- **[DSL Guide](./dsl-guide.md)** — Write complex flows in YAML with conditionals, loops, and transformations
- **[Studio Guide](./studio-guide.md)** — Master the visual builder for APIs and multi-tenant routing
- **[Rate Limiting Guide](./rate-limiting.md)** — Protect your backend with token bucket or fixed-window limits
- **[Tenancy & API Keys](./tenancy-and-api-keys.md)** — Support multiple customers with isolated configurations
- **[Security Guide](./security.md)** — Add authentication (JWT, API keys) and encryption
- **[AI Gateway](./ai-gateway.md)** — Integrate LLMs (Claude, GPT, Gemini) for intelligent routing and responses

---

## Option B: Command-Line with YAML and curl

If you prefer not to use the Studio UI, you can deploy APIs directly via YAML and curl.

### Step 1: Create a YAML Configuration

Create a file named `apis.yaml`:

```yaml
flows:
  - name: hello_world
    code: |
      return(200, "Hello, World!")

apis:
  - name: hello-api
    path: /hello
    method: GET
    flow_name: hello_world
```

### Step 2: Sync to RAH

Use curl to deploy:

```bash
curl -X POST http://localhost:8081/sync \
  -H "Content-Type: application/json" \
  -d @apis.yaml
```

RAH will respond with a success message.

### Step 3: Test

```bash
curl http://localhost:8080/hello
```

Output:
```
Hello, World!
```

Every time you update `apis.yaml`, just re-run the curl command. RAH hot-swaps configuration with zero downtime.

---

## Troubleshooting

### "Connection refused" when testing
- Ensure Docker services are running: `docker compose ps`
- Verify you're using the correct ports: Gateway is 8080, Management API is 8081, Studio is 8082

### "404 Not Found" from the gateway
- Check the path is correct (e.g., if you created `/hello`, test `/hello`, not `/hello-api`)
- Verify the API was deployed successfully (check Studio or sync response)

### Can't access Studio at localhost:8082
- Make sure all containers are running: `docker compose logs studio`
- Try refreshing the browser, or clear cache

### Need help?
- Review the [Concepts Guide](./concepts.md) for detailed explanations
- Check Docker logs: `docker compose logs -f`
- Read the [DSL Guide](./dsl-guide.md) for more complex examples

---

**Congratulations!** You now have RAH running. Pick a next step below or explore the guides at your own pace.
