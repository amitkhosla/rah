# RAH vs Kong vs Tyk — Full Benchmark Results (All Concurrency Levels)
**Date**: 2026-06-30 | **VM**: n2-standard-2 (2 vCPU, 8GB) | **Load VM**: n2-standard-8

## Run Configurations

| Run | Timestamp | RAH Config | Notes |
|-----|-----------|------------|-------|
| Run 1 (1% traces) | 20260629_161147 | traces enabled, sample_rate=0.01 | Baseline full run |
| Run 2 (100% traces) | 20260630_135938 | traces enabled, sample_rate=1.0 | Observability overhead test |
| Run 3 (traces off) | 20260630_144949 | traces disabled, access log only | **Definitive benchmark** |

Kong and Tyk config unchanged across all runs.
- **Kong**: nginx access log → file, Prometheus plugin enabled
- **Tyk**: 100% analytics to Redis (60s TTL), log_level=info

---

## Scenario Descriptions

| Scenario | Description | Gateways |
|----------|-------------|----------|
| A | API key auth + per-tenant rate limit (100K/min) + upstream proxy | RAH, Kong, Tyk |
| B | JWT RS256 validation + upstream proxy | RAH, Kong, Tyk |
| B2 | JWT RS256 cached — skips verify on hit + upstream proxy | RAH only |
| Bauth | JWT RS256 validate → synthetic 200/401, no upstream | RAH, Kong, Tyk |
| Bauth2 | JWT result cached → return from memory, no RS256, no upstream | RAH only |
| C | API key auth + in-process cache hit (no upstream on hit) | RAH, Kong, Tyk |
| D | Correlation ID + API key auth + cache + rate limit + upstream proxy | RAH, Kong, Tyk |
| E | Pure cache hit, no auth, no upstream — ceiling test | RAH, Kong, Tyk |

Concurrency levels tested: **c=50, 100, 200, 500, 1000, 2000**
Each run: 20s warmup (50 reqs) + 20s per concurrency level.

---

## Run 3 — Traces Off, Access Log Only (Definitive)
*Timestamp: 20260630_144949*

### Scenario A: API Key Auth + Rate Limit + Proxy

| c | RAH rps | RAH avg | RAH p99 | Kong rps | Kong avg | Kong p99 | Tyk rps | Tyk avg | Tyk p99 |
|---|---------|---------|---------|----------|----------|----------|---------|---------|---------|
| 50 | 9,954 | 5.0ms | 15.4ms | 4,475 | 11.2ms | 27.5ms | 1,494 | 33.5ms | 151.6ms |
| 100 | 9,688 | 10.3ms | 30.1ms | 5,646 | 17.7ms | 44.1ms | 548 | 181.5ms | 350.9ms |
| 200 | 9,599 | 20.9ms | 47.5ms | 6,562 | 30.5ms | 63.9ms | 143 | — | — |
| 500 | 8,704 | 57.5ms | 110.0ms | 5,332 | 93.7ms | 215.7ms | 1,135 | 435.1ms | — |
| 1000 | 8,582 | 116.6ms | 190.8ms | 5,657 | 176.0ms | 409.6ms | 452 | — | — |
| 2000 | 8,365 | 238.0ms | 370.1ms | 6,184 | 322.9ms | 692.3ms | 909 | — | — |
| **Peak** | **9,954** | | | **6,562** | | | **1,494** | | |

### Scenario B: JWT RS256 Validation + Proxy

| c | RAH rps | RAH avg | RAH p99 | Kong rps | Kong avg | Kong p99 | Tyk rps | Tyk avg | Tyk p99 |
|---|---------|---------|---------|----------|----------|----------|---------|---------|---------|
| 50 | 5,718 | 8.7ms | 33.4ms | 3,452 | 14.5ms | 32.2ms | 1,349 | 37.1ms | 171.9ms |
| 100 | 6,287 | 15.9ms | 46.2ms | 3,346 | 29.9ms | 60.2ms | 6,906* | 14.5ms | 165.3ms |
| 200 | 6,054 | 33.1ms | 71.4ms | 3,246 | 61.6ms | 118.0ms | 582 | 340.1ms | — |
| 500 | 5,765 | 86.7ms | 154.8ms | 3,313 | 150.6ms | 266.7ms | 439 | — | — |
| 1000 | 5,716 | 174.7ms | 316.0ms | 3,209 | 310.1ms | 751.9ms | 896 | — | — |
| 2000 | 5,606 | 356.3ms | 569.5ms | 3,039 | 664.6ms | — | 905 | — | — |
| **Peak** | **6,287** | | | **3,452** | | | **—** | | |

*Tyk B at c=100 is an anomalous spike — collapses at higher concurrency. Not a reliable number.

### Scenario B2: JWT Cached + Proxy (RAH only)

