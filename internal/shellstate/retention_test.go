package shellstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

func TestExpireTombstones(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	stone := func(name string, age time.Duration) Tombstone {
		return Tombstone{Definition: Definition{TmuxName: name}, DeletedAt: now.Add(-age)}
	}
	undated := Tombstone{Definition: Definition{TmuxName: "undated"}}

	tests := []struct {
		name   string
		tombs  []Tombstone
		window time.Duration
		want   []string
	}{
		{
			name:   "expired records are dropped",
			tombs:  []Tombstone{stone("old", 15*24*time.Hour), stone("fresh", time.Hour)},
			window: config.DefaultTombstoneRetention,
			want:   []string{"fresh"},
		},
		{
			name:   "a record exactly at the window is kept",
			tombs:  []Tombstone{stone("edge", config.DefaultTombstoneRetention)},
			window: config.DefaultTombstoneRetention,
			want:   []string{"edge"},
		},
		{
			name:   "a zero window keeps everything",
			tombs:  []Tombstone{stone("ancient", 3650*24*time.Hour)},
			window: config.KeepTombstonesForever,
			want:   []string{"ancient"},
		},
		{
			// Guessing wrong here deletes a record nobody asked to delete.
			name:   "a tombstone with no deletedAt is kept",
			tombs:  []Tombstone{undated},
			window: time.Hour,
			want:   []string{"undated"},
		},
		{
			name:   "all expired",
			tombs:  []Tombstone{stone("a", time.Hour), stone("b", 2*time.Hour)},
			window: time.Minute,
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpireTombstones(tt.tombs, now, tt.window)
			if len(got) != len(tt.want) {
				t.Fatalf("kept %d records (%+v); want %v", len(got), got, tt.want)
			}
			for i, name := range tt.want {
				if got[i].TmuxName != name {
					t.Fatalf("kept[%d] = %q; want %q", i, got[i].TmuxName, name)
				}
			}
		})
	}
}

func TestExpireTombstonesDoesNotEditCallerSlice(t *testing.T) {
	now := time.Now().UTC()
	tombs := []Tombstone{
		{Definition: Definition{TmuxName: "old"}, DeletedAt: now.Add(-48 * time.Hour)},
		{Definition: Definition{TmuxName: "fresh"}, DeletedAt: now},
	}
	if got := ExpireTombstones(tombs, now, time.Hour); len(got) != 1 {
		t.Fatalf("expired list = %+v", got)
	}
	if len(tombs) != 2 || tombs[0].TmuxName != "old" || tombs[1].TmuxName != "fresh" {
		t.Fatalf("caller slice was edited: %+v", tombs)
	}
}

// withRetention pins the window for one test rather than leaving the process
// cache holding whatever the developer's config.json says.
func withRetention(t *testing.T, window time.Duration) {
	t.Helper()
	SetTombstoneRetention(window)
	t.Cleanup(ResetTombstoneRetention)
}

func TestWriteExpiresStaleTombstones(t *testing.T) {
	withRetention(t, 24*time.Hour)
	path := filepath.Join(t.TempDir(), "shells.json")
	now := time.Now().UTC()
	writeTestManifest(t, path, manifest{Version: CurrentVersion, Tombstones: []Tombstone{
		{Definition: Definition{TmuxName: "stale", DisplayName: "long gone", Namespace: "/tmp/socket"}, DeletedAt: now.Add(-72 * time.Hour)},
		{Definition: Definition{TmuxName: "recent", DisplayName: "yesterday", Namespace: "/tmp/socket"}, DeletedAt: now.Add(-time.Hour)},
	}})

	// Any write sweeps; this one is unrelated to either tombstone.
	if err := AddAtPath(path, Definition{TmuxName: "live", DisplayName: "Live", Namespace: "/tmp/socket"}); err != nil {
		t.Fatal(err)
	}

	m, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tombstones) != 1 || m.Tombstones[0].TmuxName != "recent" {
		t.Fatalf("tombstones after write = %+v", m.Tombstones)
	}
}

func TestListTombstonesHidesExpiredRecords(t *testing.T) {
	withRetention(t, time.Hour)
	path := filepath.Join(t.TempDir(), "shells.json")
	now := time.Now().UTC()
	writeTestManifest(t, path, manifest{Version: CurrentVersion, Tombstones: []Tombstone{
		{Definition: Definition{TmuxName: "stale", DisplayName: "long gone", Namespace: "/tmp/socket"}, DeletedAt: now.Add(-48 * time.Hour)},
	}})

	tombs, err := ListTombstonesAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tombs) != 0 {
		t.Fatalf("expired tombstone was listed: %+v", tombs)
	}

	// And the restore a caller might still attempt is a clean not-found, not a
	// resurrection of a record past its window.
	if _, err := RestoreAtPath(path, Identity{TmuxName: "stale", Namespace: "/tmp/socket"}); !IsNotFound(err) {
		t.Fatalf("RestoreAtPath on an expired tombstone = %v; want not found", err)
	}
}

// Retention is config-backed, not a constant in a writer: the window a write
// applies is the one config.json asks for.
func TestTombstoneRetentionComesFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"shells":{"tombstoneRetention":"1h"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	config.SetTestConfigPath(cfgPath)
	t.Cleanup(config.ResetTestConfigPath)
	ResetTombstoneRetention()
	t.Cleanup(ResetTombstoneRetention)

	if got := TombstoneRetention(); got != time.Hour {
		t.Fatalf("TombstoneRetention() = %v; want 1h", got)
	}
}
