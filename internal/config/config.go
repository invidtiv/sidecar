package config

import (
	"path/filepath"
	"time"
)

const (
	OverviewWorktreeScopeProject  = "project"
	OverviewWorktreeScopeWorktree = "worktree"
)

// Config is the root configuration structure.
type Config struct {
	Projects ProjectsConfig `json:"projects"`
	Plugins  PluginsConfig  `json:"plugins"`
	Keymap   KeymapConfig   `json:"keymap"`
	UI       UIConfig       `json:"ui"`
	Features FeaturesConfig `json:"features"`
}

// FeaturesConfig holds feature flag settings.
type FeaturesConfig struct {
	Flags map[string]bool `json:"flags"`
}

// ProjectsConfig configures project detection and layout.
type ProjectsConfig struct {
	Mode string          `json:"mode"` // "single" for now
	Root string          `json:"root"` // "." default
	List []ProjectConfig `json:"list"` // list of configured projects for switcher
}

// ProjectConfig represents a single project in the project switcher.
type ProjectConfig struct {
	Name          string               `json:"name"`                    // display name for the project
	Path          string               `json:"path"`                    // absolute path to project root (supports ~ expansion)
	Theme         *ThemeConfig         `json:"theme,omitempty"`         // per-project theme (nil = use global)
	LastOpenInApp string               `json:"lastOpenInApp,omitempty"` // last app used to open this project (e.g. "vscode", "goland")
	OpenIn        string               `json:"openIn,omitempty"`        // preferred "open in" app for this project; last-used is the fallback
	WorktreeSetup *WorktreeSetupConfig `json:"worktreeSetup,omitempty"` // optional per-project setup policy
}

// WorktreeSetupForProject returns the project override when present, otherwise
// the workspace-wide default.
func (c *Config) WorktreeSetupForProject(projectPath string) WorktreeSetupConfig {
	if c == nil {
		return WorktreeSetupConfig{}
	}
	for _, project := range c.Projects.List {
		if filepath.Clean(ExpandPath(project.Path)) == filepath.Clean(projectPath) && project.WorktreeSetup != nil {
			return *project.WorktreeSetup
		}
	}
	return c.Plugins.Workspace.WorktreeSetup
}

// PluginsConfig holds per-plugin configuration.
type PluginsConfig struct {
	GitStatus     GitStatusPluginConfig     `json:"git-status"`
	TDMonitor     TDMonitorPluginConfig     `json:"td-monitor"`
	FileBrowser   FileBrowserPluginConfig   `json:"file-browser"`
	Conversations ConversationsPluginConfig `json:"conversations"`
	Workspace     WorkspacePluginConfig     `json:"workspace"`
	Notes         NotesPluginConfig         `json:"notes"`
	Tasks         TasksPluginConfig         `json:"tasks"`
}

// Tab positions for the Tasks plugin.
const (
	// TasksPositionAfterWorkspaces places the Tasks tab immediately after the
	// workspaces tab. This is the default.
	TasksPositionAfterWorkspaces = "after-workspaces"
	// TasksPositionAfterNotes places the Tasks tab immediately after the notes
	// tab.
	TasksPositionAfterNotes = "after-notes"
)

// TasksPluginConfig configures the embedded Tasks plugin.
//
// Whether the plugin loads at all is governed by the "tasks_plugin" feature
// flag (default off), not by a field here — that keeps enablement on the same
// lever as the other opt-in surfaces (see internal/features).
//
// There is deliberately no store/JSONL path: the embedded Tasks package uses
// Tasks' own configuration resolution.
type TasksPluginConfig struct {
	// Position was the anchor the Tasks tab was inserted after while Tasks was
	// a project plugin. Tasks is now a tab of the global space (Agents,
	// Workspaces, Tasks), whose order is fixed, so the value is accepted,
	// validated, and preserved for older configs but no longer moves anything.
	// One of TasksPositionAfterWorkspaces (default) or TasksPositionAfterNotes;
	// unknown values are coerced back to the default by Validate.
	Position string `json:"position,omitempty"`
}

// GitStatusPluginConfig configures the git status plugin.
type GitStatusPluginConfig struct {
	Enabled         bool          `json:"enabled"`
	RefreshInterval time.Duration `json:"refreshInterval"`
}

// FileBrowserPluginConfig configures the file browser plugin.
type FileBrowserPluginConfig struct {
	// Enabled controls whether the file browser plugin is loaded. Default: true.
	Enabled bool `json:"enabled"`
}

// TDMonitorPluginConfig configures the TD monitor plugin.
type TDMonitorPluginConfig struct {
	Enabled         bool          `json:"enabled"`
	RefreshInterval time.Duration `json:"refreshInterval"`
	DBPath          string        `json:"dbPath"`
}

