package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// Slice 4 items 1, 2 and 4 of docs/plans/active/global-overview-workspaces.md,
// from the app's side of the journey: an activation raised by the global
// Workspaces browser is validated, lands on the exact owning project workspace,
// and reaches the plugin as a pending selection and nothing else.
//
// The browser's own half — which key and which click raise the request, and
// which identity travels — is proved in internal/overview/navigate_test.go. The
// Agents board's identical path, the pre-validation races, and the destination
// switcher are already covered in overview_test.go and are not restated here.

// globalNavigationModel is a two-project model sitting on the global Workspaces
// tab with a real (validating) collector behind the Overview.
func globalNavigationModel(t *testing.T) (Model, string, map[string]*navigationPlugin) {
	t.Helper()
	stateBase = t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(config.ResetTestConfigPath)
	// Sidecar's own state file goes to an isolated directory too: navigation
	// records the project it landed on, and no test may write the real one.
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	source := newOverviewGitRepo(t, "source")
	cfg := config.Default()
	km := keymap.NewRegistry()
	ctx := &plugin.Context{WorkDir: source, ProjectRoot: source, Config: cfg, Keymap: km}
	reg := plugin.NewRegistry(ctx)
	plugins := map[string]*navigationPlugin{}
	for _, id := range []string{"git", workspacePluginID} {
		p := &navigationPlugin{id: id}
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
		plugins[id] = p
	}
	m := New(reg, km, cfg, "", source, source, "git")
	m.overview = overview.New(workspaceinventory.Collector{})
	m.scope, m.globalTab = ScopeGlobal, GlobalWorkspaces
	m.width, m.height, m.ready = 140, 40, true
	m.intro.Active, m.intro.Done = false, true
	m.updateContext()
	return m, source, plugins
}

// openFromGlobalWorkspaces runs the whole activation journey: the request the
// browser raises, the validation it schedules, and the result.
func openFromGlobalWorkspaces(t *testing.T, m Model, workspace workspaceinventory.Workspace) (Model, tea.Cmd) {
	t.Helper()
	request := overviewNavigation(t, m.overview, workspace)
	updated, validationCmd := m.Update(request)
	m = asAppModel(t, updated)
	if validationCmd == nil {
		t.Fatal("the global Workspaces tab did not schedule validation")
	}
	validation, ok := validationCmd().(overview.ValidationMsg)
	if !ok {
		t.Fatalf("validation command produced %T", validationCmd())
	}
	updated, cmd := m.Update(validation)
	return asAppModel(t, updated), cmd
}

func TestGlobalWorkspacesOpensAPlainWorktreeInAnotherProject(t *testing.T) {
	m, source, plugins := globalNavigationModel(t)
	target := newOverviewGitRepo(t, "target")

	// A main worktree with no agent at all is a real destination: the catalog
	// lists it, so opening it has to work without a fabricated agent identity.
	workspace := overviewWorkspace(target)
	workspace.Plain = true
	updated, cmd := openFromGlobalWorkspaces(t, m, workspace)

	if updated.inGlobalScope() {
		t.Fatal("opening an item stayed in the global space")
	}
	if updated.ui.WorkDir != target {
		t.Fatalf("work dir = %q, want the opened project %q", updated.ui.WorkDir, target)
	}
	workspacePlugin := plugins[workspacePluginID]
	if !workspacePlugin.focused || updated.activePlugin != 1 {
		t.Fatalf("opened item did not focus project Workspaces: focused=%v active=%d", workspacePlugin.focused, updated.activePlugin)
	}
	if workspacePlugin.pending == nil || workspacePlugin.pending.Path != target ||
		workspacePlugin.pending.Kind != plugin.WorkspaceSelectionWorktree {
		t.Fatalf("pending selection = %#v", workspacePlugin.pending)
	}
	if workspacePlugin.keyInputs != 0 || plugins["git"].keyInputs != 0 {
		t.Fatal("opening an item sent keys to a plugin")
	}
	if cmd == nil {
		t.Fatal("project switch returned no commands")
	}
	if updated.ui.ProjectRoot == source {
		t.Fatalf("project root stayed on the source project: %q", updated.ui.ProjectRoot)
	}
}

