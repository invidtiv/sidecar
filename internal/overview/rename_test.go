package overview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
)

func TestROnShellOpensRenameModalPrefillingTheName(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	if !m.workspaces.SelectID("c") {
		t.Fatal("could not select the shell")
	}

	handled, cmd := m.WorkspacesKey(key("R"))
	if !handled {
		t.Fatal("R on a shell was not handled")
	}
	run(t, m, cmd)
	if !m.RenameShellOpen() {
		t.Fatal("R on a shell did not open the rename modal")
	}
	if m.renameInput.Value() != "charlie" {
		t.Fatalf("prefill = %q, want charlie", m.renameInput.Value())
	}
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "Rename Shell") {
		t.Fatalf("modal missing title:\n%s", view)
	}
	if !strings.Contains(view, "Current:") || !strings.Contains(view, "charlie") {
		t.Fatalf("modal missing current name:\n%s", view)
	}
	if strings.Contains(view, "sc-sh") {
		t.Fatalf("rename modal showed the tmux session name:\n%s", view)
	}
}

func TestROnWorktreeIsIgnored(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.workspaces.SelectID("a")
	handled, cmd := m.WorkspacesKey(key("R"))
	if handled || cmd != nil || m.RenameShellOpen() {
		t.Fatalf("R on a worktree opened rename (handled=%v cmd=%v)", handled, cmd != nil)
	}
}

func TestRWhileTypingGoesToThePane(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if !handled {
		t.Fatal("interactive R was not handled")
	}
	run(t, m, cmd)
	if m.RenameShellOpen() {
		t.Fatal("typing R opened the rename modal")
	}
	if len(terminal.keys) == 0 || terminal.keys[len(terminal.keys)-1] != "R" {
		t.Fatalf("pane keys = %v, want R", terminal.keys)
	}
}

