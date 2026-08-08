package workspace

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
)

// These tests model two whole Sidecar instances sharing one shells.json — the
// exact shape of the td-8d18de incident, where an isolated proof run rewrote the
// live user's manifest and the live user's sidebar lost six running shells.
//
// An instance is a shellStartupHooks set: that is the seam withDefaults()
// already provides, and it lets both "processes" run in one test binary against
// one real file on disk. loadManifest is the real LoadShellManifest for exactly
// that reason — the file, not a fake, is what the instances contend over.
//
// Package isolation (tmux server and Sidecar state root) comes from TestMain in
// tmux_isolation_test.go. Only TestProofRunCannotWriteRealManifest overrides the
// environment further, and it does so to point at a *fake* home.

const (
	namespaceA = "hostA:/tmp/sock-a/default"
	namespaceB = "hostA:/tmp/sock-b/default"
)

// fakeInstance builds the hooks of one Sidecar instance: its own working
// directory, its own tmux server identity, its own set of live sessions, and a
// manifest path it shares with every other instance in the test.
func fakeInstance(t *testing.T, manifestPath, workDir, namespace string, live []string) shellStartupHooks {
	t.Helper()
	return shellStartupHooks{
		resolveProjectDir: func(string) (string, error) {
			return filepath.Dir(manifestPath), nil
		},
		loadManifest: LoadShellManifest,
		discoverSessions: func(string) []string {
			// Discovery is per working directory in the real code; the caller
			// has already picked names this workDir could produce.
			return append([]string(nil), live...)
		},
		getPaneID:         func(name string) string { return "%" + name },
		getWorkspaceState: func(string) state.WorkspaceState { return state.WorkspaceState{} },
		setWorkspaceState: func(string, state.WorkspaceState) error { return nil },
		now:               func() time.Time { return time.Unix(1000, 0) },
		namespace:         func() string { return namespace },
		newWatcher: func(string) (shellManifestWatcher, error) {
			return newStartupTestWatcher(), nil
		},
	}
}

// runStartup performs one instance's startup reconciliation against the shared
// file: load from disk, discover, reconcile, persist.
func runStartup(t *testing.T, hooks shellStartupHooks, manifestPath, workDir string) ([]*ShellSession, map[string]bool) {
	t.Helper()
	manifest, err := hooks.loadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadShellManifest(%s) error = %v", manifestPath, err)
	}
	return reconcileShellStartup(manifest, hooks.discoverSessions(workDir), workDir, workDir, hooks)
}

func manifestNames(t *testing.T, manifestPath string) []string {
	t.Helper()
	manifest, err := LoadShellManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadShellManifest(%s) error = %v", manifestPath, err)
	}
	names := make([]string, 0, len(manifest.Shells))
	for _, definition := range manifest.Shells {
		names = append(names, definition.TmuxName)
	}
	return names
}

func sessionNameSeries(count int, prefix string) []string {
	names := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		names = append(names, prefix+"-"+strconv.Itoa(i))
	}
	return names
}

// TestIsolatedInstanceCannotEvictPeerShells is the write half of the incident:
// instance B starts up, sees none of instance A's sessions on its own tmux
// server, and must still leave every one of them in the shared file.
func TestIsolatedInstanceCannotEvictPeerShells(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "shells.json")

	liveA := sessionNameSeries(3, "sidecar-sh-sidecar")
	workDirA := filepath.Join(root, "sidecar")
	instanceA := fakeInstance(t, manifestPath, workDirA, namespaceA, liveA)

	if shells, _ := runStartup(t, instanceA, manifestPath, workDirA); len(shells) != 3 {
		t.Fatalf("instance A shells = %d, want 3", len(shells))
	}
	if got := manifestNames(t, manifestPath); len(got) != 3 {
		t.Fatalf("manifest after instance A = %v, want 3 entries", got)
	}

	// The proof run: a different working directory, a different tmux server,
	// one session of its own.
	workDirB := filepath.Join(root, "sidecar-agent-status")
	instanceB := fakeInstance(t, manifestPath, workDirB, namespaceB,
		[]string{"sidecar-sh-sidecar-agent-status-1"})
	runStartup(t, instanceB, manifestPath, workDirB)

	reloaded, err := LoadShellManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	if len(reloaded.Shells) != 4 {
		t.Fatalf("manifest after the proof run = %v, want A's 3 plus B's 1", manifestNames(t, manifestPath))
	}
	for _, name := range liveA {
		definition := reloaded.FindShell(name)
		if definition == nil {
			t.Fatalf("proof run evicted live peer shell %s from %s", name, manifestPath)
		}
		if definition.Namespace != namespaceA {
			t.Errorf("peer definition %s namespace = %q, want %q (B restamped A's entry)",
				name, definition.Namespace, namespaceA)
		}
	}
	if reloaded.FindShell("sidecar-sh-sidecar-agent-status-1") == nil {
		t.Error("proof run did not record its own session")
	}
}

