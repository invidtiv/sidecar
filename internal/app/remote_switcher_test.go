package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

type recordingInitPlugin struct {
	id      string
	workDir string
	hostID  string
	ctx     *plugin.Context
}

func (p *recordingInitPlugin) ID() string   { return p.id }
func (p *recordingInitPlugin) Name() string { return p.id }
func (p *recordingInitPlugin) Icon() string { return "" }
func (p *recordingInitPlugin) Init(ctx *plugin.Context) error {
	p.ctx = ctx
	p.workDir = ctx.WorkDir
	p.hostID = ctx.HostID
	return nil
}
func (p *recordingInitPlugin) Start() tea.Cmd {
	if p.ctx != nil && p.ctx.HostWorkspaces != nil {
		_ = p.ctx.HostWorkspaces()
	}
	return nil
}
func (p *recordingInitPlugin) Stop() {}
func (p *recordingInitPlugin) Update(tea.Msg) (plugin.Plugin, tea.Cmd) {
	return p, nil
}
func (p *recordingInitPlugin) View(int, int) string       { return "" }
func (p *recordingInitPlugin) IsFocused() bool            { return false }
func (p *recordingInitPlugin) SetFocused(bool)            {}
func (p *recordingInitPlugin) Commands() []plugin.Command { return nil }
func (p *recordingInitPlugin) FocusContext() string       { return "" }

func switcherTestModel(t *testing.T, workDir string, projects []config.ProjectConfig) Model {
	t.Helper()
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = projects
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return New(plugin.NewRegistry(&plugin.Context{WorkDir: workDir, ProjectRoot: workDir}), keymap.NewRegistry(), cfg, "", workDir, workDir, "")
}

func TestRemoteSwitcherRowsAppendFromCatalog(t *testing.T) {
	m := switcherTestModel(t, "/tmp/one", []config.ProjectConfig{
		{Name: "one", Path: "/tmp/one"},
	})
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateOnline},
		Projects: []overview.HostCatalogProject{{
			Key:  "/home/me/sidecar",
			Name: "Sidecar",
			Root: "/home/me/sidecar",
		}},
	}}
	destinations := m.projectSwitcherDestinations("")
	if len(destinations) < 3 {
		t.Fatalf("destinations = %#v, want Overview, local, remote", destinations)
	}
	remote := destinations[len(destinations)-1]
	if !remote.isRemote() || remote.Name != "[aerie] Sidecar" {
		t.Fatalf("remote row = %#v, want FormatDestination label", remote)
	}
	if remote.Name != FormatDestination(remote.Destination) {
		t.Errorf("row name %q != FormatDestination", remote.Name)
	}
	if remote.Path != "" {
		t.Errorf("remote Path = %q, want unused", remote.Path)
	}
}

func TestRemoteSwitcherFilterMatchesHostNameAndPath(t *testing.T) {
	m := switcherTestModel(t, "/tmp/one", []config.ProjectConfig{
		{Name: "one", Path: "/tmp/one"},
	})
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateOnline},
		Projects: []overview.HostCatalogProject{{
			Key:  "/home/me/sidecar",
			Name: "Sidecar",
			Root: "/home/me/sidecar",
		}},
	}}
	for _, q := range []string{"aer", "side", "/HOME/ME"} {
		got := m.projectSwitcherDestinations(q)
		var remote *projectSwitcherDestination
		for i := range got {
			if got[i].isRemote() {
				if remote != nil {
					t.Fatalf("query %q matched extra remotes: %#v", q, got)
				}
				remote = &got[i]
			}
			if got[i].Kind == destinationProject && !got[i].isRemote() {
				t.Fatalf("query %q matched local %q", q, got[i].Name)
			}
		}
		if remote == nil {
			t.Fatalf("query %q = %#v, want the remote row", q, got)
		}
	}
}

