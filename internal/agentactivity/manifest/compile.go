package manifest

import (
	"fmt"
	"regexp"
	"strings"
)

// RegexIncompatibleNote is the evidence note an Explain carries for a rule the
// engine could not compile. Four upstream patterns use Rust's `\p{Alphabetic}`
// binary property, which RE2 has no spelling for; the vendored file keeps them
// verbatim and the matching Sidecar overlay carries an RE2 rewrite under the
// same rule id. Until an overlay replaces such a rule it never matches, and the
// note is how `explain` says so instead of the rule silently reading as a
// well-considered no-match. See docs/reference/herdr-detection-parity.md
// ("Regex compatibility").
const RegexIncompatibleNote = "regex_incompatible"

// Compiled is a manifest with every pattern compiled and every `contains`
// needle folded, ready to evaluate. Compilation happens once per manifest;
// evaluation is state-free and safe for concurrent use.
type Compiled struct {
	// Manifest is the merged manifest this was built from. Callers read its
	// ID and Version for explain records; they must not mutate it.
	Manifest *Manifest
	// Source is a human-readable description of where the manifest came from,
	// in Herdr's `manifest_source` style. The loader fills it in.
	Source string
	// OverlayApplied records that a Sidecar overlay was merged in.
	OverlayApplied bool

	rules []compiledRule
}

type compiledRule struct {
	rule *Rule
	gate compiledGate
	// incompatible names the first pattern RE2 refused, or "" when the rule
	// compiled. An incompatible rule is evaluated as a no-match with the
	// RegexIncompatibleNote in its evidence, never as a load failure: dropping
	// the whole manifest because one spinner pattern uses a Unicode binary
	// property would take twenty working rules down with it.
	incompatible string
}

type compiledGate struct {
	all       []compiledGate
	any       []compiledGate
	not       []compiledGate
	contains  []string
	regex     []*regexp.Regexp
	lineRegex []*regexp.Regexp
}

// Compile prepares a manifest for evaluation. It returns an error only for a
// manifest it cannot make sense of at all (a nil manifest, or a region spec the
// validator would have rejected). A pattern RE2 cannot express is recorded on
// the rule instead, per RegexIncompatibleNote.
func Compile(m *Manifest) (*Compiled, error) {
	if m == nil {
		return nil, fmt.Errorf("manifest is nil")
	}
	c := &Compiled{Manifest: m, rules: make([]compiledRule, len(m.Rules))}
	for i := range m.Rules {
		rule := &m.Rules[i]
		if rule.region.Spec == "" {
			region, err := ParseRegion(rule.RegionName())
			if err != nil {
				return nil, fmt.Errorf("rule %s uses invalid region %q", rule.ID, rule.RegionName())
			}
			rule.region = region
		}
		gateSpec := rule.RootGate()
		gate, incompatible := compileGate(&gateSpec)
		c.rules[i] = compiledRule{rule: rule, gate: gate, incompatible: incompatible}
	}
	return c, nil
}

// compileGate ports Herdr's compile_gate (manifest.rs:1175): needles are folded
// to lower case once, patterns are compiled once. The one deviation is the
// return of an incompatibility rather than an error, for the reason on
// compiledRule.incompatible.
func compileGate(gate *Gate) (compiledGate, string) {
	var out compiledGate
	var incompatible string
	note := func(field, pattern string, err error) {
		if incompatible == "" {
			incompatible = fmt.Sprintf("%s %q: %v", field, pattern, err)
		}
	}
	for _, needle := range gate.Contains {
		out.contains = append(out.contains, strings.ToLower(needle))
	}
	for _, pattern := range gate.Regex {
		re, err := CompileRegex(pattern)
		if err != nil {
			note("regex", pattern, err)
			continue
		}
		out.regex = append(out.regex, re)
	}
	for _, pattern := range gate.LineRegex {
		re, err := CompileRegex(pattern)
		if err != nil {
			note("line_regex", pattern, err)
			continue
		}
		out.lineRegex = append(out.lineRegex, re)
	}
	for i := range gate.All {
		nested, nestedErr := compileGate(&gate.All[i])
		if nestedErr != "" && incompatible == "" {
			incompatible = nestedErr
		}
		out.all = append(out.all, nested)
	}
	for i := range gate.Any {
		nested, nestedErr := compileGate(&gate.Any[i])
		if nestedErr != "" && incompatible == "" {
			incompatible = nestedErr
		}
		out.any = append(out.any, nested)
	}
	for i := range gate.Not {
		nested, nestedErr := compileGate(&gate.Not[i])
		if nestedErr != "" && incompatible == "" {
			incompatible = nestedErr
		}
		out.not = append(out.not, nested)
	}
	return out, incompatible
}

// matches ports Herdr's compiled_gate_matches (manifest.rs:1237). The order of
// the six checks is upstream's and is load-bearing only for cost, not for the
// result: every direct matcher must hold, every nested `all` must hold, at
// least one nested `any` must hold when any are present, and no nested `not`
// may hold.
func (g *compiledGate) matches(text, lower string) bool {
	for _, needle := range g.contains {
		if !strings.Contains(lower, needle) {
			return false
		}
	}
	for _, re := range g.regex {
		if !re.MatchString(text) {
			return false
		}
	}
	for _, re := range g.lineRegex {
		if !matchesAnyLine(re, text) {
			return false
		}
	}
	for i := range g.all {
		if !g.all[i].matches(text, lower) {
			return false
		}
	}
	if len(g.any) > 0 {
		satisfied := false
		for i := range g.any {
			if g.any[i].matches(text, lower) {
				satisfied = true
				break
			}
		}
		if !satisfied {
			return false
		}
	}
	for i := range g.not {
		if g.not[i].matches(text, lower) {
			return false
		}
	}
	return true
}

// matchesAnyLine is `text.lines().any(|line| regex.is_match(line))`. It walks
// the text without allocating a line slice, which matters because line_regex is
// the most common matcher kind in the corpus and the workspace evaluates every
// visible pane several times a second.
func matchesAnyLine(re *regexp.Regexp, text string) bool {
	for len(text) > 0 {
		line := text
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			line, text = text[:i], text[i+1:]
		} else {
			text = ""
		}
		if re.MatchString(strings.TrimSuffix(line, "\r")) {
			return true
		}
	}
	return false
}

// usesContains reports whether any matcher in the tree needs the folded text,
// so an observation only pays for the fold when a rule will read it.
func (g *compiledGate) usesContains() bool {
	if len(g.contains) > 0 {
		return true
	}
	for _, group := range [][]compiledGate{g.all, g.any, g.not} {
		for i := range group {
			if group[i].usesContains() {
				return true
			}
		}
	}
	return false
}
