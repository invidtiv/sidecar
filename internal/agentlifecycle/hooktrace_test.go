package agentlifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase D traced Codex and Claude Code, which are hook-shaped rather than
// bus-shaped: a provider runs one short-lived process per event instead of
// emitting onto a stream a plugin subscribes to. Their traces therefore have a
// different column layout from the Phase A/B OpenCode files, and their own
// reader.
//
// These tests exist for the same reason the OpenCode trace tests do: every
// claim in capabilities.json that says "traced" should be a claim something
// reads back and asserts, so that deleting or altering a trace breaks a test
// rather than quietly turning a measurement into a sentence.

// hookRow is one sanitized hook-trace line: the relative millisecond offset,
// the provider's event name, placeholder session and turn identifiers, the tool
// name where there was one, and the payload's field NAMES.
//
// Field names are carried because they are evidence in their own right. A
// payload with a field called "prompt" is a payload Sidecar must never persist,
// and recording the name costs nothing while recording the value would be the
// exact privacy failure the plan forbids.
type hookRow struct {
	event   string
	session string
	turn    string
	tool    string
	fields  []string
}

func readHookTrace(t *testing.T, provider, name string) []hookRow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "traces", provider, name))
	if err != nil {
		t.Fatal(err)
	}
	var rows []hookRow
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 6 {
			t.Fatalf("malformed hook trace row in %s/%s: %q", provider, name, line)
		}
		rows = append(rows, hookRow{
			event: cols[1], session: cols[2], turn: cols[3], tool: cols[4],
			fields: strings.Split(cols[5], ","),
		})
	}
	return rows
}

func eventsOf(rows []hookRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.event)
	}
	return out
}

func assertEvents(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event sequence %v, want %v", got, want)
	}
}

// TestNoHookTraceCarriesAValue is the privacy gate over the fixtures
// themselves. The traces record which fields a payload had, and a field named
// "prompt" or "tool_input" is exactly the kind of thing that must never appear
// with a value beside it.
func TestNoHookTraceCarriesAValue(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		entries, err := os.ReadDir(filepath.Join("testdata", "traces", provider))
		if err != nil {
			t.Fatalf("%s has no traces but its capability entry claims real evidence: %v", provider, err)
		}
		for _, e := range entries {
			rows := readHookTrace(t, provider, e.Name())
			if len(rows) == 0 {
				t.Fatalf("%s/%s is empty", provider, e.Name())
			}
			for _, r := range rows {
				if !strings.HasPrefix(r.session, "session-") && r.session != "-" {
					t.Fatalf("%s/%s carries a real session identifier %q", provider, e.Name(), r.session)
				}
				if !strings.HasPrefix(r.turn, "turn-") && r.turn != "-" {
					t.Fatalf("%s/%s carries a real turn identifier %q", provider, e.Name(), r.turn)
				}
			}
		}
	}
}

// TestCodexTraceProvesEveryFullLifecycleTransition is the evidence behind the
// recorded claim that Codex's own contract would support the full tier. It is
// asserted rather than described, so removing a trace or changing its shape
// fails here instead of leaving the registry asserting something unbacked.
func TestCodexTraceProvesEveryFullLifecycleTransition(t *testing.T) {
	// Work start, tool use, turn completion, session identity.
	assertEvents(t, eventsOf(readHookTrace(t, "codex", "exec-tool-turn.tsv")),
		"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop")

	// Blocking and unblocking. PermissionRequest is the last event before the
	// pane blocks, and PostToolUse is what follows an approval.
	assertEvents(t, eventsOf(readHookTrace(t, "codex", "permission-approved.tsv")),
		"UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse", "Stop")

	// Process exit.
	assertEvents(t, eventsOf(readHookTrace(t, "codex", "session-end.tsv")), "SessionEnd")
}

