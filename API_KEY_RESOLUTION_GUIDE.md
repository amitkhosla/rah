# API Key Resolution Guide

## 🎯 Overview

The gateway supports multi-level API key resolution with automatic fallback. This allows flexible API key management across tenants and models without requiring separate gateway deployments.

**Resolution Priority (first match wins):**
1. **X-API-Key header** (runtime override)
2. **Per-tenant per-model key** (registered via API)
3. **Per-tenant default key** (registered via API)
4. **Model config key** (from rah.yaml)
5. **Empty string** (if nothing else found)

---

## 📋 Usage Patterns

### Pattern 1: X-API-Key Header (Recommended for clients)

Client sends API key in request header:

```bash
curl -X POST http://localhost:8080/api/llm/chat \
  -H "X-API-Key: sk-..." \
  -H "Content-Type: application/json" \
  -d '{"prompt": "Hello!"}'
```

**Flow:**
1. Gateway receives request with X-API-Key header
2. LLM step reads header in llm_call instruction
3. Uses header key instead of model config key
4. No configuration needed!

**When to use:**
- Client has their own API key
- Different clients use different keys
- Dynamic key management per request

---

### Pattern 2: Per-Tenant Per-Model Key (Admin sets up)

Admin registers API key for specific tenant/model combination:

```bash
# Via APIKeyResolver (future API endpoint)
POST /api/v1/api-keys
{
  "tenant_id": "acme-corp",
  "model": "gpt-4o",
  "api_key": "sk-..."
}
```

**Flow:**
1. Request comes in (no X-API-Key header)
2. Gateway extracts tenant_id from request
3. Looks up registered key: "acme-corp:gpt-4o"
4. Uses that key for LLM call
5. Falls back to per-tenant default if not found

**When to use:**
- Tenant-specific API keys
- Different models use different keys
- Admin wants to rotate keys per tenant

---

### Pattern 3: Per-Tenant Default Key (Admin sets up)

Admin registers default key for all models of a tenant:

```bash
# Via APIKeyResolver (future API endpoint)
POST /api/v1/api-keys/default
{
  "tenant_id": "startup-xyz",
  "api_key": "sk-..."
}
```

**Flow:**
1. Request comes in (no X-API-Key header)
2. Looks up per-tenant-per-model key
3. Not found, falls back to per-tenant default
4. Uses default key for all models

**When to use:**
- All models share same API key per tenant
- Simpler management
- Tenant controls their own key

---

### Pattern 4: Model Config Key (Default)

API key specified directly in rah.yaml:

```yaml
llm:
  models:
    - alias: gpt-4o
      provider: openai
      api_key_ref: env:OPENAI_API_KEY
      capabilities:
        max_context_tokens: 128000
```

**Flow:**
1. Request comes in (no X-API-Key, no registered overrides)
2. Uses key from model config (resolved at startup)
3. Same key used for all requests, all tenants

**When to use:**
- Single shared API key for the model
- No tenant-specific overrides needed
- Simplest setup

---

## 🔧 Programmatic API Key Registration

### Direct Registration (In Code)

```go
// In main.go or initialization code
resolver := fm.APIKeyResolver

// Register per-model key
resolver.RegisterTenantKey("acme-corp", "gpt-4o", "sk-acme-gpt4-...")
resolver.RegisterTenantKey("acme-corp", "claude-3", "sk-acme-claude-...")

// Register default key (used for all models)
resolver.RegisterTenantDefaultKey("startup-xyz", "sk-startup-default-...")
```

### Via Management API (Future)

```bash
# Register per-model key
curl -X POST http://localhost:8081/api/v1/api-keys \
  -H "X-Admin-Token: admin-secret" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "acme-corp",
    "model": "gpt-4o",
    "api_key": "sk-..."
  }'

# Register default key
curl -X POST http://localhost:8081/api/v1/api-keys/default \
  -H "X-Admin-Token: admin-secret" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant_id": "startup-xyz",
    "api_key": "sk-..."
  }'

# List registered keys
curl -H "X-Admin-Token: admin-secret" \
  http://localhost:8081/api/v1/api-keys
```

---

## 📊 Resolution Examples

### Example 1: Header wins

```
Request:
  X-API-Key: sk-client-key-123

Registered overrides:
  acme-corp:gpt-4o → sk-acme-key-456
  acme-corp:default → sk-default-789

Model config:
  gpt-4o → sk-model-config-000

Result: sk-client-key-123 (header wins!)
```

### Example 2: Per-model key

