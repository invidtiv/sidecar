package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg.Projects.Mode != "single" {
		t.Errorf("got mode %q, want 'single'", cfg.Projects.Mode)
	}
	if !cfg.Plugins.GitStatus.Enabled {
		t.Error("git-status should be enabled by default")
	}
	if cfg.Plugins.GitStatus.RefreshInterval != time.Second {
		t.Errorf("got refresh %v, want 1s", cfg.Plugins.GitStatus.RefreshInterval)
	}
	if cfg.Plugins.Workspace.OverviewWorktreeScope != OverviewWorktreeScopeProject {
		t.Errorf("got Overview worktree scope %q, want %q", cfg.Plugins.Workspace.OverviewWorktreeScope, OverviewWorktreeScopeProject)
	}
	if cfg.Plugins.Workspace.ResizeDebounceMs != 300 {
		t.Errorf("got ResizeDebounceMs %d, want 300", cfg.Plugins.Workspace.ResizeDebounceMs)
	}
}

func TestResizeDebounceMs(t *testing.T) {
	t.Run("missing key keeps default 300", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"plugins":{"workspace":{}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadFrom(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Plugins.Workspace.ResizeDebounceMs != 300 {
			t.Fatalf("missing key = %d, want 300", cfg.Plugins.Workspace.ResizeDebounceMs)
		}
	})
	t.Run("explicit 0 is kept", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"plugins":{"workspace":{"resizeDebounceMs":0}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadFrom(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Plugins.Workspace.ResizeDebounceMs != 0 {
			t.Fatalf("explicit 0 = %d, want 0", cfg.Plugins.Workspace.ResizeDebounceMs)
		}
	})
	t.Run("negative becomes 300", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"plugins":{"workspace":{"resizeDebounceMs":-1}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadFrom(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Plugins.Workspace.ResizeDebounceMs != 300 {
			t.Fatalf("negative = %d, want 300", cfg.Plugins.Workspace.ResizeDebounceMs)
		}
	})
	t.Run("Validate leaves 0 and rewrites negative", func(t *testing.T) {
		cfg := Default()
		cfg.Plugins.Workspace.ResizeDebounceMs = 0
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		if cfg.Plugins.Workspace.ResizeDebounceMs != 0 {
			t.Fatalf("Validate rewrote 0 to %d", cfg.Plugins.Workspace.ResizeDebounceMs)
		}
		cfg.Plugins.Workspace.ResizeDebounceMs = -1
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		if cfg.Plugins.Workspace.ResizeDebounceMs != 300 {
			t.Fatalf("Validate(-1) = %d, want 300", cfg.Plugins.Workspace.ResizeDebounceMs)
		}
	})
}

func TestLoadFrom_OverviewWorktreeScope(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "unset defaults to project", raw: `{}`, want: OverviewWorktreeScopeProject},
		{name: "legacy worktree scope", raw: `{"plugins":{"workspace":{"overviewWorktreeScope":"worktree"}}}`, want: OverviewWorktreeScopeWorktree},
		{name: "invalid keeps default", raw: `{"plugins":{"workspace":{"overviewWorktreeScope":"repository"}}}`, want: OverviewWorktreeScopeProject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.raw), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Plugins.Workspace.OverviewWorktreeScope != tt.want {
				t.Fatalf("OverviewWorktreeScope = %q, want %q", cfg.Plugins.Workspace.OverviewWorktreeScope, tt.want)
			}
		})
	}
}

func TestLoadFrom_NonExistent(t *testing.T) {
	cfg, err := LoadFrom("/nonexistent/path/config.json")
	if err != nil {
		t.Errorf("should not error on missing file: %v", err)
	}
	if cfg == nil {
		t.Error("should return default config")
	}
}

func TestLoadFrom_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	content := []byte(`{
		"plugins": {
			"git-status": {
				"enabled": false,
				"refreshInterval": "5s"
			}
		}
	}`)

	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if cfg.Plugins.GitStatus.Enabled {
		t.Error("git-status should be disabled")
	}
	if cfg.Plugins.GitStatus.RefreshInterval != 5*time.Second {
		t.Errorf("got refresh %v, want 5s", cfg.Plugins.GitStatus.RefreshInterval)
	}
	// Default values should still be present
	if !cfg.Plugins.TDMonitor.Enabled {
		t.Error("td-monitor should still be enabled (default)")
	}
}

