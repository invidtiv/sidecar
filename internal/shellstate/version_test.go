package shellstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// futureManifest is what a newer Sidecar's shells.json looks like from here: a
// version this build has never heard of, and a per-record field it cannot
// parse. Rewriting it would drop `pinned` silently, which is the exact failure
// the version guard exists to prevent.
const futureManifest = `{
  "version": 99,
  "shells": [
    {
      "tmuxName": "sidecar-sh-one",
      "displayName": "from the future",
      "namespace": "/tmp/socket",
      "createdAt": "2026-08-20T10:00:00Z",
      "pinned": true
    }
  ],
  "tombstones": []
}
`

func TestWritersRefuseUnknownSchemaVersion(t *testing.T) {
	id := Identity{TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket"}
	writers := map[string]func(path string) error{
		"AddAtPath": func(path string) error {
			return AddAtPath(path, Definition{TmuxName: "sidecar-sh-two", DisplayName: "new", Namespace: "/tmp/socket"})
		},
		"RemoveAtPath": func(path string) error { return RemoveAtPath(path, id) },
		"RemoveIfUnchangedAtPath": func(path string) error {
			return RemoveIfUnchangedAtPath(path, id, time.Time{})
		},
		"RestoreAtPath": func(path string) error {
			_, err := RestoreAtPath(path, id)
			return err
		},
		"RenameAtPath": func(path string) error {
			_, err := RenameAtPath(path, RenameRequest{TmuxName: id.TmuxName, Namespace: id.Namespace, Name: "renamed"})
			return err
		},
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shells.json")
			if err := os.WriteFile(path, []byte(futureManifest), 0644); err != nil {
				t.Fatal(err)
			}
			err := write(path)
			if err == nil {
				t.Fatalf("%s rewrote a version 99 manifest instead of refusing", name)
			}
			if !IsUnknownVersion(err) {
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

// Reads keep working against a newer file: refusing to read it would break
// `sidecar shell name` for no gain, since a read cannot lose a field.
func TestReadsAllowUnknownSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	if err := os.WriteFile(path, []byte(futureManifest), 0644); err != nil {
		t.Fatal(err)
	}
	defs, err := ListAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].DisplayName != "from the future" {
		t.Fatalf("ListAtPath = %+v", defs)
	}
}

func TestM0VersionTwoManifestCompatibilityFixture(t *testing.T) {
	defs, err := ListAtPath("testdata/v2-shells.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("definitions = %+v", defs)
	}
	got := defs[0]
	if got.TmuxName != "sidecar-sh-sidecar-4" || got.DisplayName != "reviewer" || got.AgentType != "codex" || !got.SkipPerms || got.WorkDir != "/repo" {
		t.Fatalf("v2 fixture drifted: %+v", got)
	}
	manifest, err := readManifest("testdata/v2-shells.json")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 2 || len(manifest.Tombstones) != 0 {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func TestFirstWriteUpgradesVersionOneInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	created := time.Now().UTC().Truncate(time.Second)
	kept := Definition{
		TmuxName: "sidecar-sh-one", DisplayName: "One", Namespace: "/tmp/socket",
		CreatedAt: created, AgentType: "codex", SkipPerms: true, WorkDir: "/tmp/project",
	}
	writeTestManifest(t, path, manifest{Version: 1, Shells: []Definition{kept}})

	if err := AddAtPath(path, Definition{TmuxName: "sidecar-sh-two", DisplayName: "Two", Namespace: "/tmp/socket"}); err != nil {
		t.Fatal(err)
	}

	m, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != CurrentVersion {
		t.Fatalf("version after write = %d; want %d", m.Version, CurrentVersion)
	}
	if len(m.Shells) != 2 {
		t.Fatalf("shells after write = %+v", m.Shells)
	}
	if m.Shells[0] != kept {
		t.Fatalf("upgrade changed the v1 definition:\n got %+v\nwant %+v", m.Shells[0], kept)
	}
}

// A rename is the one writer that does not go through mutateManifestLive, so
// it gets its own upgrade check rather than being assumed to follow.
func TestRenameUpgradesVersionOneInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	writeTestManifest(t, path, manifest{Version: 1, Shells: []Definition{
		{TmuxName: "sidecar-sh-one", DisplayName: "old", Namespace: "/tmp/socket"},
	}})
	if _, err := RenameAtPath(path, RenameRequest{TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket", Name: "new"}); err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Version int `json:"version"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Version != CurrentVersion {
		t.Fatalf("version after rename = %d; want %d", raw.Version, CurrentVersion)
	}
}
