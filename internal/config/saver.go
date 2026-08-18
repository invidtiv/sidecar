package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// saveConfig is the JSON-marshaling intermediary that uses string durations.
type saveConfig struct {
	Projects saveProjectsConfig `json:"projects"`
	Plugins  savePluginsConfig  `json:"plugins"`
	Keymap   KeymapConfig       `json:"keymap"`
	UI       UIConfig           `json:"ui"`
	Features FeaturesConfig     `json:"features,omitempty"`
	// TerminalResources is written only when it has content; see Save.
	TerminalResources saveTerminalResourcesConfig `json:"terminalResources,omitempty"`
	Selection         saveSelectionConfig         `json:"selection"`
}

type saveSelectionConfig struct {
	CopyOnSelect *bool `json:"copyOnSelect,omitempty"`
}

type saveTerminalResourcesConfig struct {
	Providers []saveTerminalResourceProviderConfig `json:"providers,omitempty"`
}

type saveTerminalResourceProviderConfig struct {
	ID      string   `json:"id"`
	Command []string `json:"command"`
	PassEnv []string `json:"passEnv,omitempty"`
	Enabled bool     `json:"enabled"`
	Timeout string   `json:"timeout,omitempty"`
}

type saveProjectsConfig struct {
	Mode string          `json:"mode,omitempty"`
	Root string          `json:"root,omitempty"`
	List []ProjectConfig `json:"list,omitempty"`
}

type savePluginsConfig struct {
	GitStatus     saveGitStatusConfig     `json:"git-status,omitempty"`
	TDMonitor     saveTDMonitorConfig     `json:"td-monitor,omitempty"`
	FileBrowser   saveFileBrowserConfig   `json:"file-browser,omitempty"`
	Conversations saveConversationsConfig `json:"conversations,omitempty"`
	Workspace     saveWorkspaceConfig     `json:"workspace,omitempty"`
	Tasks         saveTasksConfig         `json:"tasks,omitempty"`
}

type saveTasksConfig struct {
	Position string `json:"position,omitempty"`
}

type saveFileBrowserConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type saveGitStatusConfig struct {
	Enabled         *bool  `json:"enabled,omitempty"`
	RefreshInterval string `json:"refreshInterval,omitempty"`
}

type saveTDMonitorConfig struct {
	Enabled         *bool  `json:"enabled,omitempty"`
	RefreshInterval string `json:"refreshInterval,omitempty"`
	DBPath          string `json:"dbPath,omitempty"`
}

type saveConversationsConfig struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	ClaudeDataDir string `json:"claudeDataDir,omitempty"`
}

type saveWorkspaceConfig struct {
	DirPrefix             *bool                 `json:"dirPrefix,omitempty"`
	DefaultAgentType      string                `json:"defaultAgentType,omitempty"`
	Agents                []string              `json:"agents,omitempty"`
	AgentStart            map[string]string     `json:"agentStart,omitempty"`
	TmuxCaptureMaxBytes   *int                  `json:"tmuxCaptureMaxBytes,omitempty"`
	ResizeDebounceMs      *int                  `json:"resizeDebounceMs,omitempty"`
	AutoCreateShell       *bool                 `json:"autoCreateShell,omitempty"`
	InteractiveExitKey    string                `json:"interactiveExitKey,omitempty"`
	InteractiveAttachKey  string                `json:"interactiveAttachKey,omitempty"`
	InteractiveCopyKey    string                `json:"interactiveCopyKey,omitempty"`
	InteractivePasteKey   string                `json:"interactivePasteKey,omitempty"`
	CopyOnSelect          *bool                 `json:"copyOnSelect,omitempty"`
	OverviewWorktreeScope string                `json:"overviewWorktreeScope,omitempty"`
	SidebarDisplay        *SidebarDisplayConfig `json:"sidebarDisplay,omitempty"`
	WorktreeSetup         WorktreeSetupConfig   `json:"worktreeSetup"`
}

