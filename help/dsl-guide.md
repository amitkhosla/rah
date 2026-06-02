# Writing Flows with the DSL

The RAH DSL (Domain-Specific Language) is a simple language for expressing API logic. Each line is one operation. Flows are defined as text blocks inside YAML bundles.

---

## Basic YAML Bundle Structure

```yaml
flows:
  - name: my_flow
    action: upsert   # or "delete"
    code: |
      # DSL goes here, one statement per line
      return(200, "OK")

apis:
  - name: my-api
    path: /v1/resource
    method: GET
    flow_name: my_flow
    action: upsert
```

---

## Variables and Data Binding

Get data from the request into variables:

```
user_id = header("X-User-ID")        # from request header
page    = query("page")              # from query string
name    = body("user.name")          # from JSON body (dot notation)
segment = path("0")                  # first path segment
ip      = client_ip()               # resolved client IP
msg     = "Hello, {user_id}!"       # template literal
msg     = "static value"            # constant
```

---

## Calling Upstream Services (HTTP)

```
url = registry.url("primary")        # tenant's upstream URL
resp = http.get(url_var: url, timeout: 5000)
resp = http.post(url: "https://api.example.com/data", timeout: 3000)
return(200, resp)
```

---

## Caching

```
cached = cache.get("product:{product_id}")
cache.set("product:{product_id}", resp, ttl: 300)
cache.delete("product:{product_id}")
shared = shared_cache.get("global-config")  # cross-tenant
```

---

## Multi-tenancy (Registry)

```
registry.lookup(header("X-Tenant-ID"))      # identify tenant
url     = registry.url("primary")           # tenant's upstream URL
api_key = registry.id("api_key")            # tenant's stored credential
tier    = registry.meta("tier")             # tenant metadata
```

---

## Authentication

```
# Validate JWT / OAuth token
validate_token(header.Authorization, jwks_url: "https://idp/.well-known/jwks.json",
  alg: "RS256", on_failure: stop)

# Validate native RAH API key
validate_api_key(header.X-API-Key, on_failure: stop)

# Token introspection (opaque tokens)
validate_introspection(url: "https://auth/introspect", token_header: "Authorization",
  cache_ttl: "30", on_failure: stop)
```

---

## Rate Limiting

```
rate_limit_v2(config: "standard", count_by: tenant)
rate_limit_v2(config: "per-ip", count_by: ip)
rate_limit_v2(config: "per-key", count_by: slot, slot: api_key)
```

---

## Conditionals and Branching

```
if (tier == "premium") {
  call premium_flow
} else {
  call standard_flow
}

switch (region) {
  "us": call us_backend
  "eu": call eu_backend
}
```

---

## Sub-flows

```
call auth_check           # call a named sub-flow
call tenant_routing       # reuse common logic
```

---

## Setting Response

```
return(200, resp)                   # respond with body
return(404, "Not found")            # error response
fail(500, "Upstream unreachable")   # mark as failed
output.status = 201
output.header("X-Request-ID") = correlation_id()
output.body = resp
```

---

## String Operations

```
lower  = to_lower(email)
upper  = to_upper(code)
len    = byte_length(body_var)
sub    = substring(text, start: 0, length: 10)
joined = concat(first_name, last_name)
```

---

## Secrets

```
key = load_secret("env://MY_API_KEY")
key = load_secret("gcp://my-project/my-secret")
key = load_secret("aws://my-secret-name")
```

---

## AI / LLM Instructions

```
response = llm(prompt, model: "claude", max_tokens: "2000")
label    = classify_llm(text, model: "gpt-4", labels: "positive,negative,neutral")
embedding = embed_text(text, model: "text-embedding")
results  = vector_search(embedding, store: "knowledge-base", top_k: "5")
```

---

## Observability

```
log("user_id", user_id)             # write to access log
corr_id = correlation_id()          # auto-generated per request
```

---

## Complete Example: Tenant-aware Authenticated Proxy

```yaml
flows:
  - name: secure_proxy
    action: upsert
    code: |
      # 1. Authenticate
      validate_token(header.Authorization, jwks_url: "https://auth/.well-known/jwks.json",
        alg: "RS256", on_failure: stop)
      
      # 2. Identify tenant
      registry.lookup(header("X-Tenant-ID"))
      
      # 3. Rate limit (per tenant, per second)
      rate_limit_v2(config: "standard-api", count_by: tenant)
      
      # 4. Check cache
      key = concat("user:", header("X-User-ID"))
      cached = cache.get(key)
      if (cached != "") {
        return(200, cached)
      }
      
      # 5. Call upstream
      url = registry.url("primary")
      resp = http.get(url_var: url, timeout: 5000)
      
      # 6. Cache and return
      cache.set(key, resp, ttl: 60)
      return(200, resp)

apis:
  - name: secure-proxy
    path: /api/{path}
    method: GET
    flow_name: secure_proxy
    action: upsert
```

---

## Tips

- Variables are scoped to the current request; they don't persist between requests
- Sub-flows share the same variable space as the caller
- The `on_failure: stop` parameter on validate_token means "return 401 and stop"
- `on_failure: continue` means "set a result variable and keep going"
- Use `call` for reusable logic across multiple flows

---

## Complete Examples (Additional)


