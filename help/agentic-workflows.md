# Building Agentic Workflows

An agentic workflow is a multi-step process where an AI agent doesn't just answer a question—it reasons, decides what actions to take, executes those actions via tools, and iterates based on results. RAH provides first-class support for agentic loops: calling LLMs, dispatching tool calls, executing those tools, feeding results back, and looping until the agent reaches a conclusion.

## The Agentic Loop Pattern

The classic agentic loop follows this cycle:

```
┌─────────────────────────────────────────────────┐
│ 1. User Query / Context                         │
└─────────────┬───────────────────────────────────┘
              │
┌─────────────▼───────────────────────────────────┐
│ 2. Call LLM with available tools                │
│    (Returns: text response OR tool calls)       │
└─────────────┬───────────────────────────────────┘
              │
        ┌─────┴─────┐
        │           │
        ▼           ▼
   [Final Answer] [Tool Calls]
        │           │
        │      ┌────▼──────────────┐
        │      │ 3. Execute Tools  │
        │      │    (Get Results)  │
        │      └────┬──────────────┘
        │           │
        │      ┌────▼──────────────────┐
        │      │ 4. Append to History  │
        │      │    (Loop to Step 2)   │
        │      └────┬──────────────────┘
        │           │
        └───────────┴─────────────────┐
                                      │
                                ┌─────▼──────┐
                                │ Done       │
                                └────────────┘
```

In RAH, this becomes a simple DSL flow with a `while` loop.

## Complete Agentic Flow Example

Here's a full working example of a customer support agent that can search a knowledge base, look up customer orders, and create tickets:

```yaml
flows:
  - name: support-agent
    action: upsert
    code: |
      # Step 1: Set up context
      tenant_id = header("X-Tenant-ID")
      session_id = header("X-Session-ID")
      user_message = body("message")
      
      # Load conversation history
      history = cache.get("support-session:{session_id}")
      if (history == null) {
        history = "[]"
      }
      
      # Add user message to history
      append_message(history, role: "user", content: user_message)
      
      # Step 2: Cost guard
      enforce_cost_budget(tenant_id: tenant_id, daily_limit: "20.00")
      
      # Step 3: Agent loop
      has_tool_calls = "true"
      loop_count = "0"
      max_loops = "10"  # Prevent infinite loops
      
      while (has_tool_calls == "true" && loop_count < max_loops) {
        loop_count = add(loop_count, "1")
        
        # Call LLM with available tools
        call support_agent_turn
      }
      
      # Save final history
      cache.set("support-session:{session_id}", history, ttl: 3600)
      
      # Return final response
      return(200, final_response)
  
  - name: support-agent-turn
    action: upsert
    code: |
      # Fetch available tool schemas
      tools_schema = mcp_list_tools(
        server: "support-tools",
        mode: "full")  # Include parameters and descriptions
      
      # Call LLM with history and tools
      llm_response = llm(
        history,
        model: "claude",
        tools: tools_schema,
        stop_reason_variable: stop_reason,
        max_tokens: "2000")
      
      # Append LLM response to history
      append_message(history, role: "assistant", content: llm_response)
      
      # Check if LLM wants to call tools
      if (stop_reason == "tool_use") {
        # Tools were requested
        parse_tool_calls(llm_response, into: tool_calls)
        call execute_support_tools
        has_tool_calls = "true"
      } else {
        # No tools; LLM has final answer
        final_response = llm_response
        has_tool_calls = "false"
      }
  
  - name: execute-support-tools
    action: upsert
    code: |
      # Execute each tool call from the LLM
      foreach (tool_calls as tool_call) {
        tool_name = tool_call.name
        tool_args = tool_call.arguments
        
        # Execute via MCP server
        result = call_mcp_tool(
          server: "support-tools",
          tool: tool_name,
          args: tool_args)
        
        # Append tool result to history
        append_tool_result(
          history,
          tool_call_id: tool_call.id,
          tool_name: tool_name,
          result: result)
      }

apis:
  - name: support-api
    path: /support/chat
    method: POST
    flow_name: support-agent
    action: upsert
```

