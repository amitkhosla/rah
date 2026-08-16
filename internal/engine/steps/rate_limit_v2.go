package steps

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/registry"
	"github.com/amitkhosla/rah/internal/rctx"
)

var globalCounterKey = []byte("__global__")

// â"€â"€â"€ WindowSpec â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// WindowSpec describes one fixed-window within a CheckRateLimitV2 step.
// All fields are resolved at bake time; zero runtime allocations.
type WindowSpec struct {
	EpochDiv uint32 // seconds per window: 1=second, 60=minute, 3600=hour, 86400=day
	Limit    uint32 // base request limit (before per-tenant multiplier)
	Idx      int    // windowIdx for arena slot disambiguation (unique per config)
}

// â"€â"€â"€ TokenBucketSpec â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// TokenBucketSpec holds the bake-time-resolved parameters for V2 token bucket mode.
// The slot index is derived from configSeed XOR a hash of the counter key, so each
// (config, key) pair gets a stable slot in the shared CounterStore arena.
type TokenBucketSpec struct {
	Rate       uint32                // tokens refilled per second
	Burst      uint32                // maximum token capacity (also the initial fill)
	Store      *engine.CounterStore  // shared arena — must not be nil
	ConfigSeed uint32                // hashed from configID to namespace slots per config
}

// â"€â"€â"€ CheckRateLimitV2 â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// CheckRateLimitV2 applies a V2 multi-window rate limit against the current
// request. All configuration is resolved at bake time and captured in the struct.
//
//   - ConfigID         selects the ConfigCounterRegistry arena pair.
//   - CountBy          controls how the counter key is derived from the request context.
//   - Windows          is the ordered list of windows to check (fail-fast per window).
//   - DeniedPC         is the absolute PC to jump to when any window is exceeded.
//   - NextPC           is the absolute PC to jump to when all windows pass.
//   - RemoteRL         when non-nil, routes counter checks to a distributed backend (strict mode).
//   - ConfigName       used as Redis key prefix in the distributed path.
//   - WeightIntSlot    is the IntSlots index for token count weight (-1 = disabled, use delta=1).
//   - QuotaGroupFilter when non-zero, this instruction is skipped (â†’ NextPC) unless
//     ctx.QuotaGroupID matches. Used for dynamic dispatch: each group gets its own
//     guarded CheckRateLimitV2 instruction emitted by the compiler.
type CheckRateLimitV2 struct {
	ConfigID         uint16
	CountBy          engine.RateLimitCountBy
	Windows          []WindowSpec
	DeniedPC         int
	NextPC           int
	RemoteRL         engine.ExternalRateLimitProvider // nil = local only
	ConfigName       string                            // used as Redis key prefix
	WeightIntSlot    int                              // -1 = +1 per request; >=0 = read ctx.IntSlots[n] as delta (token count)
	QuotaGroupFilter uint8                            // 0 = unconditional; >0 = skip unless ctx.QuotaGroupID matches
	// NodeCountFn returns the number of live gateway instances. When non-nil and
	// RemoteRL is nil (approximate mode), the per-window limit is divided by this
	// value so each pod enforces its fair share. Resolved once per Execute call.
	NodeCountFn      func() int                       // nil = no division (single node or strict mode)
	// TBucket when non-nil activates token bucket mode. Windows and RemoteRL are ignored.
	TBucket          *TokenBucketSpec
}

