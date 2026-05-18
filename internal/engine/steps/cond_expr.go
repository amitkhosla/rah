package steps

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"rah/internal/rctx"
)

// ConditionFunc is a pre-compiled condition closure that evaluates against a
// request context. Built once at compile/bake time; called on every request.
type ConditionFunc func(ctx *rctx.Context) bool

// tokenKind classifies lexer tokens.
type tokenKind int

const (
	tkIdent  tokenKind = iota // identifier or keyword
	tkStr                     // quoted string literal
	tkNum                     // integer literal
	tkAnd                     // &&
	tkOr                      // ||
	tkNot                     // !
	tkEq                      // ==
	tkNe                      // !=
	tkLt                      // <
	tkLe                      // <=
	tkGt                      // >
	tkGe                      // >=
	tkLParen                  // (
	tkRParen                  // )
	tkComma                   // ,
	tkEOF
)

type token struct {
	kind tokenKind
	val  string // raw text for ident/str/num
}

// tokenize splits expr into a flat token slice.
func tokenize(expr string) ([]token, error) {
	var out []token
	i := 0
	for i < len(expr) {
		ch := expr[i]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			i++
			continue
		}
		switch {
		case ch == '&' && i+1 < len(expr) && expr[i+1] == '&':
			out = append(out, token{tkAnd, "&&"})
			i += 2
		case ch == '|' && i+1 < len(expr) && expr[i+1] == '|':
			out = append(out, token{tkOr, "||"})
			i += 2
		case ch == '!' && i+1 < len(expr) && expr[i+1] == '=':
			out = append(out, token{tkNe, "!="})
			i += 2
		case ch == '!':
			out = append(out, token{tkNot, "!"})
			i++
		case ch == '=' && i+1 < len(expr) && expr[i+1] == '=':
			out = append(out, token{tkEq, "=="})
			i += 2
		case ch == '<' && i+1 < len(expr) && expr[i+1] == '=':
			out = append(out, token{tkLe, "<="})
			i += 2
		case ch == '<':
			out = append(out, token{tkLt, "<"})
			i++
		case ch == '>' && i+1 < len(expr) && expr[i+1] == '=':
			out = append(out, token{tkGe, ">="})
			i += 2
		case ch == '>':
			out = append(out, token{tkGt, ">"})
			i++
		case ch == '(':
			out = append(out, token{tkLParen, "("})
			i++
		case ch == ')':
			out = append(out, token{tkRParen, ")"})
			i++
		case ch == ',':
			out = append(out, token{tkComma, ","})
			i++
		case ch == '"' || ch == '\'':
			// quoted string
			quote := ch
			i++
			start := i
			for i < len(expr) && expr[i] != quote {
				i++
			}
			if i >= len(expr) {
				return nil, fmt.Errorf("unterminated string literal in condition")
			}
			out = append(out, token{tkStr, expr[start:i]})
			i++ // consume closing quote
		case ch >= '0' && ch <= '9' || (ch == '-' && i+1 < len(expr) && expr[i+1] >= '0' && expr[i+1] <= '9'):
			start := i
			if ch == '-' {
				i++
			}
			for i < len(expr) && expr[i] >= '0' && expr[i] <= '9' {
				i++
			}
			out = append(out, token{tkNum, expr[start:i]})
		case ch == '_' || unicode.IsLetter(rune(ch)):
			start := i
			for i < len(expr) && (expr[i] == '_' || expr[i] == '.' || unicode.IsLetter(rune(expr[i])) || unicode.IsDigit(rune(expr[i]))) {
				i++
			}
			out = append(out, token{tkIdent, expr[start:i]})
		default:
			return nil, fmt.Errorf("unexpected character %q in condition", string(ch))
		}
	}
	out = append(out, token{tkEOF, ""})
	return out, nil
}

// parser is a simple Pratt/recursive-descent parser that builds ConditionFunc closures.
type parser struct {
	tokens  []token
	pos     int
	slotMap map[string]int
}

func (p *parser) peek() token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return token{tkEOF, ""}
}

func (p *parser) consume() token {
	t := p.peek()
	p.pos++
	return t
}

func (p *parser) expect(k tokenKind) (token, error) {
	t := p.consume()
	if t.kind != k {
		return t, fmt.Errorf("expected token kind %d, got %q", k, t.val)
	}
	return t, nil
}

// parseExpr is the entry point (handles || with lowest precedence).
func (p *parser) parseExpr() (ConditionFunc, error) {
	return p.parseOr()
}

func (p *parser) parseOr() (ConditionFunc, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tkOr {
		p.consume()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(ctx *rctx.Context) bool { return l(ctx) || r(ctx) }
	}
	return left, nil
}

