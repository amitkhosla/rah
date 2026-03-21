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
// Resolution order (first non-empty result wins):
//  1. X-Forwarded-For header — first IP in the comma-separated list.
//     Trusted only when the gateway is behind a known reverse proxy.
//  2. X-Real-IP header — set by nginx and similar proxies.
//  3. RemoteAddr — the raw TCP peer address (always present; may be the proxy).
//
// The stored value is an IP string with no port (e.g. "203.0.113.42").
// If extraction fails for all three sources, the slot is left unchanged.
func BindClientIP(destSlot int) engine.Instruction {
	return engine.Instruction{
		Name: "BIND_CLIENT_IP",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			if destSlot < 0 || destSlot >= len(ctx.ByteSlots) {
				return s.PC + 1
			}

			if ip := firstIPFromHeader(ctx.Request.Header.Get("X-Forwarded-For")); ip != "" {
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

// firstIPFromHeader returns the first IP from a comma-separated X-Forwarded-For value.
// "203.0.113.1, 10.0.0.1, 192.168.1.1" → "203.0.113.1"
func firstIPFromHeader(header string) string {
	if header == "" {
		return ""
	}
	idx := strings.IndexByte(header, ',')
	if idx < 0 {
		return strings.TrimSpace(header)
	}
	return strings.TrimSpace(header[:idx])
}