func TestGlobalWorkspacesOpensAShellInTheCurrentProjectWithoutReinit(t *testing.T) {
	m, source, plugins := globalNavigationModel(t)

	// A durable shell of the project already open: no switch, no reinit, just
	// the exact pending selection and focus.
	writeShellManifest(t, source, "sc-shell-2")
	workspace := workspaceinventory.Workspace{
		ProjectKey:  workspaceinventory.CanonicalPath(source),
		ProjectRoot: source,
		Kind:        workspaceinventory.KindShell,
		Key:         "sc-shell-2",
		TmuxName:    "sc-shell-2",
		Path:        source,
	}
	inits := totalNavigationInits(plugins)
	updated, _ := openFromGlobalWorkspaces(t, m, workspace)

	if updated.ui.WorkDir != source || totalNavigationInits(plugins) != inits {
		t.Fatalf("same-project open moved or reinitialized the project: work=%q inits=%d", updated.ui.WorkDir, totalNavigationInits(plugins))
	}
	if updated.inGlobalScope() {
		t.Fatal("same-project open stayed in the global space")
	}
	pending := plugins[workspacePluginID].pending
	if pending == nil || pending.Kind != plugin.WorkspaceSelectionShell || pending.Key != "sc-shell-2" {
		t.Fatalf("pending shell selection = %#v", pending)
	}
	if !plugins[workspacePluginID].focused {
		t.Fatal("same-project open did not focus project Workspaces")
	}
	if plugins[workspacePluginID].keyInputs != 0 {
		t.Fatal("same-project open sent keys to the plugin")
	}
}

// An item that disappeared between the render and the keypress must say so and
// change nothing — no neighbour, no switch, no reinit.
func TestGlobalWorkspacesRefusesADisappearedItem(t *testing.T) {
	m, source, plugins := globalNavigationModel(t)
	gone := filepath.Join(t.TempDir(), "removed")
	inits := totalNavigationInits(plugins)

	updated, cmd := openFromGlobalWorkspaces(t, m, overviewWorkspace(gone))
	if !updated.inGlobalScope() || updated.ui.WorkDir != source || totalNavigationInits(plugins) != inits {
		t.Fatalf("a disappeared item mutated state: global=%v work=%q inits=%d",
			updated.inGlobalScope(), updated.ui.WorkDir, totalNavigationInits(plugins))
	}
	if plugins[workspacePluginID].pending != nil {
		t.Fatalf("a disappeared item still set a pending selection: %#v", plugins[workspacePluginID].pending)
	}
	if cmd == nil {
		t.Fatal("a disappeared item produced no feedback")
	}
	toast, ok := cmd().(ToastMsg)
	if !ok || !toast.IsError || !strings.Contains(toast.Message, "stale") {
		t.Fatalf("disappeared-item feedback = %#v", cmd())
	}
}

// Opening the main worktree of a project whose last visit was a linked worktree
// must land on the item that was chosen, not on the remembered neighbour.
func TestGlobalWorkspacesOpensTheChosenWorktreeNotTheRememberedOne(t *testing.T) {
	m, _, plugins := globalNavigationModel(t)
	target := newOverviewGitRepo(t, "target")
	if out, err := exec.Command("git", "-C", target, "commit", "-q", "--allow-empty", "-m", "root").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	linked := filepath.Join(t.TempDir(), "topic")
	if out, err := exec.Command("git", "-C", target, "worktree", "add", "-q", "-b", "topic", linked).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	normalizedMain, _ := normalizePath(target)
	normalizedLinked, _ := normalizePath(linked)
	if err := state.SetLastWorktreePath(normalizedMain, normalizedLinked); err != nil {
		t.Fatal(err)
	}

	main := overviewWorkspace(target)
	main.Plain = true
	updated, _ := openFromGlobalWorkspaces(t, m, main)
	if got, _ := normalizePath(updated.ui.WorkDir); got != normalizedMain {
		t.Fatalf("work dir = %q, want the chosen main worktree %q", updated.ui.WorkDir, normalizedMain)
	}
	if pending := plugins[workspacePluginID].pending; pending == nil || pending.Path != target {
		t.Fatalf("pending selection = %#v, want the chosen worktree", pending)
	}

	// The linked worktree itself is reachable too, and it is a different
	// destination from its main repo even though both are one project. Under
	// the configured worktree scope the landing follows the card exactly.
	m2, _, plugins2 := globalNavigationModel(t)
	m2.cfg.Plugins.Workspace.OverviewWorktreeScope = config.OverviewWorktreeScopeWorktree
	linkedWorkspace := workspaceinventory.Workspace{
		ProjectKey:  workspaceinventory.CanonicalPath(target),
		ProjectRoot: target,
		Kind:        workspaceinventory.KindWorktree,
		Key:         workspaceinventory.CanonicalPath(linked),
		Path:        linked,
		Plain:       true,
	}
	updated2, _ := openFromGlobalWorkspaces(t, m2, linkedWorkspace)
	if got, _ := normalizePath(updated2.ui.WorkDir); got != normalizedLinked {
		t.Fatalf("linked-worktree open landed on %q, want %q", updated2.ui.WorkDir, normalizedLinked)
	}
	if pending := plugins2[workspacePluginID].pending; pending == nil || pending.Path != linked {
		t.Fatalf("linked-worktree pending selection = %#v", pending)
	}
}

