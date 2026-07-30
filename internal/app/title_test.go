package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
)

func titleModel(template string) Model {
	return Model{
		registry:      plugin.NewRegistry(nil),
		titleTemplate: template,
		intro:         IntroModel{RepoName: "sidecar"},
		ui:            &UIState{WorkDir: "/Users/marcus/code/sidecar"},
	}
}

func TestTerminalTitle(t *testing.T) {
	t.Run("project name on the main worktree", func(t *testing.T) {
		m := titleModel("{project}{worktree}")
		if got, want := m.terminalTitle(), "sidecar"; got != want {
			t.Errorf("terminalTitle() = %q, want %q", got, want)
		}
	})

	t.Run("linked worktree appends its branch", func(t *testing.T) {
		m := titleModel("{project}{worktree}")
		m.cachedWorktreeInfo = &WorktreeInfo{Branch: "charm", IsMain: false}
		if got, want := m.terminalTitle(), "sidecar [charm]"; got != want {
			t.Errorf("terminalTitle() = %q, want %q", got, want)
		}
	})

	t.Run("main worktree branch is not shown", func(t *testing.T) {
		m := titleModel("{project}{worktree}")
		m.cachedWorktreeInfo = &WorktreeInfo{Branch: "main", IsMain: true}
		if got, want := m.terminalTitle(), "sidecar"; got != want {
			t.Errorf("terminalTitle() = %q, want %q", got, want)
		}
	})

	t.Run("detached worktree falls back to a label", func(t *testing.T) {
		m := titleModel("{project}{worktree}")
		m.cachedWorktreeInfo = &WorktreeInfo{Branch: "", IsMain: false}
		if got, want := m.terminalTitle(), "sidecar [worktree]"; got != want {
			t.Errorf("terminalTitle() = %q, want %q", got, want)
		}
	})

	t.Run("dir variable", func(t *testing.T) {
		m := titleModel("{dir}")
		if got, want := m.terminalTitle(), "sidecar"; got != want {
			t.Errorf("terminalTitle() = %q, want %q", got, want)
		}
	})

	// GetRepoName returns "" outside a git repository. Without a fallback the
	// default template would render nothing there, and sidecar would either say
	// nothing at all or blank the title the user's shell had set.
	t.Run("falls back to the directory outside a git repo", func(t *testing.T) {
		m := titleModel("{project}{worktree}")
		m.intro.RepoName = ""
		if got, want := m.terminalTitle(), "sidecar"; got != want {
			t.Errorf("terminalTitle() = %q, want %q", got, want)
		}
	})

	t.Run("falls back when a custom template renders empty", func(t *testing.T) {
		m := titleModel("{worktree}")
		if got, want := m.terminalTitle(), "sidecar"; got != want {
			t.Errorf("terminalTitle() = %q, want %q", got, want)
		}
	})

	t.Run("empty template leaves the title alone", func(t *testing.T) {
		m := titleModel("")
		if got := m.terminalTitle(); got != "" {
			t.Errorf("terminalTitle() = %q, want empty", got)
		}
	})
}

// The title must not ride on tea.View.WindowTitle: Bubble Tea clears a title it
// owns whenever the renderer stops, which blanks the terminal for the whole
// duration of every tea.ExecProcess child — an attached session, most of all.
func TestViewDoesNotOwnTheTitle(t *testing.T) {
	// View() renders the whole app, so it needs the keymap the footer reads.
	m := titleModel("{project}{worktree}")
	m.keymap = keymap.NewRegistry()
	m.cfg = &config.Config{}
	m.width, m.height = 100, 40
	m.ready = true

	if got := m.View().WindowTitle; got != "" {
		t.Errorf("View().WindowTitle = %q, want empty — sidecar emits the title itself", got)
	}
}

func TestSyncTerminalTitleOnlyEmitsOnChange(t *testing.T) {
	m := titleModel("{project}{worktree}")

	cmd := m.syncTerminalTitle(false)
	if cmd == nil {
		t.Fatal("syncTerminalTitle() returned nil for the first title")
	}
	raw, ok := cmd().(tea.RawMsg)
	if !ok {
		t.Fatalf("syncTerminalTitle() produced %T, want tea.RawMsg", cmd())
	}
	if got, want := raw.Msg, "\x1b]0;sidecar\x07"; got != want {
		t.Errorf("raw sequence = %q, want %q", got, want)
	}

	if cmd := m.syncTerminalTitle(false); cmd != nil {
		t.Error("syncTerminalTitle() re-emitted an unchanged title")
	}

	m.intro.RepoName = "td"
	if cmd := m.syncTerminalTitle(false); cmd == nil {
		t.Error("syncTerminalTitle() did not emit after the project changed")
	}
}

// A program run through tea.ExecProcess can leave its own icon name behind, and
// nothing reports back that it finished — the forced resync is what recovers it.
func TestSyncTerminalTitleForceReemitsUnchanged(t *testing.T) {
	m := titleModel("{project}{worktree}")
	if cmd := m.syncTerminalTitle(false); cmd == nil {
		t.Fatal("syncTerminalTitle() returned nil for the first title")
	}
	if cmd := m.syncTerminalTitle(true); cmd == nil {
		t.Error("syncTerminalTitle(force) did not re-emit an unchanged title")
	}
}

// Emitting an empty title would wipe the name the user's shell had set, so a
// title that renders to nothing is never sent — even on the forced resync.
func TestSyncTerminalTitleNeverEmitsEmpty(t *testing.T) {
	m := titleModel("{project}")
	m.intro.RepoName = ""
	m.ui.WorkDir = "" // no project name and no directory to fall back to
	if cmd := m.syncTerminalTitle(false); cmd != nil {
		t.Errorf("syncTerminalTitle() = %v, want nil for an empty title", cmd())
	}
	if cmd := m.syncTerminalTitle(true); cmd != nil {
		t.Errorf("syncTerminalTitle(force) = %v, want nil for an empty title", cmd())
	}
}

func TestSyncTerminalTitleDisabled(t *testing.T) {
	m := titleModel("")
	if cmd := m.syncTerminalTitle(false); cmd != nil {
		t.Error("syncTerminalTitle() emitted with an empty template")
	}
	if cmd := m.syncTerminalTitle(true); cmd != nil {
		t.Error("syncTerminalTitle(force) emitted with an empty template")
	}
}
