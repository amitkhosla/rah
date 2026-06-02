# RAH Security Features

RAH provides a multi-layered defense system that protects your APIs from network-level attacks to application-layer threats. This guide covers all security capabilities and production deployment patterns.

## Security Layers Overview

Every request flows through multiple security checkpoints before reaching your business logic:

```
[Request arrives]
    ↓
[IP Restriction / Geo Block]        ← Network layer
    ↓
[Body Size Limit / Bot Detection]   ← Request hygiene
    ↓
[OWASP Checks]                      ← Threat detection
    ↓
[Authentication (JWT/API Key)]      ← Identity
    ↓
[Rate Limiting]                     ← Abuse prevention
    ↓
[Your Business Logic]               ← Application
    ↓
[Security Headers on Response]      ← Defense in depth
```

Each layer is optional and independently configurable. Combine them to create a defense posture matched to your threat model.

---

## Authentication

Authentication proves that a request comes from a known identity. RAH supports multiple authentication mechanisms — you can use one or chain multiple together.

### JWT / OAuth 2.0 Validation

JWT validation is the primary method for OAuth 2.0 and OpenID Connect flows. RAH validates the token signature using JWKS (JSON Web Key Set) and enforces standard claims.

```yaml
steps:
  - name: validate_token
    kind: validate_token
    params:
      token_source: header.Authorization
      jwks_url: https://auth.mycompany.com/.well-known/jwks.json
      alg: RS256
      issuer: https://auth.mycompany.com/
      audience: my-api
      on_failure: stop
      claims_var: claims
      subject_var: user_id
```

**Supported algorithms**: RS256, RS384, RS512, ES256, ES384, ES512, PS256, HS256

**Key features**:
- JWKS automatically refreshed on expiry
- Token signature validated before claims are examined
- Standard claims checked: `iss` (issuer), `aud` (audience), `exp` (expiration)
- Custom claims extracted and available in request context
- Flush cached keys on issuer key rotation:
  ```bash
  curl -X POST http://localhost:8081/admin/jwks/flush
  ```

**On-failure modes**:
- `stop`: Request rejected immediately (401)
- `continue`: Sets an error flag in `claims_var` and continues processing (useful for guest access flows)

**Example: Guest fallback**
```yaml
validate_token(
  token_source: header.Authorization,
  jwks_url: https://auth.example.com/.well-known/jwks.json,
  on_failure: continue,
  claims_var: auth_result
)

# If auth failed, auth_result.error is set; if succeeded, auth_result.claims has the token claims
if auth_result.error {
  # unauthenticated user — guest mode
  tenant_id = "guest-tenant"
} else {
  # authenticated — extract tenant from token
  tenant_id = auth_result.claims["tenant_id"]
}
```

### Native RAH API Keys

API keys stored in RAH's tenant registry. Keys are hashed with SHA-256 — plaintext is never persisted.

```yaml
steps:
  - name: validate_api_key
    kind: validate_api_key
    params:
      token_source: header.X-API-Key
      on_failure: stop
```

**Key features**:
- Keys stored as SHA-256 hashes; only hashes persist to disk
- Sets `CallerID` and `CallerKey` in request context for audit
- Optional tenant restriction: a key can be locked to specific tenants
- Keys rotated via management API — old keys remain valid during rotation window

**Set an API key** (management API):
```bash
curl -X POST http://localhost:8081/api/v1/api-keys \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{
    "key_plaintext": "sk_prod_ABC123XYZ...",
    "tenant_id": "acme-corp",
    "expires_at": "2027-06-02T00:00:00Z",
    "metadata": {"service": "billing-integration"}
  }'
```

### Token Introspection (RFC 7662)

For opaque tokens (tokens you cannot inspect locally), use a remote introspection endpoint. Common in OAuth 2.0 deployments where you use an external authorization server.

```yaml
steps:
  - name: validate_introspection
    kind: validate_introspection
    params:
      url: https://auth.mycompany.com/oauth2/introspect
      token_source: header.Authorization
      client_id: gateway-client
      client_secret_ref: env://INTROSPECT_CLIENT_SECRET
      cache_ttl_sec: 60
      on_failure: stop
      claims_var: token_info
```

**Key features**:
- Introspection responses cached to reduce latency and load on auth server
- Cache TTL should match your token lifetime (typically 5–60 seconds)
- `active` field checked — inactive tokens rejected
- Custom claims from introspection response available in `claims_var`

