# Cheapest Cloud Instances for RAH Gateway

RAH Gateway is a static Go binary that idles at <20 MB RSS and <5% CPU on a single core.
The cheapest ARM instances are a near-perfect fit.

---

## Instance Comparison (as of 2026-04)

| Cloud | Instance | vCPU | RAM | Price/mo | Arch | Notes |
|-------|----------|------|-----|----------|------|-------|
| **AWS** | t4g.micro | 2 | 1 GB | ~$3.07 | arm64 (Graviton2) | Free tier eligible; burstable |
| **AWS** | t4g.small | 2 | 2 GB | ~$6.14 | arm64 (Graviton2) | Recommended for prod w/ Redis |
| **Azure** | B2ats v2 | 2 | 1 GB | ~$3.79 | arm64 (Ampere Altra) | Burstable; cheapest Azure ARM |
| **Azure** | B2als v2 | 2 | 4 GB | ~$7.57 | arm64 (Ampere Altra) | More RAM for LLM workloads |
| **GCP** | e2-micro | 2 | 1 GB | ~$6.11 | x86_64 (always free tier) | Free tier: 1 e2-micro/month |
| **GCP** | t2a-standard-1 | 1 | 4 GB | ~$10.76 | arm64 (Ampere Altra) | GCP's cheapest ARM |

**Recommendation**: AWS t4g.micro (dev) / t4g.small (prod).

---

## AWS — EKS on t4g

```bash
# Create a node group with Graviton (arm64)
eksctl create nodegroup \
  --cluster my-cluster \
  --name rah-arm \
  --node-type t4g.small \
  --nodes 1 \
  --nodes-min 1 \
  --nodes-max 3 \
  --node-ami-family AmazonLinux2

# Deploy (image already multi-arch, pulls arm64 automatically)
helm upgrade --install rah deploy/helm/rah-gateway \
  --namespace rah --create-namespace \
  --values deploy/cloud/aws-values.yaml
```

**`deploy/cloud/aws-values.yaml`:**
```yaml
image:
  repository: ghcr.io/YOUR_ORG/rah-gateway
  tag: "v0.1.0"

nodeSelector:
  kubernetes.io/arch: arm64
  node.kubernetes.io/instance-type: t4g.small

resources:
  requests:
    cpu: 50m
    memory: 64Mi
  limits:
    cpu: 800m
    memory: 256Mi

secretEnv:
  RAH_REDIS_ADDR: "elasticache-endpoint:6379"
  RAH_PG_DSN: "postgres://gw:PASS@rds-endpoint:5432/gateway?sslmode=require"
```

---

## GCP — GKE Autopilot / Standard on e2-micro (free tier)

```bash
# Standard cluster (Autopilot doesn't support free-tier e2-micro)
gcloud container clusters create rah-cluster \
  --zone us-central1-a \
  --machine-type e2-micro \
  --num-nodes 1

gcloud container clusters get-credentials rah-cluster --zone us-central1-a

helm upgrade --install rah deploy/helm/rah-gateway \
  --namespace rah --create-namespace \
  --values deploy/cloud/gcp-values.yaml
```

**`deploy/cloud/gcp-values.yaml`:**
```yaml
image:
  repository: ghcr.io/YOUR_ORG/rah-gateway
  tag: "v0.1.0"

resources:
  requests:
    cpu: 50m
    memory: 64Mi
  limits:
    cpu: 500m
    memory: 256Mi

secretEnv:
  RAH_REDIS_ADDR: "memorystore-ip:6379"
  RAH_PG_DSN: "postgres://gw:PASS@cloud-sql-ip:5432/gateway"
```

---

## Azure — AKS on B2ats v2 (ARM)

```bash
# Create ARM node pool
az aks create \
  --resource-group myRG \
  --name rah-cluster \
  --node-count 1 \
  --node-vm-size Standard_B2ats_v2 \
  --generate-ssh-keys

az aks get-credentials --resource-group myRG --name rah-cluster

helm upgrade --install rah deploy/helm/rah-gateway \
  --namespace rah --create-namespace \
  --values deploy/cloud/azure-values.yaml
```

**`deploy/cloud/azure-values.yaml`:**
```yaml
image:
  repository: ghcr.io/YOUR_ORG/rah-gateway
  tag: "v0.1.0"

nodeSelector:
  kubernetes.io/arch: arm64

resources:
  requests:
    cpu: 50m
    memory: 64Mi
  limits:
    cpu: 800m
    memory: 256Mi

secretEnv:
  RAH_REDIS_ADDR: "azure-redis-endpoint:6380"
  RAH_PG_DSN: "postgres://gw:PASS@azure-pg-endpoint:5432/gateway?sslmode=require"
```

---

## Docker-only (no Kubernetes)

For the absolute cheapest single-machine deployment (e.g. a $4/mo VPS):

```bash
# Pull multi-arch image — automatically gets arm64 or amd64
docker pull ghcr.io/YOUR_ORG/rah-gateway:latest

# Run with your own gateway.yaml
docker run -d \
  --name rah-gateway \
  --restart unless-stopped \
  -p 8080:8080 \
  -p 8081:8081 \
  -v $(pwd)/gateway.yaml:/app/gateway.yaml:ro \
  -e RAH_REDIS_ADDR=host.docker.internal:6379 \
  ghcr.io/YOUR_ORG/rah-gateway:latest
```

Or use the full local stack:

```bash
git clone https://github.com/YOUR_ORG/rah
cd rah
docker compose up -d
```

---

## Cost Summary

| Scenario | Monthly cost |
|----------|-------------|
| Dev (t4g.micro, no DB) | ~$3 |
| Dev (t4g.micro + ElastiCache + RDS t4g.micro) | ~$15–25 |
| Prod HA (2× t4g.small + managed Redis + RDS) | ~$40–60 |
| Single VPS (Hetzner CAX11 ARM, 2 vCPU, 4 GB) | ~$5 |

**Cheapest viable prod setup**: Hetzner CAX11 (€3.79/mo ARM VPS) + Docker Compose.