func TestLoadFrom_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte(`{invalid`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFrom(path)
	if err == nil {
		t.Error("should error on invalid JSON")
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input  string
		expect string
	}{
		{"~/.claude", filepath.Join(home, ".claude")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tc := range tests {
		got := ExpandPath(tc.input)
		if got != tc.expect {
			t.Errorf("ExpandPath(%q) = %q, want %q", tc.input, got, tc.expect)
		}
	}
}

func TestValidate(t *testing.T) {
	cfg := Default()
	cfg.Plugins.GitStatus.RefreshInterval = -1

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate failed: %v", err)
	}

	// Negative values should be corrected
	if cfg.Plugins.GitStatus.RefreshInterval != time.Second {
		t.Errorf("got %v, want 1s after validation", cfg.Plugins.GitStatus.RefreshInterval)
	}
}

func TestLoadFrom_ProjectsList(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	// Create a test project directory
	testProjectDir := filepath.Join(dir, "myproject")
	if err := os.MkdirAll(testProjectDir, 0755); err != nil {
		t.Fatal(err)
	}

	content := []byte(`{
		"projects": {
			"list": [
				{"name": "My Project", "path": "` + testProjectDir + `"},
				{"name": "Tilde Project", "path": "~/code/test"}
			]
		}
	}`)

	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(configPath)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if len(cfg.Projects.List) != 2 {
		t.Errorf("got %d projects, want 2", len(cfg.Projects.List))
	}

	// Check first project
	if cfg.Projects.List[0].Name != "My Project" {
		t.Errorf("got name %q, want 'My Project'", cfg.Projects.List[0].Name)
	}
	if cfg.Projects.List[0].Path != testProjectDir {
		t.Errorf("got path %q, want %q", cfg.Projects.List[0].Path, testProjectDir)
	}

	// Check tilde expansion
	home, _ := os.UserHomeDir()
	expectedPath := filepath.Join(home, "code/test")
	if cfg.Projects.List[1].Path != expectedPath {
		t.Errorf("got path %q, want %q (tilde expanded)", cfg.Projects.List[1].Path, expectedPath)
	}
}

func TestLoadFrom_EmptyProjectsList(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	content := []byte(`{
		"projects": {
			"mode": "single"
		}
	}`)

	if err := os.WriteFile(configPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(configPath)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if len(cfg.Projects.List) != 0 {
		t.Errorf("got %d projects, want 0", len(cfg.Projects.List))
	}
}

func TestLoadFrom_WorkspaceAgentSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	content := []byte(`{
		"plugins": {
			"workspace": {
				"defaultAgentType": "opencode",
				"agents": ["claude", "grok", "opencode"],
				"agentStart": {
					"opencode": "opencode --profile fast"
				}
			}
		}
	}`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if cfg.Plugins.Workspace.DefaultAgentType != "opencode" {
		t.Errorf("DefaultAgentType = %q, want %q", cfg.Plugins.Workspace.DefaultAgentType, "opencode")
	}
	if got := cfg.Plugins.Workspace.AgentStart["opencode"]; got != "opencode --profile fast" {
		t.Errorf("AgentStart[opencode] = %q, want %q", got, "opencode --profile fast")
	}
	wantAgents := []string{"claude", "grok", "opencode"}
	if len(cfg.Plugins.Workspace.Agents) != len(wantAgents) {
		t.Fatalf("Agents = %v, want %v", cfg.Plugins.Workspace.Agents, wantAgents)
	}
	for i, a := range wantAgents {
		if cfg.Plugins.Workspace.Agents[i] != a {
			t.Errorf("Agents[%d] = %q, want %q", i, cfg.Plugins.Workspace.Agents[i], a)
		}
	}
}

func TestLoadFrom_WorkspaceDefaultAgentTypeEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := []byte(`{
		"plugins": {
			"workspace": {
				"defaultAgentType": "opencode"
			}
		}
	}`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SIDECAR_WORKSPACE_DEFAULT_AGENT_TYPE", "codex")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}
	if cfg.Plugins.Workspace.DefaultAgentType != "codex" {
		t.Errorf("DefaultAgentType = %q, want %q", cfg.Plugins.Workspace.DefaultAgentType, "codex")
	}
}

func TestLoadFrom_WorkspaceDefaultAgentTypeEnvOverride_NoConfigFile(t *testing.T) {
	t.Setenv("SIDECAR_WORKSPACE_DEFAULT_AGENT_TYPE", "antigravity")

	cfg, err := LoadFrom("/definitely/missing/config.json")
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}
	if cfg.Plugins.Workspace.DefaultAgentType != "antigravity" {
		t.Errorf("DefaultAgentType = %q, want %q", cfg.Plugins.Workspace.DefaultAgentType, "antigravity")
	}
}

func TestLoadFrom_WorkspaceDefaultAgentTypeEnvOverride_LegacyAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := []byte(`{"plugins":{"workspace":{"defaultAgentType":"opencode"}}}`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SIDECAR_DEFAULT_AGENT_TYPE", "cursor")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}
	if cfg.Plugins.Workspace.DefaultAgentType != "cursor" {
		t.Errorf("DefaultAgentType = %q, want %q", cfg.Plugins.Workspace.DefaultAgentType, "cursor")
	}
}

func TestLoadFrom_WorkspaceDefaultAgentTypeEnvOverride_PrefersPrimaryVar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := []byte(`{"plugins":{"workspace":{"defaultAgentType":"opencode"}}}`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SIDECAR_DEFAULT_AGENT_TYPE", "cursor")
	t.Setenv("SIDECAR_WORKSPACE_DEFAULT_AGENT_TYPE", "codex")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}
	if cfg.Plugins.Workspace.DefaultAgentType != "codex" {
		t.Errorf("DefaultAgentType = %q, want %q", cfg.Plugins.Workspace.DefaultAgentType, "codex")
	}
}

