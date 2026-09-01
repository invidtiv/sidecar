package manifest

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Herdr's limits, from src/detect/manifest.rs. Reproduced by value, not by
// reference, because a manifest that passes here must be one Herdr's engine
// would also accept.
const (
	MaxRulesPerManifest = 128
	MaxGateDepth        = 8
	MaxTotalGates       = 512
	MaxMatchersPerGate  = 32
	MaxTotalMatchers    = 1024
	MaxMatcherChars     = 512
)

// complexity accumulates the manifest-wide counters Herdr's validator carries
// through the whole rule set rather than resetting per rule.
type complexity struct {
	totalGates    int
	totalMatchers int
	opts          ValidateOptions
}

// ValidateOptions tunes the one check that cannot be a faithful port, because
// the two engines do not share a regex dialect.
type ValidateOptions struct {
	// AllowIncompatibleRegex keeps a pattern that Rust's regex crate accepts
	// but RE2 cannot compile from failing validation. The sync tool sets it so
	// that vendoring is never blocked by a dialect gap upstream is entitled to
	// have; it then names every such pattern in its report, and an overlay
	// carries the RE2 rewrite. Collect them with Manifest.RegexIncompatibilities.
	AllowIncompatibleRegex bool
}

// Validate applies Herdr's validate_manifest to a decoded manifest: rule count,
// rule ids, skip_state_update coupling, region names, gate depth and counts,
// matcher counts and lengths, and regex compilation. Regexes are compiled with
// Go's regexp (RE2); Herdr compiles them with Rust's regex crate, which is the
// same family, so a pattern that fails here is a genuine incompatibility and
// worth reporting rather than silently skipping.
//
// Validate does not require `version` or `min_engine_version`; Herdr's engine
// treats both as optional for a bundled manifest. ValidateDistribution adds
// the requirements that apply to a published or vendored file.
func Validate(m *Manifest) error { return ValidateWith(m, ValidateOptions{}) }

// ValidateWith is Validate with the regex-dialect option applied.
func ValidateWith(m *Manifest, opts ValidateOptions) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	if len(m.Rules) == 0 {
		return fmt.Errorf("manifest must contain at least one rule")
	}
	if len(m.Rules) > MaxRulesPerManifest {
		return fmt.Errorf("manifest contains %d rules, max is %d", len(m.Rules), MaxRulesPerManifest)
	}
	// Herdr types version as Option<ManifestVersion>, so a malformed version
	// fails while deserializing and never reaches validate_manifest. Parse
	// carries that check; it is repeated here so a Manifest built in Go rather
	// than decoded from TOML is held to the same rule.
	if strings.TrimSpace(m.Version) != "" {
		if _, err := ValidateVersion(m.Version); err != nil {
			return err
		}
	}
	if m.MinEngineVersion != nil && (*m.MinEngineVersion < 0 || *m.MinEngineVersion > math.MaxUint32) {
		return fmt.Errorf("min_engine_version %d does not fit Herdr's u32 min_engine_version", *m.MinEngineVersion)
	}

	c := complexity{opts: opts}
	for i := range m.Rules {
		rule := &m.Rules[i]
		if strings.TrimSpace(rule.ID) == "" {
			return fmt.Errorf("manifest rule id must not be empty")
		}
		if rule.SkipStateUpdate {
			if rule.State == nil || *rule.State != StateUnknown {
				return fmt.Errorf("rule %s uses skip_state_update without state = \"unknown\"", rule.ID)
			}
			if rule.VisibleIdle || rule.VisibleBlocker || rule.VisibleWorking {
				return fmt.Errorf("rule %s uses skip_state_update with visible state evidence", rule.ID)
			}
		}
		region, err := ParseRegion(rule.RegionName())
		if err != nil {
			return fmt.Errorf("rule %s uses invalid region: %w", rule.ID, err)
		}
		rule.region = region
		if strings.HasPrefix(strings.TrimSpace(rule.RegionName()), "top_non_empty_lines(") &&
			m.MinEngineVersion != nil && *m.MinEngineVersion < TopNonEmptyLinesEngineVersion {
			return fmt.Errorf("rule %s uses top_non_empty_lines but min_engine_version is below %d",
				rule.ID, TopNonEmptyLinesEngineVersion)
		}
		gate := rule.RootGate()
		if err := validateGate(&gate, "rule", 0, &c); err != nil {
			return fmt.Errorf("rule %s has invalid matcher gates: %w", rule.ID, err)
		}
	}
	return nil
}

