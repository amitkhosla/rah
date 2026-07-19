# Model Registration & API Key Management Guide

This guide shows how to:
1. **Register multiple models** in the gateway
2. **Manage API keys** securely (env vars, tenant-specific, dynamic)
3. **Add models dynamically via REST API** (no recompile)
4. **Set up your model hierarchy** (Gemma → gpt-nano → Flash → bigger models)
5. **Make cost/latency metadata optional** (not sent to client by default)

---

## Part 1: Registering Models in Config

### Step 1: Define Models in YAML

Create `config/rah.yaml` with all your models:

```yaml
models:
  # ═══ TIER 1: First-line defense (ultra-cheap, fast)
  gemma-7b:
    provider: "custom"
    adapter: "custom"
    base_url: "${GEMMA_BASE_URL}"        # e.g., http://localhost:8000
    alias: "gemma-7b"                   # for custom providers
    capabilities:
      max_context_tokens: 8192
    max_tokens: 1024
    cost_per_input_token: 0.00001        # $0.01 per 1M tokens!
    cost_per_output_token: 0.00002
    timeout_ms: 5000
    max_retries: 1

  # ═══ TIER 2: Small models (cheap, flexible)
  gpt-4o-mini:
    provider: "openai"
    adapter: "openai"
    base_url: "https://api.openai.com"
    model_id: "gpt-4o-mini"
    capabilities:
      max_context_tokens: 128000
    max_tokens: 4096
    cost_per_input_token: 0.00015
    cost_per_output_token: 0.0006
    timeout_ms: 15000
    max_retries: 2

  gpt-4-turbo-preview:  # More flexible than nano but still cheap
    provider: "openai"
    adapter: "openai"
    base_url: "https://api.openai.com"
    model_id: "gpt-4-turbo-preview"
    capabilities:
      max_context_tokens: 128000
    max_tokens: 4096
    cost_per_input_token: 0.003
    cost_per_output_token: 0.006
    timeout_ms: 20000
    max_retries: 2

  google-generative-ai-flash:
    provider: "google"
    adapter: "gemini"
    base_url: "https://generativelanguage.googleapis.com/v1beta"
    model_id: "gemini-2.0-flash"
    capabilities:
      max_context_tokens: 1000000              # 1M tokens!
    max_tokens: 4096
    cost_per_input_token: 0.000075
    cost_per_output_token: 0.0003
    timeout_ms: 20000
    max_retries: 2

  google-generative-ai-mini:
    provider: "google"
    adapter: "gemini"
    base_url: "https://generativelanguage.googleapis.com/v1beta"
    model_id: "gemini-2.0-mini"
    capabilities:
      max_context_tokens: 1000000
    max_tokens: 4096
    cost_per_input_token: 0.000032
    cost_per_output_token: 0.00008
    timeout_ms: 20000
    max_retries: 2

  # ═══ TIER 3: Mid-range (balanced quality/cost)
  claude-haiku:
    provider: "anthropic"
    adapter: "anthropic"
    base_url: "https://api.anthropic.com"
    model_id: "claude-3-5-haiku-20241022"
    capabilities:
      max_context_tokens: 200000
    max_tokens: 4096
    cost_per_input_token: 0.00008
    cost_per_output_token: 0.0004
    timeout_ms: 25000
    max_retries: 2

  # ═══ TIER 4: Premium (high quality)
  claude-sonnet:
    provider: "anthropic"
    adapter: "anthropic"
    base_url: "https://api.anthropic.com"
    model_id: "claude-3-5-sonnet-20241022"
    capabilities:
      max_context_tokens: 200000
    max_tokens: 4096
    cost_per_input_token: 0.003
    cost_per_output_token: 0.015
    timeout_ms: 30000
    max_retries: 2

  gemini-pro:
    provider: "google"
    adapter: "gemini"
    base_url: "https://generativelanguage.googleapis.com/v1beta"
    model_id: "gemini-1.5-pro"
    capabilities:
      max_context_tokens: 2000000              # 2M tokens!
    max_tokens: 8192
    cost_per_input_token: 0.000625
    cost_per_output_token: 0.00125
    timeout_ms: 40000
    max_retries: 2

  # ═══ TIER 5: Ultra-premium (best quality, slowest)
  claude-opus:
    provider: "anthropic"
    adapter: "anthropic"
    base_url: "https://api.anthropic.com"
    model_id: "claude-3-opus-20250219"
    capabilities:
      max_context_tokens: 200000
    max_tokens: 4096
    cost_per_input_token: 0.015
    cost_per_output_token: 0.075
    timeout_ms: 50000
    max_retries: 2

  gpt-4o:
    provider: "openai"
    adapter: "openai"
    base_url: "https://api.openai.com"
    model_id: "gpt-4o"
    capabilities:
      max_context_tokens: 128000
    max_tokens: 4096
    cost_per_input_token: 0.005
    cost_per_output_token: 0.015
    timeout_ms: 30000
    max_retries: 2

  # ═══ TIER 6: Specialist (for specific tasks)
  o1-preview:                           # Reasoning model
    provider: "openai"
    adapter: "openai"
    base_url: "https://api.openai.com"
    model_id: "o1-preview"
    capabilities:
      max_context_tokens: 128000
    max_tokens: 4096
    cost_per_input_token: 0.015
    cost_per_output_token: 0.06
    timeout_ms: 120000                  # Reasoning takes time
    max_retries: 1

# ═════════════════════════════════════════════════════════════════════════
# API KEY MANAGEMENT
# ═════════════════════════════════════════════════════════════════════════

# Strategy 1: Environment Variables (baked at startup)
# Set before running gateway:
#   export OPENAI_API_KEY=sk-...
#   export ANTHROPIC_API_KEY=sk-ant-...
#   export GOOGLE_API_KEY=...

# Strategy 2: Tenant-specific keys (injected per request)
# Client sends key in header:
#   X-API-Key-OpenAI: sk-...
# Gateway injects into request at runtime

# Strategy 3: Key vault integration (enterprise)
# Use HashiCorp Vault / AWS Secrets Manager
# Fetch key per request from secure backend

flows:
  chat_with_tiered_routing:
    description: "Smart routing: Gemma → Flash/Haiku → Sonnet → Opus"

    metadata:
      slots:
        - index: 0
          name: "prompt_text"
          type: "bytes"
        - index: 0
          name: "token_count"
          type: "int"
        - index: 2
          name: "model_slug"
          type: "bytes"
        - index: 3
          name: "tenant_tier"
          type: "bytes"
        - index: 14
          name: "api_key_runtime"
          type: "bytes"
        - index: 8
          name: "llm_response"
          type: "bytes"
        - index: 11
          name: "input_tokens"
          type: "int"
        - index: 12
          name: "output_tokens"
          type: "int"

    steps:
      # Extract prompt
      - id: "extract_prompt"
        type: "extract_text"
        config:
          source_path: "body.messages[0].content"
          output_slot: 0

      # Count tokens
      - id: "tokenize"
        type: "count_tokens"
        config:
          model: "gpt-4o-mini"
          input_slot: 0
          output_slot: 0  # IntSlots[0]

      # Extract tenant info from header
      - id: "extract_tenant_tier"
        type: "extract_text"
        config:
          source_path: "header.X-Tenant-Tier"
          output_slot: 3
          default: "standard"

      # ═══════════════════════════════════════════════════════════════════
      # SMART ROUTING: Token-based + Tier-based + Price optimization
      # ═══════════════════════════════════════════════════════════════════
      - id: "classify_and_route"
        type: "route_llm"
        config:
          token_slot: 0
          meta_slot: 3
          output_slot: 2
          rules:
            # TIER 1: Tiny prompts → Gemma (save money)
            - condition: "token_count < 100"
              model: "gemma-7b"
              description: "Micro prompt → gemma-7b (1¢/1M tokens)"

            # TIER 2: Small prompts, not premium → gpt-4o-mini
            - condition: "token_count >= 100 AND token_count < 500 AND meta != premium"
              model: "gpt-4o-mini"
              description: "Small prompt, standard tier → gpt-4o-mini"

            # TIER 2.5: Small prompts, premium → Flash (Google, cheap + good)
            - condition: "token_count >= 100 AND token_count < 500 AND meta == premium"
              model: "google-generative-ai-flash"
              description: "Small prompt, premium tier → Google Flash"

            # TIER 3: Medium prompts, standard → gpt-4-turbo
            - condition: "token_count >= 500 AND token_count < 2000 AND meta == standard"
              model: "gpt-4-turbo-preview"
              description: "Medium prompt, standard → gpt-4-turbo"

            # TIER 3: Medium prompts, premium → Haiku (Claude is better quality)
            - condition: "token_count >= 500 AND token_count < 2000 AND meta == premium"
              model: "claude-haiku"
              description: "Medium prompt, premium → claude-haiku"

            # TIER 4: Large prompts, standard → Flash (1M context!)
            - condition: "token_count >= 2000 AND token_count < 10000 AND meta == standard"
              model: "google-generative-ai-flash"
              description: "Large prompt, standard → Flash (1M window)"

            # TIER 4: Large prompts, premium → Sonnet
            - condition: "token_count >= 2000 AND token_count < 10000 AND meta == premium"
              model: "claude-sonnet"
              description: "Large prompt, premium → claude-sonnet"

            # TIER 5: Very large prompts, standard → Gemini Pro (2M context)
            - condition: "token_count >= 10000 AND token_count < 100000 AND meta == standard"
              model: "gemini-pro"
              description: "Very large, standard → Gemini Pro (2M context)"

            # TIER 5: Very large, premium → Opus (best quality, no matter size)
            - condition: "token_count >= 10000 AND meta == premium"
              model: "claude-opus"
              description: "Very large, premium → Claude Opus"

          default: "gpt-4o-mini"

      # Context fitting
      - id: "context_fit"
        type: "llm_context_fit"
        config:
          model_slot: 2
          input_slot: 0
          max_tokens_output: 10
          strategy: "sliding_window"

      # ═══════════════════════════════════════════════════════════════════
      # API KEY INJECTION (dynamic per tenant)
      # ═══════════════════════════════════════════════════════════════════
      # Option A: Extract tenant-specific key from header
      - id: "inject_api_key"
        type: "extract_text"
        config:
          source_path: "header.X-API-Key"
          output_slot: 14
          optional: true
          description: "If tenant provides their own key, use it"

      # If not provided, gateway falls back to env vars
      # (configured below in api_key_strategy)

      # Call LLM with dynamic model + dynamic key
      - id: "invoke_llm"
        type: "llm_call"
        config:
          model_slot: 2                  # Dynamic: read model from classifier
          prompt_slot: 0
          result_slot: 8
          input_tokens_output: 11
          output_tokens_output: 12

          # ═══ API Key Strategy ═══
          # Option 1: Use key from slot (tenant-provided)
          api_key_slot: 14               # Read from ByteSlots[14]

          # Option 2: Fallback to env var if slot is empty/missing
          # (handler code checks: if apiKeySlot is set but empty, use env vars)

          # Option 3: Per-model API keys (configured in api_key_strategy)
          api_key_strategy:
            openai:
              env_var: "OPENAI_API_KEY"
              vault_path: "secret/openai"
            anthropic:
              env_var: "ANTHROPIC_API_KEY"
              vault_path: "secret/anthropic"
            google:
              env_var: "GOOGLE_API_KEY"
              vault_path: "secret/google"

          max_retries: 2
          timeout_ms: 30000
          temperature: 0.7

          # Fallback chain: if primary model fails, try cheaper alternatives
          fallback_chain:
            - model: "gpt-4o-mini"
            - model: "gemma-7b"

      # Format response (optional: include/exclude metadata)
      - id: "format_response"
        type: "format_response"
        config:
          response_slot: 8
          output_slot: 9
          # ═══ Control what metadata to send to client ═══
          include_metadata: false        # ← DEFAULT: DON'T send cost/latency
          metadata_fields:
            # Only include what's needed; cost/latency are OPTIONAL
            - field: "model"              # Safe to expose
            - field: "stop_reason"        # Safe to expose
            # - field: "input_tokens"     # You can include if needed
            # - field: "output_tokens"    # You can include if needed
            # - field: "cost_usd"         # DON'T expose by default!
            # - field: "latency_ms"       # DON'T expose by default!

      # Log internally (not sent to client)
      - id: "emit_trace"
        type: "emit_event"
        config:
          kind: "llm_request_trace"
          capture_all: true              # Full trace for internal logging
          redact_client_response: false   # Store everything for analysis
```

