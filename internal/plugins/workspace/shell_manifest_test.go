package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

func TestShellManifest_LoadMissing(t *testing.T) {
	// Load from non-existent file should return empty manifest
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	m, err := LoadShellManifest(path)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	if m == nil {
		t.Fatal("LoadShellManifest() returned nil")
	}
	if len(m.Shells) != 0 {
		t.Errorf("expected empty shells, got %d", len(m.Shells))
	}
}

func TestShellManifest_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".sidecar", "shells.json")

	// Create and save manifest
	m := &ShellManifest{
		Version: manifestVersion,
		Shells:  []ShellDefinition{},
		path:    path,
	}

	def := ShellDefinition{
		TmuxName:    "sidecar-sh-test-1",
		DisplayName: "Test Shell",
		CreatedAt:   time.Now().Truncate(time.Second),
		AgentType:   "claude",
		SkipPerms:   true,
	}

	if err := m.AddShell(def); err != nil {
		t.Fatalf("AddShell() error = %v", err)
	}

	// Load and verify
	m2, err := LoadShellManifest(path)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	if len(m2.Shells) != 1 {
		t.Fatalf("expected 1 shell, got %d", len(m2.Shells))
	}
	if m2.Shells[0].TmuxName != def.TmuxName {
		t.Errorf("TmuxName = %q, want %q", m2.Shells[0].TmuxName, def.TmuxName)
	}
	if m2.Shells[0].DisplayName != def.DisplayName {
		t.Errorf("DisplayName = %q, want %q", m2.Shells[0].DisplayName, def.DisplayName)
	}
	if m2.Shells[0].AgentType != def.AgentType {
		t.Errorf("AgentType = %q, want %q", m2.Shells[0].AgentType, def.AgentType)
	}
	if m2.Shells[0].SkipPerms != def.SkipPerms {
		t.Errorf("SkipPerms = %v, want %v", m2.Shells[0].SkipPerms, def.SkipPerms)
	}
}

func TestShellManifest_AddRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	m, _ := LoadShellManifest(path)

	// Add two shells
	_ = m.AddShell(ShellDefinition{TmuxName: "shell-1", DisplayName: "Shell 1"})
	_ = m.AddShell(ShellDefinition{TmuxName: "shell-2", DisplayName: "Shell 2"})

	if len(m.Shells) != 2 {
		t.Fatalf("expected 2 shells, got %d", len(m.Shells))
	}

	// Remove first
	if err := m.RemoveShell("shell-1"); err != nil {
		t.Fatalf("RemoveShell() error = %v", err)
	}

	if len(m.Shells) != 1 {
		t.Fatalf("expected 1 shell after remove, got %d", len(m.Shells))
	}
	if m.Shells[0].TmuxName != "shell-2" {
		t.Errorf("wrong shell remaining: %q", m.Shells[0].TmuxName)
	}
	if len(m.Tombstones) != 1 || m.Tombstones[0].TmuxName != "shell-1" || m.Tombstones[0].DeletedAt.IsZero() {
		t.Fatalf("tombstones after remove = %+v", m.Tombstones)
	}
	reloaded, err := LoadShellManifest(path)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	if len(reloaded.Tombstones) != 1 || reloaded.Tombstones[0].TmuxName != "shell-1" || reloaded.Tombstones[0].DisplayName != "Shell 1" {
		t.Fatalf("reloaded tombstones = %+v", reloaded.Tombstones)
	}
}

func TestShellManifest_FindShell(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	m, _ := LoadShellManifest(path)

	_ = m.AddShell(ShellDefinition{TmuxName: "shell-1", DisplayName: "Shell 1"})

	// Find existing
	found := m.FindShell("shell-1")
	if found == nil {
		t.Fatal("FindShell() returned nil for existing shell")
	}
	if found.DisplayName != "Shell 1" {
		t.Errorf("DisplayName = %q, want %q", found.DisplayName, "Shell 1")
	}

	// Find non-existing
	if m.FindShell("shell-999") != nil {
		t.Error("FindShell() should return nil for non-existing shell")
	}
}

