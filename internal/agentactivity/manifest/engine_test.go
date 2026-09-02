package manifest_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
)

func regionOf(t *testing.T, screen, spec string) string {
	t.Helper()
	text, err := manifest.RegionText(manifest.Input{Screen: screen}, spec)
	if err != nil {
		t.Fatalf("region %q: %v", spec, err)
	}
	return text
}

// --- the read window -------------------------------------------------------

func TestReadWindowIsTheTailOfTheBufferAtThePaneHeight(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 2000; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("", 0))
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	got := manifest.ReadWindow(b.String(), 39)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	// This is the measurement in docs/reference/herdr-detection-parity.md
	// ("Read window"), reproduced exactly: herdr 0.8.2 on a 39-row pane printing
	// 2000 numbered lines returned lines 1963-2000. Thirty-eight rows, not
	// thirty-nine, because the thirty-ninth row of the grid is the cursor
	// sitting blank below the output — the empty piece the terminating newline
	// leaves behind, which is a row and spends a row of the budget.
	//
	// Selecting the window before trimming is what makes that true. Trimming
	// first and windowing after returns 1962-2000, one row further up the
	// buffer than the pane can show.
	if len(lines) != 38 {
		t.Fatalf("window has %d rows, want the 38 herdr 0.8.2 returned", len(lines))
	}
	if lines[0] != "line 1963" || lines[len(lines)-1] != "line 2000" {
		t.Fatalf("window spans %q..%q, want line 1963..line 2000", lines[0], lines[len(lines)-1])
	}
}

func TestReadWindowFallsBackToTwentyFourRows(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		b.WriteString("row ")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	for _, rows := range []int{0, -1} {
		got := manifest.ReadWindow(b.String(), rows)
		lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
		// Twenty-four grid rows, of which the last is the blank cursor row, so
		// twenty-three carry text: rows 78 through 100.
		if len(lines) != manifest.DefaultDetectionRows-1 {
			t.Fatalf("rows=%d produced %d lines, want %d", rows, len(lines), manifest.DefaultDetectionRows-1)
		}
		if lines[0] != "row 78" || lines[len(lines)-1] != "row 100" {
			t.Fatalf("rows=%d window spans %q..%q", rows, lines[0], lines[len(lines)-1])
		}
	}
}

func TestReadWindowTrimsTrailingBlanksWithoutExtendingUpward(t *testing.T) {
	// Herdr trims the trailing padding a tall pane leaves and does *not* pull
	// extra history down to fill the gap, so the window can be shorter than the
	// pane. A window that grew back to full height would let a resolved
	// historical prompt back into view.
	//
	// Seven grid rows: three of text, then a blank, a whitespace-only row, a
	// blank, and the cursor row the terminating newline leaves.
	screen := "keep 1\nkeep 2\nkeep 3\n\n   \n\n"
	if got := manifest.ReadWindow(screen, 7); got != "keep 1\nkeep 2\nkeep 3\n" {
		t.Fatalf("window = %q", got)
	}

	// And the cost of not backfilling, which is the half that matters: on a
	// three-row pane the last three rows are all blank, so the window is empty
	// and the text three rows above it is simply not on screen. Trimming before
	// windowing would return the three keeps here, which is a pane state the
	// user cannot see.
	if got := manifest.ReadWindow(screen, 3); got != "" {
		t.Fatalf("three-row window = %q, want empty", got)
	}
}

func TestReadWindowKeepsInteriorBlanksAndRightTrimsEachRow(t *testing.T) {
	got := manifest.ReadWindow("alpha   \n\n  beta\t\n", 24)
	if got != "alpha\n\n  beta\n" {
		t.Fatalf("window = %q", got)
	}
}

func TestReadWindowStripsSGREscapesBeforeAnythingElse(t *testing.T) {
	// A row holding nothing but escape bytes renders as blank and must be
	// trimmed as the padding it is; a coloured prompt marker must still be
	// column-anchored once the escapes are gone.
	got := manifest.ReadWindow("\x1b[36m› \x1b[0mprompt\n\x1b[0m\n", 24)
	if got != "› prompt\n" {
		t.Fatalf("window = %q", got)
	}
}

// --- region resolution -----------------------------------------------------

