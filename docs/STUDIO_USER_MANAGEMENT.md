# RAH Studio — User Management Guide

_Covers first-run setup, daily operations, API reference, and automation._

---

## How Studio auth works

RAH Studio has its **own** user store that is completely independent of the gateway management API.

```
Browser → POST /api/auth/login
        → StudioUserStore.Authenticate()   (bcrypt, local)
        → session cookie set (httpOnly, 8 h TTL)

Studio → Management API proxy calls
        → RAH_GATEWAY_AUTH_USERNAME / RAH_GATEWAY_AUTH_PASSWORD
          (a dedicated service account — unrelated to Studio users)
```

Studio users never touch the gateway's `/admin/users` endpoint. Gateway credentials are a separate concern configured via env vars.

---

## First-run bootstrap

On startup, Studio checks for users in this priority order:

| Priority | Source | `must_change_password` |
|---|---|---|
| 1 | `auth_users` in config file | No (operator explicitly set it) |
| 2 | `RAH_STUDIO_ADMIN_PASSWORD` env var | No (automation set it intentionally) |
| 3 | Hardcoded default `admin / admin` | **Yes** — forced change on first login |

### Default credentials (dev / first-run)

If you start Studio with `--auth-enabled` and configure nothing else:

```
Username: admin
Password: admin
```

Studio prints a prominent warning at startup and forces a password change on the first login. After the password is changed the flag is cleared, persisted to the user file, and **never shown again**.

### Environment variable (Docker / Kubernetes)

```bash
# Docker
docker run \
  -e RAH_STUDIO_AUTH_ENABLED=true \
  -e RAH_STUDIO_ADMIN_PASSWORD=MyStr0ngPass \
  rah-studio

# Kubernetes — reference a Secret
env:
  - name: RAH_STUDIO_AUTH_ENABLED
    value: "true"
  - name: RAH_STUDIO_ADMIN_PASSWORD
    valueFrom:
      secretKeyRef:
        name: rah-studio-secrets
        key: admin-password
```

No forced change — the operator explicitly set the password.

### Config file (GitOps)

Store bcrypt hashes in your config repo. **Never store plaintext passwords.**

**Step 1 — generate a hash** (the `/hash` endpoint is always public, no credentials needed):

```bash
curl -s -X POST http://localhost:8092/api/studio/users/hash \
  -H 'Content-Type: application/json' \
  -d '{"password": "MyStr0ngPass"}' | jq -r .hash
# → $2a$10$...
```

**Step 2 — add to targets.json / ServerConfig**:

```json
{
  "auth_enabled": true,
  "auth_store_path": "/data/studio-users.db",
  "auth_users": [
    {
      "username": "alice",
      "password_hash": "$2a$10$...",
      "role": "admin"
    },
    {
      "username": "bob",
      "password_hash": "$2a$10$...",
      "role": "admin"
    }
  ],
  "targets": [...]
}
```

Config users always take priority over persisted file users. To reset a user's password, change the hash in the config and restart.

---

## Encrypted user file

User records (including usernames and bcrypt hashes) are stored as a single AES-256-GCM encrypted blob.

### Generate an encryption key

```bash
# 32-byte key as hex (64 hex characters)
openssl rand -hex 32
# example output: a3f1c2d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2
```

### Start Studio with encryption

```bash
export RAH_STUDIO_ENCRYPTION_KEY=a3f1c2d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2
export RAH_STUDIO_AUTH_ENABLED=true

./rah-studio \
  --auth-enabled \
  --auth-store-path /data/studio-users.db
```

| Scenario | Behavior |
|---|---|
| Key set + file path set | Encrypted file; survives restarts |
| Key not set + file path set | Plaintext file + startup warning |
| File path not set | Memory only; users re-bootstrap on restart |
| Key wrong on load | Fatal error: "decrypt user file (wrong key?)" |

### Key rotation

1. Export all users via `GET /api/studio/users`
2. Stop Studio
3. Delete the old user file
4. Set the new `RAH_STUDIO_ENCRYPTION_KEY`
5. Start Studio — it bootstraps from config users (or re-create users via API)

