# Clock Package

## Purpose
Provides a **cache-line friendly clock abstraction** with periodic updates. Avoids expensive time.Now() calls in hot paths by maintaining a global clock updated via heartbeat goroutine.

## Files
- **clock.go**: GlobalClock structure and heartbeat maintenance

## Key Types

### GlobalClock
A single cache-line friendly struct (~64 bytes = 1 cache line):
- **ElapsedSec**: uint32 - seconds since gateway start
- **MinuteID**: uint32 - minutes since start (ElapsedSec / 60)
- **HourID**: uint32 - hours since start (ElapsedSec / 3600)
- **DayID**: uint32 - days since start (ElapsedSec / 86400)
- **UnixCurTime**: int64 - current Unix timestamp

## Global Instance
```go
var CurrentClock GlobalClock
```

## Responsibilities
1. **Periodic Updates**: Maintain current time without syscall overhead
2. **Cache-Line Layout**: Single cache line fits all fields (no false sharing)
3. **Multiple Time Bases**: Provide ElapsedSec, MinuteID, HourID, DayID
4. **Efficient Access**: Avoid time.Now() in hot paths

## Dependencies
- No external dependencies
- Used by: cache (TTL tiers), control (request timestamps), observability (timestamps)

## Operations

### Start Clock Heartbeat
```go
StartClockHeartbeat()
// Spawns goroutine that updates CurrentClock every 100ms
```

### Read Current Time
```go
elapsed := CurrentClock.ElapsedSec     // Seconds since start
minute := CurrentClock.MinuteID        // Minute bucket
hour := CurrentClock.HourID            // Hour bucket
day := CurrentClock.DayID              // Day bucket
unix := CurrentClock.UnixCurTime       // Unix timestamp
```

### Set Time Manually
```go
clock.SetTime(unixNano, elapsed)
// Updates all fields based on elapsed seconds
```

## Anti-Pattern: Background Goroutine for Clock Updates

Using a background goroutine to update a global clock is an **anti-pattern** with serious performance implications:

1. **⚠️ CPU Spikes (Primary Issue)**: The background goroutine wakes up every 100ms to update the clock, causing unnecessary CPU activity and scheduler thrashing. This constant background activity creates periodic CPU usage patterns.
2. **Unnecessary complexity**: Goroutines add overhead and require synchronization (atomic stores).
3. **Potential for drift**: Updates are not guaranteed to be perfectly accurate due to scheduler jitter.
4. **Resource contention**: Background goroutines consume CPU cycles, memory, and scheduler resources.
5. **Testing difficulties**: Mocking global state with background updates is difficult and fragile.
6. **Accuracy considerations**: The GlobalClock is only updated every 100ms, so readings are up to 100ms stale.

### Alternatives

1. **Direct `time.Now()` Calls**: For non-hot paths, direct calls to time.Now().UnixNano() or time.Now().Unix() are acceptable. The cost (100-200ns) is often negligible for non-critical logic.
2. **Request-Scoped Timestamp**: Cache the timestamp at the start of the request and reuse it throughout the request lifecycle. This guarantees consistency within the request scope without a global state. See `rctx.Context.StartedAtUnixNano`.

### Recommendation

**Replace `clock.CurrentClock` with a request-scoped timestamp** and use `time.Now()` directly in non-critical paths. This eliminates the background goroutine and provides better accuracy with minimal overhead.

### Refactoring Steps
1. Remove `StartClockHeartbeat()` call from `main.go`.
2. Remove `GlobalClock` struct from `clock.go`.
3. Remove `clock.go` file.
4. Replace `clock.CurrentClock.ElapsedSec` with time-based calls where necessary, or cache the time values in request scoped values.


### Typical Overhead
- **time.Now()**: ~100-200 ns per call (syscall)
- **Clock read**: ~0-10 ns (memory read, already in cache)
- **Savings**: 10-20x improvement

### Use Cases
- TTL calculations: Compare ElapsedSec with entry timestamp
- Rate limiting: Use MinuteID for per-minute buckets
- Logging: Use UnixCurTime without syscalls
- Cache eviction: Check ElapsedSec against entry age

## Heartbeat Behavior
```
StartClockHeartbeat():
  Start Unix time captured
    ↓
  Spawn goroutine with 100ms ticker
    ↓
  Every 100ms:
    now = time.Now().Unix()
    elapsed = now - start
    Update CurrentClock fields

Accuracy:
  ±100ms drift possible (depends on scheduler)
  Acceptable for most use cases
  Not suitable for sub-10ms timing
```

## Cache-Line Efficiency
```
GlobalClock layout (32 bytes):
  ElapsedSec    uint32  (4 bytes)
  MinuteID      uint32  (4 bytes)
  HourID        uint32  (4 bytes)
  DayID         uint32  (4 bytes)
  UnixCurTime   int64   (8 bytes)
  Total:                (24 bytes on 64-bit)

L1 Cache line: 64 bytes
  All 5 fields fit in single cache line
  No false sharing with adjacent data
  Single cache miss per thread
```

## Typical Integration

### TTL Checks (Cache)
```go
entryAge := CurrentClock.ElapsedSec - entry.CreatedAt
if entryAge > ttl {
  // Entry expired
}
```

### Rate Limiting
```go
currentMinute := CurrentClock.MinuteID
if rateLimitBucket[tenantID][currentMinute] > limit {
  // Rate limited
}
```

### Logging Timestamps
```go
log.Printf("[%d] Request started", CurrentClock.UnixCurTime)
```

## Accuracy Considerations
- **±100ms drift**: Heartbeat updates every 100ms
- **Monotonic**: Never goes backwards
- **Acceptable for**: TTL, rate limiting, logging
- **Not acceptable for**: Sub-millisecond measurements (use time.Now())

## Future Optimization
Could use **atomic updates** instead of direct assignment to avoid partial reads, but current implementation assumes 64-bit atomicity by CPU.

## Comparison: Direct time.Now() vs Clock Abstraction
| Operation | Direct time.Now() | GlobalClock |
|-----------|------------------|------------|
| Latency | 100-200ns | <10ns |
| Accuracy | Exact | ±100ms |
| Calls per req | Multiple | Zero |
| Syscall overhead | High | None |
| Use case | Tracing, audit | TTL, rate limit |

## Design Pattern
```
Request handling:
  For time-sensitive logic: Use CurrentClock.ElapsedSec (fast)
  For tracing/logging: Cache UnixCurTime at request start
  For exact timing: Call time.Now() only when needed
```
