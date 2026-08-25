package workspace

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
)

type liveCountWrite struct {
	path            string
	before, after   int
	identityRemoval bool
}

func wrapLiveCountWrites(t *testing.T) *[]liveCountWrite {
	t.Helper()
	var writes []liveCountWrite
	prev := shellstate.ObserveLiveCountWrite
	t.Cleanup(func() { shellstate.ObserveLiveCountWrite = prev })
	shellstate.ObserveLiveCountWrite = func(path string, before, after int, identityRemoval bool) {
		if prev != nil {
			prev(path, before, after, identityRemoval)
		}
		writes = append(writes, liveCountWrite{path, before, after, identityRemoval})
		if after < before && !identityRemoval {
			t.Errorf("manifest write at %s shrank live shells from %d to %d without an identity removal", path, before, after)
		}
	}
	return &writes
}

func seedManifest(t *testing.T, path string, defs ...ShellDefinition) *ShellManifest {
	t.Helper()
	m, err := LoadShellManifest(path)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	for _, def := range defs {
		if err := m.AddShell(def); err != nil {
			t.Fatalf("AddShell(%s) = %v", def.TmuxName, err)
		}
	}
	return m
}

func durabilityDefs(ns, workDir string) []ShellDefinition {
	return []ShellDefinition{
		{
			TmuxName:    "sidecar-sh-durability-1",
			DisplayName: "Research agent",
			Namespace:   ns,
			CreatedAt:   time.Unix(10, 0).UTC(),
			AgentType:   string(AgentClaude),
			SkipPerms:   true,
			WorkDir:     workDir,
		},
		{
			TmuxName:    "sidecar-sh-durability-2",
			DisplayName: "Test runner",
			Namespace:   ns,
			CreatedAt:   time.Unix(11, 0).UTC(),
			AgentType:   string(AgentCodex),
			SkipPerms:   false,
			WorkDir:     workDir,
		},
		{
			TmuxName:    "sidecar-sh-durability-3",
			DisplayName: "Notes",
			Namespace:   ns,
			CreatedAt:   time.Unix(12, 0).UTC(),
			WorkDir:     workDir,
		},
	}
}

