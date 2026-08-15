package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestInit(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current

	// Use InitWithDir to avoid reading real user state
	err := InitWithDir(filepath.Join(tmpDir, ".config", "sidecar"))
	if err != nil {
		t.Fatalf("InitWithDir() failed: %v", err)
	}

	if current == nil {
		t.Error("current state should be initialized")
	}
	if current.GitDiffMode != "unified" {
		t.Errorf("default GitDiffMode = %q, want unified", current.GitDiffMode)
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestLoad_NonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current

	path = filepath.Join(tmpDir, "nonexistent", "state.json")

	err := Load()
	if err != nil {
		t.Fatalf("Load() for non-existent file should return nil, got %v", err)
	}

	if current == nil {
		t.Error("current should be initialized with defaults")
	}
	if current.GitDiffMode != "unified" {
		t.Errorf("default GitDiffMode = %q, want unified", current.GitDiffMode)
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestLoad_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile

	// Create a state file
	testState := State{GitDiffMode: "side-by-side"}
	data, _ := json.Marshal(testState)
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		t.Fatalf("failed to write test state file: %v", err)
	}

	err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if current.GitDiffMode != "side-by-side" {
		t.Errorf("GitDiffMode = %q, want side-by-side", current.GitDiffMode)
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestLoad_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile

	// Create invalid JSON file
	if err := os.WriteFile(stateFile, []byte("invalid json"), 0644); err != nil {
		t.Fatalf("failed to write invalid JSON: %v", err)
	}

	err := Load()
	if err == nil {
		t.Error("Load() should return error for invalid JSON")
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestSave(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current

	stateFile := filepath.Join(tmpDir, "config", "sidecar", "state.json")
	path = stateFile

	current = &State{GitDiffMode: "side-by-side"}

	err := Save()
	if err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state file not created: %v", err)
	}

	// Verify contents
	data, _ := os.ReadFile(stateFile)
	var loaded State
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("failed to unmarshal saved state: %v", err)
	}

	if loaded.GitDiffMode != "side-by-side" {
		t.Errorf("saved GitDiffMode = %q, want side-by-side", loaded.GitDiffMode)
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestSave_CreateDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current

	stateFile := filepath.Join(tmpDir, "deep", "nested", "config", "sidecar", "state.json")
	path = stateFile

	current = &State{GitDiffMode: "unified"}

	err := Save()
	if err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state file not created: %v", err)
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestSave_NilCurrent(t *testing.T) {
	originalPath := path
	originalCurrent := current

	current = nil
	path = "/tmp/nonexistent/state.json"

	// Should not error when current is nil
	err := Save()
	if err != nil {
		t.Fatalf("Save() with nil current should not error, got %v", err)
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestGetGitDiffMode_Default(t *testing.T) {
	originalCurrent := current

	current = nil
	mode := GetGitDiffMode()
	if mode != "unified" {
		t.Errorf("GetGitDiffMode() with nil current = %q, want unified", mode)
	}

	// Cleanup
	current = originalCurrent
}

func TestGetGitDiffMode_Set(t *testing.T) {
	originalCurrent := current

	current = &State{GitDiffMode: "side-by-side"}
	mode := GetGitDiffMode()
	if mode != "side-by-side" {
		t.Errorf("GetGitDiffMode() = %q, want side-by-side", mode)
	}

	// Cleanup
	current = originalCurrent
}

func TestSetGitDiffMode(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile

	current = &State{GitDiffMode: "unified"}

	err := SetGitDiffMode("side-by-side")
	if err != nil {
		t.Fatalf("SetGitDiffMode() failed: %v", err)
	}

	// Verify in-memory value
	if current.GitDiffMode != "side-by-side" {
		t.Errorf("current.GitDiffMode = %q, want side-by-side", current.GitDiffMode)
	}

	// Verify saved to disk
	data, _ := os.ReadFile(stateFile)
	var loaded State
	_ = json.Unmarshal(data, &loaded)
	if loaded.GitDiffMode != "side-by-side" {
		t.Errorf("saved GitDiffMode = %q, want side-by-side", loaded.GitDiffMode)
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestSetGitDiffMode_InitializesNilState(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile

	current = nil

	err := SetGitDiffMode("side-by-side")
	if err != nil {
		t.Fatalf("SetGitDiffMode() failed: %v", err)
	}

	if current == nil {
		t.Error("SetGitDiffMode() should initialize current state")
	}
	if current.GitDiffMode != "side-by-side" {
		t.Errorf("GitDiffMode = %q, want side-by-side", current.GitDiffMode)
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestGetGitGraphEnabled_Default(t *testing.T) {
	current = nil
	enabled := GetGitGraphEnabled()
	if enabled {
		t.Errorf("GetGitGraphEnabled() with nil current = %v, want false", enabled)
	}
}

func TestGetGitGraphEnabled_Set(t *testing.T) {
	current = &State{GitGraphEnabled: true}
	enabled := GetGitGraphEnabled()
	if !enabled {
		t.Errorf("GetGitGraphEnabled() = %v, want true", enabled)
	}
}

func TestSetGitGraphEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile
	current = &State{GitGraphEnabled: false}

	err := SetGitGraphEnabled(true)
	if err != nil {
		t.Fatalf("SetGitGraphEnabled() failed: %v", err)
	}

	if !current.GitGraphEnabled {
		t.Errorf("current.GitGraphEnabled = %v, want true", current.GitGraphEnabled)
	}

	// Verify saved to disk
	data, _ := os.ReadFile(stateFile)
	var loaded State
	_ = json.Unmarshal(data, &loaded)
	if !loaded.GitGraphEnabled {
		t.Errorf("saved GitGraphEnabled = %v, want true", loaded.GitGraphEnabled)
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile

	current = &State{GitDiffMode: "unified"}

	// Run concurrent reads and writes
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			mode := "unified"
			if n%2 == 0 {
				mode = "side-by-side"
			}
			if err := SetGitDiffMode(mode); err != nil {
				errors <- err
			}
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = GetGitDiffMode()
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		if err != nil {
			t.Errorf("concurrent access error: %v", err)
		}
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile

	// Set and save
	current = &State{GitDiffMode: "side-by-side"}
	if err := Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Load into fresh state
	current = nil
	if err := Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if current.GitDiffMode != "side-by-side" {
		t.Errorf("round-trip GitDiffMode = %q, want side-by-side", current.GitDiffMode)
	}

	// Cleanup
	path = originalPath
	current = originalCurrent
}

func TestGetWorkspaceState_Default(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = nil
	state := GetWorkspaceState("/path/to/project")
	if state.WorkspaceName != "" || state.ShellTmuxName != "" || len(state.ShellDisplayNames) > 0 {
		t.Errorf("GetWorkspaceState() with nil current should return empty state")
	}
}

func TestWorktreeContentStateMigratesAdditivelyFromProjectRoot(t *testing.T) {
	originalPath, originalCurrent := path, current
	defer func() { path, current = originalPath, originalCurrent }()
	path = filepath.Join(t.TempDir(), "state.json")
	root, worktree := "/repos/sidecar", "/repos/sidecar-feature"
	current = &State{
		FileBrowser:  map[string]FileBrowserState{root: {SelectedFile: "root.go"}},
		ActivePlugin: map[string]string{root: "git-status"},
	}

	if got := GetFileBrowserStateForWorkDir(worktree, root); got.SelectedFile != "root.go" {
		t.Fatalf("migrated file = %q, want root.go", got.SelectedFile)
	}
	if got := GetActivePluginForWorkDir(worktree, root); got != "git-status" {
		t.Fatalf("migrated active plugin = %q, want git-status", got)
	}
	if _, ok := current.FileBrowser[root]; !ok {
		t.Fatal("migration removed legacy file-browser root entry")
	}
	if _, ok := current.ActivePlugin[root]; !ok {
		t.Fatal("migration removed legacy active-plugin root entry")
	}

	current.FileBrowser[worktree] = FileBrowserState{SelectedFile: "feature.go"}
	current.ActivePlugin[worktree] = "file-browser"
	if got := GetFileBrowserStateForWorkDir(worktree, root); got.SelectedFile != "feature.go" {
		t.Fatalf("existing worktree file = %q, want feature.go", got.SelectedFile)
	}
	if got := GetActivePluginForWorkDir(worktree, root); got != "file-browser" {
		t.Fatalf("existing worktree plugin = %q, want file-browser", got)
	}
}

func TestWorktreeContentStateRestoresAToBToA(t *testing.T) {
	originalPath, originalCurrent := path, current
	defer func() { path, current = originalPath, originalCurrent }()
	path = filepath.Join(t.TempDir(), "state.json")
	current = &State{}
	root, a, b := "/repo", "/repo-a", "/repo-b"

	if err := SetFileBrowserState(a, FileBrowserState{SelectedFile: "file-a"}); err != nil {
		t.Fatal(err)
	}
	if err := SetActivePlugin(a, "git-status"); err != nil {
		t.Fatal(err)
	}
	if err := SetFileBrowserState(b, FileBrowserState{SelectedFile: "file-b"}); err != nil {
		t.Fatal(err)
	}
	if err := SetActivePlugin(b, "file-browser"); err != nil {
		t.Fatal(err)
	}

	if got := GetFileBrowserStateForWorkDir(a, root).SelectedFile; got != "file-a" {
		t.Fatalf("A after A to B to A = %q, want file-a", got)
	}
	if got := GetActivePluginForWorkDir(a, root); got != "git-status" {
		t.Fatalf("A plugin after A to B to A = %q, want git-status", got)
	}
}

func TestGetWorkspaceState_EmptyMap(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = &State{Workspace: nil}
	state := GetWorkspaceState("/path/to/project")
	if state.WorkspaceName != "" || state.ShellTmuxName != "" || len(state.ShellDisplayNames) > 0 {
		t.Errorf("GetWorkspaceState() with nil map should return empty state")
	}
}

func TestGetWorkspaceState_Found(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = &State{
		Workspace: map[string]WorkspaceState{
			"/path/to/project": {
				WorkspaceName: "feature-branch",
				ShellTmuxName: "sidecar-sh-project-1",
				ShellDisplayNames: map[string]string{
					"sidecar-sh-project-1": "Backend",
				},
			},
		},
	}
	state := GetWorkspaceState("/path/to/project")
	if state.WorkspaceName != "feature-branch" {
		t.Errorf("WorkspaceName = %q, want feature-branch", state.WorkspaceName)
	}
	if state.ShellTmuxName != "sidecar-sh-project-1" {
		t.Errorf("ShellTmuxName = %q, want sidecar-sh-project-1", state.ShellTmuxName)
	}
	if state.ShellDisplayNames["sidecar-sh-project-1"] != "Backend" {
		t.Errorf("ShellDisplayNames[sidecar-sh-project-1] = %q, want Backend", state.ShellDisplayNames["sidecar-sh-project-1"])
	}
}

func TestSetWorkspaceState(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile
	current = &State{}

	wtState := WorkspaceState{
		WorkspaceName: "my-workspace",
		ShellTmuxName: "",
		ShellDisplayNames: map[string]string{
			"sidecar-sh-project-1": "Backend",
		},
	}

	err := SetWorkspaceState("/projects/sidecar", wtState)
	if err != nil {
		t.Fatalf("SetWorkspaceState() failed: %v", err)
	}

	// Verify in memory
	stored := current.Workspace["/projects/sidecar"]
	if stored.WorkspaceName != "my-workspace" {
		t.Errorf("stored WorkspaceName = %q, want my-workspace", stored.WorkspaceName)
	}
	if stored.ShellDisplayNames["sidecar-sh-project-1"] != "Backend" {
		t.Errorf("stored ShellDisplayNames[sidecar-sh-project-1] = %q, want Backend", stored.ShellDisplayNames["sidecar-sh-project-1"])
	}

	// Verify saved to disk
	data, _ := os.ReadFile(stateFile)
	var loaded State
	_ = json.Unmarshal(data, &loaded)
	if loaded.Workspace["/projects/sidecar"].WorkspaceName != "my-workspace" {
		t.Errorf("persisted WorkspaceName = %q, want my-workspace", loaded.Workspace["/projects/sidecar"].WorkspaceName)
	}
	if loaded.Workspace["/projects/sidecar"].ShellDisplayNames["sidecar-sh-project-1"] != "Backend" {
		t.Errorf("persisted ShellDisplayNames[sidecar-sh-project-1] = %q, want Backend", loaded.Workspace["/projects/sidecar"].ShellDisplayNames["sidecar-sh-project-1"])
	}
}

func TestSetWorkspaceState_ShellSelection(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	path = filepath.Join(tmpDir, "state.json")
	current = &State{}

	// Save shell selection
	wtState := WorkspaceState{
		WorkspaceName: "",
		ShellTmuxName: "sidecar-sh-project-2",
	}

	err := SetWorkspaceState("/projects/myapp", wtState)
	if err != nil {
		t.Fatalf("SetWorkspaceState() failed: %v", err)
	}

	// Verify
	stored := current.Workspace["/projects/myapp"]
	if stored.ShellTmuxName != "sidecar-sh-project-2" {
		t.Errorf("stored ShellTmuxName = %q, want sidecar-sh-project-2", stored.ShellTmuxName)
	}
	if stored.WorkspaceName != "" {
		t.Errorf("stored WorkspaceName = %q, want empty", stored.WorkspaceName)
	}
}

func TestWorkspacePaneLayoutJSONRoundTrip(t *testing.T) {
	want := State{Workspace: map[string]WorkspaceState{"/repo": {
		ShellTmuxName: "shell-1",
		PaneLayout: &PaneLayoutJSON{Root: "/repo", Surface: "shell:shell-1", Split: &PaneSplitJSON{
			Axis: "cols", Ratio: 63,
			A: &PaneLayoutJSON{Kind: "terminal"},
			B: &PaneLayoutJSON{Kind: "doc", Active: 0, Tabs: []PaneDocTabJSON{{Path: "README.md", Mode: "raw"}}},
		}},
		PaneLayouts: map[string]*PaneLayoutJSON{
			"shell:shell-1": {Root: "/repo", Surface: "shell:shell-1", Open: true, Split: &PaneSplitJSON{
				Axis: "cols", Ratio: 63,
				A: &PaneLayoutJSON{Kind: "terminal"},
				B: &PaneLayoutJSON{Kind: "doc", Active: 0, Tabs: []PaneDocTabJSON{{Path: "README.md", Mode: "raw"}}},
			}},
		},
	}}}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got State
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	layout := got.Workspace["/repo"].PaneLayout
	if layout == nil || layout.Root != "/repo" || layout.Surface != "shell:shell-1" || layout.Split == nil || layout.Split.Ratio != 63 ||
		layout.Split.B == nil || len(layout.Split.B.Tabs) != 1 || layout.Split.B.Tabs[0].Mode != "raw" {
		t.Fatalf("pane layout round trip = %#v", layout)
	}
	mapped := got.Workspace["/repo"].PaneLayouts["shell:shell-1"]
	if mapped == nil || !mapped.Open || mapped.Split == nil || mapped.Split.Ratio != 63 ||
		mapped.Split.B == nil || len(mapped.Split.B.Tabs) != 1 || mapped.Split.B.Tabs[0].Path != "README.md" {
		t.Fatalf("pane layouts map round trip = %#v", got.Workspace["/repo"].PaneLayouts)
	}

	var malformed State
	if err := json.Unmarshal([]byte(`{"workspace":{"/repo":{"paneLayout":{"split":{"axis":7}}}}}`), &malformed); err == nil {
		t.Fatal("malformed pane layout JSON unexpectedly decoded")
	}
}

func TestPaneIssueTabJSONRoundTripOmitsLegacyScalars(t *testing.T) {
	want := PaneLayoutJSON{
		Kind:   "issue",
		Active: 1,
		IssueTabs: []PaneIssueTabJSON{
			{Issue: "td-1111aa", Scroll: 2},
			{Issue: "td-2222bb"},
		},
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(encoded)
	if strings.Contains(raw, `"issue":"td-1111aa"`) && !strings.Contains(raw, `"issueTabs"`) {
		t.Fatalf("encoded as legacy issue: %s", raw)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["issue"]; ok {
		t.Fatalf("legacy issue field present: %s", raw)
	}
	if _, ok := decoded["scroll"]; ok {
		t.Fatalf("legacy scroll field present: %s", raw)
	}

	var got PaneLayoutJSON
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != "issue" || got.Active != 1 || len(got.IssueTabs) != 2 ||
		got.IssueTabs[0].Issue != "td-1111aa" || got.IssueTabs[0].Scroll != 2 ||
		got.IssueTabs[1].Issue != "td-2222bb" || got.Issue != "" || got.Scroll != 0 {
		t.Fatalf("issue tabs round trip = %#v", got)
	}

	var legacy PaneLayoutJSON
	if err := json.Unmarshal([]byte(`{"kind":"issue","issue":"td-1a2b3c","scroll":4}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Issue != "td-1a2b3c" || legacy.Scroll != 4 || legacy.IssueTabs != nil {
		t.Fatalf("legacy decode = %#v", legacy)
	}
}

func TestMigratePaneLayoutsCopiesLegacySlot(t *testing.T) {
	legacy := &PaneLayoutJSON{Root: "/repo", Surface: "shell:A", Kind: "terminal"}
	s := WorkspaceState{PaneLayout: legacy}
	MigratePaneLayouts(&s)
	if got := s.PaneLayoutFor("shell:A"); got != legacy {
		t.Fatalf("migrated map = %#v, want the legacy record at shell:A", s.PaneLayouts)
	}
	if s.PaneLayout != legacy {
		t.Fatal("migrate cleared the legacy field")
	}

	kept := &PaneLayoutJSON{Root: "/repo", Surface: "shell:B", Kind: "doc"}
	s = WorkspaceState{
		PaneLayout:  legacy,
		PaneLayouts: map[string]*PaneLayoutJSON{"shell:B": kept},
	}
	MigratePaneLayouts(&s)
	if s.PaneLayoutFor("shell:A") != nil || s.PaneLayoutFor("shell:B") != kept {
		t.Fatalf("non-empty map was overwritten: %#v", s.PaneLayouts)
	}

	emptySurface := WorkspaceState{PaneLayout: &PaneLayoutJSON{Root: "/repo", Kind: "terminal"}}
	MigratePaneLayouts(&emptySurface)
	if emptySurface.PaneLayouts != nil {
		t.Fatalf("empty-surface legacy was keyed: %#v", emptySurface.PaneLayouts)
	}

	legacySplit := &PaneLayoutJSON{Root: "/repo", Surface: "shell:C", Split: &PaneSplitJSON{
		Axis: "cols", Ratio: 50,
		A: &PaneLayoutJSON{Kind: "terminal"},
		B: &PaneLayoutJSON{Kind: "doc", Tabs: []PaneDocTabJSON{{Path: "README.md"}}},
	}}
	s = WorkspaceState{PaneLayout: legacySplit}
	MigratePaneLayouts(&s)
	if !legacySplit.Open || !PaneLayoutOpen(s.PaneLayoutFor("shell:C")) {
		t.Fatal("legacy split omitted Open was not treated as open")
	}
}

func TestRekeyPaneLayoutMigratesLegacySurfaceWithoutOverwritingCanonical(t *testing.T) {
	legacy := &PaneLayoutJSON{Root: "/repo", Surface: "workspace:old", Kind: "terminal"}
	s := WorkspaceState{PaneLayouts: map[string]*PaneLayoutJSON{
		"workspace:old": legacy,
		"shell:sibling": {Surface: "shell:sibling", Kind: "terminal"},
	}}
	got, changed := RekeyPaneLayout(&s, "workspace:old", "workspace:canonical")
	if !changed || got != legacy || got.Surface != "workspace:canonical" {
		t.Fatalf("rekey = %#v changed=%v", got, changed)
	}
	if s.PaneLayouts["workspace:old"] != nil || s.PaneLayouts["workspace:canonical"] != legacy || s.PaneLayouts["shell:sibling"] == nil {
		t.Fatalf("rekey map = %#v", s.PaneLayouts)
	}

	canonical := &PaneLayoutJSON{Surface: "workspace:canonical", Kind: "doc"}
	s.PaneLayouts["workspace:old"] = legacy
	s.PaneLayouts["workspace:canonical"] = canonical
	got, changed = RekeyPaneLayout(&s, "workspace:old", "workspace:canonical")
	if !changed || got != canonical || s.PaneLayouts["workspace:old"] != nil {
		t.Fatalf("canonical winner = %#v changed=%v map=%#v", got, changed, s.PaneLayouts)
	}
}

func TestForgetPaneLayoutsPreservesSiblingsAndWritesLastEntryRemoval(t *testing.T) {
	legacy := &PaneLayoutJSON{Surface: "shell:A", Kind: "terminal"}
	s := WorkspaceState{
		PaneLayout: legacy,
		PaneLayouts: map[string]*PaneLayoutJSON{
			"shell:A": legacy,
			"shell:B": {Surface: "shell:B", Kind: "terminal"},
		},
	}
	if !ForgetPaneLayouts(&s, "shell:A") {
		t.Fatal("forget reported no change")
	}
	if s.PaneLayout != nil || s.PaneLayouts["shell:A"] != nil || s.PaneLayouts["shell:B"] == nil {
		t.Fatalf("forget A = %#v", s)
	}
	if !ForgetPaneLayouts(&s, "shell:B") || s.PaneLayouts != nil {
		t.Fatalf("last entry was not removed: %#v", s.PaneLayouts)
	}
	if ForgetPaneLayouts(&s, "shell:missing") {
		t.Fatal("missing surface reported a change")
	}
}

func TestGetLastWorktreePath_Default(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = nil
	result := GetLastWorktreePath("/main/repo")
	if result != "" {
		t.Errorf("GetLastWorktreePath() with nil current = %q, want empty", result)
	}
}

func TestGetLastWorktreePath_EmptyMap(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = &State{LastWorktreePath: nil}
	result := GetLastWorktreePath("/main/repo")
	if result != "" {
		t.Errorf("GetLastWorktreePath() with nil map = %q, want empty", result)
	}
}

func TestGetLastWorktreePath_Found(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = &State{
		LastWorktreePath: map[string]string{
			"/main/repo": "/worktrees/feature-auth",
		},
	}
	result := GetLastWorktreePath("/main/repo")
	if result != "/worktrees/feature-auth" {
		t.Errorf("GetLastWorktreePath() = %q, want /worktrees/feature-auth", result)
	}
}

func TestSetLastWorktreePath(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile
	current = &State{}

	err := SetLastWorktreePath("/main/repo", "/worktrees/feature-billing")
	if err != nil {
		t.Fatalf("SetLastWorktreePath() failed: %v", err)
	}

	// Verify in memory
	if current.LastWorktreePath["/main/repo"] != "/worktrees/feature-billing" {
		t.Errorf("stored path = %q, want /worktrees/feature-billing", current.LastWorktreePath["/main/repo"])
	}

	// Verify saved to disk
	data, _ := os.ReadFile(stateFile)
	var loaded State
	_ = json.Unmarshal(data, &loaded)
	if loaded.LastWorktreePath["/main/repo"] != "/worktrees/feature-billing" {
		t.Errorf("persisted path = %q, want /worktrees/feature-billing", loaded.LastWorktreePath["/main/repo"])
	}
}

func TestGetLastGlobalTab_Default(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = nil
	if got := GetLastGlobalTab(); got != "" {
		t.Errorf("GetLastGlobalTab() with nil current = %q, want empty", got)
	}
}

func TestGetLastGlobalTab_Set(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = &State{LastGlobalTab: "workspaces"}
	if got := GetLastGlobalTab(); got != "workspaces" {
		t.Errorf("GetLastGlobalTab() = %q, want workspaces", got)
	}
}

func TestSetLastGlobalTab(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile
	current = &State{}

	if err := SetLastGlobalTab("workspaces"); err != nil {
		t.Fatalf("SetLastGlobalTab() failed: %v", err)
	}
	if current.LastGlobalTab != "workspaces" {
		t.Errorf("current.LastGlobalTab = %q, want workspaces", current.LastGlobalTab)
	}

	data, _ := os.ReadFile(stateFile)
	var loaded State
	_ = json.Unmarshal(data, &loaded)
	if loaded.LastGlobalTab != "workspaces" {
		t.Errorf("saved LastGlobalTab = %q, want workspaces", loaded.LastGlobalTab)
	}
}

func TestSetLastGlobalTab_InitializesNilState(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	path = filepath.Join(tmpDir, "state.json")
	current = nil

	if err := SetLastGlobalTab("tasks"); err != nil {
		t.Fatalf("SetLastGlobalTab() failed: %v", err)
	}
	if current == nil {
		t.Error("SetLastGlobalTab() should initialize current state")
	}
	if current.LastGlobalTab != "tasks" {
		t.Errorf("LastGlobalTab = %q, want tasks", current.LastGlobalTab)
	}
}

func TestGetShowIdleWorktrees_Default(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = nil
	if got := GetShowIdleWorktrees(); got {
		t.Error("GetShowIdleWorktrees() with nil current = true, want false")
	}
}

func TestGetShowIdleWorktrees_Set(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = &State{ShowIdleWorktrees: true}
	if got := GetShowIdleWorktrees(); !got {
		t.Error("GetShowIdleWorktrees() = false, want true")
	}
}

func TestSetShowIdleWorktrees(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile
	current = &State{}

	if err := SetShowIdleWorktrees(true); err != nil {
		t.Fatalf("SetShowIdleWorktrees() failed: %v", err)
	}
	if !current.ShowIdleWorktrees {
		t.Error("current.ShowIdleWorktrees = false, want true")
	}

	data, _ := os.ReadFile(stateFile)
	var loaded State
	_ = json.Unmarshal(data, &loaded)
	if !loaded.ShowIdleWorktrees {
		t.Error("saved ShowIdleWorktrees = false, want true")
	}
	if !strings.Contains(string(data), `"showIdleWorktrees"`) {
		t.Fatalf("persisted JSON should name showIdleWorktrees:\n%s", data)
	}
}

func TestSetShowIdleWorktrees_InitializesNilState(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	path = filepath.Join(tmpDir, "state.json")
	current = nil

	if err := SetShowIdleWorktrees(true); err != nil {
		t.Fatalf("SetShowIdleWorktrees() failed: %v", err)
	}
	if current == nil {
		t.Error("SetShowIdleWorktrees() should initialize current state")
	}
	if !current.ShowIdleWorktrees {
		t.Error("ShowIdleWorktrees = false, want true")
	}
}

func TestGetPinnedWorkspaceIDs_Default(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = nil
	if got := GetPinnedWorkspaceIDs(); got != nil {
		t.Errorf("GetPinnedWorkspaceIDs() with nil current = %v, want nil", got)
	}
}

func TestGetPinnedWorkspaceIDs_Set(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = &State{PinnedWorkspaceIDs: []string{"a", "b"}}
	got := GetPinnedWorkspaceIDs()
	if strings.Join(got, ",") != "a,b" {
		t.Errorf("GetPinnedWorkspaceIDs() = %v, want [a b]", got)
	}
	got[0] = "mutated"
	if current.PinnedWorkspaceIDs[0] != "a" {
		t.Error("GetPinnedWorkspaceIDs() exposed the stored slice")
	}
}

func TestSetPinnedWorkspaceIDs(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile
	current = &State{}

	if err := SetPinnedWorkspaceIDs([]string{"s1", "", "s2", "s1"}); err != nil {
		t.Fatalf("SetPinnedWorkspaceIDs() failed: %v", err)
	}
	if got := strings.Join(current.PinnedWorkspaceIDs, ","); got != "s1,s2" {
		t.Errorf("current.PinnedWorkspaceIDs = %s, want s1,s2", got)
	}

	data, _ := os.ReadFile(stateFile)
	var loaded State
	_ = json.Unmarshal(data, &loaded)
	if got := strings.Join(loaded.PinnedWorkspaceIDs, ","); got != "s1,s2" {
		t.Errorf("saved PinnedWorkspaceIDs = %s, want s1,s2", got)
	}
	if !strings.Contains(string(data), `"pinnedWorkspaceIDs"`) {
		t.Fatalf("persisted JSON should name pinnedWorkspaceIDs:\n%s", data)
	}
}

func TestSetPinnedWorkspaceIDs_InitializesNilState(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	path = filepath.Join(tmpDir, "state.json")
	current = nil

	if err := SetPinnedWorkspaceIDs([]string{"s1"}); err != nil {
		t.Fatalf("SetPinnedWorkspaceIDs() failed: %v", err)
	}
	if current == nil {
		t.Error("SetPinnedWorkspaceIDs() should initialize current state")
	}
	if got := strings.Join(current.PinnedWorkspaceIDs, ","); got != "s1" {
		t.Errorf("PinnedWorkspaceIDs = %s, want s1", got)
	}
}

func TestSetLastWorktreePath_InitializesNilState(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	path = filepath.Join(tmpDir, "state.json")
	current = nil

	err := SetLastWorktreePath("/main/repo", "/worktrees/feature")
	if err != nil {
		t.Fatalf("SetLastWorktreePath() failed: %v", err)
	}

	if current == nil {
		t.Error("SetLastWorktreePath() should initialize current state")
	}
	if current.LastWorktreePath["/main/repo"] != "/worktrees/feature" {
		t.Errorf("path = %q, want /worktrees/feature", current.LastWorktreePath["/main/repo"])
	}
}

func TestClearLastWorktreePath(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	path = filepath.Join(tmpDir, "state.json")
	current = &State{
		LastWorktreePath: map[string]string{
			"/main/repo": "/worktrees/feature",
		},
	}

	err := ClearLastWorktreePath("/main/repo")
	if err != nil {
		t.Fatalf("ClearLastWorktreePath() failed: %v", err)
	}

	// Verify removed
	if _, exists := current.LastWorktreePath["/main/repo"]; exists {
		t.Error("ClearLastWorktreePath() should remove the entry")
	}

	// Verify saved to disk
	data, _ := os.ReadFile(path)
	var loaded State
	_ = json.Unmarshal(data, &loaded)
	if _, exists := loaded.LastWorktreePath["/main/repo"]; exists {
		t.Error("ClearLastWorktreePath() should persist removal")
	}
}

func TestClearLastWorktreePath_NilState(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = nil
	err := ClearLastWorktreePath("/main/repo")
	if err != nil {
		t.Fatalf("ClearLastWorktreePath() with nil state should not error: %v", err)
	}
}

func TestClearLastWorktreePath_NilMap(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = &State{LastWorktreePath: nil}
	err := ClearLastWorktreePath("/main/repo")
	if err != nil {
		t.Fatalf("ClearLastWorktreePath() with nil map should not error: %v", err)
	}
}

func TestGetLineWrapEnabled_Default(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = nil
	enabled := GetLineWrapEnabled()
	if enabled {
		t.Errorf("GetLineWrapEnabled() with nil current = %v, want false", enabled)
	}
}

func TestGetLineWrapEnabled_Set(t *testing.T) {
	originalCurrent := current
	defer func() { current = originalCurrent }()

	current = &State{LineWrapEnabled: true}
	enabled := GetLineWrapEnabled()
	if !enabled {
		t.Errorf("GetLineWrapEnabled() = %v, want true", enabled)
	}
}

func TestSetLineWrapEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile
	current = &State{LineWrapEnabled: false}

	err := SetLineWrapEnabled(true)
	if err != nil {
		t.Fatalf("SetLineWrapEnabled() failed: %v", err)
	}

	if !current.LineWrapEnabled {
		t.Errorf("current.LineWrapEnabled = %v, want true", current.LineWrapEnabled)
	}

	// Verify saved to disk
	data, _ := os.ReadFile(stateFile)
	var loaded State
	_ = json.Unmarshal(data, &loaded)
	if !loaded.LineWrapEnabled {
		t.Errorf("saved LineWrapEnabled = %v, want true", loaded.LineWrapEnabled)
	}
}

func TestSetLineWrapEnabled_InitializesNilState(t *testing.T) {
	tmpDir := t.TempDir()
	originalPath := path
	originalCurrent := current
	defer func() {
		path = originalPath
		current = originalCurrent
	}()

	stateFile := filepath.Join(tmpDir, "state.json")
	path = stateFile
	current = nil

	err := SetLineWrapEnabled(true)
	if err != nil {
		t.Fatalf("SetLineWrapEnabled() failed: %v", err)
	}

	if current == nil {
		t.Error("SetLineWrapEnabled() should initialize current state")
	}
	if !current.LineWrapEnabled {
		t.Errorf("LineWrapEnabled = %v, want true", current.LineWrapEnabled)
	}
}