func TestConnectingCatalogInsertsRowsWithoutMovingLocalCursor(t *testing.T) {
	m := switcherTestModel(t, "/tmp/one", []config.ProjectConfig{
		{Name: "one", Path: "/tmp/one"},
		{Name: "two", Path: "/tmp/two"},
	})
	m.initProjectSwitcher()
	m.showProjectSwitcher = true
	if !m.isCurrentSwitcherDestination(m.projectSwitcherFiltered[m.projectSwitcherCursor]) {
		t.Fatalf("cursor = %d (%#v), want the local current project", m.projectSwitcherCursor, m.projectSwitcherFiltered)
	}
	current := m.projectSwitcherFiltered[m.projectSwitcherCursor]
	if current.isRemote() || current.Path != "/tmp/one" {
		t.Fatalf("highlighted = %#v, want local one", current)
	}

	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateOnline},
		Projects: []overview.HostCatalogProject{{
			Key:  "/home/me/sidecar",
			Name: "Sidecar",
			Root: "/home/me/sidecar",
		}},
	}}
	m.refreshOpenProjectSwitcher()

	if m.projectSwitcherCursor >= len(m.projectSwitcherFiltered) {
		t.Fatal("cursor out of range after remotes inserted")
	}
	got := m.projectSwitcherFiltered[m.projectSwitcherCursor]
	if got.identityKey() != current.identityKey() {
		t.Fatalf("cursor moved from %#v to %#v", current, got)
	}
	var sawRemote bool
	for _, d := range m.projectSwitcherFiltered {
		if d.isRemote() {
			sawRemote = true
		}
	}
	if !sawRemote {
		t.Fatal("expected remote rows after catalog arrived")
	}
}

func TestDisabledUnreachableRowDoesNotBind(t *testing.T) {
	workDir := t.TempDir()
	m := switcherTestModel(t, workDir, []config.ProjectConfig{
		{Name: "one", Path: workDir},
	})
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateUnreachable, Detail: "ssh failed"},
		Projects: []overview.HostCatalogProject{{
			Key:  "/home/me/sidecar",
			Name: "Sidecar",
			Root: "/home/me/sidecar",
		}},
	}}
	destinations := m.projectSwitcherDestinations("")
	var remote projectSwitcherDestination
	for _, d := range destinations {
		if d.isRemote() {
			remote = d
		}
	}
	if remote.DisabledReason == "" {
		t.Fatalf("unreachable row was not disabled: %#v", remote)
	}
	if !strings.Contains(remote.DisabledReason, string(hosts.StateUnreachable)) {
		t.Errorf("DisabledReason = %q, want the Sessions health sentence", remote.DisabledReason)
	}

	cmd := m.activateProjectSwitcherDestination(remote)
	if cmd == nil {
		t.Fatal("disabled enter should toast")
	}
	got := cmd()
	toast, ok := got.(msg.ToastMsg)
	if !ok || !strings.Contains(toast.Message, remote.DisabledReason) {
		t.Fatalf("toast = %#v, want disabled reason", got)
	}
	if m.boundDestination.HostID != "" {
		t.Fatalf("bound destination = %+v, Enter must not bind", m.boundDestination)
	}
	if m.ui.WorkDir != workDir {
		t.Fatalf("WorkDir = %q, want unchanged %q", m.ui.WorkDir, workDir)
	}
}

