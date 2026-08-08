package workspace

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
)

type startupHookCounts struct {
	resolve  atomic.Int32
	load     atomic.Int32
	discover atomic.Int32
	pane     atomic.Int32
	watcher  atomic.Int32
}

type startupTestWatcher struct {
	started atomic.Int32
	stopped atomic.Int32
	msgs    chan tea.Msg
}

func newStartupTestWatcher() *startupTestWatcher {
	return &startupTestWatcher{msgs: make(chan tea.Msg, 1)}
}

func (w *startupTestWatcher) Start() <-chan tea.Msg {
	w.started.Add(1)
	return w.msgs
}

func (w *startupTestWatcher) Stop() {
	w.stopped.Add(1)
}

func startupTestHooks(
	counts *startupHookCounts,
	watcher *startupTestWatcher,
	manifestPath string,
) shellStartupHooks {
	definition := ShellDefinition{
		TmuxName:    "sidecar-sh-project-1",
		DisplayName: "Shell 1",
		CreatedAt:   time.Unix(100, 0),
	}
	return shellStartupHooks{
		resolveProjectDir: func(string) (string, error) {
			counts.resolve.Add(1)
			return filepath.Dir(manifestPath), nil
		},
		loadManifest: func(path string) (*ShellManifest, error) {
			counts.load.Add(1)
			return &ShellManifest{
				Version: manifestVersion,
				Shells:  []ShellDefinition{definition},
				path:    path,
			}, nil
		},
		discoverSessions: func(string) ([]string, error) {
			counts.discover.Add(1)
			return []string{definition.TmuxName}, nil
		},
		getPaneID: func(string) string {
			counts.pane.Add(1)
			return "%42"
		},
		newWatcher: func(string) (shellManifestWatcher, error) {
			counts.watcher.Add(1)
			return watcher, nil
		},
		getWorkspaceState: func(string) state.WorkspaceState {
			return state.WorkspaceState{}
		},
		setWorkspaceState: func(string, state.WorkspaceState) error {
			return nil
		},
		now: func() time.Time {
			return time.Unix(100, 0)
		},
	}
}

