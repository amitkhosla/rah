# Multi-Tenancy and API Key Management

## What is Multi-Tenancy in RAH

RAH has native multi-tenancy built into every layer. Each tenant (customer account) is identified by one or more aliases and gets automatic isolation of:

- **Service URLs** — Each tenant's own upstream endpoints (primary, fallback, etc.)
- **Credentials and Identifiers** — Stored API keys, client IDs, passwords (hashed and encrypted)
- **Metadata** — Tier, region, plan, custom attributes (used for conditional logic)
- **Cache Namespace** — Tenant-specific cache is completely isolated; no data leakage
- **Rate Limit Overrides** — Per-tenant throttling adjustments
- **Observability** — Traces and logs automatically tagged by tenant for easy filtering

Multi-tenancy is not a feature you need to opt into—it's the foundation of RAH's architecture. Every request, every cache operation, every datastore access is tenant-aware.

---

## How Tenants Work

A **tenant** is a logical customer account or organizational boundary.

Each tenant has one or more **aliases** — user-friendly identifiers that map to the tenant:
- Hostnames (e.g., `acme.example.com`)
- UUIDs (e.g., `550e8400-e29b-41d4-a716-446655440000`)
- Subdomains (e.g., `acme`)
- Custom strings (e.g., `acme-corp`, `customer-123`)
- Account IDs or org IDs

At **request time**, a `registry.lookup()` instruction maps any alias to an internal tenant ID. Everything downstream — cache reads/writes, datastore operations, rate limiting, logging — automatically uses this tenant context. No explicit tenant passing required.

**Example flow**:
```
incoming request with header X-Tenant-ID: acme-corp
  ↓
registry.lookup(header("X-Tenant-ID"))
  ↓
tenant ID resolved → all downstream operations use this tenant ID
  ↓
cache.get("session-token") → fetched from acme-corp's cache partition
rate_limit_v2() → counted against acme-corp's limits
upstream call → uses acme-corp's service URL and credentials
```

---

## Tenant Data Model

Each tenant contains three categories of data:

```
Tenant "acme-corp"
├── Aliases: ["acme-corp", "acme.example.com", "550e8400-e29b-41d4-a716"]
├── Service URLs:
│   ├── primary   → https://api.acme-corp.internal
│   └── fallback  → https://backup.acme-corp.internal
├── Identifiers:
│   ├── api_key   → [sha256 hash]
│   └── client_id → "acme_prod_client"
└── Metadata:
    ├── tier      → "enterprise"
    ├── region    → "us-east"
    └── plan      → "unlimited"
```

### Service URLs
Store upstream endpoint addresses per tenant. Common patterns:
- `primary` / `fallback` — for redundancy
- `v1` / `v2` — for version routing
- `read` / `write` — for database separation
- Any custom key name your flows need

### Identifiers
Store tenant-specific credentials and API keys used in flows:
- `api_key` — upstream authentication token
- `client_id` — OAuth2 client identifier
- `client_secret` — OAuth2 secret
- `db_password` — database credentials
- Any custom identifier your flows reference

Identifiers are hashed at rest (SHA-256) and never returned via API — only referenced by key name in flows.

### Metadata
Free-form key-value data for conditional logic:
- `tier` — billing tier (free, pro, enterprise)
- `region` — geographic region
- `plan` — feature plan
- `max_requests` — custom quotas
- Any business attribute your flows check

---

## Creating Tenants

### Via Studio UI

1. Open **Studio** → **Tenants**
2. Click **New Tenant**
3. Enter a primary **alias** (e.g., `acme-corp`)
4. Under **Service URLs**, add your upstream endpoints:
   - Key: `primary`, Value: `https://api.acme.internal`
   - Key: `fallback`, Value: `https://backup.acme.internal` (optional)
5. Under **Identifiers**, add your credentials:
   - Key: `api_key`, Value: `acme-secret-key-123`
   - Key: `client_id`, Value: `acme_prod`
6. Under **Metadata**, add any business attributes:
   - Key: `tier`, Value: `enterprise`
   - Key: `region`, Value: `us-east`
7. Click **Save**

### Via REST API

```bash
curl -X POST http://localhost:8081/tenants \
  -H "Content-Type: application/json" \
  -d '{
    "alias": "acme-corp",
    "urls": {
      "primary": "https://api.acme.internal",
      "fallback": "https://backup.acme.internal"
    },
    "ids": {
      "api_key": "acme-secret-key-123",
      "client_id": "acme_prod"
    },
    "meta": {
      "tier": "enterprise",
      "region": "us-east",
      "plan": "unlimited"
    }
  }'
```

**Response:**
```json
{
  "id": 42,
  "alias": "acme-corp",
  "urls": { ... },
  "ids": { ... },
  "meta": { ... }
}
```

---

## Tenant Identification in a Flow