func TestRegionResolutionForEveryNamedRegion(t *testing.T) {
	// One synthetic screen shaped like a Codex pane: a block marker, a
	// finished response, a horizontal-rule prompt box, and a live composer.
	codex := "• ran the tool\nsome output\n\n" +
		"────────────\n" +
		"box body\n" +
		"────────────\n" +
		"› ask me anything\n"

	tests := []struct {
		spec   string
		screen string
		want   string
	}{
		{"whole_recent", codex, codex},
		// The composer is live (no block marker below it), so the region is
		// empty. That emptiness is the point: it is what stops a stale blocker
		// phrase in the scrollback from matching.
		{"whole_recent_without_current_prompt_marker", codex, ""},
		{"whole_recent_without_current_prompt_marker", "• done\nno composer here\n", "• done\nno composer here\n"},
		{"after_last_prompt_marker", codex, ""},
		{"after_last_prompt_marker", "no composer at all\n", "no composer at all\n"},
		{"after_last_prompt_marker", "› old\nafter it\n", "after it\n"},
		{"before_current_prompt_marker", codex, "• ran the tool\nsome output\n\n────────────\nbox body\n────────────\n"},
		{"before_current_prompt_marker", "no composer\n", "no composer\n"},
		{"current_prompt_block_marker", codex, "• ran the tool"},
		{"current_prompt_block_marker", "no marker\n› live\n", ""},
		{"after_current_prompt_block_marker", codex, codex},
		{"after_current_prompt_block_marker", "no marker\n› live\n", ""},
		{"prompt_box_body", codex, "box body\n"},
		{"prompt_box_body", "no box here\n", ""},
		{"above_prompt_box", codex, "• ran the tool\nsome output\n\n"},
		{"above_prompt_box", "no box here\n", "no box here\n"},
		{"last_non_empty_above_prompt_box", codex, "some output"},
		{"after_last_horizontal_rule", codex, "› ask me anything\n"},
		{"after_last_horizontal_rule", "no rule\n", "no rule\n"},
		{"bottom_lines(2)", codex, "────────────\n› ask me anything\n"},
		{"bottom_lines(0)", codex, ""},
		{"bottom_lines(99)", codex, codex},
		{"bottom_non_empty_lines(2)", codex, "────────────\n› ask me anything\n"},
		{"bottom_non_empty_lines(99)", codex, codex},
		{"bottom_non_empty_lines(1)", "tail\n\n\n", "tail\n"},
		{"bottom_non_empty_lines(1)", "   \n", ""},
		{"top_non_empty_lines(1)", codex, "• ran the tool\n"},
		{"top_non_empty_lines(3)", codex, "• ran the tool\nsome output\n\n────────────\n"},
		{"top_non_empty_lines(99)", codex, codex},
	}
	for _, tt := range tests {
		t.Run(tt.spec+"/"+strings.SplitN(tt.screen, "\n", 2)[0], func(t *testing.T) {
			if got := regionOf(t, tt.screen, tt.spec); got != tt.want {
				t.Fatalf("region %s = %q, want %q", tt.spec, got, tt.want)
			}
		})
	}
}

func TestOSCRegionsReadTheirOwnStringsNotTheScreen(t *testing.T) {
	in := manifest.Input{Screen: "screen text\n", Title: "⠋ project", Progress: "4;0;"}
	if got, _ := manifest.RegionText(in, "osc_title"); got != "⠋ project" {
		t.Fatalf("osc_title = %q", got)
	}
	if got, _ := manifest.RegionText(in, "osc_progress"); got != "4;0;" {
		t.Fatalf("osc_progress = %q", got)
	}
	// Under tmux, Progress is always empty. The region resolves rather than
	// being skipped, so an explain record shows the rule as evaluated and
	// unmatched with empty evidence.
	if got, _ := manifest.RegionText(manifest.Input{Screen: "x"}, "osc_progress"); got != "" {
		t.Fatalf("osc_progress under tmux = %q, want empty", got)
	}
}

