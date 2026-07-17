package steps

// owasp_check â€” OWASP Top-10 injection pattern scanning step.
//
// Scans the incoming request (URL query, body, selected headers) for SQL
// injection, XSS, path traversal, and command injection patterns.
// Operates in two modes:
//
//   - block (default): returns HTTP 403 (or configured status) and halts the flow.
//   - tag: writes the name of the first matched check category (or "") into a
//     slot and continues execution.
//
// All matching is case-insensitive substring matching â€” no regex, zero alloc
// per request. Patterns are pre-lowercased once at bake time.
//
// Config keys:
//
//	owasp.checks          â€” comma-separated checks to run: "sqli","xss","path_traversal","cmd_inject","all"
//	owasp.targets         â€” comma-separated scan targets: "body","query","headers","all"
//	owasp.mode            â€” "block" (default) or "tag"
//	owasp.tag_var         â€” slot name to write first matched category into when mode=tag
//	owasp.failure_status  â€” HTTP status when blocked (default 403)
//	owasp.failure_body    â€” response body when blocked (default "request blocked")

import (
	"strings"

	"github.com/amitkhosla/rah/internal/engine"
	"github.com/amitkhosla/rah/internal/rctx"
)

// owaspSQLiPatterns is the pre-lowercased SQL injection pattern list.
var owaspSQLiPatterns = []string{
	"' or '1'='1",
	"' or 1=1",
	"; drop table",
	"union select",
	"' --",
	"xp_cmdshell",
	"exec(",
	"execute(",
	"cast(",
	"convert(",
	"char(",
	"nchar(",
	"varchar(",
	"0x",
	"waitfor delay",
	"benchmark(",
	"sleep(",
	"load_file(",
	"into outfile",
	"information_schema",
	"sysobjects",
	"syscolumns",
}

// owaspXSSPatterns is the pre-lowercased XSS pattern list.
var owaspXSSPatterns = []string{
	"<script",
	"javascript:",
	"onerror=",
	"onload=",
	"onclick=",
	"<iframe",
	"<img",
	"<svg",
	"<body",
	"<object",
	"<embed",
	"expression(",
	"vbscript:",
	"data:text/html",
	"&#",
	"%3cscript",
	"%3e",
}