func TestRenameModalEscClosesWithoutWriting(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.workspaces.SelectID("c")
	press(t, m, "R")
	if !m.RenameShellOpen() {
		t.Fatal("premise: modal should be open")
	}
	m.renameInput.SetValue("should-not-persist")
	press(t, m, "esc")
	if m.RenameShellOpen() {
		t.Fatal("esc did not close the rename modal")
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.Name != "charlie" {
		t.Fatalf("esc wrote the name: %#v", workspace)
	}
}

func TestRenameModalQTypesIntoTheName(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.workspaces.SelectID("c")
	press(t, m, "R")
	if !m.RenameShellOpen() {
		t.Fatal("premise: modal should be open")
	}
	before := m.renameInput.Value()
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if !handled {
		t.Fatal("q in the rename field was not handled")
	}
	run(t, m, cmd)
	if !m.RenameShellOpen() {
		t.Fatal("q closed the rename modal instead of typing")
	}
	if !strings.Contains(m.renameInput.Value(), "q") {
		t.Fatalf("name after q = %q, want it to contain q (was %q)", m.renameInput.Value(), before)
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.Name != "charlie" {
		t.Fatalf("typing q persisted the name: %#v", workspace)
	}
}

func TestInvalidRenameNameStaysInModalWithSharedError(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.workspaces.SelectID("c")
	press(t, m, "R")
	m.renameInput.SetValue("")
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled {
		t.Fatal("enter in the rename modal was not handled")
	}
	if cmd != nil {
		t.Fatal("invalid name scheduled persistence")
	}
	if !m.RenameShellOpen() {
		t.Fatal("invalid name dismissed the modal")
	}
	_, sharedErr := shellstate.NormalizeName("")
	if sharedErr == nil || m.renameError != sharedErr.Error() {
		t.Fatalf("modal error = %q, shared = %v", m.renameError, sharedErr)
	}
	view := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(view, "Error:") {
		t.Fatalf("validation error not shown:\n%s", view)
	}
}

func TestRenamePersistsToOwningProjectManifest(t *testing.T) {
	stateDir := t.TempDir()
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)

	// Persist against a project Sidecar is not sitting in.
	projectRoot := filepath.Join(t.TempDir(), "other-repo")
	projectState, err := projectdir.Resolve(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	const namespace = "/tmp/socket"
	writeShellManifest(t, filepath.Join(projectState, "shells.json"), []shellstate.Definition{
		{TmuxName: "sc-sh", DisplayName: "charlie", Namespace: namespace},
		{TmuxName: "sc-other", DisplayName: "taken", Namespace: namespace},
	})

	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	stampShellOwner(m, "c", projectRoot, "sc-sh", namespace)

	press(t, m, "R")
	m.renameInput.SetValue("new context")
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || cmd == nil {
		t.Fatal("confirm did not schedule persistence")
	}
	run(t, m, cmd)
	if m.RenameShellOpen() {
		t.Fatalf("successful rename left the modal open: %s", m.renameError)
	}

	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.Name != "new context" {
		t.Fatalf("row name = %#v, want new context", workspace)
	}
	got := readShellManifest(t, filepath.Join(projectState, "shells.json"))
	if got[0].DisplayName != "new context" {
		t.Fatalf("owning manifest name = %q", got[0].DisplayName)
	}
	if got[0].TmuxName != "sc-sh" {
		t.Fatalf("tmux name was rewritten to %q", got[0].TmuxName)
	}
	if got[1].DisplayName != "taken" {
		t.Fatal("sibling shell was rewritten")
	}
}

func TestRenameDeadShellStillWritesManifest(t *testing.T) {
	stateDir := t.TempDir()
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)

	projectRoot := filepath.Join(t.TempDir(), "idle-repo")
	projectState, err := projectdir.Resolve(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeShellManifest(t, filepath.Join(projectState, "shells.json"), []shellstate.Definition{
		{TmuxName: "sc-sh", DisplayName: "charlie", Namespace: ""},
	})

	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	stampShellOwner(m, "c", projectRoot, "sc-sh", "")
	// Ambiguous / no live pane: still renameable. Manifest is the authority.
	if ws, _ := m.SelectedWorkspace(); ws.Live {
		t.Fatal("premise: fixture shell c should not be a live pane")
	}

	press(t, m, "R")
	m.renameInput.SetValue("offline name")
	_, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	run(t, m, cmd)
	if m.RenameShellOpen() {
		t.Fatalf("dead-shell rename stayed open: %s", m.renameError)
	}
	got := readShellManifest(t, filepath.Join(projectState, "shells.json"))
	if got[0].DisplayName != "offline name" {
		t.Fatalf("dead-shell manifest = %q", got[0].DisplayName)
	}
}

func TestListCommandsAdvertiseRenameOnlyForAShell(t *testing.T) {
	m, _ := previewModel(t)
	m.workspaces.SelectID("a")
	for _, cmd := range m.Commands() {
		if cmd.Name == "Rename" {
			t.Fatalf("worktree Commands() advertised Rename: %#v", m.Commands())
		}
	}
	m.workspaces.SelectID("c")
	var found bool
	for _, cmd := range m.Commands() {
		if cmd.Key == "R" && cmd.Name == "Rename" {
			found = true
		}
	}
	if !found {
		t.Fatalf("shell Commands() omitted Rename: %#v", m.Commands())
	}
}

func stampShellOwner(m *Model, id, projectRoot, tmuxName, namespace string) {
	ws := m.catalog[id]
	ws.ProjectRoot = projectRoot
	ws.ProjectKey = "sidecar"
	ws.TmuxName = tmuxName
	ws.Key = tmuxName
	ws.Namespace = namespace
	m.catalog[id] = ws
	result := m.results[ws.ProjectKey]
	for i := range result.Workspaces {
		if result.Workspaces[i].ID == id {
			result.Workspaces[i] = ws
			break
		}
	}
	m.results[ws.ProjectKey] = result
	m.syncBoard()
	m.workspaces.SelectID(id)
}

func writeShellManifest(t *testing.T, path string, shells []shellstate.Definition) {
	t.Helper()
	data, err := json.MarshalIndent(struct {
		Version int                     `json:"version"`
		Shells  []shellstate.Definition `json:"shells"`
	}{Version: 1, Shells: shells}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readShellManifest(t *testing.T, path string) []shellstate.Definition {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Shells []shellstate.Definition `json:"shells"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed.Shells
}