func TestShellManifest_UpdateShell(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	m, _ := LoadShellManifest(path)

	_ = m.AddShell(ShellDefinition{TmuxName: "shell-1", DisplayName: "Original"})

	// Update
	if err := m.UpdateShell(ShellDefinition{TmuxName: "shell-1", DisplayName: "Updated"}); err != nil {
		t.Fatalf("UpdateShell() error = %v", err)
	}

	found := m.FindShell("shell-1")
	if found.DisplayName != "Updated" {
		t.Errorf("DisplayName = %q, want %q", found.DisplayName, "Updated")
	}
}

func TestShellManifest_RenameShellUsesSharedAtomicOperation(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	m := &ShellManifest{Version: manifestVersion, path: path}
	for _, def := range []ShellDefinition{
		{TmuxName: "sidecar-sh-one", DisplayName: "old", Namespace: "/tmp/socket"},
		{TmuxName: "sidecar-sh-two", DisplayName: "taken", Namespace: "/tmp/socket"},
	} {
		if err := m.AddShell(def); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.RenameShell("sidecar-sh-one", "/tmp/socket", "taken"); err == nil {
		t.Fatal("duplicate rename succeeded")
	}
	if got := m.FindShell("sidecar-sh-one").DisplayName; got != "old" {
		t.Fatalf("failed persistence changed memory to %q", got)
	}
	result, err := m.RenameShell("sidecar-sh-one", "/tmp/socket", "new")
	if err != nil || !result.Changed {
		t.Fatalf("RenameShell() = %+v, %v", result, err)
	}
	if got := m.FindShell("sidecar-sh-one").DisplayName; got != "new" {
		t.Fatalf("successful persistence left memory at %q", got)
	}
	reloaded, err := LoadShellManifest(path)
	if err != nil || reloaded.FindShell("sidecar-sh-one").DisplayName != "new" {
		t.Fatalf("persisted rename missing: %+v, %v", reloaded, err)
	}
}

func TestShellManifest_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	sidecarDir := filepath.Join(dir, ".sidecar")
	_ = os.MkdirAll(sidecarDir, 0755)
	path := filepath.Join(sidecarDir, "shells.json")

	// Write corrupted JSON
	_ = os.WriteFile(path, []byte("{invalid json"), 0644)

	// Should return empty manifest, not error
	m, err := LoadShellManifest(path)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	if len(m.Shells) != 0 {
		t.Errorf("expected empty shells for corrupted file, got %d", len(m.Shells))
	}
}

func TestShellManifest_AddDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	m, _ := LoadShellManifest(path)

	_ = m.AddShell(ShellDefinition{TmuxName: "shell-1", DisplayName: "Original"})
	_ = m.AddShell(ShellDefinition{TmuxName: "shell-1", DisplayName: "Updated"})

	// Should update, not duplicate
	if len(m.Shells) != 1 {
		t.Fatalf("expected 1 shell, got %d", len(m.Shells))
	}
	if m.Shells[0].DisplayName != "Updated" {
		t.Errorf("DisplayName = %q, want %q", m.Shells[0].DisplayName, "Updated")
	}
}

// TestShellManifest_ConcurrentAdd tests concurrent AddShell calls (td-6db032)
func TestShellManifest_ConcurrentAdd(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	m, _ := LoadShellManifest(path)

	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			err := m.AddShell(ShellDefinition{
				TmuxName:    fmt.Sprintf("shell-%d", idx),
				DisplayName: fmt.Sprintf("Shell %d", idx),
			})
			if err != nil {
				t.Errorf("AddShell(%d) error = %v", idx, err)
			}
		}(i)
	}

	wg.Wait()

	if len(m.Shells) != numGoroutines {
		t.Errorf("expected %d shells, got %d", numGoroutines, len(m.Shells))
	}
}

// TestShellManifest_ConcurrentAddRemove tests concurrent Add and Remove (td-6db032)
func TestShellManifest_ConcurrentAddRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	m, _ := LoadShellManifest(path)

	// Add initial shells
	for i := 0; i < 5; i++ {
		_ = m.AddShell(ShellDefinition{TmuxName: fmt.Sprintf("shell-%d", i)})
	}

	var wg sync.WaitGroup
	// Concurrent adds
	for i := 5; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = m.AddShell(ShellDefinition{TmuxName: fmt.Sprintf("shell-%d", idx)})
		}(i)
	}
	// Concurrent removes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = m.RemoveShell(fmt.Sprintf("shell-%d", idx))
		}(i)
	}

	wg.Wait()

	// Should have exactly 5 shells (0-4 removed, 5-9 added)
	if len(m.Shells) != 5 {
		t.Errorf("expected 5 shells, got %d", len(m.Shells))
	}
}

