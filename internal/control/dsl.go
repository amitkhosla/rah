package control

// dsl.go — RAH Flow DSL parser (Go port of dsl_parse.ts)
//
// Entry point: ParseDSL(code string) (*DSLResult, error)
//
// The DSL mirrors what the Studio UI "code" tab displays. A flow is a sequence
// of statements, one per line (comments with //, blank lines ignored).
//
// Grammar summary (see dsl_parse.ts for canonical reference):
//
//	var = header("X")              → bind_header
//	var = query("p")               → bind_query_param
//	var = body("path.to")          → bind_body
//	var = path("0")                → concat source=path.0
//	var = client_ip()              → bind_client_ip
//	var = correlation_id()         → bind_correlation_id
//	var = transaction_id()         → store_internal_tx_id
//	var = "literal"                → set_const
//	var = "Hello {name}!"          → set_const + concat chain (template)
//	var = some_slot                → set_const value=some_slot (bare word)
//	var = action.call(params)      → mapped action steps
//	http.get(…) / http.post(…) …
//	cache.get/set/delete …
//	registry.lookup/url/id/…
//	rate_limit(scope: ip)
//	validate_token(…)
//	return / return(200) / return(200, body)
//	fail(500, "msg")
//	call flow_name
//	if (cond) { … } else { … }
//	switch (var) { "v": call flow … }
//	foreach (list as item) { call flow }
//	log("field", slot)
//	set_upstream_header("X-Key", slot)

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
)

// ── Public result type ────────────────────────────────────────────────────────

// DSLResult is returned by ParseDSL.
type DSLResult struct {
	// Steps are the top-level instructions for the named flow.
	Steps []StepConfig
	// ExtraFlows holds auto-generated anonymous flows created for inline
	// if/else/then blocks that aren't a single `call` statement.
	ExtraFlows map[string][]StepConfig
}

// ParseDSL parses a DSL code string and returns a DSLResult.
// ExtraFlows will be non-nil only when anonymous flows were generated.
func ParseDSL(code string) (*DSLResult, error) {
	p := &dslParser{
		lines:      joinContinuationLines(splitLines(code)),
		extraFlows: make(map[string][]StepConfig),
	}
	steps := p.parseBlock(0)
	return &DSLResult{
		Steps:      steps,
		ExtraFlows: p.extraFlows,
	}, nil
}

// joinContinuationLines merges lines that are part of an unfinished function
// call (unbalanced opening brackets) with the following lines until the call
// is closed. This lets users write multi-line DSL calls like:
//
//	validate_token(header.Authorization,
//	  jwks_url: "https://...",
//	  alg: "RS256")
func joinContinuationLines(lines []string) []string {
	result := make([]string, 0, len(lines))
	i := 0
	for i < len(lines) {
		line := lines[i]
		depth := countBracketDepth(line)
		if depth <= 0 {
			result = append(result, line)
			i++
			continue
		}
		// Unbalanced open brackets — join subsequent lines until depth reaches 0.
		joined := line
		i++
		for i < len(lines) && depth > 0 {
			next := strings.TrimSpace(lines[i])
			joined += " " + next
			depth += countBracketDepth(next)
			i++
		}
		result = append(result, joined)
	}
	return result
}

// countBracketDepth counts the net opening-bracket depth of a single line,
// ignoring characters inside single- or double-quoted strings.
// Only counts ( ) [ ] — NOT { } which are block delimiters in DSL, not
// function-call continuation markers.
func countBracketDepth(line string) int {
	depth := 0
	inSingle := false
	inDouble := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inSingle {
			if c == '\'' && (i == 0 || line[i-1] != '\\') {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '"' && (i == 0 || line[i-1] != '\\') {
				inDouble = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		}
	}
	return depth
}

// ── Internal counter for anonymous flow/template slot names ──────────────────

var dslAnonCounter int64

func dslNextAnon() int64 { return atomic.AddInt64(&dslAnonCounter, 1) }

// ── Parser state ──────────────────────────────────────────────────────────────

type dslParser struct {
	lines      []string
	tplCounter int
	extraFlows map[string][]StepConfig
}

func (p *dslParser) tpl() string {
	n := p.tplCounter
	p.tplCounter++
	return fmt.Sprintf("__t%d", n)
}

// splitLines splits text into lines, trimming trailing \r.
func splitLines(text string) []string {
	raw := strings.Split(text, "\n")
	out := make([]string, len(raw))
	for i, l := range raw {
		out[i] = strings.TrimRight(l, "\r")
	}
	return out
}

// ── Parameter parser ──────────────────────────────────────────────────────────

// dslParseParams parses "key: val, key2: \"quoted\", key3: {json}" → map[string]string.
// Quoted values are stored WITHOUT surrounding quotes (escape sequences resolved).
func dslParseParams(raw string) map[string]string {
	out := make(map[string]string)
	if strings.TrimSpace(raw) == "" {
		return out
	}
	i := 0
	n := len(raw)
	ws := func() {
		for i < n && unicode.IsSpace(rune(raw[i])) {
			i++
		}
	}
	for i < n {
		ws()
		if i >= n {
			break
		}
		ks := i
		for i < n && raw[i] != ':' {
			i++
		}
		key := strings.TrimSpace(raw[ks:i])
		// Mixed positional+named params produce keys like "user_message, model".
		// Strip everything up to and including the last comma so only the
		// identifier after the comma is used as the key.
		if idx := strings.LastIndex(key, ","); idx >= 0 {
			key = strings.TrimSpace(key[idx+1:])
		}
		if key == "" || i >= n {
			break
		}
		i++ // skip ':'
		ws()
		var val string
		if i < n && (raw[i] == '"' || raw[i] == '\'') {
			q := raw[i]
			i++
			vs := i
			for i < n && raw[i] != q {
				if raw[i] == '\\' {
					i++
				}
				i++
			}
			// Resolve simple escape sequences for " and '
			inner := raw[vs:i]
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			inner = strings.ReplaceAll(inner, `\'`, `'`)
			val = inner
			if i < n {
				i++
			}
		} else if i < n && (raw[i] == '{' || raw[i] == '[') {
			open := raw[i]
			close := byte('}')
			if open == '[' {
				close = ']'
			}
			depth := 0
			vs := i
			for i < n {
				if raw[i] == open {
					depth++
				} else if raw[i] == close {
					depth--
					if depth == 0 {
						i++
						break
					}
				}
				i++
			}
			val = strings.TrimSpace(raw[vs:i])
		} else {
			vs := i
			for i < n && raw[i] != ',' {
				i++
			}
			val = strings.TrimSpace(raw[vs:i])
		}
		if key != "" {
			out[key] = val
		}
		ws()
		if i < n && raw[i] == ',' {
			i++
		}
	}
	return out
}

// ── Function call splitter ────────────────────────────────────────────────────

// dslSplitFuncCall splits "fn(args)" → (fn, args, true).
// Returns ("", "", false) if text is not a valid function call.
// Handles nested parentheses/brackets.
func dslSplitFuncCall(text string) (fn string, rawArgs string, ok bool) {
	lp := strings.IndexByte(text, '(')
	if lp < 0 {
		return "", "", false
	}
	fn = strings.TrimSpace(text[:lp])
	if fn == "" {
		return "", "", false
	}
	depth := 0
	rp := -1
	for k := lp; k < len(text); k++ {
		switch text[k] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				rp = k
			}
		}
		if rp >= 0 {
			break
		}
	}
	if rp < 0 {
		return "", "", false
	}
	return fn, text[lp+1 : rp], true
}

