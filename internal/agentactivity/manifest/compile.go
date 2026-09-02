package manifest

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// RegexIncompatibleNote is the evidence note an Explain carries for a rule the
// engine could not compile. Four upstream patterns use Rust's `\p{Alphabetic}`
// binary property, which RE2 has no spelling for; the vendored file keeps them
// verbatim and the matching Sidecar overlay carries an RE2 rewrite under the
// same rule id. Until an overlay replaces such a rule it never matches, and the
// note is how `explain` says so instead of the rule silently reading as a
// well-considered no-match. See docs/reference/herdr-detection-parity.md
// ("Regex compatibility").
//
// "Dead" is the accurate word and it is deliberately not "unmatched": the whole
// rule is skipped, not just the pattern. Where the pattern sits inside a `not`
// gate that is the opposite of Herdr's behaviour — upstream would evaluate the
// pattern, fail to match it, and so satisfy the `not`, usually firing the rule.
// Skipping is the safer direction (a rule that cannot be evaluated asserts
// nothing) and it is unreachable today: all four incompatible patterns are
// positive `line_regex` matchers, and each has an overlay carrying an RE2
// rewrite. TestOverlaysMakeEveryRuleCompilable is what keeps it unreachable.
const RegexIncompatibleNote = "regex_incompatible: rule is dead"

// foldLower is Rust's `str::to_lowercase()`, which Herdr's compile_gate and
// compiled_rule_matches both use for `contains`.
//
// It is not `strings.ToLower`. Go's folds each rune independently with the
// simple mappings; Rust's applies the full Unicode lowercase algorithm,
// including SpecialCasing (U+0130 LATIN CAPITAL LETTER I WITH DOT ABOVE lowers
// to "i" + U+0307, two runes) and the Final_Sigma condition (a Σ ending a word
// lowers to ς, not σ). Verified against herdr 0.8.2: needle "istanbul" against
// a screen reading "İSTANBUL" matches here and not upstream under the simple
// fold, and needle "ΠΣ" against "ΠΣΒ" splits the other way.
//
// No needle in any vendored manifest diverges today, so this changes no
// verdict; it removes a class of divergence a future sync could introduce
// silently.
//
// A Caser is stateful and must not be shared between goroutines, so they are
// pooled rather than shared or rebuilt. Rebuilding one per call was measurable:
// this runs on the live poll path — once per `contains` region per pane per
// frame, every 200ms while a pane is active — and building a Caser allocates a
// transformer chain each time.
var lowerCasers = sync.Pool{New: func() any {
	caser := cases.Lower(language.Und)
	return &caser
}}

func foldLower(s string) string {
	caser := lowerCasers.Get().(*cases.Caser)
	defer lowerCasers.Put(caser)
	return caser.String(s)
}

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
		out.contains = append(out.contains, foldLower(needle))
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