Use the `registry.lookup()` instruction to identify a tenant from an incoming request. Once identified, use subsequent `registry.url()`, `registry.id()`, and `registry.meta()` instructions to access tenant data.

### Common Tenant Identification Patterns

**From an HTTP header:**
```yaml
flow_name: api_flow
steps:
  - kind: registry.lookup
    source: header
    header_name: X-Tenant-ID
    
  - kind: http.get
    url_from_slot: true
    url_slot: 0  # or slot containing registry.url("primary")
```

**From a JWT claim (extract claim first):**
```yaml
flow_name: api_flow
steps:
  - kind: validate_token
    token_header: Authorization
    jwks_url: https://auth0.com/.well-known/jwks.json
    subject_var: user_sub
    
  - kind: registry.lookup
    source: variable
    var: user_sub
```

**From a subdomain:**
```yaml
flow_name: api_flow
steps:
  - kind: registry.lookup
    source: header
    header_name: Host
    # will match any registered alias that matches the hostname
```

**From a path segment:**
```yaml
flow_name: api_flow
steps:
  - kind: registry.lookup
    source: path
    segment: 0  # /acme-corp/... → looks up "acme-corp"
```

### Accessing Tenant Data

Once `registry.lookup()` succeeds, use:

- `registry.url(key)` — Retrieve a service URL (e.g., `registry.url("primary")`)
- `registry.id(key)` — Retrieve an identifier (e.g., `registry.id("api_key")`)
- `registry.meta(key)` — Retrieve metadata (e.g., `registry.meta("tier")`)

**Example flow:**
```yaml
flow_name: api_flow
steps:
  - kind: registry.lookup
    source: header
    header_name: X-Tenant-ID
  
  - kind: registry.url
    key: primary
    var: upstream_url
  
  - kind: registry.id
    key: api_key
    var: upstream_key
  
  - kind: registry.meta
    key: tier
    var: customer_tier
  
  - kind: http.get
    url_var: upstream_url
    headers:
      Authorization: "Bearer #{upstream_key}"
```

If lookup fails (tenant not found), the request is rejected with a 404.

---

## Managing Aliases

Each tenant can have multiple aliases for flexibility. For example, a customer might be identified by:
- Their company name: `acme-corp`
- Their domain: `acme.example.com`
- Their tenant UUID: `550e8400-e29b-41d4-a716-446655440000`

### Adding an Alias

```bash
curl -X POST http://localhost:8081/tenants/acme-corp/aliases \
  -H "Content-Type: application/json" \
  -d '{"alias": "acme.example.com"}'
```

Now both `acme-corp` and `acme.example.com` resolve to the same tenant.

### Listing Aliases

```bash
curl http://localhost:8081/tenants/acme-corp
```

Response includes all aliases for the tenant:
```json
{
  "id": 42,
  "aliases": ["acme-corp", "acme.example.com"],
  "urls": { ... },
  "ids": { ... },
  "meta": { ... }
}
```

---

## API Key Management (Native RAH API Keys)

RAH provides a built-in API key system for authenticating client applications. This is separate from tenant-specific credentials stored in the registry.

### How API Keys Work

1. **Creation** — You create an API key with a secret value
2. **Storage** — RAH hashes the key (SHA-256) and stores only the hash
3. **Validation** — At request time, `validate_api_key()` hashes the incoming key and compares it
4. **Scoping** — Each key is linked to an **App** and can be restricted to specific tenants
5. **Context** — When valid, sets `CallerID` and `CallerKey` context variables

API keys are never returned via API after creation — store them securely on your client.

### Creating an API Key

```bash
curl -X POST http://localhost:8081/api/keys \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-mobile-app",
    "key": "my-secret-api-key-xyz",
    "allowed_tenants": ["acme-corp", "beta-corp"]
  }'
```

**Response:**
```json
{
  "id": "key_abc123def456",
  "name": "my-mobile-app",
  "hash": "[sha256-hash]",
  "allowed_tenants": ["acme-corp", "beta-corp"],
  "created_at": "2026-06-02T14:30:00Z"
}
```

**Note:** The actual `key` value is NOT returned. Save it securely on your client immediately — we never store or display it again.

### Validating an API Key in a Flow

```yaml
flow_name: api_flow
steps:
  - kind: validate_api_key
    header_name: X-API-Key
    on_failure: stop
  
  # After validation:
  # - CallerID = app name (e.g., "my-mobile-app")
  # - CallerKey = key ID (e.g., "key_abc123def456")
  
  - kind: registry.lookup
    source: header
    header_name: X-Tenant-ID
  
  - kind: http.get
    url_var: upstream_url
```

If validation fails (invalid or missing key), the request is rejected with a 401 (Unauthorized).

### Listing API Keys

```bash
curl http://localhost:8081/api/keys
```

