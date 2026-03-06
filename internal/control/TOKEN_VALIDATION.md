# Token Validation Instruction (`token_validation`)

This document explains how `token_validation` works in Rah, what each validation case does,
and how to configure it for customer-specific deployments (property-based, registry-based,
or cache-manager-backed resolution).

## 1) What the compiler does

When flow step action is `token_validation`, the compiler:

1. reads `step.KeyIdentifier` + `step.Input`
2. parses them once into typed `TokenValidationConfig`
3. appends `TOKEN_VALIDATE` runtime instruction

This avoids request-time map parsing and reduces hot-path overhead.

## 2) Runtime flow (`TOKEN_VALIDATE`)

Per request, runtime instruction performs:

1. Read token from configured source (`slot`, `header`, `query`, or `cookie`).
2. Normalize token (accept `Bearer <jwt>` and raw JWT value).
3. Validate JWT structure (3 segments).
4. Decode + parse JWT header/claims.
5. Validate enabled claim checks (`iss`, `aud`, `exp`, `nbf`).
6. Validate required scopes (if configured).
7. Validate signature (if enabled):
   - resolve JWKS URI (static URI or `jwks_ref` resolver)
   - load JWK set (from cache provider first, network fallback)
   - select key by `kid`
   - verify RSA signature (`RS256`)
8. On failure: short-circuit response as `401 unauthorized`.
9. On success: continue to next instruction.

## 3) Slot size and large JWTs

- `ByteSlots` are `[][]byte`: slot count is fixed by layout (`MaxBytesSlots`), but each slot stores variable-length bytes.
- JWTs with large claim sets/scopes (KB-level) are supported.
- For best control and lower copy overhead, you can read token directly from request object via `token.source` instead of relying only on slot binding.

## 4) Configuration fields

All fields are supplied in `step.Input`.

### Token source fields

- `token.source`: `slot` (default), `header`, `query`, `cookie`
- `token.key`: key name for header/query/cookie reads

If `token.source=slot`, token comes from compiled slot binding (`step.key_identifier`).

### Core JWT fields

- `jwt.jwks_uri` (preferred static URI)
- `jwt.jwks_url` (legacy alias)
- `jwt.jwks_ref` (reference key for dynamic resolver path)
- `jwt.alg` (default `RS256`)
- `jwt.leeway_seconds` (default `30`)
- `jwt.prefetch_jwks` (`true|false`) prefetch on instruction construction

### Claim fields

- `jwt.issuer`
- `jwt.audience`
- `jwt.validate` (CSV list controlling checks)

`jwt.validate` supports:

- `signature` / `sig`
- `issuer` / `iss`
- `audience` / `aud`
- `expiry` / `exp`
- `not_before` / `nbf`
- `all` / `*`

Default behavior when not provided: all checks enabled.

### Scope fields

- `jwt.required_scopes`: comma-separated required scopes
- `jwt.scope_claims`: optional claim names to inspect for scopes (default: `scope,scp`)

Scope extraction supports space-delimited string scopes and array-of-string scopes.

## 5) JWKS source strategy

Rah supports multiple source patterns for JWKS URI and payload data:

### A. Static property based

Set `jwt.jwks_uri` directly in flow config.

### B. Registry/cache/property lookup

Set `jwt.jwks_ref` and register a `JWKSURIResolver` implementation.
Resolver can fetch URI from tenant metadata, registry, or any external source.

Important: resolver is called at request time when `jwks_uri` is not statically set.
This means request A and request B can resolve to different JWKS URIs safely
(e.g., multi-tenant/bring-your-own-IdP scenarios).

### C. External cache provider for payload bytes

Use `SetJWTCacheProvider(...)` to plug in cache backend policy
(local in-memory, redis adapter, custom cache manager wrapper).

Runtime lookup order for JWKS payload bytes:

1. `JWTCacheProvider.Get(cacheKey)`
2. fallback HTTP fetch
3. optional `JWTCacheProvider.Set(cacheKey, payload, ttl)`

Cache key includes full JWKS URI (`jwks:<uri>`), so keys from different providers
are naturally isolated.

## 6) Latency considerations

For low-latency execution:

- keep `jwt.validate` minimal for your use case
- use `jwt.prefetch_jwks=true` to reduce first-hit latency
- use `JWTCacheProvider` backed by fast cache for cross-instance reuse
- prefer `jwt.jwks_ref` + resolver when per-tenant values are dynamic
- use direct `token.source=header/query/cookie` when that fits your ingest model

## 7) Example flow snippets

### Slot-based (default)

```json
{
  "action": "token_validation",
  "key_identifier": "header.Authorization",
  "input": {
    "jwt.jwks_ref": "tenant/acme/oidc",
    "jwt.validate": "signature,iss,exp,nbf",
    "jwt.issuer": "https://acme.okta.com/oauth2/default",
    "jwt.required_scopes": "read:users,write:users",
    "jwt.prefetch_jwks": "true"
  }
}
```

### Direct header-source read

```json
{
  "action": "token_validation",
  "key_identifier": "header.Authorization",
  "input": {
    "token.source": "header",
    "token.key": "X-Access-Token",
    "jwt.jwks_uri": "https://issuer/.well-known/jwks.json"
  }
}
```

## 8) Security notes

- `RS256` is currently supported.
- Missing/invalid token always fails closed (`401`).
- Signature validation requires resolvable JWKS URI and matching `kid`.