// A shell names a project, not a worktree — it is stored under the project root
// and resolves the same from anywhere in the repo. Opening one in another
// project must therefore keep that project's remembered worktree, both for the
// landing and for the memory itself, so a later `@` switch still returns there.
func TestGlobalWorkspacesOpensAShellInTheRememberedWorktree(t *testing.T) {
	m, _, plugins := globalNavigationModel(t)
	target := newOverviewGitRepo(t, "target")
	if out, err := exec.Command("git", "-C", target, "commit", "-q", "--allow-empty", "-m", "root").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	linked := filepath.Join(t.TempDir(), "topic")
	if out, err := exec.Command("git", "-C", target, "worktree", "add", "-q", "-b", "topic", linked).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	normalizedMain, _ := normalizePath(target)
	normalizedLinked, _ := normalizePath(linked)
	if err := state.SetLastWorktreePath(normalizedMain, normalizedLinked); err != nil {
		t.Fatal(err)
	}
	writeShellManifest(t, target, "sc-shell-2")

	workspace := workspaceinventory.Workspace{
		ProjectKey:  workspaceinventory.CanonicalPath(target),
		ProjectRoot: target,
		Kind:        workspaceinventory.KindShell,
		Key:         "sc-shell-2",
		TmuxName:    "sc-shell-2",
		Path:        target,
	}
	updated, _ := openFromGlobalWorkspaces(t, m, workspace)

	if got, _ := normalizePath(updated.ui.WorkDir); got != normalizedLinked {
		t.Fatalf("work dir = %q, want the remembered worktree %q", updated.ui.WorkDir, normalizedLinked)
	}
	if remembered := state.GetLastWorktreePath(normalizedMain); remembered != normalizedLinked {
		t.Fatalf("remembered worktree = %q, want it untouched at %q", remembered, normalizedLinked)
	}
	pending := plugins[workspacePluginID].pending
	if pending == nil || pending.Kind != plugin.WorkspaceSelectionShell || pending.Key != "sc-shell-2" {
		t.Fatalf("pending shell selection = %#v", pending)
	}
}

