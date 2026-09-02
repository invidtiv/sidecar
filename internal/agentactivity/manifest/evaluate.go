package manifest

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxRegionPreviewChars caps the region text an Explain carries per rule. It is
// Herdr's MAX_CHARS in bounded_preview (manifest.rs:1228): the *first* 240
// characters, with a literal "..." appended when the region is longer. The cap
// is what makes an explain record safe to paste into an issue.
const MaxRegionPreviewChars = 240

// Verdict is one evaluation's outcome, without the per-rule diagnostics.
type Verdict struct {
	State           State
	MatchedRule     *MatchedRule
	VisibleIdle     bool
	VisibleBlocker  bool
	VisibleWorking  bool
	SkipStateUpdate bool
	// SkippedUpdateReason is Herdr's "matched_rule:<id>" when the matched rule
	// carries skip_state_update, and empty otherwise.
	SkippedUpdateReason string
	// FallbackReason is DefaultKnownAgentIdleFallback when no rule matched.
	FallbackReason string
}

// MatchedRule identifies the rule that authored a verdict.
type MatchedRule struct {
	ID       string `json:"id"`
	Priority int    `json:"priority"`
	Region   string `json:"region"`
	State    State  `json:"state"`
}

// Explain is a full evaluation record. The JSON tags are Herdr's snake_case
// spellings (explain_to_json_value, manifest.rs:831) so the differential
// harness can diff the two engines structurally rather than by eye. The three
// Sidecar-only fields — manifest_source's overlay form, overlay_applied, and
// the per-rule regex_incompatible note — are additions Herdr has no equivalent
// for; nothing Herdr emits is renamed or dropped from the shared fields.
type Explain struct {
	// Agent is the agent the caller asked about, from Input.Agent, which is
	// what Herdr reports here too. It is not the loaded manifest's id: see the
	// note on Input.Agent.
	Agent           string       `json:"agent,omitempty"`
	State           State        `json:"state"`
	ManifestSource  string       `json:"manifest_source"`
	ManifestVersion string       `json:"manifest_version"`
	OverlayApplied  bool         `json:"overlay_applied"`
	MatchedRule     *MatchedRule `json:"matched_rule"`
	VisibleIdle     bool         `json:"visible_idle"`
	VisibleBlocker  bool         `json:"visible_blocker"`
	VisibleWorking  bool         `json:"visible_working"`
	SkipStateUpdate bool         `json:"skip_state_update"`
	// SkippedUpdateReason and FallbackReason are plain strings rather than
	// pointers: Herdr emits null where these are empty, and the differential
	// harness normalises null to "" on both sides rather than making every Go
	// caller dereference.
	SkippedUpdateReason string `json:"skipped_update_reason"`
	FallbackReason      string `json:"fallback_reason"`
	// Warning is what the loader had to ignore to produce this manifest: a
	// local override or a Sidecar overlay that was found and refused, with the
	// reason. Herdr emits the same field under the same name for the same
	// reason, so a refused override reads identically in both records.
	Warning        string          `json:"warning"`
	EvaluatedRules []EvaluatedRule `json:"evaluated_rules"`
}

// EvaluatedRule is one rule's result. Every rule in the manifest appears here,
// matched or not, because "which rules were even looked at" is half of what
// makes a wrong badge explainable.
type EvaluatedRule struct {
	ID       string       `json:"id"`
	Priority int          `json:"priority"`
	Region   string       `json:"region"`
	State    State        `json:"state"`
	Matched  bool         `json:"matched"`
	Evidence RuleEvidence `json:"evidence"`
}

// RuleEvidence is what the rule was asked to match and what it was shown.
// Field-for-field Herdr's RuleEvidence (manifest.rs:112) plus Incompatible.
type RuleEvidence struct {
	Contains  []string `json:"contains"`
	Regex     []string `json:"regex"`
	LineRegex []string `json:"line_regex"`
	AllCount  int      `json:"all_count"`
	AnyCount  int      `json:"any_count"`
	NotCount  int      `json:"not_count"`
	// RegionBytes is the region's UTF-8 byte length, as Herdr's region_text.len().
	RegionBytes   int    `json:"region_bytes"`
	RegionPreview string `json:"region_preview"`
	// Incompatible carries RegexIncompatibleNote and the offending pattern when
	// the rule could not be compiled under RE2. Herdr has no such field because
	// its regex crate compiles everything upstream writes.
	Incompatible string `json:"regex_incompatible,omitempty"`
}

// Evaluate runs every rule and returns the winning verdict.
//
// Herdr's evaluate_loaded_manifest (manifest.rs:446) evaluates every rule
// rather than stopping at the first match, keeps the highest priority, and on a
// tie keeps the rule that appeared earlier in the file (`previous.priority >=
// rule.priority` leaves the previous winner in place). Sidecar's pre-manifest
// Evaluate stopped at the first match in file order and derived the visible_*
// flags from the state alone; both differences are deliberate here.
func (c *Compiled) Evaluate(in Input) Verdict {
	verdict, _ := c.evaluate(in, false)
	return verdict
}

