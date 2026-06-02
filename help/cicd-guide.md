# CI/CD for API Publishing

Automate your API deployments through a pipeline. This guide covers GitOps-style workflows: APIs are defined as YAML files in Git, tested in CI, and promoted through environments (dev → staging → prod) using releases.

## The API Publishing Workflow

```
Developer writes YAML → Git commit → CI validates → Deploy to dev → 
Staging tests pass → Create release → Deploy to production
```

Each step is independent — you can deploy to one environment without affecting others.

## GitHub Actions Example

### Complete Workflow File

Here's a production-ready GitHub Actions workflow that validates, deploys, and tests across all environments:

```yaml
name: Deploy APIs

on:
  push:
    branches: [main]
    paths: ['apis/**']

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Validate API definitions
        run: |
          curl -X POST ${{ secrets.STAGING_GATEWAY }}/sync?draft=true \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer ${{ secrets.GATEWAY_TOKEN }}" \
            --data-binary @apis/my-apis.yaml \
            --fail-with-body

  deploy-staging:
    needs: validate
    runs-on: ubuntu-latest
    environment: staging
    steps:
      - uses: actions/checkout@v4
      
      - name: Deploy to staging
        run: |
          curl -X POST ${{ secrets.STAGING_GATEWAY }}/sync \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer ${{ secrets.GATEWAY_TOKEN }}" \
            --data-binary @apis/my-apis.yaml \
            --fail-with-body
      
      - name: Health check
        run: |
          sleep 2
          curl --fail ${{ secrets.STAGING_URL }}/health

  deploy-prod:
    needs: deploy-staging
    runs-on: ubuntu-latest
    environment: production
    steps:
      - uses: actions/checkout@v4
      
      - name: Create release
        id: release
        run: |
          RELEASE=$(curl -X POST ${{ secrets.STUDIO_URL }}/api/releases \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer ${{ secrets.STUDIO_TOKEN }}" \
            -d '{
              "tag": "v${{ github.run_number }}",
              "git_commit": "${{ github.sha }}",
              "git_branch": "${{ github.ref_name }}",
              "git_repo": "${{ github.repository }}",
              "author": "${{ github.actor }}"
            }')
          echo "release_id=$(echo $RELEASE | jq -r .id)" >> $GITHUB_OUTPUT
      
      - name: Deploy release to production
        run: |
          curl -X POST ${{ secrets.STUDIO_URL }}/api/deploy \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer ${{ secrets.STUDIO_TOKEN }}" \
            -d '{
              "release_id": "${{ steps.release.outputs.release_id }}",
              "targets": ["production"],
              "levels": ["live"]
            }'
```

### Required GitHub Secrets

Store these in Settings → Secrets and variables → Actions:

| Secret | Value |
|--------|-------|
| `STAGING_GATEWAY` | Staging gateway URL (e.g., https://staging-gateway.example.com:8081) |
| `STAGING_URL` | Staging app URL for health checks |
| `PROD_GATEWAY` | Production gateway URL |
| `PROD_URL` | Production app URL |
| `STUDIO_URL` | Studio URL (e.g., https://studio.example.com:8082) |
| `GATEWAY_TOKEN` | Bearer token for gateway `/sync` endpoint |
| `STUDIO_TOKEN` | Bearer token for Studio API (releases, deploy) |

**Tip**: Use GitHub's "Environments" feature (Settings → Environments) to control which secrets are available per job. This prevents accidental production access from dev jobs.

## GitLab CI Example

### Complete Pipeline File

```yaml
stages:
  - validate
  - staging
  - production

validate:
  stage: validate
  script:
    - |
      curl -X POST "${STAGING_GATEWAY}/sync?draft=true" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${GATEWAY_TOKEN}" \
        --data-binary @apis/my-apis.yaml \
        --fail

deploy-staging:
  stage: staging
  environment: staging
  script:
    - |
      curl -X POST "${STAGING_GATEWAY}/sync" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${GATEWAY_TOKEN}" \
        --data-binary @apis/my-apis.yaml \
        --fail
    - sleep 2
    - curl --fail "${STAGING_URL}/health"
  only: [main]

deploy-prod:
  stage: production
  environment: production
  when: manual
  script:
    - |
      curl -X POST "${PROD_GATEWAY}/sync" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${GATEWAY_TOKEN}" \
        --data-binary @apis/my-apis.yaml \
        --fail
  only: [main]
```

### Required GitLab CI Variables

Set in Settings → CI/CD → Variables (make production variables protected):

| Variable | Value |
|----------|-------|
| `STAGING_GATEWAY` | Staging gateway URL |
| `STAGING_URL` | Staging app URL |
| `PROD_GATEWAY` | Production gateway URL |
| `PROD_URL` | Production app URL |
| `GATEWAY_TOKEN` | Bearer token (protected) |

## Environment Promotion Strategy

Recommended three-tier approach:

```
environments:
  dev:   http://dev-gateway:8081       (auto-deploy on every commit)
  staging: http://stg-gateway:8081     (auto-deploy on main branch)
  prod:  http://prd-gateway:8081       (manual approval, release-based)
```

**Why this matters**:
- **Dev**: Catch errors early. Fast feedback loop for developers.
- **Staging**: Full integration tests, smoke tests, load testing.
- **Prod**: Only after staging passes. Manual gate prevents accidents.

## Release Management for Production

Use releases (not direct deploys) for production deployments:

```bash
# Create a release in Studio
RELEASE=$(curl -X POST http://studio:8082/api/releases \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $STUDIO_TOKEN" \
  -d '{
    "tag": "v1.0.0",
    "git_commit": "abc123def456",
    "git_branch": "main",
    "author": "alice@example.com"
  }')

RELEASE_ID=$(echo $RELEASE | jq -r .id)

# Deploy release to production
curl -X POST http://studio:8082/api/deploy \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $STUDIO_TOKEN" \
  -d "{
    \"release_id\": \"$RELEASE_ID\",
    \"targets\": [\"production\"],
    \"levels\": [\"live\"]
  }"
```

**Benefits**:
- Immutable snapshot of API definitions at a point in time
- Linked to Git commit, branch, and author
- Can rollback by re-deploying a previous release
- Audit trail in Studio: who deployed what, when

## Rollback Procedure

### List Recent Releases

```bash
curl http://studio:8082/api/releases | jq '.[] | {id, tag, created_at}'
```

Output:
```json
{
  "id": "rel-20260602-001",
  "tag": "v1.0.0",
  "created_at": "2026-06-02T14:30:00Z"
}
{
  "id": "rel-20260601-001",
  "tag": "v0.9.9",
  "created_at": "2026-06-01T10:15:00Z"
}
```

### Redeploy a Previous Release

```bash
curl -X POST http://studio:8082/api/deploy \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $STUDIO_TOKEN" \
  -d '{
    "release_id": "rel-20260601-001",
    "targets": ["production"]
  }'
```

Takes effect immediately. You can also do this from the Studio UI.

## Validating APIs Before Deploy

The gateway returns lint results on sync. Check them in CI:

```bash
# Push as draft for validation
RESPONSE=$(curl -s -X POST "http://gateway:8081/sync?draft=true" \
  -H "Content-Type: application/json" \
  --data-binary @my-apis.yaml)

# Extract errors
ERRORS=$(echo "$RESPONSE" | jq '.errors')
if [ "$ERRORS" != "[]" ]; then
  echo "Compilation errors found:"
  echo "$RESPONSE" | jq .
  exit 1
fi

echo "Validation passed. Ready to deploy."
```

Example response:
```json
{
  "status": "ok",
  "warnings": [
    {"flow": "my_flow", "message": "slot 'user_id' is unfilled"}
  ],
  "errors": []
}
```

## Secrets in CI/CD

Never hardcode API keys or credentials in YAML files.

### 1. Use Secret References in Flows

```yaml
flows:
  - name: third_party_flow
    action: upsert
    code: |
      api_key = load_secret("env://MY_API_KEY")
      service_url = registry.url("third_party")
      resp = http.get(
        url_var: service_url,
        headers: {"X-API-Key": api_key},
        timeout: 5000
      )
      return(resp.status, resp.body)
```

### 2. Inject as Environment Variables in CI

The gateway reads `load_secret("env://KEY_NAME")` from its environment.

**GitHub Actions**:
```yaml
deploy:
  env:
    MY_API_KEY: ${{ secrets.MY_API_KEY }}
  run: |
    curl ... --data-binary @apis.yaml
```

**GitLab CI**:
```yaml
deploy:
  variables:
    MY_API_KEY: $MY_API_KEY  # Predefined in GitLab secrets
  script: |
    curl ... --data-binary @apis.yaml
```

### 3. Use External Secrets Manager (Optional)

For production, integrate with AWS Secrets Manager or HashiCorp Vault:

```yaml
flows:
  - name: aws_secrets_flow
    action: upsert
    code: |
      db_password = load_secret("aws-secrets://prod/db-password")
      api_key = load_secret("aws-secrets://prod/api-key")
```

Configure RAH to authenticate with your secrets manager (usually via IAM role or service account).

## Testing APIs in CI

After deploying to staging, run integration tests:

```bash
#!/bin/bash
# scripts/test-apis.sh
set -e

GATEWAY=${1:-http://localhost:8081}
echo "Testing APIs against $GATEWAY"

# Test 1: Health check
echo -n "Testing health endpoint ... "
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/health")
[ "$STATUS" == "200" ] && echo "OK" || (echo "FAILED"; exit 1)

# Test 2: Public API
echo -n "Testing public API ... "
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/v1/public")
[ "$STATUS" == "200" ] && echo "OK" || (echo "FAILED"; exit 1)

# Test 3: Protected endpoint requires auth
echo -n "Testing protected endpoint (no auth) ... "
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$GATEWAY/v1/protected")
[ "$STATUS" == "401" ] && echo "OK" || (echo "FAILED"; exit 1)

# Test 4: Auth required but valid token grants access
echo -n "Testing protected endpoint (with auth) ... "
TOKEN="your-test-jwt-here"
STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $TOKEN" \
  "$GATEWAY/v1/protected")
[ "$STATUS" == "200" ] && echo "OK" || (echo "FAILED"; exit 1)

echo "All tests passed!"
```

Add to CI pipeline:

```yaml
deploy-staging:
  script:
    - curl -X POST $STAGING_GATEWAY/sync ...
    - sleep 2
    - chmod +x scripts/test-apis.sh
    - ./scripts/test-apis.sh $STAGING_URL
```

## Monitoring After Deploy

Always check these signals post-deployment:

```bash
# Is the gateway responding?
curl -f http://gateway:8081/health

# Check error rates in the last 5 minutes
curl http://gateway:8081/observability/metrics | grep rah_request_errors_total

# Spot-check latency
curl -w "Response time: %{time_total}s\n" http://gateway:8081/v1/my-api
```

If you notice spikes in errors or latency, rollback immediately (see Rollback Procedure above).

## Common Error Scenarios

### Validation passes locally but fails in CI

**Cause**: Different rah-sync or gateway versions.

**Solution**: Pin versions:
```yaml
- name: Validate
  run: |
    export RAH_GATEWAY_VERSION=v1.2.3
    curl -L https://releases.example.com/rah-sync/${RAH_GATEWAY_VERSION}/rah-sync -o rah-sync
    chmod +x rah-sync
    ./rah-sync --validate --dir ./apis/
```

### Deployment succeeds but APIs don't respond

**Check**:
1. Gateway is live: `curl -f https://gateway:8081/health`
2. Routes are registered: `curl https://gateway:8081/observability/routes | grep my-api`
3. Upstream is reachable: Check gateway logs for connection errors

### Timeout during deployment

**Cause**: Large bundle taking too long to compile.

**Solution**:
- Increase timeout in curl: `curl --max-time 120 ...`
- Or split into smaller syncs:
  ```bash
  curl ... --data-binary @apis/batch-1.yaml
  curl ... --data-binary @apis/batch-2.yaml
  curl ... --data-binary @apis/batch-3.yaml
  ```

## Best Practices

### Do's

- **Always validate first**: Test locally with `rah-sync --validate`
- **Use draft mode in staging**: Preview with `?draft=true` before going live
- **Run tests after deploy**: Every deployment needs smoke tests
- **Create releases for production**: Makes rollback easy
- **Keep audit trail**: Log all deployments (who, what, when, where)
- **Use manual approval gates**: Require human sign-off before prod
- **Monitor post-deploy**: Check metrics and logs for 5-10 minutes after

### Don'ts

- **Don't skip validation**: Catches 90% of issues before production
- **Don't combine multiple changes**: Deploy one logical unit at a time for easier rollback
- **Don't hardcode secrets**: Use environment variables or secrets manager
- **Don't deploy Friday afternoon**: Schedule during business hours
- **Don't ignore CI warnings**: Lint warnings often hide subtle bugs
- **Don't forget to test**: A passing deploy doesn't mean the API works

## Example: Complete Production Deployment

Safe, step-by-step production rollout:

```bash
#!/bin/bash
set -e

# 1. Validate
echo "Step 1: Validating YAML..."
curl -X POST "http://staging:8081/sync?draft=true" \
  -H "Content-Type: application/json" \
  --data-binary @apis/my-apis.yaml \
  --fail-with-body
echo "✓ Validation passed"

# 2. Deploy to staging
echo "Step 2: Deploying to STAGING..."
curl -X POST "http://staging:8081/sync" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $STAGING_TOKEN" \
  --data-binary @apis/my-apis.yaml
sleep 2

# 3. Test staging
echo "Step 3: Testing STAGING..."
curl --fail http://staging-url:8080/health
./scripts/test-apis.sh http://staging-url:8080
echo "✓ Staging tests passed"

# 4. Manual confirmation
echo ""
echo "Step 4: Ready to deploy to PRODUCTION"
read -p "Type 'yes' to continue: " CONFIRM
[ "$CONFIRM" == "yes" ] || exit 0

# 5. Create release
echo "Step 5: Creating release..."
RELEASE=$(curl -X POST "http://studio:8082/api/releases" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $STUDIO_TOKEN" \
  -d '{
    "tag": "v'$(date +%Y%m%d-%H%M%S)'",
    "git_commit": "'$(git rev-parse HEAD)'",
    "author": "'$USER'"
  }')
RELEASE_ID=$(echo $RELEASE | jq -r .id)
echo "✓ Release created: $RELEASE_ID"

# 6. Deploy release to production
echo "Step 6: Deploying to PRODUCTION..."
curl -X POST "http://studio:8082/api/deploy" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $STUDIO_TOKEN" \
  -d '{
    "release_id": "'$RELEASE_ID'",
    "targets": ["production"]
  }'
sleep 2

# 7. Verify
echo "Step 7: Verifying production..."
curl --fail http://prod-url:8080/health
./scripts/test-apis.sh http://prod-url:8080
echo "✓ Production verified"

echo ""
echo "✓ Production deployment complete!"
```

## Frequently Asked Questions

**Q: Can I deploy just one API without redeploying all of them?**
A: Yes. Create a YAML file with only that API and sync it. Other APIs remain unchanged.

**Q: What if a deployment partially succeeds?**
A: The gateway is atomic — either all resources in your bundle deploy, or none do. Check logs for the error and retry.

**Q: How do I know if my API is actually being used?**
A: Check access logs: `curl http://gateway:8081/observability/access-log | grep my-api`

**Q: Can I deploy the same YAML to multiple gateways?**
A: Yes. Loop through your gateway URLs in CI:
```bash
for GATEWAY in $DEV_URL $STAGING_URL $PROD_URL; do
  curl -X POST "$GATEWAY/sync" \
    -H "Content-Type: application/json" \
    --data-binary @apis/my-apis.yaml
done
```

**Q: How do I test if an API works before users see it?**
A: Use draft mode: `curl http://gateway:8081/sync?draft=true --data-binary @apis.yaml`. Changes compile but don't go live.

---

For more details on the sync utility itself, see [sync-utility.md](sync-utility.md).