// owaspPathTraversalPatterns is the pre-lowercased path traversal pattern list.
var owaspPathTraversalPatterns = []string{
	"../",
	`..\ `,
	"%2e%2e/",
	`%2e%2e\`,
	"/etc/passwd",
	"/etc/shadow",
	`c:\windows`,
	"c:/windows",
	"%252e",
	"....//",
	"..;/",
}

// owaspCmdInjectPatterns is the pre-lowercased command injection pattern list.
var owaspCmdInjectPatterns = []string{
	"; ls",
	"; cat ",
	"| ls",
	"| cat ",
	"&& ls",
	"|| ls",
	"`ls`",
	"$(",
	"system(",
	"exec(",
	"passthru(",
	"shell_exec(",
	"popen(",
	"proc_open(",
}

// OWASPConfig is the bake-time compiled config for owasp_check.
type OWASPConfig struct {
	// CheckSQLi enables SQL injection pattern scanning.
	CheckSQLi bool
	// CheckXSS enables XSS pattern scanning.
	CheckXSS bool
	// CheckPathTraversal enables path traversal pattern scanning.
	CheckPathTraversal bool
	// CheckCmdInject enables command injection pattern scanning.
	CheckCmdInject bool

	// ScanBody enables scanning of the raw request body.
	ScanBody bool
	// ScanQuery enables scanning of the URL query string.
	ScanQuery bool
	// ScanHeaders enables scanning of selected request headers.
	ScanHeaders bool

	// ModeBlock is true when mode=block (default), false when mode=tag.
	ModeBlock bool
	// TagSlot is the ByteSlot index to write the matched category name into
	// when ModeBlock=false. -1 means no slot configured.
	TagSlot int
	// OnFailureStatus is the HTTP status to return when a request is blocked (default 403).
	OnFailureStatus int
	// OnFailureBody is the response body to return when a request is blocked.
	OnFailureBody string
}

// ParseOWASPConfig converts a step Input map into an OWASPConfig.
// tagSlot is the pre-resolved ByteSlot index for owasp.tag_var (-1 if not set).
func ParseOWASPConfig(input map[string]string, tagSlot int) OWASPConfig {
	cfg := OWASPConfig{
		ModeBlock:       true,
		TagSlot:         tagSlot,
		OnFailureStatus: 403,
		OnFailureBody:   "request blocked",
	}

	// Parse mode.
	if v := strings.TrimSpace(input["owasp.mode"]); strings.EqualFold(v, "tag") {
		cfg.ModeBlock = false
	}

	// Parse failure status.
	if v := strings.TrimSpace(input["owasp.failure_status"]); v != "" {
		if n, err := parseInt(v); err == nil {
			cfg.OnFailureStatus = n
		}
	}

	// Parse failure body.
	if v := strings.TrimSpace(input["owasp.failure_body"]); v != "" {
		cfg.OnFailureBody = v
	}

	// Parse checks â€” default "all".
	checksRaw := strings.TrimSpace(input["owasp.checks"])
	if checksRaw == "" {
		checksRaw = "all"
	}
	allChecks := false
	for _, part := range strings.Split(checksRaw, ",") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "all":
			allChecks = true
		case "sqli":
			cfg.CheckSQLi = true
		case "xss":
			cfg.CheckXSS = true
		case "path_traversal":
			cfg.CheckPathTraversal = true
		case "cmd_inject":
			cfg.CheckCmdInject = true
		}
	}
	if allChecks {
		cfg.CheckSQLi = true
		cfg.CheckXSS = true
		cfg.CheckPathTraversal = true
		cfg.CheckCmdInject = true
	}

	// Parse targets â€” default "all".
	targetsRaw := strings.TrimSpace(input["owasp.targets"])
	if targetsRaw == "" {
		targetsRaw = "all"
	}
	allTargets := false
	for _, part := range strings.Split(targetsRaw, ",") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "all":
			allTargets = true
		case "body":
			cfg.ScanBody = true
		case "query":
			cfg.ScanQuery = true
		case "headers":
			cfg.ScanHeaders = true
		}
	}
	if allTargets {
		cfg.ScanBody = true
		cfg.ScanQuery = true
		cfg.ScanHeaders = true
	}

	return cfg
}

// CheckOWASP builds the OWASP injection scanning instruction.
func CheckOWASP(cfg OWASPConfig) engine.Instruction {
	return engine.Instruction{
		Name: "OWASP_CHECK",
		Action: func(ctx *rctx.Context, s *engine.ExecutionState) int16 {
			stopFail := func() int16 {
				status := cfg.OnFailureStatus
				body := cfg.OnFailureBody
				ctx.ResponseStatus = status
				ctx.Write([]byte(body)) //nolint:errcheck
				ctx.Failed = true
				ctx.ErrorCode = int16(status)
				ctx.ErrorMsg = ctx.Alloc(len(body))
				copy(ctx.ErrorMsg, body)
				return engine.StopPlan
			}

			tagMatch := func(category string) int16 {
				if cfg.TagSlot >= 0 && cfg.TagSlot < len(ctx.ByteSlots) {
					b := ctx.Alloc(len(category))
					copy(b, category)
					ctx.ByteSlots[cfg.TagSlot] = b
				}
				return s.PC + 1
			}

			// Collect scan targets.
			var targets []string

			if cfg.ScanBody && len(ctx.RequestBuffer) > 0 {
				targets = append(targets, strings.ToLower(string(ctx.RequestBuffer)))
			}

			if cfg.ScanQuery && ctx.Request != nil {
				q := ctx.Request.URL.RawQuery
				if q != "" {
					targets = append(targets, strings.ToLower(q))
				}
			}

			if cfg.ScanHeaders && ctx.Request != nil {
				var sb strings.Builder
				for _, hdr := range []string{"User-Agent", "Content-Type", "Referer", "Cookie"} {
					if v := ctx.Request.Header.Get(hdr); v != "" {
						sb.WriteString(v)
						sb.WriteByte(' ')
					}
				}
				if sb.Len() > 0 {
					targets = append(targets, strings.ToLower(sb.String()))
				}
			}

			if len(targets) == 0 {
				// Nothing to scan â€” continue.
				if !cfg.ModeBlock && cfg.TagSlot >= 0 && cfg.TagSlot < len(ctx.ByteSlots) {
					ctx.ByteSlots[cfg.TagSlot] = []byte("")
				}
				return s.PC + 1
			}

			// Run enabled checks across all targets.
			checkPatterns := func(patterns []string, category string) (matched bool, ret int16) {
				for _, target := range targets {
					for _, p := range patterns {
						if strings.Contains(target, p) {
							if cfg.ModeBlock {
								return true, stopFail()
							}
							return true, tagMatch(category)
						}
					}
				}
				return false, 0
			}

			if cfg.CheckSQLi {
				if matched, ret := checkPatterns(owaspSQLiPatterns, "sqli"); matched {
					return ret
				}
			}
			if cfg.CheckXSS {
				if matched, ret := checkPatterns(owaspXSSPatterns, "xss"); matched {
					return ret
				}
			}
			if cfg.CheckPathTraversal {
				if matched, ret := checkPatterns(owaspPathTraversalPatterns, "path_traversal"); matched {
					return ret
				}
			}
			if cfg.CheckCmdInject {
				if matched, ret := checkPatterns(owaspCmdInjectPatterns, "cmd_inject"); matched {
					return ret
				}
			}

			// No match found.
			if !cfg.ModeBlock && cfg.TagSlot >= 0 && cfg.TagSlot < len(ctx.ByteSlots) {
				ctx.ByteSlots[cfg.TagSlot] = []byte("")
			}
			return s.PC + 1
		},
	}
}
