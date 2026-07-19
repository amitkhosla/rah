# Smart LLM Classifier & Router Setup Guide

This guide shows how to set up RAH as a smart API gateway that:
1. **Classifies** incoming prompts (by size, complexity, etc.)
2. **Routes** to appropriate LLM models based on classification
3. **Transforms** requests (remove unnecessary tools, trim context, etc.)
4. **Observes** the entire pipeline (request → classification → model selection → response)

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                     Client Request                               │
│              (prompt + tools + system message)                   │
└──────────────────────────┬──────────────────────────────────────┘
                           ▼
           ┌─────────────────────────────────┐
           │  Extraction Layer               │
           │  - Extract prompt text          │
           │  - Count tokens                 │
           │  - Detect available tools       │
           │  - Get tenant metadata          │
           └────────────┬────────────────────┘
                        ▼
           ┌─────────────────────────────────┐
           │  Classification Layer           │
           │  (Rule-based conditions):       │
           │  - token_count < 1000 → fast    │
           │  - token_count 1000-5000 → mid  │
           │  - token_count > 5000 → long    │
           │  - meta == "premium" → best     │
           │  - meta != "premium" → default  │
           └────────────┬────────────────────┘
                        ▼
           ┌─────────────────────────────────┐
           │  Model Selection Layer          │
           │  Resolved model:                │
           │  - gpt-4o-mini (fast, cheap)    │
           │  - gpt-4o (balanced)            │
           │  - claude-opus (best, slow)     │
           └────────────┬────────────────────┘
                        ▼
           ┌─────────────────────────────────┐
           │  Request Transform Layer        │
           │  - Filter tools (if any)        │
           │  - Set max_tokens (per model)   │
           │  - Adjust temperature           │
           │  - Truncate context if needed   │
           └────────────┬────────────────────┘
                        ▼
           ┌─────────────────────────────────┐
           │  Execute LLM Call               │
           │  - Send to resolved model       │
           │  - Capture response + timing    │
           │  - Record token usage           │
           │  - Handle failures/fallback     │
           └────────────┬────────────────────┘
                        ▼
           ┌─────────────────────────────────┐
           │  Observability & Logging        │
           │  - Log: client request          │
           │  - Log: classifier decision     │
           │  - Log: model selected          │
           │  - Log: transformed request     │
           │  - Log: LLM response            │
           │  - Log: client response         │
           │  - Metrics: latency/tokens      │
           └────────────┬────────────────────┘
                        ▼
        ┌──────────────────────────────────┐
        │  Return to Client                │
        │  (with streaming if applicable)  │
        └──────────────────────────────────┘
```

---

## Step 1: Configuration (YAML)

### Add to your `config.yaml`:

```yaml
# Model Catalog: Define all available models
models:
  gpt-4o-mini:
    provider: "openai"
    adapter: "openai"
    base_url: "https://api.openai.com/v1/chat/completions"
    model_id: "gpt-4o-mini"
    max_tokens: 4096
    capabilities:
      max_context_tokens: 128000
    cost_per_input_token: 0.00015
    cost_per_output_token: 0.0006

  gpt-4o:
    provider: "openai"
    adapter: "openai"
    base_url: "https://api.openai.com/v1/chat/completions"
    model_id: "gpt-4o"
    max_tokens: 4096
    capabilities:
      max_context_tokens: 128000
    cost_per_input_token: 0.005
    cost_per_output_token: 0.015

  claude-opus:
    provider: "anthropic"
    adapter: "anthropic"
    base_url: "https://api.anthropic.com/v1/messages"
    model_id: "claude-opus-4-1"
    max_tokens: 4096
    capabilities:
      max_context_tokens: 200000
    cost_per_input_token: 0.015
    cost_per_output_token: 0.075

  gemini-flash:
    provider: "google"
    adapter: "gemini"
    base_url: "https://generativelanguage.googleapis.com/v1beta"
    model_id: "gemini-2.0-flash"
    max_tokens: 4096
    capabilities:
      max_context_tokens: 1000000
    cost_per_input_token: 0.000075
    cost_per_output_token: 0.0003

  local-llm:
    provider: "ollama"
    adapter: "ollama"
    base_url: "http://localhost:11434"
    model_id: "llama3.2"
    max_tokens: 2048
    capabilities:
      max_context_tokens: 8192
    cost_per_input_token: 0.0
    cost_per_output_token: 0.0

  deepseek-chat:
    provider: "deepseek"
    adapter: "deepseek"
    base_url: "https://api.deepseek.com"
    model_id: "deepseek-chat"
    max_tokens: 4096
    capabilities:
      max_context_tokens: 32000
    cost_per_input_token: 0.0001
    cost_per_output_token: 0.0002

  claude-bedrock:
    provider: "anthropic"
    adapter: "bedrock"
    base_url: "us-east-1"
    model_id: "anthropic.claude-3-5-sonnet-20241022-v2:0"
    max_tokens: 4096
    capabilities:
      max_context_tokens: 200000
    cost_per_input_token: 0.003
    cost_per_output_token: 0.015