---

## Part 2: Dynamic Model Registration via REST API

### Create an endpoint to register models without restart

**File**: `internal/gateway/model_manager.go` (new)

```go
package gateway

import (
	"encoding/json"
	"net/http"
	"sync"

	"rah/internal/config"
	"rah/internal/engine"
)

// ModelManager allows dynamic model registration
type ModelManager struct {
	mu     sync.RWMutex
	models map[string]config.LLMModelConfig
}

// RegisterModel adds a new model or updates existing
func (m *ModelManager) RegisterModel(slug string, cfg config.LLMModelConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate
	if slug == "" {
		return errors.New("slug required")
	}
	if cfg.Provider == "" {
		return errors.New("provider required")
	}

	m.models[slug] = cfg
	return nil
}

// GetModel retrieves a model config
func (m *ModelManager) GetModel(slug string) (config.LLMModelConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, ok := m.models[slug]
	return cfg, ok
}

// ListModels returns all registered models
func (m *ModelManager) ListModels() map[string]config.LLMModelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to prevent external mutation
	result := make(map[string]config.LLMModelConfig)
	for k, v := range m.models {
		result[k] = v
	}
	return result
}

// HTTP Handlers

// RegisterModelHandler: POST /admin/models/register
func (m *ModelManager) RegisterModelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Require admin token
	if !isAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Slug       string                  `json:"slug"`
		Config     config.LLMModelConfig   `json:"config"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := m.RegisterModel(req.Slug, req.Config); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"slug":   req.Slug,
	})
}

