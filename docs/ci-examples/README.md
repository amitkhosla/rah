# RAH Bundle CI/CD Examples

This directory contains example configurations for integrating RAH bundle linting and publishing into your CI/CD pipeline.

## Overview

The `rah-sync` tool provides three main operations:

- **lint** — Validate bundle YAML/JSON for configuration errors
- **publish** — Upload bundle to Studio and optionally auto-deploy
- **promote** — Deploy an existing release to a target environment
- **diff** — Show differences between two releases

## Quick Start

### 1. Install rah-sync

#### Using the install script:
```bash
curl -fsSL https://github.com/anthropics/rah/raw/main/install.sh | bash
```

#### Or download from GitHub releases:
Visit [releases](https://github.com/anthropics/rah/releases) and download the appropriate binary for your OS/architecture.

#### Or build from source:
```bash
go build -o rah-sync ./cmd/rah-sync/
```

### 2. Quick test locally

```bash
# Lint a directory
rah-sync lint ./definitions

# Lint with strict mode (fail on warnings)
rah-sync lint ./definitions --strict

# Lint and output JSON
rah-sync lint ./definitions --output json
```

## CI/CD Platform Examples

### GitHub Actions

See `.github/workflows/rah-lint.yml` in the repository root.

**Features:**
- Lints on every PR with comment feedback
- Publishes to Studio on merge to main
- Includes git metadata (commit, branch, author)
- Automatic retry on transient failures

**Setup:**
1. Add `STUDIO_URL` secret to your repository (Settings → Secrets)
2. The workflow is automatically enabled

**Usage:**
```yaml
# On PR: Runs lint, comments results
# On merge to main: Publishes with auto-deploy to UAT
```

### GitLab CI

See `gitlab-ci.yml` in this directory.

**Features:**
- Lints on merge requests and main branch
- Manual publish button for main branch
- Automatic publish on tags
- JUnit report artifacts for lint results

**Setup:**
1. Add `STUDIO_URL` variable to GitLab CI/CD settings
2. Add `gitlab-ci.yml` to your `.gitlab-ci.yml`:
```yaml
include:
  - local: 'docs/ci-examples/gitlab-ci.yml'
```

**Usage:**
```bash
# Linting is automatic on MR/main
# Publishing: Click "Play" button in GitLab UI for main branch
# Tag releases: Automatically published when you push a tag
```

### Docker

See `Dockerfile.rah-sync` in the repository root.

**Build:**
```bash
docker build -f Dockerfile.rah-sync -t rah-sync:latest .
```

**Run locally:**
```bash
# Lint a directory
docker run -v $(pwd)/definitions:/workspace/definitions \
  rah-sync:latest lint /workspace/definitions

# Publish to Studio
docker run -v $(pwd)/definitions:/workspace/definitions \
  rah-sync:latest publish /workspace/definitions \
    --studio https://studio.example.com \
    --tag v1.0.0
```

**In Kubernetes:**
```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: rah-lint
spec:
  template:
    spec:
      containers:
      - name: rah-sync
        image: rah-sync:latest
        command:
        - rah-sync
        - lint
        - /workspace/definitions
        - --strict
        volumeMounts:
        - name: definitions
          mountPath: /workspace/definitions
      volumes:
      - name: definitions
        configMap:
          name: rah-definitions
      restartPolicy: Never
```

## Environment Variables

| Variable | Purpose | Example |
|----------|---------|---------|
| `STUDIO_URL` | Studio server endpoint | `https://studio.example.com` |
| `AUTO_DEPLOY_ENV` | Auto-deploy target on publish | `uat` or `staging` |
| `INSTALL_DIR` | Directory for binary installation | `/usr/local/bin` (default) |

## Workflow Patterns

### Pattern 1: Lint on PR, Publish on Merge

```
PR created
  → Lint runs automatically
  → Comment with results
  → Author fixes issues

Merge to main
  → Lint again (must pass)
  → Publish to Studio
  → Auto-deploy to UAT
```

**Implementation:**
- GitHub Actions: see `.github/workflows/rah-lint.yml`
- GitLab CI: see `gitlab-ci.yml` (manual publish)

### Pattern 2: Staged Deployments

```
Develop branch → Lint + Publish to dev
Staging branch  → Lint + Publish to staging
Main branch     → Lint + Publish to production
```

**Implementation with GitHub Actions:**
```yaml
on:
  push:
    branches:
      - develop
      - staging
      - main

jobs:
  deploy:
    runs-on: ubuntu-latest
    env:
      ENV_NAME: ${{ github.ref == 'refs/heads/main' && 'production' || github.ref == 'refs/heads/staging' && 'staging' || 'dev' }}
    steps:
      - uses: actions/checkout@v4
      - run: |
          go build -o bin/rah-sync ./cmd/rah-sync/
          ./bin/rah-sync publish ./definitions \
            --studio ${{ secrets.STUDIO_URL }} \
            --auto-deploy $ENV_NAME
```

### Pattern 3: Manual Promotion

```
Release created
  → Publish to Studio (manually via UI)
  → Select release from list
  → Promote to UAT (test)
  → Promote to Production (after approval)
```

**Command reference:**
```bash
# List releases
curl https://studio.example.com/api/releases

# Promote specific release
rah-sync promote release-abc123 --env uat --studio https://studio.example.com
```

### Pattern 4: Change Detection

```
Detect changes in definitions/
  → Run full linting + publishing
Unchanged
  → Skip CI (faster)
```

**GitHub Actions:**
```yaml
on:
  push:
    paths:
      - 'definitions/**'
      - '.github/workflows/rah-lint.yml'
```

## Troubleshooting

### ❌ Lint fails with "variable used but never assigned"

**Cause:** A step references a variable that isn't assigned earlier.

**Fix:**
```yaml
flows:
  - name: my_flow
    instructions:
      - action: http_call
        input:
          url: "https://api.example.com"
        as: response_data  # ← Assign the response
      - action: cache_put
        input:
          key: my_key
          value_var: response_data  # ← Now available
```

### ❌ Publish fails with "lint errors found"

**Cause:** Bundle has configuration errors.

**Fix:** Run linting locally to see details:
```bash
rah-sync lint ./definitions --strict
```

### ❌ Deploy fails with "release not found"

**Cause:** Release ID doesn't exist or was deleted.

**Fix:**
1. Check release ID: `curl https://studio.example.com/api/releases`
2. Verify STUDIO_URL is correct
3. Check network connectivity to Studio

### ❌ GitHub Actions can't find `go build`

**Cause:** Go setup step was skipped.

**Fix:** Add to your workflow:
```yaml
- name: Set up Go
  uses: actions/setup-go@v4
  with:
    go-version: '1.25'
```

## Security Considerations

1. **Secrets Management:**
   - Store `STUDIO_URL` in CI/CD secrets, not in version control
   - Use environment-scoped variables when possible

2. **Access Control:**
   - Restrict publish/promote permissions to authorized users
   - Use role-based access in your Studio instance

3. **Bundle Validation:**
   - Always run with `--strict` in CI/CD
   - Catch configuration errors early

4. **Audit Trail:**
   - Include git metadata (commit, branch, author)
   - Monitor deployments via Studio API

## Performance Tips

1. **Cache Dependencies:**
   - Cache Go modules: `~/.cache/go-build`, `/go/pkg/mod`
   - Reuse Docker layers with multi-stage builds

2. **Parallel Linting:**
   ```bash
   # Split definitions into multiple directories
   rah-sync lint ./definitions/apis &
   rah-sync lint ./definitions/flows &
   wait
   ```

3. **Early Exit:**
   ```bash
   rah-sync lint ./definitions || exit 1  # Stop if linting fails
   rah-sync publish ./definitions --studio ...
   ```

## Next Steps

- Review Studio documentation for release management UI
- Set up approval workflows for production deployments
- Integrate with your team's notification system (Slack, Teams, etc.)
- Monitor deployments via Studio metrics dashboard
