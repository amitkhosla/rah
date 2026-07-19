# LLM Router Architecture Diagram

## Complete Request Flow

```
╔════════════════════════════════════════════════════════════════════════════╗
║                           CLIENT REQUEST                                   ║
║                        (HTTP POST /api/chat)                               ║
║                                                                             ║
║  Headers: X-Tenant: acme-corp, X-Tenant-Tier: premium                     ║
║  Body: {                                                                    ║
║    messages: [                                                              ║
║      {role: "user", content: "Analyze this document..."}                   ║
║    ],                                                                        ║
║    tools: [                                                                 ║
║      {name: "web_search", ...},                                            ║
║      {name: "code_execution", ...}                                         ║
║    ]                                                                         ║
║  }                                                                          ║
└────────────────────────────┬─────────────────────────────────────────────────┘
                             ▼
┌────────────────────────────────────────────────────────────────────────────┐
│                     INSTRUCTION 0: EXTRACT                                  │
│                   (extract_prompt, extract_tools)                           │
├────────────────────────────────────────────────────────────────────────────┤
│  Action:                                                                     │
│    - Read "messages[0].content" from body                                   │
│    - Write to ByteSlots[0] = "Analyze this document..."                    │
│    - Read "tools" from body                                                 │
│    - Write to ByteSlots[4] = JSON-encoded tools array                      │
│                                                                             │
│  Context After:                                                             │
│    ByteSlots[0] = "Analyze this document..."                               │
│    ByteSlots[4] = "[{name: web_search, ...}, {name: code_ex, ...}]"      │
│                                                                             │
│  Latency: ~100ns (string copy)                                             │
└────────────────────────────┬─────────────────────────────────────────────────┘
                             ▼
┌────────────────────────────────────────────────────────────────────────────┐
│               INSTRUCTION 1: COUNT TOKENS                                   │
│                  (count_tokens using gpt-4o-mini tokenizer)                │
├────────────────────────────────────────────────────────────────────────────┤
│  Action:                                                                     │
│    - Tokenize ByteSlots[0] using GPT tokenizer                             │
│    - Count = 120 tokens (estimated)                                         │
│    - Write to IntSlots[0] = 120                                             │
│                                                                             │
│  Context After:                                                             │
│    IntSlots[0] = 120                                                        │
│                                                                             │
│  Latency: ~5ms (tokenizer)                                                 │
└────────────────────────────┬─────────────────────────────────────────────────┘
                             ▼
┌────────────────────────────────────────────────────────────────────────────┐
│              INSTRUCTION 2: CLASSIFY & ROUTE (★ SMART ROUTER ★)            │
│                         (route_llm instruction)                             │
├────────────────────────────────────────────────────────────────────────────┤
│  Routing Rules (evaluated in order):                                       │
│                                                                             │
│  Rule 1: IF token_count < 500                                              │
│          THEN use "gpt-4o-mini"                                             │
│          ELSE continue to next rule                                         │
│          ✗ No match (120 tokens < 500? YES) → MODEL = gpt-4o-mini          │
│                                                                             │
│  🎯 SELECTED MODEL: gpt-4o-mini                                             │
│                                                                             │
│  Context After:                                                             │
│    ByteSlots[2] = "gpt-4o-mini"                                             │
│                                                                             │
│  Log: {                                                                     │
│    "classifier": "classify_by_size",                                       │
│    "rule_matched": "token_count < 500",                                    │
│    "selected_model": "gpt-4o-mini",                                        │
│    "reason": "Small prompt, standard tier, cost optimization"              │
│  }                                                                          │
│                                                                             │
│  Latency: ~1us (rule evaluation + string copy)                             │
└────────────────────────────┬─────────────────────────────────────────────────┘
                             ▼
┌────────────────────────────────────────────────────────────────────────────┐
│            INSTRUCTION 3: FIT TO CONTEXT WINDOW                             │
│          (llm_context_fit with sliding_window strategy)                    │
├────────────────────────────────────────────────────────────────────────────┤
│  Model Context Window: gpt-4o-mini = 128,000 tokens                        │
│  Prompt Tokens: 120                                                         │
│  System Tokens: 0 (not used)                                                │
│  Tools Tokens: 50 (estimated)                                               │
│  Total So Far: 170                                                          │
│  Max Output Tokens: 1024 (per config)                                       │
│  Final Used: 170 + 1024 = 1194 tokens (< 128,000)                          │
│                                                                             │
│  ✓ NO TRUNCATION NEEDED                                                    │
│                                                                             │
│  Context After:                                                             │
│    IntSlots[10] = 1024 (final max_tokens)                                  │
│    ByteSlots[15] = "" (not truncated)                                      │
│                                                                             │
│  Latency: ~500ns (arithmetic, no actual truncation)                        │
└────────────────────────────┬─────────────────────────────────────────────────┘
                             ▼
┌────────────────────────────────────────────────────────────────────────────┐
│               INSTRUCTION 4: FILTER TOOLS (optional)                        │
│         (llm_tool_filter: only allow tier-appropriate tools)               │
├────────────────────────────────────────────────────────────────────────────┤
│  Tenant Tier: "premium" (from X-Tenant-Tier header)                        │
│  Requested Tools: [web_search, code_execution]                             │
│  Premium Allowed: [web_search, code_execution, image_generation]           │
│                                                                             │
│  ✓ ALL TOOLS ALLOWED (premium tier has access)                             │
│                                                                             │
│  Context After:                                                             │
│    ByteSlots[6] = "[{name: web_search}, {name: code_execution}]"          │
│                                                                             │
│  Latency: ~5us (JSON filtering)                                            │
└────────────────────────────┬─────────────────────────────────────────────────┘
                             ▼
┌────────────────────────────────────────────────────────────────────────────┐
│                  INSTRUCTION 5: CALL LLM (★ THE MAIN EVENT ★)              │
│               (llm_call to selected model with fallback)                    │
├────────────────────────────────────────────────────────────────────────────┤
│  LLM Call Details:                                                          │
│    Model: gpt-4o-mini (from ByteSlots[2])                                  │
│    Endpoint: https://api.openai.com/v1/chat/completions                   │
│    Prompt: ByteSlots[0] = "Analyze this document..."                      │
│    Tools: ByteSlots[6] = [web_search, code_execution]                     │
│    Max Tokens: IntSlots[10] = 1024                                          │
│    Temperature: 0.7                                                         │
│    Timeout: 30s                                                             │
│    Retries: 2                                                               │
│                                                                             │
│  HTTP Request:                                                              │
│    POST https://api.openai.com/v1/chat/completions                        │
│    Headers:                                                                 │
│      Authorization: Bearer sk-...                                          │
│      Content-Type: application/json                                         │
│    Body:                                                                    │
│      {                                                                      │
│        "model": "gpt-4o-mini",                                              │
│        "messages": [                                                        │
│          {"role": "user", "content": "Analyze this document..."}           │
│        ],                                                                   │
│        "tools": [                                                           │
│          {"type": "function", "function": {"name": "web_search", ...}},   │
│          {"type": "function", "function": {"name": "code_execution", ...}}│
│        ],                                                                   │
│        "max_tokens": 1024,                                                  │
│        "temperature": 0.7                                                   │
│      }                                                                      │
│                                                                             │
│  LLM Response (received in ~800ms):                                         │
│    Status: 200 OK                                                           │
│    Body:                                                                    │
│      {                                                                      │
│        "choices": [{                                                        │
│          "message": {                                                       │
│            "role": "assistant",                                             │
│            "content": "Based on the analysis...<150 tokens>..."            │
│          },                                                                 │
│          "finish_reason": "stop"                                            │
│        }],                                                                  │
│        "usage": {                                                           │
│          "prompt_tokens": 118,                                              │
│          "completion_tokens": 150,                                          │
│          "total_tokens": 268                                                │
│        }                                                                    │
│      }                                                                      │
│                                                                             │
│  Context After:                                                             │
│    ByteSlots[8] = "Based on the analysis...<150 tokens>..."               │
│    IntSlots[11] = 118 (input_tokens_used)                                 │
│    IntSlots[12] = 150 (output_tokens_used)                                │
│    ByteSlots[13] = "stop" (stop_reason)                                    │
│                                                                             │
│  Latency: ~800ms (network + model inference)                               │
└────────────────────────────┬─────────────────────────────────────────────────┘
                             ▼
┌────────────────────────────────────────────────────────────────────────────┐
│              INSTRUCTION 6: FORMAT RESPONSE                                 │
│          (prepare response with metadata for client)                        │
├────────────────────────────────────────────────────────────────────────────┤
│  Gather Metadata:                                                           │
│    - Model: ByteSlots[2] = "gpt-4o-mini"                                   │
│    - Input Tokens: IntSlots[11] = 118                                      │
│    - Output Tokens: IntSlots[12] = 150                                     │
│    - Stop Reason: ByteSlots[13] = "stop"                                   │
│    - Was Truncated: ByteSlots[15] = "" (false)                             │
│                                                                             │
│  Calculate Cost (cost_per_X_token is per-million-token price):             │
│    Input Cost: (118 / 1,000,000) × $0.15 = $0.0000177                     │
│    Output Cost: (150 / 1,000,000) × $0.60 = $0.0000900                    │
│    Total: $0.0001077                                                        │
│                                                                             │
│  Build Response:                                                            │
│    {                                                                        │
│      "content": "Based on the analysis...<150 tokens>...",                │
│      "metadata": {                                                          │
│        "model": "gpt-4o-mini",                                              │
│        "input_tokens": 118,                                                 │
│        "output_tokens": 150,                                                │
│        "stop_reason": "stop",                                               │
│        "was_truncated": false,                                              │
│        "cost_usd": 0.0001077,                                                 │
│        "latency_ms": 810                                                    │
│      }                                                                      │
│    }                                                                        │
│                                                                             │
│  Context After:                                                             │
│    ByteSlots[9] = (formatted response JSON)                                │
│                                                                             │
│  Latency: ~1ms (JSON serialization)                                        │
└────────────────────────────┬─────────────────────────────────────────────────┘
                             ▼
┌────────────────────────────────────────────────────────────────────────────┐
│            INSTRUCTION 7: EMIT TRACE FOR OBSERVABILITY                      │
│                  (emit_event to logging/analytics)                          │
├────────────────────────────────────────────────────────────────────────────┤
│  Emit Structured Event:                                                     │
│    {                                                                        │
│      "event_type": "llm_request_trace",                                     │
│      "timestamp": "2026-04-05T14:30:00Z",                                   │
│      "request_id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",                │
│      "tenant_id": "acme-corp",                                              │
│      "tenant_tier": "premium",                                              │
│                                                                             │
│      "classification": {                                                    │
│        "classifier_name": "classify_by_size",                              │
│        "rule_matched": "token_count < 500",                                │
│        "estimated_tokens": 120                                              │
│      },                                                                     │
│                                                                             │
│      "model_selection": {                                                   │
│        "selected_model": "gpt-4o-mini",                                     │
│        "provider": "openai",                                                │
│        "context_window": 128000,                                            │
│        "routing_decision": "cost_optimization"                              │
│      },                                                                     │
│                                                                             │
│      "request_transformation": {                                            │
│        "original_length": 25,                                               │
│        "was_truncated": false,                                              │
│        "tools_filtered": 0                                                  │
│      },                                                                     │
│                                                                             │
│      "llm_execution": {                                                     │
│        "llm_response": "Based on the analysis...",                         │
│        "input_tokens": 118,                                                 │
│        "output_tokens": 150,                                                │
│        "stop_reason": "stop",                                               │
│        "llm_latency_ms": 800,                                               │
│        "timeout_ms": 30000,                                                 │
│        "max_retries": 2,                                                    │
│        "actual_retries": 0                                                  │
│      },                                                                     │
│                                                                             │
│      "cost": {                                                              │
│        "input_cost": 0.0000177,                                             │
│        "output_cost": 0.0000900,                                            │
│        "total_usd": 0.0001077,                                              │
│        "model_pricing": {                                                   │
│          "model": "gpt-4o-mini",                                            │
│          "cost_per_input_token": 0.00015,                                   │
│          "cost_per_output_token": 0.0006                                    │
│        }                                                                    │
│      },                                                                     │
│                                                                             │
│      "performance": {                                                       │
│        "extract_latency_ns": 100,                                           │
│        "tokenize_latency_ms": 5,                                            │
│        "classify_latency_us": 1,                                            │
│        "context_fit_latency_ns": 500,                                       │
│        "tool_filter_latency_us": 5,                                         │
│        "llm_call_latency_ms": 800,                                          │
│        "format_latency_ms": 1,                                              │
│        "emit_latency_us": 10,                                               │
│        "total_latency_ms": 811,                                             │
│        "gateway_overhead_ms": 11,                                           │
│        "gateway_overhead_pct": 1.3                                          │
│      }                                                                      │
│    }                                                                        │
│                                                                             │
│  Destination:                                                               │
│    - Log file (local)                                                       │
│    - Datadog (production observability)                                     │
│    - BigQuery (analytics warehouse)                                         │
│    - Prometheus (metrics)                                                   │
│                                                                             │
│  Latency: ~10us (serialization + emit)                                     │
└────────────────────────────┬─────────────────────────────────────────────────┘
                             ▼
╔════════════════════════════════════════════════════════════════════════════╗
║                        CLIENT RESPONSE (200 OK)                             ║
║                                                                             ║
║  HTTP Headers:                                                               ║
║    Content-Type: application/json                                           ║
║    X-Request-ID: f47ac10b-58cc-4372-a567-0e02b2c3d479                     ║
║    X-Model-Used: gpt-4o-mini                                                │
║                                                                             ║
║  Response Body:                                                             ║
║  {                                                                          │
║    "content": "Based on the analysis...<150 tokens>...",                  │
║    "metadata": {                                                            │
║      "model": "gpt-4o-mini",                                                │
║      "input_tokens": 118,                                                   │
║      "output_tokens": 150,                                                  │
║      "stop_reason": "stop",                                                 │
║      "was_truncated": false,                                                │
║      "latency_ms": 810,                                                     │
║      "cost_usd": 0.01077                                                    │
║    }                                                                        │
║  }                                                                          │
║                                                                             ║
║  Total Latency: 811ms (800ms LLM + 11ms gateway)                            │
║  Cost Savings: Used gpt-4o-mini ($0.011) instead of claude-opus ($0.150)  │
║               → Saved 92.6% by smart routing!                               │
╚════════════════════════════════════════════════════════════════════════════╝
```