# LLM Flow: Defines the routing logic
flows:
  chat_with_routing:
    steps:
      # Step 0: Extract and count tokens from incoming prompt
      - type: "extract_text"
        name: "extract_prompt"
        config:
          source: "body.messages[0].content"  # JMESPath
          slot: 1                              # ByteSlots[1]

      - type: "count_tokens"
        name: "count_prompt_tokens"
        config:
          text_slot: 1
          result_slot: 0                        # IntSlots[0] = token count

      # Step 1: Classify based on token count
      - type: "route_llm"
        name: "classifier"
        config:
          rules:
            # Small prompts → cheap fast model
            - condition: "token_count < 1000"
              model: "gpt-4o-mini"

            # Medium prompts → balanced model
            - condition: "token_count >= 1000 AND token_count < 5000"
              model: "gpt-4o"

            # Large prompts + premium tenant → best model
            - condition: "token_count >= 5000 AND meta == premium"
              model: "claude-opus"

          default: "gpt-4o"                     # fallback
          result_slot: 2                        # ByteSlots[2] = model slug
          token_slot: 0                         # read from IntSlots[0]
          meta_slot: 3                          # ByteSlots[3] = tenant meta

      # Step 2: Apply context fitting (truncate if needed)
      - type: "llm_context_fit"
        name: "fit_to_model"
        config:
          model_slot: 2                         # read model from step 1
          prompt_slot: 1
          max_tokens_slot: 10                   # write final max_tokens here
          strategy: "sliding_window"            # keep most recent N tokens

      # Step 3: Filter tools (optional)
      - type: "llm_tool_filter"                 # hypothetical; see note below
        name: "filter_tools"
        config:
          # Only include if tenant has access
          tools_slot: 4
          access_level_slot: 5
          output_slot: 6

      # Step 4: Call the LLM
      - type: "llm_call"
        name: "invoke_llm"
        config:
          model_slot: 2                         # dynamic model from routing
          prompt_slot: 1                        # user prompt
          system_slot: 7                        # system message (if any)
          result_slot: 8                        # response text
          max_tokens_slot: 10                   # from context_fit

          # Token tracking
          input_tokens_slot: 11
          output_tokens_slot: 12
          stop_reason_slot: 13

          # API key per tenant (optional)
          api_key_slot: 14

          # Fallback chain if primary fails
          fallback_chain:
            - model: "gpt-4o"
            - model: "gpt-4o-mini"

      # Step 5: Format response (with metadata)
      - type: "llm_format_response"
        name: "format_response"
        config:
          llm_response_slot: 8
          client_response_slot: 9
          metadata:
            classifier_model: 2                  # slot index
            input_tokens: 11
            output_tokens: 12
            latency_ms: -1                       # auto-filled by observability
```

---

## Step 2: Slot Allocation (Data Plane)

RAH uses **slots** to carry data through the pipeline. Allocate them once in your context:

```go
// Internal allocation (in compiler or config):
slotAllocation := map[string]int{
    // Extraction
    "prompt_text":           0,    // ByteSlots[0]
    "token_count":           0,    // IntSlots[0]

    // Routing
    "model_slug":            2,    // ByteSlots[2]
    "tenant_meta":           3,    // ByteSlots[3]

    // Transformation
    "filtered_tools":        4,    // ByteSlots[4]
    "access_level":          5,    // ByteSlots[5]
    "final_tools":           6,    // ByteSlots[6]
    "system_prompt":         7,    // ByteSlots[7]

    // LLM Response
    "llm_response":          8,    // ByteSlots[8]
    "client_response":       9,    // ByteSlots[9]
    "final_max_tokens":      10,   // IntSlots[10]
    "input_tokens":          11,   // IntSlots[11]
    "output_tokens":         12,   // IntSlots[12]
    "stop_reason":           13,   // ByteSlots[13]
    "api_key_runtime":       14,   // ByteSlots[14]
}
```

---

## Step 3: Observability & Logging

RAH provides per-instruction timing and metrics. Here's how to extract observability:

### Enable Per-Instruction Timing

In your main startup:

```go
// Enable observability
observability.EnableInstructionTiming()  // default: disabled