| c | RAH rps | RAH avg | RAH p99 |
|---|---------|---------|---------|
| 50 | 5,841 | 8.6ms | 30.8ms |
| 100 | 9,111 | 11.0ms | 31.4ms |
| 200 | 8,745 | 22.9ms | 51.9ms |
| 500 | 8,286 | 60.4ms | 106.9ms |
| 1000 | 8,199 | 122.0ms | 209.0ms |
| 2000 | 7,979 | 250.2ms | 400.9ms |
| **Peak** | **9,111** | | |

B2 vs B uplift: **1.45x** — JWT cache skips RS256 on every hit.

### Scenario Bauth: JWT Validate → Synthetic Response, No Upstream

| c | RAH rps | RAH avg | RAH p99 | Kong rps | Kong avg | Kong p99 | Tyk rps | Tyk avg | Tyk p99 |
|---|---------|---------|---------|----------|----------|----------|---------|---------|---------|
| 50 | 9,957 | 5.0ms | 20.3ms | 3,763 | 13.3ms | 31.8ms | 1,689 | 30.1ms | — |
| 100 | 10,017 | 10.0ms | 34.1ms | 3,825 | 26.2ms | 52.7ms | 1,676 | 60.8ms | — |
| 200 | 9,894 | 20.2ms | 53.6ms | 3,744 | 53.3ms | 149.8ms | 1,076 | 188.5ms | — |
| 500 | 9,634 | 52.0ms | 112.9ms | 3,706 | 134.5ms | 230.1ms | 1,408 | 358.4ms | — |
| 1000 | 9,672 | 103.2ms | 209.3ms | 3,660 | 271.8ms | 515.7ms | 1,163 | — | — |
| 2000 | 9,500 | 210.1ms | 413.8ms | 3,672 | 538.4ms | — | 1,229 | — | — |
| **Peak** | **10,017** | | | **3,825** | | | **1,747** | | |

### Scenario Bauth2: JWT Cached → In-Memory Return, No RS256, No Upstream (RAH only)

| c | RAH rps | RAH avg | RAH p99 |
|---|---------|---------|---------|
| 50 | 20,887 | 2.4ms | 11.8ms |
| 100 | 20,737 | 4.8ms | 19.7ms |
| 200 | 21,262 | 9.4ms | 31.8ms |
| 500 | 20,744 | 24.1ms | 60.6ms |
| 1000 | 19,776 | 50.6ms | 117.1ms |
| 2000 | 19,494 | 103.0ms | 207.5ms |
| **Peak** | **21,262** | | |

### Scenario C: API Key Auth + Cache Hit (No Upstream)

| c | RAH rps | RAH avg | RAH p99 | Kong rps | Kong avg | Kong p99 | Tyk rps | Tyk avg | Tyk p99 |
|---|---------|---------|---------|----------|----------|----------|---------|---------|---------|
| 50 | 19,221 | 2.6ms | 12.4ms | 3,959 | 12.6ms | 36.1ms | 5,969 | 8.4ms | 25.3ms |
| 100 | 19,342 | 5.2ms | 21.5ms | 4,301 | 23.3ms | 64.3ms | 5,945 | 16.8ms | 45.7ms |
| 200 | 19,282 | 10.4ms | 33.5ms | 5,333 | 37.5ms | 89.2ms | 5,800 | 34.5ms | 82.9ms |
| 500 | 19,006 | 26.4ms | 67.1ms | 5,896 | 84.7ms | 183.7ms | 5,581 | 89.5ms | 181.0ms |
| 1000 | 18,336 | 54.5ms | 123.0ms | 4,701 | 211.9ms | 488.1ms | 5,404 | 184.3ms | 327.7ms |
| 2000 | 18,192 | 110.6ms | 267.6ms | 5,354 | 370.7ms | — | 5,013 | 395.5ms | — |
| **Peak** | **19,342** | | | **5,896** | | | **5,969** | | |

### Scenario D: Correlation ID + Auth + Cache + Rate Limit + Proxy (Full Chain)

| c | RAH rps | RAH avg | RAH p99 | Kong rps | Kong avg | Kong p99 | Tyk rps | Tyk avg | Tyk p99 |
|---|---------|---------|---------|----------|----------|----------|---------|---------|---------|
| 50 | 19,043 | 2.6ms | 12.0ms | 3,601 | 13.9ms | 45.5ms | 5,731 | 8.7ms | 28.5ms |
| 100 | 19,054 | 5.3ms | 20.4ms | 4,094 | 24.5ms | 70.1ms | 3,838 | 26.0ms | 60.3ms |
| 200 | 18,627 | 10.8ms | 35.9ms | 5,487 | 36.4ms | 88.8ms | 3,868 | 51.7ms | 105.1ms |
| 500 | 18,418 | 27.2ms | 67.2ms | 5,563 | 89.8ms | 267.5ms | 5,202 | 96.0ms | 201.9ms |
| 1000 | 17,652 | 56.7ms | 130.7ms | 4,368 | 228.0ms | 485.4ms | 3,600 | 277.0ms | 464.9ms |
| 2000 | 17,099 | 117.8ms | 267.1ms | 5,569 | 356.3ms | 661.2ms | 3,728 | 530.2ms | — |
| **Peak** | **19,054** | | | **5,563** | | | **5,731** | | |