Call it:
```bash
curl -X POST http://localhost:8081/support/chat \
  -H "X-Tenant-ID: customer-456" \
  -H "X-Session-ID: support-session-789" \
  -d '{"message": "I need help with my order"}'
```

The agent will:
1. Receive the user message
2. Call Claude with the list of tools
3. If Claude requests tools: execute them, collect results, loop back
4. If Claude gives a final answer: return it
5. Save conversation history for the next turn

## MCP (Model Context Protocol)

**MCP** is an open protocol for connecting AI agents to tools and data sources. RAH has built-in support for:

- **Calling external MCP servers** (your tools, databases, APIs)
- **Serving as an MCP server** (expose RAH flows as tools to other clients)
- **Creating virtual MCP servers** (combine multiple tool sources)

### Connecting to an External MCP Server

Register an MCP server that hosts your tools:

```bash
curl -X POST http://localhost:8081/ai/mcp/servers \
  -H "Content-Type: application/json" \
  -d '{
    "alias": "support-tools",
    "url": "http://your-mcp-server:3000",
    "api_key_ref": "env://MCP_SERVER_API_KEY"
  }'
```

Test connectivity:
```bash
curl http://localhost:8081/ai/mcp/servers/support-tools/ping

curl http://localhost:8081/ai/mcp/servers/support-tools/tools
```

The second command returns the full list of available tools with parameters and descriptions—exactly what the LLM needs to call them intelligently.

### Using MCP Tools in a Flow

```yaml
flows:
  - name: knowledge-search-tool
    action: upsert
    code: |
      # Get a list of all available tools
      tools_list = mcp_list_tools(
        server: "support-tools",
        mode: "full")
      
      # Call a specific tool by name
      search_results = call_mcp_tool(
        server: "support-tools",
        tool: "search_knowledge_base",
        args: '{"query": "refund policy", "limit": 5}')
      
      return(200, search_results)

apis:
  - name: knowledge-search-api
    path: /tools/search
    method: POST
    flow_name: knowledge-search-tool
    action: upsert
```

Tool arguments are passed as JSON strings. RAH automatically marshals them to the MCP server.

### Virtual MCP Servers

Combine multiple MCP servers into a single unified endpoint:

```bash
curl -X POST http://localhost:8081/ai/mcp/virtual \
  -H "Content-Type: application/json" \
  -d '{
    "alias": "unified-tools",
    "description": "Combined support tools: KB + Orders + Tickets",
    "sources": [
      {"kind": "mcp_server", "alias": "knowledge-base-tools"},
      {"kind": "mcp_server", "alias": "order-tools"},
      {"kind": "mcp_server", "alias": "ticket-tools"}
    ]
  }'
```

Now you can use `"server": "unified-tools"` in any flow, and the LLM will see all three tool sets as a single interface.

### Serving RAH as an MCP Server

Expose a RAH endpoint as an MCP server so Claude Desktop, Claude.ai, or other MCP clients can use it:

```yaml
flows:
  - name: mcp-serve-flow
    action: upsert
    code: |
      # Serve RAH flows as tools via MCP
      serve_mcp(
        server: "my-rah-tools",
        tools: [
          {name: "lookup-order", flow: "get-order-details"},
          {name: "check-balance", flow: "get-account-balance"}
        ])

apis:
  - name: mcp-endpoint
    path: /mcp
    method: POST
    flow_name: mcp-serve-flow
    action: upsert
```

Point your MCP client to `http://your-gateway:8081/mcp`, and Claude will be able to call your RAH flows as tools.

## Plan Execution (DAG-based Agents)

For complex workflows with dependencies, let the LLM generate a plan as a directed acyclic graph (DAG), and RAH executes it with automatic parallelization:

```yaml
flows:
  - name: plan-executor-agent
    action: upsert
    code: |
      user_request = body("request")
      
      # Step 1: LLM generates an execution plan as JSON
      plan_prompt = concat(
        "Generate a JSON execution plan for: ",
        user_request,
        "\nFormat: {steps: [{name, description, depends_on: []}]}")
      
      plan_json = llm(
        plan_prompt,
        model: "claude",
        max_tokens: "2000")
      
      # Step 2: Parse and execute the plan
      execute_plan(
        plan: plan_json,
        context: request_context)
      
      return(200, plan_result)
```