### DPoP (RFC 9449) — Prevent Token Theft

DPoP (Demonstrating Proof-of-Possession) binds a token to a specific client's public key. If a token is stolen, it cannot be replayed by a different client.

```yaml
steps:
  - name: validate_token
    kind: validate_token
    params:
      token_source: header.Authorization
      jwks_url: https://auth.mycompany.com/.well-known/jwks.json
      on_failure: stop
      claims_var: token_claims
  
  - name: validate_dpop
    kind: validate_dpop
    params:
      dpop_header: DPoP
      access_token: token_claims.access_token
      max_age_sec: 60
```

DPoP is always used in combination with JWT validation. The DPoP proof (a signed JWT in the DPoP header) is validated and its public key compared to the `cnf` claim in the access token.

### JWT Revocation (Blocklist)

Even if a JWT is valid, you may want to revoke it before expiration (e.g., user logout, key compromise). Store revoked token IDs in a fast lookup store.

```yaml
steps:
  - name: validate_token
    kind: validate_token
    params:
      token_source: header.Authorization
      jwks_url: https://auth.mycompany.com/.well-known/jwks.json
      on_failure: stop
      claims_var: token_claims
  
  - name: check_revoked
    kind: check_token_revoked
    params:
      jti: token_claims.jti
      cache_key: revoked_tokens:{jti}
      on_revoked: stop
```

Revoked token IDs stored in RAH's cache with a TTL matching the original token's expiration. When a token expires naturally, the revocation entry is automatically cleaned up.

---

## Secret Management

Secrets are credentials needed by your flows: API keys for upstream services, encryption keys, credentials for datastores. **Secrets must never be hardcoded in flow definitions.**

### Secret Sources

RAH supports multiple secret sources. Reference them using the `load_secret()` function:

```yaml
# Environment variable
my_key = load_secret("env://MY_SECRET_KEY")

# Mounted file (for Kubernetes secrets, Docker secrets)
api_key = load_secret("file:///etc/secrets/api.key")

# Google Secret Manager
gcp_secret = load_secret("gcp://my-project/my-secret?version=latest")

# AWS Secrets Manager
aws_secret = load_secret("aws://my-secret-name")

# HashiCorp Vault
vault_secret = load_secret("vault://secret/api-key")
```

All secret sources are loaded at flow compile time, not request time. The loaded secret is cached in compiled flow memory.

### Secret Encryption at Rest

By default, secrets in RAH's datastore and cache are encrypted with AES-256-GCM using a master key.

Configure the master key in `gateway.yaml`:

```yaml
secrets:
  encrypted:
    kind: env
    env_var: RAH_MASTER_KEY        # must be 32 bytes, base64-encoded
    rotation_days: 90              # trigger alerts at 90 days
```

Generate a new master key:
```bash
openssl rand -base64 32
```

Rotate the master key without downtime:
1. Set `RAH_MASTER_KEY_OLD` to current key
2. Set `RAH_MASTER_KEY` to new key
3. RAH automatically re-encrypts on write
4. After 30 days, remove `RAH_MASTER_KEY_OLD`

---

## Network Security

### IP Restriction (CIDR)

Block or allow requests based on source IP address. Useful for internal-only APIs or blocking known bad actors.

```yaml
steps:
  - name: check_ip
    kind: ip_restriction
    params:
      mode: allow
      cidrs:
        - 10.0.0.0/8
        - 172.16.0.0/12
```

**Modes**:
- `allow`: Only IPs matching the list are allowed; all others rejected
- `deny`: IPs matching the list are rejected; all others allowed

The client's IP is extracted from `X-Forwarded-For` (if behind a proxy) or the TCP socket source address.

**Example: Internal only**
```yaml
ip_restriction(
  mode: allow,
  cidrs: ["10.0.0.0/8", "127.0.0.1/32"]
)
```

**Example: Block malicious subnet**
```yaml
ip_restriction(
  mode: deny,
  cidrs: ["203.0.113.0/24"]
)
```

### Geo Blocking (MaxMind GeoLite2)

Block or allow requests based on the client's country of origin. Requires MaxMind GeoLite2 database (free tier available).

