package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestLocalWorktreeSwitcherWithNoHosts(t *testing.T) {
	m := switcherTestModel(t, "/tmp/sidecar", []config.ProjectConfig{
		{Name: "Sidecar", Path: "/tmp/sidecar"},
	})
	m.cachedWorktreeInventory = []WorktreeInfo{
		{Path: "/tmp/sidecar", Branch: "main", IsMain: true},
		{Path: "/tmp/sidecar-feature", Branch: "feature"},
	}
	m.initWorktreeSwitcher()
	if len(m.worktreeSwitcherAll) != 2 {
		t.Fatalf("rows = %+v, want the captured local inventory", m.worktreeSwitcherAll)
	}
	for i, row := range m.worktreeSwitcherAll {
		if row.isRemote() {
			t.Fatalf("row %d is remote: %+v", i, row)
		}
		if row.Local.Path != m.cachedWorktreeInventory[i].Path || row.Local.Branch != m.cachedWorktreeInventory[i].Branch {
			t.Fatalf("row %d = %+v, want captured inventory", i, row.Local)
		}
	}
}

func TestWorktreeSwitcherSameNamedHostContributesLinkedWorktrees(t *testing.T) {
	m := switcherTestModel(t, "/tmp/sidecar", []config.ProjectConfig{
		{Name: "Sidecar", Path: "/tmp/sidecar"},
	})
	m.cachedWorktreeInventory = []WorktreeInfo{
		{Path: "/tmp/sidecar", Branch: "main", IsMain: true},
		{Path: "/tmp/sidecar-feature", Branch: "feature"},
	}
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateOnline},
		Projects: []overview.HostCatalogProject{
			{
				Key:  "/home/me/sidecar",
				Name: "Sidecar",
				Root: "/home/me/sidecar",
				Workspaces: []workspaceinventory.Workspace{
					{Kind: workspaceinventory.KindWorktree, Key: "/home/me/sidecar", Name: "main", IsMain: true},
					{Kind: workspaceinventory.KindWorktree, Key: "/home/me/sidecar-feature", Name: "feature", Branch: "feature"},
					{Kind: workspaceinventory.KindShell, Key: "s1", Name: "Claude pane"},
				},
			},
			{
				Key:  "/home/me/td",
				Name: "td",
				Root: "/home/me/td",
				Workspaces: []workspaceinventory.Workspace{
					{Kind: workspaceinventory.KindWorktree, Key: "/home/me/td-other", Name: "other", Branch: "other"},
				},
			},
		},
	}}
	m.initWorktreeSwitcher()

	var remotes []worktreeSwitcherRow
	for _, row := range m.worktreeSwitcherAll {
		if row.isRemote() {
			remotes = append(remotes, row)
		}
	}
	if len(m.worktreeSwitcherAll) != 3 {
		t.Fatalf("rows = %+v, want 2 local + 1 remote linked worktree", m.worktreeSwitcherAll)
	}
	if len(remotes) != 1 {
		t.Fatalf("remote rows = %+v, want only [aerie] Sidecar [[feature]]", remotes)
	}
	got := remotes[0]
	if got.Destination.WorktreeKey != "/home/me/sidecar-feature" {
		t.Errorf("WorktreeKey = %q", got.Destination.WorktreeKey)
	}
	if label := FormatDestination(got.Destination); label != "[aerie] Sidecar [[feature]]" {
		t.Errorf("label = %q", label)
	}
	if got.DisabledReason != "" {
		t.Errorf("online row disabled: %q", got.DisabledReason)
	}
}