// Herdr's osc_title is a decoded OSC 0/2 payload and carries no SGR by
// construction. Sidecar's is tmux `#{pane_title}`, which hands back whatever
// bytes the program wrote, so a provider that colours its spinner ships an
// escape ahead of the glyph. Every upstream osc_title rule is anchored at the
// start of the title (`^[\x{2800}-\x{28FF}\x{25D0}-\x{25D3}] ` in claude.toml),
// so an unstripped escape turns the rule into a permanent no-match and a
// working pane reads as idle.
func TestOSCTitleIsStrippedOfSGRBeforeAnyRuleSeesIt(t *testing.T) {
	in := manifest.Input{Screen: "screen text\n", Title: "\x1b[33m⠋\x1b[0m project"}
	if got, _ := manifest.RegionText(in, "osc_title"); got != "⠋ project" {
		t.Fatalf("osc_title = %q, want the stripped title", got)
	}
	// Stripping a title with no escapes is identity, so nothing upstream
	// evaluates differently because of this.
	plain := manifest.Input{Title: "⠋ project"}
	if got, _ := manifest.RegionText(plain, "osc_title"); got != "⠋ project" {
		t.Fatalf("plain osc_title = %q", got)
	}
}

func TestHorizontalRuleGlyphRules(t *testing.T) {
	// Herdr's is_horizontal_rule (manifest.rs:1511): only U+2500 counts, only
	// in a run at the start of the trimmed line, and a run shorter than three
	// must be the whole line.
	rule := func(line string) bool {
		// A line is a rule exactly when a two-rule screen makes a prompt box.
		screen := line + "\nbody\n" + line + "\ntail\n"
		return regionOf(t, screen, "prompt_box_body") == "body\n"
	}
	for _, line := range []string{"─", "───", "  ────  ", "─── (bypass permissions on) ─"} {
		if !rule(line) {
			t.Fatalf("%q should be a horizontal rule", line)
		}
	}
	for _, line := range []string{"", "   ", "── label", "-----", "━━━━━", "text ───"} {
		if rule(line) {
			t.Fatalf("%q should not be a horizontal rule", line)
		}
	}
}

// --- evaluation semantics --------------------------------------------------

func TestEveryRuleIsEvaluatedAndHighestPriorityWins(t *testing.T) {
	compiled := compileSource(t, `
id = "codex"

[[rules]]
id = "first_in_file"
state = "idle"
priority = 1
contains = ["token"]

[[rules]]
id = "later_but_higher"
state = "blocked"
priority = 9
contains = ["token"]
`)
	verdict, explain := compiled.Explain(manifest.Input{Screen: "token"})
	if matchedID(verdict) != "later_but_higher" {
		t.Fatalf("rule = %q; Herdr evaluates every rule and keeps the highest priority, it does not stop at the first match", matchedID(verdict))
	}
	if len(explain.EvaluatedRules) != 2 {
		t.Fatalf("evaluated %d rules, want both", len(explain.EvaluatedRules))
	}
	for _, rule := range explain.EvaluatedRules {
		if !rule.Matched {
			t.Fatalf("rule %s should have matched", rule.ID)
		}
	}
}

func TestPriorityTieKeepsTheEarlierRule(t *testing.T) {
	compiled := compileSource(t, `
id = "codex"

[[rules]]
id = "earlier"
state = "idle"
priority = 5
contains = ["token"]

[[rules]]
id = "later"
state = "blocked"
priority = 5
contains = ["token"]
`)
	verdict := compiled.Evaluate(manifest.Input{Screen: "token"})
	if matchedID(verdict) != "earlier" || verdict.State != manifest.StateIdle {
		t.Fatalf("rule = %q state = %q, want the earlier rule to keep a tie", matchedID(verdict), verdict.State)
	}
}

func TestContainsIsCaseInsensitiveAndRegexIsNot(t *testing.T) {
	compiled := compileSource(t, `
id = "codex"

[[rules]]
id = "folded"
state = "blocked"
priority = 1
contains = ["Do You Want To Proceed"]

[[rules]]
id = "literal"
state = "working"
priority = 2
regex = ["Do You Want To Proceed"]
`)
	verdict, explain := compiled.Explain(manifest.Input{Screen: "do you want to proceed?\n"})
	if matchedID(verdict) != "folded" {
		t.Fatalf("rule = %q, want contains to fold case", matchedID(verdict))
	}
	for _, rule := range explain.EvaluatedRules {
		if rule.ID == "literal" && rule.Matched {
			t.Fatal("regex must not fold case unless the pattern says (?i)")
		}
	}
}