// Leaving the catalog before the validation lands drops it: an activation must
// not open a project under a user who has already moved on.
func TestGlobalWorkspacesActivationDroppedAfterLeavingTheCatalog(t *testing.T) {
	exits := []struct {
		name string
		exit func(t *testing.T, m Model) Model
	}{
		{"back to the project", func(t *testing.T, m Model) Model {
			updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			return asAppModel(t, updated)
		}},
		{"on to another global tab", func(t *testing.T, m Model) Model {
			m.globalTasks = &globalTasksHost{plugin: &hostedTestPlugin{id: "tasks"}, ctx: &plugin.Context{Keymap: m.keymap}}
			cmd := m.setGlobalTab(GlobalTasks)
			if cmd != nil {
				cmd()
			}
			return m
		}},
	}
	for _, tc := range exits {
		t.Run(tc.name, func(t *testing.T) {
			m, source, plugins := globalNavigationModel(t)
			target := newOverviewGitRepo(t, "target")
			workspace := overviewWorkspace(target)
			workspace.Plain = true
			request := overviewNavigation(t, m.overview, workspace)
			updated, validationCmd := m.Update(request)
			m = asAppModel(t, updated)
			validation := validationCmd().(overview.ValidationMsg)

			left := tc.exit(t, m)
			inits := totalNavigationInits(plugins)
			after, cmd := left.Update(validation)
			result := asAppModel(t, after)
			if cmd != nil || result.ui.WorkDir != source || totalNavigationInits(plugins) != inits {
				t.Fatalf("late validation acted: cmd=%v work=%q inits=%d", cmd != nil, result.ui.WorkDir, totalNavigationInits(plugins))
			}
			if plugins[workspacePluginID].pending != nil {
				t.Fatalf("late validation set a pending selection: %#v", plugins[workspacePluginID].pending)
			}
		})
	}
}