// toSaveConfig converts Config to the JSON-serializable format.
func toSaveConfig(cfg *Config) saveConfig {
	return saveConfig{
		Projects: saveProjectsConfig{
			Mode: cfg.Projects.Mode,
			Root: cfg.Projects.Root,
			List: cfg.Projects.List,
		},
		Plugins: savePluginsConfig{
			GitStatus: saveGitStatusConfig{
				Enabled:         &cfg.Plugins.GitStatus.Enabled,
				RefreshInterval: cfg.Plugins.GitStatus.RefreshInterval.String(),
			},
			TDMonitor: saveTDMonitorConfig{
				Enabled:         &cfg.Plugins.TDMonitor.Enabled,
				RefreshInterval: cfg.Plugins.TDMonitor.RefreshInterval.String(),
				DBPath:          cfg.Plugins.TDMonitor.DBPath,
			},
			FileBrowser: saveFileBrowserConfig{
				Enabled: &cfg.Plugins.FileBrowser.Enabled,
			},
			Conversations: saveConversationsConfig{
				Enabled:       &cfg.Plugins.Conversations.Enabled,
				ClaudeDataDir: cfg.Plugins.Conversations.ClaudeDataDir,
			},
			Tasks: saveTasksConfig{
				Position: cfg.Plugins.Tasks.Position,
			},
			Workspace: saveWorkspaceConfig{
				DirPrefix:             &cfg.Plugins.Workspace.DirPrefix,
				DefaultAgentType:      cfg.Plugins.Workspace.DefaultAgentType,
				Agents:                cfg.Plugins.Workspace.Agents,
				AgentStart:            cfg.Plugins.Workspace.AgentStart,
				TmuxCaptureMaxBytes:   &cfg.Plugins.Workspace.TmuxCaptureMaxBytes,
				ResizeDebounceMs:      &cfg.Plugins.Workspace.ResizeDebounceMs,
				AutoCreateShell:       &cfg.Plugins.Workspace.AutoCreateShell,
				InteractiveExitKey:    cfg.Plugins.Workspace.InteractiveExitKey,
				InteractiveAttachKey:  cfg.Plugins.Workspace.InteractiveAttachKey,
				InteractiveCopyKey:    cfg.Plugins.Workspace.InteractiveCopyKey,
				InteractivePasteKey:   cfg.Plugins.Workspace.InteractivePasteKey,
				CopyOnSelect:          &cfg.Plugins.Workspace.CopyOnSelect,
				OverviewWorktreeScope: cfg.Plugins.Workspace.OverviewWorktreeScope,
				WorktreeSetup:         cfg.Plugins.Workspace.WorktreeSetup,
				SidebarDisplay:        &cfg.Plugins.Workspace.SidebarDisplay,
			},
		},
		Keymap:            cfg.Keymap,
		UI:                cfg.UI,
		Features:          cfg.Features,
		TerminalResources: toSaveTerminalResources(cfg.TerminalResources),
		Selection:         saveSelectionConfig{CopyOnSelect: &cfg.Selection.CopyOnSelect},
	}
}

func toSaveTerminalResources(cfg TerminalResourcesConfig) saveTerminalResourcesConfig {
	out := saveTerminalResourcesConfig{}
	for _, p := range cfg.Providers {
		sp := saveTerminalResourceProviderConfig{
			ID:      p.ID,
			Command: append([]string(nil), p.Command...),
			PassEnv: append([]string(nil), p.PassEnv...),
			Enabled: p.Enabled,
		}
		if p.Timeout > 0 {
			sp.Timeout = p.Timeout.String()
		}
		out.Providers = append(out.Providers, sp)
	}
	return out
}