---

## Supported Providers & Examples

The router works with any LLM provider. Here are configuration examples for all supported providers:

### OpenAI
```yaml
gpt-4o-mini:
  provider: "openai"
  adapter: "openai"
  base_url: "https://api.openai.com/v1/chat/completions"
  model_id: "gpt-4o-mini"
  max_tokens: 4096
  cost_per_input_token: 0.00015
  cost_per_output_token: 0.0006
```

### Anthropic
```yaml
claude-opus:
  provider: "anthropic"
  adapter: "anthropic"
  base_url: "https://api.anthropic.com/v1/messages"
  model_id: "claude-opus-4-1"
  max_tokens: 4096
  cost_per_input_token: 0.015
  cost_per_output_token: 0.075
```

### Google Gemini
```yaml
gemini-flash:
  provider: "google"
  adapter: "gemini"
  base_url: "https://generativelanguage.googleapis.com/v1beta"
  model_id: "gemini-2.0-flash"
  max_tokens: 4096
  cost_per_input_token: 0.000075
  cost_per_output_token: 0.0003
```

### Ollama (Local)
```yaml
local-llm:
  provider: "ollama"
  adapter: "ollama"
  base_url: "http://localhost:11434"
  model_id: "llama3.2"
  max_tokens: 2048
  cost_per_input_token: 0.0
  cost_per_output_token: 0.0
```