// Item 4: each global tab keeps its own in-memory view state while the user
// toggles spaces. The list's own state is proved in
// internal/overview/navigate_test.go; what matters here is that the app's
// entry/exit path does not reset it.
func TestGlobalTabViewStateSurvivesSpaceToggles(t *testing.T) {
	m, _, _ := globalNavigationModel(t)

	// Filter and sort the browser, then leave and come back the way a user does.
	// s opens the view fly-out; j + enter picks Project and closes it.
	for _, k := range []tea.KeyPressMsg{
		{Code: 's', Text: "s"},
		{Code: 'j', Text: "j"},
		{Code: tea.KeyEnter},
		{Code: '/', Text: "/"},
	} {
		updated, _ := m.Update(k)
		m = asAppModel(t, updated)
	}
	for _, r := range "topic" {
		updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = asAppModel(t, updated)
	}
	// Enter leaves the query in place and hands navigation back to the list,
	// which is what makes K mean "leave the space" again.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = asAppModel(t, updated)
	before := ansi.Strip(m.renderGlobalContent(m.width, 20))
	if !strings.Contains(before, "topic") || !strings.Contains(before, "Project") {
		t.Fatalf("filter/sort state is not on screen before leaving:\n%s", before)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if m.inGlobalScope() {
		t.Fatal("K did not return to the project")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() || m.globalTab != GlobalWorkspaces {
		t.Fatalf("K did not return to the remembered global tab: global=%v tab=%v", m.inGlobalScope(), m.globalTab)
	}
	after := ansi.Strip(m.renderGlobalContent(m.width, 20))
	if !strings.Contains(after, "topic") || !strings.Contains(after, "Project") {
		t.Fatalf("re-entry reset the tab's filter/sort state:\n%s", after)
	}
}

func totalNavigationInits(plugins map[string]*navigationPlugin) int {
	total := 0
	for _, p := range plugins {
		total += p.inits
	}
	return total
}

// stateBase is the isolated Sidecar state root the current navigation model was
// built against, so a manifest written for a test lands where the collector
// looks for it.
var stateBase string

// writeShellManifest gives a project one durable shell so the shell identity
// validates the way a collected one does.
func TestOpenInGitCrossProjectSelectsTargetCheckout(t *testing.T) {
	m, source, _ := globalNavigationModel(t)
	// The cache is the current project's worktrees — the same state a
	// SwitchWorktree call would use, and the thing that used to keep
	// ProjectRoot on A after WorkDir moved to B.
	m.cachedWorktreeInventory = []WorktreeInfo{{Path: source, IsMain: true}}
	target := newOverviewGitRepo(t, "target")

	updated, cmd := m.Update(overview.OpenInGitMsg{Path: target})
	m = asAppModel(t, updated)
	if cmd == nil {
		t.Fatal("OpenInGitMsg produced no command")
	}
	msg := cmd()
	seq := reflect.ValueOf(msg)
	if seq.Kind() != reflect.Slice || seq.Len() != 2 {
		t.Fatalf("OpenInGit sequence = %T len=%d, want two-command Sequence", msg, seq.Len())
	}
	first := seq.Index(0).Interface().(tea.Cmd)()
	switchMsg, ok := first.(openInGitSwitchMsg)
	if !ok {
		t.Fatalf("first sequence message = %T, want openInGitSwitchMsg", first)
	}
	if switchMsg.Path != target {
		t.Fatalf("switch path = %q, want %q", switchMsg.Path, target)
	}
	updated, _ = m.Update(switchMsg)
	m = asAppModel(t, updated)
	if workspaceinventory.CanonicalPath(m.ui.WorkDir) != workspaceinventory.CanonicalPath(target) {
		t.Fatalf("WorkDir = %q, want target %q", m.ui.WorkDir, target)
	}
	if workspaceinventory.CanonicalPath(m.ui.ProjectRoot) == workspaceinventory.CanonicalPath(source) {
		t.Fatalf("ProjectRoot stayed on the source project: %q", m.ui.ProjectRoot)
	}
	if workspaceinventory.CanonicalPath(m.ui.ProjectRoot) != workspaceinventory.CanonicalPath(target) &&
		workspaceinventory.CanonicalPath(m.ui.ProjectRoot) != workspaceinventory.CanonicalPath(GetMainWorktreePath(target)) {
		t.Fatalf("ProjectRoot = %q, want target %q (or its main)", m.ui.ProjectRoot, target)
	}

	second := seq.Index(1).Interface().(tea.Cmd)()
	focus, ok := second.(FocusPluginByIDMsg)
	if !ok {
		t.Fatalf("second sequence message = %T, want FocusPluginByIDMsg", second)
	}
	if focus.PluginID != "git-status" {
		t.Fatalf("focus plugin = %q, want git-status", focus.PluginID)
	}
	updated, _ = m.Update(focus)
	m = asAppModel(t, updated)
	if m.inGlobalScope() {
		t.Fatal("FocusPlugin left the app in global")
	}
}

func TestOpenInGitMissingPathStaysInGlobal(t *testing.T) {
	m, source, plugins := globalNavigationModel(t)
	m.cachedWorktreeInventory = []WorktreeInfo{{Path: source, IsMain: true}}
	gitBefore := plugins["git"].focused
	inits := totalNavigationInits(plugins)

	updated, cmd := m.Update(overview.OpenInGitMsg{Path: filepath.Join(t.TempDir(), "gone")})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() || m.ui.WorkDir != source || m.ui.ProjectRoot != source {
		t.Fatalf("missing path left global or moved project: global=%v work=%q root=%q", m.inGlobalScope(), m.ui.WorkDir, m.ui.ProjectRoot)
	}
	if totalNavigationInits(plugins) != inits {
		t.Fatal("missing path reinitialized plugins")
	}
	if plugins["git"].focused != gitBefore {
		t.Fatal("missing path focused the Git plugin")
	}
	if cmd == nil {
		t.Fatal("missing path produced no toast")
	}
	toast, ok := cmd().(ToastMsg)
	if !ok || !toast.IsError || !strings.Contains(toast.Message, "no longer exists") {
		t.Fatalf("missing path cmd = %#v, want an error toast", toast)
	}
	// Applying the toast must not leave global or focus Git.
	updated, more := m.Update(toast)
	m = asAppModel(t, updated)
	if more != nil {
		t.Fatalf("toast produced a follow-up command: %T", more())
	}
	if !m.inGlobalScope() || plugins["git"].focused != gitBefore {
		t.Fatal("applying the missing-path toast left global or focused Git")
	}
}

func TestFocusPluginByIDMsgLeavesGlobal(t *testing.T) {
	m, _, _ := globalNavigationModel(t)
	if !m.inGlobalScope() {
		t.Fatal("premise: should start in global")
	}
	updated, _ := m.Update(FocusPluginByIDMsg{PluginID: "git"})
	m = asAppModel(t, updated)
	if m.inGlobalScope() {
		t.Fatal("FocusPluginByIDMsg left the app in global")
	}
}

func writeShellManifest(t *testing.T, root, tmuxName string) {
	t.Helper()
	projectState, err := projectdir.ResolveWithBase(stateBase, root)
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"shells":[{"tmuxName":"` + tmuxName + `","displayName":"Shell 2"}]}`
	if err := os.WriteFile(filepath.Join(projectState, "shells.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}