// Herdr folds `contains` with Rust's str::to_lowercase, which is the full
// Unicode lowercase algorithm: SpecialCasing (İ lowers to two runes) and the
// Final_Sigma condition (a Σ ending a word lowers to ς). Go's strings.ToLower
// folds each rune with the simple mapping and does neither, so a needle and a
// screen that Herdr separates would match here. Both splits below were verified
// against herdr 0.8.2.
//
// No vendored needle diverges today. This pins the fold so a sync that adds one
// cannot introduce a silent verdict difference between the two engines.
func TestContainsFoldsCaseTheWayRustDoes(t *testing.T) {
	compiled := compileSource(t, `
id = "codex"

[[rules]]
id = "dotted_i"
state = "blocked"
priority = 1
contains = ["istanbul"]

[[rules]]
id = "final_sigma"
state = "working"
priority = 1
contains = ["\u03a0\u03a3"]
`)
	// "İSTANBUL" lowers to "i" + U+0307 + "stanbul" under the full algorithm, so
	// the needle "istanbul" is not in it. Under a simple per-rune fold it is.
	for _, tt := range []struct {
		screen string
		want   string
	}{
		{"\u0130STANBUL\n", ""},
		{"istanbul\n", "dotted_i"},
		// "ΠΣΒ" keeps a medial sigma (πσβ); the needle "ΠΣ" ends in one, so it
		// folds to "πς" and does not occur in "πσβ".
		{"\u03a0\u03a3\u0392\n", ""},
		// The same needle against the same word ending the sigma matches.
		{"\u03a0\u03a3\n", "final_sigma"},
	} {
		verdict, _ := compiled.Explain(manifest.Input{Screen: tt.screen})
		if got := matchedID(verdict); got != tt.want {
			t.Fatalf("screen %q matched %q, want %q", tt.screen, got, tt.want)
		}
	}
}

func TestVisibleFlagsComeFromTheMatchedRuleAndItsState(t *testing.T) {
	compiled := compileSource(t, `
id = "codex"

[[rules]]
id = "idle_without_visible"
state = "idle"
priority = 1
contains = ["quiet"]

[[rules]]
id = "mismatched_visible"
state = "idle"
priority = 2
visible_working = true
contains = ["quiet"]
`)
	verdict := compiled.Evaluate(manifest.Input{Screen: "quiet"})
	if verdict.VisibleIdle || verdict.VisibleWorking || verdict.VisibleBlocker {
		t.Fatalf("verdict = %+v; a visible_working flag on an idle rule shows nothing", verdict)
	}
}

func TestSkipStateUpdateYieldsUnknownWithAReason(t *testing.T) {
	compiled := compileSource(t, `
id = "codex"

[[rules]]
id = "viewer"
state = "unknown"
priority = 10
skip_state_update = true
contains = ["transcript viewer"]
`)
	verdict := compiled.Evaluate(manifest.Input{Screen: "transcript viewer\n"})
	if verdict.State != manifest.StateUnknown || !verdict.SkipStateUpdate {
		t.Fatalf("verdict = %+v", verdict)
	}
	if verdict.SkippedUpdateReason != "matched_rule:viewer" {
		t.Fatalf("skipped_update_reason = %q", verdict.SkippedUpdateReason)
	}
}

func TestAnIncompatibleRegexNeverMatchesAndSaysSo(t *testing.T) {
	// \p{Alphabetic} is a Unicode binary property Rust's regex crate has and
	// RE2 does not. Validation is asked to tolerate it the way vendoring does.
	m, err := manifest.ParseAndValidateWith([]byte(`
id = "kiro"

[[rules]]
id = "tool_spinner_working"
state = "working"
priority = 90
line_regex = ['^\s*(◔|◑|◕|●)\s+\p{Alphabetic}']
`), manifest.ValidateOptions{AllowIncompatibleRegex: true})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := manifest.Compile(m)
	if err != nil {
		t.Fatalf("an incompatible pattern must not fail the load: %v", err)
	}
	verdict, explain := compiled.Explain(manifest.Input{Screen: "◔ Reading files\n"})
	if verdict.MatchedRule != nil {
		t.Fatalf("an uncompilable rule must never match, got %q", matchedID(verdict))
	}
	note := explain.EvaluatedRules[0].Evidence.Incompatible
	if !strings.HasPrefix(note, manifest.RegexIncompatibleNote) {
		t.Fatalf("evidence note = %q, want a %s note", note, manifest.RegexIncompatibleNote)
	}
}

// --- explain ---------------------------------------------------------------