// TestManifestWritesNeverShrinkLiveCountExceptIdentityRemoval wraps the
// shells.json writer boundary and fails if a write reduces live entries except
// through RemoveShell, RemoveAtPath, or RemoveIfUnchangedAtPath. Startup
// reconcile and EnsureShells must not shrink.
func TestManifestWritesNeverShrinkLiveCountExceptIdentityRemoval(t *testing.T) {
	writes := wrapLiveCountWrites(t)
	path := filepath.Join(t.TempDir(), "shells.json")
	workDir := "/repo/durability"
	defs := durabilityDefs(testNamespace, workDir)
	m := seedManifest(t, path, defs...)

	if _, err := m.EnsureShells(defs[:2]); err != nil {
		t.Fatalf("EnsureShells(subset) = %v", err)
	}
	if _, err := m.EnsureShells([]ShellDefinition{{
		TmuxName:    "sidecar-sh-durability-4",
		DisplayName: "Extra",
		Namespace:   testNamespace,
		WorkDir:     workDir,
	}}); err != nil {
		t.Fatalf("EnsureShells(add) = %v", err)
	}
	extra := *m.FindShell("sidecar-sh-durability-1")
	extra.DisplayName = "Research agent (renamed)"
	if err := m.UpdateShell(extra); err != nil {
		t.Fatalf("UpdateShell() = %v", err)
	}
	if err := m.BackfillWorkDirs(map[string]string{"sidecar-sh-durability-4": workDir}); err != nil {
		t.Fatalf("BackfillWorkDirs() = %v", err)
	}

	loaded, err := LoadShellManifest(path)
	if err != nil {
		t.Fatalf("LoadShellManifest() = %v", err)
	}
	reconcileShellStartup(loaded, nil, workDir, workDir, reconcileTestHooks(testNamespace))
	reconcileShellStartup(loaded, []string{"sidecar-sh-other-99"}, workDir, workDir, reconcileTestHooks(testNamespace))

	hooks := shellStartupHooks{
		resolveProjectDir: func(string) (string, error) { return filepath.Dir(path), nil },
		loadManifest:      LoadShellManifest,
		discoverSessions: func(string) ([]string, tmuxserver.Incarnation, error) {
			return []string{"sidecar-sh-restarted-1"}, tmuxserver.Present(9, 10, 11), nil
		},
		getPaneID:         func(name string) string { return "%" + name },
		newWatcher:        func(string) (shellManifestWatcher, error) { return newStartupTestWatcher(), nil },
		getWorkspaceState: func(string) state.WorkspaceState { return state.WorkspaceState{} },
		setWorkspaceState: func(string, state.WorkspaceState) error { return nil },
		now:               func() time.Time { return time.Unix(30, 0) },
		namespace:         func() string { return testNamespace },
	}
	p := newShellStartupTestPlugin(t, 1, hooks)
	if _, ok := p.loadShellStartup()().(shellStartupResultMsg); !ok {
		t.Fatal("loadShellStartup did not return shellStartupResultMsg")
	}

	liveBeforeRemove := len(loadShellManifestMust(t, path).Shells)
	if err := m.RemoveShell("sidecar-sh-durability-3"); err != nil {
		t.Fatalf("RemoveShell() = %v", err)
	}
	if err := shellstate.RemoveAtPath(path, shellstate.Identity{TmuxName: "sidecar-sh-durability-2", Namespace: testNamespace}); err != nil {
		t.Fatalf("RemoveAtPath() = %v", err)
	}
	if err := shellstate.RemoveIfUnchangedAtPath(path, shellstate.Identity{TmuxName: "sidecar-sh-durability-4", Namespace: testNamespace}, time.Time{}); err != nil {
		t.Fatalf("RemoveIfUnchangedAtPath() = %v", err)
	}

	var removals, unauthorized int
	for _, w := range *writes {
		if w.after < w.before {
			if !w.identityRemoval {
				unauthorized++
			} else {
				removals++
			}
		}
	}
	if unauthorized != 0 {
		t.Fatalf("unauthorized live-count shrinks = %d, want 0", unauthorized)
	}
	if removals < 3 {
		t.Fatalf("identity-removal shrinks = %d, want at least 3 (RemoveShell, RemoveAtPath, RemoveIfUnchangedAtPath)", removals)
	}
	got := loadShellManifestMust(t, path)
	if len(got.Shells) >= liveBeforeRemove {
		t.Fatalf("live shells after identity removals = %d, want fewer than %d", len(got.Shells), liveBeforeRemove)
	}

	t.Run("guard_reports_unauthorized_shrink", func(t *testing.T) {
		var flagged []liveCountWrite
		prev := shellstate.ObserveLiveCountWrite
		t.Cleanup(func() { shellstate.ObserveLiveCountWrite = prev })
		shellstate.ObserveLiveCountWrite = func(path string, before, after int, identityRemoval bool) {
			if after < before && !identityRemoval {
				flagged = append(flagged, liveCountWrite{path, before, after, identityRemoval})
			}
		}
		shrinking := seedManifest(t, filepath.Join(t.TempDir(), "shrink.json"), defs[0], defs[1])
		if err := shrinking.mutateLocked(func(shells []ShellDefinition) ([]ShellDefinition, bool) {
			return shells[:1], true
		}); err != nil {
			t.Fatalf("mutateLocked shrink = %v", err)
		}
		if len(flagged) == 0 {
			t.Fatal("writer boundary did not report an unauthorized live-count shrink")
		}
	})
}

func loadShellManifestMust(t *testing.T, path string) *ShellManifest {
	t.Helper()
	m, err := LoadShellManifest(path)
	if err != nil {
		t.Fatalf("LoadShellManifest() = %v", err)
	}
	return m
}

