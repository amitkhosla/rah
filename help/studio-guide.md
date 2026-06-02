# Using the Studio UI

The RAH Studio is a browser-based management console for the RAH API Gateway. Create APIs, manage tenants, monitor traffic, configure AI/LLM models, and deploy releases — all without touching the command line.

## Accessing Studio

Open your web browser and navigate to:
```
http://localhost:8082
```

The default port is 8082. Log in with your admin credentials (configured in your gateway setup).

---

## What is Studio?

Studio is the primary interface for managing the RAH API Gateway. It provides:

- **API Management**: Create, edit, and deploy API flows using a visual builder or DSL editor
- **Tenant Management**: Multi-tenant isolation with aliases, service URLs, identifiers, and metadata
- **Rate Limiting**: Create and assign rate limit configurations per tenant
- **AI/LLM Integration**: Register models, test AI capabilities, set up MCP servers
- **Observability**: Real-time logs, traces, metrics, and performance diagnostics
- **Release Management**: Track versions and deploy to environments
- **AI Assistant**: Built-in chat to help you configure APIs

Studio is stateless and proxies all operations to the gateway's management API. You can also use the REST API directly at `http://localhost:8082/api/` if you prefer programmatic access.

---

## Navigation and Sections

### APIs Section

View, create, and manage all deployed APIs and flows.

**What you can do:**

- **View APIs**: See all deployed APIs with their paths, methods, and associated flows
- **View Flows**: List all flows (reusable logic blocks) and their status
- **Create a new flow**: Click "New Flow", give it a name, and define it using the visual builder or DSL editor
- **Edit a flow**: Modify flow logic, add steps, set conditions
- **Import from OpenAPI**: Click "Import OpenAPI" to upload a Swagger/OpenAPI spec. The system auto-generates flows with validation rules for your endpoints
- **Sync to Gateway**: Click "Sync" to deploy all changes live to the gateway
- **View metrics**: Per-API request counts, error rates, latency percentiles, and other performance data
- **View traces**: Per-request instruction-level timing to identify slow steps
- **View access logs**: Request details including headers, body, response, and execution time

### Tenants Section

Manage multi-tenancy. Each tenant is an isolated customer or application.

**What you can do:**

- **List tenants**: View all tenants with cursor-based pagination
- **Create a new tenant**: Click "New Tenant" and provide:
  - **Aliases**: Optional hostnames or identifiers (e.g., `api.customer-a.com`, `customer-a`)
  - **Service URLs**: Upstream endpoints that this tenant routes to (e.g., `https://internal-service.example.com`)
  - **Identifiers**: Credentials or API keys stored per tenant (e.g., `api_key`, `oauth_token`, `customer_id`)
  - **Metadata**: Custom key-value pairs (e.g., `tier: premium`, `region: us-west`)

- **Add aliases**: Assign additional hostnames to an existing tenant
- **View tenant details**: ID, all aliases, service URLs, identifiers, and metadata
- **Delete a tenant**: Remove a tenant and all its associated configuration (use with caution)

**Rate Limits sub-section:**
- Assign rate limit configurations to specific tenants
- View which configs apply to which tenants
- Create new rate limit configs from this view

### Rate Limits Section

Create and manage named rate limit configurations.

**What you can do:**

- **View all rate limit configs**: See V1 and V2 configurations
- **Create a new config**: Define limits using:
  - **V1**: Simple fixed-window limits (e.g., 1000 requests per minute)
  - **V2**: Advanced multi-window limits (e.g., 1000 per minute AND 100,000 per day)
- **Assign to tenants**: Specify which tenants use each config
- **Edit**: Modify limit values or time windows
- **Delete**: Remove a config (detach from tenants first)

Common patterns:
- **Standard API**: 1000 req/min per tenant
- **Premium API**: 10000 req/min per tenant
- **Per-IP**: 100 req/min per client IP
- **Per-API-Key**: 500 req/min per API key

### AI / LLM Section

Register and manage large language models for AI-powered flows.

**What you can do:**

- **Register Models**: Add LLM providers:
  - Anthropic Claude (various versions)
  - OpenAI GPT-4, GPT-3.5
  - Google Gemini
  - Ollama (self-hosted)
  - AWS Bedrock
  - Custom providers

