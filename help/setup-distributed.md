# Setting Up a Distributed Gateway

This guide covers running RAH in a distributed, production-ready configuration: multiple gateway instances, Redis for distributed caching and rate limiting, and PostgreSQL for persistent state.

## Why Distributed Mode?

Use distributed mode when you need:

- **Multiple gateway instances** — 2+ instances behind a load balancer for high availability and scaling
- **Shared cache** — Cache hits work regardless of which instance handles the request
- **Global rate limiting** — Rate limits enforced across all instances, not per-instance
- **Persistent observability** — Access logs, traces, and metrics stored permanently in a database
- **Cross-instance sync** — Configuration changes propagate automatically to all running instances
- **Zero-downtime deployments** — Add/remove instances without losing requests or state

---

## Architecture

```
[Clients]
    ↓
[Load Balancer]
  /   |   \
RAH RAH  RAH  ← Share config, cache, rate limits
 1   2   3
  \   |   /
    ↓ ↓ ↓
  [Redis]       ← Shared cache, rate limiting, sync
  [PostgreSQL]  ← Durable storage for APIs, tenants, observability
```

**Key components:**

1. **Load Balancer** — Routes incoming requests to gateway instances (nginx, HAProxy, AWS ALB, etc.)
2. **Gateway Instances** — Stateless execution engines (scale horizontally; add/remove at will)
3. **Redis** — Shared L2 cache, distributed rate limiting, cross-instance config sync
4. **PostgreSQL** — Persistent storage for API definitions, tenant data, access logs, traces, metrics

---

## Docker Compose Full Stack

The easiest way to run distributed mode locally or in production.

Create a `docker-compose.full.yml`:

```yaml
version: '3.8'

services:
  # Distributed RAH Gateway (2 instances)
  gateway1:
    image: rahgateway/rah-gateway:latest
    ports:
      - "8080:8080"
      - "8081:8081"
    volumes:
      - ./gateway-full.yaml:/etc/rah/gateway.yaml
    environment:
      - RAH_CONFIG=/etc/rah/gateway.yaml
      - RAH_INSTANCE_ID=gateway-1
    depends_on:
      - redis
      - postgres
    restart: unless-stopped

  gateway2:
    image: rahgateway/rah-gateway:latest
    ports:
      - "8090:8080"
      - "8091:8081"
    volumes:
      - ./gateway-full.yaml:/etc/rah/gateway.yaml
    environment:
      - RAH_CONFIG=/etc/rah/gateway.yaml
      - RAH_INSTANCE_ID=gateway-2
    depends_on:
      - redis
      - postgres
    restart: unless-stopped

  # Studio (browser-based management UI)
  studio:
    image: rahgateway/rah-studio:latest
    ports:
      - "8082:8082"
    environment:
      - RAH_GATEWAY_URL=http://gateway1:8081
    depends_on:
      - gateway1
    restart: unless-stopped

  # Redis (shared cache, rate limiting, config sync)
  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    volumes:
      - redis-data:/data
    command: redis-server --appendonly yes
    restart: unless-stopped

  # PostgreSQL (persistent storage)
  postgres:
    image: postgres:16-alpine
    ports:
      - "5432:5432"
    environment:
      POSTGRES_DB: rah
      POSTGRES_USER: rah
      POSTGRES_PASSWORD: rahsecret123
    volumes:
      - postgres-data:/var/lib/postgresql/data
    restart: unless-stopped

volumes:
  redis-data:
  postgres-data:
```

Start the full stack:

```bash
docker-compose -f docker-compose.full.yml up -d
```

Verify everything is running:

```bash
docker-compose -f docker-compose.full.yml ps
# Should show 5 services: gateway1, gateway2, studio, redis, postgres (all "Up")

# Test gateways
curl http://localhost:8080/health
curl http://localhost:8090/health
# Both should return: {"status":"ok"}

# Access Studio
open http://localhost:8082
```

---

## Full Configuration File (gateway-full.yaml)

Create a `gateway-full.yaml` with Redis and PostgreSQL backends:

```yaml
# Gateway API server
port: 8080
management_port: 8081

# Datastore configuration
datastore:
  stores:
    # Redis: for cache, rate limiting, config sync
    redis:
      kind: redis
      address: redis:6379
      db: 0
      
    # PostgreSQL: for persistent API definitions, tenant data, observability
    postgres:
      kind: postgresql
      host: postgres
      port: 5432
      database: rah
      username: rah
      password: rahsecret123
      ssl_mode: disable
      
  bindings:
    # API definitions and flows stored in PostgreSQL
    api_definitions: postgres
    flows: postgres
    
    # Tenant data stored in PostgreSQL
    tenant_data: postgres
    
    # Cache in Redis (shared across all gateway instances)
    cache: redis
    
    # Rate limiting in Redis (distributed across instances)
    rate_limit: redis
    
    # Observability stored in PostgreSQL
    obs_access_log: postgres
    obs_traces: postgres
    obs_metrics: postgres
    
    # Config sync via Redis Streams (cross-instance notifications)
    rate_limit_sync: redis

# Ingest pipeline: forward events to Redis Streams for real-time monitoring
ingest:
  enabled: true
  sinks:
    - kind: redis_stream
      address: redis:6379
      stream: rah:events          # All events: access logs, errors, etc.
    
    - kind: redis_stream
      address: redis:6379
      stream: rah:config-sync     # Config change notifications

# Observability configuration
observability:
  log_level: info
  
  # Access logs: one entry per API request
  access_log:
    enabled: true
    buffer_size: 1000
    flush_interval: 5s
  
  # Traces: detailed timing per instruction
  traces:
    enabled: true
    sample_rate: 0.05          # Sample 5% of requests for detailed traces
    instruction_timing: true   # Time each instruction
  
  # Metrics: request counts, latency percentiles, error rates
  metrics:
    enabled: true
    windows: [1m, 5m, 1h]      # Time windows for aggregation
  
  # Export metrics to Prometheus
  export:
    prometheus:
      enabled: true
      listen: ":9090"

# Cache configuration
cache:
  enabled: true
  max_entries: 10000
  ttl: 3600s                    # Default 1-hour cache TTL
```

---

## Supported Datastore Backends

RAH supports multiple backends for different components. Choose based on your needs:

| Backend | Kind | Best For | Notes |
|---------|------|----------|-------|
| **Disk** | `disk` | Development, single-instance | Local filesystem, no networking |
| **Redis** | `redis` | Cache, rate limiting, sync | Fast, in-memory, supports clustering |
| **Dragonfly** | `dragonfly` | Redis alternative | Higher throughput, Redis protocol |
| **PostgreSQL** | `postgresql` | Durable API definitions, observability | ACID guarantees, complex queries |
| **MongoDB** | `mongodb` | Document-oriented tenant data | Flexible schema, horizontal scaling |
| **Cassandra** | `cassandra` | Massive scale, high availability | Fault-tolerant, distributed |

**Common configurations:**

1. **Small team (3-5 APIs):** Redis cache + PostgreSQL storage
2. **Medium (50+ APIs):** Redis cache + PostgreSQL storage + Prometheus metrics
3. **Enterprise:** Dragonfly cache + PostgreSQL storage + Cassandra for massive scale

---

## Cross-Instance Config Sync

When you deploy API definitions, tenant updates, or rate limit changes, all running instances are notified automatically via Redis Streams.

**How it works:**

1. **Change via Studio or API:** You create a new API or update a tenant config
2. **Persisted to PostgreSQL:** The change is stored durably
3. **Event in Redis Stream:** A notification is published to `rah:config-sync`
4. **All instances notified:** Every running gateway reads the stream and reloads the updated config
5. **Zero-downtime:** No restart needed; instances reload in-memory tables within milliseconds

**Example:**

You're running 3 gateway instances. You update a tenant's upstream URL in Studio:

```
Time 1: Studio sends: PUT /tenants/acme-corp/upstream_url
Time 2: PostgreSQL updated ✓
Time 3: Redis stream event published ✓
Time 4: Gateway 1 reloads ✓
Time 5: Gateway 2 reloads ✓
Time 6: Gateway 3 reloads ✓
Total: ~50-100ms, zero requests dropped
```

All subsequent requests use the new URL, regardless of which instance handles them.

---

## Distributed Rate Limiting

In distributed mode, rate limits are global across all instances.

**Example scenario:**

You set a rate limit: "acme-corp: 1000 requests/minute"

- **Request 1** hits Gateway 1 → Counter incremented in Redis
- **Request 2** hits Gateway 2 → Reads counter from Redis → Counter incremented
- **Request 1001** hits Gateway 3 → Counter check fails → Returns 429 (Too Many Requests)

All instances share the same counter in Redis, so the limit is enforced globally, not per-instance.

**Configuration:**

```yaml
datastore:
  bindings:
    rate_limit: redis           # Rate limits in Redis
    rate_limit_sync: redis      # Sync notifications via Redis
```

---

## Scaling Horizontally

Adding more gateway instances is simple:

**Add a third instance:**

```yaml
gateway3:
  image: rahgateway/rah-gateway:latest
  ports:
    - "8100:8080"
    - "8101:8081"
  volumes:
    - ./gateway-full.yaml:/etc/rah/gateway.yaml
  environment:
    - RAH_CONFIG=/etc/rah/gateway.yaml
    - RAH_INSTANCE_ID=gateway-3
  depends_on:
    - redis
    - postgres
```