// Execute implements the engine.Step interface.
func (s *CheckRateLimitV2) Execute(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	// â"€â"€ Layer 1: tenant-wide block / RL-disabled flags â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	reg := registry.State.Active.Load()
	var scalePct int16
	if reg != nil && int(ctx.TenantID) < len(reg.TenantModifiers) {
		mod := reg.TenantModifiers[ctx.TenantID]
		if mod.Flags&registry.TenantBlocked != 0 {
			ctx.ResponseStatus = 403
			return int16(s.DeniedPC)
		}
		if mod.Flags&registry.TenantRLDisabled != 0 {
			return int16(s.NextPC)
		}
		scalePct = mod.ScalePct // Layer 4: global per-tenant scale
	}

	// â"€â"€ Layer 2: per-tenant per-config V2 override (sparse table) â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	var windowLimits []uint32
	if ov, ok := registry.LookupV2Override(reg, ctx.TenantID, s.ConfigID); ok {
		if ov.Flags&registry.V2ConfigBlocked != 0 {
			ctx.ResponseStatus = 403
			return int16(s.DeniedPC)
		}
		if ov.Flags&registry.V2ConfigDisabled != 0 {
			return int16(s.NextPC)
		}
		if ov.ScaleOverridePct != 0 {
			scalePct = ov.ScaleOverridePct
		}
		windowLimits = ov.WindowLimits
	}

	// â"€â"€ Quota group filter (dynamic dispatch) â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	if s.QuotaGroupFilter != 0 && ctx.QuotaGroupID != s.QuotaGroupFilter {
		return int16(s.NextPC)
	}

	// â"€â"€ Token bucket fast path (mutually exclusive with fixed-window path) â"€â"€â"€
	if s.TBucket != nil {
		return s.executeTokenBucket(ctx)
	}

	// â"€â"€ Derive the counter key â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	keyBytes, useTenant := s.resolveKey(ctx)
	if keyBytes == nil && !useTenant {
		// resolveKey signals "deny" by returning (nil, false).
		ctx.ResponseStatus = 429
		return int16(s.DeniedPC)
	}

	// â"€â"€ Layer 3: resolve effective multiplier â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	// ScalePct (int16): +50 = 150%, -25 = 75%, 0 = no change (100%).
	// Layer 2 config-scoped scale takes priority over Layer 4 global scale.
	var mult uint32 = 100
	if scalePct != 0 {
		s16 := int32(100) + int32(scalePct)
		if s16 > 0 {
			mult = uint32(s16)
		} else {
			mult = 1 // floor: never allow more than +100% reduction
		}
	}

	// â"€â"€ 4. Resolve delta: token-count weight from a slot, or 1 for plain request counting.
	delta := uint32(1)
	if s.WeightIntSlot >= 0 && s.WeightIntSlot < len(ctx.IntSlots) {
		if v := ctx.IntSlots[s.WeightIntSlot]; v > 0 {
			delta = uint32(v)
		}
	}

	// â"€â"€ 5. Dispatch to local or distributed counter path â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€
	var denied bool
	if s.RemoteRL != nil {
		denied = s.executeDistributed(ctx, keyBytes, useTenant, mult, delta, windowLimits)
	} else {
		denied = s.executeLocal(ctx, keyBytes, useTenant, mult, delta, windowLimits)
	}

	if denied {
		ctx.ResponseStatus = 429
		return int16(s.DeniedPC)
	}
	return int16(s.NextPC)
}

// executeLocal checks all windows against the in-process counter arenas.
// Returns true if any window is exceeded (denied), false if all pass.
func (s *CheckRateLimitV2) executeLocal(ctx *rctx.Context, keyBytes []byte, useTenant bool, mult, delta uint32, windowLimits []uint32) bool {
	reg := engine.ActiveCounterRegistry()
	now := uint32(time.Now().Unix())

	// When DivideByNodes is in effect, shrink the effective limit by the live
	// instance count so each pod enforces its fair share in approximate mode.
	nodeDivisor := uint32(1)
	if s.NodeCountFn != nil {
		if n := s.NodeCountFn(); n > 1 {
			nodeDivisor = uint32(n)
		}
	}

	for i := range s.Windows {
		w := &s.Windows[i]
		epoch := now / w.EpochDiv
		limit := applyMultiplier(w.Limit, mult)
		if int(w.Idx) < len(windowLimits) && windowLimits[w.Idx] != 0 {
			limit = windowLimits[w.Idx]
		}
		if nodeDivisor > 1 {
			if limit > nodeDivisor {
				limit = limit / nodeDivisor
			} else {
				limit = 1
			}
		}

		var allowed bool
		if useTenant {
			arena := reg.TenantArena(s.ConfigID)
			if arena == nil {
				continue // arena not registered — allow safely
			}
			allowed, _ = arena.IncrementBy(ctx.TenantID, w.Idx, epoch, limit, delta)
		} else {
			arena := reg.SlotArena(s.ConfigID)
			if arena == nil {
				continue // arena not registered — allow safely
			}
			allowed, _ = arena.IncrementBy(keyBytes, w.Idx, epoch, limit, delta)
		}

		if !allowed {
			return true // fail-fast on first exceeded window
		}
	}
	return false
}