// Per-request, after execution completes:
metrics := ctx.Metrics()  // or state.Metrics()
```

### Log Everything

After `engine.Execute()` returns, RAH populates metrics. Extract and log:

```go
type RequestTrace struct {
    // Client Request
    RequestID       string            `json:"request_id"`
    IncomingPrompt  string            `json:"incoming_prompt"`
    TokenCount      int               `json:"token_count"`

    // Classification
    ClassifierName  string            `json:"classifier_name"`
    RoutingRule     string            `json:"routing_rule_matched"`

    // Model Selection
    SelectedModel   string            `json:"selected_model"`
    ModelProvider   string            `json:"model_provider"`

    // Request Transform
    TransformedPrompt   string        `json:"transformed_prompt"`
    FinalMaxTokens      int           `json:"final_max_tokens"`
    IncludedTools       []string      `json:"included_tools"`
    SystemPrompt        string        `json:"system_prompt"`
    Temperature         float64       `json:"temperature"`

    // LLM Execution
    LLMResponse         string        `json:"llm_response"`
    InputTokensUsed     int           `json:"input_tokens_used"`
    OutputTokensUsed    int           `json:"output_tokens_used"`
    StopReason          string        `json:"stop_reason"`
    LLMLatencyMs        int           `json:"llm_latency_ms"`

    // Response to Client
    ClientResponse      string        `json:"client_response"`
    HttpStatus          int           `json:"http_status"`
    TotalLatencyMs      int           `json:"total_latency_ms"`

    // Cost (optional)
    EstimatedCostUSD    float64       `json:"estimated_cost_usd"`
    CostBreakdown       map[string]float64 `json:"cost_breakdown,omitempty"`
}
```

### Extraction from Context

After execution, populate the trace:

```go
trace := RequestTrace{
    RequestID:      string(ctx.ByteSlots[internalTxIDSlot]),
    IncomingPrompt: string(ctx.ByteSlots[0]),  // extract_prompt
    TokenCount:     ctx.IntSlots[0],            // count_tokens result
    SelectedModel:  string(ctx.ByteSlots[2]),   // route_llm result
    LLMResponse:    string(ctx.ByteSlots[8]),   // llm_call response
    InputTokensUsed:    ctx.IntSlots[11],       // from llm_call
    OutputTokensUsed:   ctx.IntSlots[12],
    StopReason:         string(ctx.ByteSlots[13]),
}

// Calculate cost
trace.EstimatedCostUSD = calculateCost(
    trace.SelectedModel,
    trace.InputTokensUsed,
    trace.OutputTokensUsed,
)

// Log as structured JSON
logger.Info("llm_request_complete", trace)
```

---

## Step 4: Request/Response Transformation

The pipeline transforms requests at multiple points:

### A. Token Counting (Step 0)

**What it does**: Estimate token count without calling LLM.

**Instruction**: `count_tokens` (built-in)

**How**:
```yaml
- type: "count_tokens"
  config:
    model: "gpt-4o-mini"        # use this model's tokenizer
    text_slot: 1                 # read prompt from ByteSlots[1]
    result_slot: 0               # write count to IntSlots[0]
```

### B. Context Fitting (Step 2)

**What it does**: If prompt + system + tools > model context window, truncate intelligently.

**Instruction**: `llm_context_fit` (built-in)

**How**:
```yaml
- type: "llm_context_fit"
  config:
    model_slot: 2                # dynamic model to fit for
    prompt_slot: 1               # source prompt
    system_slot: 7               # system message
    tools_slot: 4                # tools array
    max_tokens_needed: 1024      # reserve for response
    strategy: "sliding_window"   # keep most recent N tokens
    output_slot: 10              # write adjusted max_tokens
    truncated_marker_slot: 15    # ByteSlots[15] = "truncated" if trimmed
