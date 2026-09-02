package manifest

import (
	"fmt"
	"regexp"
	"strings"
)

// Herdr compiles manifest patterns with Rust's `regex` crate. Go compiles them
// with `regexp` (RE2). The two are the same family — linear time, no lookaround,
// no backreferences — and almost all of the syntax the upstream manifests use is
// spelled identically. Two things are not:
//
//  1. Rust accepts `\uHHHH` and `\u{H...}` as code-point escapes. RE2 spells the
//     same thing `\x{H...}` and rejects `\u` outright. TranslateRustRegex
//     rewrites those escapes. The rewrite is purely syntactic: `⠀` and
//     `\x{2800}` denote the same single code point, so nothing about what the
//     pattern matches changes.
//
//  2. Rust accepts Unicode *binary properties* by name, such as
//     `\p{Alphabetic}`. RE2 accepts only general categories (`\p{L}`) and
//     scripts (`\p{Greek}`). There is no lossless rewrite, so a pattern using one
//     is reported as an incompatibility rather than quietly changed. The overlay
//     mechanism is where a rewritten equivalent belongs.
//
// Nothing here tries to be a Rust regex parser. It translates what upstream
// actually uses and lets RE2 reject anything else, which is what surfaces a new
// incompatibility on a sync PR instead of in a user's pane.

// TranslateRustRegex rewrites Rust `regex` code-point escapes into their RE2
// spelling. It leaves every other byte untouched, including inside character
// classes, and it does not touch an escaped backslash's following `u`.
func TranslateRustRegex(pattern string) string {
	if !strings.Contains(pattern, `\u`) {
		return pattern
	}
	var b strings.Builder
	b.Grow(len(pattern))
	for i := 0; i < len(pattern); {
		c := pattern[i]
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(pattern) {
			b.WriteByte(c)
			i++
			continue
		}
		next := pattern[i+1]
		if next != 'u' {
			// Consume the escape pair whole, so `\\u2800` stays a literal
			// backslash followed by the characters u, 2, 8, 0, 0.
			b.WriteByte(c)
			b.WriteByte(next)
			i += 2
			continue
		}
		if hex, width, ok := rustCodePointEscape(pattern[i+2:]); ok {
			b.WriteString(`\x{`)
			b.WriteString(hex)
			b.WriteString(`}`)
			i += 2 + width
			continue
		}
		b.WriteByte(c)
		b.WriteByte(next)
		i += 2
	}
	return b.String()
}

// rustCodePointEscape reads the body of a `\u` escape: either `{H...}` with one
// to six hex digits, or exactly four bare hex digits.
func rustCodePointEscape(rest string) (hex string, width int, ok bool) {
	if strings.HasPrefix(rest, "{") {
		end := strings.IndexByte(rest, '}')
		if end < 2 || end > 8 {
			return "", 0, false
		}
		body := rest[1:end]
		if !isHex(body) {
			return "", 0, false
		}
		return body, end + 1, true
	}
	if len(rest) < 4 || !isHex(rest[:4]) {
		return "", 0, false
	}
	return rest[:4], 4, true
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// CompileRegex compiles a manifest pattern for the Go engine, translating the
// Rust code-point escapes first. An error means RE2 genuinely cannot express
// the pattern.
func CompileRegex(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(TranslateRustRegex(pattern))
}

// RegexIncompatibility is one upstream pattern RE2 cannot compile.
type RegexIncompatibility struct {
	RuleID     string `json:"rule_id"`
	Field      string `json:"field"`
	Pattern    string `json:"pattern"`
	Translated string `json:"translated"`
	Error      string `json:"error"`
}

func (r RegexIncompatibility) String() string {
	return fmt.Sprintf("rule %s %s %q: %s", r.RuleID, r.Field, r.Pattern, r.Error)
}

// RegexIncompatibilities returns every pattern in the manifest that RE2 cannot
// compile, in file order.
func (m *Manifest) RegexIncompatibilities() []RegexIncompatibility {
	var out []RegexIncompatibility
	for _, p := range m.Patterns() {
		translated := TranslateRustRegex(p.Pattern)
		if _, err := regexp.Compile(translated); err != nil {
			out = append(out, RegexIncompatibility{
				RuleID:     p.RuleID,
				Field:      p.Field,
				Pattern:    p.Pattern,
				Translated: translated,
				Error:      err.Error(),
			})
		}
	}
	return out
}