```yaml
observability:
  geo_db:
    enabled: true
    db_path: /opt/geolite2/GeoLite2-Country.mmdb
    update_interval_days: 7

steps:
  - name: geo_check
    kind: geo_block
    params:
      mode: block_list
      countries:
        - CN
        - RU
```

**Modes**:
- `allow_list`: Only listed countries are allowed; all others blocked
- `block_list`: Listed countries are blocked; all others allowed

**Example: GDPR compliance (block EU)**
```yaml
geo_block(
  mode: block_list,
  countries: ["AT", "BE", "BG", "HR", "CY", "CZ", "DK", "EE", "FI", "FR",
              "DE", "GR", "HU", "IE", "IT", "LV", "LT", "LU", "MT", "NL",
              "PL", "PT", "RO", "SK", "SI", "ES", "SE"]
)
```

---

## Threat Protection

### OWASP Attack Detection

RAH detects and blocks common web attacks: SQL injection, XSS, command injection, path traversal. The detector uses pattern matching and heuristics.

```yaml
steps:
  - name: owasp_check
    kind: owasp_check
    params:
      mode: block        # or "tag"
```

**Modes**:
- `block`: Requests matching attack patterns are rejected (403)
- `tag`: Requests are allowed but tagged with `X-OWASP-Threat: true` header; you can then log, rate-limit, or handle them specially

**Detected attacks**:
- SQL injection: Common SQL keywords in unexpected places (`'; DROP`, `UNION SELECT`, etc.)
- XSS: Script tags and event handlers (`<script>`, `onclick=`, `javascript:`, etc.)
- Command injection: Shell metacharacters and keywords (`; ls`, `| cat`, etc.)
- Path traversal: Directory traversal attempts (`../../../etc/passwd`)
- LDAP injection: LDAP filter syntax in form fields

**Example: Log attacks but allow**
```yaml
owasp_check(mode: tag)
if header("X-OWASP-Threat") == "true" {
  log("warn", "OWASP threat detected from {client_ip}")
}
```

### Bot Detection

Detect and block bots, scrapers, and automated tools. Uses fingerprinting and header analysis.

```yaml
steps:
  - name: bot_check
    kind: detect_bot
    params:
      mode: block        # or "tag"
```

**Modes**:
- `block`: Bots rejected (403)
- `tag`: Bots allowed but tagged with `X-Bot-Score: <0-100>` header

**What gets detected**:
- Scrapers: Googlebot, scrapy, curl (no User-Agent)
- Headless browsers: Playwright, Selenium, Puppeteer
- Known attack tools: sqlmap, nikto, nmap
- Suspicious patterns: No JavaScript execution, missing typical browser headers

**Example: Allow crawlers but block attackers**
```yaml
detect_bot(mode: tag)

if header("X-Bot-Score") > 80 {
  # High bot score — likely scraper or attacker
  return status_code: 403
} else if header("X-Bot-Score") > 50 {
  # Medium score — likely a crawler; rate-limit it
  rate_limit_v2(window_sec: 60, limit: 10)
}
```

### Body Size Limit

Prevent large payload attacks (e.g., zip bomb, buffer overflow attempts) by rejecting requests with bodies exceeding a size limit.

```yaml
steps:
  - name: size_check
    kind: limit_body
    params:
      max: 1MB           # Returns 413 Payload Too Large if exceeded
```

**Unit** options: `B`, `KB`, `MB`, `GB`

**Example: Strict limit for login endpoint**
```yaml
limit_body(max: 10KB)    # Most login payloads are < 1KB
validate_token(...)
```

### Prompt Injection Detection (for AI flows)

When your flows call LLM APIs, detect and reject attempts to manipulate the model via injection attacks.

```yaml
steps:
  - name: sanitize_prompt
    kind: sanitize_prompt
    params:
      input: user_input
      mode: reject        # or "sanitize", "tag"
      rules:
        - injection
        - pii
        - jailbreak
```

**Modes**:
- `reject`: Requests with detected injection patterns are rejected (400)
- `sanitize`: Detected patterns are removed or masked
- `tag`: Allowed but tagged for logging

**Rules**:
- `injection`: Common prompt injection patterns (`Ignore instructions`, `Pretend you are`, etc.)
- `pii`: Credit cards, SSN, emails, phone numbers
- `jailbreak`: Known jailbreak patterns (`ignore safety guidelines`, `role play as evil`, etc.)