// Save writes the config to ~/.config/sidecar/config.json, preserving
// any keys it doesn't manage (e.g. "prompts").
func Save(cfg *Config) error {
	path := ConfigPath()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Read existing file to preserve unknown keys
	var raw map[string]json.RawMessage
	if existing, err := os.ReadFile(path); err == nil {
		if jsonErr := json.Unmarshal(existing, &raw); jsonErr != nil {
			slog.Warn("config: invalid JSON, unmanaged keys will be lost", "error", jsonErr)
			raw = make(map[string]json.RawMessage)
		}
	} else {
		raw = make(map[string]json.RawMessage)
	}

	// Marshal each known field into the map
	sc := toSaveConfig(cfg)
	fields := map[string]interface{}{
		"projects":  sc.Projects,
		"plugins":   sc.Plugins,
		"keymap":    sc.Keymap,
		"ui":        sc.UI,
		"selection": sc.Selection,
	}
	if len(sc.Features.Flags) > 0 {
		fields["features"] = sc.Features
	}
	// terminalResources is a managed key: written when providers are
	// configured, removed when the last one goes. Writing an empty section into
	// every config file would be noise, but leaving the key untouched when it
	// empties would resurrect a provider the user just deleted, because Save's
	// unknown-key preservation would carry the old section forward.
	//
	// Every read-modify-write helper reloads before saving, so "empty" here
	// always means the user emptied it, never that the caller never read it.
	if len(sc.TerminalResources.Providers) > 0 {
		fields["terminalResources"] = sc.TerminalResources
	} else {
		delete(raw, "terminalResources")
	}
	for key, val := range fields {
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", key, err)
		}
		raw[key] = b
	}

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// SaveTheme updates only the theme name in config and saves.
func SaveTheme(themeName string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	cfg.UI.Theme.Name = themeName
	cfg.UI.Theme.Community = ""
	cfg.UI.Theme.Overrides = nil
	return Save(cfg)
}

// SaveThemeWithOverrides saves a theme name and full overrides map to config.
func SaveThemeWithOverrides(themeName string, overrides map[string]interface{}) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	cfg.UI.Theme.Name = themeName
	cfg.UI.Theme.Community = ""
	cfg.UI.Theme.Overrides = overrides
	return Save(cfg)
}

// SaveCommunityTheme saves a community theme reference with optional user overrides.
// Only the scheme name is stored — the full palette is computed at runtime.
func SaveCommunityTheme(communityName string, userOverrides map[string]interface{}) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	cfg.UI.Theme.Name = "default"
	cfg.UI.Theme.Community = communityName
	cfg.UI.Theme.Overrides = userOverrides
	return Save(cfg)
}

// SaveProjectTheme updates a specific project's theme in config and saves.
func SaveProjectTheme(projectPath string, theme *ThemeConfig) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	for i, proj := range cfg.Projects.List {
		if proj.Path == projectPath {
			cfg.Projects.List[i].Theme = theme
			return Save(cfg)
		}
	}
	return fmt.Errorf("project not found: %s", projectPath)
}

// SaveGlobalTheme saves a ThemeConfig as the global UI theme.
func SaveGlobalTheme(tc ThemeConfig) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	cfg.UI.Theme = tc
	return Save(cfg)
}

// SaveLastOpenInApp persists the last-used "open in" app ID.
// If projectPath matches a configured project, that project's LastOpenInApp is set.
// The global UI.LastOpenInApp is always set as a fallback.
func SaveLastOpenInApp(projectPath, appID string) error {
	cfg, err := LoadFrom(ConfigPath())
	if err != nil {
		return err
	}
	for i, proj := range cfg.Projects.List {
		if proj.Path == projectPath {
			cfg.Projects.List[i].LastOpenInApp = appID
			break
		}
	}
	cfg.UI.LastOpenInApp = appID
	return Save(cfg)
}

// SaveWorkspace applies a change to the plugins.workspace section and writes
// it. Like SaveUI it reloads first, so a setting changed in Configuration never
// overwrites an edit made to the file since Sidecar started.
func SaveWorkspace(mutate func(*WorkspacePluginConfig)) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	mutate(&cfg.Plugins.Workspace)
	if err := cfg.Validate(); err != nil {
		return err
	}
	return Save(cfg)
}

// SaveUI applies a change to the ui section and writes it. It reloads first, so
// a setting changed in Configuration never overwrites an edit made to the file
// since Sidecar started.
func SaveUI(mutate func(*UIConfig)) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	mutate(&cfg.UI)
	return Save(cfg)
}

// SavePlugins applies a change to the plugins section and writes it. It is the
// panel-enablement path: which surfaces Sidecar assembles, and the handful of
// inputs those surfaces read. Like the other helpers it reloads first, so a
// setting changed in Configuration never overwrites an edit made to the file
// since Sidecar started, and it validates before writing so an out-of-range
// interval cannot reach disk.
func SavePlugins(mutate func(*PluginsConfig)) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	mutate(&cfg.Plugins)
	if err := cfg.Validate(); err != nil {
		return err
	}
	return Save(cfg)
}