```

**Strategies**:
- `"sliding_window"`: Keep most recent tokens (usually best for chat)
- `"prefix"`: Keep first N tokens (good for docs)
- `"hybrid"`: Keep first 20%, recent 80% (balance)

### C. Tool Filtering (Step 3)

**What it does**: Remove tools not available for tenant tier.

**Custom instruction** (you may need to add if not present):

```go
// internal/engine/steps/llm_tool_filter.go
func LLMToolFilter(cfg ToolFilterConfig) engine.Instruction {
    return engine.Instruction{
        Name: "llm_tool_filter",
        Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
            toolsJSON := ctx.ByteSlots[cfg.ToolsSlot]
            accessLevel := string(ctx.ByteSlots[cfg.AccessLevelSlot])

            var tools []ToolDef
            json.Unmarshal(toolsJSON, &tools)

            // Filter based on access level
            var filtered []ToolDef
            for _, t := range tools {
                if isAllowed(t.Name, accessLevel) {
                    filtered = append(filtered, t)
                }
            }

            // Write filtered JSON back
            out, _ := json.Marshal(filtered)
            dst := ctx.Alloc(len(out))
            copy(dst, out)
            ctx.ByteSlots[cfg.OutputSlot] = dst

            return state.PC + 1
        },
    }
}
```

---

## Step 5: Complete Flow Example

### YAML Configuration

```yaml
flows:
  chat_with_smart_routing:
    description: "Production chat with classifier, router, and full observability"

    # Slot assignments for this flow
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
          name: "tenant_meta"
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
      # Extract user prompt
      - id: "extract"
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

      # Classify and route
      - id: "classify"
        type: "route_llm"
        config:
          token_slot: 0
          meta_slot: 3
          output_slot: 2
          rules:
            - condition: "token_count < 1000"
              model: "gpt-4o-mini"
            - condition: "token_count >= 1000 AND token_count < 5000"
              model: "gpt-4o"
            - condition: "meta == premium"
              model: "claude-opus"
          default: "gpt-4o"

      # Fit to context window
      - id: "context_fit"
        type: "llm_context_fit"
        config:
          model_slot: 2
          input_slot: 0
          max_tokens_output: 10
          strategy: "sliding_window"

      # Call LLM
      - id: "invoke"
        type: "llm_call"
        config:
          model_slot: 2
          prompt_slot: 0
          output_slot: 8
          input_tokens_output: 11
          output_tokens_output: 12
          timeout_ms: 30000
          max_retries: 2
          fallback_chain:
            - model: "gpt-4o"
            - model: "gpt-4o-mini"

      # Format and return response
      - id: "format"
        type: "format_response"
        config:
          response_slot: 8
          include_metadata: true
          metadata_fields:
            - model
            - input_tokens
            - output_tokens
            - latency_ms
```

---

## Step 6: Observability Endpoints

Add to your HTTP handler to expose observability:

### Metrics Endpoint

```go
// GET /metrics?flow=chat_with_smart_routing
func handleMetrics(w http.ResponseWriter, r *http.Request) {
    flowName := r.URL.Query().Get("flow")

    // Aggregate metrics from all requests
    metrics := observability.GetFlowMetrics(flowName)

    response := map[string]interface{}{
        "flow": flowName,
        "instructions": map[string]interface{}{
            "extract": {
                "count": metrics.Extract.Count,
                "avg_duration_ns": metrics.Extract.AvgDurationNs,
            },
            "tokenize": {
                "count": metrics.Tokenize.Count,
                "avg_duration_ns": metrics.Tokenize.AvgDurationNs,
            },
            "classify": {
                "count": metrics.Classify.Count,
                "avg_duration_ns": metrics.Classify.AvgDurationNs,
                "model_distribution": map[string]int{
                    "gpt-4o-mini": metrics.ModelCount["gpt-4o-mini"],
                    "gpt-4o": metrics.ModelCount["gpt-4o"],
                    "claude-opus": metrics.ModelCount["claude-opus"],
                },
            },
            "invoke": {
                "count": metrics.Invoke.Count,
                "avg_latency_ms": metrics.Invoke.AvgLatencyMs,
                "total_input_tokens": metrics.Invoke.TotalInputTokens,
                "total_output_tokens": metrics.Invoke.TotalOutputTokens,
            },
        },
        "cost": {
            "total_usd": metrics.TotalCostUSD,
            "by_model": metrics.CostByModel,
        },
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(response)
}
```

### Request Log Endpoint

```go
// GET /logs?limit=100&flow=chat_with_smart_routing
func handleRequestLogs(w http.ResponseWriter, r *http.Request) {
    limit := 100
    flowName := r.URL.Query().Get("flow")

    logs := observability.GetRequestLogs(flowName, limit)

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(logs)
}
```

---

## Step 7: Testing the Router

### Test Case 1: Small Prompt → gpt-4o-mini

```bash
curl -X POST http://localhost:8080/api/chat \
  -H "Content-Type: application/json" \
  -H "X-Tenant: acme-corp" \
  -d '{
    "messages": [
      {"role": "user", "content": "What is 2+2?"}
    ]
  }'

# Expected:
# {
#   "response": "4",
#   "metadata": {
#     "classifier_model": "gpt-4o-mini",
#     "input_tokens": 10,
#     "output_tokens": 2,
#     "latency_ms": 450
#   }
# }
```

### Test Case 2: Large Prompt + Premium → claude-opus

```bash
curl -X POST http://localhost:8080/api/chat \
  -H "Content-Type: application/json" \
  -H "X-Tenant: premium-customer" \
  -d '{
    "messages": [
      {"role": "user", "content": "[10,000 token prompt here...]"}
    ]
  }'

