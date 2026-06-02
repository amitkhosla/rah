# Setting Up a Standalone Gateway

This guide walks you through running RAH as a single instance — ideal for development, testing, or small deployments that don't need distributed state synchronization.

## What is Standalone Mode?

In standalone mode, RAH runs as a single process with:
- **Disk-based state storage** — All API definitions, flows, and tenant configuration stored on local disk
- **In-memory cache** — Response caching for improved performance within the instance
- **Per-instance rate limiting** — Rate limits enforced locally (not synchronized across instances)
- **Minimal external dependencies** — No Redis, PostgreSQL, or other services required

Standalone mode is perfect for:
- **Development and testing** — Rapid iteration without infrastructure overhead
- **Small teams** — Proof-of-concept deployments and internal APIs
- **Low-traffic APIs** — APIs with modest request volumes
- **Single-instance deployments** — When you need only one gateway running

When to upgrade to distributed mode:
- You're running multiple gateway instances behind a load balancer
- You need cache hits to work across instances
- You require persistent observability (access logs, traces)
- You need globally enforced rate limits

---

## Option A: Docker (Recommended)

Docker is the easiest way to get started. You'll run the gateway in a container with persistent data on your host machine.

### Prerequisites

- Docker Engine 20.10+
- Docker Compose (included with Docker Desktop)
- 512 MB available memory minimum

### Quick Start

Create a minimal `docker-compose.yml`:

```yaml
version: '3.8'
services:
  gateway:
    image: rahgateway/rah-gateway:latest
    ports:
      - "8080:8080"  # API traffic
      - "8081:8081"  # Management API
    volumes:
      - ./data:/data
      - ./gateway.yaml:/etc/rah/gateway.yaml
    environment:
      - RAH_CONFIG=/etc/rah/gateway.yaml
    restart: unless-stopped
```

Create a minimal `gateway.yaml`:

```yaml
port: 8080
management_port: 8081

datastore:
  stores:
    local:
      kind: disk
      path: /data
  bindings:
    api_definitions: local
    flows: local
    tenant_data: local
    cache: local
    rate_limit: local

observability:
  log_level: info
  access_log:
    enabled: true
    path: /data/access.log
```

Start the gateway:

```bash
docker-compose up -d
```

Verify it's running:

```bash
curl http://localhost:8081/health
# Expected: {"status":"ok"}
```

---

## Option B: Binary

If you prefer to run the gateway without Docker, you can build and run the binary directly.

### Prerequisites

- Go 1.22 or later
- Basic familiarity with command-line tools

### Build from Source

Clone the RAH repository and build:

```bash
git clone https://github.com/rahgateway/rah.git
cd rah
go build -o bin/rah-gateway ./cmd/rah-gateway/
```

### Run the Gateway

Create a config file `gateway.yaml`:

```yaml
port: 8080
management_port: 8081

datastore:
  stores:
    local:
      kind: disk
      path: ./data
  bindings:
    api_definitions: local
    flows: local
    tenant_data: local
    cache: local
    rate_limit: local

observability:
  log_level: info
  access_log:
    enabled: true
    path: ./data/access.log
```

Start the gateway:

```bash
./bin/rah-gateway -config gateway.yaml
```

You should see output like:

```
[INFO] RAH Gateway v1.0.0 started
[INFO] Listening on port 8080 (API traffic)
[INFO] Listening on port 8081 (Management API)
[INFO] Using disk datastore at ./data
```

Verify it's running:

```bash
curl http://localhost:8081/health
# Expected: {"status":"ok"}
```

---

## Minimal Config Explained

The config file has a few essential sections:

### Datastore Configuration

```yaml
datastore:
  stores:
    local:
      kind: disk
      path: ./data      # Directory to store all data
  bindings:
    api_definitions: local    # Where to store API definitions
    flows: local              # Where to store flow logic
    tenant_data: local        # Where to store tenant configs
    cache: local              # Where to store response cache
    rate_limit: local         # Where to store rate limit counters
```

All bindings point to the same `local` store, which is a disk-based store. Data is persisted to JSON files in the `./data` directory.

### Observability Configuration

```yaml
observability:
  log_level: info           # Log level: debug, info, warn, error
  access_log:
    enabled: true
    path: ./data/access.log # One access log entry per request
  traces:
    enabled: false          # Optional: detailed per-instruction timing
  metrics:
    enabled: false          # Optional: performance metrics
```

Access logs are written to a file and include: timestamp, method, path, status code, latency, tenant ID, errors.

---

## What's Stored Where

