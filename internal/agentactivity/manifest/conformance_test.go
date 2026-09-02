package manifest_test

// Conformance suite: Herdr's own manifest tests, ported.
//
// Every case below carries the name of the Rust test it came from
// (~/code/herdr/src/detect/manifest/tests.rs at e2b85c7) so a reviewer can map
// the two files one to one. Herdr's tests are the executable specification of
// the grammar; if one of these fails, the Go engine has diverged from the
// engine the differential harness checks against, not merely from an opinion.
//
// Nine of Herdr's 45 inline tests are NOT ported, all for the same reason: they
// exercise Herdr's *loader* (its remote-manifest cache under the state
// directory, its ~/.config/herdr/agent-detection override, and the explicit
// reload boundary between them), not its engine.
//
// Sidecar has all three of those since Phase 5, so the equivalent assertions
// exist -- they live in the manifests package against Sidecar's own loader,
// which is vendored-plus-overlay under a fetched file rather than Herdr's flat
// three-source stack, and they are named for what they assert here rather than
// for the Rust test they answer. The map, in Herdr's order:
//
//	remote_manifest_loads_between_local_override_and_bundled
//	  manifests.TestAFetchedManifestNewerThanTheVendoredOneBecomesActive
//	fallback_explain_preserves_active_manifest_version
//	  the version fields on every explain path; no single test
//	older_cached_remote_manifest_does_not_shadow_newer_bundled_manifest
//	  manifests.TestAFetchedManifestOlderThanTheVendoredOneIsCachedButNotActive
//	local_override_shadows_cached_remote_manifest
//	  manifests.TestALocalOverrideWinsOverAFetchedManifest
//	invalid_local_override_falls_back_to_cached_remote_manifest
//	  manifests.TestAnInvalidLocalOverrideFallsBackToTheCachedManifest
//	detection_uses_cached_manifest_until_explicit_reload
//	  manifests.TestFetchInvalidatesOnlyTheAgentsWhoseCacheMoved
//
// Two more are covered elsewhere rather than here:
// all_bundled_manifests_parse_and_validate is
// manifests.TestAllVendoredManifestsParseAndValidate plus
// TestEveryVendoredManifestCompiles below, and
// top_non_empty_lines_requires_a_canonical_positive_bounded_count is a region
// parser test that already lives in manifest_test.go; it is repeated here
// because Herdr counts it as a grammar test.
//
// The nearest Sidecar equivalent of invalid_local_override_falls_back_to_...
// is TestOverlayFailureFallsBackToUpstream in the manifests package: a broken
// Sidecar overlay is reported as a diagnostic and the vendored file is used
// alone.

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/agentactivity/manifests"
)

// bundled compiles the *vendored upstream* manifest for an agent, with no
// Sidecar overlay merged over it.
//
// That is deliberate and it is what makes these ports worth having. Every test
// in this file is a port of one of Herdr's own inline tests, and what it
// asserts is Herdr's behaviour on Herdr's bytes: it exists to say whether this
// engine reads a manifest the way Herdr's engine reads it. Running them against
// the merged manifest would conflate that question with a second one — whether
// Sidecar's overlay changes the verdict — and a deliberate overlay would then
// fail a test whose subject is the engine. Two of them would today:
// `sidecar.composer_idle` and the `osc_title_idle` disable in
// manifests/sidecar/codex.toml are exactly such a deliberate change, and
// TestTheCodexOverlayReplacesTitleIdleWithComposerIdle in the agentactivity
// package is what pins *that*.
//
// AllowIncompatibleRegex matches how production loads the vendored tree: four
// upstream rules use `\p{Alphabetic}`, which RE2 cannot compile, and each has an
// overlay carrying the rewrite. None of the four agents is exercised here.
func bundled(t *testing.T, agent string) *manifest.Compiled {
	t.Helper()
	data, err := manifests.UpstreamBytes(agent + ".toml")
	if err != nil {
		t.Fatalf("read upstream %s: %v", agent, err)
	}
	m, err := manifest.ParseAndValidateWith(data, manifest.ValidateOptions{AllowIncompatibleRegex: true})
	if err != nil {
		t.Fatalf("parse upstream %s: %v", agent, err)
	}
	compiled, err := manifest.Compile(m)
	if err != nil {
		t.Fatalf("compile upstream %s: %v", agent, err)
	}
	return compiled
}

// compileSource parses and compiles an inline manifest, as Herdr's
// parse_manifest + compile_manifest pair does.
func compileSource(t *testing.T, source string) *manifest.Compiled {
	t.Helper()
	m, err := manifest.ParseAndValidate([]byte(source))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	compiled, err := manifest.Compile(m)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return compiled
}