### DeepSeek
```yaml
deepseek-chat:
  provider: "deepseek"
  adapter: "deepseek"
  base_url: "https://api.deepseek.com"
  model_id: "deepseek-chat"
  max_tokens: 4096
  cost_per_input_token: 0.0001
  cost_per_output_token: 0.0002
```

### AWS Bedrock
```yaml
claude-bedrock:
  provider: "anthropic"
  adapter: "bedrock"
  base_url: "us-east-1"  # AWS region
  model_id: "anthropic.claude-3-5-sonnet-20241022-v2:0"
  max_tokens: 4096
  cost_per_input_token: 0.003
  cost_per_output_token: 0.015
```

---

## Slot Memory Layout

```
┌─ BYTESLOTS ────────────────────────────────────────┐
│ [0]  prompt_text          "Analyze this..."        │
│ [2]  model_slug           "gpt-4o-mini"            │
│ [3]  tenant_meta          "premium"                │
│ [4]  incoming_tools       JSON array               │
│ [6]  filtered_tools       JSON array               │
│ [7]  system_prompt        (empty)                  │
│ [8]  llm_response         "Based on analysis..."   │
│ [9]  client_response      (formatted JSON)         │
│ [13] stop_reason          "stop"                   │
│ [15] was_truncated        "" or "truncated"        │
└─────────────────────────────────────────────────────┘

┌─ INTSLOTS ──────────────────────────────────────────┐
│ [0]  token_count          120                       │
│ [10] final_max_tokens     1024                      │
│ [11] input_tokens_used    118                       │
│ [12] output_tokens_used   150                       │
└─────────────────────────────────────────────────────┘
```