func TestShellstateIdentityRemovalIsTheOnlyLiveCountShrink(t *testing.T) {
	writes := wrapLiveCountWrites(t)
	path := filepath.Join(t.TempDir(), "shells.json")
	def := shellstate.Definition{
		TmuxName: "sidecar-sh-one", DisplayName: "one", Namespace: "/tmp/socket",
		CreatedAt: time.Unix(1, 0).UTC(), AgentType: "claude", SkipPerms: true,
	}
	if err := shellstate.AddAtPath(path, def); err != nil {
		t.Fatalf("AddAtPath() = %v", err)
	}
	if _, err := shellstate.RenameAtPath(path, shellstate.RenameRequest{
		TmuxName: def.TmuxName, Namespace: def.Namespace, Name: "renamed",
	}); err != nil {
		t.Fatalf("RenameAtPath() = %v", err)
	}
	if err := shellstate.RemoveAtPath(path, shellstate.Identity{TmuxName: def.TmuxName, Namespace: def.Namespace}); err != nil {
		t.Fatalf("RemoveAtPath() = %v", err)
	}
	var sawRemoval bool
	for _, w := range *writes {
		if w.after < w.before {
			if !w.identityRemoval {
				t.Errorf("non-removal write shrank live count: %+v", w)
			}
			sawRemoval = true
		}
	}
	if !sawRemoval {
		t.Fatal("RemoveAtPath did not report a live-count shrink at the writer boundary")
	}
}

