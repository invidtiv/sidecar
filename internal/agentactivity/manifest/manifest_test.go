package manifest

import (
	"fmt"
	"strings"
	"testing"
)

func mustParse(t *testing.T, body string) *Manifest {
	t.Helper()
	m, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

const minimalManifest = `
id = "test"
version = "2026.01.01.1"
min_engine_version = 1
updated_at = "2026-01-01T00:00:00Z"
aliases = ["test-cli"]

[[rules]]
id = "r1"
state = "working"
priority = 10
contains = ["esc to interrupt"]
`

func TestParseAcceptsTheFullTopLevelShape(t *testing.T) {
	m := mustParse(t, minimalManifest)
	if m.ID != "test" || m.Version != "2026.01.01.1" || m.UpdatedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("unexpected header: %+v", m)
	}
	if m.MinEngineVersion == nil || *m.MinEngineVersion != 1 {
		t.Fatalf("min_engine_version = %v, want 1", m.MinEngineVersion)
	}
	if len(m.Aliases) != 1 || m.Aliases[0] != "test-cli" {
		t.Fatalf("aliases = %v", m.Aliases)
	}
	if len(m.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(m.Rules))
	}
	rule := m.Rules[0]
	if rule.Region != nil {
		t.Fatalf("region key was absent but decoded as %q", *rule.Region)
	}
	if rule.RegionName() != DefaultRegion {
		t.Fatalf("region = %q, want the whole_recent default", rule.RegionName())
	}
	if rule.RegionSpec().Kind != RegionWholeRecent {
		t.Fatalf("parsed region = %v", rule.RegionSpec())
	}
	if rule.EffectiveState() != StateWorking {
		t.Fatalf("state = %q", rule.EffectiveState())
	}
	if err := Validate(m); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	cases := map[string]string{
		"unknown top-level key": minimalManifest + "\nnickname = \"nope\"\n",
		"unknown rule key": `
id = "test"
[[rules]]
id = "r1"
contains = ["x"]
weight = 3
`,
		"unknown gate key": `
id = "test"
[[rules]]
id = "r1"
any = [{ contains = ["x"], weight = 3 }]
`,
		"unknown nested not key": `
id = "test"
[[rules]]
id = "r1"
contains = ["x"]
not = [{ contains = ["y"], mode = "strict" }]
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(body)); err == nil {
				t.Fatalf("Parse accepted a manifest with an unknown key")
			}
		})
	}
}

func TestParseRejectsUnknownState(t *testing.T) {
	body := `
id = "test"
[[rules]]
id = "r1"
state = "busy"
contains = ["x"]
`
	_, err := Parse([]byte(body))
	if err == nil {
		t.Fatal("Parse accepted state = \"busy\"")
	}
	if !strings.Contains(err.Error(), "unknown state") {
		t.Fatalf("error does not name the state: %v", err)
	}
}

func TestParseKeepsAbsentStateDistinctFromUnknown(t *testing.T) {
	absent := mustParse(t, `
id = "test"
[[rules]]
id = "r1"
contains = ["x"]
`)
	if absent.Rules[0].State != nil {
		t.Fatal("absent state decoded as a value")
	}
	if got := absent.Rules[0].EffectiveState(); got != StateUnknown {
		t.Fatalf("EffectiveState = %q, want unknown", got)
	}

	explicit := mustParse(t, `
id = "test"
[[rules]]
id = "r1"
state = "unknown"
contains = ["x"]
`)
	if explicit.Rules[0].State == nil || *explicit.Rules[0].State != StateUnknown {
		t.Fatal("explicit unknown state did not decode")
	}
}

func TestParseReadsNestedGates(t *testing.T) {
	m := mustParse(t, `
id = "test"
[[rules]]
id = "r1"
state = "blocked"
region = "bottom_non_empty_lines(6)"
visible_blocker = true
any = [
  { contains = ["run this command?"], any = [{ contains = ["(y)"] }] },
  { line_regex = ['(?i)^\s*allow'] },
]
not = [
  { contains = ["esc to cancel"] },
]
`)
	rule := m.Rules[0]
	if !rule.VisibleBlocker || rule.VisibleIdle || rule.VisibleWorking {
		t.Fatalf("visible flags = %+v", rule)
	}
	if rule.RegionSpec().Kind != RegionBottomNonEmptyLines || rule.RegionSpec().Count != 6 {
		t.Fatalf("region = %v", rule.RegionSpec())
	}
	if len(rule.Any) != 2 || len(rule.Any[0].Any) != 1 || len(rule.Not) != 1 {
		t.Fatalf("gate tree did not decode: %+v", rule)
	}
	if err := Validate(m); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsSkipStateUpdateMisuse(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"wrong state": {`
id = "test"
[[rules]]
id = "transcript"
state = "idle"
skip_state_update = true
contains = ["x"]
`, `rule transcript uses skip_state_update without state = "unknown"`},
		"absent state": {`
id = "test"
[[rules]]
id = "transcript"
skip_state_update = true
contains = ["x"]
`, `rule transcript uses skip_state_update without state = "unknown"`},
		"visible flag": {`
id = "test"
[[rules]]
id = "transcript"
state = "unknown"
skip_state_update = true
visible_idle = true
contains = ["x"]
`, "rule transcript uses skip_state_update with visible state evidence"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := mustParse(t, tc.body)
			err := Validate(m)
			if err == nil {
				t.Fatal("Validate accepted skip_state_update misuse")
			}
			if err.Error() != tc.want {
				t.Fatalf("error = %q, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateAcceptsSkipStateUpdateWithUnknownState(t *testing.T) {
	m := mustParse(t, `
id = "test"
[[rules]]
id = "transcript"
state = "unknown"
skip_state_update = true
contains = ["conversation history"]
`)
	if err := Validate(m); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRequiresAtLeastOneRule(t *testing.T) {
	m := mustParse(t, `id = "test"`)
	err := Validate(m)
	if err == nil || err.Error() != "manifest must contain at least one rule" {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRejectsEmptyRuleID(t *testing.T) {
	m := mustParse(t, `
id = "test"
[[rules]]
id = "   "
contains = ["x"]
`)
	err := Validate(m)
	if err == nil || err.Error() != "manifest rule id must not be empty" {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRequiresAPositiveMatcher(t *testing.T) {
	m := mustParse(t, `
id = "test"
[[rules]]
id = "r1"
not = [{ contains = ["x"] }]
`)
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "rule must contain a positive matcher") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRejectsAnEmptyNotGate(t *testing.T) {
	m := mustParse(t, `
id = "test"
[[rules]]
id = "r1"
contains = ["x"]
not = [{}]
`)
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "rule contains an empty not gate") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRejectsAnInvalidRegex(t *testing.T) {
	m := mustParse(t, `
id = "test"
[[rules]]
id = "r1"
regex = ["(unclosed"]
`)
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "rule contains invalid regex pattern") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateAllowIncompatibleRegexSkipsCompilation(t *testing.T) {
	body := `
id = "test"
[[rules]]
id = "r1"
line_regex = ['^\s*\p{Alphabetic}+']
`
	m := mustParse(t, body)
	if err := Validate(m); err == nil {
		t.Fatal("Validate accepted a pattern RE2 cannot compile")
	}
	if err := ValidateWith(m, ValidateOptions{AllowIncompatibleRegex: true}); err != nil {
		t.Fatalf("ValidateWith(AllowIncompatibleRegex): %v", err)
	}
	incompatible := m.RegexIncompatibilities()
	if len(incompatible) != 1 || incompatible[0].RuleID != "r1" || incompatible[0].Field != "line_regex" {
		t.Fatalf("RegexIncompatibilities = %+v", incompatible)
	}
}

// --- limits: each rejects at limit+1 and accepts at the limit -----------------

func manifestWithRules(n int) string {
	var b strings.Builder
	b.WriteString("id = \"test\"\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "\n[[rules]]\nid = \"r%d\"\ncontains = [\"x\"]\n", i)
	}
	return b.String()
}

func TestRuleCountLimit(t *testing.T) {
	if err := Validate(mustParse(t, manifestWithRules(MaxRulesPerManifest))); err != nil {
		t.Fatalf("%d rules rejected: %v", MaxRulesPerManifest, err)
	}
	err := Validate(mustParse(t, manifestWithRules(MaxRulesPerManifest+1)))
	want := fmt.Sprintf("manifest contains %d rules, max is %d", MaxRulesPerManifest+1, MaxRulesPerManifest)
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// nestedAllGate builds `depth` levels of nested `all` gates below the rule.
func nestedAllGate(depth int) string {
	inner := `{ contains = ["x"] }`
	for i := 0; i < depth; i++ {
		inner = "{ all = [" + inner + "] }"
	}
	return "id = \"test\"\n\n[[rules]]\nid = \"r1\"\nall = [" + inner + "]\n"
}

func TestGateDepthLimit(t *testing.T) {
	// The rule's own gate is depth 0, so `MaxGateDepth` further levels of
	// nesting are the deepest Herdr accepts.
	if err := Validate(mustParse(t, nestedAllGate(MaxGateDepth-1))); err != nil {
		t.Fatalf("depth %d rejected: %v", MaxGateDepth, err)
	}
	err := Validate(mustParse(t, nestedAllGate(MaxGateDepth)))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("all gate exceeds max gate depth %d", MaxGateDepth)) {
		t.Fatalf("error = %v", err)
	}
}

// manifestWithGates builds exactly `total` gates while staying inside the rule
// limit: every rule is one gate, and the remainder is spread over nested `any`
// entries.
func manifestWithGates(total int) string {
	rules := min(total, MaxRulesPerManifest)
	extra := total - rules
	per := extra / rules
	remainder := extra % rules
	var b strings.Builder
	b.WriteString("id = \"test\"\n")
	for i := 0; i < rules; i++ {
		nested := per
		if i < remainder {
			nested++
		}
		fmt.Fprintf(&b, "\n[[rules]]\nid = \"r%d\"\ncontains = [\"x\"]\n", i)
		if nested > 0 {
			b.WriteString("any = [")
			for j := 0; j < nested; j++ {
				b.WriteString(`{ contains = ["y"] },`)
			}
			b.WriteString("]\n")
		}
	}
	return b.String()
}

func TestTotalGateLimit(t *testing.T) {
	if err := Validate(mustParse(t, manifestWithGates(MaxTotalGates))); err != nil {
		t.Fatalf("%d gates rejected: %v", MaxTotalGates, err)
	}
	err := Validate(mustParse(t, manifestWithGates(MaxTotalGates+1)))
	want := fmt.Sprintf("manifest exceeds max gate count %d", MaxTotalGates)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func manifestWithMatchersInOneGate(n int) string {
	var b strings.Builder
	b.WriteString("id = \"test\"\n\n[[rules]]\nid = \"r1\"\ncontains = [")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "\"m%d\",", i)
	}
	b.WriteString("]\n")
	return b.String()
}

func TestMatchersPerGateLimit(t *testing.T) {
	if err := Validate(mustParse(t, manifestWithMatchersInOneGate(MaxMatchersPerGate))); err != nil {
		t.Fatalf("%d matchers rejected: %v", MaxMatchersPerGate, err)
	}
	err := Validate(mustParse(t, manifestWithMatchersInOneGate(MaxMatchersPerGate+1)))
	want := fmt.Sprintf("rule has %d direct matchers, max is %d", MaxMatchersPerGate+1, MaxMatchersPerGate)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// manifestWithMatchers builds exactly `total` matchers spread over the rule
// limit, so the matcher ceiling is what rejects and not the rule ceiling.
func manifestWithMatchers(total int) string {
	rules := min(total, MaxRulesPerManifest)
	per := total / rules
	remainder := total % rules
	var b strings.Builder
	b.WriteString("id = \"test\"\n")
	for i := 0; i < rules; i++ {
		n := per
		if i < remainder {
			n++
		}
		fmt.Fprintf(&b, "\n[[rules]]\nid = \"r%d\"\ncontains = [", i)
		for j := 0; j < n; j++ {
			fmt.Fprintf(&b, "\"m%d\",", j)
		}
		b.WriteString("]\n")
	}
	return b.String()
}

func TestTotalMatcherLimit(t *testing.T) {
	if err := Validate(mustParse(t, manifestWithMatchers(MaxTotalMatchers))); err != nil {
		t.Fatalf("%d matchers rejected: %v", MaxTotalMatchers, err)
	}
	err := Validate(mustParse(t, manifestWithMatchers(MaxTotalMatchers+1)))
	want := fmt.Sprintf("manifest exceeds max matcher count %d", MaxTotalMatchers)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func manifestWithMatcherOfLength(n int) string {
	// Multi-byte runes prove the limit counts characters, not bytes, the way
	// Rust's value.chars().count() does.
	return "id = \"test\"\n\n[[rules]]\nid = \"r1\"\ncontains = [\"" + strings.Repeat("é", n) + "\"]\n"
}

func TestMatcherLengthLimit(t *testing.T) {
	if err := Validate(mustParse(t, manifestWithMatcherOfLength(MaxMatcherChars))); err != nil {
		t.Fatalf("%d-char matcher rejected: %v", MaxMatcherChars, err)
	}
	err := Validate(mustParse(t, manifestWithMatcherOfLength(MaxMatcherChars+1)))
	want := fmt.Sprintf("rule matcher exceeds max length %d", MaxMatcherChars)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// --- distribution checks ------------------------------------------------------

func TestValidateDistributionRequiresVersionAndEngine(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"no version": {`
id = "test"
min_engine_version = 1
[[rules]]
id = "r1"
contains = ["x"]
`, "remote manifest must include version"},
		"no min_engine_version": {`
id = "test"
version = "2026.01.01.1"
[[rules]]
id = "r1"
contains = ["x"]
`, "remote manifest must include min_engine_version"},
		"non-numeric version": {`
id = "test"
version = "2026.01.01-beta"
min_engine_version = 1
[[rules]]
id = "r1"
contains = ["x"]
`, "must be dotted numeric"},
		"empty id": {`
id = "  "
version = "1"
min_engine_version = 1
[[rules]]
id = "r1"
contains = ["x"]
`, "id must be a non-empty string"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRemote([]byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestValidateDistributionRejectsANewerEngine(t *testing.T) {
	body := fmt.Sprintf(`
id = "test"
version = "2026.01.01.1"
min_engine_version = %d
[[rules]]
id = "r1"
contains = ["x"]
`, EngineVersion+1)
	_, err := ParseRemote([]byte(body))
	if err == nil {
		t.Fatal("ParseRemote accepted a manifest needing a newer engine")
	}
	tooNew, ok := AsEngineTooNew(err)
	if !ok {
		t.Fatalf("error is not an *EngineTooNewError: %T %v", err, err)
	}
	if tooNew.Required != EngineVersion+1 || tooNew.Engine != EngineVersion {
		t.Fatalf("EngineTooNewError = %+v", tooNew)
	}
}

func TestValidateRejectsTopNonEmptyLinesBelowEngineThree(t *testing.T) {
	body := `
id = "test"
version = "2026.01.01.1"
min_engine_version = 2
[[rules]]
id = "banner"
region = "top_non_empty_lines(4)"
contains = ["x"]
`
	m := mustParse(t, body)
	err := Validate(m)
	want := fmt.Sprintf("rule banner uses top_non_empty_lines but min_engine_version is below %d", TopNonEmptyLinesEngineVersion)
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}

	ok := strings.Replace(body, "min_engine_version = 2", "min_engine_version = 3", 1)
	if _, err := ParseRemote([]byte(ok)); err != nil {
		t.Fatalf("engine 3 manifest rejected: %v", err)
	}
}

func TestValidateDistributionRejectsAZeroLineCount(t *testing.T) {
	// The engine's own region parser accepts bottom_lines(0); the published
	// form does not, and a vendored file must satisfy both.
	body := `
id = "test"
version = "2026.01.01.1"
min_engine_version = 1
[[rules]]
id = "r1"
region = "bottom_lines(0)"
contains = ["x"]
`
	m := mustParse(t, body)
	if err := Validate(m); err != nil {
		t.Fatalf("engine validation rejected bottom_lines(0): %v", err)
	}
	if err := ValidateDistribution(m); err == nil {
		t.Fatal("ValidateDistribution accepted bottom_lines(0)")
	}
}

// --- version comparison --------------------------------------------------------

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"2026.07.16.1", "2026.07.16.2", -1},
		{"2026.07.16.2", "2026.07.16.1", 1},
		{"2026.07.16.1", "2026.07.16.1", 0},
		{"2026.8.1", "2026.10.1", -1},
		{"1.2", "1.2.0", 0},
		{"1.2.0", "1.2", 0},
		{"1.2", "1.2.1", -1},
		{"1.2.1", "1.2", 1},
		{"1", "1.0.0.0", 0},
		{"2026.08.29.1", "2026.08.29", 1},
		{"10", "9", 1},
		{"2026.06.10.1", "2026.06.10.10", -1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.left, tc.right); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestValidateVersion(t *testing.T) {
	valid := []string{"1", "2026.08.29.1", "0", "1.0.0"}
	for _, v := range valid {
		if _, err := ValidateVersion(v); err != nil {
			t.Errorf("ValidateVersion(%q) = %v, want nil", v, err)
		}
	}
	invalid := []string{"", "   ", "1.", ".1", "1..2", "1.2.3-beta", "v1.2", "1.2.x"}
	for _, v := range invalid {
		if _, err := ValidateVersion(v); err == nil {
			t.Errorf("ValidateVersion(%q) = nil, want an error", v)
		}
	}
}

// --- regions -------------------------------------------------------------------

func TestParseRegionTable(t *testing.T) {
	valid := []struct {
		spec  string
		kind  RegionKind
		count int
	}{
		{"whole_recent", RegionWholeRecent, 0},
		{"whole_recent_without_current_prompt_marker", RegionWholeRecentWithoutCurrentPromptMarker, 0},
		{"after_last_prompt_marker", RegionAfterLastPromptMarker, 0},
		{"before_current_prompt_marker", RegionBeforeCurrentPromptMarker, 0},
		{"current_prompt_block_marker", RegionCurrentPromptBlockMarker, 0},
		{"after_current_prompt_block_marker", RegionAfterCurrentPromptBlockMarker, 0},
		{"prompt_box_body", RegionPromptBoxBody, 0},
		{"above_prompt_box", RegionAbovePromptBox, 0},
		{"last_non_empty_above_prompt_box", RegionLastNonEmptyAbovePromptBox, 0},
		{"after_last_horizontal_rule", RegionAfterLastHorizontalRule, 0},
		{"osc_title", RegionOSCTitle, 0},
		{"osc_progress", RegionOSCProgress, 0},
		{"bottom_lines(3)", RegionBottomLines, 3},
		{"bottom_non_empty_lines(30)", RegionBottomNonEmptyLines, 30},
		{"top_non_empty_lines(4)", RegionTopNonEmptyLines, 4},
		{"  bottom_lines(12)  ", RegionBottomLines, 12},
		// Herdr's engine, unlike its distribution checker, accepts these.
		{"bottom_lines(0)", RegionBottomLines, 0},
		{"bottom_non_empty_lines(007)", RegionBottomNonEmptyLines, 7},
		{"top_non_empty_lines(65535)", RegionTopNonEmptyLines, 65535},
	}
	if len(valid) < 15 {
		t.Fatalf("region table covers %d cases, want all fifteen kinds", len(valid))
	}
	for _, tc := range valid {
		region, err := ParseRegion(tc.spec)
		if err != nil {
			t.Errorf("ParseRegion(%q) = %v", tc.spec, err)
			continue
		}
		if region.Kind != tc.kind || region.Count != tc.count {
			t.Errorf("ParseRegion(%q) = {%v %d}, want {%v %d}", tc.spec, region.Kind, region.Count, tc.kind, tc.count)
		}
	}

	invalid := []string{
		"",
		"whole",
		"whole_recent()",
		"bottom_lines",
		"bottom_lines()",
		"bottom_lines(-1)",
		"bottom_lines(three)",
		"bottom_lines(3",
		"bottom_lines 3)",
		"bottom_non_empty_lines(1.5)",
		"top_non_empty_lines(0)",     // leading zero rejected outright
		"top_non_empty_lines(04)",    // leading zero rejected outright
		"top_non_empty_lines(65536)", // above MAX_TOP_REGION_LINE_COUNT
		"top_non_empty_lines(+4)",
		"osc_titles",
		"OSC_TITLE",
	}
	for _, spec := range invalid {
		if region, err := ParseRegion(spec); err == nil {
			t.Errorf("ParseRegion(%q) = %v, want an error", spec, region)
		}
	}
}

func TestRegionStringRoundTrip(t *testing.T) {
	for _, spec := range []string{"whole_recent", "osc_progress", "bottom_lines(3)", "top_non_empty_lines(2)"} {
		region, err := ParseRegion(spec)
		if err != nil {
			t.Fatalf("ParseRegion(%q): %v", spec, err)
		}
		if got := region.String(); got != spec {
			t.Errorf("Region(%q).String() = %q", spec, got)
		}
	}
}

func TestRegionUsesScreen(t *testing.T) {
	for _, spec := range []string{"osc_title", "osc_progress"} {
		region, _ := ParseRegion(spec)
		if region.UsesScreen() {
			t.Errorf("%q reported as a screen region", spec)
		}
	}
	region, _ := ParseRegion("whole_recent")
	if !region.UsesScreen() {
		t.Error("whole_recent reported as a non-screen region")
	}
}

// --- regex dialect ---------------------------------------------------------------

func TestTranslateRustRegex(t *testing.T) {
	cases := []struct{ in, want string }{
		{`^\s*[\u2800-\u28FF]`, `^\s*[\x{2800}-\x{28FF}]`},
		{`^⚠[\u{fe0e}\u{fe0f}]?(?:\s|$)`, `^⚠[\x{fe0e}\x{fe0f}]?(?:\s|$)`},
		{`^[\x{2800}-\x{28FF}] `, `^[\x{2800}-\x{28FF}] `},
		{`(?i)plain`, `(?i)plain`},
		{`\\u2800`, `\\u2800`}, // an escaped backslash, not a code point escape
		{`\u28`, `\u28`},       // too short to be a bare escape; left alone to fail loudly
	}
	for _, tc := range cases {
		if got := TranslateRustRegex(tc.in); got != tc.want {
			t.Errorf("TranslateRustRegex(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCompileRegexAcceptsRustCodePointEscapes(t *testing.T) {
	re, err := CompileRegex(`^[\u2800-\u28FF]+$`)
	if err != nil {
		t.Fatalf("CompileRegex: %v", err)
	}
	if !re.MatchString("⠋⠙") {
		t.Error("translated braille class did not match braille")
	}
	if re.MatchString("ab") {
		t.Error("translated braille class matched ASCII")
	}
}