Response:
```json
[
  {
    "id": "key_abc123def456",
    "name": "my-mobile-app",
    "allowed_tenants": ["acme-corp", "beta-corp"],
    "created_at": "2026-06-02T14:30:00Z"
  }
]
```

**Note:** The actual key hash is never returned.

### Deleting an API Key

```bash
curl -X DELETE http://localhost:8081/api/keys/key_abc123def456
```

All existing requests using this key will fail after deletion. No grace period.

---

## JWT / OAuth Token Validation

Validate JWT tokens from your authorization provider (Auth0, Okta, AWS Cognito, etc.).

### Basic JWT Validation

```yaml
flow_name: api_flow
steps:
  - kind: validate_token
    token_header: Authorization
    jwks_url: https://auth0.com/.well-known/jwks.json
    alg: RS256
    issuer: https://auth0.com/
    audience: my-api
    on_failure: stop
    subject_var: user_id
    claims_var: token_claims
```

**Parameters:**
- `token_header` — HTTP header containing the token (typically `Authorization`)
- `jwks_url` — URL to fetch public keys (auto-cached and refreshed)
- `alg` — Expected algorithm (RS256, ES256, PS256, HS256, etc.)
- `issuer` — Expected token issuer
- `audience` — Expected audience claim
- `on_failure` — `stop` (reject with 401), `continue` (proceed anyway)
- `subject_var` — Variable to store the `sub` claim (user ID)
- `claims_var` — Variable to store all claims as JSON

### Token Format

Expect the `Authorization` header in standard Bearer format:
```
Authorization: Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9...
```

RAH automatically extracts and validates the token.

### Supported Algorithms

- **RS256** — RSA with SHA-256 (recommended for public JWKS)
- **ES256** — ECDSA with SHA-256
- **PS256** — RSA PSS with SHA-256
- **HS256** — HMAC with SHA-256 (for symmetric keys)
- And others (ES384, PS384, etc.)

### Caching JWKS Keys

JWKS keys are automatically cached in memory and refreshed when needed. To manually flush the cache:

```bash
curl -X POST "http://localhost:8081/admin/jwks/flush?issuer=https://auth0.com/"
```

---

## DPoP Proof Validation (Advanced)

Demonstrating Proof-of-Possession (RFC 9449) binds an access token to a specific client's public key, preventing token theft if intercepted.

Use DPoP validation after `validate_token()`:

```yaml
flow_name: api_flow
steps:
  - kind: validate_token
    token_header: Authorization
    jwks_url: https://auth0.com/.well-known/jwks.json
    subject_var: user_id
  
  - kind: validate_dpop
    access_token_var: token_var      # the token from validate_token
    max_age: "60"                     # max age of proof in seconds
    on_failure: stop
```

**How it works:**
1. Client creates a DPoP proof JWT signed with their private key
2. Client sends proof in `DPoP` header along with token in `Authorization`
3. RAH validates the proof signature and binds it to the token
4. If token is later stolen and used without the original client's proof, validation fails

---

## Token Introspection (for Opaque Tokens)

If your tokens are opaque (not JWTs), use introspection to validate them with your authorization server.

```yaml
flow_name: api_flow
steps:
  - kind: validate_introspection
    url: https://auth.example.com/oauth2/introspect
    token_header: Authorization
    client_id: my-gateway-client
    client_secret_ref: env://INTROSPECT_SECRET
    cache_ttl: "30"
    on_failure: stop
    active_var: token_active
    scope_var: token_scopes
```

**Parameters:**
- `url` — Introspection endpoint URL
- `token_header` — Header containing the token
- `client_id` — Gateway's client ID for introspection
- `client_secret_ref` — Reference to secret (e.g., `env://VAR_NAME`, `vault://secret/path`)
- `cache_ttl` — Cache valid tokens for this many seconds
- `on_failure` — `stop` (reject with 401), `continue`
- `active_var` — Variable to store `active` boolean
- `scope_var` — Variable to store token scopes

**Introspection response** (cached):
```json
{
  "active": true,
  "scope": "read write",
  "exp": 1234567890,
  "sub": "user123"
}
```

---

## Per-Tenant Rate Limit Overrides

Tenants can be given custom rate limit adjustments independent of their API configuration.

### Scale Limit Percentage

Give a tenant 50% more capacity than configured:

```bash
curl -X POST http://localhost:8081/tenants/premium-corp/rate-limits \
  -H "Content-Type: application/json" \
  -d '{"scale_pct": 50}'
```

If the API is configured for 1000 req/min, this tenant gets 1500 req/min.

### Block a Tenant

Reject all traffic from a tenant:

```bash
curl -X POST http://localhost:8081/tenants/bad-actor/rate-limits \
  -H "Content-Type: application/json" \
  -d '{"blocked": true}'
```

All requests return 429 (Too Many Requests).

### Disable Rate Limits

