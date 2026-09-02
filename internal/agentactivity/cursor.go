package agentactivity

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Cursor Agent CLI. The screen rules are Herdr's, executed from the vendored
// `manifests/upstream/cursor.toml` by the manifest engine (Phase 2 of
// docs/plans/active/herdr-detection-parity.md) with three Sidecar rules layered
// over them in `manifests/sidecar/cursor.toml`. What remains here is Sidecar's:
// the process gate, and the identity fallback Identify uses.
//
// Cursor's launcher is a bash wrapper that re-execs its bundled node with
// `exec -a "$0"`. tmux may therefore report `cursor-agent`, `agent`, or the
// shared runtime `node`. Identity follows Herdr: process name or a foreground
// argv[0] whose `agent` symlink resolves to cursor-agent, not activity phrases.
// A bare node remains ambiguous; only ProcessIdentity can make it Cursor.
//
// Status authority is the live bottom buffer. Session hooks report identity
// only (Herdr's model); do not reintroduce SQLite mtime as activity.
//
// The Go rule table is gone. Upstream carries every blocker it had —
// `write_file_approval` for the write prompt, `approval_prompt` for the shell,
// MCP, delete, web and edit shapes, which it reaches through their shared
// control lines ("keep (n)", "skip (esc or n)", "run … (y)") rather than
// through a regex per prompt — plus `stop_hint_working` and `spinner_working`.
// Three rules had no upstream equivalent and live on as overlay rules; see that
// file. One was dropped outright: `cursor.screen.activity-working`, an
// alternation over "running command", "working…", "starting…" and similar
// phrases anywhere in the bottom ten lines. It had no fixture, upstream has no
// counterpart, and every phrase in it is one a turn can print about its own
// work — which is how a finished turn that narrated "running command" kept a
// pane on the working lane.
//
// Two narrower branches went with it, and the Phase 2 review recorded why in
// docs/reference/herdr-detection-parity.md rather than restoring them. The
// write and web/edit prompts used to match on "Add to allowlist" or "reject &
// propose changes" *without* the "(y)" control line upstream also requires: the
// strings are in the cursor-cli 2026.08.04 harvest, but no captured screen
// renders either without a control line, and Cursor's fallback is low-evidence
// so the cost of being wrong is a missing badge rather than a false completion.
// And `cursor.screen.spinner-working` matched past-tense steps ("⬢ Thought 3s")
// beside the live one; a Cursor step list shows finished rows above the running
// row, so matching them is how a settled pane stays on the working lane.

// cursorScreenIdentity is a last-resort claim for the `agent` comm name
// when PATH does not resolve the binary. Herdr identifies Cursor from the
// process / symlink target only (never from screen). These phrases are the
// header/tagline, not activity hints that Codex and Grok also print.
var cursorScreenIdentity = regexp.MustCompile(`(?is)(Cursor Agent|Plan, search, build anything)`)

// lookUpAgentAlias reports the provider for a bare `agent` comm name by
// resolving $PATH once, matching Herdr's cursor-agent symlink test. Empty
// means "not Cursor." Tests replace this so Identify is not host-dependent.
var lookUpAgentAlias = lookupCursorAgentAlias

var (
	agentAliasOnce sync.Once
	agentAliasID   string
)

func lookupCursorAgentAlias() string {
	agentAliasOnce.Do(func() {
		path, err := exec.LookPath("agent")
		if err != nil {
			return
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolved = path
		}
		switch strings.ToLower(filepath.Base(resolved)) {
		case "cursor-agent", "cursor-agent.cmd", "cursor":
			agentAliasID = "cursor"
		}
	})
	return agentAliasID
}

// DetectCursor classifies a Cursor pane. The process gate runs first and refuses
// before any manifest is evaluated; everything after it is upstream's, plus the
// three overlay rules.
func DetectCursor(ob Observation) Result {
	if ob.Agent != "cursor" {
		return Result{State: StateUnknown, Evidence: "cursor.process-mismatch"}
	}
	return DetectManifestResult(ob)
}

// cursorProcess is Sidecar's refusal. Cursor is the one provider whose gate
// reads more than the command name: `agent` and `node` are shared, so
// processGate also accepts a resolved argv[0] of "cursor".
func cursorProcess(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "cursor-agent", "cursor", "cursor-agent.cmd", "agent":
		return true
	default:
		return false
	}
}
