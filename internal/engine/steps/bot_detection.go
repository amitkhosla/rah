package steps

// bot_detection â€” User-Agent based bot/scraper/scanner detection.
//
// Checks the incoming request's User-Agent header against known bot, scraper,
// and vulnerability-scanner patterns. Operates in two modes:
//
//   - block (default): returns HTTP 403 (or configured status) and halts the flow.
//   - tag: writes "true"/"false" into a slot and continues execution.
//
// All matching is case-insensitive substring matching â€” no regex, zero alloc.
// Empty or missing User-Agent is treated as a bot.
//
// Config keys:
//
//	bot.mode             â€” "block" (default) or "tag"
//	bot.tag_var          â€” slot name to write result into when mode=tag
//	bot.failure_status   â€” HTTP status when blocked (default 403)
//	bot.failure_body     â€” response body when blocked (default "access denied")
//	bot.allow_crawlers   â€” "true" to allow known search engine crawlers
//	bot.extra_patterns   â€” comma-separated additional UA substrings to block

import (
	"strings"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// builtinBotPatterns is the compile-time list of known bot/scraper/scanner UA substrings.
// All entries are already lowercased for zero-alloc matching at runtime.
var builtinBotPatterns = []string{
	// Script / tool clients
	"curl/",
	"wget/",
	"python-requests/",
	"python-urllib/",
	"go-http-client/",
	"java/",
	"libwww-perl",
	"lwp-",
	"axios/",
	"node-fetch",
	"node.js",
	"ruby",
	"perl",
	"php/",
	"cfnetwork",

	// Scrapers
	"scrapy",
	"httrack",
	"sitesniffer",
	"sitesucke",
	"winhttrack",
	"webcopier",
	"websuck",
	"webzip",
	"webwhacker",
	"teleport",
	"offline explorer",
	"webcollector",

	// SEO / audit bots (non-crawlers)
	"semrushbot",
	"ahrefsbot",
	"dotbot",
	"mj12bot",
	"blexbot",
	"yetibot",
	"spbot",
	"linkdexbot",
	"rogerbot",
	"exabot",
	"gigabot",
	"ia_archiver",

	// Vulnerability scanners
	"sqlmap",
	"nikto",
	"nmap",
	"masscan",
	"nessus",
	"openvas",
	"w3af",
	"skipfish",
	"acunetix",
	"netsparker",
	"burpsuite",
	"havij",
	"pangolin",
	"grabber",
	"dirbuster",
	"gobuster",
	"wfuzz",
	"hydra",
	"medusa",
}

// builtinCrawlerPatterns is the list of known legitimate search engine crawlers.
// When bot.allow_crawlers=true, a UA matching any of these is allowed through
// without being checked against builtinBotPatterns.
var builtinCrawlerPatterns = []string{
	"googlebot",
	"bingbot",
	"slurp",
	"duckduckbot",
	"baiduspider",
	"yandexbot",
	"facebookexternalhit",
	"twitterbot",
}

// BotDetectionConfig is the bake-time compiled config for detect_bot.
type BotDetectionConfig struct {
	// ModeBlock is true when mode=block (default), false when mode=tag.
	ModeBlock bool
	// TagSlot is the ByteSlot index to write "true"/"false" into when ModeBlock=false.
	// -1 means no slot configured.
	TagSlot int
	// OnFailureStatus is the HTTP status to return when a bot is blocked (default 403).
	OnFailureStatus int
	// OnFailureBody is the response body to return when a bot is blocked (default "access denied").
	OnFailureBody string
	// AllowCrawlers skips bot detection for known search engine crawlers.
	AllowCrawlers bool
	// Patterns is the full pre-lowercased list of UA substrings to block (builtin + extra).
	Patterns []string
}

// ParseBotDetectionConfig converts a step Input map into a BotDetectionConfig.
// tagSlot is the pre-resolved ByteSlot index for bot.tag_var (-1 if not set).
func ParseBotDetectionConfig(input map[string]string, tagSlot int) BotDetectionConfig {
	cfg := BotDetectionConfig{
		ModeBlock:       true,
		TagSlot:         tagSlot,
		OnFailureStatus: 403,
		OnFailureBody:   "access denied",
		AllowCrawlers:   false,
	}

	if v := strings.TrimSpace(input["bot.mode"]); strings.EqualFold(v, "tag") {
		cfg.ModeBlock = false
	}
	if v := strings.TrimSpace(input["bot.failure_status"]); v != "" {
		if n, err := parseInt(v); err == nil {
			cfg.OnFailureStatus = n
		}
	}
	if v := strings.TrimSpace(input["bot.failure_body"]); v != "" {
		cfg.OnFailureBody = v
	}
	if strings.EqualFold(strings.TrimSpace(input["bot.allow_crawlers"]), "true") {
		cfg.AllowCrawlers = true
	}

	// Build the final pattern list: builtin + extra (all lowercased at bake time).
	patterns := make([]string, len(builtinBotPatterns))
	copy(patterns, builtinBotPatterns)

	if v := strings.TrimSpace(input["bot.extra_patterns"]); v != "" {
		for _, part := range strings.Split(v, ",") {
			p := strings.ToLower(strings.TrimSpace(part))
			if p != "" {
				patterns = append(patterns, p)
			}
		}
	}
	cfg.Patterns = patterns

	return cfg
}

// DetectBot builds the bot detection instruction.
//
// Step config keys:
//
//	bot.mode             â€” "block" (default) or "tag"
//	bot.tag_var          â€” slot name for result in tag mode
//	bot.failure_status   â€” HTTP status when blocked (default 403)
//	bot.failure_body     â€” response body when blocked (default "access denied")
//	bot.allow_crawlers   â€” "true" to allow known search engine crawlers
//	bot.extra_patterns   â€” comma-separated additional UA substrings to block
func DetectBot(cfg BotDetectionConfig) engine.Instruction {
	return engine.Instruction{
		Name: "DETECT_BOT",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			stopFail := func() int16 {
				status := cfg.OnFailureStatus
				body := cfg.OnFailureBody
				ctx.ResponseStatus = status
				_, _ = ctx.Write([]byte(body))
				ctx.Failed = true
				ctx.ErrorCode = int16(status)
				ctx.ErrorMsg = ctx.Alloc(len(body))
				copy(ctx.ErrorMsg, body)
				return engine.StopPlan
			}

			// Read User-Agent from the request.
			var ua string
			if ctx.Request != nil {
				ua = ctx.Request.Header.Get("User-Agent")
			}
			uaLower := strings.ToLower(ua)

			// Empty / missing UA â†’ treat as bot.
			if uaLower == "" {
				if cfg.ModeBlock {
					return stopFail()
				}
				if cfg.TagSlot >= 0 && cfg.TagSlot < len(ctx.ByteSlots) {
					ctx.ByteSlots[cfg.TagSlot] = []byte("true")
				}
				return s.PC + 1
			}

			// Known crawler whitelist check (runs first when allow_crawlers=true).
			if cfg.AllowCrawlers {
				for _, p := range builtinCrawlerPatterns {
					if strings.Contains(uaLower, p) {
						// Legitimate crawler â€” skip bot check entirely.
						if !cfg.ModeBlock && cfg.TagSlot >= 0 && cfg.TagSlot < len(ctx.ByteSlots) {
							ctx.ByteSlots[cfg.TagSlot] = []byte("false")
						}
						return s.PC + 1
					}
				}
			}

			// Bot pattern matching.
			for _, p := range cfg.Patterns {
				if strings.Contains(uaLower, p) {
					// Bot detected.
					if cfg.ModeBlock {
						return stopFail()
					}
					if cfg.TagSlot >= 0 && cfg.TagSlot < len(ctx.ByteSlots) {
						ctx.ByteSlots[cfg.TagSlot] = []byte("true")
					}
					return s.PC + 1
				}
			}

			// Not a bot.
			if !cfg.ModeBlock && cfg.TagSlot >= 0 && cfg.TagSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[cfg.TagSlot] = []byte("false")
			}
			return s.PC + 1
		},
	}
}