// ── Quote helpers ─────────────────────────────────────────────────────────────

// dslIsQuoted returns true if s is wrapped in " or '.
func dslIsQuoted(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\''))
}

// dslUnquote strips surrounding quotes from s (trims spaces first).
func dslUnquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

// ── Positional argument extractor ─────────────────────────────────────────────

// dslPositionals returns the first n positional (non-keyed) comma-separated
// tokens from raw, stopping as soon as a "key: val" pair is encountered.
func dslPositionals(raw string, n int) []string {
	out := []string{}
	if n <= 0 {
		return out
	}
	i := 0
	depth := 0
	cur := strings.Builder{}
	for i < len(raw) && len(out) < n {
		c := raw[i]
		switch c {
		case '(', '[', '{':
			depth++
			cur.WriteByte(c)
		case ')', ']', '}':
			depth--
			cur.WriteByte(c)
		case ',':
			if depth == 0 {
				token := strings.TrimSpace(cur.String())
				// A token is a key:value named param only when it contains ':'
				// AND is NOT a quoted string (quoted strings can contain ':' in URLs).
				if strings.Contains(token, ":") && !dslIsQuoted(token) {
					// Encountered a key: val pair — stop
					goto done
				}
				out = append(out, token)
				cur.Reset()
				i++
				continue
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
		i++
	}
done:
	if cur.Len() > 0 && len(out) < n {
		token := strings.TrimSpace(cur.String())
		isNamedPair := strings.Contains(token, ":") && !dslIsQuoted(token)
		if !isNamedPair && token != "" {
			out = append(out, token)
		}
	}
	return out
}

// ── Source function set ───────────────────────────────────────────────────────

var dslSrcFns = map[string]bool{
	"header": true, "query": true, "body": true, "path": true,
	"client_ip": true, "correlation_id": true, "transaction_id": true,
}

// dslSrcStep emits a binding step for a source function (header, query, etc.)
// into slot `slot`. Returns nil if fn is not a recognized source function.
func dslSrcStep(fn, rawArgs, slot string) *StepConfig {
	a := dslUnquote(rawArgs)
	switch fn {
	case "header":
		return &StepConfig{Action: "bind_header", Key: a, As: slot}
	case "query":
		return &StepConfig{Action: "bind_query_param", Key: a, As: slot}
	case "body":
		return &StepConfig{Action: "bind_body", Key: a, As: slot}
	case "path":
		return &StepConfig{Action: "concat", Source: "path." + a, As: slot}
	case "client_ip":
		return &StepConfig{Action: "bind_client_ip", KeyIdentifier: slot}
	case "correlation_id":
		key := a
		if key == "" {
			key = "X-Correlation-ID"
		}
		return &StepConfig{Action: "bind_correlation_id", Key: key, As: slot, GenerateIfMissing: true}
	case "transaction_id":
		return &StepConfig{Action: "store_internal_tx_id", As: slot}
	}
	return nil
}

// ── Template string builder ───────────────────────────────────────────────────

type tplSegKind int

const (
	tplText tplSegKind = iota
	tplVar
)

type tplSeg struct {
	kind tplSegKind
	val  string
}

// dslTplSegments breaks a template string (already unquoted) into alternating
// text / variable segments by scanning for {identifier} patterns.
func dslTplSegments(tmpl string) []tplSeg {
	var segs []tplSeg
	last := 0
	i := 0
	for i < len(tmpl) {
		if tmpl[i] == '{' {
			// find closing }
			j := i + 1
			for j < len(tmpl) && tmpl[j] != '}' {
				j++
			}
			if j >= len(tmpl) {
				break
			}
			name := tmpl[i+1 : j]
			// Only treat as variable if name is a valid identifier
			if dslIsIdent(name) {
				if i > last {
					segs = append(segs, tplSeg{kind: tplText, val: tmpl[last:i]})
				}
				segs = append(segs, tplSeg{kind: tplVar, val: name})
				last = j + 1
				i = j + 1
				continue
			}
		}
		i++
	}
	if last < len(tmpl) {
		segs = append(segs, tplSeg{kind: tplText, val: tmpl[last:]})
	}
	return segs
}

func dslIsIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				return false
			}
		}
	}
	return true
}