---

## Decision Tree: How Routing Decisions Are Made

```
START
  │
  ├─→ Extract prompt "Analyze this document..."
  │    └─→ Count tokens = 120
  │
  ├─→ Read tenant tier from header = "premium"
  │
  ├─→ Evaluate routing rules:
  │
  │   Rule 1: IF token_count < 500 THEN model = "gpt-4o-mini"
  │   └─→ Is 120 < 500? YES! ✓
  │        └─→ Selected: gpt-4o-mini
  │
  │   (Other rules not evaluated; first match wins)
  │
  └─→ Route to gpt-4o-mini
```

---

## Cost Comparison: What You Save

```
SCENARIO: 120-token prompt from premium user

OPTION A: Always use claude-opus  ($15.00/$75.00 per 1M tokens)
  Cost = (118/1,000,000 × $15.00) + (150/1,000,000 × $75.00) = $0.00177 + $0.01125 = $0.01302

OPTION B: Smart routing → gpt-4o-mini  ($0.15/$0.60 per 1M tokens)
  Cost = (118/1,000,000 × $0.15) + (150/1,000,000 × $0.60) = $0.0000177 + $0.0000900 = $0.0001077

SAVINGS: $0.01302 - $0.0001077 = $0.0129 per request
PERCENTAGE: 99.2% cheaper!

AT 10,000 REQUESTS/DAY:
  Always opus:     $130.20/day
  Smart routing:   $1.08/day
  Daily savings:   $129.12
  Annual savings:  $47,126,500
```