func TestWorktreeSwitcherPairsByTrimmedCaseInsensitiveNameNotProjectKey(t *testing.T) {
	m := switcherTestModel(t, "/tmp/sidecar", []config.ProjectConfig{
		{Name: "Sidecar", Path: "/tmp/sidecar"},
	})
	m.cachedWorktreeInventory = []WorktreeInfo{
		{Path: "/tmp/sidecar", Branch: "main", IsMain: true},
		{Path: "/tmp/sidecar-wt", Branch: "wt"},
	}
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateOnline},
		Projects: []overview.HostCatalogProject{
			{
				// Same path-shaped key as the local checkout: that is the twin-path
				// coincidence, not a match. Different registered name.
				Key:  "/tmp/sidecar",
				Name: "td",
				Root: "/tmp/sidecar",
				Workspaces: []workspaceinventory.Workspace{
					{Kind: workspaceinventory.KindWorktree, Key: "/tmp/sidecar-twin", Name: "twin", Branch: "twin"},
				},
			},
			{
				Key:  "/home/me/other",
				Name: "  sidecar  ",
				Root: "/home/me/other",
				Workspaces: []workspaceinventory.Workspace{
					{Kind: workspaceinventory.KindWorktree, Key: "/home/me/other-feature", Name: "feature", Branch: "feature"},
				},
			},
		},
	}}
	m.initWorktreeSwitcher()
	var remotes []worktreeSwitcherRow
	for _, row := range m.worktreeSwitcherAll {
		if row.isRemote() {
			remotes = append(remotes, row)
		}
	}
	if len(remotes) != 1 {
		t.Fatalf("remote rows = %+v, want the name-matched project only", remotes)
	}
	if remotes[0].Destination.ProjectKey != "/home/me/other" {
		t.Errorf("paired ProjectKey = %q, pairing used ProjectKey equality", remotes[0].Destination.ProjectKey)
	}
	if remotes[0].Destination.WorktreeKey != "/home/me/other-feature" {
		t.Errorf("WorktreeKey = %q", remotes[0].Destination.WorktreeKey)
	}
}

func TestWorktreeSwitcherBoundRemotelyOmitsLocalRowsWithoutCache(t *testing.T) {
	original := listWorktreesForSwitcher
	defer func() { listWorktreesForSwitcher = original }()
	calls := 0
	listWorktreesForSwitcher = func(string) []WorktreeInfo {
		calls++
		return []WorktreeInfo{{Path: "/tmp/sidecar", Branch: "main", IsMain: true}}
	}

	m := switcherTestModel(t, "/tmp/sidecar", []config.ProjectConfig{
		{Name: "Sidecar", Path: "/tmp/sidecar"},
	})
	m.boundDestination = Destination{HostID: "aerie", ProjectName: "Sidecar", ProjectKey: "/home/me/sidecar"}
	m.ui.WorkDir = ""
	m.cachedWorktreeInventory = nil
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateOnline},
		Projects: []overview.HostCatalogProject{{
			Key:  "/home/me/sidecar",
			Name: "Sidecar",
			Root: "/home/me/sidecar",
			Workspaces: []workspaceinventory.Workspace{
				{Kind: workspaceinventory.KindWorktree, Key: "/home/me/sidecar-feature", Branch: "feature"},
			},
		}},
	}}
	m.initWorktreeSwitcher()
	if calls != 0 {
		t.Fatalf("git worktree list calls = %d, want 0 while bound remotely", calls)
	}
	if len(m.worktreeSwitcherAll) != 1 || !m.worktreeSwitcherAll[0].isRemote() {
		t.Fatalf("rows = %+v, want only the remote linked worktree", m.worktreeSwitcherAll)
	}
}

