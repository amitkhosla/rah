package steps

import (
	"strings"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// CheckUpstreamRateLimit checks whether the upstream URL in the given slot
// is within rate limits defined in the UpstreamRegistry.
//
// Flow:
//  1. Read the upstream URL from ByteSlots[URLSlotIndex] (set by a prior step).
//  2. Call ActiveUpstreamRegistry().MatchWithPolicy(url) to get (configID, allow).
//  3. If !allow â†’ return DeniedPC (URL is blocked by fail_closed policy).
//  4. If configID == 0 â†’ no rate limit config â†’ return NextPC (fail_open or unmatched).
//  5. Extract hostname from URL and call CheckUpstreamLimitWithDetail(host).
//  6. If !allowed â†’ return DeniedPC.
//  7. Return NextPC.
type CheckUpstreamRateLimit struct {
	URLSlotIndex int // which ByteSlot holds the upstream URL
	DeniedPC     int
	NextPC       int
}

// Execute implements the engine.Step interface.
func (s *CheckUpstreamRateLimit) Execute(ctx *rctx.Context, state *engine.ExecutionState) int16 {
	// 1. Get URL from slot.
	var urlBytes []byte
	if s.URLSlotIndex >= 0 && s.URLSlotIndex < len(ctx.ByteSlots) {
		urlBytes = ctx.ByteSlots[s.URLSlotIndex]
	}
	if len(urlBytes) == 0 {
		return int16(s.NextPC) // no URL set â€” nothing to check
	}
	urlStr := string(urlBytes)

	// 2. Registry match â€” applies unmatched policy (fail_open / fail_closed / default).
	_, allow := engine.ActiveUpstreamRegistry().MatchWithPolicy(urlStr)
	if !allow {
		ctx.ResponseStatus = 429
		return int16(s.DeniedPC)
	}

	// 3. Per-resource rate limit (hostname-keyed).
	host := upstreamHost(urlBytes)
	_, allowed := engine.CheckUpstreamLimitWithDetail(host)
	if !allowed {
		ctx.ResponseStatus = 429
		return int16(s.DeniedPC)
	}

	return int16(s.NextPC)
}

// upstreamHost extracts the host portion from a URL like "https://api.openai.com/v1/chat".
// Returns the full URL string if no "/" after scheme â€” this is fine as a cache key.
func upstreamHost(url []byte) string {
	s := string(url)
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	return s
}
