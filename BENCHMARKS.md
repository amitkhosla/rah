# RAH Benchmark Results

Comparison of RAH against Kong 3.9.2 and Tyk 5.14.0 under realistic API gateway workloads.

---

## Methodology

### Hardware

| Role | Machine | Specs |
|---|---|---|
| **Gateway VM** | GCP n2-standard-2 | 2 vCPU, 8 GB RAM |
| **Load VM** | GCP n2-standard-8 | 8 vCPU, 32 GB RAM |

Gateways (RAH, Kong, Tyk) ran as Docker containers on the gateway VM.  
Bombardier, mock upstream server, mock AuthN server, RAH's Redis + PostgreSQL, and Tyk's Redis all ran on the load VM.

Mock upstream CPU utilisation peaked below 50% across all runs — the upstream was never CPU-starved.

### Test tool

[Bombardier](https://github.com/codesenberg/bombardier) — HTTP benchmarking tool. Each concurrency level was held for **15 seconds**. Concurrency levels tested: 50, 100, 200, 500, 1000.

### Gateway versions

| Gateway | Version | Logging config |
|---|---|---|
| RAH | this repo (`main`) | Access log disabled |
| Kong | 3.9.2 | Access log disabled |
| Tyk | 5.14.0 | Access log disabled |

All three gateways ran with logging disabled for the primary comparison so the measured overhead is gateway logic only, not I/O.  
A separate section below shows RAH with access logging enabled.

### Scenarios tested

Each scenario is a real API flow — not a synthetic passthrough. RAH's flow definitions for each scenario are in [`bench/configs/rah/`](bench/configs/rah/).

| ID | Scenario | Auth | Rate Limit | Upstream |
|---|---|---|---|---|
| S0 | Passthrough | — | — | Real upstream |
| S1 | API Key auth | API Key | — | Mock response (in-process) |
| S2 | Rate limit by IP | — | IP-based | Mock response |
| S3 | Rate limit by header | — | Header-based | Mock response |
| S4 | Rate limit by IP + header | — | IP + Header | Mock response |
| S5 | JWT validation | JWT (audience + scope) | — | Mock response |
| S5.1 | JWT + IP restriction | JWT + IP restrict (from claim) | — | Mock response |
| S6 | Correlation ID | — | — | Mock response |
| S7 | Full auth chain | JWT + API Key + Rate limit by IP | Yes | Mock response |
| S7.1 | Full auth chain, JWT cached | JWT cached + API Key + Rate limit | Yes | Mock response |
| S9 | Cache read | — | — | Mock response (cache hit) |
| S11 | Full chain + real upstream | JWT cached + API Key + Rate limit | Yes | Real upstream, no delay |
| S11.1 | Full chain + 100ms upstream | JWT cached + Rate limit by azp | Yes | 1 KB, 100ms delay |
| S11.2a | Full chain + 50ms upstream | JWT cached + API Key + Rate limit | Yes | 500 B, 50ms delay |
| S11.2b | Full chain + 100ms upstream | JWT cached + API Key + Rate limit | Yes | 1 KB, 100ms delay |
| S11.2c | Full chain + 500ms upstream | JWT cached + API Key + Rate limit | Yes | 5 KB, 500ms delay |
| S11.2d | Full chain + 1s upstream | JWT cached + API Key + Rate limit | Yes | 5 KB, 1s delay |

> **JWT caching differences across gateways**: Each gateway behaves differently on JWT caching, which affects which rows are directly comparable:
> - **RAH S5/S7**: Full RS256 validation on every request — no caching
> - **RAH S7.1/S11.\***: JWT result cached after first validation — subsequent requests skip RS256
> - **Kong**: Full RS256 on every request (JWT caching could not be configured — those cells are excluded from the cached-scenario tables)
> - **Tyk**: Automatically caches JWT tokens by default with no option to disable — all Tyk JWT results reflect cached-token performance
>
> **Fair comparisons**: RAH S5/S7 vs Kong S5/S7 (both full RS256). RAH S7.1 vs Tyk S7 (both cached). Mixing these groups would not be a valid comparison.

---

## Results

### No-upstream scenarios (pure gateway overhead)

These scenarios return a mock response directly from the gateway — no upstream call. They measure raw gateway throughput.

#### Peak RPS across concurrency levels

| Scenario | RAH | Tyk 5.14.0 | Kong 3.9.2 | RAH vs Kong |
|---|---|---|---|---|
| S1: API Key auth | **43,766** | 25,309 | 10,579 | **+4.1×** |
| S2: Rate limit by IP | **45,401** | 25,945 | 13,094 | **+3.5×** |
| S3: Rate limit by header | **46,003** | 25,536 | 13,642 | **+3.4×** |
| S4: Rate limit by IP + header | **45,249** | 25,712 | 14,177 | **+3.2×** |
| S5: JWT RS256 (full verify) | **14,513** | — ¹ | 4,560 | **+3.2×** |
| S6: Correlation ID | **45,491** | 25,445 | 20,822 | **+2.2×** |
| S7: Full chain (JWT full verify) | **14,309** | — ¹ | 3,806 | **+3.8×** |
| S7.1: Full chain (JWT cached) | **40,060** | 25,733 ¹ | — | — |
| S9: Cache read | **39,000+** | 21,000+ | 22,000+ | ~+1.8× |

¹ Tyk in S5/S7 uses a pre-cached JWT. RAH's equivalent is S7.1, not S5/S7. The RPS figures are not directly comparable.

> **Coverage note**: Some scenario/gateway combinations could not be fully configured in this benchmark run and are omitted (`—`) from the table rather than shown as zero.

#### Latency at c=100 (ms, mean / max)

| Scenario | RAH mean | RAH max | Kong mean | Kong max |
|---|---|---|---|---|
| S1: API Key auth | 2.3ms | 40.6ms | 12.3ms | 84.7ms |
| S2: Rate limit by IP | 2.2ms | 34.1ms | 8.3ms | 40.1ms |
| S5: JWT RS256 | 6.9ms | 66.9ms | 21.8ms | 77.1ms |
| S7: Full chain | 7.1ms | 59.4ms | 29.9ms | 73.0ms |
| S7.1: Full chain JWT cached | 3.3ms | 45.7ms | — | — |

---

### Real upstream scenarios (production-like)

These scenarios include a real upstream call with a configurable payload size and delay. They show how RAH behaves when request handling competes with upstream latency.

#### Full auth chain (JWT cached + API Key + Rate limit) — peak RPS

| Upstream | RAH | Tyk | Kong |
|---|---|---|---|
| No delay | **16,938** | 3,261 ² | 2,984 |
| 50ms, 500B payload | **12,789** | — ² | 3,830 ³ |
| 100ms, 1KB payload | **9,577** | — ² | 2,703 |
| 500ms, 5KB payload | **1,944** | — ² | 1,857 |
| 1s, 5KB payload | **190** | — ² | 189 |

² These upstream scenarios were not fully configured for Tyk in this benchmark run — results excluded rather than shown as misleading numbers.  
³ These concurrency levels were not fully configured for Kong in this scenario — excluded for the same reason.

**What the upstream results show:**

- When the upstream has no delay, RAH handles **5–6× more requests** than Kong and Tyk. The gateway is doing the same work (JWT, API key lookup, rate limit check) — the difference is how efficiently each gateway schedules and dispatches those requests.
- When upstream delay is 100ms, RAH scales correctly with concurrency (Little's Law: at c=1000 / 100ms = ~10K RPS theoretical ceiling). RAH reaches 9,577 — Kong reaches 2,703, suggesting it saturates its worker pool well before 1000 concurrent connections.
- When upstream delay dominates (500ms, 1s), RAH and Kong converge to similar RPS. At this latency scale, the gateway's own overhead is less than 1% of the request time. This is the expected and honest result.

---

### JWT caching impact (RAH only)

| Scenario | RPS (c=100) | RPS (c=1000) |
|---|---|---|
| S5: JWT RS256 full verify every request | 14,478 | 13,665 |
| S7.1: Full chain with JWT cached | 40,800+ | 37,000+ |

Caching the JWT result (skipping RS256 on repeat tokens within the TTL window) gives **~2.8× throughput improvement** on JWT-heavy flows.

---

### RAH: with logging vs without logging

All scenarios above had access logging disabled on all gateways. This section shows RAH's own logging overhead.

| Scenario | RAH (no logs) | RAH (with logs) | Overhead |
|---|---|---|---|
| S1: API Key auth, c=100 | 43,049 | 34,175 | −21% |
| S2: Rate limit, c=100 | 44,876 | 35,378 | −21% |
| S7: Full chain, c=100 | 14,098 | 12,868 | −9% |
| S0: Passthrough, c=100 | 18,529 | 16,742 | −10% |
| S11.1: 100ms upstream, c=1000 | 9,577 | 9,404 | −2% |
| S11.2b: 100ms upstream full chain, c=1000 | 9,381 | 9,419 | ~0% |

**Key takeaway**: On no-upstream scenarios (pure mock response), access logging costs ~10–21% throughput. On upstream-bound scenarios (100ms delay), logging overhead drops to under 2% — the network round-trip dominates.

---

## Reproduce these results

### Requirements

- Two GCP VMs (or equivalent): one n2-standard-2 (gateway), one n2-standard-8 (load)
- Docker on the gateway VM
- Bombardier on the load VM
- RAH's Redis and PostgreSQL on the load VM (see `docker-compose.full.yml`)

### Steps

```bash
# On gateway VM — start RAH
docker compose -f docker-compose.bench.yml up -d

# On load VM — run bombardier
cd bench/
GATEWAY_HOST=<gateway-vm-ip> AUTH_URL=http://<auth-vm-ip>:9401 API_KEY=<your-key> \
  bash benchmark-all.sh --duration 15s
```

RAH flow definitions and API configurations used in each scenario:

```
bench/configs/rah/
├── apis/           # API definitions for each scenario
├── flows/          # Flow definitions (auth, rate limit, cache, upstream steps)
└── gateway.yaml    # Gateway config used during benchmarking
```

---

## Notes

1. **Test duration**: 15 seconds per concurrency level, no separate warmup phase. Results at low concurrency (c=50) should be interpreted as steady-state, not burst.

2. **Same-machine load generation**: Bombardier and mock upstream both ran on the n2-standard-8 load VM. Mock upstream CPU stayed below 50% across all runs, confirming it was not a bottleneck.

3. **Network path**: All gateway → upstream calls crossed the GCP internal network between the two VMs. This is a realistic deployment topology — not localhost benchmarking.

4. **Incomplete scenario coverage**: Some Kong and Tyk scenarios could not be fully configured without disturbing other active API definitions on those gateways. Results for those combinations are excluded (`—`) rather than shown as zero, which would misrepresent the gateway's capability.

5. **Kong worker count**: Kong was configured with its default worker count on a 2-core VM. Increasing workers may improve Kong's concurrency ceiling.

6. **RAH configuration**: RAH was configured with the Redis + PostgreSQL datastore stack. The flow definitions used are published in `bench/configs/rah/` so the exact pipeline can be inspected and reproduced.