func TestWorktreeSwitcherBoundRemotelyListsCachedLocalSameNamedProject(t *testing.T) {
	m := switcherTestModel(t, "/tmp/sidecar", []config.ProjectConfig{
		{Name: "Sidecar", Path: "/tmp/sidecar"},
	})
	m.setWorktreeInventory([]WorktreeInfo{
		{Path: "/tmp/sidecar", Branch: "main", IsMain: true},
		{Path: "/tmp/sidecar-feature", Branch: "feature"},
	}, "/tmp/sidecar")
	m.boundDestination = Destination{HostID: "aerie", ProjectName: "Sidecar", ProjectKey: "/home/me/sidecar"}
	m.ui.WorkDir = ""
	m.cachedWorktreeInventory = nil
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateOnline},
		Projects: []overview.HostCatalogProject{{
			Key:  "/home/me/sidecar",
			Name: "Sidecar",
			Root: "/home/me/sidecar",
			Workspaces: []workspaceinventory.Workspace{
				{Kind: workspaceinventory.KindWorktree, Key: "/home/me/sidecar-wt", Branch: "remote-feature"},
			},
		}},
	}}
	m.initWorktreeSwitcher()
	if len(m.worktreeSwitcherAll) != 3 {
		t.Fatalf("rows = %+v, want 2 cached local + 1 remote", m.worktreeSwitcherAll)
	}
	if m.worktreeSwitcherAll[0].isRemote() || m.worktreeSwitcherAll[0].Local.Path != "/tmp/sidecar" {
		t.Fatalf("first row = %+v, want unprefixed local main", m.worktreeSwitcherAll[0])
	}
	if !m.worktreeSwitcherAll[2].isRemote() {
		t.Fatalf("last row = %+v, want remote", m.worktreeSwitcherAll[2])
	}
}

func TestWorktreeSwitcherConnectingCatalogDoesNotYankLocalCursor(t *testing.T) {
	m := switcherTestModel(t, "/tmp/sidecar", []config.ProjectConfig{
		{Name: "Sidecar", Path: "/tmp/sidecar"},
	})
	m.cachedWorktreeInventory = []WorktreeInfo{
		{Path: "/tmp/sidecar", Branch: "main", IsMain: true},
		{Path: "/tmp/sidecar-feature", Branch: "feature"},
	}
	m.initWorktreeSwitcher()
	m.showWorktreeSwitcher = true
	m.worktreeSwitcherCursor = 1
	highlighted := m.worktreeSwitcherFiltered[1]

	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateOnline},
		Projects: []overview.HostCatalogProject{{
			Key:  "/home/me/sidecar",
			Name: "Sidecar",
			Root: "/home/me/sidecar",
			Workspaces: []workspaceinventory.Workspace{
				{Kind: workspaceinventory.KindWorktree, Key: "/home/me/sidecar-feature", Branch: "feature"},
			},
		}},
	}}
	m.refreshOpenWorktreeSwitcher()
	if m.worktreeSwitcherCursor >= len(m.worktreeSwitcherFiltered) {
		t.Fatal("cursor out of range after remotes inserted")
	}
	got := m.worktreeSwitcherFiltered[m.worktreeSwitcherCursor]
	if got.identityKey() != highlighted.identityKey() {
		t.Fatalf("cursor moved from %+v to %+v", highlighted, got)
	}
	var sawRemote bool
	for _, row := range m.worktreeSwitcherFiltered {
		if row.isRemote() {
			sawRemote = true
		}
	}
	if !sawRemote {
		t.Fatal("expected remote rows after catalog arrived")
	}
}

func TestWorktreeSwitcherDisabledUnreachableRowDoesNotBind(t *testing.T) {
	workDir := t.TempDir()
	m := switcherTestModel(t, workDir, []config.ProjectConfig{
		{Name: "Sidecar", Path: workDir},
	})
	m.cachedWorktreeInventory = []WorktreeInfo{
		{Path: workDir, Branch: "main", IsMain: true},
		{Path: workDir + "-feature", Branch: "feature"},
	}
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateUnreachable, Detail: "ssh failed"},
		Projects: []overview.HostCatalogProject{{
			Key:  "/home/me/sidecar",
			Name: "Sidecar",
			Root: "/home/me/sidecar",
			Workspaces: []workspaceinventory.Workspace{
				{Kind: workspaceinventory.KindWorktree, Key: "/home/me/sidecar-feature", Branch: "feature"},
			},
		}},
	}}
	m.initWorktreeSwitcher()
	var remote worktreeSwitcherRow
	for _, row := range m.worktreeSwitcherAll {
		if row.isRemote() {
			remote = row
		}
	}
	if remote.DisabledReason == "" {
		t.Fatalf("unreachable row was not disabled: %+v", remote)
	}
	if !strings.Contains(remote.DisabledReason, string(hosts.StateUnreachable)) {
		t.Errorf("DisabledReason = %q, want the Sessions health sentence", remote.DisabledReason)
	}

	cmd := m.activateWorktreeSwitcherRow(remote)
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