// oscExplain is Herdr's osc_explain helper: a screen plus the two OSC strings.
func oscExplain(t *testing.T, agent, screen, title, progress string) (manifest.Verdict, *manifest.Explain) {
	t.Helper()
	return bundled(t, agent).Explain(manifest.Input{Screen: screen, Title: title, Progress: progress})
}

func matchedID(v manifest.Verdict) string {
	if v.MatchedRule == nil {
		return ""
	}
	return v.MatchedRule.ID
}

// --- grammar ---------------------------------------------------------------

// Herdr: known_agent_no_match_defaults_to_idle_fallback.
func TestKnownAgentNoMatchDefaultsToIdleFallback(t *testing.T) {
	verdict := bundled(t, "codex").Evaluate(manifest.Input{Screen: "ordinary prompt text"})
	if verdict.State != manifest.StateIdle {
		t.Fatalf("state = %q, want idle", verdict.State)
	}
	if verdict.VisibleIdle {
		t.Fatal("a fallback idle is not visible idle: it is the absence of evidence, not evidence of absence")
	}
	if verdict.FallbackReason != manifest.DefaultKnownAgentIdleFallback {
		t.Fatalf("fallback_reason = %q", verdict.FallbackReason)
	}
}

// Herdr: rule_semantics_apply_gates_priority_and_line_regex.
func TestRuleSemanticsApplyGatesPriorityAndLineRegex(t *testing.T) {
	compiled := compileSource(t, `
id = "codex"

[[rules]]
id = "low_contains"
state = "idle"
priority = 1
contains = ["match"]

[[rules]]
id = "high_nested_gates"
state = "working"
priority = 10
contains = ["match"]
all = [
  { any = [{ regex = ["w[io]n"] }, { contains = ["fallback"] }] },
]
not = [
  { contains = ["blocked"] },
]

[[rules]]
id = "line_regex"
state = "blocked"
priority = 20
line_regex = ["^exact line$"]
`)

	tests := []struct {
		name   string
		screen string
		state  manifest.State
		rule   string
	}{
		{"nested gates win on priority", "match win", manifest.StateWorking, "high_nested_gates"},
		{"a not gate demotes to the lower rule", "match win blocked", manifest.StateIdle, "low_contains"},
		{"line_regex matches one line, not the whole region", "before\nexact line\nafter", manifest.StateBlocked, "line_regex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict := compiled.Evaluate(manifest.Input{Screen: tt.screen})
			if verdict.State != tt.state || matchedID(verdict) != tt.rule {
				t.Fatalf("state = %q rule = %q, want %q %q", verdict.State, matchedID(verdict), tt.state, tt.rule)
			}
		})
	}
}