Example: A data processing request might generate:
```json
{
  "steps": [
    {"name": "fetch_data", "description": "Download CSV from S3", "depends_on": []},
    {"name": "validate", "description": "Check data quality", "depends_on": ["fetch_data"]},
    {"name": "transform", "description": "Clean and transform", "depends_on": ["validate"]},
    {"name": "upload", "description": "Upload to warehouse", "depends_on": ["transform"]},
    {"name": "notify", "description": "Send completion email", "depends_on": ["upload"]}
  ]
}
```

RAH will:
- Execute `fetch_data` first
- Wait for it to complete
- Execute `validate` and any other steps with no dependencies
- Parallelize where possible
- Respect dependencies
- Return results

## Intent Detection for Routing

Route user requests to specialized agents based on intent:

```yaml
flows:
  - name: main-dispatcher
    action: upsert
    code: |
      user_query = body("message")
      
      # Classify the user's intent
      detected_intent = detect_intent(
        user_query,
        model: "claude-haiku",
        labels: "support,billing,sales,general")
      
      # Route to the appropriate agent
      switch (detected_intent) {
        "support": call support_agent
        "billing":  call billing_agent
        "sales":    call sales_agent
        "general":  call general_qa_agent
      }

apis:
  - name: main-dispatcher-api
    path: /chat
    method: POST
    flow_name: main-dispatcher
    action: upsert
```

Intent detection happens via the LLM, not string matching—so "Can I get a refund?" is recognized as "support" even if the user doesn't say the word "support".

## Streaming Agentic Responses

For long-running agents, stream intermediate steps back to the client in real time:

```yaml
flows:
  - name: streaming-agent
    action: upsert
    code: |
      session_id = header("X-Session-ID")
      user_query = body("question")
      
      # Load history
      history = cache.get("agent:{session_id}")
      if (history == null) {
        history = "[]"
      }
      
      append_message(history, role: "user", content: user_query)
      
      # Fetch tools
      tools_schema = mcp_list_tools(server: "tools", mode: "full")
      
      # Agent loop with streaming
      has_tool_calls = "true"
      while (has_tool_calls == "true") {
        send_sse_event(
          data: '{"status": "thinking"}',
          event: "step")
        
        # Call LLM
        response = llm(history,
          model: "claude",
          tools: tools_schema,
          stop_reason_variable: stop_reason)
        
        append_message(history, role: "assistant", content: response)
        
        if (stop_reason == "tool_use") {
          parse_tool_calls(response, into: tool_calls)
          
          foreach (tool_calls as tool) {
            send_sse_event(
              data: concat('{"tool":"', tool.name, '"}'),
              event: "tool_start")
            
            result = call_mcp_tool(
              server: "tools",
              tool: tool.name,
              args: tool.arguments)
            
            send_sse_event(
              data: result,
              event: "tool_result")
            
            append_tool_result(history, tool_call: tool, result: result)
          }
          
          has_tool_calls = "true"
        } else {
          send_sse_event(
            data: response,
            event: "done")
          
          has_tool_calls = "false"
        }
      }
      
      cache.set("agent:{session_id}", history, ttl: 3600)

apis:
  - name: streaming-agent-api
    path: /chat/stream
    method: POST
    flow_name: streaming-agent
    action: upsert
```

Call it with curl to see real-time events:
```bash
curl -X POST http://localhost:8081/chat/stream \
  -H "X-Session-ID: session-123" \
  -d '{"question": "Find me the latest invoice"}' \
  -N  # Disable buffering
```

Each event arrives immediately, giving the client real-time visibility into the agent's progress.

## Cost Control for Agents

Agents make many LLM calls (one per loop iteration). Enforce strict budgets:

```yaml
flows:
  - name: cost-controlled-agent
    action: upsert
    code: |
      tenant_id = header("X-Tenant-ID")
      
      # Enforce STRICT budget (stop if exceeded)
      enforce_cost_budget(
        tenant_id: tenant_id,
        daily_limit: "5.00",
        on_exceeded: "stop")  # Reject the request
      
      # Agent loop
      session_id = header("X-Session-ID")
      history = cache.get("session:{session_id}")
      
      user_msg = body("message")
      append_message(history, role: "user", content: user_msg)
      
      has_tool_calls = "true"
      loop_count = "0"
      total_cost = "0"
      
      while (has_tool_calls && loop_count < "10") {
        loop_count = add(loop_count, "1")
        
        # Call LLM
        response = llm(history,
          model: "claude",
          input_tokens_variable: in_tok,
          output_tokens_variable: out_tok,
          stop_reason_variable: stop_reason)
        
        # Track cost
        loop_cost = calculate_cost(
          model: "claude",
          input_tokens: in_tok,
          output_tokens: out_tok)
        
        total_cost = add(total_cost, loop_cost)
        
        # Check soft limit (warn but continue)
        if (total_cost > "3.00") {
          send_log(level: "warning",
            message: concat("Agent loop cost: $", total_cost))
        }
        
        append_message(history, role: "assistant", content: response)
        
        if (stop_reason == "tool_use") {
          parse_tool_calls(response, into: tool_calls)
          foreach (tool_calls as tool) {
            result = call_mcp_tool(server: "tools", tool: tool.name, args: tool.arguments)
            append_tool_result(history, tool_call: tool, result: result)
          }
          has_tool_calls = "true"
        } else {
          final_response = response
          has_tool_calls = "false"
        }
      }
      
      # Record final cost
      record_cost(tenant_id: tenant_id, cost: total_cost)
      
      cache.set("session:{session_id}", history, ttl: 3600)
      return(200, final_response)

apis:
  - name: cost-controlled-agent-api
    path: /agent/chat
    method: POST
    flow_name: cost-controlled-agent
    action: upsert
```

## Safety Guardrails for Agents

Agents operate with elevated permissions. Enforce safety checks:

```yaml
flows:
  - name: guarded-agent
    action: upsert
    code: |
      tenant_id = header("X-Tenant-ID")
      user_input = body("message")
      
      # --- Input Safety ---
      
      # Check for prompt injection
      sanitize_prompt(
        input: user_input,
        mode: "reject",
        rules: "injection",
        on_reject: stop)  # Return 400 if injection detected
      
      # Check for harmful intent
      intent = classify_llm(
        user_input,
        model: "claude-haiku",
        labels: "benign,harmful")
      
      if (intent == "harmful") {
        return(403, "Request not permitted")
      }
      
      # --- Agent Loop ---
      
      session_id = header("X-Session-ID")
      history = cache.get("agent:{session_id}")
      append_message(history, role: "user", content: user_input)
      
      tools_schema = mcp_list_tools(server: "tools", mode: "full")
      
      has_tool_calls = "true"
      while (has_tool_calls) {
        response = llm(history,
          model: "claude",
          tools: tools_schema,
          stop_reason_variable: stop_reason)
        
        append_message(history, role: "assistant", content: response)
        
        if (stop_reason == "tool_use") {
          parse_tool_calls(response, into: tool_calls)
          
          # --- Tool Call Safety ---
          
          foreach (tool_calls as tool_call) {
            # Whitelist allowed tools
            allowed_tools = "search_kb,lookup_order,create_ticket"
            if (!contains(allowed_tools, tool_call.name)) {
              send_log(level: "error",
                message: concat("Tool not whitelisted: ", tool_call.name))
              continue  # Skip this tool call
            }
            
            # Rate limit tool calls
            rate_limit(
              key: concat("tool:", tool_call.name, ":", tenant_id),
              max_per_minute: "10",
              on_exceeded: drop)
            
            result = call_mcp_tool(
              server: "tools",
              tool: tool_call.name,
              args: tool_call.arguments,
              timeout: "5s")  # Timeout after 5 seconds
            
            append_tool_result(history, tool_call: tool_call, result: result)
          }
          
          has_tool_calls = "true"
        } else {
          final_response = response
          has_tool_calls = "false"
        }
      }
      
      cache.set("agent:{session_id}", history, ttl: 3600)
      return(200, final_response)

apis:
  - name: guarded-agent-api
    path: /agent/secure
    method: POST
    flow_name: guarded-agent
    action: upsert
```