### Scenario E: Pure Cache, No Auth — Ceiling Test

| c | RAH rps | RAH avg | RAH p99 | Kong rps | Kong avg | Kong p99 | Tyk rps | Tyk avg | Tyk p99 |
|---|---------|---------|---------|----------|----------|----------|---------|---------|---------|
| 50 | 21,409 | 2.3ms | 11.7ms | 6,725 | 7.4ms | 23.6ms | 9,265 | 5.4ms | 19.8ms |
| 100 | 21,359 | 4.7ms | 19.9ms | 6,656 | 15.1ms | 45.2ms | 9,031 | 11.1ms | 32.7ms |
| 200 | 21,571 | 9.3ms | 31.7ms | 6,738 | 30.0ms | 70.5ms | 8,926 | 22.6ms | 53.6ms |
| 500 | 21,411 | 23.4ms | 64.3ms | 6,610 | 76.1ms | 157.3ms | 8,233 | 60.7ms | 135.1ms |
| 1000 | 20,278 | 49.3ms | 116.7ms | 6,556 | 151.9ms | 312.9ms | 8,129 | 123.9ms | 244.0ms |
| 2000 | 20,527 | 98.0ms | 211.9ms | 6,367 | 311.6ms | 576.5ms | 7,774 | 256.0ms | 434.4ms |
| **Peak** | **21,571** | | | **6,738** | | | **9,265** | | |

---

## Peak RPS Summary — Run 3 (Traces Off)

| Scenario | RAH | Kong | Tyk | RAH vs Kong | RAH vs Tyk |
|----------|-----|------|-----|-------------|------------|
| A: apikey+RL+proxy | **9,954** | 6,562 | 1,494 | +52% | +6.7x |
| B: JWT RS256+proxy | **6,287** | 3,452 | — | +82% | — |
| B2: JWT cached+proxy (RAH) | **9,111** | — | — | — | — |
| Bauth: JWT synth no upstream | **10,017** | 3,825 | 1,747 | +2.6x | +5.7x |
| Bauth2: JWT cached no upstream (RAH) | **21,262** | — | — | — | — |
| C: apikey+cache hit | **19,342** | 5,896 | 5,969 | +3.3x | +3.2x |
| D: full chain | **19,054** | 5,563 | 5,731 | +3.4x | +3.3x |
| E: pure cache ceiling | **21,571** | 6,738 | **9,265** | +3.2x | +2.3x |

---

## Observability Overhead Comparison

Same gateway, different trace configs. Scenario E (pure cache, peak RPS):

| Config | RAH E rps | vs Traces Off |
|--------|-----------|---------------|
| Traces off, access log only | 21,571 | baseline |
| 1% traces to Postgres | ~21,248 | -1.5% (negligible) |
| 100% traces to Postgres | 3,246 | **-85% (-6.6x)** |

**Root cause of 100% trace overhead** (from pprof analysis):
- `StartRequest` allocates new `RequestTrace` struct per request: **3.99 GB/30s allocated**
- `AppendInstructionEvent` allocates per-instruction events: **2.40 GB/30s allocated**
- Postgres write path (pgx encode + JSON marshal): **12.5 GB/30s allocated**
- Total observability allocations: **~47% of all heap allocations**
- Fix: pool-allocate `RequestTrace` structs (sync.Pool) + pre-allocate instruction event slices

---

## Run 2 — 100% Traces to Postgres (Observability Cost Reference)
*Timestamp: 20260630_135938*

### Peak RPS (all scenarios capped at ~3,000 rps by Postgres write throughput)

| Scenario | RAH rps | Kong rps | Tyk rps |
|----------|---------|----------|---------|
| A | 2,787 | 6,292 | 1,465 |
| B | 2,741 | 3,435 | 1,416 |
| Bauth | 3,048 | 3,863 | 1,747 |
| Bauth2 | 3,195 | — | — |
| C | 2,963 | 6,061 | 6,141 |
| D | 2,993 | 5,653 | 5,715 |
| E | 3,246 | 6,743 | 9,082 |

RAH was bottlenecked by trace writes at all concurrency levels. The ~3K ceiling is Postgres write throughput, not gateway logic.

---

## Notes

1. **Tyk instability**: Tyk hits 2ms timeout walls under JWT/apikey auth load at higher concurrency (c≥200). Redis saturation from 100% analytics recording under concurrent JWT verification is the likely cause. Cache-only scenarios (C, D, E) are stable.

2. **Kong scaling**: Kong peaks between c=200-500 and degrades at c=1000. Nginx worker limit (2 workers configured) is the ceiling.

3. **RAH scaling**: RAH is stable across all concurrency levels with graceful degradation. No timeout walls or error spikes observed in any run.

4. **Tyk B anomaly**: At c=100, Tyk B showed 6,906 rps (anomalous). This was a single measurement spike — collapsed to <600 rps at c=200. Not a reliable number.
