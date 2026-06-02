# RAH as an AI Gateway

RAH transforms how you deploy and manage AI-powered applications. By running LLM calls through RAH, you get cost controls, request caching, multi-provider routing, security enforcement, rate limiting, and production observability—without changing a line of application code.

## Why an AI Gateway

Deploying LLMs directly from your application creates operational friction:

- **Cost sprawl**: Each client calls the LLM independently; no visibility into spending
- **No caching**: Identical requests hit the LLM repeatedly; wasted tokens and latency
- **Single provider lock-in**: Switching from Claude to GPT-4 means redeploying code
- **Rate limit chaos**: Clients fight over quota; no fair allocation
- **Security gaps**: API keys scattered across services; no audit trail
- **Latency blind spots**: You see response time, not LLM time vs. gateway time vs. network

RAH solves all of this:

| Problem | RAH Solution |
|---------|--------------|
| Cost opacity | Per-tenant token tracking; daily/monthly quotas; cost reports |
| Cache misses | Request caching and semantic similarity caching |
| Provider lock-in | Swap models mid-flight; fallback chains; A/B testing |
| Rate limit thrashing | Fair-queueing by tenant tier; backpressure handling |
| Key sprawl | Centralized key management; rotate without redeploying |
| Audit blind spots | Every request logged with user, tokens, model, cost |
| Latency guessing | Per-step timing breakdown in traces |

## Registering LLM Models

### Via Studio (UI)

1. Open RAH Studio → **AI** → **Models**
2. Click **Add Model**
3. Fill in:
   - **Provider**: Anthropic, OpenAI, Google Gemini, Ollama, AWS Bedrock
   - **Alias**: How you'll reference this model in flows (e.g., `claude`, `gpt4`)
   - **Model ID**: Provider's official model name (e.g., `claude-opus-4-6`)
   - **API Key**: Enter directly or reference an environment variable
4. Click **Test** to verify connectivity

### Via REST API

Register Anthropic:
```bash
curl -X POST http://localhost:8081/ai/llm/models \
  -H "Content-Type: application/json" \
  -d '{
    "alias": "claude",
    "provider": "anthropic",
    "model": "claude-opus-4-6",
    "api_key_ref": "env://ANTHROPIC_API_KEY"
  }'
```

Register OpenAI:
```bash
curl -X POST http://localhost:8081/ai/llm/models \
  -H "Content-Type: application/json" \
  -d '{
    "alias": "gpt4",
    "provider": "openai",
    "model": "gpt-4o",
    "api_key_ref": "env://OPENAI_API_KEY"
  }'
```

Register local Ollama:
```bash
curl -X POST http://localhost:8081/ai/llm/models \
  -H "Content-Type: application/json" \
  -d '{
    "alias": "local-llama",
    "provider": "ollama",
    "model": "llama3.2",
    "base_url": "http://ollama:11434"
  }'
```

Register AWS Bedrock:
```bash
curl -X POST http://localhost:8081/ai/llm/models \
  -H "Content-Type: application/json" \
  -d '{
    "alias": "bedrock-claude",
    "provider": "bedrock",
    "model": "anthropic.claude-3-sonnet-20240229-v1:0",
    "region": "us-east-1",
    "aws_role_arn": "arn:aws:iam::ACCOUNT:role/RahBedrockRole"
  }'
```

List registered models:
```bash
curl http://localhost:8081/ai/llm/models
```

## Basic LLM Call in a Flow

Create a flow that accepts a prompt and returns LLM output:

```yaml
flows:
  - name: simple-llm-call
    action: upsert
    code: |
      prompt = header("X-Prompt")
      response = llm(prompt, model: "claude", max_tokens: "2000")
      return(200, response)

apis:
  - name: simple-prompt-api
    path: /prompt
    method: POST
    flow_name: simple-llm-call
    action: upsert
```

Call it:
```bash
curl -X POST http://localhost:8081/prompt \
  -H "X-Prompt: What is machine learning?"
```

## Multi-turn Conversations

Build a stateful conversation API where history persists across requests:

```yaml
flows:
  - name: chat-multi-turn
    action: upsert
    code: |
      # Extract request data
      session_id = header("X-Session-ID")
      user_message = body("message")
      
      # Load conversation history from cache
      history = cache.get("conversation:{session_id}")
      
      # If no history, create empty message array
      if (history == null) {
        history = "[]"
      }
      
      # Append user message to history
      append_message(history, role: "user", content: user_message)
      
      # Call LLM with full history
      response = llm(history, 
        model: "claude",
        max_tokens: "2000")
      
      # Append assistant response to history
      append_message(history, role: "assistant", content: response)
      
      # Save updated history (7-day TTL)
      cache.set("conversation:{session_id}", history, ttl: 604800)
      
      return(200, response)

apis:
  - name: chat-api
    path: /chat
    method: POST
    flow_name: chat-multi-turn
    action: upsert
```

Call it (first message):
```bash
curl -X POST http://localhost:8081/chat \
  -H "X-Session-ID: user-123" \
  -d '{"message": "What is the capital of France?"}'
```

Call it (second message—history is preserved):
```bash
curl -X POST http://localhost:8081/chat \
  -H "X-Session-ID: user-123" \
  -d '{"message": "Tell me about its history."}'
```

## Fallback Chains

Automatically retry with a different model if the primary one fails (timeout, rate limit, error):

```yaml
flows:
  - name: chat-with-fallback
    action: upsert
    code: |
      prompt = body("message")
      
      # Try Claude; fall back to GPT-4 if it fails
      response = llm(prompt,
        model: "claude",
        fallback_model: "gpt4",
        max_tokens: "2000",
        timeout: "10s")
      
      return(200, response)

apis:
  - name: chat-fallback-api
    path: /chat/fallback
    method: POST
    flow_name: chat-with-fallback
    action: upsert
```

If Claude times out or returns an error, RAH automatically retries with GPT-4 without the client knowing.

## Model Routing

Route different requests to different models based on tenant tier, input size, or custom logic:

```yaml
flows:
  - name: smart-model-routing
    action: upsert
    code: |
      prompt = body("message")
      
      # Look up tenant properties (set during tenant registration)
      tier = registry.meta("tier")
      
      # Count tokens in prompt
      estimate_tokens(prompt, into: token_count)
      
      # Route based on tier and size
      if (tier == "premium") {
        selected_model = "claude-opus"
      } else if (token_count > 50000) {
        selected_model = "claude-haiku"
      } else {
        selected_model = "gpt-4o-mini"
      }
      
      # Call LLM using selected model
      response = llm(prompt,
        model_slot: selected_model,
        max_tokens: "2000")
      
      return(200, response)

apis:
  - name: smart-routing-api
    path: /chat/smart
    method: POST
    flow_name: smart-model-routing
    action: upsert
```

Set tenant properties via REST:
```bash
curl -X POST http://localhost:8081/tenants/my-customer/properties \
  -d '{"tier": "premium"}'
```

## Semantic Caching

Cache LLM responses based on semantic similarity, not exact string match. Useful for queries like "What is ML?" and "Tell me about machine learning?"—they should hit the same cache entry.

```yaml
flows:
  - name: semantic-cache-llm
    action: upsert
    code: |
      prompt = body("message")
      
      # Generate embedding of the prompt
      embed_text(prompt, model: "text-embedding-3", into: query_embed)
      
      # Check semantic cache (0.95 = 95% similarity threshold)
      cached_response = null
      cache_hit = "false"
      semantic_cache_get(
        query_embed,
        store: "llm-responses",
        min_score: "0.95",
        into: cached_response,
        hit: cache_hit)
      
      if (cache_hit == "true") {
        return(200, cached_response)
      }
      
      # Not in cache; call LLM
      response = llm(prompt,
        model: "claude",
        max_tokens: "2000")
      
      # Store result in semantic cache
      semantic_cache_put(
        query_embed,
        response,
        store: "llm-responses",
        ttl: "3600")
      
      return(200, response)

apis:
  - name: semantic-cache-api
    path: /ask
    method: POST
    flow_name: semantic-cache-llm
    action: upsert
```

Call it multiple times; requests with similar meanings will reuse cached responses.

## Cost Management

Monitor and enforce token/cost budgets per tenant. Essential for shared services where one customer shouldn't burn everyone's budget.

### Cost Guards in Flows

