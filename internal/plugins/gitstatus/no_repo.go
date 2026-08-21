package gitstatus

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/gitinit"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
)

// sidecarGitignoreEntries is kept as an alias so existing tests keep working.
var sidecarGitignoreEntries = gitinit.SidecarGitignoreEntries

const (
	regionInitRepo     = "git-init-repo"
	initRepoButtonText = " Initialize Git Repository "
	// RenderPanel draws a 1-cell border; content therefore starts at (2, 1)
	// after the left padding of 1 that RenderPanel applies.
	noRepoContentX = 2
	noRepoContentY = 1
)

// RepoDetectedMsg is sent after probing for a repository in the current workdir.
type RepoDetectedMsg struct {
	Epoch uint64
	Root  string
}

// GetEpoch implements plugin.EpochMessage.
func (m RepoDetectedMsg) GetEpoch() uint64 { return m.Epoch }

// RepoInitDoneMsg is sent after attempting to run git init.
// Root is set on successful repository creation. Err is optional and may contain
// non-fatal warnings (for example, .gitignore update failures).
type RepoInitDoneMsg struct {
	Epoch uint64
	Root  string
	Err   error
}

// GetEpoch implements plugin.EpochMessage.
func (m RepoInitDoneMsg) GetEpoch() uint64 { return m.Epoch }

// updateNoRepo handles key events when the current project has no git repository.
func (p *Plugin) updateNoRepo(msg tea.KeyPressMsg) (plugin.Plugin, tea.Cmd) {
	switch msg.String() {
	case "i", "enter":
		return p.startRepoInit()
	case "r":
		return p, p.detectRepo()
	}
	return p, nil
}

func (p *Plugin) startRepoInit() (plugin.Plugin, tea.Cmd) {
	if p.repoInitInProgress {
		return p, nil
	}
	p.repoInitInProgress = true
	return p, p.initRepo()
}

func (p *Plugin) handleNoRepoMouse(msg tea.MouseMsg) (plugin.Plugin, tea.Cmd) {
	if p.mouseHandler == nil {
		return p, nil
	}
	action := p.mouseHandler.HandleMouse(msg)
	if action.Type != mouse.ActionClick || action.Region == nil {
		return p, nil
	}
	if action.Region.ID == regionInitRepo {
		return p.startRepoInit()
	}
	return p, nil
}

// detectRepo checks whether the current working directory is now inside a git repo.
func (p *Plugin) detectRepo() tea.Cmd {
	if p.ctx == nil {
		return nil
	}
	workDir := p.ctx.WorkDir
	epoch := p.ctx.Epoch

	return func() tea.Msg {
		root, err := resolveGitRoot(workDir)
		if err != nil {
			return RepoDetectedMsg{Epoch: epoch}
		}
		return RepoDetectedMsg{Epoch: epoch, Root: root}
	}
}

// initRepo initializes a git repository at the current workdir and ensures
// sidecar local-state paths are ignored.
func (p *Plugin) initRepo() tea.Cmd {
	if p.ctx == nil {
		return nil
	}
	workDir := p.ctx.WorkDir
	epoch := p.ctx.Epoch

	return func() tea.Msg {
		root, err := gitinit.Init(workDir)
		return RepoInitDoneMsg{Epoch: epoch, Root: root, Err: err}
	}
}

// renderNoRepoView renders the git plugin view when no repository exists.
func (p *Plugin) renderNoRepoView() string {
	if p.mouseHandler != nil {
		p.mouseHandler.Clear()
	}

	lines := []string{
		styles.Title.Render("Git"),
		"",
		styles.Muted.Render("Sidecar uses Git repositories for worktrees, status, and diffs."),
		styles.Muted.Render("No git repository was found in this directory."),
		"",
	}

	if p.repoInitInProgress {
		lines = append(lines, styles.StatusInProgress.Render("Initializing repository…"))
	} else {
		pill := styles.RenderPillWithStyle(initRepoButtonText, styles.ButtonHover, nil)
		buttonLine := len(lines)
		lines = append(lines, "  "+pill)
		if p.mouseHandler != nil {
			p.mouseHandler.HitMap.AddRect(
				regionInitRepo,
				noRepoContentX+2,
				noRepoContentY+buttonLine,
				ansi.StringWidth(pill),
				1,
				nil,
			)
		}
	}

	panelHeight := p.height
	if panelHeight < 4 {
		panelHeight = 4
	}
	return styles.RenderPanel(strings.Join(lines, "\n"), p.width, panelHeight, true)
}

func ensureGitignoreEntries(workDir string, entries []string) error {
	return gitinit.EnsureGitignore(workDir, entries)
}