func (p *parser) parseAnd() (ConditionFunc, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tkAnd {
		p.consume()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(ctx *rctx.Context) bool { return l(ctx) && r(ctx) }
	}
	return left, nil
}

func (p *parser) parseUnary() (ConditionFunc, error) {
	if p.peek().kind == tkNot {
		p.consume()
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return func(ctx *rctx.Context) bool { return !inner(ctx) }, nil
	}
	return p.parsePrimary()
}

// isStringPredicate returns true if ident is one of the string predicate functions.
func isStringPredicate(name string) bool {
	switch name {
	case "contains", "startsWith", "endsWith", "matches":
		return true
	}
	return false
}

// resolveOperand returns a func that reads the value of a named identifier as a
// string (for comparisons), plus a bool indicating whether it is a byte-slot
// (as opposed to a special field like "status").
func (p *parser) resolveStringReader(name string) func(ctx *rctx.Context) string {
	switch name {
	case "status":
		return func(ctx *rctx.Context) string { return strconv.Itoa(ctx.ResponseStatus) }
	case "method":
		return func(ctx *rctx.Context) string { return string(ctx.Method) }
	case "path":
		return func(ctx *rctx.Context) string { return string(ctx.Path) }
	}
	// Check byte slot
	if idx, ok := p.slotMap[name]; ok {
		return func(ctx *rctx.Context) string {
			if idx < len(ctx.ByteSlots) {
				return string(ctx.ByteSlots[idx])
			}
			return ""
		}
	}
	// Unknown — return empty string
	return func(ctx *rctx.Context) string { return "" }
}

func (p *parser) resolveIntReader(name string) func(ctx *rctx.Context) int64 {
	if name == "status" {
		return func(ctx *rctx.Context) int64 { return int64(ctx.ResponseStatus) }
	}
	if idx, ok := p.slotMap[name]; ok {
		return func(ctx *rctx.Context) int64 {
			if idx < len(ctx.ByteSlots) {
				v, _ := strconv.ParseInt(string(ctx.ByteSlots[idx]), 10, 64)
				return v
			}
			return 0
		}
	}
	return func(ctx *rctx.Context) int64 { return 0 }
}