func TestLoadFrom_WorkspaceAgentStartLegacyStringBackwardCompat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := []byte(`{
		"plugins": {
			"workspace": {
				"agentStart": "custom-agent --legacy"
			}
		}
	}`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}
	if got := cfg.Plugins.Workspace.AgentStart["*"]; got != "custom-agent --legacy" {
		t.Errorf("AgentStart[*] = %q, want %q", got, "custom-agent --legacy")
	}
}

func TestApplyEnvOverrides_WorkspaceVarTakesPrecedence(t *testing.T) {
	t.Setenv(envWorkspaceDefaultAgentType, "opencode")
	t.Setenv(envDefaultAgentType, "antigravity")

	cfg := Default()
	applyEnvOverrides(cfg)

	if cfg.Plugins.Workspace.DefaultAgentType != "opencode" {
		t.Errorf("DefaultAgentType = %q, want %q", cfg.Plugins.Workspace.DefaultAgentType, "opencode")
	}
}

func TestApplyEnvOverrides_FallsThruWhenWorkspaceVarBlank(t *testing.T) {
	// When SIDECAR_WORKSPACE_DEFAULT_AGENT_TYPE is set but blank, we should
	// NOT short-circuit — SIDECAR_DEFAULT_AGENT_TYPE must still be honoured.
	t.Setenv(envWorkspaceDefaultAgentType, "   ")
	t.Setenv(envDefaultAgentType, "antigravity")

	cfg := Default()
	applyEnvOverrides(cfg)

	if cfg.Plugins.Workspace.DefaultAgentType != "antigravity" {
		t.Errorf("DefaultAgentType = %q, want %q (blank workspace var should fall through)", cfg.Plugins.Workspace.DefaultAgentType, "antigravity")
	}
}

func TestApplyEnvOverrides_OnlyDefaultVar(t *testing.T) {
	t.Setenv(envDefaultAgentType, "codex")

	cfg := Default()
	applyEnvOverrides(cfg)

	if cfg.Plugins.Workspace.DefaultAgentType != "codex" {
		t.Errorf("DefaultAgentType = %q, want %q", cfg.Plugins.Workspace.DefaultAgentType, "codex")
	}
}