```yaml
flows:
  - name: cost-controlled-llm
    action: upsert
    code: |
      tenant_id = header("X-Tenant-ID")
      prompt = body("message")
      
      # Enforce daily budget ($10/day)
      enforce_cost_budget(
        tenant_id: tenant_id,
        daily_limit: "10.00")
      
      # Estimate tokens before calling (optional)
      estimate_tokens(prompt, into: estimated_tokens)
      
      # Call LLM
      response = llm(prompt,
        model: "claude",
        input_tokens_variable: input_tok,
        output_tokens_variable: output_tok,
        max_tokens: "2000")
      
      # Calculate and record actual cost
      calculate_cost(
        model: "claude",
        input_tokens: input_tok,
        output_tokens: output_tok,
        into: cost_usd)
      
      record_cost(
        tenant_id: tenant_id,
        cost: cost_usd)
      
      return(200, response)

apis:
  - name: cost-controlled-api
    path: /chat/budget
    method: POST
    flow_name: cost-controlled-llm
    action: upsert
```

### Setting Tenant Quotas

```bash
# Set daily and monthly limits
curl -X POST http://localhost:8081/ai/quotas \
  -d '{
    "tenant_id": "customer-42",
    "daily_limit_usd": 10.00,
    "monthly_limit_usd": 200.00
  }'

# View usage
curl http://localhost:8081/ai/quotas/customer-42/usage

# View cost report
curl http://localhost:8081/api/v1/costs?tenant=customer-42&start=2024-06-01&end=2024-06-30
```

When a tenant hits their budget, the `enforce_cost_budget` step either blocks further calls or defers them based on the flow's configuration.

## Prompt Safety

Scan prompts for PII and injection attacks before sending to the LLM:

```yaml
flows:
  - name: safe-llm-call
    action: upsert
    code: |
      user_prompt = body("message")
      
      # Sanitize: strip PII and reject injection attempts
      clean_prompt = null
      sanitize_prompt(
        input: user_prompt,
        mode: "strip",
        rules: "pii,injection",
        into: clean_prompt)
      
      # Call LLM with clean prompt
      response = llm(clean_prompt,
        model: "claude",
        max_tokens: "2000")
      
      return(200, response)

apis:
  - name: safe-api
    path: /chat/safe
    method: POST
    flow_name: safe-llm-call
    action: upsert
```

Options:
- **mode: "strip"** — Remove PII; keep prompt otherwise valid
- **mode: "reject"** — Return error if PII or injection detected
- **mode: "flag"** — Allow through but set a flag in the response

Rules:
- **"pii"** — Detect emails, phone numbers, SSN, credit cards
- **"injection"** — Block SQL injection, prompt injection patterns
- **"malware"** — Block suspicious binary/code patterns

## RAG (Retrieval-Augmented Generation)

Augment LLM responses with context from your knowledge base:

```yaml
flows:
  - name: rag-qa
    action: upsert
    code: |
      user_query = body("question")
      
      # Embed the question
      embed_text(
        user_query,
        model: "text-embedding-3",
        into: query_embed)
      
      # Search knowledge base (vector store)
      # Returns top 5 results with similarity >= 0.7
      search_results = vector_search(
        query_embed,
        store: "knowledge-base",
        top_k: "5",
        min_score: "0.7")
      
      # Build context from search results
      context = concat(
        "Use this context to answer the question:\n",
        search_results)
      
      # Construct full prompt
      full_prompt = concat(
        context,
        "\n\nQuestion: ",
        user_query)
      
      # Get answer with context
      answer = llm(
        full_prompt,
        model: "claude",
        max_tokens: "1000")
      
      return(200, answer)

apis:
  - name: rag-api
    path: /ask-docs
    method: POST
    flow_name: rag-qa
    action: upsert
```

Populate the knowledge base by uploading documents:
```bash
curl -X POST http://localhost:8081/ai/vectorstore/knowledge-base/upsert \
  -d '{
    "doc_id": "doc-123",
    "text": "RAH is a high-performance API gateway...",
    "source": "docs/README.md"
  }'
```

## Streaming Responses (Server-Sent Events)

Stream LLM tokens back to the client in real time instead of waiting for the full response:

```yaml
flows:
  - name: streaming-llm
    action: upsert
    code: |
      prompt = body("message")
      
      # Call LLM with stream: "true"
      token_stream = llm(
        prompt,
        model: "claude",
        max_tokens: "2000",
        stream: "true")
      
      # Send each token as an SSE event
      while (has_tokens(token_stream)) {
        token = next_token(token_stream)
        send_sse_event(
          data: token,
          event: "token")
      }
      
      send_sse_event(
        data: '{"status": "done"}',
        event: "done")

apis:
  - name: streaming-api
    path: /chat/stream
    method: POST
    flow_name: streaming-llm
    action: upsert
```

Call from a browser or curl:
```bash
curl -X POST http://localhost:8081/chat/stream \
  -d '{"message": "Explain quantum computing"}' \
  -N  # -N disables buffering to see tokens in real time
```

## Anthropic API Compatibility

Point any Anthropic SDK client directly at RAH. No code changes needed:

```bash
# Set environment variable
export ANTHROPIC_BASE_URL=http://localhost:8081/ai
export ANTHROPIC_API_KEY=your-gateway-token

# Your existing Python code works unchanged
python -c "
from anthropic import Anthropic
client = Anthropic()
msg = client.messages.create(
  model='claude-opus-4-6',
  max_tokens=1024,
  messages=[{'role': 'user', 'content': 'Hello'}]
)
print(msg.content[0].text)
"
```

Benefits:
- All LLM calls flow through RAH (logging, cost tracking, rate limits)
- Swap models without code changes
- Use cache, semantic caching, and fallbacks transparently

## OpenAI API Compatibility

Similarly, point OpenAI SDK clients at RAH:

```bash
export OPENAI_BASE_URL=http://localhost:8081/ai
export OPENAI_API_KEY=your-gateway-token

python -c "
from openai import OpenAI
client = OpenAI()
resp = client.chat.completions.create(
  model='gpt-4o',
  messages=[{'role': 'user', 'content': 'Hello'}]
)
print(resp.choices[0].message.content)
"
```

The gateway handles the protocol translation and cost tracking for you.

## Observability for AI

Every LLM call is automatically tracked and observable:

### View in Studio

1. Open RAH Studio → **Observability** → **Traces**
2. Filter by:
   - **Tenant**: View costs per customer
   - **Model**: See which models are being used
   - **Time range**: Daily/weekly/monthly trends
3. Click any trace to see:
   - Full prompt and response
   - Token counts (input, output, cache_read)
   - Latency breakdown (LLM response time vs. gateway overhead)
   - Cost calculated
   - Any errors or retries

### REST API for Cost Reports

```bash
# Daily costs by model
curl http://localhost:8081/api/v1/costs/by-model?start=2024-06-01&end=2024-06-30

# Costs by tenant
curl http://localhost:8081/api/v1/costs/by-tenant?start=2024-06-01&end=2024-06-30

# Cost details for a single tenant
curl http://localhost:8081/api/v1/costs?tenant=customer-42&start=2024-06-01&end=2024-06-30
```

### Ingest Events

All activity is sent to the ingest pipeline. Access raw events:
```bash
# View recent events (requires ingest backend configured)
curl http://localhost:8081/api/v1/events?kind=llm_request&limit=100
```

Event kinds:
- **llm_request** — LLM call initiated
- **llm_response** — LLM response received
- **cache_hit** — Semantic/request cache hit
- **token_count** — Input/output token breakdown
- **cost** — Cost calculated and recorded

## Supported Providers

| Provider | ID | Models | Notes |
|----------|---|----|-------|
| **Anthropic** | `anthropic` | claude-opus-4-6, claude-sonnet-4-6, claude-haiku-4-5 | Official API; fast and reliable |
| **OpenAI** | `openai` | gpt-4o, gpt-4o-mini, o1, o3-mini | Full API parity; streaming supported |
| **Google Gemini** | `gemini` | gemini-2.0-flash, gemini-1.5-pro, gemini-1.5-flash | Long context; vision support |
| **AWS Bedrock** | `bedrock` | Claude (Anthropic), Titan, Llama | Runs in AWS account; IAM auth |
| **Ollama** (local) | `ollama` | llama3.2, mistral, neural-chat, phi-4 | Run locally; no internet required |

### Adding a New Provider

Contact the RAH team or open a GitHub issue with:
- Provider API documentation
- Required authentication method
- Model pricing (if applicable)
- Use case description

---

**Next steps**: Read [Building Agentic Workflows](./agentic-workflows.md) to learn how to build multi-step AI agents that take actions via tools.
