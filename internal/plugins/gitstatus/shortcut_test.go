package gitstatus

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestCommaAndPeriodCycleFilesInFullScreenDiff(t *testing.T) {
	p := &Plugin{
		ctx: &plugin.Context{},
		tree: &FileTree{Modified: []*FileEntry{
			{Path: "one.go", Status: StatusModified},
			{Path: "two.go", Status: StatusModified},
		}},
		diffFile: "one.go",
	}

	_, cmd := p.updateDiff(tea.KeyPressMsg{Code: '.', Text: "."})
	if cmd == nil {
		t.Fatal("next-file . returned no load command")
	}
	if p.diffFile != "two.go" {
		t.Fatalf("diffFile after . = %q, want two.go", p.diffFile)
	}

	_, cmd = p.updateDiff(tea.KeyPressMsg{Code: ',', Text: ","})
	if cmd == nil {
		t.Fatal("prev-file , returned no load command")
	}
	if p.diffFile != "one.go" {
		t.Fatalf("diffFile after , = %q, want one.go", p.diffFile)
	}
}

func TestFileStepDistinguishesStagedAndUnstagedRowsForTheSamePath(t *testing.T) {
	p := &Plugin{
		ctx: &plugin.Context{},
		tree: &FileTree{
			Staged: []*FileEntry{{Path: "a.go", Status: StatusModified, Staged: true}},
			Modified: []*FileEntry{
				{Path: "a.go", Status: StatusModified},
				{Path: "b.go", Status: StatusModified},
			},
		},
		diffFile:   "a.go",
		diffStaged: true,
	}

	p.updateDiff(tea.KeyPressMsg{Code: '.', Text: "."})
	if p.diffFile != "a.go" || p.diffStaged {
		t.Fatalf("first . selected (%q, staged=%v), want unstaged a.go", p.diffFile, p.diffStaged)
	}
	entry := p.currentWorkingTreeDiffEntry()
	if entry == nil || entry.Staged {
		t.Fatalf("full-file lookup selected %#v, want unstaged a.go", entry)
	}
	p.updateDiff(tea.KeyPressMsg{Code: '.', Text: "."})
	if p.diffFile != "b.go" || p.diffStaged {
		t.Fatalf("second . selected (%q, staged=%v), want unstaged b.go", p.diffFile, p.diffStaged)
	}
}

func TestGitMenusBlockGlobalKeys(t *testing.T) {
	for _, mode := range []ViewMode{ViewModePushMenu, ViewModePullMenu, ViewModeConfirmDiscard, ViewModeBranchPicker, ViewModeConfirmStashPop, ViewModePullConflict, ViewModeError} {
		p := &Plugin{viewMode: mode}
		if !p.BlocksGlobalKeys() {
			t.Errorf("view mode %v does not block global keys", mode)
		}
	}
	if (&Plugin{viewMode: ViewModeDiff}).BlocksGlobalKeys() {
		t.Fatal("full-screen diff should allow global keys")
	}
}

// The full-screen diff has no tabs, so the braces are deliberately inert here
// rather than being reused for file stepping — { and } mean "cycle tabs"
// everywhere else in Sidecar, and a silent wrong action is worse than a no-op.
func TestBracesAreInertInFullScreenDiff(t *testing.T) {
	for _, key := range []rune{'{', '}'} {
		p := &Plugin{
			ctx: &plugin.Context{},
			tree: &FileTree{Modified: []*FileEntry{
				{Path: "one.go", Status: StatusModified},
				{Path: "two.go", Status: StatusModified},
			}},
			diffFile: "one.go",
		}
		p.updateDiff(tea.KeyPressMsg{Code: key, Text: string(key)})
		if p.diffFile != "one.go" {
			t.Fatalf("%q changed the file to %q; it must not step files", string(key), p.diffFile)
		}
	}
}

// The inline diff pane's horizontal-scroll reset moved from `0` to `|` when the
// header took over the whole number row (8/9/0 select Sessions/Activity/Tasks).
// `0` never reaches this plugin any more, so a binding on it is dead code that
// looks alive.
func TestPipeResetsInlineDiffHorizontalScroll(t *testing.T) {
	p := &Plugin{
		ctx:        &plugin.Context{},
		tree:       &FileTree{},
		activePane: PaneDiff,
	}

	p.diffPaneHorizScroll = 40
	p.diffPaneScroll = 12
	if _, cmd := p.updateStatusDiffPane(tea.KeyPressMsg{Code: '|', Text: "|"}); cmd != nil {
		t.Fatalf("| returned a command, want none")
	}
	if p.diffPaneHorizScroll != 0 {
		t.Errorf("diffPaneHorizScroll after | = %d, want 0", p.diffPaneHorizScroll)
	}
	if p.diffPaneScroll != 12 {
		t.Errorf("| moved vertical scroll to %d; it must only reset the horizontal axis (that is what g is for)", p.diffPaneScroll)
	}

	// `0` is the host's — keymap.GlobalKeys owns the whole number row — so the
	// plugin must not act on it even if it is handed one directly.
	p.diffPaneHorizScroll = 40
	p.updateStatusDiffPane(tea.KeyPressMsg{Code: '0', Text: "0"})
	if p.diffPaneHorizScroll != 40 {
		t.Errorf("0 still resets horizontal scroll; that binding is unreachable through the host and must not come back")
	}
}

// The new binding has to be discoverable, which the old one never was: it was
// in neither Commands() nor the keymap registry, so it appeared in no footer
// and in no help sheet.
func TestResetHscrollIsDiscoverable(t *testing.T) {
	p := &Plugin{ctx: &plugin.Context{}}
	found := false
	for _, cmd := range p.Commands() {
		if cmd.ID == "reset-hscroll" && cmd.Context == "git-status-diff" {
			found = true
		}
	}
	if !found {
		t.Error("reset-hscroll is not in Commands() for git-status-diff, so it cannot reach the footer or ?")
	}

	// The footer pairs a command with a key from the keymap registry, so a
	// command with no binding is still invisible.
	bound := ""
	for _, b := range keymap.DefaultBindings() {
		if b.Context != "git-status-diff" {
			continue
		}
		if b.Command == "reset-hscroll" {
			bound = b.Key
		}
	}
	if bound != "|" {
		t.Errorf("reset-hscroll is bound to %q in git-status-diff, want |", bound)
	}
}
