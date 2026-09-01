// Package manifest implements Herdr's agent-detection manifest grammar in Go.
//
// The data model, the strict TOML decoding, and the validator are ports of
// Herdr's src/detect/manifest.rs (types, limits, validate_manifest) and its
// distribution checker scripts/agent_detection_manifest_check.py, at Herdr
// commit e2b85c7. Field names, defaults, limits, and error wording follow the
// Rust originals closely enough that a reader can map a Go failure back to the
// Rust line that produced it.
//
// This file carries only the data model, the parser, and the region/version
// value types. Evaluation (regions over a screen, gate matching, explain) is
// added on top of these types; nothing here compiles a regex or touches a
// screen, so a caller pays only for what it uses and package init does no
// work at all.
//
// The package must not import internal/agentactivity: the dependency runs the
// other way.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// EngineVersion is the manifest engine version this package implements. It
// mirrors Herdr's MANIFEST_ENGINE_VERSION in src/detect/manifest_update.rs.
// A manifest declaring a higher min_engine_version cannot be evaluated here.
const EngineVersion = 3

// DefaultKnownAgentIdleFallback is the reason Herdr records when a known agent
// matches no rule. Kept here so the evaluator and the explain record agree on
// the exact string Herdr emits.
const DefaultKnownAgentIdleFallback = "default_known_agent_idle_fallback"

// DefaultRegion is the region a rule uses when it declares none.
const DefaultRegion = "whole_recent"

// State is a manifest rule's declared agent state. Unknown spellings are
// rejected at decode time, matching serde's rename_all = "snake_case" enum.
type State string

// The four states Herdr's ManifestState enum allows.
const (
	StateIdle    State = "idle"
	StateWorking State = "working"
	StateBlocked State = "blocked"
	StateUnknown State = "unknown"
)

// Valid reports whether the state is one of the four Herdr's ManifestState
// enum allows. go-toml assigns a named string type directly rather than going
// through encoding.TextUnmarshaler, so Parse checks this explicitly to get the
// rejection serde's enum decoding gives for free.
func (s State) Valid() bool {
	switch s {
	case StateIdle, StateWorking, StateBlocked, StateUnknown:
		return true
	default:
		return false
	}
}

// Gate is a node in a rule's matcher tree. A gate matches when every one of
// its direct matchers holds, every nested `all` gate holds, at least one
// nested `any` gate holds (when any are present), and no nested `not` gate
// holds.
type Gate struct {
	All       []Gate   `toml:"all"`
	Any       []Gate   `toml:"any"`
	Not       []Gate   `toml:"not"`
	Contains  []string `toml:"contains"`
	Regex     []string `toml:"regex"`
	LineRegex []string `toml:"line_regex"`
}

// Rule is one manifest rule. Its matcher fields are the same set a Gate
// carries; Herdr builds the rule's root gate from them (manifest_gate_from_rule
// in manifest.rs) rather than nesting a table, so the two shapes are kept
// separate here for the same reason: the TOML shape differs even though the
// semantics do not.
type Rule struct {
	ID       string `toml:"id"`
	State    *State `toml:"state"`
	Priority int    `toml:"priority"`
	Region   string `toml:"region"`

	// Herdr's key names, verbatim: visible_idle, visible_blocker,
	// visible_working. Note "blocker", not "blocked".
	VisibleIdle    bool `toml:"visible_idle"`
	VisibleBlocker bool `toml:"visible_blocker"`
	VisibleWorking bool `toml:"visible_working"`

	SkipStateUpdate bool `toml:"skip_state_update"`

	All       []Gate   `toml:"all"`
	Any       []Gate   `toml:"any"`
	Not       []Gate   `toml:"not"`
	Contains  []string `toml:"contains"`
	Regex     []string `toml:"regex"`
	LineRegex []string `toml:"line_regex"`

	// region holds the parsed form of Region. Parse fills it in; an evaluator
	// added later switches on it without re-parsing the spec string per frame.
	region Region
}

// RegionSpec returns the parsed region for the rule. It is meaningful only
// after Parse (or ParseAndValidate) has populated it.
func (r *Rule) RegionSpec() Region { return r.region }

