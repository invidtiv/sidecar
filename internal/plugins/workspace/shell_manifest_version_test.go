package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/shellstate"
)

// futureManifest is a shells.json from a newer Sidecar: a schema version this
// build has never heard of, carrying a per-record field it cannot parse.
// Marshalling ShellManifest over it would drop `pinned` silently.
const futureManifest = `{
  "version": 99,
  "shells": [
    {
      "tmuxName": "sidecar-sh-one",
      "displayName": "from the future",
      "pinned": true
    }
  ]
}
`

func TestManifestWritersRefuseUnknownSchemaVersion(t *testing.T) {
	writers := map[string]func(m *ShellManifest) error{
		"AddShell": func(m *ShellManifest) error {
			return m.AddShell(ShellDefinition{TmuxName: "sidecar-sh-two", DisplayName: "new"})
		},
		"UpdateShell": func(m *ShellManifest) error {
			return m.UpdateShell(ShellDefinition{TmuxName: "sidecar-sh-one", DisplayName: "renamed"})
		},
		"RemoveShell": func(m *ShellManifest) error { return m.RemoveShell("sidecar-sh-one") },
		"EnsureShells": func(m *ShellManifest) error {
			_, err := m.EnsureShells([]ShellDefinition{{TmuxName: "sidecar-sh-three", DisplayName: "Shell 3"}})
			return err
		},
		"BackfillWorkDirs": func(m *ShellManifest) error {
			return m.BackfillWorkDirs(map[string]string{"sidecar-sh-one": "/work/one"})
		},
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shells.json")
			if err := os.WriteFile(path, []byte(futureManifest), 0644); err != nil {
				t.Fatal(err)
			}
			m, err := LoadShellManifest(path)
			if err != nil {
				t.Fatal(err)
			}
			err = write(m)
			if err == nil {
				t.Fatalf("%s rewrote a version 99 manifest instead of refusing", name)
			}
			if !shellstate.IsUnknownVersion(err) {
				t.Fatalf("%s error = %v; want an unknown-version refusal", name, err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(after) != futureManifest {
				t.Fatalf("%s changed the file:\n%s", name, after)
			}
		})
	}
}

func TestManifestWriteUpgradesVersionOneInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	v1 := `{
  "version": 1,
  "shells": [
    {"tmuxName": "sidecar-sh-one", "displayName": "One", "agentType": "codex", "skipPerms": true, "workDir": "/work/one"}
  ]
}
`
	if err := os.WriteFile(path, []byte(v1), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadShellManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AddShell(ShellDefinition{TmuxName: "sidecar-sh-two", DisplayName: "Two"}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadShellManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Version != shellstate.CurrentVersion {
		t.Fatalf("version after write = %d; want %d", reloaded.Version, shellstate.CurrentVersion)
	}
	kept := reloaded.FindShell("sidecar-sh-one")
	if kept == nil {
		t.Fatal("the v1 definition did not survive the upgrade")
	}
	if kept.DisplayName != "One" || kept.AgentType != "codex" || !kept.SkipPerms || kept.WorkDir != "/work/one" {
		t.Fatalf("upgrade changed the v1 definition: %+v", *kept)
	}
}

// Expiring a tombstone makes a still-running session of that name adoptable
// again. That is a deliberate consequence of bounded retention, not an
// accident: after the window Sidecar no longer remembers the forget, so a
// session that is genuinely running should come back as a row rather than stay
// invisible forever. Both halves are pinned here.
func TestTombstoneExpiryRestoresAdoption(t *testing.T) {
	shellstate.SetTombstoneRetention(24 * time.Hour)
	t.Cleanup(shellstate.ResetTombstoneRetention)

	path := filepath.Join(t.TempDir(), "shells.json")
	m, err := LoadShellManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	def := ShellDefinition{TmuxName: "sidecar-sh-project-1", DisplayName: "prior task", AgentType: "codex"}
	if err := m.AddShell(def); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveShell(def.TmuxName); err != nil {
		t.Fatal(err)
	}

	// Inside the window the forget still suppresses re-adoption.
	discovered := ShellDefinition{TmuxName: def.TmuxName, DisplayName: "Shell 1"}
	if _, err := m.EnsureShells([]ShellDefinition{discovered}); err != nil {
		t.Fatal(err)
	}
	if m.FindShell(def.TmuxName) != nil {
		t.Fatal("a live tombstone failed to suppress re-adoption")
	}

	// Age the tombstone past the window, on disk, so the next handle reads it
	// the way it would after a fortnight of not touching this project.
	aged := struct {
		Version    int                    `json:"version"`
		Shells     []ShellDefinition      `json:"shells"`
		Tombstones []shellstate.Tombstone `json:"tombstones"`
	}{
		Version:    shellstate.CurrentVersion,
		Shells:     []ShellDefinition{},
		Tombstones: []shellstate.Tombstone{{Definition: def, DeletedAt: time.Now().UTC().Add(-72 * time.Hour)}},
	}
	data, err := json.MarshalIndent(aged, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadShellManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := reloaded.EnsureShells([]ShellDefinition{discovered})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("EnsureShells() changed = false; an expired tombstone should no longer suppress the name")
	}
	if got := reloaded.FindShell(def.TmuxName); got == nil {
		t.Fatal("the running session was not adopted after its tombstone expired")
	}
	if len(reloaded.Tombstones) != 0 {
		t.Fatalf("expired tombstone survived the write: %+v", reloaded.Tombstones)
	}
}