// ValidateDistribution adds the checks Herdr applies to a manifest that came
// from outside the binary: the requirements in parse_remote_manifest_for_agent
// plus the stricter ones in scripts/agent_detection_manifest_check.py.
//
// Note that duplicate rule ids are deliberately not rejected. Neither Herdr's
// validator nor its distribution checker rejects them: a duplicate id evaluates
// as two independent rules and only makes an explain record ambiguous. Rejecting
// them here would refuse a file Herdr accepts, which is the one thing a port
// must not do.
func ValidateDistribution(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("id must be a non-empty string")
	}
	if _, err := ValidateVersion(m.Version); err != nil {
		return fmt.Errorf("remote manifest must include version: %w", err)
	}
	if m.MinEngineVersion == nil {
		return fmt.Errorf("remote manifest must include min_engine_version")
	}
	if *m.MinEngineVersion > EngineVersion {
		return &EngineTooNewError{ManifestID: m.ID, Required: *m.MinEngineVersion, Engine: EngineVersion}
	}
	for i := range m.Rules {
		rule := &m.Rules[i]
		region := strings.TrimSpace(rule.RegionName())
		if err := validateDistributionRegion(region); err != nil {
			return fmt.Errorf("rule %s has invalid region %q: %w", rule.ID, region, err)
		}
	}
	return nil
}

// distributionRegionRE is the checker's REGION_RE: every parameterised region
// must carry a count matching [1-9][0-9]*, which is stricter than the engine.
var distributionRegionRE = regexp.MustCompile(
	`^(whole_recent|whole_recent_without_current_prompt_marker|after_last_prompt_marker|` +
		`before_current_prompt_marker|current_prompt_block_marker|after_current_prompt_block_marker|` +
		`prompt_box_body|above_prompt_box|last_non_empty_above_prompt_box|after_last_horizontal_rule|` +
		`osc_title|osc_progress|` +
		`bottom_lines\([1-9][0-9]*\)|bottom_non_empty_lines\([1-9][0-9]*\)|` +
		`top_non_empty_lines\([1-9][0-9]*\))$`)

func validateDistributionRegion(region string) error {
	if !distributionRegionRE.MatchString(region) {
		return fmt.Errorf("region is not a published region form")
	}
	parsed, err := ParseRegion(region)
	if err != nil {
		return err
	}
	if parsed.Kind == RegionTopNonEmptyLines && parsed.Count > MaxTopRegionLineCount {
		return fmt.Errorf("top_non_empty_lines count exceeds %d", MaxTopRegionLineCount)
	}
	return nil
}

// validateGate ports Herdr's validate_gate. context is the label Herdr uses in
// its error text: "rule" for a rule's root gate, "all gate" / "any gate" for
// nested positive gates.
func validateGate(gate *Gate, context string, depth int, c *complexity) error {
	if depth > MaxGateDepth {
		return fmt.Errorf("%s exceeds max gate depth %d", context, MaxGateDepth)
	}
	c.totalGates++
	if c.totalGates > MaxTotalGates {
		return fmt.Errorf("manifest exceeds max gate count %d", MaxTotalGates)
	}
	if err := validateMatcherLimits(gate, context, c); err != nil {
		return err
	}
	if !gateHasPositiveMatcher(gate) {
		return fmt.Errorf("%s must contain a positive matcher", context)
	}
	if err := validateRegexPatterns(gate.Regex, context, "regex", c); err != nil {
		return err
	}
	if err := validateRegexPatterns(gate.LineRegex, context, "line_regex", c); err != nil {
		return err
	}
	for i := range gate.All {
		if err := validateGate(&gate.All[i], "all gate", depth+1, c); err != nil {
			return err
		}
	}
	for i := range gate.Any {
		if err := validateGate(&gate.Any[i], "any gate", depth+1, c); err != nil {
			return err
		}
	}
	for i := range gate.Not {
		if !gateHasAnyMatcher(&gate.Not[i]) {
			return fmt.Errorf("%s contains an empty not gate", context)
		}
		if err := validateNotGate(&gate.Not[i], depth+1, c); err != nil {
			return err
		}
	}
	return nil
}