// TestShellManifest_ConcurrentUpdate tests concurrent UpdateShell calls (td-6db032)
func TestShellManifest_ConcurrentUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	m, _ := LoadShellManifest(path)

	_ = m.AddShell(ShellDefinition{TmuxName: "shell-1", DisplayName: "Original"})

	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_ = m.UpdateShell(ShellDefinition{
				TmuxName:    "shell-1",
				DisplayName: fmt.Sprintf("Update %d", idx),
			})
		}(i)
	}

	wg.Wait()

	// Should still have exactly 1 shell
	if len(m.Shells) != 1 {
		t.Errorf("expected 1 shell, got %d", len(m.Shells))
	}
}

// TestShellManifest_ConcurrentFind tests concurrent FindShell with modifications (td-6db032)
func TestShellManifest_ConcurrentFind(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	m, _ := LoadShellManifest(path)

	_ = m.AddShell(ShellDefinition{TmuxName: "shell-1", DisplayName: "Test"})

	var wg sync.WaitGroup
	// Concurrent finds
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.FindShell("shell-1")
		}()
	}
	// Concurrent updates
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = m.UpdateShell(ShellDefinition{
				TmuxName:    "shell-1",
				DisplayName: fmt.Sprintf("Update %d", idx),
			})
		}(i)
	}

	wg.Wait()
	// Test passes if no race detected (run with -race flag)
}

// TestShellManifest_MigrationFromEmptyManifest tests migrating display names to new manifest (td-e1b7ef)
func TestShellManifest_MigrationFromEmptyManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")

	// Simulate migration: manifest exists but has no display name, need to update it
	m, _ := LoadShellManifest(path)
	_ = m.AddShell(ShellDefinition{
		TmuxName:    "sidecar-sh-test-1",
		DisplayName: "", // Empty initially (like after tmux session discovery)
		CreatedAt:   time.Now(),
	})

	// Verify shell has no display name
	found := m.FindShell("sidecar-sh-test-1")
	if found == nil {
		t.Fatal("shell not found")
	}
	if found.DisplayName != "" {
		t.Errorf("initial DisplayName = %q, want empty", found.DisplayName)
	}

	// Simulate migration: update with display name from state.json
	err := m.UpdateShell(ShellDefinition{
		TmuxName:    "sidecar-sh-test-1",
		DisplayName: "Backend", // Migrated from state.json
		CreatedAt:   found.CreatedAt,
	})
	if err != nil {
		t.Fatalf("UpdateShell() error = %v", err)
	}

	// Verify migration worked
	found = m.FindShell("sidecar-sh-test-1")
	if found.DisplayName != "Backend" {
		t.Errorf("migrated DisplayName = %q, want Backend", found.DisplayName)
	}

	// Verify persisted correctly
	m2, _ := LoadShellManifest(path)
	found2 := m2.FindShell("sidecar-sh-test-1")
	if found2 == nil {
		t.Fatal("shell not found after reload")
	}
	if found2.DisplayName != "Backend" {
		t.Errorf("persisted DisplayName = %q, want Backend", found2.DisplayName)
	}
}

// TestShellManifest_MigrationPreservesExisting tests migration doesn't overwrite existing manifest data (td-e1b7ef)
func TestShellManifest_MigrationPreservesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")

	// Create manifest with existing data
	m, _ := LoadShellManifest(path)
	_ = m.AddShell(ShellDefinition{
		TmuxName:    "sidecar-sh-test-1",
		DisplayName: "Original Name",
		AgentType:   "claude",
		SkipPerms:   true,
		CreatedAt:   time.Now(),
	})

	// Simulate partial migration update (only updates display name, preserves other fields)
	found := m.FindShell("sidecar-sh-test-1")
	err := m.UpdateShell(ShellDefinition{
		TmuxName:    "sidecar-sh-test-1",
		DisplayName: "Migrated Name",
		AgentType:   found.AgentType, // Preserve
		SkipPerms:   found.SkipPerms, // Preserve
		CreatedAt:   found.CreatedAt, // Preserve
	})
	if err != nil {
		t.Fatalf("UpdateShell() error = %v", err)
	}

	// Verify all fields preserved
	updated := m.FindShell("sidecar-sh-test-1")
	if updated.DisplayName != "Migrated Name" {
		t.Errorf("DisplayName = %q, want Migrated Name", updated.DisplayName)
	}
	if updated.AgentType != "claude" {
		t.Errorf("AgentType = %q, want claude", updated.AgentType)
	}
	if !updated.SkipPerms {
		t.Error("SkipPerms should be true")
	}
}

