# Using the Sync Utility (rah-sync)

Deploy APIs programmatically using YAML bundle files and the gateway's `/sync` endpoint. This guide covers Git-based API management workflows without the Studio UI.

## What is the Sync Utility?

`rah-sync` is a CLI tool that reads YAML bundle files and pushes them to your RAH gateway for deployment. It enables code-first, Git-based API management suitable for CI/CD pipelines and automated workflows.

Key benefits:
- **Version control**: APIs defined as YAML files in Git
- **Reproducible**: Same file → same API configuration, every time
- **Auditable**: Tracks exactly what changed and when
- **CI/CD friendly**: Integrates seamlessly with GitHub Actions, GitLab CI, Jenkins

## When to use Sync vs Studio

| | Sync Utility | Studio UI |
|---|---|---|
| **Best for** | CI/CD pipelines, Git-based workflows | Manual creation, visual editing, debugging |
| **Input** | YAML files in Git | Browser UI |
| **Automation** | Full (shell scripts, CI jobs) | Limited (manual actions) |
| **Validation** | Lint errors returned as JSON | Inline UI errors |

**Rule of thumb**: Use Sync for production deployments, Studio for development and debugging.

## Bundle File Format

A bundle file contains one or more flows and/or API definitions in YAML. Each flow is a piece of executable logic; each API maps an HTTP route to a flow.

### Example Bundle

```yaml
# my-apis.yaml
flows:
  - name: get_user
    action: upsert   # "upsert" or "delete"
    code: |
      registry.lookup(header("X-Tenant-ID"))
      url = registry.url("primary")
      resp = http.get(url_var: url, timeout: 5000)
      return(200, resp)

  - name: create_user
    action: upsert
    code: |
      registry.lookup(header("X-Tenant-ID"))
      body = read_body()
      resp = http.post(
        url_var: registry.url("primary"),
        body_bytes: body,
        timeout: 5000
      )
      return(201, resp)

apis:
  - name: get-user
    path: /users/{id}
    method: GET
    flow_name: get_user
    action: upsert

  - name: create-user
    path: /users
    method: POST
    flow_name: create_user
    action: upsert
```

### Flow Definition

```yaml
flows:
  - name: <flow_name>           # Unique identifier
    action: upsert | delete     # Create/update or delete
    code: |                      # Executable DSL code
      # Your flow logic here
```

**Flow code syntax** (high-level overview):
- `registry.lookup(key)` — Resolve tenant/service
- `header(name)` — Read HTTP header
- `read_body()` — Read request body
- `http.get()`, `http.post()`, `http.put()`, `http.delete()` — Make upstream calls
- `return(status, body)` — Send response
- `url_var`, `timeout`, `retry` — Common HTTP options

See your gateway's DSL documentation for the complete instruction set.

### API Definition

```yaml
apis:
  - name: <api_name>            # Unique identifier
    path: /users/{id}           # Route pattern; {id} is a parameter
    method: GET | POST | ...    # HTTP method
    flow_name: <flow_name>      # Flows to execute
    action: upsert | delete     # Create/update or delete
```

## Running rah-sync

### Basic Commands

```bash
# Push a single file
rah-sync --gateway http://localhost:8081 --file my-apis.yaml

# Push all YAML files in a directory
rah-sync --gateway http://localhost:8081 --dir ./apis/

# Dry-run: compile without deploying
rah-sync --gateway http://localhost:8081 --file my-apis.yaml --draft

# Check draft status
rah-sync --gateway http://localhost:8081 --status

# With authentication token
rah-sync --gateway http://localhost:8081 --token $GATEWAY_TOKEN --file my-apis.yaml
```

### Common Options