func TestApplyEnvOverrides_NeitherVarSet(t *testing.T) {
	cfg := Default()
	cfg.Plugins.Workspace.DefaultAgentType = "original"

	// Ensure neither env var is set
	t.Setenv(envWorkspaceDefaultAgentType, "")
	if err := os.Unsetenv(envWorkspaceDefaultAgentType); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv(envDefaultAgentType); err != nil {
		t.Fatal(err)
	}

	applyEnvOverrides(cfg)

	if cfg.Plugins.Workspace.DefaultAgentType != "original" {
		t.Errorf("DefaultAgentType = %q, want %q (should be unchanged)", cfg.Plugins.Workspace.DefaultAgentType, "original")
	}
}

func TestDefault_FileBrowserEnabled(t *testing.T) {
	cfg := Default()
	if !cfg.Plugins.FileBrowser.Enabled {
		t.Error("file-browser should be enabled by default")
	}
}

func TestLoadFrom_FileBrowserDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	content := []byte(`{
		"plugins": {
			"file-browser": {
				"enabled": false
			}
		}
	}`)

	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if cfg.Plugins.FileBrowser.Enabled {
		t.Error("file-browser should be disabled when set to false in config")
	}
	// Other plugins should still have defaults
	if !cfg.Plugins.GitStatus.Enabled {
		t.Error("git-status should still be enabled (default)")
	}
}

func TestLoadFrom_WorkspaceAutoCreateShell(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	content := []byte(`{
		"plugins": {
			"workspace": {
				"autoCreateShell": true
			}
		}
	}`)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if !cfg.Plugins.Workspace.AutoCreateShell {
		t.Error("AutoCreateShell = false, want true")
	}
}

func TestLoadFrom_WorkspaceAutoCreateShellDefaultsOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte(`{"plugins":{"workspace":{}}}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}

	if cfg.Plugins.Workspace.AutoCreateShell {
		t.Error("AutoCreateShell = true, want false when unset")
	}
}

func TestLoadFrom_TerminalTitle(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "unset keeps the default template",
			content: `{"ui":{"showClock":false}}`,
			want:    "{project}{worktree}",
		},
		{
			name:    "custom template",
			content: `{"ui":{"terminalTitle":"{project} · {plugin}"}}`,
			want:    "{project} · {plugin}",
		},
		{
			// The pointer in rawUIConfig exists for exactly this case: an
			// explicit "" means "leave my terminal title alone".
			name:    "empty string disables retitling",
			content: `{"ui":{"terminalTitle":""}}`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			cfg, err := LoadFrom(path)
			if err != nil {
				t.Fatalf("LoadFrom failed: %v", err)
			}
			if cfg.UI.TerminalTitle != tt.want {
				t.Errorf("TerminalTitle = %q, want %q", cfg.UI.TerminalTitle, tt.want)
			}
		})
	}
}

func TestLoadFrom_SelectionCopyOnSelect(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"absent section", `{}`, false},
		{"empty section", `{"selection":{}}`, false},
		{"explicitly off", `{"selection":{"copyOnSelect":false}}`, false},
		{"opted in", `{"selection":{"copyOnSelect":true}}`, true},
		{"the terminal's own key from before there was a general one",
			`{"plugins":{"workspace":{"copyOnSelect":true}}}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			cfg, err := LoadFrom(path)
			if err != nil {
				t.Fatalf("LoadFrom failed: %v", err)
			}
			if cfg.Selection.CopyOnSelect != tt.want {
				t.Errorf("Selection.CopyOnSelect = %v, want %v", cfg.Selection.CopyOnSelect, tt.want)
			}
			// The folded key is cleared so the next save retires it; leaving it
			// set would turn the setting back on after it was turned off.
			if cfg.Plugins.Workspace.CopyOnSelect {
				t.Error("Plugins.Workspace.CopyOnSelect survived the fold into the general key")
			}
		})
	}
}

func TestDefault_SelectionCopyOnSelectOff(t *testing.T) {
	if Default().Selection.CopyOnSelect {
		t.Error("Selection.CopyOnSelect = true, want a selection that does not touch the clipboard by default")
	}
}