// TestCodexResolvesABlockedPaneByTwoDifferentEvents is the finding that decides
// whether a Codex adapter can be trusted with the blocked lane at all.
//
// Approval and denial do NOT converge on the same event. An adapter that
// unblocked only on PostToolUse would sit on `blocked` forever every time a
// user said no, which is precisely the latch that keeps Claude Code below full
// authority. Codex escapes it only because Interrupt covers the refusal path.
func TestCodexResolvesABlockedPaneByTwoDifferentEvents(t *testing.T) {
	approved := eventsOf(readHookTrace(t, "codex", "permission-approved.tsv"))
	denied := eventsOf(readHookTrace(t, "codex", "permission-denied.tsv"))

	assertEvents(t, denied, "UserPromptSubmit", "PreToolUse", "PermissionRequest", "Interrupt")

	if approved[len(approved)-1] == denied[len(denied)-1] {
		t.Fatal("the fixture no longer shows approval and denial ending differently, which is the whole finding")
	}
	for _, e := range denied {
		if e == "Stop" || e == "PostToolUse" {
			t.Fatalf("the denial trace contains %s; the recorded finding is that it contains neither", e)
		}
	}
}

// TestCodexCancellationIsFirstClass pins the transition OpenCode had to infer
// from an error class name. Codex has a dedicated event for it.
func TestCodexCancellationIsFirstClass(t *testing.T) {
	rows := readHookTrace(t, "codex", "cancelled-turn.tsv")
	assertEvents(t, eventsOf(rows), "UserPromptSubmit", "Interrupt")
	if rows[1].turn != rows[0].turn {
		t.Fatal("the interrupt does not carry the turn it cancelled, so it could not be attributed")
	}
}

// TestClaudeCancellationEmitsNothingAtAll is the contract gap, asserted.
//
// This is the single fact that caps Claude Code below full lifecycle authority,
// and it is a fact about what is ABSENT -- which is exactly the kind of claim
// that rots silently. A future Claude release that starts emitting something
// here should break this test, because that is the signal to requalify.
func TestClaudeCancellationEmitsNothingAtAll(t *testing.T) {
	rows := readHookTrace(t, "claude", "interrupted-turn.tsv")
	assertEvents(t, eventsOf(rows), "UserPromptSubmit")

	// Escape-cancelling a permission prompt is the other cancellation route and
	// is equally silent: the trace ends on Notification, with the user's answer
	// producing nothing.
	cancelled := eventsOf(readHookTrace(t, "claude", "permission-cancelled.tsv"))
	assertEvents(t, cancelled, "UserPromptSubmit", "PreToolUse", "PermissionRequest", "Notification")
}

// TestClaudeSkipsPostToolUseAfterAPermissionPrompt pins the second Claude
// finding, which is more insidious than the first because the event that goes
// missing is one that fires perfectly well on every turn that did not block.
func TestClaudeSkipsPostToolUseAfterAPermissionPrompt(t *testing.T) {
	plain := eventsOf(readHookTrace(t, "claude", "print-mode-tool-turn.tsv"))
	prompted := eventsOf(readHookTrace(t, "claude", "permission-approved-skips-posttooluse.tsv"))

	if !contains(plain, "PostToolUse") {
		t.Fatal("the unprompted trace lost PostToolUse, so the comparison proves nothing")
	}
	if contains(prompted, "PostToolUse") {
		t.Fatal("the prompted trace now contains PostToolUse; the recorded finding is that it does not")
	}
	if !contains(prompted, "Stop") {
		t.Fatal("the approved turn did not complete, so this is not the case the finding describes")
	}
}

// TestClaudeBlockingIsFirstClassOnTheCurrentRelease is the correction. An
// earlier docs-only reading recorded Claude as offering session identity and
// nothing more; PermissionRequest fires, and so does a Notification.
func TestClaudeBlockingIsFirstClassOnTheCurrentRelease(t *testing.T) {
	blocked := eventsOf(readHookTrace(t, "claude", "permission-denied.tsv"))
	if !contains(blocked, "PermissionRequest") {
		t.Fatal("PermissionRequest is absent, which would restore the stale reading of this provider")
	}
	if !contains(blocked, "Notification") {
		t.Fatal("Notification is absent from the blocked trace")
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