func TestRemoteBindDoesNotReinitTwinLocalPath(t *testing.T) {
	local := t.TempDir()
	if err := os.WriteFile(filepath.Join(local, "marker.txt"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = []config.ProjectConfig{{Name: "local", Path: local}}

	recorder := &recordingInitPlugin{id: "workspace-manager"}
	ctx := &plugin.Context{WorkDir: local, ProjectRoot: local}
	reg := plugin.NewRegistry(ctx)
	if err := reg.Register(recorder); err != nil {
		t.Fatal(err)
	}
	m := New(reg, keymap.NewRegistry(), cfg, "", local, local, "")

	dest := Destination{
		HostID:      "aerie",
		ProjectKey:  "/home/me/sidecar",
		ProjectName: "Sidecar",
		Root:        local,
	}
	_ = m.bindRemoteDestination(dest)

	if m.ui.WorkDir == local || m.ui.ProjectRoot == local {
		t.Fatalf("twin path became WorkDir/ProjectRoot: %q %q", m.ui.WorkDir, m.ui.ProjectRoot)
	}
	if ctx.WorkDir == local || ctx.ProjectRoot == local {
		t.Fatalf("plugin context twin path WorkDir/ProjectRoot = %q %q", ctx.WorkDir, ctx.ProjectRoot)
	}
	if ctx.HostID != "aerie" {
		t.Fatalf("HostID = %q, want aerie", ctx.HostID)
	}
	if recorder.workDir == local {
		t.Fatal("plugin opened the twin local tree")
	}
	if recorder.hostID != "aerie" {
		t.Fatalf("plugin HostID = %q", recorder.hostID)
	}
	if loc, ok := state.GetLastBoundLocation(); !ok || loc.HostID != "aerie" || loc.ProjectKey != dest.ProjectKey {
		t.Fatalf("persisted location = %+v ok=%v", loc, ok)
	}
	if m.intro.RepoName != FormatDestination(dest) {
		t.Errorf("intro.RepoName = %q, want FormatDestination", m.intro.RepoName)
	}
	if m.activeDestinationName() != BoundDestinationNavbarLabel(dest) {
		t.Errorf("navbar = %q", m.activeDestinationName())
	}
}

func TestRemoteBindDoesNotDeadlockOnHostInventory(t *testing.T) {
	// Workspace Start reads HostWorkspaces during ReinitHost. That callback
	// must not take the registry lock or Enter on a remote row freezes the TUI.
	local := t.TempDir()
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = []config.ProjectConfig{{Name: "local", Path: local}}

	recorder := &recordingInitPlugin{id: "workspace-manager"}
	reg := plugin.NewRegistry(&plugin.Context{WorkDir: local, ProjectRoot: local})
	if err := reg.Register(recorder); err != nil {
		t.Fatal(err)
	}
	m := New(reg, keymap.NewRegistry(), cfg, "", local, local, "")
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID: "aerie",
		Projects: []overview.HostCatalogProject{{
			Key:  "/home/me/sidecar",
			Name: "Sidecar",
			Workspaces: []workspaceinventory.Workspace{{
				Kind: workspaceinventory.KindShell, Name: "Claude pane", TmuxName: "sidecar-claude",
			}},
		}},
	}}

	done := make(chan struct{})
	go func() {
		_ = m.bindRemoteDestination(Destination{
			HostID:      "aerie",
			ProjectKey:  "/home/me/sidecar",
			ProjectName: "Sidecar",
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bindRemoteDestination deadlocked: HostWorkspaces re-entered the registry lock")
	}
}

func TestRemoteRowThemePreviewDoesNotResolveRemotePath(t *testing.T) {
	t.Cleanup(func() { styles.ApplyTheme("sidecar-modern") })
	styles.ApplyTheme("sidecar-modern")

	m := switcherTestModel(t, "/tmp/local", []config.ProjectConfig{
		{Name: "local", Path: "/tmp/local"},
		{Name: "twin", Path: "/home/me/sidecar", Theme: &config.ThemeConfig{Name: "dracula"}},
	})
	m.cfg.UI.Theme.Name = "sidecar-modern"
	m.projectSwitcherFiltered = []projectSwitcherDestination{{
		Kind: destinationProject,
		Name: "[aerie] Sidecar",
		Destination: Destination{
			HostID:      "aerie",
			ProjectName: "Sidecar",
			Root:        "/home/me/sidecar",
		},
	}}
	m.projectSwitcherCursor = 0
	_ = m.previewProjectTheme()
	if got := styles.GetCurrentTheme().Name; got == "dracula" {
		t.Fatalf("theme = %q; remote Root was passed to ResolveTheme", got)
	}
}

func TestLocalSwitcherRowStillResolvesPerPathTheme(t *testing.T) {
	t.Cleanup(func() { styles.ApplyTheme("sidecar-modern") })
	styles.ApplyTheme("sidecar-modern")

	m := switcherTestModel(t, "/tmp/local", []config.ProjectConfig{
		{Name: "twin", Path: "/home/me/sidecar", Theme: &config.ThemeConfig{Name: "dracula"}},
	})
	m.cfg.UI.Theme.Name = "sidecar-modern"
	m.projectSwitcherFiltered = []projectSwitcherDestination{{
		Kind: destinationProject,
		Name: "twin",
		Path: "/home/me/sidecar",
	}}
	m.projectSwitcherCursor = 0
	_ = m.previewProjectTheme()
	if got := styles.GetCurrentTheme().Name; got != "dracula" {
		t.Fatalf("local row theme = %q, want dracula from the per-path config", got)
	}
}

func TestRemoteBindToastUsesFormatDestination(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m := switcherTestModel(t, "/tmp/one", []config.ProjectConfig{{Name: "one", Path: "/tmp/one"}})
	dest := Destination{HostID: "aerie", ProjectName: "Sidecar", ProjectKey: "/home/me/sidecar"}
	cmd := m.bindRemoteDestination(dest)
	if cmd == nil {
		t.Fatal("bind returned nil")
	}
	if m.intro.RepoName != FormatDestination(dest) {
		t.Errorf("toast/label source = %q, want %q", m.intro.RepoName, FormatDestination(dest))
	}
}

type relayAckPlugin struct {
	recordingInitPlugin
}

func (p *relayAckPlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	reqMsg, ok := msg.(uirequest.RequestMsg)
	if !ok {
		return p, nil
	}
	req := reqMsg.Request
	if req.Origin.HostID == "" {
		return p, nil
	}
	_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
		Instance: "workspace-test",
		Status:   uirequest.StatusDeclined,
		Reason:   "test-plugin-bound",
		At:       time.Now().UTC(),
	})
	return p, nil
}