- **Test a model**: Click "Test" to send a sample prompt and see the response in real-time
- **Configure AI routing**: Set rules to route requests to different models based on:
  - Token count (large requests → larger models)
  - Tenant tier (premium tenants → better models)
  - Request metadata
  
- **Set up MCP servers**: Register Model Context Protocol servers that provide specialized tools to LLMs
  - MCP servers extend LLM capabilities with custom functions (e.g., database queries, API calls)
  - Configure authentication and connection parameters

- **Virtual MCP servers**: Compose multiple MCP servers into a single virtual server
  - Useful for grouping related tools
  
- **Tenant cost quotas**: Set daily or monthly spend caps per tenant
  - Prevent unexpected bills from high-volume AI usage
  
- **View pricing**: Access the AI pricing catalog for all registered models

Example workflow:
1. Register Anthropic Claude (API key from Anthropic console)
2. Register an MCP server for internal APIs (custom tool for your business logic)
3. Create a flow that uses both: `llm(prompt, model: "claude", context_server: "my-apis")`
4. Assign cost quota to premium tenants: `$100/day`

### Observability Section

Monitor and debug your APIs in real-time.

**What you can do:**

- **Access log**: View all incoming requests with filtering options
  - Filter by: API, tenant, status code, date range, client IP
  - See: full request/response, headers, body, execution time, errors

- **Traces**: Per-request instruction-level timing
  - Identify which steps are slow
  - Debug flow logic issues
  - See variable values at each step

- **Metrics**: Real-time and historical performance data
  - Per-API: request counts, error rates, latency (p50, p95, p99)
  - Per-tenant: usage, errors, peak traffic times
  - Per-step: instruction execution time
  
- **Prometheus export**: `/metrics` endpoint for integration with monitoring systems (Grafana, Datadog, etc.)

### Releases Section

Track and deploy API versions.

**What you can do:**

- **Create a release**: Snapshot the current API state with:
  - Name (e.g., `v1.2.0`, `2026-06-02-hotfix`)
  - Optional git metadata (commit hash, branch, author)
  - Release notes or changelog

- **View release history**: See all past releases with:
  - Author and timestamp
  - Associated git commit/branch (if provided)
  - Lint status (warnings or errors)
  
- **Deploy a release**: Select target environment(s) (dev, staging, prod) and click Deploy
  - Track deployment status in real-time
  - Rollback to previous release if needed
  
- **Release history per environment**: See which release is currently active in each environment

### AI Assistant

Built-in AI chat in the right sidebar.

**What you can do:**

- Ask questions about your API configuration
  - "How do I add rate limiting to my API?"
  - "What's the best way to cache responses?"
  
- Get suggestions for flows and steps
  - "Suggest a flow for user authentication"
  - "How do I set up MCP servers?"
  
- Get help with DSL syntax
  - "Show me an example of a conditional flow"
  - "How do I validate a JWT token?"

The AI Assistant has access to your current configuration and can provide context-aware suggestions.

---

## Creating Your First API in Studio

Follow these steps to build and deploy your first API.

### Step 1: Create a Flow

1. Click **APIs** → **Flows** tab
2. Click **New Flow**
3. Enter a flow name (e.g., `list_users`)
4. Choose your editor:
   - **Visual Builder**: Drag-and-drop step interface (recommended for beginners)
   - **DSL Editor**: Write code in the RAH DSL language
5. Add steps:
   - Click **Add Step** (visual builder) or type statements (DSL editor)
   - Example visual steps: HTTP call, cache check, rate limit, response
   - Example DSL: 
     ```
     url = registry.url("primary")
     resp = http.get(url_var: url, timeout: 5000)
     return(200, resp)
     ```
6. Click **Save Flow**

### Step 2: Create an API Endpoint

1. Click **APIs** → **APIs** tab
2. Click **New API**
3. Fill in the details:
   - **Name**: Friendly name (e.g., `List Users`)
   - **Path**: URL path (e.g., `/v1/users`)
   - **Method**: HTTP method (GET, POST, PUT, DELETE, etc.)
   - **Flow**: Select the flow you created above
4. Click **Save API**

### Step 3: Deploy to the Gateway