// ConversationsPluginConfig configures the conversations plugin.
type ConversationsPluginConfig struct {
	Enabled       bool   `json:"enabled"`
	ClaudeDataDir string `json:"claudeDataDir"`
	// DefaultCategoryFilter sets the default session category filter on startup.
	// Example: ["interactive"] hides cron/system sessions by default.
	// Empty or omitted means show all sessions (no filter).
	DefaultCategoryFilter []string `json:"defaultCategoryFilter,omitempty"`
}

// WorkspacePluginConfig configures the workspace plugin.
type WorkspacePluginConfig struct {
	// DirPrefix prefixes workspace directory names with the repo name (e.g., 'myrepo-feature-auth')
	// This helps associate conversations with the repo after workspace deletion. Default: true.
	DirPrefix bool `json:"dirPrefix"`
	// DefaultAgentType sets the default agent family selected when creating a workspace.
	// Uses workspace.AgentType values (e.g. "claude", "codex", "opencode", "grok").
	DefaultAgentType string `json:"defaultAgentType,omitempty"`
	// Agents is an ordered allowlist of agent type IDs shown in Create Shell, Create Worktree,
	// and Start Agent pickers (e.g. ["claude","codex","grok"]). Empty/omitted shows all
	// built-in UI agents. Unknown IDs are ignored. "None (attach only)" is always offered
	// in pickers regardless of this list. Stored agent types on existing workspaces still
	// resolve even if hidden from pickers.
	Agents []string `json:"agents,omitempty"`
	// AgentStart maps agent family (AgentType string) to default startup command.
	// Example: {"claude":"claude", "opencode":"opencode --profile fast", "grok":"grok"}.
	// Per-workspace .sidecar-agent-start still takes precedence when present.
	AgentStart map[string]string `json:"agentStart,omitempty"`
	// TmuxCaptureMaxBytes caps tmux pane capture size for the preview pane. Default: 2MB.
	TmuxCaptureMaxBytes int `json:"tmuxCaptureMaxBytes"`
	// ResizeDebounceMs is the shared interval for live-pane SIGWINCH during
	// layout motion (divider drag, interactive correction). Default: 300.
	// 0 restores per-event paint and poll-driven resize. Negative values
	// become 300. Unlike TmuxCaptureMaxBytes, 0 is not treated as unset.
	ResizeDebounceMs int `json:"resizeDebounceMs"`
	// AutoCreateShell creates a shell session the first time the workspaces tab is
	// focused in a session, when no shell sessions exist yet. The shell honors
	// DefaultAgentType; with none set it is a plain shell. Default: false.
	AutoCreateShell bool `json:"autoCreateShell"`
	// InteractiveExitKey is the keybinding to exit interactive mode. Default: "ctrl+\".
	// Examples: "ctrl+]", "ctrl+\\", "ctrl+x"
	InteractiveExitKey string `json:"interactiveExitKey,omitempty"`
	// InteractiveAttachKey is the keybinding to attach from interactive mode. Default: "ctrl+]".
	// When pressed in interactive mode, exits interactive and attaches to the tmux session.
	InteractiveAttachKey string `json:"interactiveAttachKey,omitempty"`
	// InteractiveCopyKey is the keybinding to copy selection in interactive mode. Default: "alt+c".
	InteractiveCopyKey string `json:"interactiveCopyKey,omitempty"`
	// InteractivePasteKey is the keybinding to paste clipboard in interactive mode. Default: "alt+v".
	InteractivePasteKey string `json:"interactivePasteKey,omitempty"`
	// CopyOnSelect copies terminal selections when a drag completes. Default: false.
	CopyOnSelect bool `json:"copyOnSelect,omitempty"`
	// OverviewWorktreeScope controls whether activating a worktree on the cross-project
	// Overview enters the project root or scopes Sidecar to that worktree. Valid values
	// are "project" (the default) and "worktree".
	OverviewWorktreeScope string `json:"overviewWorktreeScope,omitempty"`
	// SidebarDisplay controls what information is shown in the workspace sidebar entries.
	SidebarDisplay SidebarDisplayConfig `json:"sidebarDisplay"`
	// WorktreeSetup controls repository artifacts Sidecar may copy or execute when
	// creating a worktree. The creation confirmation always names the discovered
	// files and hook and requires an explicit per-operation selection.
	WorktreeSetup WorktreeSetupConfig `json:"worktreeSetup"`
}

// WorktreeSetupConfig configures the optional setup phase after git creates a
// worktree. Paths are relative to the canonical main worktree.
type WorktreeSetupConfig struct {
	CopyEnvFiles bool     `json:"copyEnvFiles"`
	EnvFiles     []string `json:"envFiles,omitempty"`
	RunHook      bool     `json:"runHook"`
	HookPath     string   `json:"hookPath,omitempty"`
	HookRequired bool     `json:"hookRequired"`
}

