package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
)

func TestROnWorktreeOpensRenameModal(t *testing.T) {
	wt := &Worktree{Name: "auth-refresh", Path: "/tmp/auth-refresh", Branch: "auth-refresh"}
	p := &Plugin{worktrees: []*Worktree{wt}, selectedIdx: 0}

	p.handleListKeys(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if p.viewMode != ViewModeRenameWorktree || p.renameWorktree != wt {
		t.Fatalf("viewMode=%v worktree=%#v, want rename-worktree modal", p.viewMode, p.renameWorktree)
	}
	if p.renameWorktreeInput.Value() != "auth-refresh" {
		t.Fatalf("prefill = %q, want auth-refresh", p.renameWorktreeInput.Value())
	}

	p.ensureRenameWorktreeModal()
	if p.renameWorktreeModal == nil {
		t.Fatal("rename worktree modal was not built")
	}
	view := ansi.Strip(p.renameWorktreeModal.Render(80, 24, mouse.NewHandler()))
	if !strings.Contains(view, "Rename Worktree") {
		t.Fatalf("modal missing title:\n%s", view)
	}
	if !strings.Contains(view, "Current:") || !strings.Contains(view, "auth-refresh") {
		t.Fatalf("modal missing current name:\n%s", view)
	}
}

func TestROnShellStillOpensRenameShell(t *testing.T) {
	shell := &ShellSession{Name: "charlie", TmuxName: "sidecar-sh-one"}
	wt := &Worktree{Name: "auth-refresh", Path: "/tmp/auth-refresh", Branch: "auth-refresh"}
	p := &Plugin{
		shells:           []*ShellSession{shell},
		selectedShellIdx: 0,
		shellSelected:    true,
		worktrees:        []*Worktree{wt},
		selectedIdx:      0,
	}

	p.handleListKeys(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if p.viewMode != ViewModeRenameShell || p.renameShellSession != shell {
		t.Fatalf("viewMode=%v shell=%#v, want rename-shell modal", p.viewMode, p.renameShellSession)
	}
	if p.renameWorktree != nil || p.viewMode == ViewModeRenameWorktree {
		t.Fatal("R on a shell opened the worktree rename modal")
	}
}

func TestRenameWorktreePersistsDisplayNameOnly(t *testing.T) {
	config.SetTestStateDir(t.TempDir())
	t.Cleanup(config.ResetTestStateDir)

	r := newCreateRepo(t)
	wt := &Worktree{Name: filepath.Base(r.linked), Path: r.linked, Branch: "staging"}
	projectState, err := projectdir.Resolve(r.main)
	if err != nil {
		t.Fatal(err)
	}
	shellsPath := filepath.Join(projectState, "shells.json")
	if err := os.WriteFile(shellsPath, []byte(`{"version":1,"shells":[{"tmuxName":"sidecar-sh-one","displayName":"charlie"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(shellsPath)
	if err != nil {
		t.Fatal(err)
	}

	input := textinputNew("Auth Refresh")
	p := &Plugin{
		viewMode:            ViewModeRenameWorktree,
		renameWorktree:      wt,
		renameWorktreeInput: input,
		worktrees:           []*Worktree{wt},
		ctx:                 &plugin.Context{ProjectRoot: r.main, WorkDir: r.linked},
	}

	cmd := p.executeRenameWorktree()
	if cmd == nil {
		t.Fatal("valid rename returned no persistence command")
	}
	msg, ok := cmd().(RenameWorktreeDoneMsg)
	if !ok || msg.Err != nil {
		t.Fatalf("rename result = %#v", msg)
	}
	p.update(msg)
	if p.viewMode != ViewModeList {
		t.Fatalf("successful rename left viewMode=%v error=%q", p.viewMode, p.renameWorktreeError)
	}
	if wt.Name != "Auth Refresh" {
		t.Fatalf("in-memory name = %q, want Auth Refresh", wt.Name)
	}
	if wt.Branch != "staging" {
		t.Fatalf("branch = %q, want staging", wt.Branch)
	}
	if wt.Path != r.linked {
		t.Fatalf("path = %q, want %q", wt.Path, r.linked)
	}
	if got := loadDisplayName(r.main, r.linked); got != "Auth Refresh" {
		t.Fatalf("persisted display name = %q", got)
	}
	if branch := mustGit(t, r.linked, "rev-parse", "--abbrev-ref", "HEAD"); branch != "staging" {
		t.Fatalf("git branch = %q, want staging", branch)
	}
	if _, err := os.Stat(r.linked); err != nil {
		t.Fatalf("worktree directory moved or removed: %v", err)
	}
	after, err := os.ReadFile(shellsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("shells.json rewritten:\n%s", after)
	}
}

func TestRenameWorktreeModalUsesSharedValidation(t *testing.T) {
	for _, name := range []string{"", strings.Repeat("a", 51), strings.Repeat("é", 26)} {
		t.Run(name, func(t *testing.T) {
			p := &Plugin{
				renameWorktree:      &Worktree{Name: "old", Path: "/tmp/old", Branch: "old"},
				renameWorktreeInput: textinputNew(name),
			}
			if cmd := p.executeRenameWorktree(); cmd != nil {
				t.Fatal("invalid modal name scheduled persistence")
			}
			_, sharedErr := shellstate.NormalizeName(name)
			if sharedErr == nil || p.renameWorktreeError != sharedErr.Error() {
				t.Fatalf("modal error = %q, shared error = %v", p.renameWorktreeError, sharedErr)
			}
		})
	}
}

func TestWorktreeCommandsAdvertiseRename(t *testing.T) {
	wt := &Worktree{Name: "auth-refresh", Path: "/tmp/auth-refresh", Branch: "auth-refresh"}
	p := &Plugin{worktrees: []*Worktree{wt}, selectedIdx: 0}

	var found bool
	for _, cmd := range p.Commands() {
		if cmd.ID == "rename-worktree" && cmd.Name == "Rename" {
			found = true
		}
		if cmd.ID == "rename-shell" {
			t.Fatalf("worktree Commands() advertised rename-shell: %#v", p.Commands())
		}
	}
	if !found {
		t.Fatalf("worktree Commands() omitted Rename: %#v", p.Commands())
	}

	p.shells = []*ShellSession{{Name: "charlie", TmuxName: "sidecar-sh-one"}}
	p.shellSelected = true
	foundShell := false
	for _, cmd := range p.Commands() {
		if cmd.ID == "rename-shell" && cmd.Name == "Rename" {
			foundShell = true
		}
		if cmd.ID == "rename-worktree" {
			t.Fatalf("shell Commands() advertised rename-worktree: %#v", p.Commands())
		}
	}
	if !foundShell {
		t.Fatalf("shell Commands() omitted Rename: %#v", p.Commands())
	}
}

func textinputNew(value string) textinput.Model {
	input := textinput.New()
	input.SetValue(value)
	return input
}
