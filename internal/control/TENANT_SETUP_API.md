# Tenant Management Interface

The `TenantController` acts as a pass-through to the high-performance Matrix Registry.

## Data Persistence & Discovery
The Registry Manager handles deduplication (interning) and matrix expansion.
- **Row Resolution:** Automated via Radix lookup.
- **Column Expansion:** Automated via "Stride Shifts".

### Sample Request
```json
{
  "tenant_id": 200,
  "upsert": {
    "aliases": ["api.globex.com", "globex-internal"],
    "endpoints": { "prod": "[https://lb.globex.com](https://lb.globex.com)" },
    "metadata": { "plan": "enterprise" }
  }
}