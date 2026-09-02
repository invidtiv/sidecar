package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentactivity/manifest"
)

// `sidecar agent explain --file PATH --agent KIND` runs the screen lane alone
// over a saved capture: no tmux, no lifecycle store, no running agent.
//
// One input is not the file: the manifest still comes from manifests.Load, so a
// local override under ~/.config/sidecar/agent-detection replaces the vendored
// manifest here exactly as it does on a live pane. Two people running this over
// the same fixture can therefore reach different verdicts. That is the point of
// the override -- tuning a rule against a saved screen is the loop it exists for
// -- and the record says which file answered on its `manifest` line, with a
// `warning` line whenever an override was found and refused. A run that must not
// see one, such as scripts/herdr-diff.sh, passes `-config` to move the config
// axis, which moves the override directory with it.
//
// It exists for two jobs the live command cannot do. First, a wrong badge is
// reproduced from the fixture that produced it, which is what turns "it said
// idle once" into a test. Second, it is how a new fixture is minted: capture a
// pane, run this, read which rule matched. It is also one half of the
// differential harness (scripts/herdr-diff.sh), whose other half is
// `herdr agent explain --file` over the same bytes.

// fixtureObservation is a capture read from a file, in the format
// internal/agentactivity/testdata uses: a header of `key: value` lines, then a
// line reading exactly `screen:`, then the SGR-stripped capture.
//
// A file with no `screen:` line is treated as a raw screen, so a capture taken
// with `tmux capture-pane -p > screen.txt` works without being dressed up
// first. --title, --rows and --agent then supply what the header would have.
type fixtureObservation struct {
	screen  string
	title   string
	command string
	rows    int
}

const fixtureScreenMarker = "screen:\n"

func parseFixture(data []byte) fixtureObservation {
	text := string(data)
	head, screen, found := strings.Cut(text, fixtureScreenMarker)
	if !found {
		return fixtureObservation{screen: text}
	}
	ob := fixtureObservation{screen: screen}
	for _, line := range strings.Split(head, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "pane_title":
			ob.title = value
		case "pane_current_command":
			ob.command = value
		case "pane_height":
			ob.rows, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	}
	return ob
}

func explainFile(env Env, f lifecycleFlags, help string) int {
	if f.agent == "" {
		cliErrf(env.Stderr, "--file requires --agent KIND so the engine knows which manifest to evaluate\n\n%s", help)
		return 2
	}
	// A kind Sidecar claims as a provider is evaluated the way a live pane is,
	// process gate and all. A kind it merely vendors a manifest for — `kiro`,
	// `qodercli` — is evaluated as a manifest, with no gate and no verdict, so
	// that the overlay rules in those two files have a fixture, a census row and
	// a differential-harness row like every other rule. See
	// agentactivity.ExplainVendoredManifest.
	provider := agentactivity.Supports(f.agent)
	if !provider && !agentactivity.HasVendoredManifest(f.agent) {
		// A rejected value, not a malformed command line: --agent was spelled
		// correctly and carried a kind Sidecar has no manifest for. Exit 2 would
		// tell a caller on another machine that its Sidecar is too old to
		// understand the flag, which is what exit 2 means everywhere else here.
		cliErrf(env.Stderr, "no screen detection for agent %q; Sidecar knows codex, claude, grok, antigravity, pi, copilot, cursor, opencode, amp, muse, and every other agent it vendors a Herdr manifest for\n", f.agent)
		return exitInputRejected
	}
	data, err := os.ReadFile(f.file)
	if err != nil {
		// Same class: the path is the value that was refused. Exit 1 is reserved
		// for a failure inside the command, and this command's documented exit 1
		// used to read "the report could not be stored" -- about a store that
		// --file never opens.
		cliErrln(env.Stderr, err.Error())
		return exitInputRejected
	}

	fixture := parseFixture(data)
	command := fixture.command
	if command == "" {
		// A raw screen carries no process name, and the process gate would
		// refuse every such file. The gate is a live-pane protection — it stops
		// claude.toml being evaluated against a pane running something else —
		// and there is no live pane here, so --agent stands in for it. Naming
		// the agent is the user asserting the identity the gate would have
		// checked.
		command = f.agent
	}
	title := fixture.title
	if f.title != "" {
		title = f.title
	}
	rows := fixture.rows
	if f.rows != 0 {
		rows = f.rows
	}

	ob := agentactivity.Observation{
		Agent:          f.agent,
		Screen:         fixture.screen,
		PaneTitle:      title,
		CurrentCommand: command,
		PaneHeight:     rows,
	}
	if f.printWindow {
		// Herdr's `agent read --source detection` prints exactly the text
		// detection saw, and its AGENTS.md calls that the thing that makes rule
		// tuning a five-minute loop. This is the offline half of it: the read
		// window this observation produced, which is also what the differential
		// harness feeds to both engines so a disagreement is about rules rather
		// than about which bytes each one was shown.
		_, _ = io.WriteString(env.Stdout, manifest.ReadWindow(ob.Screen, ob.PaneHeight))
		return 0
	}

	var explain *manifest.Explain
	if provider {
		var result agentactivity.Result
		result, explain = agentactivity.DetectManifest(ob)
		if explain == nil {
			// A true internal failure, and the only one left: the agent is
			// supported, the file was read, and the engine still could not
			// produce a record -- a manifest that failed to load or compile.
			// That is exit 1.
			cliErrf(env.Stderr, "could not evaluate %s as %s: %s\n", f.file, f.agent, result.Evidence)
			return 1
		}
	} else if explain, _ = agentactivity.ExplainVendoredManifest(f.agent, ob); explain == nil {
		cliErrf(env.Stderr, "could not evaluate %s as %s: the vendored manifest did not compile\n", f.file, f.agent)
		return 1
	}

	if f.json {
		if err := json.NewEncoder(env.Stdout).Encode(explain); err != nil {
			cliErrln(env.Stderr, err.Error())
			return 1
		}
		return 0
	}
	writeManifestExplainText(env, explain)
	return 0
}