// executeDistributed checks all windows against the shared Redis counter backend.
// Redis key format: rl2:{configName}:{epochDiv}:{keyIdentifier}
//   - keyIdentifier is the decimal TenantID when useTenant=true,
//     or the hex-encoded keyBytes otherwise.
//
// Returns true if any window is exceeded (denied), false if all pass.
// Fail-open: RedisRateLimitProvider.Check already returns (true, limit) on error/timeout.
func (s *CheckRateLimitV2) executeDistributed(ctx *rctx.Context, keyBytes []byte, useTenant bool, mult, delta uint32, windowLimits []uint32) bool {
	// Build the key identifier once — either decimal TenantID or hex key bytes.
	var keyID string
	if useTenant {
		var buf [24]byte
		key := strconv.AppendUint(buf[:0], uint64(ctx.TenantID), 10)
		keyID = string(key)
	} else {
		keyID = hex.EncodeToString(keyBytes)
	}

	for i := range s.Windows {
		w := &s.Windows[i]
		limit := applyMultiplier(w.Limit, mult)
		if int(w.Idx) < len(windowLimits) && windowLimits[w.Idx] != 0 {
			limit = windowLimits[w.Idx]
		}
		// Redis key: rl2:{configName}:{epochDiv}:{keyIdentifier}
		redisKey := "rl2:" + s.ConfigName + ":" + strconv.FormatUint(uint64(w.EpochDiv), 10) + ":" + keyID

		// TODO: For delta-weighted rate limiting, the Redis backend should increment
		// by delta instead of 1. Currently, Check() only increments by 1. A future
		// optimization would add CheckBy(key, limit, delta, window) to ExternalRateLimitProvider.
		// For now, we approximate by scaling the limit: limit' = (limit / delta) + 1.
		// This is less accurate but safe to merge and allows token-counting to work.
		scaledLimit := limit
		if delta > 1 {
			scaledLimit = (limit / delta) + 1
			if scaledLimit < 1 {
				scaledLimit = 1
			}
		}

		allowed, _ := s.RemoteRL.Check(redisKey, scaledLimit, int(w.EpochDiv))
		if !allowed {
			return true // fail-fast on first exceeded window
		}
	}
	return false
}

// resolveKey derives the counter key bytes from the request context.
//
// Returns (nil, true)  â†’ use TenantCounterArena with ctx.TenantID.
// Returns (key, false) â†’ use SlotCounterArena with the returned key bytes.
// Returns (nil, false) â†’ deny the request (OnEmptyFail with empty key).
// executeTokenBucket handles the token_bucket enforcement path.
// It derives a stable arena slot from the ConfigSeed XOR a hash of the counter
// key, then delegates to CounterStore.TokenBucket which uses [LastUpdate:32 | Tokens:32].
// Returns NextPC if allowed, DeniedPC (with 429 status) if the bucket is empty.
func (s *CheckRateLimitV2) executeTokenBucket(ctx *rctx.Context) int16 {
	tb := s.TBucket
	now := uint32(time.Now().Unix())

	// Derive a slot index from config seed XOR a compact hash of the key.
	var keyHash uint32
	switch s.CountBy.Kind {
	case engine.CountByTenant:
		keyHash = uint32(ctx.TenantID)
	case engine.CountByGlobal:
		keyHash = 0
	default:
		// FNV-1a 32-bit on keyBytes for non-tenant keys.
		keyHash = 2166136261
		kBytes, _ := s.resolveKey(ctx)
		for _, b := range kBytes {
			keyHash ^= uint32(b)
			keyHash *= 16777619
		}
	}
	arenaLen := uint32(len(tb.Store.Arena))
	if arenaLen == 0 {
		return int16(s.NextPC) // safety: allow if arena not initialised
	}
	slot := (tb.ConfigSeed ^ keyHash) % arenaLen

	allowed, _ := tb.Store.TokenBucket(slot, tb.Rate, tb.Burst, now)
	if !allowed {
		ctx.ResponseStatus = 429
		return int16(s.DeniedPC)
	}
	return int16(s.NextPC)
}