// dslTplSteps builds a set_const + concat chain for a template string.
// Returns (steps, finalSlot).
func (p *dslParser) dslTplSteps(tmpl string) ([]StepConfig, string) {
	segs := dslTplSegments(tmpl)

	// Pure text — no variables
	allText := true
	for _, s := range segs {
		if s.kind == tplVar {
			allText = false
			break
		}
	}
	if allText {
		slot := p.tpl()
		return []StepConfig{{Action: "set_const", Value: tmpl, As: slot}}, slot
	}

	var steps []StepConfig
	var acc string // current accumulated slot

	for _, seg := range segs {
		if seg.kind == tplText {
			if seg.val == "" {
				continue
			}
			s := p.tpl()
			steps = append(steps, StepConfig{Action: "set_const", Value: seg.val, As: s})
			if acc == "" {
				acc = s
			} else {
				out := p.tpl()
				steps = append(steps, StepConfig{Action: "concat", KeyIdentifier: acc, Source: s, As: out})
				acc = out
			}
		} else {
			// variable segment
			if acc == "" {
				acc = seg.val
			} else {
				out := p.tpl()
				steps = append(steps, StepConfig{Action: "concat", KeyIdentifier: acc, Source: seg.val, As: out})
				acc = out
			}
		}
	}
	if acc == "" {
		acc = p.tpl()
	}
	return steps, acc
}

// ── output.* statement handler ────────────────────────────────────────────────

func (p *dslParser) outputSteps(lhs, rhs string) []StepConfig {
	r := strings.TrimSpace(rhs)

	if lhs == "output.status" {
		return []StepConfig{{Action: "set_response_status", Value: r}}
	}

	// output.header("Name") = slot
	if strings.HasPrefix(lhs, "output.header(") {
		inner := lhs[len("output.header("):]
		// strip closing )
		if idx := strings.Index(inner, ")"); idx >= 0 {
			inner = inner[:idx]
		}
		headerName := dslUnquote(inner)
		return []StepConfig{{Action: "set_response_header", Key: headerName, Source: r}}
	}

	if lhs == "output.body" {
		if dslIsQuoted(r) {
			inner := dslUnquote(r)
			hasTplVar := false
			for _, s := range dslTplSegments(inner) {
				if s.kind == tplVar {
					hasTplVar = true
					break
				}
			}
			if hasTplVar {
				tSteps, slot := p.dslTplSteps(inner)
				tSteps = append(tSteps, StepConfig{Action: "set_response_body", Source: slot})
				return tSteps
			}
			s := p.tpl()
			return []StepConfig{
				{Action: "set_const", Value: inner, As: s},
				{Action: "set_response_body", Source: s},
			}
		}
		return []StepConfig{{Action: "set_response_body", Source: r}}
	}

	return nil
}

// ── Action call dispatcher ────────────────────────────────────────────────────