// RootGate returns the rule's matcher tree as a Gate, mirroring Herdr's
// manifest_gate_from_rule. Validation and evaluation both work through it, so
// a rule and a nested gate are held to exactly the same limits.
func (r *Rule) RootGate() Gate {
	return Gate{
		All:       r.All,
		Any:       r.Any,
		Not:       r.Not,
		Contains:  r.Contains,
		Regex:     r.Regex,
		LineRegex: r.LineRegex,
	}
}

// EffectiveState is the state a matched rule yields. Herdr maps an absent
// state to Unknown at evaluation time while keeping the "absent" and
// "explicitly unknown" cases distinct for validation.
func (r *Rule) EffectiveState() State {
	if r.State == nil {
		return StateUnknown
	}
	return *r.State
}

// Manifest is a whole agent-detection manifest. Only these six top-level keys
// are accepted; any other key rejects the file.
type Manifest struct {
	ID               string   `toml:"id"`
	Version          string   `toml:"version"`
	MinEngineVersion *int     `toml:"min_engine_version"`
	UpdatedAt        string   `toml:"updated_at"`
	Aliases          []string `toml:"aliases"`
	Rules            []Rule   `toml:"rules"`
}

// EngineTooNewError reports a manifest that declares a min_engine_version this
// engine cannot satisfy. It is a distinct type so the sync tool can name the
// file and the required version in its report instead of lumping it in with
// ordinary validation failures.
type EngineTooNewError struct {
	ManifestID string
	Required   int
	Engine     int
}

func (e *EngineTooNewError) Error() string {
	return fmt.Sprintf("manifest requires engine %d, current engine is %d", e.Required, e.Engine)
}

// AsEngineTooNew reports whether err is (or wraps) an EngineTooNewError.
func AsEngineTooNew(err error) (*EngineTooNewError, bool) {
	var target *EngineTooNewError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// Parse decodes a manifest with strict field checking. Unknown top-level keys,
// unknown rule keys, and unknown gate keys all reject the file, as Herdr's
// serde(deny_unknown_fields) does. Parse does not validate limits; use
// ParseAndValidate or Validate for that.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest is not valid TOML: %w", err)
	}
	for i := range m.Rules {
		rule := &m.Rules[i]
		if rule.State != nil && !rule.State.Valid() {
			return nil, fmt.Errorf("rule %s has unknown state %q, expected one of idle, working, blocked, unknown",
				rule.ID, *rule.State)
		}
		if strings.TrimSpace(rule.Region) == "" {
			rule.Region = DefaultRegion
		}
		// A parse failure here is reported by Validate with Herdr's wording;
		// leaving the zero Region until then keeps Parse free of policy.
		if region, err := ParseRegion(rule.Region); err == nil {
			rule.region = region
		}
	}
	return &m, nil
}

// ParseAndValidate decodes a manifest and applies Herdr's validator.
func ParseAndValidate(data []byte) (*Manifest, error) {
	return ParseAndValidateWith(data, ValidateOptions{})
}

// ParseAndValidateWith is ParseAndValidate with the regex-dialect option.
func ParseAndValidateWith(data []byte, opts ValidateOptions) (*Manifest, error) {
	m, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if err := ValidateWith(m, opts); err != nil {
		return nil, err
	}
	return m, nil
}

// ParseRemote decodes a manifest that arrived from outside the binary: a
// published catalog file, a vendored copy, or a local override. It applies
// everything ParseAndValidate does plus the distribution requirements Herdr
// puts on a remote file (parse_remote_manifest_for_agent in manifest.rs, and
// the stricter checks in scripts/agent_detection_manifest_check.py): version
// and min_engine_version must both be present, and a file needing a newer
// engine than this one is rejected with an *EngineTooNewError.
func ParseRemote(data []byte) (*Manifest, error) {
	return ParseRemoteWith(data, ValidateOptions{})
}

// ParseRemoteWith is ParseRemote with the regex-dialect option. The sync tool
// sets AllowIncompatibleRegex so a pattern RE2 cannot compile is vendored and
// reported rather than blocking the whole sync.
func ParseRemoteWith(data []byte, opts ValidateOptions) (*Manifest, error) {
	m, err := ParseAndValidateWith(data, opts)
	if err != nil {
		return nil, err
	}
	if err := ValidateDistribution(m); err != nil {
		return nil, err
	}
	return m, nil
}