func TestWorktreeSwitcherRemoteBindDoesNotReinitTwinLocalPath(t *testing.T) {
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
	cfg.Projects.List = []config.ProjectConfig{{Name: "Sidecar", Path: local}}

	recorder := &recordingInitPlugin{id: "workspace-manager"}
	ctx := &plugin.Context{WorkDir: local, ProjectRoot: local}
	reg := plugin.NewRegistry(ctx)
	if err := reg.Register(recorder); err != nil {
		t.Fatal(err)
	}
	m := New(reg, keymap.NewRegistry(), cfg, "", local, local, "")

	dest := Destination{
		HostID:       "aerie",
		ProjectKey:   "/home/me/sidecar",
		ProjectName:  "Sidecar",
		WorktreeKey:  local,
		WorktreeName: "feature",
		Root:         local,
	}
	_ = m.activateWorktreeSwitcherRow(worktreeSwitcherRow{Destination: dest})

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
	if loc, ok := state.GetLastBoundLocation(); !ok || loc.WorktreeKey != local {
		t.Fatalf("persisted location = %+v ok=%v", loc, ok)
	}
	if got := state.GetLastRemoteWorktree("aerie", "/home/me/sidecar"); got != local {
		t.Fatalf("GetLastRemoteWorktree() = %q, want the host-side key", got)
	}
	if got := state.GetLastWorktreePath("/home/me/sidecar"); got != "" {
		t.Errorf("GetLastWorktreePath saw remote key %q", got)
	}
}

func TestProjectSwitcherEnterRestoresLastRemoteWorktree(t *testing.T) {
	m := switcherTestModel(t, "/tmp/sidecar", []config.ProjectConfig{
		{Name: "Sidecar", Path: "/tmp/sidecar"},
	})
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateOnline},
		Projects: []overview.HostCatalogProject{{
			Key:  "/home/me/sidecar",
			Name: "Sidecar",
			Root: "/home/me/sidecar",
			Workspaces: []workspaceinventory.Workspace{
				{Kind: workspaceinventory.KindWorktree, Key: "/home/me/sidecar-feature", Branch: "feature"},
			},
		}},
	}}
	if err := state.SetLastRemoteWorktree("aerie", "/home/me/sidecar", "/home/me/sidecar-feature"); err != nil {
		t.Fatal(err)
	}
	if got := state.GetLastWorktreePath("/home/me/sidecar"); got != "" {
		t.Fatalf("GetLastWorktreePath leaked %q before bind", got)
	}

	dest := Destination{
		HostID:      "aerie",
		ProjectKey:  "/home/me/sidecar",
		ProjectName: "Sidecar",
		Root:        "/home/me/sidecar",
	}
	_ = m.activateProjectSwitcherDestination(projectSwitcherDestination{
		Kind:        destinationProject,
		Name:        FormatDestination(dest),
		Destination: dest,
	})
	if m.boundDestination.WorktreeKey != "/home/me/sidecar-feature" {
		t.Fatalf("bound WorktreeKey = %q, want restored last worktree", m.boundDestination.WorktreeKey)
	}
	if m.boundDestination.WorktreeName != "feature" {
		t.Errorf("WorktreeName = %q, want feature from catalog", m.boundDestination.WorktreeName)
	}
	if m.ui.WorkDir != "" {
		t.Fatalf("WorkDir = %q, restore must not use a local path", m.ui.WorkDir)
	}
}

