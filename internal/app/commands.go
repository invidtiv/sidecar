package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
)

// ToastMsg is re-exported from msg package for backward compatibility.
type ToastMsg = msg.ToastMsg

// ShowToast is re-exported from msg package for backward compatibility.
var ShowToast = msg.ShowToast

// ThemeChangedMsg is re-exported from msg package for backward compatibility.
type ThemeChangedMsg = msg.ThemeChangedMsg

// ThemeChanged is re-exported from msg package for backward compatibility.
var ThemeChanged = msg.ThemeChanged

// Message types for tea.Cmd
type (
	// TickMsg is sent on each clock tick.
	TickMsg time.Time

	// RefreshMsg triggers a full refresh.
	RefreshMsg struct{}

	// ErrorMsg represents an error condition.
	ErrorMsg struct {
		Err error
	}
)

// tickCmd returns a command that ticks every second.
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// Refresh returns a command to trigger a refresh.
func Refresh() tea.Cmd {
	return func() tea.Msg {
		return RefreshMsg{}
	}
}

// ReportError returns a command to report an error.
func ReportError(err error) tea.Cmd {
	return func() tea.Msg {
		return ErrorMsg{Err: err}
	}
}

// Tick returns a custom tick command with a tag.
func Tick(d time.Duration, tag string) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return TaggedTickMsg{Time: t, Tag: tag}
	})
}

// TaggedTickMsg is a tick with an identifying tag.
type TaggedTickMsg struct {
	Time time.Time
	Tag  string
}

// PluginFocusedMsg is sent to a plugin when it becomes the active plugin.
// Plugins can use this to refresh data or update their state on focus.
// Re-exported from plugin package for backward compatibility.
type PluginFocusedMsg = plugin.PluginFocusedMsg

// PluginFocused returns a command that sends PluginFocusedMsg.
func PluginFocused() tea.Cmd {
	return func() tea.Msg {
		return plugin.PluginFocusedMsg{}
	}
}

// FocusPluginByIDMsg requests focusing a specific plugin by ID.
// Used for cross-plugin navigation (e.g., opening file in file browser from git).
type FocusPluginByIDMsg struct {
	PluginID string
}

// NavigateToFileMsg asks the Files plugin to open a path. Hosts send this
// rather than importing filebrowser.
type NavigateToFileMsg struct {
	Path string // Relative path from workdir
	Line int    // Optional 1-based line to reveal after loading
}

// OpenPrefilledShellMsg asks the Workspaces plugin for an ordinary new shell
// with a command typed into it and left unexecuted. Hosts send this rather than
// importing workspace.
//
// Nothing about it is privileged: it is the same shell the user could create by
// hand, and the command sits at the prompt until the user reads it and presses
// Enter. Sidecar never runs it, and never sends one that needs sudo.
type OpenPrefilledShellMsg struct {
	Command string
}

// OpenConfigurationMsg asks the host to open Configuration on a destination.
// An empty or unknown Page means Configuration's own default, Sidecar Setup.
//
// It is how a surface that is empty because something is not configured yet
// offers a way out of that state — a plugin sends this rather than importing
// the Configuration surface — and it is also how a launch command's startup
// destination is honored. Escape returns to whatever sent it.
type OpenConfigurationMsg struct {
	Page configui.PageID
}

// OpenNotesPreferencesMsg asks the host to open the one existing Notes
// enablement control and focus it. It is separate from the generic page route
// so a setup dialog does not need to know Configuration's private control ID.
type OpenNotesPreferencesMsg struct{}

// OpenConfiguration returns a command that opens Configuration on a page.
func OpenConfiguration(page configui.PageID) tea.Cmd {
	return func() tea.Msg { return OpenConfigurationMsg{Page: page} }
}

func OpenNotesPreferences() tea.Cmd {
	return func() tea.Msg { return OpenNotesPreferencesMsg{} }
}

// SwitchWorktreeMsg requests switching to a different worktree.
// Used by the worktree switcher modal and workspace plugin "Open in Git Tab" command.
type SwitchWorktreeMsg struct {
	WorktreePath string // Absolute path to the worktree
}

// SwitchWorktree returns a command that requests switching to a worktree by path.
func SwitchWorktree(path string) tea.Cmd {
	return func() tea.Msg {
		return SwitchWorktreeMsg{WorktreePath: path}
	}
}

// WorktreeDeletedMsg is sent when the current worktree has been deleted.
type WorktreeDeletedMsg struct {
	DeletedPath string // Path of the deleted worktree
	MainPath    string // Path to switch to (main worktree)
}

// checkWorktreeExists returns a command that checks if the current worktree still exists.
func checkWorktreeExists(workDir string) tea.Cmd {
	return func() tea.Msg {
		exists, mainPath := CheckCurrentWorktree(workDir)
		if !exists && mainPath != "" {
			return WorktreeDeletedMsg{
				DeletedPath: workDir,
				MainPath:    mainPath,
			}
		}
		return nil
	}
}

// FocusPlugin returns a command that requests focusing a plugin by ID.
func FocusPlugin(pluginID string) tea.Cmd {
	return func() tea.Msg {
		return FocusPluginByIDMsg{PluginID: pluginID}
	}
}

// UpdateModalState represents the current state of the update modal.
type UpdateModalState int

const (
	UpdateModalClosed   UpdateModalState = iota // Modal not visible
	UpdateModalPreview                          // Show release notes before update
	UpdateModalProgress                         // Show multi-phase progress during update
	UpdateModalComplete                         // Show completion message
	UpdateModalError                            // Show error details
)

// UpdateElapsedTickMsg triggers elapsed time update during update.
type UpdateElapsedTickMsg struct{}

// ChangelogLoadedMsg signals that changelog content has been loaded.
type ChangelogLoadedMsg struct {
	Content string
	Err     error
}

// EditorReturnedMsg signals that an external editor process has exited.
// Used to restore terminal state (mouse support) after returning from vim/etc.
type EditorReturnedMsg struct {
	Err error
	// Fallback is the direct-exec argv to try when the shell that was asked to
	// load the user's profile never got as far as running the editor. It is
	// empty once the fallback has been used, so a failure can never loop.
	Fallback []string
}

// SwitchToMainWorktreeMsg requests switching to the main worktree.
// Sent when the current WorkDir (a worktree) has been deleted and sidecar
// should gracefully switch to the main repository.
type SwitchToMainWorktreeMsg struct {
	MainWorktreePath string // Path to the main worktree to switch to
}

// SwitchToMainWorktree returns a command that requests switching to the main worktree.
func SwitchToMainWorktree(mainPath string) tea.Cmd {
	return func() tea.Msg {
		return SwitchToMainWorktreeMsg{MainWorktreePath: mainPath}
	}
}