// TestShellManifest_MigrationNewShell tests migration handles shells not yet in manifest (td-e1b7ef)
func TestShellManifest_MigrationNewShell(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")

	// Empty manifest
	m, _ := LoadShellManifest(path)
	if len(m.Shells) != 0 {
		t.Fatalf("expected empty manifest, got %d shells", len(m.Shells))
	}

	// UpdateShell on non-existent shell should add it (migration behavior)
	err := m.UpdateShell(ShellDefinition{
		TmuxName:    "sidecar-sh-new",
		DisplayName: "Newly Migrated",
		CreatedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("UpdateShell() error = %v", err)
	}

	// Verify shell was added
	if len(m.Shells) != 1 {
		t.Fatalf("expected 1 shell, got %d", len(m.Shells))
	}
	found := m.FindShell("sidecar-sh-new")
	if found == nil {
		t.Fatal("shell not found")
	}
	if found.DisplayName != "Newly Migrated" {
		t.Errorf("DisplayName = %q, want Newly Migrated", found.DisplayName)
	}
}

// TestShellManifest_LockAcquisitionNonBlocking tests that lock acquisition uses non-blocking retry (td-984ead)
func TestShellManifest_LockAcquisitionNonBlocking(t *testing.T) {
	dir := t.TempDir()
	sidecarDir := filepath.Join(dir, ".sidecar")
	_ = os.MkdirAll(sidecarDir, 0755)
	path := filepath.Join(sidecarDir, "shells.json")

	// Create initial manifest
	m, _ := LoadShellManifest(path)
	_ = m.AddShell(ShellDefinition{TmuxName: "shell-1"})

	// Verify concurrent operations don't deadlock (would timeout if blocking indefinitely)
	done := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			for j := 0; j < 5; j++ {
				_ = m.UpdateShell(ShellDefinition{
					TmuxName:    "shell-1",
					DisplayName: fmt.Sprintf("Update %d-%d", idx, j),
				})
			}
			done <- true
		}(i)
	}

	// Wait for completion with timeout
	timeout := time.After(10 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-done:
			// OK
		case <-timeout:
			t.Fatal("concurrent operations timed out - possible deadlock")
		}
	}
}