// newInstancePlugin builds a real Plugin standing in for one Sidecar process.
func newInstancePlugin(t *testing.T, hooks shellStartupHooks, workDir string) *Plugin {
	t.Helper()
	p := New()
	p.shellStartupHooks = hooks
	if err := p.Init(&plugin.Context{
		WorkDir:     workDir,
		ProjectRoot: workDir,
		Config:      config.Default(),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Epoch:       1,
	}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	return p
}

// installLiveShells puts count running shells into the plugin, as if startup had
// already adopted them.
func installLiveShells(p *Plugin, names []string, paneID func(string) string) {
	p.managedSessions = make(map[string]bool, len(names))
	p.shells = nil
	for i, name := range names {
		definition := ShellDefinition{
			TmuxName:    name,
			DisplayName: "Shell " + strconv.Itoa(i+1),
			Namespace:   namespaceA,
			CreatedAt:   time.Unix(int64(i+1), 0),
		}
		p.shells = append(p.shells, shellSessionFromDefinition(definition, true, paneID))
		p.managedSessions[name] = true
	}
}

// TestForeignManifestCannotRemoveLiveShellsFromMemory is the read half: the
// shared file has already been narrowed by instance B, and instance A's watcher
// delivers that narrower manifest. A's sidebar must not lose sessions it can see
// alive on its own tmux server, and A must heal the file it just read.
func TestForeignManifestCannotRemoveLiveShellsFromMemory(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "shells.json")

	liveA := sessionNameSeries(3, "sidecar-sh-sidecar")
	workDirA := filepath.Join(root, "sidecar")
	hooksA := fakeInstance(t, manifestPath, workDirA, namespaceA, liveA)
	p := newInstancePlugin(t, hooksA, workDirA)
	installLiveShells(p, liveA, hooksA.getPaneID)

	// What instance B left behind: a manifest with only its own shell in it.
	clobbered := &ShellManifest{
		Version: manifestVersion,
		Shells: []ShellDefinition{{
			TmuxName:    "sidecar-sh-sidecar-agent-status-1",
			DisplayName: "Shell 1",
			Namespace:   namespaceB,
			CreatedAt:   time.Unix(500, 0),
		}},
		path: manifestPath,
	}
	if err := clobbered.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	running := make(map[string]bool, len(liveA))
	paneIDs := make(map[string]string, len(liveA))
	for _, name := range liveA {
		running[name] = true
		paneIDs[name] = hooksA.getPaneID(name)
	}
	reloaded, err := LoadShellManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	p.update(shellManifestSyncMsg{
		Scope:     p.currentShellStartupScope(),
		Manifest:  reloaded,
		Running:   running,
		PaneIDs:   paneIDs,
		Namespace: namespaceA,
	})

	if len(p.shells) != 4 {
		t.Fatalf("shells after foreign rewrite = %d, want 4 (B's entry plus A's 3 live)", len(p.shells))
	}
	for _, name := range liveA {
		shell := shellByTmuxName(p.shells, name)
		if shell == nil {
			t.Fatalf("live shell %s was evicted by a foreign manifest", name)
		}
		if shell.IsOrphaned {
			t.Errorf("live shell %s was marked orphaned", name)
		}
		if !p.managedSessions[name] {
			t.Errorf("live shell %s was dropped from managedSessions", name)
		}
	}

	healed := manifestNames(t, manifestPath)
	if len(healed) != 4 {
		t.Fatalf("manifest after sync = %v, want 4 entries (healed by EnsureShells)", healed)
	}
}