// dslActionSteps maps a known action function call to one or more StepConfigs.
// as_ is the LHS variable name (empty for bare calls).
func (p *dslParser) dslActionSteps(action, rawParams string, as_ string) []StepConfig {
	params := dslParseParams(rawParams)
	withAs := func(s *StepConfig) *StepConfig {
		if as_ != "" {
			s.As = as_
		}
		return s
	}

	switch action {

	// ── Cache ─────────────────────────────────────────────────────────────────
	case "cache.get":
		pos := dslPositionals(rawParams, 1)
		key := rawParams
		if len(pos) > 0 {
			key = pos[0]
		}
		return []StepConfig{*withAs(&StepConfig{Action: "cache_get", KeyIdentifier: key})}

	case "shared_cache.get":
		pos := dslPositionals(rawParams, 1)
		key := rawParams
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		return []StepConfig{*withAs(&StepConfig{Action: "cache_get_global", KeyIdentifier: key})}

	case "cache.set":
		pos := dslPositionals(rawParams, 2)
		keySlot, valSlot := "", ""
		if len(pos) > 0 {
			keySlot = pos[0]
		}
		if len(pos) > 1 {
			valSlot = pos[1]
		}
		ttl := params["ttl"]
		if ttl == "" {
			ttl = "300"
		}
		ttlN, _ := strconv.ParseUint(ttl, 10, 32)
		return []StepConfig{{Action: "cache_put", KeyIdentifier: keySlot, Source: valSlot, TTL: uint32(ttlN)}}

	case "shared_cache.set":
		pos := dslPositionals(rawParams, 2)
		keySlot, valSlot := "", ""
		if len(pos) > 0 {
			keySlot = dslUnquote(pos[0])
		}
		if len(pos) > 1 {
			valSlot = pos[1]
		}
		ttl := params["ttl"]
		if ttl == "" {
			ttl = "3600"
		}
		ttlN, _ := strconv.ParseUint(ttl, 10, 32)
		return []StepConfig{{Action: "cache_put_global", KeyIdentifier: keySlot, Source: valSlot, TTL: uint32(ttlN)}}

	case "cache.delete":
		pos := dslPositionals(rawParams, 1)
		key := strings.TrimSpace(rawParams)
		if len(pos) > 0 {
			key = pos[0]
		}
		return []StepConfig{{Action: "cache_delete", KeyIdentifier: key}}

	case "shared_cache.delete":
		pos := dslPositionals(rawParams, 1)
		key := strings.TrimSpace(rawParams)
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		return []StepConfig{{Action: "cache_delete_global", KeyIdentifier: key}}

	case "cache.get_batched":
		pos := dslPositionals(rawParams, 1)
		v := ""
		if len(pos) > 0 {
			v = pos[0]
		}
		return []StepConfig{{Action: "cache_get_batched", Variable: v, Destination: params["into"]}}

	case "batch_flush":
		return []StepConfig{{Action: "batch_flush"}}

	case "json_extract":
		pos := dslPositionals(rawParams, 1)
		v := ""
		if len(pos) > 0 {
			v = pos[0]
		}
		return []StepConfig{{Action: "json_extract_emit", Variable: v, Source: params["ops"]}}

	case "json_foreach":
		pos := dslPositionals(rawParams, 1)
		v := ""
		if len(pos) > 0 {
			v = pos[0]
		}
		return []StepConfig{{Action: "json_foreach_emit", Variable: v, Path: params["array"], Source: params["ops"]}}

	// ── Registry ──────────────────────────────────────────────────────────────
	case "registry.lookup":
		arg := strings.TrimSpace(rawParams)
		keyID := arg
		if fn2, args2, ok2 := dslSplitFuncCall(arg); ok2 && dslSrcFns[fn2] {
			a2 := dslUnquote(args2)
			switch fn2 {
			case "header":
				keyID = "header." + a2
			case "query":
				keyID = "queryparam." + a2
			default:
				keyID = fn2 + "." + a2
			}
		}
		return []StepConfig{{Action: "registry_lookup", KeyIdentifier: keyID}}

	case "registry.url":
		pos := dslPositionals(rawParams, 1)
		key := ""
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		return []StepConfig{*withAs(&StepConfig{Action: "load_service_url", Key: key})}

	case "registry.url_var":
		pos := dslPositionals(rawParams, 1)
		key := ""
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		return []StepConfig{*withAs(&StepConfig{Action: "load_service_url_var", KeyIdentifier: key})}

	case "registry.id":
		pos := dslPositionals(rawParams, 1)
		key := ""
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		return []StepConfig{*withAs(&StepConfig{Action: "load_identifier", Key: key})}

	case "registry.meta":
		pos := dslPositionals(rawParams, 1)
		key := ""
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		return []StepConfig{*withAs(&StepConfig{Action: "load_meta", Key: key})}

	case "registry.set_url":
		pos := dslPositionals(rawParams, 2)
		key, src := "", ""
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		if len(pos) > 1 {
			src = pos[1]
		}
		return []StepConfig{{Action: "set_service_url", Key: key, Source: src}}

	case "registry.set_id":
		pos := dslPositionals(rawParams, 2)
		key, src := "", ""
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		if len(pos) > 1 {
			src = pos[1]
		}
		return []StepConfig{{Action: "set_identifier", Key: key, Source: src}}

	case "registry.set_meta":
		pos := dslPositionals(rawParams, 2)
		key, src := "", ""
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		if len(pos) > 1 {
			src = pos[1]
		}
		return []StepConfig{{Action: "set_meta", Key: key, Source: src}}

	case "registry.delete_url":
		pos := dslPositionals(rawParams, 1)
		key := ""
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		return []StepConfig{{Action: "delete_service_url", Key: key}}

	// ── Rate limiting ─────────────────────────────────────────────────────────
	case "rate_limit":
		scope := params["scope"]
		ipSlot := params["ip"]
		keySlot := params["key"]
		groups := params["groups"]
		switch scope {
		case "ip":
			return []StepConfig{{Action: "check_rate_limit", Input: map[string]string{"scope": "ip", "ip_slot": ipSlot}}}
		case "slot":
			return []StepConfig{{Action: "check_rate_limit", Input: map[string]string{"scope": "slot", "key_slot": keySlot}}}
		default:
			if groups != "" {
				return []StepConfig{{Action: "check_rate_limit", Input: map[string]string{"groups": groups}}}
			}
			return []StepConfig{{Action: "check_rate_limit"}}
		}

	case "rate_limit_v2":
		config := params["config"]
		countBy := params["count_by"]
		slot := params["slot"]
		return []StepConfig{{Action: "check_rate_limit_v2", Input: map[string]string{"config": config, "count_by": countBy, "slot": slot}}}

	case "assign_quota":
		pos := dslPositionals(rawParams, 1)
		key := ""
		if len(pos) > 0 {
			key = pos[0]
		}
		return []StepConfig{{Action: "assign_quota_group", KeyIdentifier: key, Input: map[string]string{"groups": params["groups"]}}}

	// ── Token validation ──────────────────────────────────────────────────────
	case "validate_token":
		pos := dslPositionals(rawParams, 1)
		src := ""
		if len(pos) > 0 {
			src = pos[0]
		}
		input := make(map[string]string)
		if v, ok := params["checks"]; ok {
			input["jwt.validate"] = v
		}
		if v, ok := params["jwks_url"]; ok {
			input["jwt.jwks_uri"] = v
		}
		if v, ok := params["jwks_url_var"]; ok {
			input["jwt.jwks_uri_var"] = v
		}
		if v, ok := params["issuer"]; ok {
			input["jwt.issuer"] = v
		}
		if v, ok := params["audience"]; ok {
			input["jwt.audience"] = v
		}
		if v, ok := params["on_failure"]; ok {
			input["jwt.on_failure"] = v
		}
		if v, ok := params["result_var"]; ok {
			input["jwt.result_var"] = v
		}
		if v, ok := params["claims_var"]; ok {
			input["jwt.claims_var"] = v
		}
		if v, ok := params["subject_var"]; ok {
			input["jwt.subject_var"] = v
		}
		if v, ok := params["failure_status"]; ok {
			input["jwt.failure_status"] = v
		}
		if v, ok := params["leeway"]; ok {
			input["jwt.leeway_seconds"] = v
		}
		step := StepConfig{Action: "token_validation", KeyIdentifier: src}
		if len(input) > 0 {
			step.Input = input
		}
		return []StepConfig{step}

	case "validate_introspection":
		input := make(map[string]string)
		if v, ok := params["url"]; ok {
			input["introspect.endpoint"] = v
		}
		if v, ok := params["endpoint"]; ok {
			input["introspect.endpoint"] = v
		}
		if v, ok := params["token_header"]; ok {
			input["introspect.token_header"] = v
		}
		if v, ok := params["token_var"]; ok {
			input["introspect.token_var"] = v
		}
		if v, ok := params["cache_ttl"]; ok {
			input["introspect.cache_ttl_seconds"] = v
		}
		if v, ok := params["cache_ttl_seconds"]; ok {
			input["introspect.cache_ttl_seconds"] = v
		}
		if v, ok := params["subject_var"]; ok {
			input["introspect.claims_var"] = v
		}
		if v, ok := params["claims_var"]; ok {
			input["introspect.claims_var"] = v
		}
		if v, ok := params["result_var"]; ok {
			input["introspect.result_var"] = v
		}
		if v, ok := params["client_id"]; ok {
			input["introspect.client_id"] = v
		}
		if v, ok := params["client_secret"]; ok {
			input["introspect.client_secret"] = v
		}
		if v, ok := params["bearer_token"]; ok {
			input["introspect.bearer_token"] = v
		}
		if v, ok := params["on_failure"]; ok {
			input["introspect.on_failure"] = v
		}
		if v, ok := params["failure_status"]; ok {
			input["introspect.failure_status"] = v
		}
		if v, ok := params["required_scopes"]; ok {
			input["introspect.required_scopes"] = v
		}
		return []StepConfig{{Action: "validate_token_introspection", Input: input}}

	// ── Secrets ───────────────────────────────────────────────────────────────
	case "load_secret":
		pos := dslPositionals(rawParams, 1)
		ref := ""
		if len(pos) > 0 {
			ref = dslUnquote(pos[0])
		}
		slotNum := params["slot"]
		if slotNum == "" {
			slotNum = "0"
		}
		s := StepConfig{Action: "load_secret", Key: ref, Source: slotNum}
		if as_ != "" {
			s.As = as_
		}
		return []StepConfig{s}

	// ── HTTP ──────────────────────────────────────────────────────────────────
	case "http.get", "http.post", "http.put", "http.delete", "http.patch":
		method := strings.ToUpper(strings.TrimPrefix(action, "http."))
		pos := dslPositionals(rawParams, 1)
		step := StepConfig{Action: "http_call", Method: method}
		if len(pos) > 0 && pos[0] != "" {
			urlVal := pos[0]
			if dslIsQuoted(urlVal) {
				// Quoted first arg is always a literal URL (may contain ':').
				step.URL = dslUnquote(urlVal)
			} else if !strings.Contains(urlVal, ":") {
				// Unquoted with no ':' → variable reference.
				step.UrlVar = urlVal
			}
			// Unquoted with ':' would be a named param like "url: foo" — handled below.
		}
		// Apply named params
		if v, ok := params["url"]; ok {
			step.URL = dslUnquote(v)
		}
		if v, ok := params["url_var"]; ok {
			step.UrlVar = v
		}
		if v, ok := params["body_var"]; ok {
			step.BodyVar = v
		}
		if v, ok := params["content_type"]; ok {
			step.ContentType = v
		}
		if v, ok := params["response_body_var"]; ok {
			step.ResponseBodyVar = v
		}
		if v, ok := params["response_status_var"]; ok {
			step.ResponseStatusVar = v
		}
		if v, ok := params["retry_condition"]; ok {
			step.RetryCondition = v
		}
		if v, ok := params["max_retries"]; ok {
			// parse as int best-effort
			var n int
			_, _ = fmt.Sscanf(v, "%d", &n)
			step.MaxRetries = n
		}
		if v, ok := params["timeout"]; ok {
			var ms uint32
			_, _ = fmt.Sscanf(v, "%d", &ms)
			step.Timeout = ms
		}
		if as_ != "" {
			// LHS var (e.g. body = http.get(...)) → store response body there.
			step.ResponseBodyVar = as_
		}
		return []StepConfig{step}

	case "set_upstream_header":
		pos := dslPositionals(rawParams, 2)
		key, src := "", ""
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		if len(pos) > 1 {
			src = pos[1]
		}
		return []StepConfig{{Action: "set_request_header", Key: key, Source: src}}

	// ── LLM ───────────────────────────────────────────────────────────────────
	case "llm":
		pos := dslPositionals(rawParams, 1)
		src := ""
		if len(pos) > 0 {
			src = pos[0]
		}
		cfg := make(map[string]string)
		for _, k := range []string{"model", "max_tokens", "temperature"} {
			if v, ok := params[k]; ok {
				cfg[k] = v
			}
		}
		step := StepConfig{Action: "llm_call", KeyIdentifier: src}
		if as_ != "" {
			step.As = as_
		}
		if v, ok := params["history"]; ok {
			step.Source = v // history_slot maps to Source
		}
		if len(cfg) > 0 {
			step.Input = cfg
		}
		return []StepConfig{step}

	// ── IP restriction ────────────────────────────────────────────────────────
	case "ip_allow", "ip_deny":
		mode := "allow"
		if action == "ip_deny" {
			mode = "deny"
		}
		pos := dslPositionals(rawParams, 10)
		var cidrs []string
		for _, v := range pos {
			if strings.ContainsAny(v, "./:-") {
				cidrs = append(cidrs, dslUnquote(v))
			}
		}
		cidrStr := strings.Join(cidrs, ",")
		if cidrStr == "" {
			cidrStr = params["cidrs"]
		}
		status := params["on_violation"]
		if status == "" {
			status = "403"
		}
		src := params["source"]
		if src == "" {
			src = "header.X-Forwarded-For"
		}
		cfg := fmt.Sprintf(`{"mode":%q,"cidrs":%q,"source":%q,"on_violation_status":%q,"on_violation_body":"ip not allowed"}`,
			mode, cidrStr, src, status)
		step := StepConfig{Action: "ip_restriction", Input: map[string]string{"config": cfg}}
		if v, ok := params["ip"]; ok {
			step.KeyIdentifier = v
		}
		return []StepConfig{step}

	// ── Observability ─────────────────────────────────────────────────────────
	case "log":
		pos := dslPositionals(rawParams, 2)
		key, src := "", ""
		if len(pos) > 0 {
			key = dslUnquote(pos[0])
		}
		if len(pos) > 1 {
			src = pos[1]
		}
		return []StepConfig{{Action: "log_field", Key: key, Source: src}}

	// ── String ops ────────────────────────────────────────────────────────────
	case "to_lower":
		pos := dslPositionals(rawParams, 1)
		src := ""
		if len(pos) > 0 {
			src = pos[0]
		}
		return []StepConfig{*withAs(&StepConfig{Action: "to_lower", Source: src})}

	case "to_upper":
		pos := dslPositionals(rawParams, 1)
		src := ""
		if len(pos) > 0 {
			src = pos[0]
		}
		return []StepConfig{*withAs(&StepConfig{Action: "to_upper", Source: src})}

	case "byte_length":
		pos := dslPositionals(rawParams, 1)
		src := ""
		if len(pos) > 0 {
			src = pos[0]
		}
		return []StepConfig{*withAs(&StepConfig{Action: "byte_length", Source: src})}

	case "substring":
		pos := dslPositionals(rawParams, 1)
		src := ""
		if len(pos) > 0 {
			src = pos[0]
		}
		return []StepConfig{*withAs(&StepConfig{
			Action: "substring",
			Source: src,
			Input:  map[string]string{"start": params["start"], "length": params["length"]},
		})}

	case "concat":
		pos := dslPositionals(rawParams, 2)
		a, b := "", ""
		if len(pos) > 0 {
			a = pos[0]
		}
		if len(pos) > 1 {
			b = pos[1]
		}
		return []StepConfig{*withAs(&StepConfig{Action: "concat", KeyIdentifier: a, Source: b})}

	case "validate_pattern":
		pos := dslPositionals(rawParams, 2)
		src, pat := "", ""
		if len(pos) > 0 {
			src = dslUnquote(pos[0])
		}
		if len(pos) > 1 {
			pat = dslUnquote(pos[1])
		}
		return []StepConfig{*withAs(&StepConfig{
			Action: "validate_pattern",
			Source: src,
			Input:  map[string]string{"pattern": pat},
		})}

	case "extract":
		pos := dslPositionals(rawParams, 2)
		src, pat := "", ""
		if len(pos) > 0 {
			src = dslUnquote(pos[0])
		}
		if len(pos) > 1 {
			pat = dslUnquote(pos[1])
		}
		return []StepConfig{{
			Action: "extract_pattern",
			Source: src,
			Input:  map[string]string{"pattern": pat},
		}}

	case "to_int":
		pos := dslPositionals(rawParams, 1)
		src := ""
		if len(pos) > 0 {
			src = pos[0]
		}
		return []StepConfig{*withAs(&StepConfig{Action: "to_int", Source: src})}

	// ── Math ──────────────────────────────────────────────────────────────────
	case "add", "sub", "mul", "div":
		pos := dslPositionals(rawParams, 2)
		a, b := "", ""
		if len(pos) > 0 {
			a = pos[0]
		}
		if len(pos) > 1 {
			b = pos[1]
		}
		return []StepConfig{*withAs(&StepConfig{Action: action, KeyIdentifier: a, Source: b})}

	// ── Error handling ────────────────────────────────────────────────────────
	case "on_error":
		return []StepConfig{{Action: "capture_error", Key: params["code"], As: params["message"]}}

	// ── Generic fallback ──────────────────────────────────────────────────────
	default:
		step := StepConfig{Action: action}
		if as_ != "" {
			step.As = as_
		}
		if len(params) > 0 {
			step.Input = make(map[string]string, len(params))
			for k, v := range params {
				step.Input[k] = v
			}
		}
		return []StepConfig{step}
	}
}

