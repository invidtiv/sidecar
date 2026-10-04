package shellstate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEditAtPathNoOpReturnsFreshSnapshotWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	id := Identity{TmuxName: "peer", Namespace: "/tmp/socket"}
	if err := AddAtPath(path, Definition{TmuxName: id.TmuxName, Namespace: id.Namespace, DisplayName: "Peer"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stale := Snapshot{Version: 1, Shells: []Definition{{TmuxName: "stale"}}}
	got, changed, err := EditAtPath(path, &stale, false, func(m *Snapshot) (bool, error) {
		if len(m.Shells) != 1 || m.Shells[0].TmuxName != id.TmuxName {
			t.Fatalf("edit saw stale state: %+v", m)
		}
		return false, nil
	})
	if err != nil || changed || got.Version != CurrentVersion || len(got.Shells) != 1 || got.Shells[0].TmuxName != id.TmuxName {
		t.Fatalf("no-op = %+v, %v, %v", got, changed, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("no-op rewrote manifest: %v", err)
	}
	afterInfo, err := os.Stat(path)
	if err != nil || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("no-op changed manifest modification time: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("no-op created temporary writer file: %v", err)
	}
}

func TestEditAtPathRecoveryIsExplicitAndNeverOverridesFutureSchema(t *testing.T) {
	for _, content := range []string{"corrupt", `{"version":99,"shells":[],"future":true}`} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shells.json")
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			called := false
			edit := func(m *Snapshot) (bool, error) {
				called = true
				m.Shells = append(m.Shells, Definition{TmuxName: "new"})
				return true, nil
			}
			if _, _, err := EditAtPath(path, nil, false, edit); err == nil || called {
				t.Fatalf("unreadable state reached edit: called=%v, err=%v", called, err)
			}
			fallback := Snapshot{Version: CurrentVersion, Shells: []Definition{{TmuxName: "known"}}}
			got, changed, err := EditAtPath(path, &fallback, false, edit)
			if content == "corrupt" {
				if err != nil || !changed || len(got.Shells) != 2 || got.Shells[0].TmuxName != "known" {
					t.Fatalf("explicit recovery = %+v, %v, %v", got, changed, err)
				}
			} else {
				if !IsUnknownVersion(err) || called || changed {
					t.Fatalf("future schema reached edit: called=%v, changed=%v, err=%v", called, changed, err)
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != content {
					t.Fatalf("future manifest changed: %s, %v", data, err)
				}
			}
		})
	}
}

func TestEditAtPathExpiresBeforeDecisionAndReportsRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	if err := AddAtPath(path, Definition{TmuxName: "live", DisplayName: "Live"}); err != nil {
		t.Fatal(err)
	}
	previous := ObserveLiveCountWrite
	t.Cleanup(func() { ObserveLiveCountWrite = previous })
	var observed bool
	ObserveLiveCountWrite = func(gotPath string, before, after int, removal bool) {
		observed = gotPath == path && before == 1 && after == 0 && removal
	}
	// A fallback alone cannot override readable bytes. Age a real tombstone
	// with the same shared writer, then verify expiry precedes the next edit.
	if _, _, err := EditAtPath(path, nil, false, func(m *Snapshot) (bool, error) {
		m.Tombstones = []Tombstone{{Definition: Definition{TmuxName: "expired"}, DeletedAt: time.Now().Add(-2 * TombstoneRetention())}}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	got, changed, err := EditAtPath(path, nil, true, func(m *Snapshot) (bool, error) {
		if len(m.Tombstones) != 0 {
			t.Fatalf("edit saw expired tombstone: %+v", m.Tombstones)
		}
		m.Shells = nil
		return true, nil
	})
	if err != nil || !changed || len(got.Shells) != 0 || !observed {
		t.Fatalf("removal = %+v, %v, %v; observed=%v", got, changed, err, observed)
	}
}

func TestSnapshotOperationsRefusePathlessHandles(t *testing.T) {
	for _, path := range []string{"", "  "} {
		if _, err := SnapshotAtPath(path); !errors.Is(err, ErrNoManifestPath) {
			t.Fatalf("SnapshotAtPath(%q) = %v", path, err)
		}
		if _, _, err := EditAtPath(path, nil, false, func(*Snapshot) (bool, error) {
			t.Fatal("pathless edit reached callback")
			return true, nil
		}); !errors.Is(err, ErrNoManifestPath) {
			t.Fatalf("EditAtPath(%q) = %v", path, err)
		}
	}
}

func TestRenameRetainsMissingManifestRefusals(t *testing.T) {
	for _, missingParent := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "shells.json")
		want := "read shell manifest"
		if missingParent {
			path = filepath.Join(filepath.Dir(path), "missing", "shells.json")
			want = "lock shell manifest"
		}
		_, err := RenameAtPath(path, RenameRequest{TmuxName: "shell", Name: "New name"})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("missing-parent=%v rename error = %v, want %q", missingParent, err, want)
		}
		if missingParent {
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatalf("rename created missing parent: %v", err)
			}
		}
	}
}