// TestSaveRefusesRealUserManifestUnderIsolation is the td-8d18de regression at
// the write choke point: an instance that declared isolated state must not be
// able to touch the real user's manifest, not even to create the lock or temp
// file next to it.
func TestSaveRefusesRealUserManifestUnderIsolation(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv(config.IsolationEnv, "1")

	realDir := filepath.Join(fakeHome, ".local", "state", "sidecar", "projects", "sidecar")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(realDir, "shells.json")

	m := &ShellManifest{Version: manifestVersion, path: path}
	m.Shells = []ShellDefinition{{TmuxName: "sidecar-sh-sidecar-1", DisplayName: "shell 1"}}

	// Every writer funnels through mutateLocked, so they must refuse.
	if err := m.AddShell(ShellDefinition{TmuxName: "sidecar-sh-sidecar-2"}); err == nil {
		t.Error("AddShell() = nil, want refusal")
	}
	if err := m.UpdateShell(ShellDefinition{TmuxName: "sidecar-sh-sidecar-1"}); err == nil {
		t.Error("UpdateShell() = nil, want refusal")
	}
	if err := m.RemoveShell("sidecar-sh-sidecar-1"); err == nil {
		t.Error("RemoveShell() = nil, want refusal")
	}
	if _, err := m.EnsureShells([]ShellDefinition{{TmuxName: "sidecar-sh-sidecar-3"}}); err == nil {
		t.Error("EnsureShells() = nil, want refusal")
	}

	for _, name := range []string{"shells.json", "shells.json.tmp", "shells.json.lock"} {
		if _, err := os.Stat(filepath.Join(realDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s exists in the real user tree after a refused write", name)
		}
	}
}

// TestLoadRefusesRealUserManifestUnderIsolation is the read counterpart: an
// isolated instance must not even observe the real manifest, or it would adopt
// the developer's live shells as its own.
func TestLoadRefusesRealUserManifestUnderIsolation(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv(config.IsolationEnv, "1")

	realDir := filepath.Join(fakeHome, ".local", "state", "sidecar", "projects", "sidecar")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(realDir, "shells.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"shells":[{"tmuxName":"sidecar-sh-sidecar-1"}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := LoadShellManifest(path)
	if err == nil {
		t.Fatal("LoadShellManifest() = nil error, want refusal to read the real user manifest")
	}
	if m != nil {
		t.Errorf("LoadShellManifest() returned a manifest alongside the error: %+v", m)
	}
	if _, err := os.Stat(filepath.Join(realDir, "shells.json.lock")); !os.IsNotExist(err) {
		t.Error("shells.json.lock exists in the real user tree after a refused read")
	}
}

// TestSaveAllowsRealUserManifestWithoutIsolation is the other half of the
// guarantee: with no isolation promise, an ordinary run still writes its real
// manifest exactly as before.
func TestSaveAllowsRealUserManifestWithoutIsolation(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv(config.IsolationEnv, "")
	// A test binary asserts isolation by default; this is the one case that
	// deliberately exercises the ordinary path, against a temp HOME.
	t.Setenv(config.AllowRealStateEnv, "1")
	// The package-wide TestMain sets a test state dir, which itself asserts
	// isolation; clear it for this one case.
	config.ResetTestStateDir()
	t.Cleanup(func() { config.SetTestStateDir(filepath.Join(os.Getenv("XDG_STATE_HOME"), "sidecar")) })

	realDir := filepath.Join(fakeHome, ".local", "state", "sidecar", "projects", "sidecar")
	path := filepath.Join(realDir, "shells.json")

	m := &ShellManifest{Version: manifestVersion, path: path}
	if err := m.AddShell(ShellDefinition{TmuxName: "sidecar-sh-sidecar-1", DisplayName: "shell 1"}); err != nil {
		t.Fatalf("AddShell() = %v, want nil for an ordinary run", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
}

// TestManifestWritesMergeWithDisk is the concurrent-instance half of td-8d18de:
// a writer must not marshal its own stale snapshot over what another instance
// has since written. Each edit re-reads the file inside the exclusive lock.
func TestManifestWritesMergeWithDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")

	peer, err := LoadShellManifest(path)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	if err := peer.AddShell(ShellDefinition{TmuxName: "peer-1", DisplayName: "Peer shell"}); err != nil {
		t.Fatalf("AddShell() error = %v", err)
	}

	// A snapshot taken before the peer's write.
	stale := &ShellManifest{Version: manifestVersion, path: path}

	if err := stale.AddShell(ShellDefinition{TmuxName: "mine-1", DisplayName: "My shell"}); err != nil {
		t.Fatalf("AddShell() error = %v", err)
	}

	reloaded, err := LoadShellManifest(path)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	if len(reloaded.Shells) != 2 {
		t.Fatalf("manifest = %#v, want both the peer's entry and ours", reloaded.Shells)
	}
	if reloaded.FindShell("peer-1") == nil {
		t.Error("a stale writer erased the peer's concurrently written entry")
	}
	if reloaded.FindShell("mine-1") == nil {
		t.Error("our own entry was not written")
	}
}

// TestManifestRevisionTracksWrites backs the stale-sync guard: a reconciliation
// that started before a local delete must be able to tell that it did.
func TestManifestRevisionTracksWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	m, err := LoadShellManifest(path)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}

	before := m.Revision()
	if err := m.AddShell(ShellDefinition{TmuxName: "shell-1"}); err != nil {
		t.Fatalf("AddShell() error = %v", err)
	}
	if m.Revision() == before {
		t.Fatal("Revision() did not move after a write")
	}

	after := m.Revision()
	if err := m.RemoveShell("does-not-exist"); err != nil {
		t.Fatalf("RemoveShell() error = %v", err)
	}
	if m.Revision() != after {
		t.Fatal("Revision() moved on a no-op write")
	}
}