```
Request:
  (no X-API-Key header)
  tenant_id: acme-corp
  model: gpt-4o

Registered overrides:
  acme-corp:gpt-4o → sk-acme-gpt4-456
  acme-corp:default → sk-default-789

Model config:
  gpt-4o → sk-model-config-000

Result: sk-acme-gpt4-456 (per-model match)
```

### Example 3: Default key

```
Request:
  (no X-API-Key header)
  tenant_id: startup-xyz
  model: some-model

Registered overrides:
  startup-xyz:some-model → (not found)
  startup-xyz:default → sk-startup-key-111

Model config:
  some-model → sk-model-config-000

Result: sk-startup-key-111 (tenant default)
```

### Example 4: Model config key

```
Request:
  (no X-API-Key header)
  tenant_id: unknown-tenant
  model: gpt-4o

Registered overrides:
  (none for this tenant)

Model config:
  gpt-4o → sk-model-config-000

Result: sk-model-config-000 (fallback to config)
```

---

## 🔐 Security Considerations

### 1. X-API-Key Header (Use HTTPS)
- ✅ Sent per-request (client controls key)
- ✅ Supports key rotation without restart
- ⚠️ Must use HTTPS (key visible in HTTP)
- ⚠️ Key could be logged if not careful

### 2. Per-Tenant Registered Keys
- ✅ Centrally managed
- ✅ Can be rotated via API
- ✅ Audit trail of changes
- ⚠️ Stored in memory (lost on restart)
- 🔜 Future: Store in encrypted datastore

### 3. Model Config Keys
- ✅ Immutable (can't be changed at runtime)
- ✅ Stored in version-controlled config
- ⚠️ Requires restart to change
- ⚠️ Same key for all tenants

---

## 🛡️ Best Practices

### For Platform Operators

1. **Production**: Use X-API-Key header (clients manage keys)
2. **Development**: Use model config keys
3. **Multi-tenant**: Use per-tenant registered keys
4. **Key rotation**: Implement via registered keys (no restart)

### For API Clients

1. **Always use X-API-Key header** for security
2. Don't embed keys in request body
3. Rotate keys periodically
4. Use environment variables for local development

### For Gateway Administrators

1. Never commit real API keys to rah.yaml
2. Use `env:` references for secrets
3. Implement audit logging for key registration
4. Monitor key usage patterns
5. Set up alerts for unusual patterns

---

## 🔄 Implementation Status

| Feature | Status | Notes |
|---------|--------|-------|
| X-API-Key header support | ✅ Complete | Reads from request.Header |
| APIKeyResolver infrastructure | ✅ Complete | In FlowManager |
| LLM step integration | ✅ Complete | Updated llm.go |
| Per-tenant key registration | ✅ Ready | APIKeyResolver prepared |
| Management API (list/register/revoke) | 🔜 Future | Endpoint framework ready |
| Encrypted datastore persistence | 🔜 Future | Hook into secrets manager |

---

## 📝 Configuration Examples

### rah.yaml (Model Config Keys)

```yaml
llm:
  models:
    - alias: gpt-4o
      provider: openai
      api_key_ref: env:OPENAI_API_KEY  # Read from environment

    - alias: claude-3
      provider: anthropic
      api_key_ref: file:///etc/secrets/anthropic.key  # Read from file
```

### Environment Variables

```bash
# Set API keys as environment variables
export OPENAI_API_KEY="sk-..."
export ANTHROPIC_API_KEY="sk-..."

# Start gateway
./rah-gateway -config rah.yaml
```

### Runtime Header (Curl Example)

```bash
# Client sends X-API-Key
curl -X POST http://gateway.local:8080/api/llm/chat \
  -H "X-API-Key: sk-user-key-123" \
  -H "Content-Type: application/json" \
  -d '{"prompt": "Hello, Claude!"}'
```

---

## 🚨 Troubleshooting

### Issue: "Invalid API key" from provider

1. Check X-API-Key header (if present) is valid
2. Check registered key for tenant:model
3. Check environment variable for model config
4. Verify key provider matches model provider

### Issue: Wrong key used for request

1. Print API key resolution order in logs
2. Verify X-API-Key header is/isn't being sent
3. Check registered keys for tenant
4. Check model config has key

### Issue: Keys lost after restart

1. Current: Registered keys are in-memory only (lost on restart)
2. Future: Will be stored in encrypted datastore
3. Workaround: Re-register keys from script at startup

---

## 📚 Related Files

- `internal/engine/api_key_resolver.go` - Key resolution logic
- `internal/engine/steps/llm.go` - LLM step X-API-Key support
- `internal/engine/manager.go` - FlowManager.APIKeyResolver field
- `cmd/rah-gateway/main.go` - Initialization

---

**Status**: X-API-Key support COMPLETE ✅
**Next**: Management API endpoints for key registration (future)