---

## Outbound Security (mTLS)

When your flows call upstream APIs, protect the connection with client certificates (mutual TLS).

```yaml
steps:
  - name: call_upstream
    kind: http
    params:
      method: GET
      url: https://upstream.example.com/api/data
      cert_ref: env://CLIENT_CERT
      key_ref: env://CLIENT_KEY
      ca_ref: env://CA_BUNDLE
      timeout_ms: 5000
```

**References**:
- `cert_ref`: Client certificate (PEM-encoded X.509)
- `key_ref`: Client private key (PEM-encoded)
- `ca_ref`: CA bundle for server certificate verification (optional; uses system default if not specified)

**Example: mTLS to internal service**
```yaml
http.get(
  url: https://internal-api:8443/secure,
  cert_ref: env://INTERNAL_CERT,
  key_ref: env://INTERNAL_KEY,
  ca_ref: file:///etc/tls/internal-ca.crt
)
```

Certificate and key are loaded once at gateway startup and reused for all requests. To rotate certificates:
1. Update the environment variable or file
2. Restart the gateway (or reload via `POST /admin/reload`)

---

## Security Response Headers

Add HTTP headers to every response to enforce security policies in the browser and prevent common attacks.

```yaml
steps:
  - name: add_sec_headers
    kind: set_security_headers
    params:
      hsts: max-age=31536000; includeSubDomains
      x_frame_options: DENY
      x_content_type_options: nosniff
      referrer_policy: strict-origin-when-cross-origin
      csp: "default-src 'self'; script-src 'self' 'unsafe-inline'"
```

**Standard headers**:

| Header | Purpose | Example Value |
|--------|---------|--------|
| `Strict-Transport-Security` | Force HTTPS | `max-age=31536000; includeSubDomains` |
| `X-Frame-Options` | Prevent clickjacking | `DENY` or `SAMEORIGIN` |
| `X-Content-Type-Options` | Prevent MIME sniffing | `nosniff` |
| `Referrer-Policy` | Control referrer leakage | `strict-origin-when-cross-origin` |
| `Content-Security-Policy` | XSS and injection prevention | `default-src 'self'` |
| `Permissions-Policy` | Restrict browser features | `geolocation=(), microphone=()` |

**Example: Strict security headers**
```yaml
set_security_headers(
  hsts: "max-age=31536000; includeSubDomains; preload",
  x_frame_options: "DENY",
  x_content_type_options: "nosniff",
  referrer_policy: "strict-origin-when-cross-origin",
  csp: "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:",
  permissions_policy: "geolocation=(), microphone=(), camera=()"
)
```

---

## CORS (Cross-Origin Resource Sharing)

Allow or deny cross-origin requests from web browsers. CORS is enforced by browsers; RAH validates and returns appropriate headers.

```yaml
steps:
  - name: cors_check
    kind: cors
    params:
      allowed_origins:
        - https://app.mycompany.com
        - https://admin.mycompany.com
      allowed_methods:
        - GET
        - POST
        - PUT
        - DELETE
      allowed_headers:
        - Authorization
        - Content-Type
        - X-API-Key
      expose_headers:
        - X-Request-ID
        - X-RateLimit-Remaining
      max_age: 3600
      allow_credentials: true
```

**Key parameters**:
- `allowed_origins`: Origins allowed to make requests; use `*` to allow all (not recommended for credentialed requests)
- `allowed_methods`: HTTP methods the browser can use
- `allowed_headers`: Request headers the browser can send
- `expose_headers`: Response headers the browser can read
- `max_age`: How long the browser caches the preflight response
- `allow_credentials`: Whether cookies/auth credentials can be sent

**Example: Public API with wide CORS**
```yaml
cors(
  allowed_origins: "*",
  allowed_methods: ["GET", "OPTIONS"],
  allowed_headers: ["Content-Type"],
  expose_headers: ["X-RateLimit-Limit", "X-RateLimit-Remaining"],
  max_age: 86400
)
```

---

## Admin API Security

The RAH management API (port 8081) controls all gateway configuration. Secure it with authentication.

### Basic Authentication

```yaml
admin:
  auth:
    kind: basic
    users:
      - username: admin
        password_ref: env://ADMIN_PASSWORD
      - username: readonly
        password_ref: env://READONLY_PASSWORD
```