func TestExplainCarriesHerdrsFieldNamesAndCappedPreviews(t *testing.T) {
	compiled := compileSource(t, `
id = "codex"

[[rules]]
id = "match_me"
state = "blocked"
priority = 3
region = "bottom_lines(1)"
visible_blocker = true
contains = ["token"]
`)
	long := strings.Repeat("x", 400) + " token\n"
	_, explain := compiled.Explain(manifest.Input{Screen: long})

	data, err := json.Marshal(explain)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"state", "manifest_source", "manifest_version", "overlay_applied", "matched_rule",
		"evaluated_rules", "fallback_reason", "skipped_update_reason",
		"visible_idle", "visible_working", "visible_blocker", "skip_state_update",
	} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("explain JSON is missing %q", key)
		}
	}
	matched, _ := decoded["matched_rule"].(map[string]any)
	for _, key := range []string{"id", "priority", "region", "state"} {
		if _, ok := matched[key]; !ok {
			t.Fatalf("matched_rule is missing %q", key)
		}
	}
	rules, _ := decoded["evaluated_rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("evaluated_rules has %d entries", len(rules))
	}
	evidence, _ := rules[0].(map[string]any)["evidence"].(map[string]any)
	preview, _ := evidence["region_preview"].(string)
	if !strings.HasSuffix(preview, "...") {
		t.Fatalf("a preview longer than %d chars must be elided, got %d chars", manifest.MaxRegionPreviewChars, len([]rune(preview)))
	}
	if runes := len([]rune(strings.TrimSuffix(preview, "..."))); runes != manifest.MaxRegionPreviewChars {
		t.Fatalf("preview kept %d chars, want %d", runes, manifest.MaxRegionPreviewChars)
	}
	if bytes, _ := evidence["region_bytes"].(float64); int(bytes) != len(long) {
		t.Fatalf("region_bytes = %v, want %d", bytes, len(long))
	}
}

func TestExplainOnNoMatchRecordsTheFallbackReason(t *testing.T) {
	compiled := compileSource(t, `
id = "codex"

[[rules]]
id = "never"
state = "blocked"
contains = ["absent"]
`)
	verdict, explain := compiled.Explain(manifest.Input{Screen: "nothing here\n"})
	if verdict.State != manifest.StateIdle || verdict.MatchedRule != nil {
		t.Fatalf("verdict = %+v", verdict)
	}
	if explain.FallbackReason != manifest.DefaultKnownAgentIdleFallback {
		t.Fatalf("fallback_reason = %q", explain.FallbackReason)
	}
	if len(explain.EvaluatedRules) != 1 || explain.EvaluatedRules[0].Matched {
		t.Fatalf("evaluated rules = %+v; a fallback still reports what it looked at", explain.EvaluatedRules)
	}
}

// --- overlays --------------------------------------------------------------

const overlayUpstream = `
id = "cursor"
version = "2026.08.03.1"
min_engine_version = 1

[[rules]]
id = "spinner_working"
state = "working"
priority = 90
contains = ["spinning"]

[[rules]]
id = "approval_blocked"
state = "blocked"
priority = 100
contains = ["run this command?"]
`

func mergeOverlay(t *testing.T, overlaySource string) *manifest.Manifest {
	t.Helper()
	upstream, err := manifest.ParseAndValidate([]byte(overlayUpstream))
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := manifest.ParseOverlay([]byte(overlaySource))
	if err != nil {
		t.Fatalf("parse overlay: %v", err)
	}
	merged, err := manifest.Merge(upstream, overlay)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	return merged
}

func TestOverlayReplacesAnUpstreamRuleInPlace(t *testing.T) {
	merged := mergeOverlay(t, `
id = "cursor"

[[rules]]
id = "spinner_working"
state = "working"
priority = 90
contains = ["whirling"]
`)
	if len(merged.Rules) != 2 {
		t.Fatalf("merged has %d rules, want the upstream two", len(merged.Rules))
	}
	if merged.Rules[0].ID != "spinner_working" {
		t.Fatalf("replacement moved the rule to position of %q; a replacement keeps upstream's slot so a priority tie still breaks the same way", merged.Rules[0].ID)
	}
	compiled, err := manifest.Compile(merged)
	if err != nil {
		t.Fatal(err)
	}
	if v := compiled.Evaluate(manifest.Input{Screen: "whirling\n"}); matchedID(v) != "spinner_working" {
		t.Fatalf("overlay rule did not take effect: %+v", v)
	}
	if v := compiled.Evaluate(manifest.Input{Screen: "spinning\n"}); v.MatchedRule != nil {
		t.Fatalf("upstream rule still live after replacement: %+v", v)
	}
}

