package worktreedelete

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/mouse"
)

func render(t *testing.T, s *State) string {
	t.Helper()
	built := s.Modal(80)
	if built == nil {
		t.Fatal("no modal was built for an open confirmation")
	}
	return built.Render(80, 24, mouse.NewHandler())
}

func armed(target Target, isMain bool) *State {
	s := &State{}
	s.Open(target, isMain)
	return s
}

func TestConfirmationNamesTheWorktreeAndItsConsequences(t *testing.T) {
	s := armed(Target{Name: "feature", Branch: "feature-branch", Path: "/tmp/feature"}, false)
	// The warning is now conditional, so this consequence only appears for a
	// worktree git reported dirty — see dirtiness_test.go.
	s.Dirty = DirtinessDirty
	view := render(t, s)

	for _, want := range []string{Title, "feature", "feature-branch", "/tmp/feature",
		"This will:", "Remove the working directory", DirtyLine,
		"Delete local branch", " Delete ", " Cancel "} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation is missing %q:\n%s", want, view)
		}
	}
}

func TestMissingWorktreeOffersCleanupWordingAndPreTicksTheBranch(t *testing.T) {
	s := armed(Target{Name: "gone", Branch: "gone-branch", Path: "/tmp/gone", IsMissing: true}, false)
	if !s.DeleteLocal {
		t.Fatal("a worktree whose directory is gone did not pre-tick its branch cleanup")
	}
	view := render(t, s)
	if !strings.Contains(view, "Directory already removed") || !strings.Contains(view, "Clean up git worktree metadata") {
		t.Fatalf("missing-worktree wording absent:\n%s", view)
	}
}

func TestTheMainBranchIsProtectedFromBranchCleanup(t *testing.T) {
	s := armed(Target{Name: "main", Branch: "main", Path: "/tmp/main"}, true)
	view := render(t, s)
	if strings.Contains(view, "Delete local branch") || strings.Contains(view, "Branch Cleanup") {
		t.Fatalf("the main branch was offered branch cleanup:\n%s", view)
	}
}

func TestTheRemoteBoxAppearsOnlyOnceARemoteBranchIsKnown(t *testing.T) {
	s := armed(Target{Name: "feature", Branch: "feature", Path: "/tmp/feature"}, false)
	if strings.Contains(render(t, s), "Delete remote branch") {
		t.Fatal("the remote branch box appeared before a remote branch was found")
	}
	s.HasRemote = true
	s.Invalidate()
	view := render(t, s)
	if !strings.Contains(view, "Delete remote branch") || !strings.Contains(view, "origin/feature") {
		t.Fatalf("the remote branch box is missing:\n%s", view)
	}

	// A ticked remote box means nothing while no remote branch exists.
	s.DeleteRemote = true
	s.HasRemote = false
	if s.DeleteRemoteBranch() {
		t.Fatal("a ticked remote box asked to delete a branch origin does not have")
	}
}

func TestKeysDecideTheConfirmation(t *testing.T) {
	cases := map[string]Outcome{
		"D":   OutcomeConfirm,
		"esc": OutcomeCancel,
		"q":   OutcomeCancel,
		"j":   OutcomeNone,
	}
	for key, want := range cases {
		s := armed(Target{Name: "feature", Branch: "feature", Path: "/tmp/feature"}, false)
		render(t, s)
		got, _ := s.HandleKey(80, keyPress(key))
		if got != want {
			t.Fatalf("%q produced outcome %v, want %v", key, got, want)
		}
	}
}

func TestAClosedConfirmationBuildsNothingAndDecidesNothing(t *testing.T) {
	s := &State{}
	if s.Active() {
		t.Fatal("the zero value reports an open confirmation")
	}
	if s.Modal(80) != nil {
		t.Fatal("a closed confirmation built a modal")
	}
	if outcome, _ := s.HandleKey(80, keyPress("D")); outcome != OutcomeNone {
		t.Fatalf("a closed confirmation answered D with %v", outcome)
	}

	s.Open(Target{Name: "feature"}, false)
	s.Clear()
	if s.Active() || s.Modal(80) != nil {
		t.Fatal("Clear left the confirmation armed")
	}
}

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
}