// TestProofRunCannotWriteRealManifest is acceptance criterion 1 in executable
// form: with isolation asserted, the real user's manifest is unreachable for
// both reads and writes, and the failure is loud rather than silent.
func TestProofRunCannotWriteRealManifest(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv(config.IsolationEnv, "1")

	realManifest := filepath.Join(fakeHome, ".local", "state", "sidecar", "projects", "sidecar", "shells.json")

	if _, err := LoadShellManifest(realManifest); err == nil {
		t.Fatal("LoadShellManifest read the real user manifest under asserted isolation")
	}

	manifest := &ShellManifest{Version: manifestVersion, path: realManifest}
	if err := manifest.Save(); err == nil {
		t.Fatal("Save wrote the real user manifest under asserted isolation")
	}
	if err := manifest.AddShell(ShellDefinition{TmuxName: "sidecar-sh-sidecar-1"}); err == nil {
		t.Fatal("AddShell wrote the real user manifest under asserted isolation")
	}

	// Not even lock or temp debris may appear in the real tree.
	if entries, err := os.ReadDir(filepath.Dir(realManifest)); err == nil {
		t.Fatalf("isolated run created %s (%d entries)", filepath.Dir(realManifest), len(entries))
	}

	// And the whole startup path refuses rather than degrading quietly.
	workDir := filepath.Join(fakeHome, "code", "sidecar")
	hooks := fakeInstance(t, realManifest, workDir, namespaceA, []string{"sidecar-sh-sidecar-1"})
	p := newInstancePlugin(t, hooks, workDir)
	result, ok := p.loadShellStartup()().(shellStartupResultMsg)
	if !ok {
		t.Fatal("startup command did not return shellStartupResultMsg")
	}
	if result.err == nil {
		t.Fatal("startup against the real user manifest succeeded, want a refusal")
	}
	if _, err := os.Stat(realManifest); !os.IsNotExist(err) {
		t.Fatalf("real user manifest exists after an isolated startup (stat err = %v)", err)
	}
}

// TestRefreshSelfHealsClobberedManifest is acceptance criterion 5 end to end:
// after a foreign rewrite has already narrowed both the file and the sidebar,
// pressing `r` must bring the surviving sessions back with no restart and no
// hand-editing of JSON.
func TestRefreshSelfHealsClobberedManifest(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "shells.json")

	liveA := sessionNameSeries(3, "sidecar-sh-sidecar")
	workDirA := filepath.Join(root, "sidecar")
	hooksA := fakeInstance(t, manifestPath, workDirA, namespaceA, liveA)

	// The degraded state the user was left in: one shell on screen, one entry
	// in the file, three tmux sessions still running.
	clobbered := &ShellManifest{
		Version: manifestVersion,
		Shells: []ShellDefinition{{
			TmuxName:    liveA[0],
			DisplayName: "Shell 1",
			Namespace:   namespaceA,
			CreatedAt:   time.Unix(1, 0),
		}},
		path: manifestPath,
	}
	if err := clobbered.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	p := newInstancePlugin(t, hooksA, workDirA)
	installLiveShells(p, liveA[:1], hooksA.getPaneID)
	loaded, err := LoadShellManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	p.shellManifest = loaded

	_, command := p.update(RefreshMsg{})
	if command == nil {
		t.Fatal("RefreshMsg returned no commands")
	}
	batch, ok := command().(tea.BatchMsg)
	if !ok {
		t.Fatalf("RefreshMsg command returned %T, want tea.BatchMsg", command())
	}
	var sync tea.Msg
	for _, cmd := range batch {
		if cmd == nil {
			continue
		}
		if msg, isSync := cmd().(shellManifestSyncMsg); isSync {
			sync = msg
		}
	}
	if sync == nil {
		t.Fatal("refresh did not schedule tmux rediscovery")
	}
	p.update(sync)

	if len(p.shells) != 3 {
		t.Fatalf("shells after refresh = %d, want the 3 surviving sessions", len(p.shells))
	}
	for _, name := range liveA {
		shell := shellByTmuxName(p.shells, name)
		if shell == nil {
			t.Fatalf("refresh did not rediscover live session %s", name)
		}
		if shell.IsOrphaned {
			t.Errorf("rediscovered shell %s is marked orphaned", name)
		}
	}
	healed := manifestNames(t, manifestPath)
	if len(healed) != 3 {
		t.Fatalf("manifest after refresh = %v, want all 3 surviving sessions", healed)
	}
}