func TestOverlayDisablesAnUpstreamRule(t *testing.T) {
	merged := mergeOverlay(t, `
id = "cursor"

[[rules]]
id = "spinner_working"
disable = true
`)
	if len(merged.Rules) != 1 || merged.Rules[0].ID != "approval_blocked" {
		t.Fatalf("merged rules = %+v", merged.Rules)
	}
}

func TestOverlayAppendsAPrefixedRule(t *testing.T) {
	merged := mergeOverlay(t, `
id = "cursor"

[[rules]]
id = "sidecar.run_everything_is_not_a_blocker"
state = "idle"
priority = 200
contains = ["run everything"]
`)
	if len(merged.Rules) != 3 || merged.Rules[2].ID != "sidecar.run_everything_is_not_a_blocker" {
		t.Fatalf("merged rules = %+v", merged.Rules)
	}
}

func TestOverlayRefusesAnUnprefixedNewRule(t *testing.T) {
	upstream, err := manifest.ParseAndValidate([]byte(overlayUpstream))
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := manifest.ParseOverlay([]byte(`
id = "cursor"

[[rules]]
id = "brand_new_rule"
state = "idle"
contains = ["anything"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Merge(upstream, overlay); err == nil {
		t.Fatalf("accepted a new overlay rule without the %q prefix", manifest.OverlayIDPrefix)
	}
}

// An overlay names the manifest it amends, and the loader keys on the file name
// instead, so the id is the only place a misfiled overlay can be caught.
func TestOverlayRefusesToAmendADifferentManifest(t *testing.T) {
	upstream, err := manifest.ParseAndValidate([]byte(overlayUpstream))
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := manifest.ParseOverlay([]byte(`
id = "codex"

[[rules]]
id = "sidecar.extra"
state = "working"
priority = 2
contains = ["busy"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Merge(upstream, overlay); err == nil {
		t.Fatal("an overlay declaring another agent's id was merged")
	} else if !strings.Contains(err.Error(), "amends") {
		t.Fatalf("error = %v", err)
	}
}

func TestOverlayRefusesToDisableAnUnknownRule(t *testing.T) {
	upstream, err := manifest.ParseAndValidate([]byte(overlayUpstream))
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := manifest.ParseOverlay([]byte(`
id = "cursor"

[[rules]]
id = "no_such_rule"
disable = true
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Merge(upstream, overlay); err == nil {
		t.Fatal("accepted a disable for a rule upstream does not have; a stale disable must be loud, not silent")
	}
}

func TestAPlainManifestRejectsDisable(t *testing.T) {
	source := `
id = "cursor"

[[rules]]
id = "spinner_working"
disable = true
`
	if _, err := manifest.Parse([]byte(source)); err == nil {
		t.Fatal("a plain manifest accepted disable")
	}
	if _, err := manifest.ParseOverlay([]byte(source)); err != nil {
		t.Fatalf("an overlay rejected disable: %v", err)
	}
}

func TestOverlayRefusesDisableCombinedWithContent(t *testing.T) {
	if _, err := manifest.ParseOverlay([]byte(`
id = "cursor"

[[rules]]
id = "spinner_working"
disable = true
state = "working"
contains = ["whirling"]
`)); err == nil {
		t.Fatal("accepted disable together with rule content; disable removes a rule, replacement edits one")
	}
}

func TestMergedManifestIsHeldToTheSameLimits(t *testing.T) {
	upstream, err := manifest.ParseAndValidate([]byte(overlayUpstream))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("id = \"cursor\"\n")
	for i := 0; i < manifest.MaxRulesPerManifest; i++ {
		b.WriteString("\n[[rules]]\nid = \"sidecar.rule_")
		b.WriteString(itoa(i))
		b.WriteString("\"\nstate = \"idle\"\ncontains = [\"ready\"]\n")
	}
	overlay, err := manifest.ParseOverlay([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Merge(upstream, overlay); err == nil {
		t.Fatalf("merged past the %d-rule cap", manifest.MaxRulesPerManifest)
	}
}