1. Click the **Sync** button (top-right)
2. Confirm the deployment
3. You'll see a success message once the API is live

### Step 4: Test Your API

1. Open a new browser tab or terminal
2. Call your API:
   ```bash
   curl http://localhost:8080/v1/users
   ```
   (Replace `8080` with your gateway's port and `/v1/users` with your API path)

3. Or use Studio's built-in test panel:
   - Click your API → **Test**
   - Add headers, query parameters, request body
   - Click **Send**
   - View the response

---

## Deploying via Studio

Releases let you version and deploy your API configurations.

### Creating a Release

1. Click **Releases**
2. Click **Create Release**
3. Fill in:
   - **Release Name**: e.g., `v1.2.0` or `2026-06-02-hotfix`
   - **Git Metadata** (optional):
     - Commit hash or branch
     - Author name
   - **Release Notes** (optional): Changelog or description
4. Click **Create**
5. The system creates a snapshot of your current APIs and flows

### Deploying to Environments

1. Click the release you created
2. Click **Deploy**
3. Select target environment(s):
   - **dev**: Development (fast iteration)
   - **staging**: Pre-production testing
   - **prod**: Live (use caution)
4. Click **Deploy to Selected**
5. Monitor deployment status in real-time

### Rolling Back

If you need to revert:

1. Click **Releases**
2. Find the previous stable release
3. Click **Deploy**
4. Select the environment and confirm

---

## Studio REST API

Everything Studio does is available as a REST API. You can call it directly for automation, CI/CD pipelines, or custom integrations.

**Base URL:**
```
http://localhost:8082/api
```

**Common endpoints:**

- `GET /apis` — List all APIs
- `POST /apis` — Create a new API
- `GET /apis/{id}` — Get API details
- `PUT /apis/{id}` — Update an API
- `DELETE /apis/{id}` — Delete an API

- `GET /flows` — List all flows
- `POST /flows` — Create a new flow
- `GET /flows/{id}` — Get flow details
- `PUT /flows/{id}` — Update a flow

- `GET /tenants` — List tenants (with cursor-based pagination)
- `POST /tenants` — Create a new tenant
- `GET /tenants/{id}` — Get tenant details
- `PUT /tenants/{id}` — Update a tenant
- `DELETE /tenants/{id}` — Delete a tenant

- `GET /rate-limit-configs` — List rate limit configs
- `POST /rate-limit-configs` — Create a new config

- `GET /releases` — List releases
- `POST /releases` — Create a new release
- `POST /releases/{id}/deploy` — Deploy a release

- `GET /metrics` — Prometheus metrics export

**Authentication**: Include your admin token in the `Authorization` header:
```bash
Authorization: Bearer YOUR_ADMIN_TOKEN
```

**Example:**
```bash
curl -H "Authorization: Bearer my-token" \
  http://localhost:8082/api/tenants
```

---

## Tips and Best Practices

- **Version your APIs**: Create releases before making major changes
- **Test in dev first**: Deploy to dev environment before staging or production
- **Monitor metrics**: Check the Observability section regularly to spot issues
- **Use caching**: Cache frequent upstream calls to reduce latency and load
- **Set rate limits**: Protect your upstream services from abuse
- **Organize flows**: Name flows clearly so you and your team know what they do
- **Use sub-flows**: Break complex logic into reusable sub-flows
- **Test before sync**: Use the test panel to verify your API works before syncing to production
- **Document with AI Assistant**: Ask the AI Assistant for help writing flows or understanding configuration

---

## Troubleshooting

**"Sync failed" error:**
- Check the error message for details
- Verify all required fields are filled (paths, methods, flow names)
- Check flow syntax if using DSL editor

**API returns 404:**
- Verify the API path and method match your request
- Make sure you clicked "Sync" after creating the API
- Check in the Observability section to see if the request reached the gateway

**Slow responses:**
- Open the Traces view to see which steps are taking time
- Check if your upstream service is responding slowly
- Consider adding caching for frequently accessed data

**Rate limiting not working:**
- Verify the rate limit config is assigned to the tenant
- Check the Observability logs to see if rate limit errors are occurring

**Need help?**
- Use the AI Assistant (right sidebar) to ask questions
- Check the DSL Guide for syntax and examples
- Contact your gateway administrator