func TestHostCatalogWorkspacesReachBoundWorkspacePlugin(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	ctx := &plugin.Context{}
	reg := plugin.NewRegistry(ctx)
	if err := reg.Register(&recordingInitPlugin{id: workspacePluginID}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", workspacePluginID)
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID: "aerie",
		Projects: []overview.HostCatalogProject{{
			Key: "/home/me/sidecar",
			Workspaces: []workspaceinventory.Workspace{{
				Kind: workspaceinventory.KindShell, Name: "Claude pane", TmuxName: "sidecar-claude", Live: true,
			}},
		}},
	}}
	_ = m.bindRemoteDestination(Destination{HostID: "aerie", ProjectKey: "/home/me/sidecar", ProjectName: "Sidecar"})
	got := m.registry.Context().HostWorkspaces()
	if len(got) != 1 || got[0].Name != "Claude pane" {
		t.Fatalf("HostWorkspaces after bind without re-install = %+v", got)
	}
}

// A bound plugin composes its host workspace id from the project key and the
// bound worktree, so the bind has to carry the worktree into the context.
// Without it every worktree of a host project browses the main checkout.
func TestBindCarriesTheWorktreeKeyIntoThePluginContext(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	ctx := &plugin.Context{}
	reg := plugin.NewRegistry(ctx)
	if err := reg.Register(&recordingInitPlugin{id: workspacePluginID}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", workspacePluginID)
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:       "aerie",
		Projects: []overview.HostCatalogProject{{Key: "/home/me/sidecar"}},
	}}

	_ = m.bindRemoteDestination(Destination{
		HostID:       "aerie",
		ProjectKey:   "/home/me/sidecar",
		ProjectName:  "Sidecar",
		WorktreeKey:  "/home/me/sidecar-feature",
		WorktreeName: "feature",
	})
	if got := m.registry.Context().HostWorktreeKey; got != "/home/me/sidecar-feature" {
		t.Fatalf("HostWorktreeKey = %q, want the bound worktree", got)
	}

	// Binding back to the main checkout must clear it, or the previous
	// worktree keeps answering for the project root.
	_ = m.bindRemoteDestination(Destination{HostID: "aerie", ProjectKey: "/home/me/sidecar", ProjectName: "Sidecar"})
	if got := m.registry.Context().HostWorktreeKey; got != "" {
		t.Fatalf("HostWorktreeKey = %q after binding the main checkout", got)
	}
}

