# Upstream Balancing (HTTP Step)

The HTTP step supports both single upstream and multiple upstream targets.

## Input formats

A URL source (static `url` or runtime `url_var`) can contain:

1. Single URL (existing behavior)
2. Comma-separated URLs
3. JSON array of URLs
4. JSON array of objects:

```json
[
  { "url": "https://svc-1.internal", "weight": 3, "healthy": true },
  { "url": "https://svc-2.internal", "weight": 1, "healthy": true }
]
```

## Strategy

`flowInput["http.upstream_strategy"]`:

- `round_robin` (default)
- `weighted_round_robin`

When a single URL is provided, balancing is bypassed.

## Registry guidance

Store endpoint paths in registry keys (`url:<service>`):

- Single upstream value: plain URL string.
- Multiple upstream value: comma-separated list or JSON array.

Runtime selection automatically chooses balancing only for multi-instance values.

## Observability

Per upstream set + strategy, the runtime tracks:

- `Selections`: successful picks
- `Errors`: selection failures (e.g., no healthy upstream)

This data is available through internal helper `readBalancerStats()` and can be wired into your metrics pipeline.
