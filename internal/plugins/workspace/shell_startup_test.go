package workspace

import (
	"io"
	"log/slog"
	"path/filepath"
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
		discoverSessions: func(string) []string {
			counts.discover.Add(1)
			return []string{definition.TmuxName}
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
				CreatedAt:   time.Unix(10, 0),
			},
			{
				TmuxName:    "sidecar-sh-project-9",
				DisplayName: "Dead shell",
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
		now: func() time.Time { return time.Unix(30, 0) },
	}

	shells, managed := reconcileShellStartup(
		manifest,
		[]string{"sidecar-sh-project-1", "sidecar-sh-project-2"},
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
