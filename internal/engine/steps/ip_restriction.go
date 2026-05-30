package steps

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// IPRestrictionConfig is bake-time configuration for ip_restriction.
type IPRestrictionConfig struct {
	Mode              string       // allow | deny
	CIDRs             string       // comma-separated CIDR list
	Source            string       // remote_addr | header.X-Forwarded-For | header.X-Real-IP
	OnViolationStatus int
	OnViolationBody   string
	TrustedProxyCIDRs []*net.IPNet // only trust XFF if peer IP is in one of these; nil/empty = no trust
}

// ParseIPRestrictionConfig parses and validates flow input for ip_restriction.
func ParseIPRestrictionConfig(input map[string]string) (IPRestrictionConfig, error) {
	cfg := IPRestrictionConfig{
		Mode:              strings.ToLower(strings.TrimSpace(input["mode"])),
		CIDRs:             strings.TrimSpace(input["cidrs"]),
		Source:            strings.ToLower(strings.TrimSpace(input["source"])),
		OnViolationStatus: 403,
		OnViolationBody:   "ip not allowed",
	}
	if cfg.Mode == "" {
		cfg.Mode = "allow"
	}
	if cfg.Mode != "allow" && cfg.Mode != "deny" {
		return cfg, errors.New("ip_restriction.mode must be allow or deny")
	}
	if cfg.CIDRs == "" {
		return cfg, errors.New("ip_restriction.cidrs is required")
	}
	if v := strings.TrimSpace(input["on_violation_status"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 400 && n <= 599 {
			cfg.OnViolationStatus = n
		}
	}
	if v := strings.TrimSpace(input["on_violation_body"]); v != "" {
		cfg.OnViolationBody = v
	}
	if cfg.Source == "" {
		cfg.Source = "header.x-forwarded-for"
	}
	// Parse trusted proxy CIDRs (comma-separated, optional).
	if trustedProxies := strings.TrimSpace(input["trusted_proxies"]); trustedProxies != "" {
		cfg.TrustedProxyCIDRs = parseCIDRs(trustedProxies)
	}
	return cfg, nil
}

// IPRestriction enforces allow/deny CIDR policy against resolved client IP.
// sourceSlot takes precedence when >= 0; otherwise source is used.
func IPRestriction(cfg IPRestrictionConfig, sourceSlot int) engine.Instruction {
	nets := parseCIDRs(cfg.CIDRs)
	modeAllow := cfg.Mode == "allow"
	onViolationBody := []byte(cfg.OnViolationBody)
	source := strings.ToLower(strings.TrimSpace(cfg.Source))
	trustedProxyCIDRs := cfg.TrustedProxyCIDRs

	return engine.Instruction{
		Name: "IP_RESTRICTION",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			ip := resolveClientIP(ctx, sourceSlot, source, trustedProxyCIDRs)
			if ip == nil {
				// Conservative default: unresolved IP behaves as no-match.
				if modeAllow {
					ctx.ResponseStatus = cfg.OnViolationStatus
					ctx.ErrorCode = int16(cfg.OnViolationStatus)
					ctx.Failed = true
					ctx.ResponseBuffer = append(ctx.ResponseBuffer[:0], onViolationBody...)
					return engine.StopPlan
				}
				return s.PC + 1
			}

			matched := false
			for _, n := range nets {
				if n.Contains(ip) {
					matched = true
					break
				}
			}
			blocked := (modeAllow && !matched) || (!modeAllow && matched)
			if blocked {
				ctx.ResponseStatus = cfg.OnViolationStatus
				ctx.ErrorCode = int16(cfg.OnViolationStatus)
				ctx.Failed = true
				ctx.ResponseBuffer = append(ctx.ResponseBuffer[:0], onViolationBody...)
				return engine.StopPlan
			}
			return s.PC + 1
		},
	}
}

func parseCIDRs(raw string) []*net.IPNet {
	parts := strings.Split(raw, ",")
	out := make([]*net.IPNet, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		_, n, err := net.ParseCIDR(p)
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

// resolveClientIP resolves the client IP according to the source config and trusted proxy settings.
// If source is "header.x-forwarded-for", XFF is only trusted if the direct peer IP
// (ctx.Request.RemoteAddr) is within one of the trustedProxyCIDRs. If no trusted proxies
// are configured or the peer is not a trusted proxy, the direct peer IP is used instead.
func resolveClientIP(ctx *rctx.Context, sourceSlot int, source string, trustedProxyCIDRs []*net.IPNet) net.IP {
	if sourceSlot >= 0 && sourceSlot < len(ctx.ByteSlots) && len(ctx.ByteSlots[sourceSlot]) > 0 {
		if ip := net.ParseIP(strings.TrimSpace(string(ctx.ByteSlots[sourceSlot]))); ip != nil {
			return ip
		}
	}

	switch source {
	case "header.x-real-ip":
		if ip := net.ParseIP(strings.TrimSpace(ctx.Request.Header.Get("X-Real-IP"))); ip != nil {
			return ip
		}
	case "remote_addr":
		if host, _, err := net.SplitHostPort(ctx.Request.RemoteAddr); err == nil {
			if ip := net.ParseIP(strings.TrimSpace(host)); ip != nil {
				return ip
			}
		}
	default: // header.x-forwarded-for
		// First, extract the direct peer IP from RemoteAddr.
		var peerIP net.IP
		if host, _, err := net.SplitHostPort(ctx.Request.RemoteAddr); err == nil {
			peerIP = net.ParseIP(strings.TrimSpace(host))
		}

		// Only trust XFF if the peer IP is in one of the trusted proxy CIDRs.
		isTrustedProxy := false
		if peerIP != nil && len(trustedProxyCIDRs) > 0 {
			for _, cidr := range trustedProxyCIDRs {
				if cidr.Contains(peerIP) {
					isTrustedProxy = true
					break
				}
			}
		}

		if isTrustedProxy {
			// Peer is a trusted proxy; use the first IP from XFF.
			if ip := net.ParseIP(firstIPFromHeader(ctx.Request.Header.Get("X-Forwarded-For"))); ip != nil {
				return ip
			}
		}

		// Fallback chain: X-Real-IP (if not trusting XFF), then direct peer IP.
		if ip := net.ParseIP(strings.TrimSpace(ctx.Request.Header.Get("X-Real-IP"))); ip != nil {
			return ip
		}
		if peerIP != nil {
			return peerIP
		}
	}
	return nil
}
