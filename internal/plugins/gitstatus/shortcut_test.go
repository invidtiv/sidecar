package gitstatus

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestBracesCycleFilesInFullScreenDiff(t *testing.T) {
	p := &Plugin{
		ctx: &plugin.Context{},
		tree: &FileTree{Modified: []*FileEntry{
			{Path: "one.go", Status: StatusModified},
			{Path: "two.go", Status: StatusModified},
		}},
		diffFile: "one.go",
	}

	_, cmd := p.updateDiff(tea.KeyPressMsg{Code: '}', Text: "}"})
	if cmd == nil {
		t.Fatal("next-file brace returned no load command")
	}
	if p.diffFile != "two.go" {
		t.Fatalf("diffFile after } = %q, want two.go", p.diffFile)
	}

	_, cmd = p.updateDiff(tea.KeyPressMsg{Code: '{', Text: "{"})
	if cmd == nil {
		t.Fatal("prev-file brace returned no load command")
	}
	if p.diffFile != "one.go" {
		t.Fatalf("diffFile after { = %q, want one.go", p.diffFile)
	}
}

func TestBracesDistinguishStagedAndUnstagedRowsForTheSamePath(t *testing.T) {
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

	p.updateDiff(tea.KeyPressMsg{Code: '}', Text: "}"})
	if p.diffFile != "a.go" || p.diffStaged {
		t.Fatalf("first } selected (%q, staged=%v), want unstaged a.go", p.diffFile, p.diffStaged)
	}
	entry := p.currentWorkingTreeDiffEntry()
	if entry == nil || entry.Staged {
		t.Fatalf("full-file lookup selected %#v, want unstaged a.go", entry)
	}
	p.updateDiff(tea.KeyPressMsg{Code: '}', Text: "}"})
	if p.diffFile != "b.go" || p.diffStaged {
		t.Fatalf("second } selected (%q, staged=%v), want unstaged b.go", p.diffFile, p.diffStaged)
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