// ── Block parser (recursive) ──────────────────────────────────────────────────

// parseBlock parses lines[start:] until a line starting with "}" or EOF.
// Returns the collected steps; the caller is responsible for advancing past
// the closing "}" line (index returned via parseBlockResult).
func (p *dslParser) parseBlockR(lines []string, start int) (steps []StepConfig, next int) {
	i := start
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "}") {
			break
		}
		if line == "" || strings.HasPrefix(line, "//") {
			i++
			continue
		}

		// ── return ─────────────────────────────────────────────────────────
		if line == "return" {
			steps = append(steps, StepConfig{Action: "return", Status: 200})
			i++
			continue
		}
		if strings.HasPrefix(line, "return(") {
			_, args, ok := dslSplitFuncCall(line)
			if ok {
				pos := dslPositionals(args, 2)
				status := 200
				if len(pos) > 0 {
					_, _ = fmt.Sscanf(pos[0], "%d", &status)
				}
				body, as := "", ""
				if len(pos) > 1 {
					if dslIsQuoted(pos[1]) {
						body = dslUnquote(pos[1])
					} else {
						as = pos[1] // unquoted → slot reference
					}
				}
				steps = append(steps, StepConfig{Action: "return", Status: status, Body: body, As: as})
				i++
				continue
			}
		}

		// ── fail ───────────────────────────────────────────────────────────
		if strings.HasPrefix(line, "fail(") {
			_, args, ok := dslSplitFuncCall(line)
			if ok {
				pos := dslPositionals(args, 2)
				status := 500
				if len(pos) > 0 {
					fmt.Sscanf(pos[0], "%d", &status)
				}
				body, as := "", ""
				if len(pos) > 1 {
					if dslIsQuoted(pos[1]) {
						body = dslUnquote(pos[1])
					} else {
						as = pos[1] // unquoted → slot reference
					}
				}
				steps = append(steps, StepConfig{Action: "fail", Status: status, Body: body, As: as})
				i++
				continue
			}
		}

		// ── call sub-flow ──────────────────────────────────────────────────
		if strings.HasPrefix(line, "call ") {
			flowName := strings.TrimSpace(line[5:])
			steps = append(steps, StepConfig{Action: "call", FlowName: flowName})
			i++
			continue
		}

		// ── if (cond) { ────────────────────────────────────────────────────
		if strings.HasPrefix(line, "if ") || strings.HasPrefix(line, "if(") {
			// Match: if (condition) {
			condStart := strings.Index(line, "(")
			if condStart >= 0 && strings.HasSuffix(line, "{") {
				condEnd := strings.LastIndex(line, ")")
				if condEnd > condStart {
					cond := strings.TrimSpace(line[condStart+1 : condEnd])
					thenSteps, thenNext := p.parseBlockR(lines, i+1)
					i = thenNext
					closingLine := ""
					if i < len(lines) {
						closingLine = strings.TrimSpace(lines[i])
					}
					var elseSteps []StepConfig
					if closingLine == "} else {" {
						elseSteps, i = p.parseBlockR(lines, i+1)
						i++ // consume closing }
					} else {
						i++ // consume closing }
					}

					step := p.buildIfStep(cond, thenSteps, elseSteps)
					steps = append(steps, step)
					continue
				}
			}
		}

		// ── switch (var) { ─────────────────────────────────────────────────
		if strings.HasPrefix(line, "switch ") || strings.HasPrefix(line, "switch(") {
			condStart := strings.Index(line, "(")
			if condStart >= 0 && strings.HasSuffix(line, "{") {
				condEnd := strings.LastIndex(line, ")")
				if condEnd > condStart {
					varName := strings.TrimSpace(line[condStart+1 : condEnd])
					var cases []string
					i++
					for i < len(lines) {
						sl := strings.TrimSpace(lines[i])
						if sl == "}" {
							i++
							break
						}
						if sl == "" || strings.HasPrefix(sl, "//") {
							i++
							continue
						}
						// "value": call flow_name  OR  value: call flow_name
						colonIdx := strings.Index(sl, ":")
						if colonIdx > 0 {
							key := dslUnquote(strings.TrimSpace(sl[:colonIdx]))
							rest := strings.TrimSpace(sl[colonIdx+1:])
							if strings.HasPrefix(rest, "call ") {
								flowName := strings.TrimSpace(rest[5:])
								cases = append(cases, key+"="+flowName)
							}
						}
						i++
					}
					casesMap := make(map[string]string, len(cases))
					for _, c := range cases {
						eqIdx := strings.IndexByte(c, '=')
						if eqIdx > 0 {
							casesMap[c[:eqIdx]] = c[eqIdx+1:]
						}
					}
					steps = append(steps, StepConfig{Action: "switch", As: varName, Cases: casesMap})
					continue
				}
			}
		}

		// ── foreach (list as item) { ───────────────────────────────────────
		if strings.HasPrefix(line, "foreach ") || strings.HasPrefix(line, "foreach(") {
			condStart := strings.Index(line, "(")
			if condStart >= 0 && strings.HasSuffix(line, "{") {
				condEnd := strings.LastIndex(line, ")")
				if condEnd > condStart {
					inner := strings.TrimSpace(line[condStart+1 : condEnd])
					// "list_var as item_var"
					asParts := strings.SplitN(inner, " as ", 2)
					listVar, itemVar := "", ""
					if len(asParts) == 2 {
						listVar = strings.TrimSpace(asParts[0])
						itemVar = strings.TrimSpace(asParts[1])
					}
					var doFlow string
					i++
					for i < len(lines) {
						fl := strings.TrimSpace(lines[i])
						if fl == "}" {
							i++
							break
						}
						if fl == "" || strings.HasPrefix(fl, "//") {
							i++
							continue
						}
						if strings.HasPrefix(fl, "call ") {
							doFlow = strings.TrimSpace(fl[5:])
						}
						i++
					}
					doSteps := []StepConfig{}
					if doFlow != "" {
						doSteps = []StepConfig{{Action: "call", FlowName: doFlow}}
					}
					steps = append(steps, StepConfig{Action: "foreach", Source: listVar, As: itemVar, Do: doSteps})
					continue
				}
			}
		}

		// ── output.* = rhs ─────────────────────────────────────────────────
		if strings.HasPrefix(line, "output.") {
			eqIdx := strings.Index(line, "=")
			if eqIdx > 0 {
				lhs := strings.TrimSpace(line[:eqIdx])
				rhs := strings.TrimSpace(line[eqIdx+1:])
				if out := p.outputSteps(lhs, rhs); len(out) > 0 {
					steps = append(steps, out...)
					i++
					continue
				}
			}
		}

		// ── var = rhs ───────────────────────────────────────────────────────
		if eqIdx := strings.Index(line, "="); eqIdx > 0 {
			potentialSlot := strings.TrimSpace(line[:eqIdx])
			if dslIsIdent(potentialSlot) {
				rhs := strings.TrimSpace(line[eqIdx+1:])

				// var = source_fn("key")
				if fn, args, ok := dslSplitFuncCall(rhs); ok && dslSrcFns[fn] {
					if s := dslSrcStep(fn, args, potentialSlot); s != nil {
						steps = append(steps, *s)
						i++
						continue
					}
				}

				// var = "literal or {template}"
				if dslIsQuoted(rhs) {
					inner := dslUnquote(rhs)
					hasTpl := false
					for _, seg := range dslTplSegments(inner) {
						if seg.kind == tplVar {
							hasTpl = true
							break
						}
					}
					if hasTpl {
						tSteps, finalSlot := p.dslTplSteps(inner)
						if len(tSteps) > 0 {
							// Rename the last step's As to potentialSlot
							last := &tSteps[len(tSteps)-1]
							if last.As == finalSlot {
								last.As = potentialSlot
							} else {
								tSteps = append(tSteps, StepConfig{Action: "concat", KeyIdentifier: finalSlot, Source: finalSlot, As: potentialSlot})
							}
						}
						steps = append(steps, tSteps...)
					} else {
						steps = append(steps, StepConfig{Action: "set_const", Value: inner, As: potentialSlot})
					}
					i++
					continue
				}

				// var = action.call(params)
				if fn, args, ok := dslSplitFuncCall(rhs); ok {
					steps = append(steps, p.dslActionSteps(fn, args, potentialSlot)...)
					i++
					continue
				}

				// var = bare_word → set_const with string value
				steps = append(steps, StepConfig{Action: "set_const", Value: rhs, As: potentialSlot})
				i++
				continue
			}
		}

		// ── bare action call ────────────────────────────────────────────────
		if fn, args, ok := dslSplitFuncCall(line); ok {
			steps = append(steps, p.dslActionSteps(fn, args, "")...)
			i++
			continue
		}

		i++ // skip unknown line
	}
	return steps, i
}