func newShellStartupTestPlugin(
	t *testing.T,
	epoch uint64,
	hooks shellStartupHooks,
) *Plugin {
	t.Helper()
	cfg := config.Default()
	workDir := filepath.Join(t.TempDir(), "project")
	p := New()
	p.shellStartupHooks = hooks
	err := p.Init(&plugin.Context{
		WorkDir:     workDir,
		ProjectRoot: workDir,
		Config:      cfg,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Epoch:       epoch,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	return p
}

func TestShellStartup_InitAndStartDoNotRunIOHooks(t *testing.T) {
	counts := &startupHookCounts{}
	watcher := newStartupTestWatcher()
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	p := newShellStartupTestPlugin(t, 7, startupTestHooks(counts, watcher, manifestPath))

	if command := p.Start(); command == nil {
		t.Fatal("Start() returned nil")
	}

	if got := counts.resolve.Load(); got != 0 {
		t.Errorf("resolve hook calls after Init/Start = %d, want 0", got)
	}
	if got := counts.load.Load(); got != 0 {
		t.Errorf("load hook calls after Init/Start = %d, want 0", got)
	}
	if got := counts.discover.Load(); got != 0 {
		t.Errorf("tmux discovery hook calls after Init/Start = %d, want 0", got)
	}
	if got := counts.pane.Load(); got != 0 {
		t.Errorf("pane lookup hook calls after Init/Start = %d, want 0", got)
	}
	if got := counts.watcher.Load(); got != 0 {
		t.Errorf("watcher hook calls after Init/Start = %d, want 0", got)
	}
	if got := watcher.started.Load(); got != 0 {
		t.Errorf("watcher Start calls before Update = %d, want 0", got)
	}
}

func TestShellStartup_StaleProjectResultIsRejectedAndWatcherStopped(t *testing.T) {
	counts := &startupHookCounts{}
	watcher := newStartupTestWatcher()
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	hooks := startupTestHooks(counts, watcher, manifestPath)
	p := newShellStartupTestPlugin(t, 10, hooks)

	result, ok := p.loadShellStartup()().(shellStartupResultMsg)
	if !ok {
		t.Fatal("startup command did not return shellStartupResultMsg")
	}

	nextRoot := filepath.Join(t.TempDir(), "next-project")
	if err := p.Init(&plugin.Context{
		WorkDir:     nextRoot,
		ProjectRoot: nextRoot,
		Config:      config.Default(),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Epoch:       11,
	}); err != nil {
		t.Fatalf("second Init() error = %v", err)
	}

	_, cleanup := p.update(result)
	if len(p.shells) != 0 {
		t.Fatalf("stale startup installed %d shells, want 0", len(p.shells))
	}
	if p.shellManifest != nil {
		t.Fatal("stale startup installed its manifest")
	}
	if p.shellWatcher != nil {
		t.Fatal("stale startup installed its watcher")
	}
	if cleanup == nil {
		t.Fatal("stale watcher did not receive a cleanup command")
	}
	cleanup()
	if got := watcher.stopped.Load(); got != 1 {
		t.Errorf("watcher Stop calls = %d, want 1", got)
	}
	if got := watcher.started.Load(); got != 0 {
		t.Errorf("stale watcher Start calls = %d, want 0", got)
	}
}

func TestShellStartup_UpdateAssignsWatcherAndStartsShellPolling(t *testing.T) {
	counts := &startupHookCounts{}
	watcher := newStartupTestWatcher()
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	p := newShellStartupTestPlugin(t, 21, startupTestHooks(counts, watcher, manifestPath))

	result, ok := p.loadShellStartup()().(shellStartupResultMsg)
	if !ok {
		t.Fatal("startup command did not return shellStartupResultMsg")
	}
	if p.shellManifest != nil || p.shellWatcher != nil || len(p.shells) != 0 {
		t.Fatal("startup command mutated plugin state before Update")
	}
	if got := watcher.started.Load(); got != 0 {
		t.Fatalf("watcher Start calls before Update = %d, want 0", got)
	}

	shellName := result.shells[0].TmuxName
	beforeGeneration := p.pollScheduler.Current(shellPollKey(shellName))
	_, command := p.update(result)

	if p.shellManifest != result.manifest {
		t.Fatal("Update did not assign the startup manifest")
	}
	if p.shellWatcher != watcher {
		t.Fatal("Update did not assign the startup watcher")
	}
	if p.shellWatcherMessages != watcher.msgs {
		t.Fatal("Update did not retain the watcher message stream")
	}
	if got := watcher.started.Load(); got != 1 {
		t.Errorf("watcher Start calls after Update = %d, want 1", got)
	}
	if len(p.shells) != 1 || p.shells[0].Agent == nil {
		t.Fatalf("Update installed shells = %#v, want one running shell", p.shells)
	}
	if after := p.pollScheduler.Current(shellPollKey(shellName)); after <= beforeGeneration {
		t.Errorf("shell poll generation after Update = %d, want > %d", after, beforeGeneration)
	}
	if command == nil {
		t.Fatal("Update did not schedule watcher listening and shell polling")
	}
}

func TestReconcileShellStartup_PreservesManifestAndMigrationSemantics(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	manifest := &ShellManifest{
		Version: manifestVersion,
		Shells: []ShellDefinition{
			{
				TmuxName:    "sidecar-sh-project-1",
				DisplayName: "Old name",
				Namespace:   testNamespace,
				CreatedAt:   time.Unix(10, 0),
			},
			{
				// Same tmux server, and a name this workDir's discovery could
				// have produced: absence really does mean death (td-8d18de).
				TmuxName:    "sidecar-sh-project-9",
				DisplayName: "Dead shell",
				Namespace:   testNamespace,
				CreatedAt:   time.Unix(20, 0),
			},
		},
		path: manifestPath,
	}
	legacyCleared := false
	hooks := shellStartupHooks{
		getPaneID: func(name string) string { return "%" + name },
		getWorkspaceState: func(string) state.WorkspaceState {
			return state.WorkspaceState{
				ShellDisplayNames: map[string]string{
					"sidecar-sh-project-1": "Migrated name",
				},
			}
		},
		setWorkspaceState: func(_ string, workspaceState state.WorkspaceState) error {
			legacyCleared = workspaceState.ShellDisplayNames == nil
			return nil
		},
		now:       func() time.Time { return time.Unix(30, 0) },
		namespace: func() string { return testNamespace },
	}

	shells, managed := reconcileShellStartup(
		manifest,
		[]string{"sidecar-sh-project-1", "sidecar-sh-project-2"},
		false,
		"/repo/project",
		"/repo/project",
		hooks,
	)

	if len(shells) != 2 {
		t.Fatalf("reconciled shells = %d, want 2", len(shells))
	}
	if shells[0].TmuxName != "sidecar-sh-project-1" || shells[0].Name != "Migrated name" {
		t.Errorf("manifest shell = %#v, want migrated running shell", shells[0])
	}
	if shells[1].TmuxName != "sidecar-sh-project-2" || shells[1].Name != "Shell 2" {
		t.Errorf("discovered shell = %#v, want upgraded Shell 2", shells[1])
	}
	if managed["sidecar-sh-project-9"] {
		t.Error("dead manifest shell remained managed")
	}
	if !managed["sidecar-sh-project-1"] || !managed["sidecar-sh-project-2"] {
		t.Errorf("managed sessions = %#v, want both running shells", managed)
	}
	if !legacyCleared {
		t.Error("legacy display-name state was not cleared after migration")
	}

	reloaded, err := LoadShellManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	if len(reloaded.Shells) != 2 {
		t.Fatalf("persisted manifest shells = %d, want 2", len(reloaded.Shells))
	}
	if reloaded.FindShell("sidecar-sh-project-9") != nil {
		t.Error("dead shell remained in persisted manifest")
	}
	if definition := reloaded.FindShell("sidecar-sh-project-1"); definition == nil || definition.DisplayName != "Migrated name" {
		t.Errorf("persisted migrated definition = %#v", definition)
	}
}

// testNamespace stands in for tmuxenv.Namespace() so reconciliation tests can
// state which tmux server an entry belongs to without touching the environment.
const testNamespace = "/tmp/tmux-test/default"

func reconcileTestHooks(namespace string) shellStartupHooks {
	return shellStartupHooks{
		getPaneID:         func(name string) string { return "%" + name },
		getWorkspaceState: func(string) state.WorkspaceState { return state.WorkspaceState{} },
		setWorkspaceState: func(string, state.WorkspaceState) error { return nil },
		now:               func() time.Time { return time.Unix(30, 0) },
		namespace:         func() string { return namespace },
	}
}

func definitionByTmuxName(definitions []ShellDefinition, tmuxName string) *ShellDefinition {
	for i := range definitions {
		if definitions[i].TmuxName == tmuxName {
			return &definitions[i]
		}
	}
	return nil
}

func shellByTmuxName(shells []*ShellSession, tmuxName string) *ShellSession {
	for _, shell := range shells {
		if shell.TmuxName == tmuxName {
			return shell
		}
	}
	return nil
}

// TestReconcileKeepsForeignNamespaceEntries covers the isolated-proof-run case:
// another Sidecar on a different tmux socket owns those sessions, so this
// instance not seeing them says nothing about whether they are alive.
func TestReconcileKeepsForeignNamespaceEntries(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	manifest := &ShellManifest{
		Version: manifestVersion,
		Shells: []ShellDefinition{
			{TmuxName: "sidecar-sh-project-1", DisplayName: "Foreign shell", Namespace: "/tmp/tmux-999/default", CreatedAt: time.Unix(10, 0)},
			{TmuxName: "sidecar-sh-project-9", DisplayName: "Foreign shell 9", Namespace: "/tmp/tmux-999/default", CreatedAt: time.Unix(11, 0)},
		},
		path: manifestPath,
	}

	shells, managed := reconcileShellStartup(manifest, nil, false, "/repo/project", "/repo/project", reconcileTestHooks(testNamespace))

	if len(shells) != 2 {
		t.Fatalf("reconciled shells = %d, want 2 survivors", len(shells))
	}
	for _, shell := range shells {
		if !shell.IsOrphaned {
			t.Errorf("shell %s should be orphaned, not live", shell.TmuxName)
		}
		if managed[shell.TmuxName] {
			t.Errorf("shell %s should not be managed by this instance", shell.TmuxName)
		}
	}

	// Nothing changed, so reconciliation must not have rewritten the shared
	// file at all — the manifest still holds both foreign definitions.
	if len(manifest.Shells) != 2 {
		t.Fatalf("manifest shells = %d, want 2 (nothing pruned)", len(manifest.Shells))
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Errorf("manifest was rewritten despite no changes (stat err = %v)", err)
	}
}

// TestReconcileClaimsLegacyEntriesMatchingOurPattern is the migration half of
// the namespace rule: an entry written before td-8d18de has no namespace, but a
// name only this working directory's discovery could produce can only have come
// from this machine's default server, so it stays prunable rather than becoming
// a permanent offline row.
func TestReconcileClaimsLegacyEntriesMatchingOurPattern(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	manifest := &ShellManifest{
		Version: manifestVersion,
		Shells: []ShellDefinition{
			{TmuxName: "sidecar-sh-project-1", DisplayName: "Legacy dead shell", CreatedAt: time.Unix(10, 0)},
			{TmuxName: "sidecar-sh-other-1", DisplayName: "Legacy sibling shell", CreatedAt: time.Unix(11, 0)},
		},
		path: manifestPath,
	}

	shells, _ := reconcileShellStartup(manifest, nil, false, "/repo/project", "/repo/project", reconcileTestHooks(testNamespace))

	if len(shells) != 0 {
		t.Fatalf("reconciled shells = %d, want 0 displayed", len(shells))
	}
	if len(manifest.Shells) != 1 || manifest.Shells[0].TmuxName != "sidecar-sh-other-1" {
		t.Fatalf("manifest shells = %#v, want only the sibling worktree's entry", manifest.Shells)
	}
}

// TestReconcileNeverPrunesWhenDiscoveryFails is the "absence is not proof of
// death" rule on the discovery axis: if we could not ask tmux, every entry
// survives and the file is not rewritten.
func TestReconcileNeverPrunesWhenDiscoveryFails(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	manifest := &ShellManifest{
		Version: manifestVersion,
		Shells: []ShellDefinition{
			{TmuxName: "sidecar-sh-project-1", DisplayName: "Shell 1", Namespace: testNamespace, CreatedAt: time.Unix(10, 0)},
			{TmuxName: "sidecar-sh-project-2", DisplayName: "Shell 2", Namespace: testNamespace, CreatedAt: time.Unix(11, 0)},
		},
		path: manifestPath,
	}

	shells, managed := reconcileShellStartup(manifest, nil, true, "/repo/project", "/repo/project", reconcileTestHooks(testNamespace))

	if len(shells) != 2 {
		t.Fatalf("reconciled shells = %d, want both kept after a discovery failure", len(shells))
	}
	if len(managed) != 0 {
		t.Errorf("managed = %#v, want none (nothing is known to be running)", managed)
	}
	if len(manifest.Shells) != 2 {
		t.Fatalf("manifest shells = %d, want 2", len(manifest.Shells))
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Errorf("manifest was rewritten after a failed discovery (stat err = %v)", err)
	}
}

// TestReconcileKeepsEntriesOutsideDiscoveryPattern is the reported scenario:
// a linked worktree starts up, its discovery prefix differs, and the six live
// shells of the main checkout must not be pruned out of the shared manifest.
func TestReconcileKeepsEntriesOutsideDiscoveryPattern(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	definitions := make([]ShellDefinition, 0, 6)
	for i := 1; i <= 6; i++ {
		definitions = append(definitions, ShellDefinition{
			TmuxName:    "sidecar-sh-sidecar-" + strconv.Itoa(i),
			DisplayName: "Shell " + strconv.Itoa(i),
			Namespace:   testNamespace,
			CreatedAt:   time.Unix(int64(i), 0),
		})
	}
	manifest := &ShellManifest{Version: manifestVersion, Shells: definitions, path: manifestPath}

	shells, managed := reconcileShellStartup(
		manifest,
		[]string{"sidecar-sh-sidecar-agent-status-1"},
		false,
		"/tmp/x/sidecar-agent-status",
		"/tmp/x/sidecar-agent-status",
		reconcileTestHooks(testNamespace),
	)

	// Survival is in the manifest, not on this instance's screen: the six shells
	// belong to the main checkout and are alive there, so showing them here as
	// offline rows whose only action (recreate) would collide with a live tmux
	// session would trade data loss for permanent unusable noise.
	if len(shells) != 1 || shells[0].TmuxName != "sidecar-sh-sidecar-agent-status-1" {
		t.Fatalf("reconciled shells = %#v, want only this worktree's own session", shells)
	}
	for i := 1; i <= 6; i++ {
		name := "sidecar-sh-sidecar-" + strconv.Itoa(i)
		if shellByTmuxName(shells, name) != nil {
			t.Errorf("sibling worktree shell %s should not be displayed here", name)
		}
		if definitionByTmuxName(manifest.Shells, name) == nil {
			t.Fatalf("live shell %s was pruned by a worktree instance", name)
		}
	}
	if !managed["sidecar-sh-sidecar-agent-status-1"] {
		t.Error("discovered session was not managed")
	}

	reloaded, err := LoadShellManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	if len(reloaded.Shells) != 7 {
		t.Fatalf("persisted manifest shells = %d, want 7", len(reloaded.Shells))
	}
}

func TestReconcilePrunesOwnDeadSession(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	manifest := &ShellManifest{
		Version: manifestVersion,
		Shells: []ShellDefinition{
			{TmuxName: "sidecar-sh-project-1", DisplayName: "Shell 1", Namespace: testNamespace, CreatedAt: time.Unix(10, 0)},
			{TmuxName: "sidecar-sh-project-2", DisplayName: "Shell 2", Namespace: testNamespace, CreatedAt: time.Unix(11, 0)},
		},
		path: manifestPath,
	}

	shells, _ := reconcileShellStartup(
		manifest,
		[]string{"sidecar-sh-project-1"},
		false,
		"/repo/project",
		"/repo/project",
		reconcileTestHooks(testNamespace),
	)

	if len(shells) != 1 || shells[0].TmuxName != "sidecar-sh-project-1" {
		t.Fatalf("reconciled shells = %v, want only the live session", shells)
	}
	reloaded, err := LoadShellManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	if reloaded.FindShell("sidecar-sh-project-2") != nil {
		t.Error("our own dead session was not pruned")
	}
}

func TestReconcileStampsNamespaceOnLiveEntries(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	manifest := &ShellManifest{
		Version: manifestVersion,
		Shells: []ShellDefinition{
			{TmuxName: "sidecar-sh-project-1", DisplayName: "Shell 1", CreatedAt: time.Unix(10, 0)},
		},
		path: manifestPath,
	}

	reconcileShellStartup(
		manifest,
		[]string{"sidecar-sh-project-1", "sidecar-sh-project-2"},
		false,
		"/repo/project",
		"/repo/project",
		reconcileTestHooks(testNamespace),
	)

	reloaded, err := LoadShellManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	for _, name := range []string{"sidecar-sh-project-1", "sidecar-sh-project-2"} {
		definition := reloaded.FindShell(name)
		if definition == nil {
			t.Fatalf("definition %s missing from manifest", name)
		}
		if definition.Namespace != testNamespace {
			t.Errorf("definition %s namespace = %q, want %q", name, definition.Namespace, testNamespace)
		}
	}
}

// TestRefreshMsgTriggersShellRediscovery pins td-8d18de item 5: `r` must rerun
// tmux discovery so shells a foreign manifest rewrite hid come back without
// restarting the app.
func TestRefreshMsgTriggersShellRediscovery(t *testing.T) {
	counts := &startupHookCounts{}
	watcher := newStartupTestWatcher()
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	hooks := startupTestHooks(counts, watcher, manifestPath)
	hooks.namespace = func() string { return testNamespace }
	p := newShellStartupTestPlugin(t, 3, hooks)
	p.shellManifest = &ShellManifest{Version: manifestVersion, path: manifestPath}

	_, command := p.update(RefreshMsg{})
	if command == nil {
		t.Fatal("RefreshMsg returned no commands")
	}

	batch, ok := command().(tea.BatchMsg)
	if !ok {
		t.Fatalf("RefreshMsg command returned %T, want tea.BatchMsg", command())
	}
	found := false
	for _, cmd := range batch {
		if cmd == nil {
			continue
		}
		if sync, ok := cmd().(shellManifestSyncMsg); ok {
			found = true
			if sync.Namespace != testNamespace {
				t.Errorf("sync namespace = %q, want %q", sync.Namespace, testNamespace)
			}
			if !sync.Running["sidecar-sh-project-1"] {
				t.Errorf("sync did not rediscover the live session: %#v", sync.Running)
			}
		}
	}
	if !found {
		t.Fatal("RefreshMsg did not schedule tmux rediscovery (no shellManifestSyncMsg)")
	}
	if got := counts.discover.Load(); got == 0 {
		t.Error("refresh never ran tmux discovery")
	}
}