// writeManifestExplainText renders the record in Herdr's own explain layout, so
// a Sidecar record and a `herdr agent explain --file` record can be read side
// by side without translating between two vocabularies.
func writeManifestExplainText(env Env, e *manifest.Explain) {
	_, _ = fmt.Fprintf(env.Stdout, "agent: %s\n", e.Agent)
	_, _ = fmt.Fprintf(env.Stdout, "state: %s\n", e.State)
	_, _ = fmt.Fprintf(env.Stdout, "manifest: %s %s\n", e.ManifestSource, e.ManifestVersion)
	if e.Warning != "" {
		// A local override or an overlay that was found and refused. It goes
		// directly under the manifest line because it is the answer to "why is
		// the manifest not the one I put there".
		_, _ = fmt.Fprintf(env.Stdout, "warning: %s\n", e.Warning)
	}
	if e.MatchedRule != nil {
		_, _ = fmt.Fprintf(env.Stdout, "rule: %s (region=%s priority=%d)\n",
			e.MatchedRule.ID, e.MatchedRule.Region, e.MatchedRule.Priority)
		for _, rule := range e.EvaluatedRules {
			if rule.ID == e.MatchedRule.ID && rule.Evidence.RegionPreview != "" {
				_, _ = fmt.Fprintf(env.Stdout, "evidence: %q\n", rule.Evidence.RegionPreview)
				break
			}
		}
	} else {
		_, _ = fmt.Fprintln(env.Stdout, "rule: none")
	}
	if e.FallbackReason != "" {
		_, _ = fmt.Fprintf(env.Stdout, "fallback_reason: %s\n", e.FallbackReason)
	}
	if e.SkippedUpdateReason != "" {
		_, _ = fmt.Fprintf(env.Stdout, "skipped_update_reason: %s\n", e.SkippedUpdateReason)
	}
	_, _ = fmt.Fprintf(env.Stdout, "visible: idle=%v blocker=%v working=%v\n",
		e.VisibleIdle, e.VisibleBlocker, e.VisibleWorking)
	if len(e.EvaluatedRules) == 0 {
		return
	}
	_, _ = fmt.Fprintln(env.Stdout, "evaluated_rules:")
	for _, rule := range e.EvaluatedRules {
		mark := "✗"
		if rule.Matched {
			mark = "✓"
		}
		_, _ = fmt.Fprintf(env.Stdout, "  %s %s priority=%d region=%s state=%s\n",
			mark, rule.ID, rule.Priority, rule.Region, rule.State)
		_, _ = fmt.Fprintf(env.Stdout, "    matchers: contains=%v regex=%v line_regex=%v all=%d any=%d not=%d\n",
			rule.Evidence.Contains, rule.Evidence.Regex, rule.Evidence.LineRegex,
			rule.Evidence.AllCount, rule.Evidence.AnyCount, rule.Evidence.NotCount)
		_, _ = fmt.Fprintf(env.Stdout, "    region: bytes=%d preview=%q\n",
			rule.Evidence.RegionBytes, rule.Evidence.RegionPreview)
		if rule.Evidence.Incompatible != "" {
			_, _ = fmt.Fprintf(env.Stdout, "    %s\n", rule.Evidence.Incompatible)
		}
	}
}