// parseBlock is the public entry (wraps parseBlockR, discards index).
func (p *dslParser) parseBlock(start int) []StepConfig {
	steps, _ := p.parseBlockR(p.lines, start)
	return steps
}

// buildIfStep constructs an `if` StepConfig from condition + then/else steps.
// Inline blocks that are a single `call flow_name` are folded into Then/Else
// directly; otherwise an anonymous flow is auto-generated.
func (p *dslParser) buildIfStep(cond string, thenSteps, elseSteps []StepConfig) StepConfig {
	step := StepConfig{Action: "if", Condition: cond}

	step.Then = p.inlineOrAnon("then", thenSteps)
	if len(elseSteps) > 0 {
		step.Else = p.inlineOrAnon("else", elseSteps)
	}
	return step
}

// inlineOrAnon returns the flow name to use for a branch.
// If steps is a single `call flow_name`, returns that flow name directly.
// Otherwise auto-generates an anonymous flow `__dsl_{label}_N`, stores it in
// extraFlows, and returns its name.
func (p *dslParser) inlineOrAnon(label string, steps []StepConfig) string {
	if len(steps) == 1 && steps[0].Action == "call" && steps[0].FlowName != "" {
		return steps[0].FlowName
	}
	n := dslNextAnon()
	name := fmt.Sprintf("__dsl_%s_%d", label, n)
	p.extraFlows[name] = steps
	return name
}