func TestWorktreeSwitcherFilterMatchesHostProjectWorktreeAndPath(t *testing.T) {
	m := switcherTestModel(t, "/tmp/sidecar", []config.ProjectConfig{
		{Name: "Sidecar", Path: "/tmp/sidecar"},
	})
	m.cachedWorktreeInventory = []WorktreeInfo{
		{Path: "/tmp/sidecar", Branch: "main", IsMain: true},
		{Path: "/tmp/sidecar-billing", Branch: "billing"},
	}
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID:     "aerie",
		Health: hosts.Health{State: hosts.StateOnline},
		Projects: []overview.HostCatalogProject{{
			Key:  "/home/me/sidecar",
			Name: "Sidecar",
			Root: "/home/me/sidecar",
			Workspaces: []workspaceinventory.Workspace{
				{Kind: workspaceinventory.KindWorktree, Key: "/home/me/sidecar-feature", Branch: "feature"},
			},
		}},
	}}
	m.initWorktreeSwitcher()

	if got := filterWorktreeRows(m.worktreeSwitcherAll, "aer"); len(got) != 1 || !got[0].isRemote() {
		t.Fatalf("host filter = %+v", got)
	}
	if got := filterWorktreeRows(m.worktreeSwitcherAll, "feature"); len(got) != 1 || !got[0].isRemote() {
		t.Fatalf("worktree name filter = %+v", got)
	}
	if got := filterWorktreeRows(m.worktreeSwitcherAll, "billing"); len(got) != 1 || got[0].isRemote() {
		t.Fatalf("local branch filter = %+v", got)
	}
	if got := filterWorktreeRows(m.worktreeSwitcherAll, "/home/me/sidecar-feature"); len(got) != 1 || !got[0].isRemote() {
		t.Fatalf("path filter = %+v", got)
	}
}

func TestWorktreeSwitcherLocalMainAfterRemoteBindDoesNotRestoreLastWorktree(t *testing.T) {
	main := newOverviewGitRepo(t, "main")
	if out, err := exec.Command("git", "-C", main, "commit", "-q", "--allow-empty", "-m", "root").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	feature := filepath.Join(t.TempDir(), "feature")
	if out, err := exec.Command("git", "-C", main, "worktree", "add", "-q", "-b", "feature", feature).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	normalizedMain, _ := normalizePath(main)
	normalizedFeature, _ := normalizePath(feature)

	m := switcherTestModel(t, main, []config.ProjectConfig{
		{Name: "Sidecar", Path: main},
	})
	if err := state.SetLastWorktreePath(normalizedMain, normalizedFeature); err != nil {
		t.Fatal(err)
	}
	m.boundDestination = Destination{HostID: "aerie", ProjectName: "Sidecar", ProjectKey: "/home/me/sidecar"}
	m.ui.WorkDir = ""
	m.cachedWorktreeInventory = nil

	_ = m.activateWorktreeSwitcherRow(worktreeSwitcherRow{
		Local: WorktreeInfo{Path: main, Branch: "main", IsMain: true},
	})
	if got, _ := normalizePath(m.ui.WorkDir); got != normalizedMain {
		t.Fatalf("WorkDir = %q, want the chosen main %q", m.ui.WorkDir, normalizedMain)
	}
	if m.boundDestination.HostID != "" {
		t.Fatalf("HostID = %q, want cleared", m.boundDestination.HostID)
	}

	m2 := switcherTestModel(t, main, []config.ProjectConfig{
		{Name: "Sidecar", Path: main},
	})
	if err := state.SetLastWorktreePath(normalizedMain, normalizedFeature); err != nil {
		t.Fatal(err)
	}
	m2.boundDestination = Destination{HostID: "aerie", ProjectName: "Sidecar", ProjectKey: "/home/me/sidecar"}
	m2.ui.WorkDir = ""
	m2.cachedWorktreeInventory = nil
	_ = m2.activateWorktreeSwitcherRow(worktreeSwitcherRow{
		Local: WorktreeInfo{Path: feature, Branch: "feature"},
	})
	if got, _ := normalizePath(m2.ui.WorkDir); got != normalizedFeature {
		t.Fatalf("feature WorkDir = %q, want %q", m2.ui.WorkDir, normalizedFeature)
	}
	if m2.boundDestination.HostID != "" {
		t.Fatalf("feature HostID = %q, want cleared", m2.boundDestination.HostID)
	}
}