Allow unlimited traffic (for premium tier):

```bash
curl -X POST http://localhost:8081/tenants/enterprise-corp/rate-limits \
  -H "Content-Type: application/json" \
  -d '{"rl_disabled": true}'
```

---

## Tiers — Group Tenants into Rate Limit Policies

Instead of individual overrides, organize tenants into tiers with shared policies.

### Create a Tier

```bash
curl -X POST http://localhost:8081/tiers \
  -H "Content-Type: application/json" \
  -d '{
    "name": "enterprise",
    "rate_limit_config": "enterprise-limits"
  }'
```

### Assign Tenant to Tier

When creating or updating a tenant, set the `tier` metadata:

```bash
curl -X POST http://localhost:8081/tenants \
  -d '{
    "alias": "acme-corp",
    "meta": {
      "tier": "enterprise"
    }
  }'
```

### Use Tier in Flow

```yaml
flow_name: api_flow
steps:
  - kind: registry.lookup
    source: header
    header_name: X-Tenant-ID
  
  - kind: registry.meta
    key: tier
    var: customer_tier
  
  - kind: rate_limit_v2
    dynamic_config_var: customer_tier
    count_by: tenant
    # Maps to: free → free-limits, pro → pro-limits, enterprise → enterprise-limits
```

---

## Listing Tenants

### Paginated List

```bash
curl "http://localhost:8081/tenants?limit=50&cursor=0"
```

Response:
```json
{
  "tenants": [
    {
      "id": 1,
      "aliases": ["acme-corp", "acme.example.com"],
      "urls": { "primary": "..." },
      "ids": { ... },
      "meta": { "tier": "enterprise" }
    }
  ],
  "next_cursor": "50"
}
```

**Parameters:**
- `limit` — Number of results per page (default 10, max 100)
- `cursor` — Pagination token from previous response

Pagination is cursor-based and stable even if tenants are added/removed between requests.

### Get Specific Tenant

```bash
curl http://localhost:8081/tenants/acme-corp
```

or by ID:

```bash
curl http://localhost:8081/tenants/42
```

---

## Deleting a Tenant

```bash
curl -X DELETE http://localhost:8081/tenants/acme-corp
```

**Warning:** This removes all aliases, URLs, identifiers, and metadata for the tenant. This is permanent and cannot be undone.

After deletion:
- Cache partitions for this tenant are cleared
- Datastore entries (if any) remain but become inaccessible
- Existing requests using this tenant's context will fail

---

## Tenant Isolation Guarantees

RAH ensures complete isolation between tenants:

| Layer | Isolation | Notes |
|---|---|---|
| **Cache** | Separate namespace per tenant | No cross-tenant cache access possible |
| **Datastore** | Tenant ID in every KV operation | Redis/DB keys include tenant prefix |
| **Rate Limiting** | Separate counters per tenant | Tenant A's traffic doesn't affect tenant B's limits |
| **Observability** | Traces/logs tagged by tenant ID | Filter observability data by tenant |
| **Secrets** | Hashed at rest, no export | API never returns identifier values |
| **Metadata** | Tenant-specific attributes | Each tenant has independent metadata |

---

## Troubleshooting

### Tenant Lookup Fails (404)

- Check that the alias is registered: `curl http://localhost:8081/tenants/{alias}`
- Verify the incoming identifier (header/claim/path) matches a registered alias exactly
- Lookup is case-sensitive

### Identifier Not Found

- Confirm the identifier key exists: `curl http://localhost:8081/tenants/acme-corp`
- Check the key name matches exactly (e.g., `api_key` vs `apiKey`)
- Identifiers are hashed; you cannot view the actual value, only confirm existence

### Rate Limit Override Not Taking Effect

- Apply the override at the tenant level, not the API level
- Verify the tenant is identified correctly in your flow
- Restart the gateway for override changes to apply (or check if hot reload is enabled)

### API Key Validation Fails

- Confirm the key is registered: `curl http://localhost:8081/api/keys`
- Check that the incoming header name matches `token_header` in the validate step
- Ensure the secret value sent by the client is exactly as created (no truncation, encoding issues)
- Check that the client's tenant is in the key's `allowed_tenants` list

---

## Best Practices

1. **Use aliases wisely** — Pick identifiers that are stable and don't change (UUIDs preferred over email addresses)
2. **Never share API keys** — Each client app should get its own key
3. **Rotate credentials regularly** — Delete old keys/identifiers and create new ones
4. **Store secrets safely** — Use environment variables or secret management (Vault, AWS Secrets Manager) for client secrets
5. **Monitor failed lookups** — High 404s indicate misconfigured tenant identification
6. **Separate concerns** — Use different metadata keys for different purposes (tier, region, plan, etc.)
7. **Test tenant isolation** — Verify that tenant A cannot access tenant B's cache or data