---

## Observability Metrics Dashboard

After deploying the router, monitor these metrics:

```
┌─────────────────────────────────────────────────────────┐
│           Model Selection Distribution (%)              │
├─────────────────────────────────────────────────────────┤
│ gpt-4o-mini      ████████████████░░░░  68%             │
│ gpt-4o           ███████░░░░░░░░░░░░░░  25%             │
│ claude-opus      ██░░░░░░░░░░░░░░░░░░░   7%             │
│ fallback models  ░░░░░░░░░░░░░░░░░░░░░   0%             │
└─────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────┐
│       Token Count Distribution (classified)             │
├─────────────────────────────────────────────────────────┤
│ <500 tokens      ████████████████░░░░  68%             │
│ 500-2000 tokens  ███████░░░░░░░░░░░░░░  25%             │
│ >2000 tokens     ██░░░░░░░░░░░░░░░░░░░   7%             │
└─────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────┐
│      Average Cost per Request (by selected model)       │
├─────────────────────────────────────────────────────────┤
│ gpt-4o-mini      $0.018 ███░░░░░░░░░░░░░░░░░░         │
│ gpt-4o           $0.123 ███████░░░░░░░░░░░░░░         │
│ claude-opus      $0.847 ██████████████░░░░░░░░        │
│ BLENDED AVERAGE  $0.087 ████░░░░░░░░░░░░░░░░░░        │
└─────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────┐
│       Request Latency Breakdown (P50 / P99)             │
├─────────────────────────────────────────────────────────┤
│ Extract              0.1ms / 0.2ms                       │
│ Tokenize             2ms / 5ms                           │
│ Classify             0.001ms / 0.003ms                   │
│ Context Fit          0.5ms / 1ms                         │
│ Tool Filter          5us / 10us                          │
│ LLM Call            800ms / 3500ms  ← Most time!         │
│ Format               1ms / 2ms                           │
│ TOTAL               803ms / 3508ms                       │
│                                                          │
│ Gateway Overhead    1% of total (very efficient!)       │
└─────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────┐
│       Routing Rule Accuracy (hits vs. expectations)     │
├─────────────────────────────────────────────────────────┤
│ Rule: token_count < 500 → gpt-4o-mini                  │
│   Expected hits: 70%   Actual hits: 68%   Accuracy: 97%│
│                                                          │
│ Rule: token_count 500-2000 && tier=standard → gpt-4o   │
│   Expected hits: 20%   Actual hits: 22%   Accuracy: 110%│
│   (Higher = more expensive; might need tuning)          │
│                                                          │
│ Rule: token_count >= 2000 && tier=premium → claude-opus│
│   Expected hits: 5%    Actual hits: 7%    Accuracy: 140%│
│   (Higher = fewer premium users than expected)          │
└─────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────┐
│          Error Rates (by model)                          │
├─────────────────────────────────────────────────────────┤
│ gpt-4o-mini      0.1%  ✓                                │
│ gpt-4o           0.2%  ✓                                │
│ claude-opus      0.3%  ✓                                │
│ Fallback models  2.1%  ⚠ (investigate!)                │
└─────────────────────────────────────────────────────────┘
```

---

## Production Checklist

- [ ] Config file created and tested (`config/rah.yaml`)
- [ ] API keys configured in environment
- [ ] Routing rules tuned for your use case
- [ ] Observability/logging configured (Datadog, CloudWatch, etc.)
- [ ] Metrics dashboard set up
- [ ] Cost calculator implemented
- [ ] Load testing completed
- [ ] Fallback models configured
- [ ] Error handling tested
- [ ] Performance targets validated
- [ ] Cost savings projected
- [ ] Team trained on operation

---

## Related Documentation

- **LLM Router Guide**: `docs/LLM_ROUTER_GUIDE.md` (comprehensive)
- **Quick Start**: `examples/QUICKSTART_LLM_ROUTER.md` (5-minute setup)
- **Config Example**: `examples/llm_router_config.yaml` (ready to use)
- **Handler Code**: `examples/llm_router_handler.go` (observability extraction)
- **RAH Architecture**: `docs/ARCHITECTURE.md` (system design)