---

## Gateway service account (proxy credentials)

Studio proxies all gateway management API calls using a dedicated service account, separate from Studio users:

```bash
export RAH_GATEWAY_AUTH_USERNAME=studio-svc
export RAH_GATEWAY_AUTH_PASSWORD=GatewaySecret
```

If the gateway has auth disabled (default dev mode) these can be left unset.

---

## Via the UI

_The Users section is visible only to `admin`-role users._

### Accessing User Management

1. Sign in to Studio (`http://localhost:8092`)
2. In the left sidebar, navigate to **Settings → Users**
   _(not yet implemented as a tab — use the API for now; the UI section is on the roadmap)_

### First login with default credentials

1. Open Studio in a browser
2. Enter `admin` / `admin`
3. The **Change Password** screen appears — you cannot access the rest of the UI until the password is changed
4. Enter the current password (`admin`) and your new password (minimum 8 characters)
5. Click **Set new password** — you land directly in the main app

### Changing your own password

Currently only available via the API. A "Change password" option in the sidebar settings is on the roadmap.

---

## Via the API

All endpoints below require a valid session cookie (`rah_session`) unless marked **public**.

### Authentication

#### Sign in

```bash
curl -c cookies.txt -X POST http://localhost:8092/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username": "alice", "password": "MyStr0ngPass"}'
```

**Response:**
```json
{
  "username": "alice",
  "role": "admin",
  "auth_enabled": true,
  "must_change_password": false
}
```

If `must_change_password` is `true`, call `change-password` before any other operation.

#### Check current session

```bash
curl -b cookies.txt http://localhost:8092/api/auth/me
```

**Response:**
```json
{
  "username": "alice",
  "role": "admin",
  "auth_enabled": true,
  "must_change_password": false
}
```

Returns `401` if no valid session exists.

#### Change password (required on first login with default credentials)

```bash
curl -b cookies.txt -X POST http://localhost:8092/api/auth/change-password \
  -H 'Content-Type: application/json' \
  -d '{
    "current_password": "admin",
    "new_password": "MyNewStr0ngPass"
  }'
```

**Response:**
```json
{"status": "password changed"}
```

- `must_change_password` is cleared in the persisted user file and set to `false`
- Subsequent `/api/auth/me` calls return `"must_change_password": false`
- This endpoint is called exactly once per forced-change cycle — never shown again

#### Sign out

```bash
curl -b cookies.txt -c cookies.txt -X POST http://localhost:8092/api/auth/logout
```

Returns `204 No Content`. The `rah_session` cookie is cleared.

---

### User management

#### Generate a bcrypt hash — PUBLIC, no credentials needed

Use this to generate hashes for config files or before any users exist.

```bash
curl -X POST http://localhost:8092/api/studio/users/hash \
  -H 'Content-Type: application/json' \
  -d '{"password": "MyStr0ngPass"}'
```

**Response:**
```json
{"hash": "$2a$10$..."}
```

#### List all users

```bash
curl -b cookies.txt http://localhost:8092/api/studio/users
```

**Response:**
```json
{
  "users": [
    {
      "username": "alice",
      "role": "admin",
      "must_change_password": false,
      "created_at": 1748160000,
      "updated_at": 1748160000
    },
    {
      "username": "bob",
      "role": "admin",
      "created_at": 1748160100,
      "updated_at": 1748160100
    }
  ]
}
```

`password_hash` is always redacted from list responses.

#### Create or update a user

The `password` field is plaintext — Studio hashes it with bcrypt before storing.

```bash
curl -b cookies.txt -X POST http://localhost:8092/api/studio/users \
  -H 'Content-Type: application/json' \
  -d '{
    "username": "charlie",
    "password": "TempPass123",
    "role": "admin"
  }'
```

**Response:**
```json
{"username": "charlie", "role": "admin"}
```

- If the user already exists, the password and role are updated
- `role` defaults to `"admin"` if omitted (only `"admin"` is defined so far; more roles coming with RBAC)

#### Delete a user

