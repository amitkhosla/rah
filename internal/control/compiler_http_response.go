package control

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileSetCookie emits a SET_COOKIE instruction.
//
// YAML syntax:
//
//	- set_cookie:
//	    name: "session_id"       # literal cookie name (required)
//	    value_slot: my_var       # slot holding the value at runtime (required)
//	    http_only: true
//	    secure: true
//	    same_site: "Lax"         # "Strict" | "Lax" | "None"
//	    max_age_sec: 3600        # 0 = session cookie
//	    path: "/"
//	    domain: ""               # empty = omit Domain attribute
//
// Compilation strategy:
//   - The complete "name=" prefix is built as []byte at bake time.
//   - The full attribute suffix ("; Path=/; Max-Age=3600; HttpOnly; ...") is
//     built as []byte at bake time.
//   - Only the cookie value is read from ByteSlots[valueSlot] at runtime.
//   - Runtime cost: one ctx.Alloc + three copies — zero heap allocation.
func (c *Compiler) compileSetCookie(step StepConfig) error {
	cookieName := strings.TrimSpace(step.Input["name"])
	if cookieName == "" {
		return fmt.Errorf("set_cookie: 'name' is required")
	}

	valueSlotName := strings.TrimSpace(step.Input["value_slot"])
	if valueSlotName == "" {
		return fmt.Errorf("set_cookie: 'value_slot' is required")
	}
	valueSlot, err := c.getSlot(valueSlotName)
	if err != nil {
		return fmt.Errorf("set_cookie: value_slot: %w", err)
	}

	// Optional attributes — all resolved at compile time.
	path := strings.TrimSpace(step.Input["path"])
	if path == "" {
		path = "/"
	}

	maxAgeSec := 0
	if raw := strings.TrimSpace(step.Input["max_age_sec"]); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil {
			maxAgeSec = n
		}
	}

	httpOnly := strings.ToLower(strings.TrimSpace(step.Input["http_only"])) == "true"
	secure := strings.ToLower(strings.TrimSpace(step.Input["secure"])) == "true"

	sameSite := strings.TrimSpace(step.Input["same_site"])
	// Normalise to canonical casing accepted by browsers.
	switch strings.ToLower(sameSite) {
	case "strict":
		sameSite = "Strict"
	case "lax":
		sameSite = "Lax"
	case "none":
		sameSite = "None"
	default:
		sameSite = ""
	}

	domain := strings.TrimSpace(step.Input["domain"])

	// Build compile-time prefix and suffix — hot path does zero allocation.
	namePrefix := []byte(cookieName + "=")
	suffix := steps.BuildSetCookieSuffix(path, maxAgeSec, httpOnly, secure, sameSite, domain)

	c.GlobalTable = append(c.GlobalTable, steps.SetCookieStep(namePrefix, suffix, valueSlot))
	return nil
}

// compileRedirect emits a REDIRECT instruction.
//
// YAML syntax (mutually exclusive url / url_slot):
//
//	- redirect:
//	    url: "https://provider.com/auth"   # literal URL
//	    status: 302                         # 301 or 302 (default 302)
//
//	- redirect:
//	    url_slot: my_url_var               # slot holding the URL at runtime
//	    status: 302
//
// Compilation strategy:
//   - Literal URL: stored as []byte at bake time. Runtime: zero allocation.
//   - Dynamic URL: read from ByteSlots[urlSlot] at runtime; copied into arena.
//   - Status code is baked into the closure.
//   - The step always terminates the flow (returns StopPlan).
func (c *Compiler) compileRedirect(step StepConfig) error {
	rawURL := strings.TrimSpace(step.Input["url"])
	urlSlotName := strings.TrimSpace(step.Input["url_slot"])

	if rawURL == "" && urlSlotName == "" {
		return fmt.Errorf("redirect: one of 'url' (literal) or 'url_slot' (slot name) is required")
	}
	if rawURL != "" && urlSlotName != "" {
		return fmt.Errorf("redirect: 'url' and 'url_slot' are mutually exclusive")
	}

	status := 302
	if raw := strings.TrimSpace(step.Input["status"]); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && (n == 301 || n == 302) {
			status = n
		}
	}

	var staticURL []byte
	urlSlot := -1

	if rawURL != "" {
		// Bake the URL as []byte at compile time — zero runtime allocation.
		staticURL = []byte(rawURL)
	} else {
		var err error
		urlSlot, err = c.getSlot(urlSlotName)
		if err != nil {
			return fmt.Errorf("redirect: url_slot: %w", err)
		}
	}

	c.GlobalTable = append(c.GlobalTable, steps.RedirectStep(staticURL, urlSlot, status))
	return nil
}