**Behind a load balancer:**

Configure your load balancer (nginx, HAProxy, AWS ALB) to route traffic to all instances:

```nginx
upstream rah {
    server gateway1:8080;
    server gateway2:8080;
    server gateway3:8080;
}

server {
    listen 80;
    server_name _;
    
    location / {
        proxy_pass http://rah;
        proxy_set_header Host $host;
    }
}
```

That's it. The new instance automatically connects to Redis and PostgreSQL, loads the current config, and starts handling requests.

---

## Dragonfly as Redis Alternative

If you need higher throughput than Redis, consider Dragonfly (Redis-compatible, better performance).

**Swap Redis for Dragonfly:**

In `docker-compose.full.yml`:

```yaml
# Replace the Redis service
redis:
  image: dragonflydb/dragonfly:latest
  ports:
    - "6379:6379"
  volumes:
    - redis-data:/data
```

In `gateway-full.yaml`:

```yaml
datastore:
  stores:
    dragonfly:
      kind: dragonfly           # Changed from "redis"
      address: redis:6379       # Same address
```

No other changes needed. Dragonfly speaks the Redis protocol, so it's a drop-in replacement.

---

## Monitoring and Observability

### Access Logs

Every API request is logged to PostgreSQL:

```sql
SELECT timestamp, method, path, status_code, latency_ms, tenant_id
FROM access_logs
WHERE timestamp > now() - interval '1 hour'
ORDER BY timestamp DESC
LIMIT 100;
```

### Traces

Detailed timing per instruction (5% of requests sampled):

```sql
SELECT instruction_name, timing_us, flow_name
FROM instruction_traces
WHERE timestamp > now() - interval '1 hour'
ORDER BY timing_us DESC
LIMIT 50;
```

### Metrics (Prometheus)

Export metrics to Prometheus for graphing and alerting:

```
http://localhost:9090/metrics
```

Common metrics:
- `rah_requests_total` — Total requests by status code
- `rah_request_latency_seconds` — Request latency (p50, p95, p99)
- `rah_cache_hits` — Cache hit rate
- `rah_rate_limit_rejections` — Requests rejected by rate limits

---

## Production Checklist

Before deploying to production:

- [ ] **SSL/TLS** — Configure HTTPS between client and gateway (load balancer terminates SSL)
- [ ] **Authentication** — Protect the management API (8081 port) with authentication
- [ ] **PostgreSQL backup** — Set up daily backups of the PostgreSQL database
- [ ] **Redis persistence** — Enable RDB or AOF for Redis durability
- [ ] **Monitoring** — Set up alerts on Prometheus metrics (latency, errors, cache hit rate)
- [ ] **Load balancer health checks** — Configure health checks hitting `/health` endpoint
- [ ] **Scaling policy** — Define when to add/remove instances (e.g., CPU > 70%)
- [ ] **Disaster recovery** — Test restoring from PostgreSQL backups
- [ ] **Log retention** — Configure PostgreSQL to rotate/archive old access logs
- [ ] **Security** — Use strong passwords, restrict database access, enable Redis AUTH

---

## Troubleshooting

### Instances not syncing config

**Problem:** You deploy a change to one instance, but the others don't pick it up.

**Solution:** Check Redis Streams:

```bash
redis-cli
> XLEN rah:config-sync
# Should show a count > 0
```

If the stream is empty, check that `ingest` is enabled in your config and `rate_limit_sync: redis` is bound.

### High latency to PostgreSQL

**Problem:** Gateway responses are slow.

**Solution:** PostgreSQL is remote or overloaded. Check:

1. Network latency: `ping postgres-host`
2. Database load: `SELECT count(*) FROM pg_stat_activity`
3. Move PostgreSQL closer (same VPC/AZ) or add read replicas

### Redis memory full

**Problem:** `Error: OOM command not allowed when used memory > maxmemory`

**Solution:** Redis memory limit reached. Either:

1. Increase Redis memory allocation
2. Reduce cache size in gateway config: `cache.max_entries: 5000` (was 10000)
3. Lower cache TTL: `cache.ttl: 1800s` (was 3600s)

### Dragonfly connection refused

**Problem:** Gateway can't connect to Dragonfly.

**Solution:** Ensure Dragonfly is listening on the right port:

```bash
docker-compose logs dragonfly | grep "listening"
# Should show something like "listening on port 6379"
```

---

## Next Steps

- **Learn concepts:** [Key Concepts](./concepts.md)
- **Manage APIs:** [Studio Guide](./studio-guide.md)
- **Set up CI/CD:** [CI/CD Guide](./cicd-guide.md)
- **Monitor performance:** [Observability Guide](./observability.md) (if available)