// Explain runs every rule and returns the verdict together with the per-rule
// record. It costs one region preview per rule more than Evaluate, so polling
// surfaces call Evaluate and diagnostics call this.
func (c *Compiled) Explain(in Input) (Verdict, *Explain) {
	return c.evaluate(in, true)
}

func (c *Compiled) evaluate(in Input, explain bool) (Verdict, *Explain) {
	res := newResolver(in)

	var winner *compiledRule
	var evaluated []EvaluatedRule
	if explain {
		evaluated = make([]EvaluatedRule, 0, len(c.rules))
	}

	for i := range c.rules {
		cr := &c.rules[i]
		region := cr.rule.region
		text := res.region(region)
		matched := false
		if cr.incompatible == "" {
			lower := ""
			if cr.gate.usesContains() {
				lower = res.lowerRegion(region)
			}
			matched = cr.gate.matches(text, lower)
		}

		if explain {
			gate := cr.rule.RootGate()
			evidence := RuleEvidence{
				Contains:      nonNil(gate.Contains),
				Regex:         nonNil(gate.Regex),
				LineRegex:     nonNil(gate.LineRegex),
				AllCount:      len(gate.All),
				AnyCount:      len(gate.Any),
				NotCount:      len(gate.Not),
				RegionBytes:   len(text),
				RegionPreview: boundedPreview(text),
			}
			if cr.incompatible != "" {
				evidence.Incompatible = RegexIncompatibleNote + ": " + cr.incompatible
			}
			evaluated = append(evaluated, EvaluatedRule{
				ID:       cr.rule.ID,
				Priority: cr.rule.Priority,
				Region:   cr.rule.RegionName(),
				State:    cr.rule.EffectiveState(),
				Matched:  matched,
				Evidence: evidence,
			})
		}

		if !matched {
			continue
		}
		if winner == nil || cr.rule.Priority > winner.rule.Priority {
			winner = cr
		}
	}

	if winner == nil {
		verdict := Verdict{State: StateIdle, FallbackReason: DefaultKnownAgentIdleFallback}
		return verdict, c.explainRecord(in.Agent, verdict, evaluated, explain)
	}

	rule := winner.rule
	state := rule.EffectiveState()
	verdict := Verdict{
		State: state,
		MatchedRule: &MatchedRule{
			ID:       rule.ID,
			Priority: rule.Priority,
			Region:   rule.RegionName(),
			State:    state,
		},
		// Herdr gates each visible_* flag on the matched state as well as the
		// flag, so a rule declaring visible_working with state = "idle" shows
		// nothing (manifest.rs:509-511).
		VisibleIdle:     rule.VisibleIdle && state == StateIdle,
		VisibleBlocker:  rule.VisibleBlocker && state == StateBlocked,
		VisibleWorking:  rule.VisibleWorking && state == StateWorking,
		SkipStateUpdate: rule.SkipStateUpdate,
	}
	if rule.SkipStateUpdate {
		verdict.SkippedUpdateReason = "matched_rule:" + rule.ID
	}
	return verdict, c.explainRecord(in.Agent, verdict, evaluated, explain)
}

func (c *Compiled) explainRecord(agent string, v Verdict, evaluated []EvaluatedRule, want bool) *Explain {
	if !want {
		return nil
	}
	if evaluated == nil {
		evaluated = []EvaluatedRule{}
	}
	return &Explain{
		Agent:               agent,
		State:               v.State,
		ManifestSource:      c.Source,
		ManifestVersion:     c.Manifest.Version,
		OverlayApplied:      c.OverlayApplied,
		MatchedRule:         v.MatchedRule,
		VisibleIdle:         v.VisibleIdle,
		VisibleBlocker:      v.VisibleBlocker,
		VisibleWorking:      v.VisibleWorking,
		SkipStateUpdate:     v.SkipStateUpdate,
		SkippedUpdateReason: v.SkippedUpdateReason,
		FallbackReason:      v.FallbackReason,
		Warning:             c.Warning,
		EvaluatedRules:      evaluated,
	}
}

// boundedPreview ports Herdr's bounded_preview (manifest.rs:1228): the first
// MaxRegionPreviewChars *characters*, not bytes, with "..." appended when the
// text is longer.
func boundedPreview(text string) string {
	if utf8.RuneCountInString(text) <= MaxRegionPreviewChars {
		return text
	}
	count := 0
	for i := range text {
		if count == MaxRegionPreviewChars {
			return text[:i] + "..."
		}
		count++
	}
	return text + "..."
}

// nonNil keeps a JSON array from rendering as null, so an explain record diffs
// structurally against Herdr's, which always emits [].
func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// RegionText resolves one region against an observation. It exists for the
// region-resolution tests and for `explain`'s region reporting; ordinary
// evaluation goes through the cached resolver instead.
func RegionText(in Input, spec string) (string, error) {
	region, err := ParseRegion(spec)
	if err != nil {
		return "", fmt.Errorf("invalid region %q", strings.TrimSpace(spec))
	}
	return newResolver(in).region(region), nil
}