| Option | Purpose |
|--------|---------|
| `--gateway URL` | Gateway address (default: http://localhost:8081) |
| `--file PATH` | Single YAML file to sync |
| `--dir PATH` | Directory of YAML files to sync |
| `--token TOKEN` | Bearer token for authentication |
| `--draft` | Compile without deploying (preview mode) |
| `--status` | Check draft compilation status |
| `--verbose` | Show detailed logs |

## Directory Layout Recommendation

Organize your APIs by service domain for easier management:

```
apis/
├── auth/
│   ├── token-validation.yaml
│   └── api-keys.yaml
├── products/
│   ├── catalog.yaml
│   └── inventory.yaml
├── orders/
│   ├── checkout.yaml
│   └── history.yaml
└── shared/
    └── common-flows.yaml
```

**Best practices**:
- One flow or API per file, or group related items
- Use descriptive filenames
- Keep shared/reusable flows in a `shared/` directory
- Use clear flow names: `get_user`, `create_order`, not `flow1`, `flow2`

## Using the REST API Directly

If you don't use the CLI, call the gateway's `/sync` endpoint directly:

```bash
# POST a bundle file as JSON
curl -X POST http://localhost:8081/sync \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  --data-binary @my-apis.yaml

# Response includes status, warnings, and errors
```

### Response Format

Success (even with warnings):
```json
{
  "status": "ok",
  "warnings": [
    {"flow": "my_flow", "message": "slot 'user_id' is unfilled"},
    {"api": "get-user", "message": "path parameter 'id' not used in flow"}
  ],
  "errors": []
}
```

Compilation error:
```json
{
  "status": "error",
  "errors": [
    {"line": 5, "message": "http.get expects url_var or url parameter"}
  ],
  "warnings": []
}
```

## Draft Mode

Draft mode allows you to test changes without going live. The gateway compiles your bundle and reports errors, but does not activate it.

### Push as Draft

```bash
# Use CLI
rah-sync --gateway http://localhost:8081 --file my-apis.yaml --draft

# Or REST API directly
curl -X POST "http://localhost:8081/sync?draft=true" \
  -H "Content-Type: application/json" \
  --data-binary @my-apis.yaml
```

### Check Draft Status

```bash
# CLI
rah-sync --gateway http://localhost:8081 --status

# Or REST API
curl http://localhost:8081/sync/draft/status
```

Response:
```json
{
  "compiled": true,
  "status": "ok",
  "timestamp": "2026-06-02T15:30:45Z"
}
```

Once you're satisfied, push without the `--draft` flag to deploy.

## Deleting an API or Flow

To remove APIs or flows without affecting others, use the `delete` action:

```yaml
apis:
  - name: old-api
    action: delete

flows:
  - name: old_flow
    action: delete
```

Push the bundle:

```bash
rah-sync --gateway http://localhost:8081 --file delete-apis.yaml
```

The gateway removes these items and keeps the rest intact.

## Error Handling in CI

Always check the response status in your CI pipeline. The sync endpoint returns both warnings (non-blocking) and errors (blocking):

```bash
#!/bin/bash
RESPONSE=$(curl -s -X POST http://gateway:8081/sync \
  -H "Content-Type: application/json" \
  --data-binary @my-apis.yaml)

# Check for errors
if echo "$RESPONSE" | jq -e '.errors | length > 0' > /dev/null; then
  echo "Deployment failed with errors:"
  echo "$RESPONSE" | jq '.errors'
  exit 1
fi

# Warnings are OK, but log them
if echo "$RESPONSE" | jq -e '.warnings | length > 0' > /dev/null; then
  echo "Warnings detected:"
  echo "$RESPONSE" | jq '.warnings'
fi

echo "Deployment successful"
```

## Syncing via Studio REST

If you're running Studio (port 8082) alongside the gateway, you can sync through Studio for additional audit trail and release tracking:

```bash
# POST to Studio's /api/sync endpoint
curl -X POST http://localhost:8082/api/sync \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $STUDIO_TOKEN" \
  --data-binary @my-apis.yaml
```

Benefits:
- Each sync creates a release record (who, what, when)
- Enables rollback to previous sync states
- Integrates with release management workflows

Studio mirrors the gateway's `/sync` endpoint and adds release tracking on top.

## Troubleshooting

### Gateway not responding
```bash
# Check connectivity
curl -v http://localhost:8081/health

# Ensure the gateway is running and accessible
```

### Authentication errors (401)
```bash
# Verify your token is valid and current
rah-sync --gateway http://localhost:8081 --token $TOKEN --file my-apis.yaml --verbose

# Check that your gateway environment has authentication enabled
```

### YAML syntax errors
```bash
# Validate your YAML before syncing
yamllint my-apis.yaml

# Use --draft to preview without deploying
rah-sync --gateway http://localhost:8081 --file my-apis.yaml --draft
```

### Slot allocation errors
The gateway has a fixed number of slots (memory slots for storing intermediate values). If you see "slot allocation failed":
- Simplify your flow logic (break into smaller flows)
- Reuse slots where possible (don't store unnecessary intermediate values)

Check your gateway's documentation for slot limits and optimization techniques.

## Next Steps

- **Learn the DSL**: Read your gateway's DSL syntax guide for all available instructions
- **Set up CI/CD**: See `cicd-guide.md` for GitHub Actions and GitLab CI examples
- **Understand flows**: Review sample flows in `samples-code/` directory of your gateway
- **Monitor deployments**: Use Studio to track release history and audit changes