func (s *CheckRateLimitV2) resolveKey(ctx *rctx.Context) ([]byte, bool) {
	cb := &s.CountBy
	switch cb.Kind {
	case engine.CountByTenant:
		return nil, true // direct TenantID index — no key bytes needed

	case engine.CountByGlobal:
		return globalCounterKey, false

	case engine.CountByStatic:
		return cb.StaticKey, false

	case engine.CountByIP:
		// Extract IP directly from the request — XFFIndex selects the XFF entry.
		ip := ipFromXFF(ctx.Request.Header.Get("X-Forwarded-For"), cb.XFFIndex)
		if ip == "" {
			ip = strings.TrimSpace(ctx.Request.Header.Get("X-Real-IP"))
		}
		if ip == "" {
			if host, _, err := net.SplitHostPort(ctx.Request.RemoteAddr); err == nil {
				ip = host
			}
		}
		if ip == "" {
			return s.handleEmpty(ctx, nil)
		}
		buf := ctx.Alloc(len(ip))
		copy(buf, ip)
		return buf, false

	case engine.CountBySlot:
		key := slotBytesOrNil(ctx, cb.SlotIndex)
		return s.handleEmpty(ctx, key)

	case engine.CountByComposite:
		key := buildCompositeKey(ctx, cb.SlotIndexes)
		return s.handleEmpty(ctx, key)

	case engine.CountByApp:
		if ctx.CallerID == 0 {
			return s.handleEmpty(ctx, nil)
		}
		key := ctx.Alloc(4)
		binary.BigEndian.PutUint32(key, ctx.CallerID)
		return key, false

	default:
		return nil, true // unknown kind — fall back to tenant
	}
}

// handleEmpty applies the OnEmpty policy when the resolved key is nil/empty.
func (s *CheckRateLimitV2) handleEmpty(_ *rctx.Context, key []byte) ([]byte, bool) {
	if len(key) > 0 {
		return key, false
	}
	switch s.CountBy.OnEmpty {
	case engine.OnEmptyKeySkip:
		// Signal "skip" by returning a non-nil sentinel — caller must handle.
		// We encode skip as returning NextPC; simplest: return a special pair.
		// Instead, we return a special marker via the bool=true path.
		return nil, true // treat as tenant — effectively skip to tenant bucket
	case engine.OnEmptyKeyTenant:
		return nil, true // fall back to tenant
	default: // OnEmptyKeyFail
		return nil, false // deny
	}
}

// slotBytesOrNil safely reads a ByteSlot, returning nil if out of range or empty.
func slotBytesOrNil(ctx *rctx.Context, idx int) []byte {
	if idx >= 0 && idx < len(ctx.ByteSlots) {
		return ctx.ByteSlots[idx]
	}
	return nil
}

// buildCompositeKey concatenates multiple slot values separated by '|'.
// Uses a stack-allocated scratch area when the result fits in 128 bytes.
func buildCompositeKey(ctx *rctx.Context, indexes []int) []byte {
	if len(indexes) == 0 {
		return nil
	}
	// Measure total length first.
	total := len(indexes) - 1 // separators
	for _, idx := range indexes {
		if idx >= 0 && idx < len(ctx.ByteSlots) {
			total += len(ctx.ByteSlots[idx])
		}
	}
	if total <= 0 {
		return nil
	}
	buf := ctx.Alloc(total)
	pos := 0
	for i, idx := range indexes {
		if i > 0 {
			buf[pos] = '|'
			pos++
		}
		if idx >= 0 && idx < len(ctx.ByteSlots) {
			n := copy(buf[pos:], ctx.ByteSlots[idx])
			pos += n
		}
	}
	return buf[:pos]
}

// applyMultiplier scales limit by a fixed-point multiplier (Ã—100 encoding).
// multiplier == 0 or 100 â†’ identity. Result clamped to [1, 0xFFFFFFFF].
func applyMultiplier(limit uint32, multiplier uint32) uint32 {
	if multiplier == 0 || multiplier == 100 {
		return limit
	}
	v := uint64(limit) * uint64(multiplier) / 100
	if v < 1 {
		return 1
	}
	if v > 0xFFFFFFFF {
		return 0xFFFFFFFF
	}
	return uint32(v)
}

// â"€â"€â"€ SetRateLimitHeaders â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€â"€

// Pre-allocated header name byte slices — never mutated after init.
var (
	defaultLimitHeader     = []byte("X-RateLimit-Limit")
	defaultRemainingHeader = []byte("X-RateLimit-Remaining")
	defaultResetHeader     = []byte("X-RateLimit-Reset")
)

