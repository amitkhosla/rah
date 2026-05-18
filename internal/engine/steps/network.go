package steps

// network.go — steps that extract network-level metadata from the request.

import (
	"net"
	"strings"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// BindClientIP extracts the real client IP address and stores it in destSlot.
//
// xffIndex controls which comma-separated entry in X-Forwarded-For to use:
//   0  = first/leftmost (default, the original client; use when gateway is behind one proxy)
//  -1  = last/rightmost (the most recently added hop; use for the immediate upstream proxy)
//   N  = Nth entry (0-based); if out of range, falls through to X-Real-IP / RemoteAddr
//
// Resolution order (first non-empty result wins):
//  1. X-Forwarded-For header — entry selected by xffIndex.
//     Trusted only when the gateway is behind a known reverse proxy.
//  2. X-Real-IP header — set by nginx and similar proxies.
//  3. RemoteAddr — the raw TCP peer address (always present; may be the proxy).
//
// The stored value is an IP string with no port (e.g. "203.0.113.42").
// If extraction fails for all three sources, the slot is left unchanged.
func BindClientIP(destSlot int, xffIndex int) engine.Instruction {
	return engine.Instruction{
		Name: "BIND_CLIENT_IP",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if destSlot < 0 || destSlot >= len(ctx.ByteSlots) {
				return s.PC + 1
			}

			if ip := ipFromXFF(ctx.Request.Header.Get("X-Forwarded-For"), xffIndex); ip != "" {
				ctx.ByteSlots[destSlot] = ctx.Alloc(len(ip))
				copy(ctx.ByteSlots[destSlot], ip)
				return s.PC + 1
			}

			if ip := strings.TrimSpace(ctx.Request.Header.Get("X-Real-IP")); ip != "" {
				ctx.ByteSlots[destSlot] = ctx.Alloc(len(ip))
				copy(ctx.ByteSlots[destSlot], ip)
				return s.PC + 1
			}

			if host, _, err := net.SplitHostPort(ctx.Request.RemoteAddr); err == nil && host != "" {
				ctx.ByteSlots[destSlot] = ctx.Alloc(len(host))
				copy(ctx.ByteSlots[destSlot], host)
			}

			return s.PC + 1
		},
	}
}

// ipFromXFF returns the IP at xffIndex from a comma-separated X-Forwarded-For value.
// xffIndex=0 → first (leftmost), xffIndex=-1 → last (rightmost), xffIndex=N → Nth entry.
// Returns "" if the header is empty or the index is out of range.
func ipFromXFF(header string, xffIndex int) string {
	if header == "" {
		return ""
	}
	parts := strings.Split(header, ",")
	idx := xffIndex
	if idx < 0 {
		idx = len(parts) + idx // -1 → last
	}
	if idx < 0 || idx >= len(parts) {
		return ""
	}
	return strings.TrimSpace(parts[idx])
}

// firstIPFromHeader returns the first (leftmost) IP from a comma-separated X-Forwarded-For value.
// Kept for use by ip_restriction and other callers that always want the first hop.
func firstIPFromHeader(header string) string {
	return ipFromXFF(header, 0)
}