// ListModelsHandler: GET /admin/models
func (m *ModelManager) ListModelsHandler(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	models := m.ListModels()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models)
}

// GetModelHandler: GET /admin/models/{slug}
func (m *ModelManager) GetModelHandler(w http.ResponseWriter, r *http.Request, slug string) {
	if !isAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	cfg, ok := m.GetModel(slug)
	if !ok {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

// DeleteModelHandler: DELETE /admin/models/{slug}
func (m *ModelManager) DeleteModelHandler(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !isAdmin(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	m.mu.Lock()
	delete(m.models, slug)
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

// Helper: Check admin token
func isAdmin(r *http.Request) bool {
	token := r.Header.Get("X-Admin-Token")
	// In production, validate against secure token storage
	return token == os.Getenv("ADMIN_TOKEN")
}
```

### Usage: Register models dynamically

```bash
# 1. Register Gemma
curl -X POST http://localhost:8080/admin/models/register \
  -H "X-Admin-Token: secret123" \
  -H "Content-Type: application/json" \
  -d '{
    "slug": "gemma-7b",
    "config": {
      "provider": "custom",
      "adapter": "custom",
      "base_url": "http://localhost:8000",
      "alias": "gemma-7b",
      "capabilities": {
        "max_context_tokens": 8192
      },
      "max_tokens": 1024,
      "cost_per_input_token": 0.00001,
      "cost_per_output_token": 0.00002
    }
  }'

# 2. List all models
curl http://localhost:8080/admin/models \
  -H "X-Admin-Token: secret123"

# 3. Get specific model
curl http://localhost:8080/admin/models/gemma-7b \
  -H "X-Admin-Token: secret123"

# 4. Delete a model
curl -X DELETE http://localhost:8080/admin/models/gemma-7b \
  -H "X-Admin-Token: secret123"
```

---

## Part 3: API Key Management Strategies

### Strategy 1: Environment Variables (Simple)

```bash
# Set before running gateway
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=sk-ant-...
export GOOGLE_API_KEY=...
export GEMMA_BASE_URL=http://localhost:8000

# Gateway reads at startup
go run ./cmd/rah-gateway/main.go
```

**Pros**: Simple, secure (keys not in config)
**Cons**: All tenants share same key, must restart to change

### Strategy 2: Tenant-Specific Keys (Per-Request)

Client sends their own API key in header:

```bash
curl -X POST http://localhost:8080/api/v1/chat \
  -H "X-Tenant: acme-corp" \
  -H "X-API-Key: sk-tenant-specific-key" \
  -d '{"messages": [...]}'
```

Gateway injects into LLM call:

```yaml
# In flow config, add:
- id: "inject_api_key"
  type: "extract_text"
  config:
    source_path: "header.X-API-Key"
    output_slot: 14
    optional: true

# Then in llm_call:
- id: "invoke_llm"
  type: "llm_call"
  config:
    api_key_slot: 14  # Read from ByteSlots[14]
```

**Pros**: Per-tenant billing, isolation
**Cons**: Client manages secrets, must send each request

### Strategy 3: Vault Integration (Enterprise)

```go
// Fetch key from secure backend at request time
func getAPIKey(tenantID, provider string) (string, error) {
	path := fmt.Sprintf("secret/data/llm/%s/%s", tenantID, provider)
	secret, err := vaultClient.Logical().Read(path)
	if err != nil {
		return "", err
	}
	return secret.Data["data"].(map[string]interface{})["key"].(string), nil
}
```

**Pros**: Centralized, auditable, rotate without restart
**Cons**: Extra latency, requires vault setup

### Strategy 4: Per-Model Keys (Different Provider per Model)

```yaml
api_key_strategy:
  gemma-7b:
    env_var: "GEMMA_API_KEY"
    # (if gemma-7b has auth)

  gpt-4o-mini:
    env_var: "OPENAI_API_KEY"

  google-generative-ai-flash:
    env_var: "GOOGLE_API_KEY"

  claude-haiku:
    env_var: "ANTHROPIC_API_KEY"
    vault_path: "secret/anthropic"  # Fallback to vault
```

**Implementation**: In llm.go, after resolving model, lookup key:

```go
func getKeyForModel(modelSlug string, req *http.Request) string {
	// Try tenant-provided key first
	if tenantKey := req.Header.Get("X-API-Key"); tenantKey != "" {
		return tenantKey
	}

	// Try model-specific env var
	providerKey := os.Getenv(modelToEnvVar[modelSlug])
	if providerKey != "" {
		return providerKey
	}

	// Fallback: return error or use default
	return ""
}
```

---

## Part 4: Making Cost/Latency Optional

### Why You Shouldn't Send Cost to Client By Default

1. **Breaking changes**: If client doesn't expect `cost_usd` field, it might fail
2. **Security**: Don't expose your pricing to competitors
3. **API stability**: Adds extra data, increases response size
4. **Backward compatibility**: Existing clients may fail with extra fields

### Solution: Make It Configurable

**In flow config:**

```yaml
- id: "format_response"
  type: "format_response"
  config:
    include_metadata: false    # ← DEFAULT: disable

    # Only include what's safe/expected
    metadata_fields:
      - field: "model"
      - field: "stop_reason"
      # cost_usd and latency_ms NOT included by default
```

**Per-tenant override:**

```bash
# Client opts in to see cost
curl -X POST http://localhost:8080/api/v1/chat \
  -H "X-Include-Metadata: true" \
  -d '{"messages": [...]}'

# Response includes:
{
  "content": "...",
  "metadata": {
    "model": "gpt-4o-mini",
    "input_tokens": 118,
    "output_tokens": 150,
    "cost_usd": 0.01077,     # ← Only if client requests
    "latency_ms": 810
  }
}
```

**Handler code to support this:**

```go
// Check header to see if client wants full metadata
includeMetadata := r.Header.Get("X-Include-Metadata") == "true"

// Build response
response := map[string]interface{}{
	"content": result,
}

if includeMetadata {
	response["metadata"] = map[string]interface{}{
		"model":        model,
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"cost_usd":      estimatedCost,  // Only if requested
		"latency_ms":    latency,
	}
} else {
	// Minimal metadata (safe for all clients)
	response["metadata"] = map[string]interface{}{
		"model": model,
	}
}

w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(response)
```

---

## Part 5: Complete Model Hierarchy Setup

### Your Setup: Gemma → Flash/Haiku → Sonnet → Opus

Create `config/rah.yaml`:

```yaml
models:
  # Tier 1: Local LLM (Gemma)
  gemma-7b:
    provider: "custom"
    adapter: "custom"
    base_url: "http://localhost:8000"
    alias: "gemma-7b"
    capabilities:
      max_context_tokens: 8192
    max_tokens: 1024
    cost_per_input_token: 0.00001
    cost_per_output_token: 0.00002

  # Tier 2: Cheap & fast API models
  gpt-4o-mini:
    provider: "openai"
    adapter: "openai"
    base_url: "https://api.openai.com"
    model_id: "gpt-4o-mini"
    capabilities:
      max_context_tokens: 128000
    max_tokens: 4096
    cost_per_input_token: 0.00015
    cost_per_output_token: 0.0006

  google-generative-ai-flash:
    provider: "google"
    adapter: "gemini"
    base_url: "https://generativelanguage.googleapis.com/v1beta"
    model_id: "gemini-2.0-flash"
    capabilities:
      max_context_tokens: 1000000
    max_tokens: 4096
    cost_per_input_token: 0.000075
    cost_per_output_token: 0.0003

  # Tier 3: Mid-range (quality)
  claude-haiku:
    provider: "anthropic"
    adapter: "anthropic"
    base_url: "https://api.anthropic.com"
    model_id: "claude-3-5-haiku-20241022"
    capabilities:
      max_context_tokens: 200000
    max_tokens: 4096
    cost_per_input_token: 0.00008
    cost_per_output_token: 0.0004

  # Tier 4: Premium
  claude-sonnet:
    provider: "anthropic"
    adapter: "anthropic"
    base_url: "https://api.anthropic.com"
    model_id: "claude-3-5-sonnet-20241022"
    capabilities:
      max_context_tokens: 200000
    max_tokens: 4096
    cost_per_input_token: 0.003
    cost_per_output_token: 0.015

  gemini-pro:
    provider: "google"
    adapter: "gemini"
    base_url: "https://generativelanguage.googleapis.com/v1beta"
    model_id: "gemini-1.5-pro"
    capabilities:
      max_context_tokens: 2000000
    max_tokens: 8192
    cost_per_input_token: 0.000625
    cost_per_output_token: 0.00125

  # Tier 5: Ultra (best quality)
  claude-opus:
    provider: "anthropic"
    adapter: "anthropic"
    base_url: "https://api.anthropic.com"
    model_id: "claude-3-opus-20250219"
    capabilities:
      max_context_tokens: 200000
    max_tokens: 4096
    cost_per_input_token: 0.015
    cost_per_output_token: 0.075

flows:
  smart_chat:
    steps:
      - id: "extract"
        type: "extract_text"
        config:
          source_path: "body.messages[0].content"
          output_slot: 0

      - id: "tokenize"
        type: "count_tokens"
        config:
          model: "gpt-4o-mini"
          input_slot: 0
          output_slot: 0

      - id: "classify"
        type: "route_llm"
        config:
          token_slot: 0
          meta_slot: 3
          output_slot: 2
          rules:
            # Small: Gemma (cheapest)
            - condition: "token_count < 50"
              model: "gemma-7b"

            # Small-medium: gpt-4o-mini
            - condition: "token_count >= 50 AND token_count < 500"
              model: "gpt-4o-mini"

            # Medium: Flash (1M context, cheap)
            - condition: "token_count >= 500 AND token_count < 5000"
              model: "google-generative-ai-flash"

            # Medium-premium: Haiku
            - condition: "token_count >= 5000 AND meta == premium"
              model: "claude-haiku"

            # Large: Flash or Sonnet
            - condition: "token_count >= 5000 AND token_count < 50000"
              model: "google-generative-ai-flash"

            - condition: "token_count >= 50000 AND meta == premium"
              model: "claude-sonnet"

            # Very large: Gemini Pro or Opus
            - condition: "token_count >= 50000 AND meta == enterprise"
              model: "claude-opus"

          default: "gpt-4o-mini"

      - id: "invoke"
        type: "llm_call"
        config:
          model_slot: 2
          prompt_slot: 0
          result_slot: 8
          input_tokens_output: 11
          output_tokens_output: 12
          fallback_chain:
            - model: "gpt-4o-mini"
            - model: "gemma-7b"

      - id: "format"
        type: "format_response"
        config:
          response_slot: 8
          include_metadata: false    # ← Don't expose cost by default
```

---

## Part 6: Setup Checklist

- [ ] Define all models in YAML (`models:` section)
- [ ] Set environment variables for API keys:
  ```bash
  export OPENAI_API_KEY=sk-...
  export ANTHROPIC_API_KEY=sk-ant-...
  export GOOGLE_API_KEY=...
  export ADMIN_TOKEN=secret123
  ```
- [ ] Configure routing rules (token-based + tier-based)
- [ ] Set `include_metadata: false` in format_response (safe default)
- [ ] Implement ModelManager for dynamic registration (optional)
- [ ] Wire REST endpoints for admin operations (optional)
- [ ] Test with different request sizes (verify routing works)
- [ ] Monitor cost/tokens in internal logs (don't expose to client)
- [ ] Set up alerts if unusual costs detected

---

## Supported Providers

This gateway supports the following LLM providers:

- **OpenAI** — gpt-4o, gpt-4-turbo, gpt-4o-mini, o1 models
- **Anthropic** — Claude Opus, Claude Sonnet, Claude Haiku
- **Google** — Gemini Pro, Gemini Flash, Gemini Mini
- **Ollama** — Local LLM hosting (Gemma, Llama, Mistral, etc.)
- **DeepSeek** — DeepSeek API models
- **AWS Bedrock** — Multi-model access via AWS

---

## Reference: Model Providers & Setup

### OpenAI
```
Provider: "openai"
BaseURL: "https://api.openai.com"
API Key Env: OPENAI_API_KEY
Models: gpt-4o, gpt-4o-mini, gpt-4-turbo, gpt-3.5-turbo, o1, o1-mini
```

### Anthropic
```
Provider: "anthropic"
BaseURL: "https://api.anthropic.com"
API Key Env: ANTHROPIC_API_KEY
Models: claude-opus, claude-sonnet, claude-haiku
```

### Google
```
Provider: "google"
BaseURL: "https://generativelanguage.googleapis.com/v1beta"
API Key Env: GOOGLE_API_KEY
Models: gemini-2.0-flash, gemini-2.0-mini, gemini-1.5-pro
```

### Local Gemma (Custom)
```
Provider: "custom"
BaseURL: "http://localhost:8000"
No API key needed (or custom auth)
Model: gemma-7b, gemma-13b
Setup: ollama run gemma or vLLM
```

### Ollama
```
Provider: "ollama"
BaseURL: "http://localhost:11434"
No API key needed
Models: Any model supported by Ollama (Gemma, Llama, Mistral, Neural Chat, etc.)
Setup: ollama run <model_name>
```

### DeepSeek
```
Provider: "deepseek"
BaseURL: "https://api.deepseek.com"
API Key Env: DEEPSEEK_API_KEY
Models: deepseek-chat, deepseek-coder
```

### AWS Bedrock
```
Provider: "bedrock"
BaseURL: "https://bedrock-runtime.{region}.amazonaws.com"
Credentials: AWS IAM (from environment or credentials file)
Models: Claude (Anthropic), Llama (Meta), Titan (Amazon), Mistral, etc.
Setup: Configure AWS credentials and region
```

