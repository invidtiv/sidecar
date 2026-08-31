package shellstate

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
)

// manifestBytes is the whole file, which is what a watcher diffs and therefore
// the honest way to ask "did this write anything".
func manifestBytes(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// poison edits a record directly, because the state it produces is one no
// current writer can produce — that is the whole reason the remediation exists.
func poison(t *testing.T, path, name string, edit func(*Definition)) {
	t.Helper()
	err := mutateManifest(path, func(m *manifest) error {
		for i := range m.Shells {
			if m.Shells[i].TmuxName == name {
				def := m.Shells[i]
				edit(&def)
				m.Shells[i] = def
				return nil
			}
		}
		t.Fatalf("no record named %s", name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func defAt(t *testing.T, path, name string) Definition {
	t.Helper()
	defs, err := ListAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range defs {
		if def.TmuxName == name {
			return def
		}
	}
	t.Fatalf("no record named %s", name)
	return Definition{}
}

// A shell whose provider was started by `sidecar agent start` used to record no
// kind at all, so the bind-time gate that refuses a mis-attributed hook report
// had nothing to compare against — for exactly the shells the coordinate-agents
// sequence produces.
func TestAStartedAgentKindIsRecordedSoTheReportGateCanUseIt(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	id := Identity{TmuxName: "sidecar-sh-p-1", Namespace: testNS}

	if got := defAt(t, path, id.TmuxName); got.AgentType != "" {
		t.Fatalf("fixture already records a kind: %q", got.AgentType)
	}

	if err := RecordAgentKindAtPath(path, id, "grok"); err != nil {
		t.Fatal(err)
	}
	after := defAt(t, path, id.TmuxName)
	if after.AgentType != "grok" {
		t.Fatalf("AgentType = %q, want grok", after.AgentType)
	}
	if after.Agent == nil || after.Agent.Kind != "grok" {
		t.Fatalf("Agent.Kind = %#v, want grok; both fields are written together or a reader picks a side", after.Agent)
	}

	// The point of recording it: a claude hook firing inside this grok shell is
	// now refused without any process table, on every platform.
	_, err := BindSessionAtPath(path, id, SessionUpdate{
		Ref: reportRef("grok-session-1", testLive), Kind: "claude", Live: testLive,
	})
	if !errors.Is(err, agentsession.ErrKindMismatch) {
		t.Fatalf("a claude report inside a started-grok shell was accepted: %v", err)
	}
	if _, _, bound, refErr := SessionRefAtPath(path, id); refErr != nil || bound {
		t.Fatalf("the refused report still bound something: bound=%v err=%v", bound, refErr)
	}
}

func TestRecordingTheSameKindTwiceDoesNotRewriteTheManifest(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	id := Identity{TmuxName: "sidecar-sh-p-1", Namespace: testNS}

	if err := RecordAgentKindAtPath(path, id, "codex"); err != nil {
		t.Fatal(err)
	}
	first := manifestBytes(t, path)
	if err := RecordAgentKindAtPath(path, id, "codex"); err != nil {
		t.Fatal(err)
	}
	if second := manifestBytes(t, path); second != first {
		t.Fatalf("a repeated start rewrote the manifest:\nbefore %s\nafter  %s", first, second)
	}
}

// Starting a different provider in a shell invalidates the previous provider's
// conversation. Keeping it is precisely how a restore comes to offer one
// agent's conversation to another agent's CLI.
func TestStartingADifferentProviderDropsThePreviousConversation(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	id := Identity{TmuxName: "sidecar-sh-p-1", Namespace: testNS}

	if err := RecordAgentKindAtPath(path, id, "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := BindSessionAtPath(path, id, SessionUpdate{
		Ref: reportRef("codex-session-1", testLive), Kind: "codex", Live: testLive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, bound, err := SessionRefAtPath(path, id); err != nil || !bound {
		t.Fatalf("setup did not bind a conversation: bound=%v err=%v", bound, err)
	}

	if err := RecordAgentKindAtPath(path, id, "claude"); err != nil {
		t.Fatal(err)
	}
	after := defAt(t, path, id.TmuxName)
	if after.AgentType != "claude" || after.Agent == nil || after.Agent.Kind != "claude" {
		t.Fatalf("record after the new start = %#v", after)
	}
	if after.Agent.Session != nil {
		t.Fatalf("codex's conversation survived a claude start: %#v", after.Agent.Session)
	}
	// And the record still agrees with itself, so nothing downstream refuses.
	if _, conflict := AgentKindOf(after); conflict != "" {
		t.Fatalf("recording a new kind produced a self-contradicting record (conflict %q)", conflict)
	}
}

func TestRecordAgentKindRefusesInputItCannotActOn(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	id := Identity{TmuxName: "sidecar-sh-p-1", Namespace: testNS}

	if err := RecordAgentKindAtPath(path, id, "  "); !IsValidation(err) {
		t.Fatalf("an empty kind error = %v, want a validation error", err)
	}
	if err := RecordAgentKindAtPath(path, Identity{Namespace: testNS}, "codex"); !IsValidation(err) {
		t.Fatalf("an empty session name error = %v, want a validation error", err)
	}
	if err := RecordAgentKindAtPath(path, Identity{TmuxName: "nope", Namespace: testNS}, "codex"); !IsNotFound(err) {
		t.Fatalf("an unknown shell error = %v, want not-found", err)
	}
}

// AgentKindOf is the one place that answers "which provider does this record
// name" — and reports when it names two.
func TestAgentKindOfReportsADisagreementInsteadOfPickingAField(t *testing.T) {
	cases := []struct {
		name         string
		def          Definition
		wantKind     string
		wantConflict string
	}{
		{"empty record", Definition{}, "", ""},
		{"v2 record with only agentType", Definition{AgentType: "codex"}, "codex", ""},
		{"v3 record with only the binding", Definition{Agent: &AgentBinding{Kind: "codex"}}, "codex", ""},
		{"both agreeing", Definition{AgentType: "codex", Agent: &AgentBinding{Kind: "codex"}}, "codex", ""},
		{"the poisoned shape", Definition{AgentType: "grok", Agent: &AgentBinding{Kind: "claude"}}, "claude", "grok"},
		{"the poisoned shape reversed", Definition{AgentType: "claude", Agent: &AgentBinding{Kind: "grok"}}, "grok", "claude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, conflict := AgentKindOf(tc.def)
			if kind != tc.wantKind || conflict != tc.wantConflict {
				t.Fatalf("AgentKindOf = (%q, %q), want (%q, %q)", kind, conflict, tc.wantKind, tc.wantConflict)
			}
		})
	}
}

// The read half refuses rather than answering, because every way of answering
// hands one provider's CLI another provider's conversation id.
func TestAKindDisagreementRefusesResume(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	id := Identity{TmuxName: "sidecar-sh-p-1", Namespace: testNS}

	// The shape the shipped bug produced, written the way it was written: a
	// grok shell that accepted a claude report before the gate existed.
	poison(t, path, id.TmuxName, func(def *Definition) {
		def.AgentType = "grok"
		def.Agent = &AgentBinding{Kind: "claude", Session: &agentsession.Ref{
			Kind: agentsession.RefID, Value: "grok-session-1",
			Source: "sidecar.claude.hooks", Reported: true,
			ReportedAt: time.Now().UTC(),
		}}
	})

	ref, kind, bound, err := SessionRefAtPath(path, id)
	if !errors.Is(err, ErrKindDisagreement) {
		t.Fatalf("SessionRefAtPath on a poisoned record = (%#v, %q, %v, %v); want ErrKindDisagreement",
			ref, kind, bound, err)
	}
	if bound {
		t.Fatal("a poisoned record still reported a usable binding")
	}
	if ref.Value != "" {
		t.Fatalf("the refusal still handed back the conversation value %q", ref.Value)
	}
	// Both providers are named, because the point of the refusal is that the
	// user has to decide which half is the mistake.
	for _, want := range []string{"grok", "claude"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal %q does not name %q", err, want)
		}
	}
}

// A record every current writer produces is unaffected: the refusal must not
// cost a healthy shell its resume.
func TestAnAgreeingRecordStillResolvesItsConversation(t *testing.T) {
	path := seedShell(t, "sidecar-sh-p-1")
	id := Identity{TmuxName: "sidecar-sh-p-1", Namespace: testNS}

	if _, err := BindSessionAtPath(path, id, SessionUpdate{
		Ref: reportRef("codex-session-1", testLive), Kind: "codex", Live: testLive,
	}); err != nil {
		t.Fatal(err)
	}
	ref, kind, bound, err := SessionRefAtPath(path, id)
	if err != nil || !bound || kind != "codex" || ref.Value != "codex-session-1" {
		t.Fatalf("a healthy record = (%#v, %q, %v, %v)", ref, kind, bound, err)
	}
}

// CarryForward's rule is that a writer which does not model a field must carry
// it. AgentType is modeled by the workspace serializer only for a shell whose
// in-memory session carries a chosen agent, so an adopted shell wrote an empty
// one over a recorded kind — silently disarming the bind-time gate.
func TestCarryForwardKeepsARecordedAgentKindAWriterDoesNotModel(t *testing.T) {
	prior := Definition{
		TmuxName: "sidecar-sh-p-1", AgentType: "grok",
		Agent: &AgentBinding{Kind: "grok"},
	}

	t.Run("an empty kind is silence, not a value", func(t *testing.T) {
		next := CarryForward(prior, Definition{TmuxName: "sidecar-sh-p-1"})
		if next.AgentType != "grok" {
			t.Fatalf("AgentType = %q, want grok carried forward", next.AgentType)
		}
	})

	t.Run("a writer that states a kind still wins", func(t *testing.T) {
		next := CarryForward(prior, Definition{TmuxName: "sidecar-sh-p-1", AgentType: "codex"})
		if next.AgentType != "codex" {
			t.Fatalf("AgentType = %q, want the new writer's codex", next.AgentType)
		}
	})
}