func TestLoadFrom_NotesDefaultEditorNormalizesCurrentLegacyAndUnknownValues(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "absent defaults built-in", raw: `{}`, want: NotesEditorBuiltin},
		{name: "pane", raw: `{"plugins":{"notes":{"defaultEditor":"pane"}}}`, want: NotesEditorPane},
		{name: "legacy vim keeps pane intent", raw: `{"plugins":{"notes":{"defaultEditor":"vim"}}}`, want: NotesEditorPane},
		{name: "legacy nvim keeps pane intent", raw: `{"plugins":{"notes":{"defaultEditor":"nvim"}}}`, want: NotesEditorPane},
		{name: "unknown falls back safely", raw: `{"plugins":{"notes":{"defaultEditor":"mystery"}}}`, want: NotesEditorBuiltin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.raw), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFrom(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Plugins.Notes.DefaultEditor; got != tt.want {
				t.Fatalf("default editor = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadFrom_TerminalBackgrounds(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantMode    string
		wantSpanMax int
	}{
		{"absent", `{}`, "auto", 12},
		{"bounded", `{"plugins":{"workspace":{"terminalBackgrounds":"bounded"}}}`, "bounded", 12},
		{"never", `{"plugins":{"workspace":{"terminalBackgrounds":"never"}}}`, "never", 12},
		{"unknown falls back to auto",
			`{"plugins":{"workspace":{"terminalBackgrounds":"plaid"}}}`, "auto", 12},
		{"explicit span cap",
			`{"plugins":{"workspace":{"terminalBackgrounds":"bounded","terminalBackgroundSpanMax":4}}}`,
			"bounded", 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}

			cfg, err := LoadFrom(path)
			if err != nil {
				t.Fatalf("LoadFrom failed: %v", err)
			}
			if got := cfg.Plugins.Workspace.TerminalBackgrounds; got != tt.wantMode {
				t.Errorf("TerminalBackgrounds = %q, want %q", got, tt.wantMode)
			}
			if got := cfg.Plugins.Workspace.TerminalBackgroundSpanMax; got != tt.wantSpanMax {
				t.Errorf("TerminalBackgroundSpanMax = %d, want %d", got, tt.wantSpanMax)
			}
		})
	}
}

// TestLoadFromParsesHosts is a regression test with a specific history.
//
// The loader merges a rawConfig into defaults field by field, so a key that
// exists on Config but not on rawConfig parses into nothing — silently, with
// no error. `hosts` shipped that way: a correctly written config produced no
// hosts and no complaint, and it was only found by running the feature against
// a real machine. Deleting the three-line merge block reintroduces it with a
// green suite unless this test exists.
func TestLoadFromParsesHosts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	document := `{
	  "hosts": { "list": [
	    { "id": "mac-mini", "target": "mini.local", "binary": "/opt/sidecar",
	      "config": "/etc/sc.json", "env": ["K=V"] },
	    { "target": "other", "disabled": true }
	  ] }
	}`
	if err := os.WriteFile(path, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(cfg.Hosts.List) != 2 {
		t.Fatalf("hosts = %d, want 2 — the `hosts` key parsed into nothing", len(cfg.Hosts.List))
	}
	first := cfg.Hosts.List[0]
	if first.ID != "mac-mini" || first.Target != "mini.local" {
		t.Errorf("identity lost: %+v", first)
	}
	if first.Binary != "/opt/sidecar" || first.Config != "/etc/sc.json" {
		t.Errorf("per-host paths lost: %+v", first)
	}
	if len(first.Env) != 1 || first.Env[0] != "K=V" {
		t.Errorf("env lost: %+v", first.Env)
	}
	if !cfg.Hosts.List[1].Disabled {
		t.Errorf("disabled lost: %+v", cfg.Hosts.List[1])
	}
}

// TestLoadFromWithoutHostsLeavesNone: an absent section is not an error and
// must not invent a host.
func TestLoadFromWithoutHostsLeavesNone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"ui": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(cfg.Hosts.List) != 0 {
		t.Errorf("hosts = %+v, want none", cfg.Hosts.List)
	}
}
