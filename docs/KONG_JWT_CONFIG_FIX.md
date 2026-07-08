# Kong S8 (and S5/S5.1/S7) JWT Configuration Fix

## Problem Summary

Kong S8 (JWT validation + 10-port upstream fan-out) was failing with **100% error rate** during deployment to rah-gtw. Root cause analysis revealed that Kong's JWT plugin configuration was **incomplete and missing critical RSA signature verification parameters**.

## Root Cause

The original Kong JWT plugin configuration for S8 (and similarly for S5, S5.1, S7) included:
```python
"config": {
    "claims_to_verify": ["exp"],
    "key_claim_name": "iss"
}
```

**Missing from the config**: The RSA public key needed for JWT signature verification.

Kong's JWT plugin can operate in two modes:
1. **Consumer-based JWT**: Credential registered on a consumer, Kong fetches key from there
2. **Service/Plugin-based JWT**: Public key configured directly in the plugin (preferred for service-level auth)

The original config attempted mode 1 (consumer-based) but without explicit ACL configuration to bind the JWT plugin to the `bench-jwt-consumer`, leaving Kong unable to validate tokens.

## Solution Implemented

Updated Kong JWT plugin configuration to include the **explicit RSA public key**:

```python
"config": {
    "algorithm": "RS256",
    "key_claim_name": "iss",
    "claims_to_verify": ["exp"],
    "public_key": jwks_pem  # <-- CRITICAL FIX
}
```

### Changes Made to `bench/setup-new-scenarios.sh`

#### S5: JWT + scope check (lines ~896-920)
- Added: Load RSA public key from `/tmp/kong_key001.pem`
- Added: `algorithm: "RS256"` and `public_key: s5_pem` to JWT plugin config

#### S5.1: JWT + azp rate limit (lines ~962-980)
- Added: Load RSA public key from `/tmp/kong_key001.pem`
- Added: `algorithm: "RS256"` and `public_key: s51_pem` to JWT plugin config

#### S7: Full stack JWT + rate limit (lines ~1044-1085)
- Added: Load RSA public key from `/tmp/kong_key001.pem`
- Added: `algorithm: "RS256"` and `public_key: s7_pem` to JWT plugin config

#### S8: JWT + 10-port upstream fan-out (lines ~1100-1138)
- Added: Load RSA public key from `/tmp/kong_key001.pem` (done once, reused per port)
- Added: `algorithm: "RS256"` and `public_key: jwks_pem` to JWT plugin config
- Changed condition from `if JWT_ISS:` to `if JWT_ISS and jwks_pem:` for safety

## How It Works

1. **JWT Token Generation**: Auth server (9401) issues RS256-signed JWT with `kid=key-001`
2. **Public Key Extraction**: Script extracts key from JWKS endpoint and converts to PEM format
3. **Kong Verification**: Kong plugin verifies JWT signature using the PEM public key before allowing request

## Deployment Steps

To deploy and validate Kong S8 with the fix:

### Step 1: Copy Kong Configuration to rah-gtw
```bash
gcloud compute scp --recurse rah-gateway:/home/Aarav/rah/deploy/docker/kong/ \
  rah-gtw:/home/Aarav/rah/deploy/docker/ --zone=us-east1-c
```

### Step 2: Start Kong on rah-gtw
```bash
gcloud compute ssh rah-gtw --zone=us-east1-c --command="\
  cd /home/Aarav/rah && \
  docker-compose -f deploy/docker/docker-compose.yml up -d kong postgres redis"
```
Wait ~30 seconds for containers to start.

### Step 3: Run Updated Setup Script
```bash
gcloud compute ssh rah-gtw --zone=us-east1-c --command="\
  cd /home/Aarav/rah && \
  UPSTREAM_IP=10.142.0.6 AUTH_SERVER=http://10.142.0.6:9401 \
  bash bench/setup-new-scenarios.sh"
```

### Step 4: Validate S8 Works
```bash
# Fetch JWT token from auth server
JWT=$(curl -s -X POST "http://10.142.0.6:9401/oauth/token?kid=key-001" \
  -H "Content-Type: application/json" \
  -d '{"grant_type":"client_credentials","client_id":"bench-client-001","client_secret":"bench-secret","scope":"read:orders write:orders"}' \
  | jq -r .access_token)

# Test each S8 port (p0-p9)
for p in {0..9}; do
  code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 \
    -H "Authorization: Bearer $JWT" \
    http://10.142.0.7:8000/bench/s8/p$p)
  echo "S8/p$p: HTTP $code (expected 200)"
done
```

## Technical Details

### RSA Key Format
- **Source**: JWKS endpoint at `http://{AUTH_SERVER}/.well-known/jwks.json`
- **Key ID**: `key-001` (matched by `kid` in JWT header)
- **Algorithm**: RS256 (RSA 2048-bit, SHA-256)
- **Format**: PEM-encoded public key (PKCS#1 SubjectPublicKeyInfo)

### JWT Token Structure
```
header.payload.signature
  |       |       |
  |       |       +-- Signature verified by Kong using public_key
  |       +---------- Contains: {"iss", "aud", "azp", "scope", "exp", ...}
  +---------- Contains: {"alg": "RS256", "kid": "key-001"}
```

### Kong JWT Plugin Operation
1. Extract `Authorization: Bearer <token>` header
2. Decode and verify JWT signature using configured `public_key`
3. Verify claims: `exp` (expiration), `iss` (issuer must match)
4. Allow request to proceed to upstream service

## Validation Checklist

- [ ] Kong admin API is accessible at `http://localhost:8001` on rah-gtw
- [ ] `/tmp/kong_key001.pem` exists and contains valid RSA public key
- [ ] S8 services (bench-s8-p0 through bench-s8-p9) exist in Kong
- [ ] Each S8 service has JWT plugin with `public_key` configured
- [ ] Auth server is reachable at `http://10.142.0.6:9401`
- [ ] JWT tokens can be fetched from auth server
- [ ] S8 endpoint returns 200 with valid JWT, 401 without JWT

## Rollback Instructions

If issues arise, the original incomplete configuration can be restored, but S8 will fail with:
- HTTP 401 (Unauthorized) - Kong cannot verify JWT signature
- Kong logs: `missing public key for RS256` or similar

## Related Files

- `bench/setup-new-scenarios.sh` - Updated setup script with JWT fixes
- `bench/setup-kong-bench.sh` - Separate script for initial Kong setup (S0-S8 basic routes)
- `docs/KONG_JWT_CONFIG_FIX.md` - This documentation file
- `/tmp/kong_key001.pem` - Runtime-generated RSA public key (created during setup)

## References

- Kong JWT Plugin: https://docs.konghq.com/hub/kong-inc/jwt/
- RFC 7519: JSON Web Token (JWT): https://tools.ietf.org/html/rfc7519
- RFC 7517: JSON Web Key (JWK): https://tools.ietf.org/html/rfc7517