// TestShellRecordsSurviveIsolatedTmuxServerDeath is the end-to-end proof that
// a private-socket kill-server plus a restart with different sessions leaves
// every definition on disk, renders rows as orphans, and recreate restores
// display name, agent type, and skip-perms.
//
// Isolation is both axes: TestMain's testenv.IsolateTmux plus XDG_STATE_HOME.
// Every tmux call here passes -S <private-socket>; a bare kill-server is never
// issued.
func TestShellRecordsSurviveIsolatedTmuxServerDeath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real tmux in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	tmuxTmp := os.Getenv("TMUX_TMPDIR")
	if tmuxTmp == "" {
		t.Fatal("TMUX_TMPDIR unset; refusing to talk to tmux without TestMain isolation")
	}
	socket := tmuxenv.SocketPath()
	if !strings.HasPrefix(socket, tmuxTmp) {
		t.Fatalf("socket %s is not under TMUX_TMPDIR %s; refusing kill-server", socket, tmuxTmp)
	}
	if home, err := os.UserHomeDir(); err == nil {
		realState := filepath.Join(home, ".local", "state", "sidecar")
		if strings.HasPrefix(socket, realState) || strings.HasPrefix(os.Getenv("XDG_STATE_HOME"), realState) {
			t.Fatal("refusing: tmux or state still resolve under the real Sidecar tree")
		}
		if err := config.CheckStateIsolation(); err != nil {
			t.Fatal(err)
		}
	}

	workDir := filepath.Join(t.TempDir(), "durability-fixture")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{
		"sidecar-sh-durability-fixture-1",
		"sidecar-sh-durability-fixture-2",
	}
	for _, name := range names {
		if !shellDiscoveryPattern(workDir).MatchString(name) {
			t.Fatalf("fixture name %s does not match discovery pattern for %s", name, workDir)
		}
	}

	tmuxAt := func(args ...string) *exec.Cmd {
		return exec.Command("tmux", append([]string{"-S", socket}, args...)...)
	}
	// tmux -S does not create the socket's parent; a missing directory prints
	// "error creating" and still exits 0. The directory must be 0700 or a
	// bare client (what discoverTmuxSessionNamesForWorkDir uses) refuses it.
	socketDir := filepath.Dir(socket)
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	if err := os.Chmod(socketDir, 0o700); err != nil {
		t.Fatalf("chmod socket dir: %v", err)
	}

	for _, name := range names {
		if out, err := tmuxAt("new-session", "-d", "-s", name, "-c", workDir).CombinedOutput(); err != nil {
			t.Fatalf("tmux -S %s new-session -d -s %s: %v (%s)", socket, name, err, out)
		}
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("private socket %s missing after new-session: %v", socket, err)
	}
	discovered, firstInc, discErr := discoverTmuxSessionNamesForWorkDir(workDir)
	if discErr != nil {
		t.Fatalf("discover before kill-server: %v", discErr)
	}
	if !firstInc.IsPresent() {
		t.Fatalf("incarnation before kill-server = %s, want Present", firstInc)
	}
	for _, name := range names {
		found := false
		for _, got := range discovered {
			if got == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("discover before kill-server missing %s (got %v)", name, discovered)
		}
	}

	ns := tmuxenv.Namespace()
	created := time.Now().UTC().Truncate(time.Second)
	original := []ShellDefinition{
		{
			TmuxName: names[0], DisplayName: "Research agent", Namespace: ns,
			CreatedAt: created, AgentType: string(AgentClaude), SkipPerms: true, WorkDir: workDir,
		},
		{
			TmuxName: names[1], DisplayName: "Test runner", Namespace: ns,
			CreatedAt: created.Add(time.Second), AgentType: string(AgentCodex), SkipPerms: false, WorkDir: workDir,
		},
	}

	projectDir, err := projectdir.Resolve(workDir)
	if err != nil {
		t.Fatalf("projectdir.Resolve() = %v", err)
	}
	if err := config.AssertIsolatedPath(projectDir); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(projectDir, "shells.json")
	seed := seedManifest(t, manifestPath, original...)

	if out, err := tmuxAt("kill-server").CombinedOutput(); err != nil {
		t.Fatalf("tmux -S %s kill-server: %v (%s)", socket, err, out)
	}

	restarted := "unrelated-after-restart"
	startReplacementServer(t, tmuxAt, socket, restarted)
	t.Cleanup(func() { _ = tmuxAt("kill-server").Run() })

	_, secondInc, discErr := discoverTmuxSessionNamesForWorkDir(workDir)
	if discErr != nil {
		t.Fatalf("discover after restart: %v", discErr)
	}
	if !secondInc.IsPresent() {
		t.Fatalf("incarnation after restart = %s, want Present", secondInc)
	}
	if firstInc.Equal(secondInc) {
		t.Fatalf("incarnation after restart still %s; want a different server on the same socket", secondInc)
	}

	hooks := defaultShellStartupHooks()
	hooks.newWatcher = func(string) (shellManifestWatcher, error) {
		return newStartupTestWatcher(), nil
	}
	hooks.getWorkspaceState = func(string) state.WorkspaceState { return state.WorkspaceState{} }
	hooks.setWorkspaceState = func(string, state.WorkspaceState) error { return nil }

	p := New()
	if err := p.Init(&plugin.Context{
		WorkDir:     workDir,
		ProjectRoot: workDir,
		Config:      config.Default(),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Epoch:       1,
	}); err != nil {
		t.Fatalf("Init() = %v", err)
	}
	p.shellStartupHooks = hooks

	result, ok := p.loadShellStartup()().(shellStartupResultMsg)
	if !ok {
		t.Fatal("loadShellStartup did not return shellStartupResultMsg")
	}
	if result.err != nil {
		t.Fatalf("startup err = %v", result.err)
	}

	reloaded, err := LoadShellManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadShellManifest() = %v", err)
	}
	for _, want := range original {
		got := reloaded.FindShell(want.TmuxName)
		if got == nil {
			t.Fatalf("definition %s missing from shells.json after server death", want.TmuxName)
		}
		if !definitionFieldsEqual(*got, want) {
			t.Errorf("definition %s = %#v, want original %#v", want.TmuxName, *got, want)
		}
	}
	if reloaded.FindShell(restarted) != nil {
		t.Errorf("unrelated restarted session %s was recorded as a shell", restarted)
	}
	if seed.FindShell(original[0].TmuxName) == nil {
		t.Fatal("in-memory seed lost the first definition")
	}

	if len(result.shells) < 2 {
		t.Fatalf("reconciled shells = %d, want the two fixture rows", len(result.shells))
	}
	var recreateIdx = -1
	for i, sh := range result.shells {
		for _, want := range original {
			if sh.TmuxName != want.TmuxName {
				continue
			}
			if !sh.IsOrphaned {
				t.Errorf("shell %s IsOrphaned = false after server restart", sh.TmuxName)
			}
			if sh.Name != want.DisplayName || sh.ChosenAgent != definitionToAgentType(want.AgentType) || sh.SkipPerms != want.SkipPerms {
				t.Errorf("orphaned row %s = name %q agent %q skip=%v, want %q %q %v",
					sh.TmuxName, sh.Name, sh.ChosenAgent, sh.SkipPerms, want.DisplayName, want.AgentType, want.SkipPerms)
			}
			if sh.TmuxName == original[0].TmuxName {
				recreateIdx = i
			}
		}
	}
	if recreateIdx < 0 {
		t.Fatal("first fixture shell was not in reconcile output")
	}

	p.shells = result.shells
	p.shellManifest = reloaded
	cmd := p.recreateOrphanedShell(recreateIdx)
	if cmd == nil {
		t.Fatal("recreateOrphanedShell returned nil")
	}
	msg, ok := cmd().(ShellCreatedMsg)
	if !ok {
		t.Fatalf("recreate produced %T, want ShellCreatedMsg", cmd())
	}
	if msg.Err != nil {
		t.Fatalf("recreateOrphanedShell: %v", msg.Err)
	}
	if msg.DisplayName != original[0].DisplayName {
		t.Errorf("recreated DisplayName = %q, want %q", msg.DisplayName, original[0].DisplayName)
	}
	if msg.AgentType != AgentClaude {
		t.Errorf("recreated AgentType = %q, want %q", msg.AgentType, AgentClaude)
	}
	if !msg.SkipPerms {
		t.Error("recreated SkipPerms = false, want true")
	}
	if msg.SessionName != original[0].TmuxName {
		t.Errorf("recreated SessionName = %q, want %q", msg.SessionName, original[0].TmuxName)
	}
	if err := tmuxAt("has-session", "-t", original[0].TmuxName).Run(); err != nil {
		t.Fatalf("recreated session %s is not running: %v", original[0].TmuxName, err)
	}
}