Safeguards:
- **Input validation**: Reject prompt injection and harmful intent
- **Tool whitelisting**: Only allow specified tools
- **Rate limiting**: Prevent tool spam
- **Timeouts**: Fail fast if a tool hangs
- **Logging**: Record all tool calls for audit

## Real-World Example: Customer Support Agent

A production-grade support agent with multi-turn conversation, knowledge search, order lookup, and ticket creation:

```yaml
flows:
  - name: support-agent-main
    action: upsert
    code: |
      # Authenticate and identify tenant
      validate_token(
        header("Authorization"),
        jwks_url: "https://auth.example.com/.well-known/jwks.json",
        alg: "RS256",
        on_failure: stop)
      
      tenant_id = header("X-Tenant-ID")
      
      # Look up tenant tier (affects model and budget)
      registry.lookup(tenant_id)
      tier = registry.meta("tier")
      
      # Select model and budget based on tier
      if (tier == "premium") {
        model = "claude-opus"
        daily_budget = "50.00"
      } else if (tier == "business") {
        model = "claude-sonnet"
        daily_budget = "20.00"
      } else {
        model = "claude-haiku"
        daily_budget = "5.00"
      }
      
      # Enforce budget
      enforce_cost_budget(
        tenant_id: tenant_id,
        daily_limit: daily_budget)
      
      # Load conversation
      session_id = header("X-Session-ID")
      history = cache.get("support:{session_id}")
      if (history == null) {
        history = "[]"
      }
      
      user_msg = body("message")
      append_message(history, role: "user", content: user_msg)
      
      # Sanitize input
      sanitize_prompt(
        input: user_msg,
        mode: "strip",
        rules: "pii,injection")
      
      # Get available tools
      tools_schema = mcp_list_tools(
        server: "support-tools",
        mode: "full")
      
      # Agent loop
      has_tool_calls = "true"
      loop_count = "0"
      
      while (has_tool_calls && loop_count < "8") {
        loop_count = add(loop_count, "1")
        
        # Call LLM
        response = llm(history,
          model: model,
          tools: tools_schema,
          input_tokens_variable: in_tok,
          output_tokens_variable: out_tok,
          stop_reason_variable: stop_reason,
          max_tokens: "1000")
        
        # Track cost
        loop_cost = calculate_cost(
          model: model,
          input_tokens: in_tok,
          output_tokens: out_tok)
        
        append_message(history, role: "assistant", content: response)
        
        if (stop_reason == "tool_use") {
          parse_tool_calls(response, into: tool_calls)
          
          foreach (tool_calls as tool_call) {
            # Whitelist: only KB search, order lookup, and ticket creation
            if (!contains("search_kb,lookup_order,create_ticket", tool_call.name)) {
              send_log(level: "warning",
                message: concat("Tool rejected: ", tool_call.name))
              continue
            }
            
            # Execute tool with timeout
            result = call_mcp_tool(
              server: "support-tools",
              tool: tool_call.name,
              args: tool_call.arguments,
              timeout: "5s")
            
            append_tool_result(
              history,
              tool_call_id: tool_call.id,
              tool_name: tool_call.name,
              result: result)
          }
          
          has_tool_calls = "true"
        } else {
          final_response = response
          has_tool_calls = "false"
        }
      }
      
      # Save conversation
      cache.set("support:{session_id}", history, ttl: 7200)
      
      # Record cost
      record_cost(tenant_id: tenant_id, cost: loop_cost)
      
      return(200, final_response)

apis:
  - name: support-api
    path: /support/chat
    method: POST
    flow_name: support-agent-main
    action: upsert
```

This agent:
- ✅ Authenticates the request
- ✅ Enforces tenant isolation
- ✅ Tier-based model selection and budgeting
- ✅ Input sanitization
- ✅ Multi-turn conversation with history
- ✅ Tool whitelisting
- ✅ Timeout protection
- ✅ Cost tracking
- ✅ Audit logging

---

**Related guides**:
- [RAH as an AI Gateway](./ai-gateway.md) — LLM calls, caching, routing
- RAH official documentation in `docs/ARCHITECTURE.md` — System design and request flow
