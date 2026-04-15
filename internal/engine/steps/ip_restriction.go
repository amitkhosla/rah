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
	Mode              string // allow | deny
	CIDRs             string // comma-separated CIDR list
	Source            string // remote_addr | header.X-Forwarded-For | header.X-Real-IP
	OnViolationStatus int
	OnViolationBody   string
}

// ParseIPRestrictionConfig parses and validates flow input for ip_restriction.
func ParseIPRestrictionConfig(input map[string]string) (IPRestrictionConfig, error) {
	cfg := IPRestrictionConfig{
		Mode:              strings.ToLower(strings.TrimSpace(input["mode"])),
		CIDRs:             strings.TrimSpace(input["cidrs"]),
		Source:            strings.TrimSpace(input["source"]),
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
		cfg.Source = "header.X-Forwarded-For"
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

	return engine.Instruction{
		Name: "IP_RESTRICTION",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			ip := resolveClientIP(ctx, sourceSlot, source)
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

func resolveClientIP(ctx *rctx.Context, sourceSlot int, source string) net.IP {
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
		if ip := net.ParseIP(firstIPFromHeader(ctx.Request.Header.Get("X-Forwarded-For"))); ip != nil {
			return ip
		}
		if ip := net.ParseIP(strings.TrimSpace(ctx.Request.Header.Get("X-Real-IP"))); ip != nil {
			return ip
		}
		if host, _, err := net.SplitHostPort(ctx.Request.RemoteAddr); err == nil {
			if ip := net.ParseIP(strings.TrimSpace(host)); ip != nil {
				return ip
			}
		}
	}
	return nil
}