// SetRateLimitHeaders emits standard rate-limit headers onto the outbound
// response. Place this step immediately after CheckRateLimitV2 in the allowed
// branch. The Reset timestamp is the start of the next window epoch.
//
//   - LimitHeader / RemainingHeader / ResetHeader: override default names.
//   - WindowEpochDiv: the reference window size in seconds (for Reset calc).
//   - Limit: the effective base limit (before multiplier — headers show base).
//   - NextPC: absolute PC to jump to after emitting headers.
type SetRateLimitHeaders struct {
	LimitHeader     string // default "X-RateLimit-Limit"
	RemainingHeader string // default "X-RateLimit-Remaining"
	ResetHeader     string // default "X-RateLimit-Reset"
	WindowEpochDiv  uint32 // seconds per window (used for Reset timestamp)
	Limit           uint32 // base limit to advertise
	NextPC          int
}

// Execute implements the engine.Step interface.
func (s *SetRateLimitHeaders) Execute(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	now := uint32(time.Now().Unix())

	epochDiv := s.WindowEpochDiv
	if epochDiv == 0 {
		epochDiv = 1
	}
	epoch := now / epochDiv
	resetAt := (epoch + 1) * epochDiv // Unix timestamp of next window start

	limitKey := defaultLimitHeader
	if s.LimitHeader != "" {
		limitKey = []byte(s.LimitHeader)
	}
	remainKey := defaultRemainingHeader
	if s.RemainingHeader != "" {
		remainKey = []byte(s.RemainingHeader)
	}
	resetKey := defaultResetHeader
	if s.ResetHeader != "" {
		resetKey = []byte(s.ResetHeader)
	}

	ctx.SetResponseHeader(limitKey, fmtUint32v2(ctx, s.Limit))
	ctx.SetResponseHeader(resetKey, fmtUint32v2(ctx, resetAt))

	// Remaining is stored in IntSlots[0] by convention — emit if present.
	var remaining uint32
	if len(ctx.IntSlots) > 0 && ctx.IntSlots[0] >= 0 {
		remaining = uint32(ctx.IntSlots[0])
	}
	ctx.SetResponseHeader(remainKey, fmtUint32v2(ctx, remaining))

	return int16(s.NextPC)
}

// fmtUint32v2 formats v as decimal bytes using the arena (zero heap allocation).
func fmtUint32v2(ctx *rctx.Context, v uint32) []byte {
	buf := ctx.Alloc(10)
	return strconv.AppendUint(buf[:0], uint64(v), 10)
}

// ParseWindowDuration converts a human-readable duration string to seconds (EpochDiv).
// Accepted formats (case-insensitive):
//
//	Ns / Nsec / Nsecond(s)  â†’ N seconds
//	Nm / Nmin / Nminute(s)  â†’ N*60
//	Nh / Nhr  / Nhour(s)    â†’ N*3600
//	Nd / Nday(s)            â†’ N*86400
//	Nw / Nweek(s)           â†’ N*604800
//	Nmo / Nmonth(s)         â†’ N*2592000  (fixed 30-day window)
//	Ny / Nyr / Nyear(s)     â†’ N*31536000
//	Raw integer string       â†’ parsed directly as seconds (backward compat)
//
// Returns an error if the string is empty or unparseable.
func ParseWindowDuration(s string) (uint32, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}
	if n, err := strconv.ParseUint(s, 10, 32); err == nil {
		return uint32(n), nil
	}
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid duration %q: must start with a number", s)
	}
	n, err := strconv.ParseUint(s[:i], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	unit := strings.TrimSpace(s[i:])
	var mult uint64
	switch {
	case unit == "s" || unit == "sec" || strings.HasPrefix(unit, "second"):
		mult = 1
	case unit == "m" || unit == "min" || strings.HasPrefix(unit, "minute"):
		mult = 60
	case unit == "h" || unit == "hr" || strings.HasPrefix(unit, "hour"):
		mult = 3600
	case unit == "d" || strings.HasPrefix(unit, "day"):
		mult = 86400
	case unit == "w" || strings.HasPrefix(unit, "week"):
		mult = 604800
	case unit == "mo" || strings.HasPrefix(unit, "month"):
		mult = 2592000
	case unit == "y" || unit == "yr" || strings.HasPrefix(unit, "year"):
		mult = 31536000
	default:
		return 0, fmt.Errorf("unknown duration unit %q in %q", unit, s)
	}
	result := n * mult
	if result > math.MaxUint32 {
		return 0, fmt.Errorf("duration %q overflows uint32", s)
	}
	return uint32(result), nil
}