func TestRelayedOpenAfterBindAcksNotSilence(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	config.SetTestStateDir(filepath.Join(stateHome, "sidecar"))
	t.Cleanup(config.ResetTestStateDir)
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	ackPlugin := &relayAckPlugin{recordingInitPlugin: recordingInitPlugin{id: workspacePluginID}}
	reg := plugin.NewRegistry(&plugin.Context{})
	if err := reg.Register(ackPlugin); err != nil {
		t.Fatal(err)
	}
	m := New(reg, keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", workspacePluginID)
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID: "aerie",
		Projects: []overview.HostCatalogProject{{
			Key: "/home/me/sidecar",
			Workspaces: []workspaceinventory.Workspace{{
				Kind: workspaceinventory.KindShell, Name: "Claude pane", TmuxName: "sidecar-claude", Live: true,
			}},
		}},
	}}
	_ = m.bindRemoteDestination(Destination{HostID: "aerie", ProjectKey: "/home/me/sidecar", ProjectName: "Sidecar"})
	if got := m.registry.Context().HostWorkspaces(); len(got) != 1 || got[0].TmuxName != "sidecar-claude" {
		t.Fatalf("HostWorkspaces after bind = %+v", got)
	}

	req := uirequest.Request{
		ID: "req-after-bind", Action: uirequest.ActionOpen, CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin: uirequest.Origin{HostID: "aerie", TmuxSession: "sidecar-claude", ProjectKey: "/home/me/sidecar"},
		Target: uirequest.Target{Kind: uirequest.TargetKindFile, Value: "twin.txt"},
	}
	_, _ = m.Update(uirequest.RequestMsg{Request: req})
	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil || len(acks) == 0 {
		t.Fatalf("silence after bind: acks=%+v err=%v", acks, err)
	}
}

func TestBindRemoteDestinationClaimsLiveSessionLeases(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	reg := plugin.NewRegistry(&plugin.Context{})
	m := New(reg, keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", "")
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID: "aerie",
		Projects: []overview.HostCatalogProject{{
			Key: "/home/me/sidecar",
			Workspaces: []workspaceinventory.Workspace{
				{Kind: workspaceinventory.KindShell, Name: "live", TmuxName: "sidecar-claude", Live: true},
				{Kind: workspaceinventory.KindShell, Name: "dead", TmuxName: "sidecar-old", Live: false},
			},
		}},
	}}
	original := claimGeometryLease
	t.Cleanup(func() { claimGeometryLease = original })
	var claimed []string
	claimGeometryLease = func(target string) { claimed = append(claimed, target) }

	_ = m.bindRemoteDestination(Destination{HostID: "aerie", ProjectKey: "/home/me/sidecar", ProjectName: "Sidecar"})
	if len(claimed) != 1 || claimed[0] != "sidecar-claude" {
		t.Fatalf("claimed = %v, want [sidecar-claude]", claimed)
	}
}

func TestBindRemoteDestinationAnnouncesHostIDWithoutRemotePath(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m := switcherTestModel(t, "/tmp/one", []config.ProjectConfig{{Name: "one", Path: "/tmp/one"}})
	dest := Destination{HostID: "aerie", ProjectName: "Sidecar", ProjectKey: "/home/me/sidecar", Root: "/home/me/sidecar"}
	cmd := m.bindRemoteDestination(dest)
	if cmd == nil {
		t.Fatal("bind returned nil")
	}
	if m.ui.WorkDir != "" || m.ui.ProjectRoot != "" {
		t.Fatalf("WorkDir/ProjectRoot = %q %q", m.ui.WorkDir, m.ui.ProjectRoot)
	}
	if msg := m.announcePresenceCmd()(); msg != nil {
		t.Fatalf("announce returned %v", msg)
	}
	live, err := uirequest.ListInstances(config.StateDir())
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(live) != 1 || live[0].HostID != "aerie" {
		t.Fatalf("instance = %+v", live)
	}
	if live[0].WorkDir != "" || live[0].ProjectKey != "" {
		t.Fatalf("advertised remote path as local identity: %+v", live[0])
	}
}