// validateNotGate ports Herdr's validate_not_gate. A negative gate needs a
// matcher but not a positive one, and its nested positive gates carry the
// "not all gate" / "not any gate" labels in error text.
func validateNotGate(gate *Gate, depth int, c *complexity) error {
	if depth > MaxGateDepth {
		return fmt.Errorf("not gate exceeds max gate depth %d", MaxGateDepth)
	}
	c.totalGates++
	if c.totalGates > MaxTotalGates {
		return fmt.Errorf("manifest exceeds max gate count %d", MaxTotalGates)
	}
	if err := validateMatcherLimits(gate, "not gate", c); err != nil {
		return err
	}
	if !gateHasAnyMatcher(gate) {
		return fmt.Errorf("not gate must contain a matcher")
	}
	if err := validateRegexPatterns(gate.Regex, "not gate", "regex", c); err != nil {
		return err
	}
	if err := validateRegexPatterns(gate.LineRegex, "not gate", "line_regex", c); err != nil {
		return err
	}
	for i := range gate.All {
		if err := validateGate(&gate.All[i], "not all gate", depth+1, c); err != nil {
			return err
		}
	}
	for i := range gate.Any {
		if err := validateGate(&gate.Any[i], "not any gate", depth+1, c); err != nil {
			return err
		}
	}
	for i := range gate.Not {
		if err := validateNotGate(&gate.Not[i], depth+1, c); err != nil {
			return err
		}
	}
	return nil
}

func validateMatcherLimits(gate *Gate, context string, c *complexity) error {
	matcherCount := len(gate.Contains) + len(gate.Regex) + len(gate.LineRegex)
	if matcherCount > MaxMatchersPerGate {
		return fmt.Errorf("%s has %d direct matchers, max is %d", context, matcherCount, MaxMatchersPerGate)
	}
	c.totalMatchers += matcherCount
	if c.totalMatchers > MaxTotalMatchers {
		return fmt.Errorf("manifest exceeds max matcher count %d", MaxTotalMatchers)
	}
	for _, group := range [][]string{gate.Contains, gate.Regex, gate.LineRegex} {
		for _, value := range group {
			// Herdr counts chars, not bytes: value.chars().count().
			if utf8.RuneCountInString(value) > MaxMatcherChars {
				return fmt.Errorf("%s matcher exceeds max length %d", context, MaxMatcherChars)
			}
		}
	}
	return nil
}

func validateRegexPatterns(patterns []string, context, field string, c *complexity) error {
	if c.opts.AllowIncompatibleRegex {
		return nil
	}
	for _, pattern := range patterns {
		if _, err := CompileRegex(pattern); err != nil {
			return fmt.Errorf("%s contains invalid %s pattern %q: %v", context, field, pattern, err)
		}
	}
	return nil
}

func gateHasPositiveMatcher(gate *Gate) bool {
	return len(gate.Contains) > 0 || len(gate.Regex) > 0 || len(gate.LineRegex) > 0 ||
		len(gate.All) > 0 || len(gate.Any) > 0
}

func gateHasAnyMatcher(gate *Gate) bool {
	return gateHasPositiveMatcher(gate) || len(gate.Not) > 0
}

// Patterns returns every regex and line_regex pattern in the manifest, paired
// with the rule that carries it, in file order. The vendoring tests use it to
// prove every upstream pattern compiles under RE2.
func (m *Manifest) Patterns() []Pattern {
	var out []Pattern
	for i := range m.Rules {
		rule := &m.Rules[i]
		gate := rule.RootGate()
		out = collectPatterns(&gate, rule.ID, out)
	}
	return out
}

// Pattern is one regex matcher, located by the rule that declares it.
type Pattern struct {
	RuleID  string
	Field   string
	Pattern string
}

func collectPatterns(gate *Gate, ruleID string, out []Pattern) []Pattern {
	for _, p := range gate.Regex {
		out = append(out, Pattern{RuleID: ruleID, Field: "regex", Pattern: p})
	}
	for _, p := range gate.LineRegex {
		out = append(out, Pattern{RuleID: ruleID, Field: "line_regex", Pattern: p})
	}
	for i := range gate.All {
		out = collectPatterns(&gate.All[i], ruleID, out)
	}
	for i := range gate.Any {
		out = collectPatterns(&gate.Any[i], ruleID, out)
	}
	for i := range gate.Not {
		out = collectPatterns(&gate.Not[i], ruleID, out)
	}
	return out
}