# Expected classifier selection: claude-opus
```

### Test Case 3: Token Cost Comparison

```bash
# Check metrics
curl http://localhost:8080/metrics?flow=chat_with_smart_routing | jq '.cost'

# Expected output:
# {
#   "total_usd": 2.34,
#   "by_model": {
#     "gpt-4o-mini": 0.12,
#     "gpt-4o": 1.45,
#     "claude-opus": 0.77
#   }
# }
```

---

## Step 8: Production Considerations

### Cost Optimization

1. **Tuning thresholds**: Monitor cost/model distribution; adjust token thresholds
2. **Batching**: Use `llm_execute_plan` for multi-request batches
3. **Caching**: Layer a cache before classification to skip repeated prompts
4. **Fallback strategy**: Always have cheap fallback for expensive models

### Reliability

1. **Timeout tuning**: Start with 30s, reduce after profiling
2. **Retry logic**: 2-3 retries recommended; handle rate limits gracefully
3. **Fallback chain**: Always include at least one fallback model
4. **Circuit breaker**: Monitor model errors; auto-fallback if >5% error rate

### Observability

1. **Alerting**: Alert if any model error rate > 5%
2. **Dashboard**: Track model selection distribution over time
3. **Cost tracking**: Daily cost rollup by tenant/model
4. **SLA monitoring**: P99 latency per model

---

## Related Files

- **Routing step**: `internal/engine/steps/llm_routing.go`
- **LLM call step**: `internal/engine/steps/llm.go`
- **Context fitting**: `internal/engine/steps/llm_context_fit.go`
- **Token counting**: Built into `llm.go` (uses Anthropic tokenizer)
- **Observability**: `internal/observability/observability.go`
- **Config types**: `internal/config/llm_config.go`

---

## Quick Reference: Common Routing Rules

```yaml
# Cost optimization (all tenants)
rules:
  - condition: "token_count < 1000"
    model: "gpt-4o-mini"
  - condition: "token_count >= 1000 AND token_count < 5000"
    model: "gpt-4o"
  - condition: "token_count >= 5000"
    model: "claude-opus"

# Tenant-based tiering
rules:
  - condition: "meta == gold"
    model: "claude-opus"
  - condition: "meta == silver"
    model: "gpt-4o"
  - condition: "meta == bronze"
    model: "gpt-4o-mini"

# Hybrid: cost + quality
rules:
  - condition: "token_count < 500 AND meta == bronze"
    model: "gpt-4o-mini"
  - condition: "token_count < 500 AND meta == gold"
    model: "gpt-4o"
  - condition: "token_count >= 500 AND meta == gold"
    model: "claude-opus"
  - condition: "token_count >= 500"
    model: "gpt-4o"

# Time-based (off-peak = cheaper)
# (Note: requires external time slot injection)
rules:
  - condition: "is_offpeak == true"
    model: "gpt-4o-mini"
  - condition: "is_peak == true AND meta == gold"
    model: "claude-opus"
```

---

## Supported Providers

This router supports all major LLM providers:

- **OpenAI** - gpt-4o, gpt-4o-mini, gpt-4-turbo, o1 models
- **Anthropic** - Claude Opus, Claude Sonnet, Claude Haiku
- **Google** - Gemini Flash, Gemini Pro, Gemini Mini
- **Ollama** - Local LLM hosting (Gemma, Llama, Mistral, etc.)
- **DeepSeek** - DeepSeek API models
- **AWS Bedrock** - Multi-model access via Anthropic adapter

All models are registered in the `models:` section of the config and referenced by slug in routing rules.

---

## FAQ

**Q: How do I add a new model?**
A: Add to `models:` section in config, use slug in routing rules.

**Q: Can I override the model at request time?**
A: Yes, use `model_slot` in `llm_call` config to read model from ByteSlots at runtime.

**Q: How do I handle tool filtering?**
A: Use `llm_tool_filter` step (custom or built-in if available) before `llm_call`.

**Q: What if a model fails?**
A: Use `fallback_chain` in `llm_call`; automatic retry up to `max_retries`.

**Q: How do I measure cost?**
A: Extract `input_tokens` + `output_tokens` from slots, multiply by model's pricing config.

**Q: Can I use multiple classifiers?**
A: Yes, chain multiple `route_llm` steps with different rules; later rules can override earlier ones.