```bash
curl -u admin:$ADMIN_PASSWORD http://localhost:8081/api/v1/tenants
```

### Bearer Token Authentication

```yaml
admin:
  auth:
    kind: bearer
    tokens:
      - token_ref: env://ADMIN_TOKEN
        description: "CI/CD pipeline"
      - token_ref: env://MONITORING_TOKEN
        description: "Observability system"
```

```bash
curl -H "Authorization: Bearer $ADMIN_TOKEN" http://localhost:8081/api/v1/tenants
```

### Require Gateway Auth

Require the same authentication used on the gateway's inbound APIs. Useful if your gateway enforces mutual TLS or API key auth.

```yaml
admin:
  require_gateway_auth: true     # Use the same auth configured in gateway.api.auth
```

### Rotate Admin Credentials

Credentials are typically environment variables or mounted secrets. To rotate:

1. **Set new credential** in environment / secret store
2. **Wait for gateway to reload** (automatic every 5 minutes, or manual: `POST /admin/reload`)
3. **Old credential still works** during 5-minute transition window
4. After 1 hour, old credential is deleted

This prevents deployment downtime during credential rotations.

---

## Access Log Tamper Detection

Audit logs must be trustworthy. RAH can sign every access log entry with HMAC using a secret key, allowing you to detect tampering.

```yaml
observability:
  access_log:
    signing_key_ref: env://ACCESS_LOG_SIGN_KEY
    signature_algorithm: HMAC-SHA256
```

Every access log entry includes a `_signature` field containing the HMAC of the entry's content. Verify it using:

```bash
echo -n "$LOG_ENTRY_JSON" | openssl dgst -sha256 -hmac "$ACCESS_LOG_SIGN_KEY" -hex
# Compare output to _signature field
```

---

## TLS/HTTPS Configuration

Encrypt all traffic between clients and RAH using TLS.

```yaml
tls:
  enabled: true
  cert_file: /etc/tls/server.crt
  key_file: /etc/tls/server.key
  port: 8443
  client_auth: none          # or "optional" or "required" for mTLS
  min_version: "1.2"         # TLS 1.2 or higher
```

**Client Authentication Modes**:
- `none`: Server TLS only (standard HTTPS)
- `optional`: Client certificate accepted but not required
- `required`: Client certificate must be present and valid (mutual TLS)

**Example: Mutual TLS for internal traffic**
```yaml
tls:
  enabled: true
  cert_file: /etc/tls/server.crt
  key_file: /etc/tls/server.key
  client_auth: required
  client_ca_file: /etc/tls/client-ca.crt
  port: 8443
```

---

## Production Security Checklist

Use this checklist to verify your RAH deployment is production-ready:

### Authentication & Authorization
- [ ] Enable admin authentication (`admin.auth.kind` set, not `none`)
- [ ] All API keys configured via API, not hardcoded
- [ ] JWT validation enabled for public APIs (`validate_token`)
- [ ] Token cache flushed on key rotation (`POST /admin/jwks/flush`)

### Data Protection
- [ ] TLS enabled on all ports (`tls.enabled: true`)
- [ ] All secrets loaded via `load_secret()`, none hardcoded in flows
- [ ] Secrets encryption at rest enabled (`secrets.encrypted.kind: env`)
- [ ] Client certificates used for sensitive upstream calls (mTLS)

### Network Security
- [ ] IP restrictions configured for sensitive APIs
- [ ] Geo blocking enabled if needed (GDPR, licensing)
- [ ] Body size limits set on POST/PUT endpoints (`limit_body`)
- [ ] CORS configured to match your frontend origin, not `*`

### Threat Detection
- [ ] OWASP checks enabled on public APIs (`owasp_check`)
- [ ] Bot detection active (`detect_bot`)
- [ ] Rate limiting configured on all APIs
- [ ] Prompt injection detection enabled for AI flows

### Response Security
- [ ] Security headers set (`set_security_headers`)
- [ ] No sensitive data in response headers or logs
- [ ] Access log signing enabled for audit (`access_log.signing_key_ref`)
- [ ] Error messages don't leak implementation details

### Monitoring & Incident Response
- [ ] Access logs shipped to centralized logging system
- [ ] Alerts configured for 5xx errors, rate limit spikes
- [ ] Failed auth attempts logged and monitored
- [ ] Rate limiting metrics visible in Observability dashboard