// Herdr: manifest_validation_rejects_unknown_fields_empty_rules_invalid_regions_and_regexes.
func TestManifestValidationRejectsMalformedRules(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{"unknown rule key", `
id = "codex"

[[rules]]
id = "typo"
state = "working"
contain = ["Working"]
`},
		{"rule with no matcher", `
id = "codex"

[[rules]]
id = "empty"
state = "working"
`},
		{"misspelled region", `
id = "codex"

[[rules]]
id = "bad_region"
state = "working"
region = "after_last_promt_marker"
contains = ["Working"]
`},
		{"invalid regex", `
id = "codex"

[[rules]]
id = "bad_regex"
state = "working"
regex = ["["]
`},
		{"invalid nested regex", `
id = "codex"

[[rules]]
id = "bad_nested_regex"
state = "working"
any = [{ line_regex = ["["] }]
`},
		// Sidecar addition. Herdr's serde default fires only for an absent
		// key, so an explicit empty region is an invalid region name and not a
		// silent fall-through to whole_recent.
		{"explicitly empty region", `
id = "codex"

[[rules]]
id = "empty_region"
state = "working"
region = ""
contains = ["Working"]
`},
		// Sidecar additions: the integer widths Rust enforces while
		// deserializing and Go's decoder does not.
		{"priority beyond i32", `
id = "codex"

[[rules]]
id = "wide_priority"
state = "working"
priority = 3000000000
contains = ["Working"]
`},
		{"negative min_engine_version", `
id = "codex"
min_engine_version = -1

[[rules]]
id = "ok"
state = "working"
contains = ["Working"]
`},
		{"min_engine_version beyond u32", `
id = "codex"
min_engine_version = 4294967296

[[rules]]
id = "ok"
state = "working"
contains = ["Working"]
`},
		{"non-numeric version", `
id = "codex"
version = "abc"

[[rules]]
id = "ok"
state = "working"
contains = ["Working"]
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := manifest.ParseAndValidate([]byte(tt.source)); err == nil {
				t.Fatal("accepted a manifest Herdr rejects")
			}
		})
	}
}

// Herdr: manifest_validation_keeps_skip_rules_neutral.
func TestManifestValidationKeepsSkipRulesNeutral(t *testing.T) {
	for _, source := range []string{`
id = "codex"

[[rules]]
id = "bad_skip_state"
state = "idle"
skip_state_update = true
contains = ["menu"]
`, `
id = "codex"

[[rules]]
id = "bad_skip_visible"
state = "unknown"
skip_state_update = true
visible_blocker = true
contains = ["menu"]
`} {
		if _, err := manifest.ParseAndValidate([]byte(source)); err == nil {
			t.Fatalf("accepted a skip rule that is not neutral:\n%s", source)
		}
	}
}

// Herdr: manifest_validation_rejects_excessive_rule_count.
func TestManifestValidationRejectsExcessiveRuleCount(t *testing.T) {
	var b strings.Builder
	b.WriteString("id = \"codex\"\n")
	for i := 0; i < manifest.MaxRulesPerManifest+1; i++ {
		b.WriteString("\n[[rules]]\nid = \"rule_")
		b.WriteString(strings.Repeat("x", 1))
		b.WriteString(itoa(i))
		b.WriteString("\"\nstate = \"idle\"\ncontains = [\"ready\"]\n")
	}
	if _, err := manifest.ParseAndValidate([]byte(b.String())); err == nil {
		t.Fatalf("accepted %d rules, max is %d", manifest.MaxRulesPerManifest+1, manifest.MaxRulesPerManifest)
	}
}

// Herdr: manifest_validation_rejects_excessive_gate_depth.
func TestManifestValidationRejectsExcessiveGateDepth(t *testing.T) {
	source := `
id = "codex"

[[rules]]
id = "deep"
state = "idle"
contains = ["ready"]
all = [
  { contains = ["1"], all = [
    { contains = ["2"], all = [
      { contains = ["3"], all = [
        { contains = ["4"], all = [
          { contains = ["5"], all = [
            { contains = ["6"], all = [
              { contains = ["7"], all = [
                { contains = ["8"], all = [
                  { contains = ["9"] },
                ] },
              ] },
            ] },
          ] },
        ] },
      ] },
    ] },
  ] },
]
`
	if _, err := manifest.ParseAndValidate([]byte(source)); err == nil {
		t.Fatalf("accepted a gate tree deeper than %d", manifest.MaxGateDepth)
	}
}

// Herdr: manifest_validation_rejects_excessive_matchers.
func TestManifestValidationRejectsExcessiveMatchers(t *testing.T) {
	matchers := make([]string, 0, manifest.MaxMatchersPerGate+1)
	for i := 0; i <= manifest.MaxMatchersPerGate; i++ {
		matchers = append(matchers, `"m`+itoa(i)+`"`)
	}
	source := "\nid = \"codex\"\n\n[[rules]]\nid = \"many\"\nstate = \"idle\"\ncontains = [" +
		strings.Join(matchers, ", ") + "]\n"
	if _, err := manifest.ParseAndValidate([]byte(source)); err == nil {
		t.Fatalf("accepted %d matchers in one gate, max is %d",
			manifest.MaxMatchersPerGate+1, manifest.MaxMatchersPerGate)
	}
}

// Herdr: bottom_non_empty_lines_uses_bottom_occurrence_for_repeated_text.
func TestBottomNonEmptyLinesUsesBottomOccurrenceForRepeatedText(t *testing.T) {
	got := regionOf(t, "marker\nold\n\nmiddle\nmarker\nnew\n", "bottom_non_empty_lines(2)")
	if got != "marker\nnew\n" {
		t.Fatalf("region = %q", got)
	}
}

// Herdr: top_non_empty_lines_uses_top_occurrence_for_repeated_text.
func TestTopNonEmptyLinesUsesTopOccurrenceForRepeatedText(t *testing.T) {
	got := regionOf(t, "\nmarker\nold\n\nmiddle\nmarker\nnew\n", "top_non_empty_lines(2)")
	if got != "\nmarker\nold\n" {
		t.Fatalf("region = %q", got)
	}
}

// Herdr: top_non_empty_lines_requires_a_canonical_positive_bounded_count.
func TestTopNonEmptyLinesRequiresACanonicalPositiveBoundedCount(t *testing.T) {
	for _, ok := range []string{"top_non_empty_lines(1)", "top_non_empty_lines(65535)"} {
		if _, err := manifest.ParseRegion(ok); err != nil {
			t.Fatalf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"0", "01", "+1", "65536", "999999999999999999999999"} {
		if _, err := manifest.ParseRegion("top_non_empty_lines(" + bad + ")"); err == nil {
			t.Fatalf("top_non_empty_lines accepted invalid count %q", bad)
		}
	}
}

// Herdr: top_non_empty_lines_requires_engine_three_when_declared.
func TestTopNonEmptyLinesRequiresEngineThreeWhenDeclared(t *testing.T) {
	source := `
id = "grok"
version = "1"
min_engine_version = 2

[[rules]]
id = "background"
state = "working"
region = " top_non_empty_lines(1) "
contains = ["active"]
`
	if _, err := manifest.ParseAndValidate([]byte(source)); err == nil {
		t.Fatal("accepted top_non_empty_lines on a manifest declaring engine 2")
	}
}

// --- Claude OSC rules ------------------------------------------------------

// Herdr: claude_osc_title_braille_prefix_is_working.
func TestClaudeOSCTitleBraillePrefixIsWorking(t *testing.T) {
	// "⠂" is U+2802, in the braille block U+2800-U+28FF.
	verdict, _ := oscExplain(t, "claude", "", "⠂ project", "")
	if verdict.State != manifest.StateWorking || matchedID(verdict) != "osc_title_working" || !verdict.VisibleWorking {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: claude_osc_title_half_circle_frames_are_working. This is the drift the
// plan opens with: Claude Code 2.1.228 moved from braille to half circles and
// Sidecar's hand-ported rule never followed.
func TestClaudeOSCTitleHalfCircleFramesAreWorking(t *testing.T) {
	for _, frame := range []string{"◐", "◓", "◑", "◒"} {
		verdict, _ := oscExplain(t, "claude", "", frame+" Initial conversation with Claude", "")
		if verdict.State != manifest.StateWorking || matchedID(verdict) != "osc_title_working" || !verdict.VisibleWorking {
			t.Fatalf("frame %s: verdict = %+v rule = %q", frame, verdict, matchedID(verdict))
		}
	}
}

// Herdr: claude_osc_title_static_prefix_is_idle.
func TestClaudeOSCTitleStaticPrefixIsIdle(t *testing.T) {
	verdict, _ := oscExplain(t, "claude", "", "✳ Claude Code", "")
	if verdict.State != manifest.StateIdle || matchedID(verdict) != "osc_title_idle" || !verdict.VisibleIdle {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: claude_osc_progress_4_3_alone_does_not_force_working.
func TestClaudeOSCProgress43AloneDoesNotForceWorking(t *testing.T) {
	verdict, _ := oscExplain(t, "claude", "", "", "4;3;")
	if verdict.State != manifest.StateIdle || verdict.FallbackReason != manifest.DefaultKnownAgentIdleFallback || verdict.VisibleWorking {
		t.Fatalf("verdict = %+v", verdict)
	}
}

// Herdr: claude_blocker_screen_outranks_stale_osc_progress.
func TestClaudeBlockerScreenOutranksStaleOSCProgress(t *testing.T) {
	screen := "──────────\n  1. Yes\n  2. No\n\nEnter to select · ↑/↓ to navigate · Esc to cancel\n"
	verdict, _ := oscExplain(t, "claude", screen, "✳ Task title", "4;3;")
	if verdict.State != manifest.StateBlocked || !verdict.VisibleBlocker {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: claude_osc_progress_4_0_is_idle. Under tmux this rule can never fire
// on a live pane, because tmux consumes OSC 9;4 and exposes no payload; the
// test proves the engine would honour it if a transport ever supplied one.
func TestClaudeOSCProgress40IsIdle(t *testing.T) {
	verdict, _ := oscExplain(t, "claude", "", "", "4;0;")
	if verdict.State != manifest.StateIdle || matchedID(verdict) != "osc_progress_idle" {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: claude_blocker_screen_outranks_osc_idle_title.
func TestClaudeBlockerScreenOutranksOSCIdleTitle(t *testing.T) {
	screen := "do you want to proceed?\n" +
		"bash command: rm -rf /tmp/test\n" +
		"❯ 1. Yes\n   2. No\n\n" +
		"Esc to cancel · Tab to amend · ctrl+e to explain\n"
	verdict, _ := oscExplain(t, "claude", screen, "✳ Claude Code", "")
	if verdict.State != manifest.StateBlocked || !verdict.VisibleBlocker {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: claude_mcp_elicitation_is_blocked (their issue #3283).
func TestClaudeMCPElicitationIsBlocked(t *testing.T) {
	for _, screen := range []string{
		"MCP server \u201cmy-server\u201d requests your input\n\nGrant temporary access to the demo gateway for 15 minutes?\n\n\u276f Accept    Decline\n\nEsc to cancel \u00b7 \u2191/\u2193 to navigate\n",
		"MCP server \"my-server\" requests your input\n\nserver-supplied message\n\n\u276f Accept    Decline\n\nEsc to cancel \u00b7 \u2191/\u2193 to navigate\n",
	} {
		verdict, _ := oscExplain(t, "claude", screen, "\u2733 Claude Code", "")
		if verdict.State != manifest.StateBlocked || !verdict.VisibleBlocker || matchedID(verdict) != "mcp_elicitation_prompt" {
			t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
		}
	}
}

// Herdr: claude_empty_osc_empty_screen_is_idle_fallback.
func TestClaudeEmptyOSCEmptyScreenIsIdleFallback(t *testing.T) {
	verdict, _ := oscExplain(t, "claude", "", "", "")
	if verdict.State != manifest.StateIdle || verdict.FallbackReason != manifest.DefaultKnownAgentIdleFallback || verdict.VisibleIdle {
		t.Fatalf("verdict = %+v", verdict)
	}
}

// --- Codex OSC and screen rules -------------------------------------------

// Herdr: codex_osc_title_braille_spinner_is_working.
func TestCodexOSCTitleBrailleSpinnerIsWorking(t *testing.T) {
	verdict, _ := oscExplain(t, "codex", "", "⠋ llm-proxy", "")
	if verdict.State != manifest.StateWorking || matchedID(verdict) != "osc_title_working" || !verdict.VisibleWorking {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_osc_title_action_required_is_blocked.
func TestCodexOSCTitleActionRequiredIsBlocked(t *testing.T) {
	verdict, _ := oscExplain(t, "codex", "", "[ . ] Action Required | llm-proxy", "")
	if verdict.State != manifest.StateBlocked || matchedID(verdict) != "osc_title_blocked" || !verdict.VisibleBlocker {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_osc_title_plain_is_idle.
func TestCodexOSCTitlePlainIsIdle(t *testing.T) {
	verdict, _ := oscExplain(t, "codex", "", "llm-proxy", "")
	if verdict.State != manifest.StateIdle || matchedID(verdict) != "osc_title_idle" || !verdict.VisibleIdle {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_trust_directory_requires_live_top_region. This is the one
// upstream rule that reads the *top* of the read window, so it is also the
// test that would fail first if the window were the wrong size.
func TestCodexTrustDirectoryRequiresLiveTopRegion(t *testing.T) {
	screen := "> You are in C:\\Users\\user\\project\n\n" +
		"Do you trust the contents of this\n" +
		"directory? Working with untrusted\n" +
		"contents comes with higher risk of\n" +
		"prompt injection. Trusting the\n" +
		"directory allows project-local config,\n" +
		"hooks, and exec policies to load.\n\n" +
		"› 1. Yes, continue\n" +
		"  2. No, quit\n\n" +
		"Press enter to continue\n"
	verdict, _ := oscExplain(t, "codex", screen, "project", "")
	if verdict.State != manifest.StateBlocked || matchedID(verdict) != "trust_directory" || !verdict.VisibleBlocker {
		t.Fatalf("live trust prompt: verdict = %+v rule = %q", verdict, matchedID(verdict))
	}

	transcript := "› > You are in C:\\Users\\user\\project\n\n" +
		"Do you trust the contents of this\n" +
		"directory? Working with untrusted contents comes with higher risk.\n"
	verdict, _ = oscExplain(t, "codex", transcript, "project", "")
	if verdict.State != manifest.StateIdle || matchedID(verdict) == "trust_directory" || verdict.VisibleBlocker {
		t.Fatalf("transcribed trust text: verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_background_terminal_screen_does_not_override_osc_idle.
func TestCodexBackgroundTerminalScreenDoesNotOverrideOSCIdle(t *testing.T) {
	verdict, _ := oscExplain(t, "codex", "background terminal running · /ps to view · /stop to close\n", "llm-proxy", "")
	if verdict.State != manifest.StateIdle || matchedID(verdict) != "osc_title_idle" || !verdict.VisibleIdle {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_screen_working_fallback_handles_static_osc_title.
func TestCodexScreenWorkingFallbackHandlesStaticOSCTitle(t *testing.T) {
	screen := "• I’ll run it and wait for completion.\n\n" +
		"◦ Working (1m 16s • esc to interrupt) · 1 background…\n\n" +
		"› Use /skills to list available skills\n\n" +
		"gpt-5.6-sol default · /work\n"
	verdict, _ := oscExplain(t, "codex", screen, "project", "")
	if verdict.State != manifest.StateWorking || matchedID(verdict) != "screen_working_fallback" || !verdict.VisibleWorking {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_osc_working_remains_preferred_over_screen_fallback.
func TestCodexOSCWorkingRemainsPreferredOverScreenFallback(t *testing.T) {
	screen := "• Working (4s • esc to interrupt)\n\n" +
		"› Use /skills to list available skills\n\n" +
		"gpt-5.6-sol default · /work\n"
	verdict, _ := oscExplain(t, "codex", screen, "⠸ project", "")
	if verdict.State != manifest.StateWorking || matchedID(verdict) != "osc_title_working" || !verdict.VisibleWorking {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_screen_blocker_outranks_working_fallback.
func TestCodexScreenBlockerOutranksWorkingFallback(t *testing.T) {
	screen := "• Working (4s • esc to interrupt)\n" +
		"› 1. Yes, proceed\n" +
		"Press enter to confirm or esc to cancel\n"
	verdict, _ := oscExplain(t, "codex", screen, "project", "")
	if verdict.State != manifest.StateBlocked || matchedID(verdict) != "live_strong_blocker" ||
		!verdict.VisibleBlocker || verdict.VisibleWorking {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_weak_blocker_without_current_prompt_is_blocked.
func TestCodexWeakBlockerWithoutCurrentPromptIsBlocked(t *testing.T) {
	verdict, _ := oscExplain(t, "codex", "do you want to continue? [y/n]\n", "project", "")
	if verdict.State != manifest.StateBlocked || matchedID(verdict) != "weak_blocker" {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_current_prompt_keeps_weak_text_from_overriding_working_fallback.
func TestCodexCurrentPromptKeepsWeakTextFromOverridingWorkingFallback(t *testing.T) {
	screen := "• Working (4s • esc to interrupt)\n" +
		"do you want to continue? [y/n]\n" +
		"› Use /skills to list available skills\n"
	verdict, _ := oscExplain(t, "codex", screen, "project", "")
	if verdict.State != manifest.StateWorking || matchedID(verdict) != "screen_working_fallback" || !verdict.VisibleWorking {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_weak_blocker_ignores_finished_response_above_current_prompt.
func TestCodexWeakBlockerIgnoresFinishedResponseAboveCurrentPrompt(t *testing.T) {
	screen := "• The `wt rm` transcript now shows [y/N] / esc, matching the real prompt.\n\n" +
		"─ Worked for 4m 59s ─\n\n" +
		"› Ask Codex to do anything\n"
	verdict, _ := oscExplain(t, "codex", screen, "project", "")
	if verdict.State != manifest.StateIdle || matchedID(verdict) != "osc_title_idle" {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_weak_blocker_ignores_wrapped_current_prompt_text.
func TestCodexWeakBlockerIgnoresWrappedCurrentPromptText(t *testing.T) {
	screen := "› Explain why this prompt wraps before quoting the confirmation text\n" +
		"  [y/N] / esc and whether the docs should include it\n\n" +
		"  gpt-5.6-sol default · /work\n"
	verdict, _ := oscExplain(t, "codex", screen, "project", "")
	if verdict.State != manifest.StateIdle || matchedID(verdict) != "osc_title_idle" {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_transcript_viewer_outranks_working_fallback.
func TestCodexTranscriptViewerOutranksWorkingFallback(t *testing.T) {
	screen := "• Working (4s • esc to interrupt)\n" +
		"› transcript\n" +
		"↑/↓ to scroll · pgup/pgdn to move · home/end to jump · q to quit · esc to edit prev\n"
	verdict, _ := oscExplain(t, "codex", screen, "project", "")
	if verdict.State != manifest.StateUnknown || matchedID(verdict) != "transcript_viewer" ||
		!verdict.SkipStateUpdate || verdict.VisibleWorking {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
	if verdict.SkippedUpdateReason != "matched_rule:transcript_viewer" {
		t.Fatalf("skipped_update_reason = %q", verdict.SkippedUpdateReason)
	}
}

// Herdr: codex_screen_working_fallback_ignores_stale_and_prompt_text.
func TestCodexScreenWorkingFallbackIgnoresStaleAndPromptText(t *testing.T) {
	screens := []string{
		"◦ Working (1m 16s • esc to interrupt)\n" +
			"■ Conversation interrupted\n" +
			"› Use /skills to list available skills\n" +
			"gpt-5.6-sol default · /work\n",
		"› Explain the text ◦ Working (1m 16s • esc to interrupt)\n" +
			"gpt-5.6-sol default · /work\n",
		"  ◦ Working (1m 16s • esc to interrupt)\n" +
			"› Use /skills to list available skills\n" +
			"gpt-5.6-sol default · /work\n",
	}
	for i, screen := range screens {
		verdict, _ := oscExplain(t, "codex", screen, "project", "")
		if verdict.State != manifest.StateIdle || matchedID(verdict) != "osc_title_idle" ||
			!verdict.VisibleIdle || verdict.VisibleWorking {
			t.Fatalf("screen %d: verdict = %+v rule = %q", i, verdict, matchedID(verdict))
		}
	}
}

// Herdr: codex_screen_working_fallback_ignores_interrupted_short_terminal.
func TestCodexScreenWorkingFallbackIgnoresInterruptedShortTerminal(t *testing.T) {
	screen := "◦ Working (1m 16s • esc to interrupt)\n" +
		"■ Conversation interrupted\n" +
		"›\n"
	verdict, _ := oscExplain(t, "codex", screen, "project", "")
	if verdict.State != manifest.StateIdle || matchedID(verdict) != "osc_title_idle" ||
		!verdict.VisibleIdle || verdict.VisibleWorking {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// Herdr: codex_osc_working_beats_weak_blocker_screen.
func TestCodexOSCWorkingBeatsWeakBlockerScreen(t *testing.T) {
	verdict, _ := oscExplain(t, "codex", "do you want to continue? [y/n]\n", "⠋ llm-proxy", "")
	if verdict.State != manifest.StateWorking || matchedID(verdict) != "osc_title_working" {
		t.Fatalf("verdict = %+v rule = %q", verdict, matchedID(verdict))
	}
}

// --- whole-manifest behaviour ---------------------------------------------

// Herdr: devin_manifest_detects_idle_working_and_blocked_states.
func TestDevinManifestDetectsIdleWorkingAndBlockedStates(t *testing.T) {
	tests := []struct {
		name   string
		screen string
		state  manifest.State
		rule   string
		idle   bool
		work   bool
		block  bool
	}{
		{
			name:   "prompt footer idle",
			screen: "─────────────────────────────────────────────────────\n❭ Ask Devin to build features, fix bugs, or work on\n  your code\n─────────────────────────────────────────────────────\nSWE-1.6               Context: 16k / 200k tokens (7%)",
			state:  manifest.StateIdle, idle: true,
		},
		{
			name:   "live prompt footer",
			screen: "Done.\n\n────────────────────────────────────────────────── (bypass permissions on) ─\n❭\n────────────────────────────────────────────────────────────────────────────\nClaude Opus 4.6 Thinking                                    Context: 38k / 200k tokens (18%)",
			state:  manifest.StateIdle, rule: "live_prompt_footer", idle: true,
		},
		{
			name:   "welcome prompt footer",
			screen: "⠀⠀⠀⠀⠀⣴⣾⣶⡄⠀⠀⠀⠀\n⠀⣴⣾⣶⡾⠛⠿⠟⠃⣴⣾⣶⡄  Devin CLI\n⠀⠛⠿⠟⠃⣴⣾⣶⡾⠛⠿⠟⠃  v2026.5.26-8\n⠀⣤⣶⣦⡄⠻⢿⠿⢷⣤⣶⣦⡄\n⠀⠻⢿⠿⢷⣤⣶⣦⡄⠻⢿⠿⠃  Hybrid\n⠀⠀⠀⠀⠀⠻⢿⠿⠃⠀⠀⠀⠀\n\n───────────────────────────\n❭ Ask Devin to build\n  features, fix bugs, or\n  work on your code\n───────────────────────────\nClaude Opus Looking for\n4.6 Thinkingplan mode? /\n            plan",
			state:  manifest.StateIdle, rule: "welcome_prompt_footer", idle: true,
		},
		{
			name:   "running tools",
			screen: "◔ Reading shell 91b655\n  │ Timeout: 35s\n\n⠀⡆ Running tools · 27s (esc to interrupt)\n─────────────────────────────────────────────────────\n❭ Guide Devin while it works",
			state:  manifest.StateWorking, work: true,
		},
		{
			name:   "trust prompt",
			screen: "Do you trust the authors of this directory?\nFor security, devin should not be run in directories\nwith untrusted content.\n❭ 1 Yes, trust /private/tmp/devin-hook-probe\n· 2 No, exit",
			state:  manifest.StateBlocked, block: true,
		},
		{
			name:   "permission prompt",
			screen: "⏺ Running command\n  └ $ sleep 30\n\n❭ 1 Yes  (Approve once)\n· 2 Yes, allow `sleep` commands\n· 3 Yes, always allow `sleep` commands\n· 4 No\n↑↓ select · ↵ confirm · esc cancel",
			state:  manifest.StateBlocked, block: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict := bundled(t, "devin").Evaluate(manifest.Input{Screen: tt.screen})
			if verdict.State != tt.state {
				t.Fatalf("state = %q rule = %q, want %q", verdict.State, matchedID(verdict), tt.state)
			}
			if tt.rule != "" && matchedID(verdict) != tt.rule {
				t.Fatalf("rule = %q, want %q", matchedID(verdict), tt.rule)
			}
			if verdict.VisibleIdle != tt.idle || verdict.VisibleWorking != tt.work || verdict.VisibleBlocker != tt.block {
				t.Fatalf("visible idle=%v working=%v blocker=%v", verdict.VisibleIdle, verdict.VisibleWorking, verdict.VisibleBlocker)
			}
		})
	}
}

// Herdr: muse_manifest_requires_complete_live_controls.
func TestMuseManifestRequiresCompleteLiveControls(t *testing.T) {
	tests := []struct {
		name   string
		screen string
		state  manifest.State
		idle   bool
		work   bool
		block  bool
		skip   bool
	}{
		{
			name:   "working",
			screen: "⟩ hello\n\n◆ Working (0s · esc to interrupt)\n\n────────────────\n⟩\n────────────────\ngpt-5.4 · minimal · /workspace",
			state:  manifest.StateWorking, work: true,
		},
		{
			name:   "option picker",
			screen: "Which option should I use?\n\n› 1. Alpha\n  2. Beta\n\nEnter to select · ↑/↓ to move · Tab for an optional note · Esc to interrupt\n\n────────────────\n⟩\n────────────────\ngpt-5.4 · minimal · /workspace",
			state:  manifest.StateBlocked, block: true,
		},
		{
			name:   "command approval",
			screen: "Would you like to run the following command?\n\n$ printf muse-safe-probe\n\n› 1. Allow this stage once (y)\n  2. Always allow in this workspace: printf muse-safe-probe ... (p)\n  3. Abort the entire command (esc)\n────────────────\ngpt-5.4 · minimal · /workspace",
			state:  manifest.StateBlocked, block: true,
		},
		{
			name:   "network approval",
			screen: "network: example.com:443 https\nrequested by:\n$ curl -fsS https://example.com\n\n› 1. Yes, proceed (y)\n  2. Yes, don't ask again this session (p)  example.com:443 (https)\n  3. No, and tell Muse Code what to do differently (esc)\n────────────────\ngpt-5.4 · minimal · /workspace",
			state:  manifest.StateBlocked, block: true,
		},
		{
			name:   "settings menu retains",
			screen: "Theme\n\n⟩ Default (active)\n  Dynamic\n\n↑↓ move · enter save · esc go back",
			state:  manifest.StateUnknown, skip: true,
		},
		{
			name:   "ordinary reply",
			screen: "⟩ say the phrase\n\n◆ Yes, proceed\n\n────────────────\n⟩\n────────────────\ngpt-5.4 · minimal · /workspace",
			state:  manifest.StateIdle, idle: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict := bundled(t, "muse").Evaluate(manifest.Input{Screen: tt.screen})
			if verdict.State != tt.state || verdict.SkipStateUpdate != tt.skip {
				t.Fatalf("state = %q skip = %v rule = %q", verdict.State, verdict.SkipStateUpdate, matchedID(verdict))
			}
			if verdict.VisibleIdle != tt.idle || verdict.VisibleWorking != tt.work || verdict.VisibleBlocker != tt.block {
				t.Fatalf("visible idle=%v working=%v blocker=%v", verdict.VisibleIdle, verdict.VisibleWorking, verdict.VisibleBlocker)
			}
		})
	}
}

// TestEveryVendoredManifestCompiles stands in for Herdr's
// all_bundled_manifests_parse_and_validate on the evaluation side: every
// vendored file must load, merge with its overlay, and compile.
func TestEveryVendoredManifestCompiles(t *testing.T) {
	agents, err := manifests.Agents()
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 21 {
		t.Fatalf("vendored %d manifests, expected Herdr's 21", len(agents))
	}
	for _, agent := range agents {
		compiled, source, err := manifests.Load(agent)
		if err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
		if compiled == nil {
			t.Fatalf("%s: nil compiled manifest", agent)
		}
		if source.Diagnostic != "" {
			t.Fatalf("%s: %s", agent, source.Diagnostic)
		}
		// A manifest must survive an empty observation without panicking; the
		// idle fallback is the only correct answer for one.
		if verdict := compiled.Evaluate(manifest.Input{}); verdict.State != manifest.StateIdle &&
			verdict.State != manifest.StateUnknown {
			t.Fatalf("%s on an empty screen = %q", agent, verdict.State)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