func (p *parser) parsePrimary() (ConditionFunc, error) {
	t := p.peek()

	// Parenthesised expression
	if t.kind == tkLParen {
		p.consume()
		fn, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tkRParen); err != nil {
			return nil, err
		}
		return fn, nil
	}

	// Identifier: could be a variable, special name, or function call
	if t.kind == tkIdent {
		p.consume()
		name := t.val

		// String predicate function: contains(slot, "needle")
		if isStringPredicate(name) {
			if _, err := p.expect(tkLParen); err != nil {
				return nil, fmt.Errorf("expected '(' after %s: %w", name, err)
			}
			slotTok, err := p.expect(tkIdent)
			if err != nil {
				return nil, fmt.Errorf("%s: expected slot name: %w", name, err)
			}
			if _, err := p.expect(tkComma); err != nil {
				return nil, fmt.Errorf("%s: expected comma: %w", name, err)
			}
			argTok, err := p.expect(tkStr)
			if err != nil {
				return nil, fmt.Errorf("%s: expected string argument: %w", name, err)
			}
			if _, err := p.expect(tkRParen); err != nil {
				return nil, fmt.Errorf("%s: expected ')': %w", name, err)
			}
			reader := p.resolveStringReader(slotTok.val)
			arg := argTok.val
			switch name {
			case "contains":
				return func(ctx *rctx.Context) bool { return strings.Contains(reader(ctx), arg) }, nil
			case "startsWith":
				return func(ctx *rctx.Context) bool { return strings.HasPrefix(reader(ctx), arg) }, nil
			case "endsWith":
				return func(ctx *rctx.Context) bool { return strings.HasSuffix(reader(ctx), arg) }, nil
			case "matches":
				re, err := regexp.Compile(arg)
				if err != nil {
					return nil, fmt.Errorf("matches: invalid regex %q: %w", arg, err)
				}
				return func(ctx *rctx.Context) bool { return re.MatchString(reader(ctx)) }, nil
			}
		}

		// Look ahead for a comparison operator
		next := p.peek()
		switch next.kind {
		case tkEq, tkNe, tkLt, tkLe, tkGt, tkGe:
			op := p.consume()
			rhs := p.peek()

			// RHS is a string literal
			if rhs.kind == tkStr {
				p.consume()
				rhsVal := rhs.val
				reader := p.resolveStringReader(name)
				switch op.kind {
				case tkEq:
					return func(ctx *rctx.Context) bool { return reader(ctx) == rhsVal }, nil
				case tkNe:
					return func(ctx *rctx.Context) bool { return reader(ctx) != rhsVal }, nil
				default:
					return nil, fmt.Errorf("operator %q is not valid for string comparison", op.val)
				}
			}

			// RHS is null/nil/"" → empty check
			if rhs.kind == tkIdent && (rhs.val == "null" || rhs.val == "nil") {
				p.consume()
				// Resolve as byte-slot emptiness
				idx, hasSlot := p.slotMap[name]
				switch op.kind {
				case tkEq:
					if hasSlot {
						return func(ctx *rctx.Context) bool {
							return idx >= len(ctx.ByteSlots) || len(ctx.ByteSlots[idx]) == 0
						}, nil
					}
					return func(_ *rctx.Context) bool { return true }, nil
				case tkNe:
					if hasSlot {
						return func(ctx *rctx.Context) bool {
							return idx < len(ctx.ByteSlots) && len(ctx.ByteSlots[idx]) > 0
						}, nil
					}
					return func(_ *rctx.Context) bool { return false }, nil
				default:
					return nil, fmt.Errorf("operator %q is not valid for null comparison", op.val)
				}
			}

			// RHS is a number
			if rhs.kind == tkNum {
				p.consume()
				rhsInt, err := strconv.ParseInt(rhs.val, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid number %q: %w", rhs.val, err)
				}
				reader := p.resolveIntReader(name)
				switch op.kind {
				case tkEq:
					return func(ctx *rctx.Context) bool { return reader(ctx) == rhsInt }, nil
				case tkNe:
					return func(ctx *rctx.Context) bool { return reader(ctx) != rhsInt }, nil
				case tkLt:
					return func(ctx *rctx.Context) bool { return reader(ctx) < rhsInt }, nil
				case tkLe:
					return func(ctx *rctx.Context) bool { return reader(ctx) <= rhsInt }, nil
				case tkGt:
					return func(ctx *rctx.Context) bool { return reader(ctx) > rhsInt }, nil
				case tkGe:
					return func(ctx *rctx.Context) bool { return reader(ctx) >= rhsInt }, nil
				}
			}

			return nil, fmt.Errorf("unsupported RHS token %q after operator %q", rhs.val, op.val)
		}

		// No comparison operator: bare identifier — truthiness check.
		// Special names first.
		switch name {
		case "status":
			return func(ctx *rctx.Context) bool { return ctx.ResponseStatus != 0 }, nil
		case "method":
			return func(ctx *rctx.Context) bool { return len(ctx.Method) > 0 }, nil
		case "path":
			return func(ctx *rctx.Context) bool { return len(ctx.Path) > 0 }, nil
		}

		// Check byte slot
		if idx, ok := p.slotMap[name]; ok {
			return func(ctx *rctx.Context) bool {
				return idx < len(ctx.ByteSlots) && len(ctx.ByteSlots[idx]) > 0
			}, nil
		}

		// Check bool slot by name — the slotMap for bool slots reuses the same
		// namespace (getBoolSlot calls getSlot). If not in slotMap, try BoolSlots
		// by treating the name as a numeric index (not common; skip for now).
		// For unknown identifiers: default to false (safe for conditions).
		return func(_ *rctx.Context) bool { return false }, nil
	}

	return nil, fmt.Errorf("unexpected token %q in condition expression", t.val)
}

// CompileCondition parses expr into a pre-compiled ConditionFunc closure.
//
// slotMap maps slot names to their index in ctx.ByteSlots.
//
// Supported syntax:
//   - Bare slot name: truthiness of ByteSlots[slot]
//   - Special names: "status", "method", "path"
//   - Comparisons: slot == "val", status >= 400, slot == null
//   - String predicates: contains(slot, "x"), startsWith(slot, "p"), endsWith(slot, "s"), matches(slot, "re")
//   - Logic: &&, ||, !, ( )
func CompileCondition(expr string, slotMap map[string]int) (ConditionFunc, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return func(_ *rctx.Context) bool { return false }, nil
	}

	tokens, err := tokenize(expr)
	if err != nil {
		return nil, fmt.Errorf("CompileCondition: tokenize %q: %w", expr, err)
	}

	p := &parser{tokens: tokens, slotMap: slotMap}
	fn, err := p.parseExpr()
	if err != nil {
		return nil, fmt.Errorf("CompileCondition: parse %q: %w", expr, err)
	}
	if p.peek().kind != tkEOF {
		return nil, fmt.Errorf("CompileCondition: unexpected trailing tokens in %q", expr)
	}
	return fn, nil
}