// startReplacementServer brings a new tmux server up on a socket a kill-server
// just tore down.
//
// kill-server returns once the server has been told to exit, not once it has
// exited: the process still has to drop its clients and unlink the socket.
// A new-session issued inside that window binds the path the dying server is
// still holding and fails with "server exited unexpectedly". macOS lost that
// race rarely enough to look green locally; the Linux CI runner lost it on the
// first run and turned main red.
//
// Waiting for the socket to stop answering is what makes the restart
// deterministic. The retry is kept as well because "gone" and "the path is
// free to bind" are not the same instant on every platform, and this test's
// subject is what Sidecar does across a restart, not how fast tmux tears one
// down.
func startReplacementServer(t *testing.T, tmuxAt func(...string) *exec.Cmd, socket, session string) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for {
		_, err := tmuxAt("list-sessions").Output()
		if err != nil && tmuxReportedNoServer(err) {
			break
		}
		if _, statErr := os.Stat(socket); os.IsNotExist(statErr) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tmux server on %s still answering 15s after kill-server", socket)
		}
		time.Sleep(20 * time.Millisecond)
	}

	var lastErr error
	var lastOut []byte
	for time.Now().Before(deadline) {
		out, err := tmuxAt("new-session", "-d", "-s", session).CombinedOutput()
		if err == nil {
			return
		}
		lastErr, lastOut = err, out
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tmux -S %s new-session -d -s %s: %v (%s)", socket, session, lastErr, lastOut)
}