```bash
curl -b cookies.txt -X DELETE \
  http://localhost:8092/api/studio/users/charlie
```

**Response:**
```json
{"deleted": "charlie"}
```

**Guards:**
- `400` — Cannot delete your own account
- `400` — Cannot delete the last admin user (Studio would be locked out)

---

## Automation / CI-CD cookbook

### Bootstrap in CI with a pre-hashed password

```bash
# In your CI pipeline — hash is stored as a secret, not the plaintext password
HASH=$(curl -s -X POST http://localhost:8092/api/studio/users/hash \
  -H 'Content-Type: application/json' \
  -d "{\"password\": \"$STUDIO_ADMIN_PASSWORD\"}" | jq -r .hash)

# Write to targets.json and restart Studio
```

### Create a user immediately after startup

```bash
# 1. Sign in with the env-var credential
curl -c /tmp/studio-cookies -X POST http://localhost:8092/api/auth/login \
  -H 'Content-Type: application/json' \
  -d "{\"username\": \"admin\", \"password\": \"$RAH_STUDIO_ADMIN_PASSWORD\"}"

# 2. Create the team user
curl -b /tmp/studio-cookies -X POST http://localhost:8092/api/studio/users \
  -H 'Content-Type: application/json' \
  -d '{
    "username": "deploy-bot",
    "password": "'"$BOT_PASSWORD"'",
    "role": "admin"
  }'

# 3. Sign out
curl -b /tmp/studio-cookies -c /tmp/studio-cookies \
  -X POST http://localhost:8092/api/auth/logout
```

### Health check — verify Studio is up and auth is enabled

```bash
curl -s http://localhost:8092/api/auth/me | jq .
# Without a session: {"error":"authentication required"}  → Studio is up, auth enabled
# Without auth:      {"username":"admin","auth_enabled":false}  → Studio is up, open mode
```

---

## Environment variable reference

| Variable | Default | Description |
|---|---|---|
| `RAH_STUDIO_AUTH_ENABLED` | `false` | Set to `true` to require login |
| `RAH_STUDIO_ADMIN_USERNAME` | `admin` | Username for the env-var bootstrap user |
| `RAH_STUDIO_ADMIN_PASSWORD` | _(none)_ | Password for the env-var bootstrap user |
| `RAH_STUDIO_ENCRYPTION_KEY` | _(none)_ | 32-byte AES-256 key as hex (64 chars) or base64 (44 chars) |
| `RAH_GATEWAY_AUTH_USERNAME` | _(none)_ | Service account username for management API proxy calls |
| `RAH_GATEWAY_AUTH_PASSWORD` | _(none)_ | Service account password for management API proxy calls |
| `RAH_STUDIO_PORT` | `8092` | Studio HTTP port |
| `RAH_GATEWAY_MANAGEMENT_URL` | `http://127.0.0.1:8081` | Gateway management API base URL |

## CLI flag reference

| Flag | Env var equivalent | Description |
|---|---|---|
| `--auth-enabled` | `RAH_STUDIO_AUTH_ENABLED=true` | Require login |
| `--auth-store-path` | _(targets.json only)_ | Path to encrypted user file |
| `--gateway-management-url` | `RAH_GATEWAY_MANAGEMENT_URL` | Gateway management base URL |
| `--targets-file` | `RAH_STUDIO_TARGETS_FILE` | JSON file with targets + full config |
| `--port` | `RAH_STUDIO_PORT` | Studio HTTP listen port |

---

## Security notes

| Concern | How it is handled |
|---|---|
| Password storage | bcrypt (`DefaultCost` = 10 rounds); plaintext never written anywhere |
| Username storage at rest | AES-256-GCM encrypted together with the password hash |
| Session tokens | 32 random bytes, base64url; httpOnly cookie; 8 h TTL |
| Timing attacks on login | Constant-time dummy bcrypt comparison for unknown usernames |
| Last admin guard | Delete is rejected if it would leave zero admin users |
| Self-delete guard | You cannot delete the account you are currently signed in as |
| Plaintext password over the wire | Use TLS in front of Studio in production (nginx/Caddy/ALB) |