When you run RAH with a disk datastore, it creates files in the configured `path`:

```
./data/
├── store_api_definitions.json   # Your API routes (method, path, flow_name)
├── store_flows.json             # Your flow definitions (steps, logic)
├── store_tenant_data.json       # Tenant configuration (URLs, credentials, metadata)
├── store_cache.json             # Response cache (ephemeral, can be deleted)
├── store_rate_limit.json        # Rate limit counters (resets per window)
└── access.log                   # Access log (one line per request)
```

Each JSON file is a key-value store. You can inspect them manually (they're plain JSON) or manage them via the management API.

**Important:** The `cache` and `rate_limit` stores are ephemeral. If the gateway restarts, they're cleared. This is normal and expected.

---

## Setting Up Studio Alongside the Gateway

Studio is RAH's browser-based management UI. You can run it separately from the gateway.

### Run Studio with Docker

Create a `docker-compose.yml` that includes both gateway and studio:

```yaml
version: '3.8'
services:
  gateway:
    image: rahgateway/rah-gateway:latest
    ports:
      - "8080:8080"
      - "8081:8081"
    volumes:
      - ./data:/data
      - ./gateway.yaml:/etc/rah/gateway.yaml
    environment:
      - RAH_CONFIG=/etc/rah/gateway.yaml
    restart: unless-stopped

  studio:
    image: rahgateway/rah-studio:latest
    ports:
      - "8082:8082"
    environment:
      - RAH_GATEWAY_URL=http://gateway:8081
    depends_on:
      - gateway
    restart: unless-stopped
```

Start both:

```bash
docker-compose up -d
```

Access Studio:

```
http://localhost:8082
```

Studio connects to the gateway's management API (`http://gateway:8081`). From Studio, you can:
- Create and manage APIs
- Define flows
- Manage tenants and their configurations
- Deploy changes (sync)
- Monitor gateway health

---

## Quick Test: Deploy Your First API

Once the gateway is running, deploy a simple test API:

### Via Management API (curl)

```bash
curl -X POST http://localhost:8081/apis \
  -H "Content-Type: application/json" \
  -d '{
    "name": "hello",
    "path": "/hello",
    "method": "GET",
    "flow_name": "hello_flow"
  }'
```

### Via Studio UI

1. Open http://localhost:8082
2. Click **New API**
3. Fill in:
   - **Name:** `hello`
   - **Path:** `/hello`
   - **Method:** `GET`
   - **Flow:** Create new, name it `hello_flow`, add a `return(200, "Hello World!")` step
4. Click **Deploy**

### Test the API

```bash
curl http://localhost:8080/hello
# Expected: Hello World!
```

---

## Upgrading to Distributed Mode

When should you move to distributed mode?

You need distributed mode when you:

- **Run multiple gateway instances** — Multiple containers or servers behind a load balancer
- **Need shared cache** — Cache hits must work across instances (requires Redis)
- **Want global rate limiting** — Rate limits enforced across all instances
- **Need persistent observability** — Access logs and traces stored permanently
- **Require high availability** — Instances can be restarted without data loss

Distributed mode uses external services (Redis, PostgreSQL) to share state across instances. See [Setting Up a Distributed Gateway](setup-distributed.md) for details.

---

## Troubleshooting

### Gateway won't start

**Problem:** `Error: bind: address already in use`

**Solution:** Port 8080 or 8081 is already in use. Change the ports in your config:

```yaml
port: 8090             # Changed from 8080
management_port: 8091  # Changed from 8081
```

Or stop the process using the port:

```bash
lsof -i :8080  # Find process using port 8080
kill -9 <PID>  # Kill it
```

### Data directory permission denied

**Problem:** `Error: permission denied opening /data`

**Solution:** Make sure the directory exists and is writable:

```bash
mkdir -p ./data
chmod 755 ./data
```

### Can't access Studio from browser

**Problem:** `http://localhost:8082` gives "Connection refused"

**Solution:** Check that the studio container is running:

```bash
docker-compose ps
# Should show "studio" container with "Up" status
```

If it's not running, check logs:

```bash
docker-compose logs studio
```

---

## Next Steps

- **Learn the concepts:** [Key Concepts](./concepts.md)
- **Manage tenants:** [Tenancy and API Keys](./tenancy-and-api-keys.md)
- **Set up rate limiting:** [Rate Limiting Guide](./rate-limiting.md)
- **Use CI/CD:** [CI/CD Guide](./cicd-guide.md)
- **Scale to distributed:** [Distributed Gateway Setup](./setup-distributed.md)