### Operational Security
- [ ] Admin credentials rotated quarterly
- [ ] Secrets manager (Vault, Secrets Manager) configured
- [ ] Gateway logs don't contain plaintext secrets
- [ ] Incident response runbook created and tested

---

## Common Security Patterns

### Pattern 1: Public API with Rate Limiting and Bot Check

```yaml
flows:
  - name: public-api
    api: my-public-api
    steps:
      - bot_check = detect_bot(mode: tag)
      - if bot_check.is_bot {rate_limit_v2(window_sec: 60, limit: 10)}
      - else {rate_limit_v2(window_sec: 60, limit: 1000)}
      - owasp_check(mode: tag)
      - cors(allowed_origins: "*")
      - set_security_headers(...)
      - return your_logic_here
```

### Pattern 2: Internal API with mTLS

```yaml
flows:
  - name: internal-api
    api: internal-api
    steps:
      - ip_restriction(mode: allow, cidrs: ["10.0.0.0/8"])
      - validate_api_key(header.X-API-Key, on_failure: stop)
      - rate_limit_v2(window_sec: 60, limit: 100)
      - return your_logic_here
```

### Pattern 3: OAuth 2.0 Protected API

```yaml
flows:
  - name: oauth-protected
    api: protected-api
    steps:
      - validate_token(
          token_source: header.Authorization,
          jwks_url: https://auth.example.com/.well-known/jwks.json,
          on_failure: stop,
          claims_var: token
        )
      - user_id = token.claims["sub"]
      - rate_limit_v2(window_sec: 60, limit: 100)
      - return your_logic_here
```

### Pattern 4: Sensitive Data API with Full Defense

```yaml
flows:
  - name: sensitive-api
    api: sensitive-data
    steps:
      - limit_body(max: 100KB)
      - ip_restriction(mode: allow, cidrs: ["10.0.0.0/8"])
      - validate_token(header.Authorization, on_failure: stop, claims_var: token)
      - validate_dpop(dpop_header: DPoP, access_token: token.token, max_age_sec: 60)
      - owasp_check(mode: block)
      - rate_limit_v2(window_sec: 60, limit: 10)
      - set_security_headers(hsts: "max-age=31536000", csp: "default-src 'none'")
      - return your_logic_here
```

---

## Troubleshooting Security Issues

### JWT Validation Fails

```
Error: "token validation failed: signature invalid"
```

**Cause**: JWKS endpoint unreachable or returns incorrect keys

**Fix**:
1. Verify JWKS URL is correct: `curl https://auth.mycompany.com/.well-known/jwks.json`
2. Check certificate validation: `curl -v https://auth.mycompany.com/.well-known/jwks.json`
3. Flush JWKS cache: `curl -X POST http://localhost:8081/admin/jwks/flush`

### Rate Limit Too Strict

```
Many requests getting 429 Too Many Requests
```

**Fix**:
1. Check configured limits: `curl http://localhost:8081/api/v1/rate-limit-configs`
2. Identify which window is being hit (per second vs per minute)
3. Adjust limits: `curl -X POST http://localhost:8081/api/v1/rate-limit-configs/my-limit -d '{"limit": 1000, "window_sec": 60}'`

### CORS Blocked

```
Browser console: "Cross-Origin Request Blocked"
```

**Fix**:
1. Check `Access-Control-Allow-Origin` header in response
2. Verify flow has `cors()` step with correct `allowed_origins`
3. Ensure preflight (OPTIONS) request is handled

### Secret Loading Failed

```
Error: "secret not found: env://MY_SECRET"
```

**Fix**:
1. Verify environment variable is set: `echo $MY_SECRET`
2. Check secret source is accessible (file readable, Vault reachable)
3. Restart gateway after updating secrets

---

## Further Reading

- [OWASP Top 10](https://owasp.org/www-project-top-ten/) — Web application security risks
- [NIST Cybersecurity Framework](https://www.nist.gov/cyberframework) — Governance and compliance
- [RFC 9449 (DPoP)](https://tools.ietf.org/html/rfc9449) — Demonstration of Proof-of-Possession
- [RFC 7662 (Token Introspection)](https://tools.ietf.org/html/rfc7662) — OAuth 2.0 token introspection