// SidebarDisplayConfig controls visibility of workspace sidebar entry elements.
type SidebarDisplayConfig struct {
	// HideRepoPrefix strips the repo name prefix from worktree names (e.g., "myrepo-feature" → "feature").
	// Default: false (show full name).
	HideRepoPrefix bool `json:"hideRepoPrefix"`
	// HideAgent hides the agent type label (e.g., "claude") on the second line. Default: false.
	HideAgent bool `json:"hideAgent"`
	// HideTask hides the linked task ID (e.g., "td-abc123") on the second line. Default: false.
	HideTask bool `json:"hideTask"`
	// HideStats hides the +/- line change stats on the second line. Default: false.
	HideStats bool `json:"hideStats"`
}

// NotesPluginConfig configures the notes plugin.
type NotesPluginConfig struct {
	// DefaultEditor sets the default editor mode when pressing Enter on a note.
	// Values: "builtin" (default), "vim", "nvim", or any $EDITOR value.
	// When set to "vim"/"nvim", Enter opens the note in inline vim instead of built-in editor.
	DefaultEditor string `json:"defaultEditor,omitempty"`
}

// KeymapConfig holds key binding overrides.
type KeymapConfig struct {
	Overrides map[string]string `json:"overrides"`
}

// UIConfig configures UI appearance.
type UIConfig struct {
	ShowClock        bool        `json:"showClock"`
	Theme            ThemeConfig `json:"theme"`
	NerdFontsEnabled bool        `json:"nerdFontsEnabled"`        // enables Nerd Font glyphs (pill tabs, icons, etc.)
	LastOpenInApp    string      `json:"lastOpenInApp,omitempty"` // global fallback for last app used to open projects
	// TerminalTitle templates the terminal window/tab title. Supported
	// variables: {project} {worktree} {plugin} {dir}. Empty disables retitling.
	TerminalTitle string `json:"terminalTitle"`
}

// ThemeConfig configures the color theme.
type ThemeConfig struct {
	Name      string                 `json:"name"`
	Community string                 `json:"community,omitempty"` // community scheme name (resolved at runtime)
	Overrides map[string]interface{} `json:"overrides,omitempty"` // user customizations on top
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{
		Projects: ProjectsConfig{
			Mode: "single",
			Root: ".",
		},
		Plugins: PluginsConfig{
			GitStatus: GitStatusPluginConfig{
				Enabled:         true,
				RefreshInterval: time.Second,
			},
			TDMonitor: TDMonitorPluginConfig{
				Enabled:         true,
				RefreshInterval: 2 * time.Second,
				DBPath:          ".todos/issues.db",
			},
			FileBrowser: FileBrowserPluginConfig{
				Enabled: true,
			},
			Conversations: ConversationsPluginConfig{
				Enabled:       true,
				ClaudeDataDir: "~/.claude",
			},
			Tasks: TasksPluginConfig{
				Position: TasksPositionAfterWorkspaces,
			},
			Workspace: WorkspacePluginConfig{
				DirPrefix:             true,
				TmuxCaptureMaxBytes:   2 * 1024 * 1024,
				ResizeDebounceMs:      300,
				OverviewWorktreeScope: OverviewWorktreeScopeProject,
				WorktreeSetup: WorktreeSetupConfig{
					CopyEnvFiles: true,
					EnvFiles:     []string{".env", ".env.local", ".env.development", ".env.development.local"},
					RunHook:      true, HookPath: ".worktree-setup.sh", HookRequired: true,
				},
			},
		},
		Keymap: KeymapConfig{
			Overrides: make(map[string]string),
		},
		UI: UIConfig{
			// Off by default. The header clock has had no renderer for a long
			// time; now that it has one, defaulting it on would put a clock in
			// every existing user's header for a setting they never chose.
			// Appearance is where it gets turned on.
			ShowClock:     false,
			TerminalTitle: "{project}{worktree}",
			Theme: ThemeConfig{
				Name:      "default",
				Overrides: make(map[string]interface{}),
			},
		},
		Features: FeaturesConfig{
			Flags: make(map[string]bool),
		},
	}
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if c.Plugins.GitStatus.RefreshInterval < 0 {
		c.Plugins.GitStatus.RefreshInterval = time.Second
	}
	if c.Plugins.TDMonitor.RefreshInterval < 0 {
		c.Plugins.TDMonitor.RefreshInterval = 2 * time.Second
	}
	if c.Plugins.Workspace.TmuxCaptureMaxBytes <= 0 {
		c.Plugins.Workspace.TmuxCaptureMaxBytes = 2 * 1024 * 1024
	}
	if c.Plugins.Workspace.ResizeDebounceMs < 0 {
		c.Plugins.Workspace.ResizeDebounceMs = 300
	}
	// An unrecognized (or empty) tasks position falls back to the default
	// anchor rather than failing the whole config, which is how the rest of
	// Validate treats out-of-range values.
	switch c.Plugins.Tasks.Position {
	case TasksPositionAfterWorkspaces, TasksPositionAfterNotes:
	default:
		c.Plugins.Tasks.Position = TasksPositionAfterWorkspaces
	}
	return nil
}
