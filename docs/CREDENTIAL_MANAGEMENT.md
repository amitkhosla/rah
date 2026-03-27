# Credential Management in RAH Gateway

> **Status**: Phases 1–6 implemented. Pending: rotation invalidation, in-memory fallback, pre-warm, Studio UI.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Architecture](#2-architecture)
3. [Secret References (Provider URIs)](#3-secret-references-provider-uris)
4. [Provider Reference](#4-provider-reference)
   - [env: / file:// (built-in)](#41-env--file-built-in)
   - [enc: — encrypted-at-rest (built-in)](#42-enc--encrypted-at-rest-built-in)
   - [gsm:// — Google Secret Manager](#43-gsm--google-secret-manager)
   - [vault:// — HashiCorp Vault](#44-vault--hashicorp-vault)
   - [awssm:// — AWS Secrets Manager](#45-awssm--aws-secrets-manager)
   - [googletoken:// — Google ID Token](#46-googletoken--google-id-token)
   - [oauth2token:// — OAuth2 Client Credentials](#47-oauth2token--oauth2-client-credentials)
5. [CredentialRegistry](#5-credentialregistry)
   - [Concept](#51-concept)
   - [REST API](#52-rest-api)
   - [Tenant Scoping](#53-tenant-scoping)
6. [Using Credentials in Flows](#6-using-credentials-in-flows)
   - [load_secret — raw reference](#61-load_secret--raw-reference)
   - [load_credential — named lookup](#62-load_credential--named-lookup)
7. [Gateway Configuration](#7-gateway-configuration)
   - [Full secrets block](#71-full-secrets-block)
   - [Datastore domain](#72-datastore-domain)
8. [Build Tags](#8-build-tags)
9. [Extending — Adding a New Provider](#9-extending--adding-a-new-provider)
10. [Security Model](#10-security-model)
11. [Deployment Recipes](#11-deployment-recipes)
    - [Standalone / dev](#111-standalone--dev)
    - [GCP / GKE](#112-gcp--gke)
    - [AWS / EKS](#113-aws--eks)
    - [On-premises with Vault](#114-on-premises-with-vault)
    - [Multi-cloud with OAuth2](#115-multi-cloud-with-oauth2)
12. [Pending Work](#12-pending-work)

---

## 1. Overview

RAH's credential management system allows flows to access secrets **without embedding raw values in config files or flow definitions**. Any string field that would otherwise be a plaintext credential is instead a *reference* that the gateway resolves at startup or request time.

**Key design goals:**

| Goal | How it is met |
|---|---|
| No plaintext in config files | Provider URI schemes (`env:`, `gsm://`, etc.) |
| No plaintext in flow definitions | `load_credential` uses a logical name, not a raw ref |
| No restart after secret rotation | 30-min cache TTL + planned rotation invalidation endpoint |
| Pluggable cloud backends | Registration pattern (`init()` + blank import + build tag) |
| Multi-tenant credential isolation | Tenant-scoped override in CredentialRegistry |
| Secure in-memory handling | All secrets stored as `[]byte`; zeroed on cache eviction |

---

## 2. Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  Config file / env vars                                         │
│  (credential refs: "gsm://...", "env:VAR", "vault://...")       │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌────────────────── secrets.Manager ──────────────────────────────┐
│                                                                 │
│   ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────┐  │
│   │  env     │  │  file    │  │  enc     │  │ gsm/vault/   │  │
│   │ provider │  │ provider │  │ provider │  │ awssm/token  │  │
│   └──────────┘  └──────────┘  └──────────┘  └──────────────┘  │
│                                                                 │
│   singleflight coalescing  ──  in-memory cache (30 min TTL)    │
│                                zeroing on eviction              │
└────────┬────────────────────────────────────────────────────────┘
         │
         │  Resolve(ctx, ref) → []byte
         │
   ┌─────┴─────────────────────────────────────────────┐
   │                                                   │
   ▼                                                   ▼
┌──────────────────────────┐           ┌───────────────────────────┐
│  DataStoreManager        │           │  CredentialRegistry       │
│  (startup only)          │           │  (runtime, persisted)     │
│                          │           │                           │
│  Resolves store Username │           │  Maps name → ref          │
│  and Password fields     │           │  with tenant overrides    │
│  before connecting       │           │                           │
└──────────────────────────┘           └──────────────┬────────────┘
                                                       │
                                                       │ Resolve(name, tenant)
                                                       ▼
                                         ┌─────────────────────────┐
                                         │  load_credential step   │
                                         │  in flow execution      │
                                         │  → ctx.ByteSlots[slot]  │
                                         └─────────────────────────┘
```

### Component responsibilities

| Component | File | Responsibility |
|---|---|---|
| `secrets.Manager` | `internal/secrets/manager.go` | Resolve refs, cache, singleflight, provider routing |
| `secrets.CredentialRegistry` | `internal/secrets/registry.go` | Named credential store with tenant overrides |
| `control.DataStoreManager` | `internal/control/datastore_manager.go` | Resolves store credentials at startup |
| `control.CredentialHandler` | `internal/control/credential_handler.go` | REST CRUD for CredentialRegistry |
| `steps.LoadSecret` | `internal/engine/steps/secret.go` | Instruction: raw ref → ByteSlot |
| `steps.LoadCredential` | `internal/engine/steps/credential.go` | Instruction: name → ByteSlot (via registry) |

---

## 3. Secret References (Provider URIs)

A *secret reference* is a string that tells the gateway **where** to fetch a value. Use these anywhere a credential is expected.

```
env:VAR_NAME                                   — environment variable
$VAR_NAME                                      — env variable (shell syntax)
${VAR_NAME}                                    — env variable (shell syntax)
file:///absolute/path/to/file                  — file contents (whitespace trimmed)
enc:base64encodedCiphertext                    — AES-256-GCM encrypted value
gsm://projects/my-proj/secrets/my-key/versions/latest
gsm://my-secret                                — short form (needs gsm.project in config)
vault://secret/myapp/config#api_key
awssm://us-east-1/prod/stripe-key
awssm://prod/database#password
googletoken://https://my-service.run.app
oauth2token://payment-service
<any other string>                             — treated as literal plaintext (dev only)
```

> **Rule**: The scheme prefix determines which provider resolves the reference. Providers that are not compiled in (missing build tag) will cause a startup error if referenced.

---

## 4. Provider Reference

### 4.1 `env:` / `file://` (built-in)

Always available, no build tag needed.

| Scheme | Example | Notes |
|---|---|---|
| `env:VAR` | `env:DB_PASSWORD` | Reads environment variable at resolution time |
| `$VAR` | `$DB_PASSWORD` | Alias for `env:` |
| `${VAR}` | `${DB_PASSWORD}` | Alias for `env:` |
| `file:///path` | `file:///run/secrets/api-key` | File contents, newlines trimmed — ideal for K8s Secrets mounted as files |

### 4.2 `enc:` — encrypted-at-rest (built-in)

Stores AES-256-GCM encrypted values directly in the config file. Useful when you want secrets in source control without a secret manager.

**Config:**
```yaml
secrets:
  encrypted:
    enabled: true
    key: "env:RAH_MASTER_KEY"   # 32-byte base64-encoded key
```

**Usage:**
```yaml
datastores:
  stores:
    redis_main:
      connection:
        password: "enc:dGhpcyBpcyBhIHRlc3Q..."   # generated by EncryptValue helper
```

**Generating an encrypted value** (Go helper):
```go
import "rah/internal/secrets"

key, _ := secrets.GenerateMasterKey()        // generates a random 32-byte key
ciphertext, _ := secrets.EncryptValue(key, []byte("my-plaintext-secret"))
// store key in env:RAH_MASTER_KEY
// store ciphertext in config as "enc:<ciphertext>"
```

### 4.3 `gsm://` — Google Secret Manager

**Build tag:** `-tags gsm`
**SDK:** `go get cloud.google.com/go/secretmanager@latest`

**Config:**
```yaml
secrets:
  gsm:
    enabled: true
    project: "my-gcp-project"          # default project for short-form URIs
    credentials_file: ""               # leave empty for ADC (recommended)
    # credentials_file: "file:///run/secrets/sa-key.json"   # explicit SA key
    # credentials_file: "env:GOOGLE_APPLICATION_CREDENTIALS"
```

**URI formats:**
```
gsm://projects/my-proj/secrets/stripe-key/versions/latest  — full form
gsm://projects/my-proj/secrets/stripe-key                  — defaults to /versions/latest
gsm://stripe-key                                            — uses default project
gsm://stripe-key/versions/3                                 — specific version
```

**Authentication priority:**
1. `credentials_file` if set (service account JSON)
2. `GOOGLE_APPLICATION_CREDENTIALS` env var
3. GKE Workload Identity / Cloud Run service account (zero config)
4. `gcloud auth application-default login` (local dev)

### 4.4 `vault://` — HashiCorp Vault

**Build tag:** `-tags vault`
**SDK:** `go get github.com/hashicorp/vault/api@latest`

**Config:**
```yaml
secrets:
  vault:
    enabled: true
    addr: "https://vault.internal:8200"    # or env:VAULT_ADDR

    # Token auth (dev/test only):
    auth: token
    token: "env:VAULT_TOKEN"

    # AppRole auth (production):
    auth: approle
    role_id:   "env:VAULT_ROLE_ID"
    secret_id: "env:VAULT_SECRET_ID"

    # Kubernetes auth (GKE, EKS, AKS):
    auth: kubernetes
    role: "my-vault-role"    # Vault role name bound to the K8s service account
```

**URI formats:**
```
vault://secret/myapp/config#password     — KV v2, field "password"
vault://secret/myapp/config              — KV v2, full secret as JSON
vault://kv/database/prod#host
```

### 4.5 `awssm://` — AWS Secrets Manager

**Build tag:** `-tags awssm`
**SDK:**
```
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/service/secretsmanager@latest
```

**Config:**
```yaml
secrets:
  aws_sm:
    enabled: true
    region: "us-east-1"                  # default region; also reads AWS_REGION

    # Explicit credentials (use IAM role / IRSA instead when possible):
    # access_key: "env:AWS_ACCESS_KEY_ID"
    # secret_key: "env:AWS_SECRET_ACCESS_KEY"
```

**URI formats:**
```
awssm://prod/stripe-key                  — uses default region
awssm://us-west-2/prod/stripe-key        — overrides region
awssm://prod/database#password           — parse JSON secret, return field
awssm://us-east-1/prod/database#host
```

**Region detection:** The first path segment is treated as a region if it starts with a known AWS prefix (`us-`, `eu-`, `ap-`, `sa-`, `ca-`, `me-`, `af-`, `il-`, `mx-`). Otherwise the entire path is the secret name using the default region.

### 4.6 `googletoken://` — Google ID Token

**Build tag:** `-tags googletoken`
**No new deps** (uses `google.golang.org/api` already in go.mod)

**Use case:** Calling Cloud Run services, Cloud Functions, or any Google-protected endpoint that requires a caller identity JWT.

**Config:**
```yaml
secrets:
  google_token:
    enabled: true
```

**URI format:**
```
googletoken://https://my-service-abc123-uc.a.run.app
```

**Caching:** Token cached until 5 minutes before expiry (typically ~55 minutes for a 60-minute token).

**Authentication:** Uses ADC — same chain as GSM. No additional config needed on GKE/Cloud Run.

### 4.7 `oauth2token://` — OAuth2 Client Credentials

**Build tag:** `-tags oauth2token`
**No new deps** (uses `golang.org/x/oauth2` already in go.mod)

**Use case:** Calling any service that uses standard OAuth2 machine-to-machine authentication (Auth0, Okta, Azure AD, internal auth servers).

**Config:**
```yaml
secrets:
  oauth2:
    clients:
      payment-service:
        token_url:     "https://auth.payment.example.com/oauth/token"
        client_id:     "rah-gateway-client"
        client_secret: "env:PAYMENT_CLIENT_SECRET"
        scopes:        ["payment.read", "payment.write"]

      analytics:
        token_url:     "https://auth.analytics.internal/token"
        client_id:     "rah-analytics"
        client_secret: "file:///run/secrets/analytics-secret"
        scopes:        []
```

**URI format:**
```
oauth2token://payment-service    — fetches token for the named client
oauth2token://analytics
```

**Caching:** Token cached until 1 minute before expiry (uses `expires_in` from token response). If no expiry returned, uses 30-minute default.

---

## 5. CredentialRegistry

### 5.1 Concept

The `CredentialRegistry` decouples flow definitions from credential infrastructure.

**Without CredentialRegistry:**
```yaml
# Flow embeds the raw secret location — breaks on environment change
steps:
  - action: load_secret
    source: "gsm://projects/prod-project/secrets/stripe-key/versions/latest"
    as: stripe_key
```

**With CredentialRegistry:**
```yaml
# Flow uses a logical name — ops manages where the secret lives
steps:
  - action: load_credential
    source: stripe-key    # logical name
    as: stripe_key
```

Ops registers the mapping once:
```bash
curl -X POST http://gateway:8081/credentials \
  -H "Content-Type: application/json" \
  -d '{"name": "stripe-key", "ref": "gsm://projects/prod-project/secrets/stripe-key/versions/latest"}'
```

When the secret moves to a different location (new project, different secret manager), only the registry entry needs updating — no flow changes, no redeployment.

### 5.2 REST API

All endpoints are on the **management port** (default `:8081`).

The `CredentialRegistry` is only active when `credentials` is configured as a datastore domain. See [§7.2](#72-datastore-domain).

---

#### `POST /credentials` — Create or update

```http
POST /credentials
Content-Type: application/json

{
  "name":        "stripe-key",
  "ref":         "gsm://projects/my-proj/secrets/stripe-key/versions/latest",
  "tenant":      "acme-corp",     // optional; omit for global credential
  "description": "Stripe API key for payment processing"
}
```

**Response:** `201 Created`
```json
{"status": "ok", "name": "stripe-key"}
```

**Notes:**
- `name` — logical identifier used in flow steps. Use descriptive names (`stripe-key`, `db-password`, `payment-oauth-token`).
- `ref` — any valid provider URI (`gsm://`, `vault://`, `env:`, etc.)
- `tenant` — if set, this entry overrides the global credential only for this tenant

---

#### `GET /credentials` — List

```http
GET /credentials?tenant=acme-corp
```

**Response:** `200 OK`
```json
{
  "credentials": ["stripe-key", "db-password", "analytics-token"],
  "tenant": "acme-corp"
}
```

Omit `?tenant` to list global credentials.

---

#### `GET /credentials/{name}` — Get entry

```http
GET /credentials/stripe-key?tenant=acme-corp
```

**Response:** `200 OK`
```json
{
  "ref":         "gsm://projects/my-proj/secrets/stripe-key/versions/latest",
  "description": "Stripe API key for payment processing",
  "created_at":  "2026-03-25T10:00:00Z",
  "updated_at":  "2026-03-25T10:00:00Z"
}
```

> **Security note:** The `ref` field contains the secret *location*, not the secret *value*. The plaintext value is never exposed via the API.

---

#### `DELETE /credentials/{name}` — Delete

```http
DELETE /credentials/stripe-key?tenant=acme-corp
```

**Response:** `204 No Content`

---

### 5.3 Tenant Scoping

Resolution order for `load_credential name=stripe-key, tenant=acme-corp`:

```
1. Look for "stripe-key" scoped to "acme-corp"  → found? use it
2. Look for "stripe-key" in global scope         → found? use it
3. Error: credential not found
```

This lets you have a default credential that works for all tenants, with per-tenant overrides where needed:

```bash
# Global fallback — shared Stripe test key
POST /credentials
{"name": "stripe-key", "ref": "env:STRIPE_TEST_KEY"}

# Override for production tenant — their own live key in GSM
POST /credentials
{"name": "stripe-key", "ref": "gsm://projects/acme-prod/secrets/stripe-live", "tenant": "acme-corp"}
```

---

## 6. Using Credentials in Flows

### 6.1 `load_secret` — raw reference

Resolves a provider URI directly and writes the value to a ByteSlot. Use for gateway-level secrets that don't need per-tenant overrides.

```yaml
steps:
  - action: load_secret
    source: "gsm://projects/my-proj/secrets/internal-api-key/versions/latest"
    as: internal_key

  - action: set_header
    key: "X-Internal-Auth"
    as: internal_key
```

**When to use:** Gateway-level tokens, shared infrastructure credentials, bootstrap secrets that don't vary per tenant.

### 6.2 `load_credential` — named lookup

Resolves a credential by logical name from the CredentialRegistry, with tenant-scoped fallback. Use for credentials that may differ per tenant or that ops needs to manage independently of flow deployments.

```yaml
steps:
  # Fetch tenant-specific or global credential
  - action: load_credential
    source: stripe-key        # logical name registered via POST /credentials
    as: stripe_key

  # Use the resolved value in a header
  - action: set_header
    key: "Authorization"
    as: stripe_key
```

**Full example — multi-tenant API key injection:**

```yaml
# Flow definition
flows:
  inject_upstream_auth:
    - action: load_credential
      source: upstream-api-key    # each tenant has their own key
      as: api_key
    - action: set_header
      key: "X-API-Key"
      as: api_key
    - action: http_call
      url_var: service_url
      method: GET

apis:
  - path: /v1/data
    flow: inject_upstream_auth
```

```bash
# Ops registers per-tenant keys once — no flow changes needed
POST /credentials {"name": "upstream-api-key", "ref": "gsm://projects/tenantA/secrets/api-key", "tenant": "tenant-a"}
POST /credentials {"name": "upstream-api-key", "ref": "gsm://projects/tenantB/secrets/api-key", "tenant": "tenant-b"}
POST /credentials {"name": "upstream-api-key", "ref": "env:DEFAULT_API_KEY"}  # global fallback
```

---

## 7. Gateway Configuration

### 7.1 Full secrets block

```yaml
secrets:

  # Built-in: encrypted-at-rest values
  encrypted:
    enabled: true
    key: "env:RAH_MASTER_KEY"        # 32-byte AES key, base64-encoded

  # Google Secret Manager (requires -tags gsm)
  gsm:
    enabled: true
    project: "my-gcp-project"
    credentials_file: ""             # empty = use ADC

  # HashiCorp Vault (requires -tags vault)
  vault:
    enabled: true
    addr: "https://vault.internal:8200"
    auth: kubernetes
    role: "rah-gateway"

  # AWS Secrets Manager (requires -tags awssm)
  aws_sm:
    enabled: true
    region: "us-east-1"

  # Google ID tokens (requires -tags googletoken)
  google_token:
    enabled: true

  # OAuth2 client credentials (requires -tags oauth2token)
  oauth2:
    clients:
      payment-service:
        token_url:     "https://auth.payment.example.com/token"
        client_id:     "rah-client"
        client_secret: "env:PAYMENT_SECRET"
        scopes:        ["payment.read"]
```

### 7.2 Datastore domain

The `CredentialRegistry` requires a `credentials` domain binding in the datastore config. Without it, `load_credential` is disabled.

```yaml
datastore:
  stores:
    redis_primary:
      kind: redis
      connection:
        address: "redis:6379"

  bindings:
    api_definitions: redis_primary
    flows:           redis_primary
    tenant_data:     redis_primary
    cache:           redis_primary
    credentials:     redis_primary    # ← enables CredentialRegistry
```

For standalone/dev deployments, disk store works too:

```yaml
datastore:
  stores:
    local_disk:
      kind: disk
      connection:
        address: "./data"

  bindings:
    # ... other bindings ...
    credentials: local_disk
```

---

## 8. Build Tags

| Tag | Provider | Extra deps needed |
|---|---|---|
| *(none)* | `env:`, `file://`, `enc:` | — |
| `gsm` | Google Secret Manager | `go get cloud.google.com/go/secretmanager@latest` |
| `vault` | HashiCorp Vault | `go get github.com/hashicorp/vault/api@latest` |
| `awssm` | AWS Secrets Manager | `go get github.com/aws/aws-sdk-go-v2/config@latest`<br>`go get github.com/aws/aws-sdk-go-v2/service/secretsmanager@latest` |
| `googletoken` | Google ID Token | *(already in go.mod)* |
| `oauth2token` | OAuth2 client creds | *(already in go.mod)* |

**Build examples:**
```bash
# Standard build — no cloud deps
go build ./cmd/rah-gateway/

# GCP deployment
go build -tags gsm,googletoken ./cmd/rah-gateway/

# AWS deployment
go build -tags awssm ./cmd/rah-gateway/

# On-premises
go build -tags vault,oauth2token ./cmd/rah-gateway/

# Everything
go build -tags "gsm,vault,awssm,googletoken,oauth2token" ./cmd/rah-gateway/
```

---

## 9. Extending — Adding a New Provider

Adding a provider requires **zero changes to core code**. Only new files.

**Step 1:** Create `internal/secrets/<name>/doc.go` (no build tag, keeps package visible to tooling)

**Step 2:** Create `internal/secrets/<name>/provider.go` with build tag:

```go
//go:build mycloud

package mycloud

import (
    "context"
    "rah/internal/config"
)

type Provider struct{ /* SDK client */ }

// New returns (nil, nil) when not enabled — Manager skips silently.
func New(ctx context.Context, cfg config.SecretsConfig, bootstrap BootstrapResolver) (*Provider, error) {
    if !cfg.MyCloud.Enabled {
        return nil, nil
    }
    // initialise SDK client ...
    return &Provider{}, nil
}

func (p *Provider) Scheme() string { return "mycloud" }

func (p *Provider) Resolve(ctx context.Context, ref string) ([]byte, error) {
    // fetch secret ...
}
```

> If your provider returns tokens with known expiry, also implement `TTLProvider`:
> ```go
> func (p *Provider) ResolveTTL(ctx context.Context, ref string) ([]byte, time.Duration, error) {
>     // return value, ttl-before-expiry, err
> }
> ```

**Step 3:** Create `cmd/rah-gateway/providers_<name>.go` with the same build tag:

```go
//go:build mycloud

package main

import (
    "context"
    "rah/internal/config"
    "rah/internal/secrets"
    "rah/internal/secrets/mycloud"
)

func init() {
    secrets.RegisterProviderFactory("mycloud",
        func(ctx context.Context, cfg config.SecretsConfig, bootstrap secrets.Resolver) (secrets.Provider, error) {
            p, err := mycloud.New(ctx, cfg, bootstrap)
            if err != nil || p == nil {
                return nil, err
            }
            return p, nil
        },
    )
}
```

**Step 4:** Add config types to `internal/config/secrets.go`:
```go
type MyCloudConfig struct {
    Enabled bool   `json:"enabled" yaml:"enabled"`
    Addr    string `json:"addr,omitempty" yaml:"addr,omitempty"`
    // ...
}
```

**Step 5:** Add field to `SecretsConfig`:
```go
type SecretsConfig struct {
    // existing fields ...
    MyCloud MyCloudConfig `json:"my_cloud,omitempty" yaml:"my_cloud,omitempty"`
}
```

**Step 6:** `go get` the SDK, build with `-tags mycloud`.

That is all — no changes to Manager, Compiler, or any other package.

---

## 10. Security Model

### Memory safety

- All secret values are `[]byte` throughout — never `string`
- Cache entries are **zeroed** (`clear(slice)`) on eviction and on gateway shutdown
- Providers zero their bootstrap credentials after use (e.g. GSM service account JSON)
- `ResolveString()` converts to string at the last moment and zeroes the `[]byte` immediately after

### Singleflight deduplication

Concurrent requests for the same ref share a single provider call. Each caller receives a **fresh copy** of the `[]byte` so they can zero independently.

### Bootstrap isolation

Provider credentials (e.g. the Vault token, the GSM service account key path) must themselves use only `env:` or `file://` references — not another secrets manager. This prevents circular resolution and ensures credentials are available before any cloud provider is initialised.

### CredentialRegistry exposes refs, not values

`GET /credentials/{name}` returns the *reference* (e.g. `gsm://...`), never the plaintext secret. Access to the management port should be restricted to internal networks / ops tooling.

### Principle of least privilege

- GSM: use a dedicated service account with only `secretmanager.secretAccessor` on specific secrets
- Vault: use AppRole or Kubernetes auth with a policy scoped to only the paths RAH needs
- AWS SM: use IRSA with a policy scoped to `secretsmanager:GetSecretValue` on specific ARNs

---

## 11. Deployment Recipes

### 11.1 Standalone / dev

No cloud services. Use `env:` references in config, or `enc:` for sensitive values.

```yaml
secrets:
  encrypted:
    enabled: true
    key: "env:RAH_MASTER_KEY"

datastore:
  stores:
    local:
      kind: disk
      connection:
        address: "./data"
  bindings:
    api_definitions: local
    flows:           local
    tenant_data:     local
    cache:           local
    credentials:     local
```

```bash
export RAH_MASTER_KEY="<base64-32-bytes>"
go build ./cmd/rah-gateway/
./rah-gateway -config gateway.yaml
```

### 11.2 GCP / GKE

Use Workload Identity — no credentials file needed.

```yaml
secrets:
  gsm:
    enabled: true
    project: "my-gcp-project"
  google_token:
    enabled: true    # if calling other Cloud Run services
```

```bash
go build -tags "gsm,googletoken" ./cmd/rah-gateway/
```

In GKE, annotate the service account:
```bash
kubectl annotate serviceaccount rah-gateway \
  iam.gke.io/gcp-service-account=rah-gateway@my-project.iam.gserviceaccount.com
```

### 11.3 AWS / EKS

Use IRSA — no access keys needed.

```yaml
secrets:
  aws_sm:
    enabled: true
    region: "us-east-1"
```

```bash
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/service/secretsmanager@latest
go build -tags awssm ./cmd/rah-gateway/
```

Annotate the EKS service account for IRSA:
```bash
kubectl annotate serviceaccount rah-gateway \
  eks.amazonaws.com/role-arn=arn:aws:iam::123456789:role/rah-gateway-role
```

### 11.4 On-premises with Vault

```yaml
secrets:
  vault:
    enabled: true
    addr: "https://vault.corp.internal:8200"
    auth: approle
    role_id:   "env:VAULT_ROLE_ID"
    secret_id: "env:VAULT_SECRET_ID"
```

```bash
go get github.com/hashicorp/vault/api@latest
go build -tags vault ./cmd/rah-gateway/
```

### 11.5 Multi-cloud with OAuth2

```yaml
secrets:
  gsm:
    enabled: true
    project: "main-gcp-project"
  aws_sm:
    enabled: true
    region: "eu-west-1"
  oauth2:
    clients:
      third-party-api:
        token_url:     "https://auth.vendor.com/token"
        client_id:     "my-client-id"
        client_secret: "gsm://projects/main-gcp-project/secrets/vendor-secret"
```

```bash
go build -tags "gsm,awssm,oauth2token" ./cmd/rah-gateway/
```

---

## 12. Pending Work

The following items are **not yet implemented** and represent the next development priorities.

---

### 12.1 Secret rotation invalidation (HIGH priority)

**Problem:** After rotating a secret in GSM/Vault/AWS SM, the gateway serves the old cached value for up to 30 minutes.

**Solution:** Management endpoint to evict a specific secret from the in-memory cache, forcing a fresh fetch on next access.

```
DELETE /secrets/cache?ref=gsm://projects/my-proj/secrets/stripe-key/versions/latest
DELETE /secrets/cache?name=stripe-key&tenant=acme-corp   (for CredentialRegistry entries)
```

**Files to change:**
- `internal/secrets/cache.go` — add `evict(ref string)` method
- `internal/secrets/manager.go` — expose `InvalidateCache(ref string)` on Manager
- `internal/control/credential_handler.go` — add `DELETE /secrets/cache` handler
- `cmd/rah-gateway/main.go` — register the route

**Complexity:** Low — the cache already has per-entry eviction, just needs an API endpoint.

---

### 12.2 CredentialRegistry in-memory fallback (MEDIUM priority)

**Problem:** `load_credential` is silently disabled when the `credentials` domain is not configured in the datastore. This makes standalone/dev deployments unable to use named credentials.

**Solution:** When no `CredentialStore` is configured, fall back to an in-memory `map[string]CredentialEntry`. Entries are lost on restart but sufficient for dev/testing.

```go
// In-memory store backed by a sync.RWMutex-protected map.
type memCredentialStore struct {
    mu   sync.RWMutex
    data map[string]map[string][]byte // tenant → name → entry JSON
}
```

**Files to change:**
- `internal/secrets/registry.go` — add `NewMemCredentialStore()` constructor
- `internal/control/credential_handler.go` — update `NewCredentialStore` to return mem store when domain unconfigured
- `cmd/rah-gateway/main.go` — remove the conditional `credReg != nil` check (always enabled)

**Complexity:** Low.

---

### 12.3 Pre-warm at bake time (MEDIUM priority)

**Problem:** When a flow containing `load_secret` / `load_credential` is deployed via `POST /sync`, the first real request sees a cold provider call (network latency to GSM/Vault/etc.) instead of a cache hit.

**Solution:** After compiling a flow, scan the instruction table for `load_secret` and `load_credential` instructions, extract their refs/names, and proactively call `Resolve()` to populate the cache before any traffic arrives.

**Files to change:**
- `internal/engine/steps/secret.go` — expose the `ref` field so the compiler can inspect it
- `internal/engine/steps/credential.go` — expose the `name` field
- `internal/control/management_server.go` — in `ApplyUnifiedSync`, after baking a flow call a `prewarmSecrets(instructions)` helper
- `internal/secrets/manager.go` — `PreWarm(ctx, refs []string)` convenience method

**Complexity:** Medium — requires a way to introspect compiled instructions.

---

### 12.4 Studio UI for credential management (LOW priority)

**Problem:** The `/credentials` REST API has no web UI. Ops must use curl to manage entries.

**Solution:** Add a `Credentials` tab to the Studio UI, mirroring the existing `Tenants` component.

**UI features:**
- Left sidebar: list credentials (global + per-tenant tabs)
- Detail panel: name, ref (display as `gsm://...` without revealing value), description, timestamps
- Create form: name, ref, optional tenant, description
- Delete with confirmation
- Tenant selector to switch scopes

**Files to change:**
- `internal/studio/ui/src/components/Credentials.tsx` — new component
- `internal/studio/ui/src/api.ts` — add `listCredentials`, `getCredential`, `upsertCredential`, `deleteCredential`
- `internal/studio/ui/src/types.ts` — add `CredentialEntry`, `CredentialListResponse`
- `internal/studio/ui/src/App.tsx` — add Credentials tab
- `internal/studio/server.go` — proxy `/credentials` to management server

**Complexity:** Medium — follow the same pattern as the existing Tenants UI.

---

### 12.5 Credential rotation notifications (LOW priority)

**Problem:** When a secret is rotated, ops must manually call the cache invalidation endpoint (once §12.1 is done). This is error-prone.

**Solution:** Webhook receivers that cloud secret managers can call to trigger cache invalidation automatically.

| Cloud | Mechanism |
|---|---|
| GCP (GSM) | Pub/Sub notification on secret version creation → push subscription to RAH |
| AWS SM | EventBridge rule on `RotationSucceeded` → Lambda or direct HTTP call to RAH |
| Vault | Vault Agent with `auto-auth` and `secret-id rotation` |

**Files to change:**
- New `internal/control/rotation_webhook.go` — HTTP handlers for cloud rotation notifications
- Register webhook routes on management port

**Complexity:** High — requires cloud-specific integration work per provider.

---

### 12.6 Vault token renewal (LOW priority)

**Problem:** Vault tokens have a TTL. If the gateway runs longer than the token TTL, subsequent secret fetches will fail with `403 permission denied`.

**Solution:** Background goroutine that renews the Vault token before it expires using `client.Auth().Token().RenewSelf()`.

**Files to change:**
- `internal/secrets/vault/provider.go` — add `startRenewalLoop(ctx, client)` goroutine

**Complexity:** Low for token auth; Medium for AppRole (needs to re-login, not just renew).

---

### Summary of pending work

| # | Item | Priority | Complexity | Impact |
|---|---|---|---|---|
| 12.1 | Cache invalidation API | **HIGH** | Low | Enables secret rotation without restart |
| 12.2 | In-memory CredentialStore fallback | MEDIUM | Low | Standalone/dev experience |
| 12.3 | Pre-warm at bake time | MEDIUM | Medium | Eliminates cold-start latency on first request |
| 12.4 | Studio UI | LOW | Medium | Ops usability |
| 12.5 | Rotation webhooks | LOW | High | Automated rotation |
| 12.6 | Vault token renewal | LOW | Low–Medium | Long-running deployments with Vault |
