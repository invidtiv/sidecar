package shellstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"  useful context  ", "useful context", false},
		{"", "", true},
		{strings.Repeat("a", 51), "", true},
		{strings.Repeat("é", 25), strings.Repeat("é", 25), false},
		{strings.Repeat("é", 26), "", true},
		{"line\nbreak", "", true}, {"tab\tname", "", true},
		{"\ttrimmed?", "", true},
		{"escape\x1bname", "", true}, {"c1\u0085name", "", true},
		{string([]byte{0xff}), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeName(tt.name)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("NormalizeName() = %q, %v; want %q, err=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestRenameAtPathPreservesManifestAndNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shells.json")
	created := time.Now().UTC().Truncate(time.Second)
	writeTestManifest(t, path, manifest{Version: 1, Shells: []Definition{
		{TmuxName: "sidecar-sh-one", DisplayName: "old", Namespace: "/tmp/socket", CreatedAt: created, AgentType: "codex", SkipPerms: true},
		{TmuxName: "sidecar-sh-two", DisplayName: "sibling", Namespace: "/tmp/socket", CreatedAt: created},
	}})
	result, err := RenameAtPath(path, RenameRequest{TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket", Name: "  new context "})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.OldName != "old" || result.Name != "new context" {
		t.Fatalf("unexpected result: %+v", result)
	}
	m, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Shells[0].AgentType != "codex" || !m.Shells[0].SkipPerms || !m.Shells[0].CreatedAt.Equal(created) || m.Shells[1].DisplayName != "sibling" {
		t.Fatalf("unrelated fields changed: %+v", m)
	}
	before, _ := os.Stat(path)
	time.Sleep(10 * time.Millisecond)
	result, err = RenameAtPath(path, RenameRequest{TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket", Name: "new context"})
	if err != nil || result.Changed {
		t.Fatalf("no-op = %+v, %v", result, err)
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("no-op rewrote manifest")
	}
}

func TestRenameAtPathRefusals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	writeTestManifest(t, path, manifest{Version: 1, Shells: []Definition{
		{TmuxName: "sidecar-sh-one", DisplayName: "one", Namespace: "/tmp/socket"},
		{TmuxName: "sidecar-sh-two", DisplayName: "two", Namespace: "/tmp/socket"},
	}})
	for name, req := range map[string]RenameRequest{
		"duplicate": {TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket", Name: "two"},
		"namespace": {TmuxName: "sidecar-sh-one", Namespace: "/tmp/other", Name: "new"},
		"legacy":    {TmuxName: "sidecar-sh-one", Namespace: "", Name: "new"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RenameAtPath(path, req); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func TestRenameCurrentRequiresUniqueRegisteredManifest(t *testing.T) {
	state := t.TempDir()
	for _, project := range []string{"one", "two"} {
		dir := filepath.Join(state, "projects", project)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeTestManifest(t, filepath.Join(dir, "shells.json"), manifest{Version: 1, Shells: []Definition{{TmuxName: "sidecar-sh-one", DisplayName: "old", Namespace: "/tmp/socket"}}})
	}
	_, err := RenameCurrent(state, RenameRequest{TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket", Name: "new"})
	if err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("expected ambiguity, got %v", err)
	}
	if err := os.RemoveAll(filepath.Join(state, "projects", "two")); err != nil {
		t.Fatal(err)
	}
	result, err := RenameCurrent(state, RenameRequest{TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket", Name: "new"})
	if err != nil || !result.Changed {
		t.Fatalf("rename = %+v, %v", result, err)
	}
}

func TestLookupCurrentReadsDisplayNameWithoutMutation(t *testing.T) {
	state := t.TempDir()
	dir := filepath.Join(state, "projects", "sidecar")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "shells.json")
	writeTestManifest(t, path, manifest{Version: 1, Shells: []Definition{{
		TmuxName: "sidecar-sh-one", DisplayName: "prior task", Namespace: "/tmp/socket",
	}}})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LookupCurrent(state, Identity{TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Shell != "sidecar-sh-one" || got.Name != "prior task" {
		t.Fatalf("LookupCurrent() = %+v", got)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("LookupCurrent rewrote the manifest")
	}
	_, err = LookupCurrent(state, Identity{TmuxName: "sidecar-sh-missing", Namespace: "/tmp/socket"})
	if err == nil {
		t.Fatal("expected not found for unknown shell")
	}
	var kind *Error
	if !errors.As(err, &kind) || kind.Kind != KindNotFound {
		t.Fatalf("error = %v, want KindNotFound", err)
	}
}

func TestLookupCurrentAmbiguousAcrossProjects(t *testing.T) {
	state := t.TempDir()
	for _, project := range []string{"one", "two"} {
		dir := filepath.Join(state, "projects", project)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeTestManifest(t, filepath.Join(dir, "shells.json"), manifest{Version: 1, Shells: []Definition{{
			TmuxName: "sidecar-sh-one", DisplayName: "shared", Namespace: "/tmp/socket",
		}}})
	}
	_, err := LookupCurrent(state, Identity{TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket"})
	if err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("expected ambiguity, got %v", err)
	}
}

func TestLookupOrigin(t *testing.T) {
	state := t.TempDir()
	p1 := filepath.Join(state, "projects", "p1")
	if err := os.MkdirAll(p1, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestManifest(t, filepath.Join(p1, "shells.json"), manifest{Version: 1, Shells: []Definition{
		{TmuxName: "sidecar-sh-target", DisplayName: "readable name", Namespace: "/tmp/sock", WorkDir: "/work/p1"},
	}})
	if err := os.WriteFile(filepath.Join(p1, "meta.json"), []byte(`{"path":"/work/p1"}`), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := LookupCurrent(state, Identity{TmuxName: "sidecar-sh-target", Namespace: "/tmp/sock"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "readable name" || res.Shell != "sidecar-sh-target" {
		t.Fatalf("unexpected result: %+v", res)
	}

	origin, err := LookupOrigin(state, Identity{TmuxName: "sidecar-sh-target", Namespace: "/tmp/sock"})
	if err != nil {
		t.Fatal(err)
	}
	if origin.DisplayName != "readable name" || origin.ProjectKey != "p1" || origin.WorkDir != "/work/p1" {
		t.Fatalf("unexpected origin: %+v", origin)
	}
}

func TestRenameCurrentMalformedRegisteredManifestFailsClosed(t *testing.T) {
	state := t.TempDir()
	validDir := filepath.Join(state, "projects", "valid")
	brokenDir := filepath.Join(state, "projects", "broken")
	if err := os.MkdirAll(validDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(brokenDir, 0755); err != nil {
		t.Fatal(err)
	}
	validPath := filepath.Join(validDir, "shells.json")
	writeTestManifest(t, validPath, manifest{Version: 1, Shells: []Definition{{
		TmuxName: "sidecar-sh-one", DisplayName: "old", Namespace: "/tmp/socket",
	}}})
	before, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "shells.json"), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = RenameCurrent(state, RenameRequest{TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket", Name: "new"})
	if err == nil {
		t.Fatal("RenameCurrent() succeeded with a malformed registered manifest")
	}
	var stateErr *Error
	if !errors.As(err, &stateErr) || stateErr.Kind != KindState {
		t.Fatalf("error = %T %v, want KindState", err, err)
	}
	after, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("valid matching manifest was mutated before inventory failed closed")
	}
}

func writeTestManifest(t *testing.T, path string, m manifest) {
	t.Helper()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestAddAndRemoveAtPathPreserveSiblingDefinitions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	created := time.Now().UTC().Truncate(time.Second)
	writeTestManifest(t, path, manifest{Version: 1, Shells: []Definition{{TmuxName: "one", DisplayName: "One", Namespace: "/tmp/socket", CreatedAt: created}}})
	def := Definition{TmuxName: "two", DisplayName: "Two", Namespace: "/tmp/socket", AgentType: "codex", WorkDir: "/tmp/project"}
	if err := AddAtPath(path, def); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Shells) != 2 || m.Shells[0].DisplayName != "One" || m.Shells[1].AgentType != "codex" {
		t.Fatalf("manifest after add = %+v", m.Shells)
	}
	if err := RemoveAtPath(path, Identity{TmuxName: "two", Namespace: "/tmp/socket"}); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Shells) != 1 || m.Shells[0].TmuxName != "one" || !m.Shells[0].CreatedAt.Equal(created) {
		t.Fatalf("manifest after remove = %+v", m.Shells)
	}
	if len(m.Tombstones) != 1 || m.Tombstones[0].TmuxName != "two" || m.Tombstones[0].AgentType != "codex" || m.Tombstones[0].DeletedAt.IsZero() {
		t.Fatalf("tombstones after remove = %+v", m.Tombstones)
	}
}

// An auto-close decides a shell is dead, then takes a moment to confirm it. A
// shell can be created under the same tmux name inside that moment, and the
// removal must not take the new entry with it (td-6a4100). The comparison runs
// under the same exclusive lock the creating write takes, so these two
// orderings are the only ones possible.
func TestRemoveIfUnchangedAtPathRefusesAReplacedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	observed := time.Now().UTC().Truncate(time.Second)
	id := Identity{TmuxName: "one", Namespace: "/tmp/socket"}

	// The entry on disk is newer than the one the caller looked at: the shell
	// was recreated while the death was being confirmed.
	writeTestManifest(t, path, manifest{Version: 1, Shells: []Definition{
		{TmuxName: "one", DisplayName: "One", Namespace: "/tmp/socket", CreatedAt: observed.Add(time.Second)},
	}})
	if err := RemoveIfUnchangedAtPath(path, id, observed); !errors.Is(err, ErrShellChanged) {
		t.Fatalf("RemoveIfUnchangedAtPath() error = %v, want ErrShellChanged", err)
	}
	m, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Shells) != 1 {
		t.Fatal("a shell recreated during confirmation lost its manifest entry")
	}

	// The entry is the one that was observed, so the removal proceeds.
	writeTestManifest(t, path, manifest{Version: 1, Shells: []Definition{
		{TmuxName: "one", DisplayName: "One", Namespace: "/tmp/socket", CreatedAt: observed},
	}})
	if err := RemoveIfUnchangedAtPath(path, id, observed); err != nil {
		t.Fatalf("RemoveIfUnchangedAtPath() error = %v", err)
	}
	if m, err = readManifest(path); err != nil {
		t.Fatal(err)
	}
	if len(m.Shells) != 0 {
		t.Fatalf("manifest after remove = %+v", m.Shells)
	}
	if len(m.Tombstones) != 1 || m.Tombstones[0].TmuxName != "one" {
		t.Fatalf("tombstones after remove = %+v", m.Tombstones)
	}
}

// A caller with no incarnation to check gets the unconditional behaviour.
func TestRemoveIfUnchangedAtPathWithZeroTimeRemovesUnconditionally(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	writeTestManifest(t, path, manifest{Version: 1, Shells: []Definition{
		{TmuxName: "one", Namespace: "/tmp/socket", CreatedAt: time.Now().Add(time.Hour)},
	}})
	if err := RemoveIfUnchangedAtPath(path, Identity{TmuxName: "one", Namespace: "/tmp/socket"}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Shells) != 0 {
		t.Fatalf("manifest after unconditional remove = %+v", m.Shells)
	}
	if len(m.Tombstones) != 1 || m.Tombstones[0].TmuxName != "one" {
		t.Fatalf("tombstones after unconditional remove = %+v", m.Tombstones)
	}
}

func TestTombstoneMoveAndRestoreAtPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	created := time.Now().UTC().Truncate(time.Second)
	live := Definition{
		TmuxName: "sidecar-sh-one", DisplayName: "prior task", Namespace: "/tmp/socket",
		CreatedAt: created, AgentType: "codex", SkipPerms: true, WorkDir: "/work/one",
	}
	sibling := Definition{TmuxName: "sidecar-sh-two", DisplayName: "sibling", Namespace: "/tmp/socket", CreatedAt: created}
	writeTestManifest(t, path, manifest{Version: 1, Shells: []Definition{live, sibling}})

	if err := RemoveAtPath(path, Identity{TmuxName: live.TmuxName, Namespace: live.Namespace}); err != nil {
		t.Fatal(err)
	}
	tombs, err := ListTombstonesAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tombs) != 1 || tombs[0].DisplayName != "prior task" || tombs[0].AgentType != "codex" || !tombs[0].SkipPerms || tombs[0].WorkDir != "/work/one" {
		t.Fatalf("tombstone = %+v", tombs)
	}

	got, err := RestoreAtPath(path, Identity{TmuxName: live.TmuxName, Namespace: live.Namespace})
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != live.DisplayName || got.AgentType != live.AgentType || got.SkipPerms != live.SkipPerms || got.WorkDir != live.WorkDir || !got.CreatedAt.Equal(created) {
		t.Fatalf("restored = %+v", got)
	}
	m, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tombstones) != 0 {
		t.Fatalf("tombstones after restore = %+v", m.Tombstones)
	}
	if len(m.Shells) != 2 {
		t.Fatalf("shells after restore = %+v", m.Shells)
	}
	var found Definition
	for _, s := range m.Shells {
		if s.TmuxName == live.TmuxName {
			found = s
		}
	}
	if found.DisplayName != "prior task" || found.AgentType != "codex" || !found.SkipPerms || found.WorkDir != "/work/one" {
		t.Fatalf("live after restore = %+v", found)
	}
	if m.Shells[0].TmuxName != "sidecar-sh-two" && m.Shells[1].TmuxName != "sidecar-sh-two" {
		t.Fatalf("sibling lost: %+v", m.Shells)
	}
}

func TestRestoreAtPathAlreadyLiveAndUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	writeTestManifest(t, path, manifest{Version: 1, Shells: []Definition{
		{TmuxName: "sidecar-sh-one", DisplayName: "one", Namespace: "/tmp/socket"},
	}})

	_, err := RestoreAtPath(path, Identity{TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket"})
	if !IsAlready(err) {
		t.Fatalf("live restore error = %v, want KindAlready", err)
	}
	_, err = RestoreAtPath(path, Identity{TmuxName: "sidecar-sh-missing", Namespace: "/tmp/socket"})
	if !IsNotFound(err) {
		t.Fatalf("unknown restore error = %v, want KindNotFound", err)
	}
}

func TestAddAtPathDropsMatchingTombstone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shells.json")
	def := Definition{TmuxName: "one", DisplayName: "One", Namespace: "/tmp/socket", AgentType: "claude"}
	writeTestManifest(t, path, manifest{Version: 1, Tombstones: []Tombstone{
		{Definition: def, DeletedAt: time.Now().UTC()},
	}})
	if err := AddAtPath(path, def); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Shells) != 1 || m.Shells[0].AgentType != "claude" {
		t.Fatalf("shells after add = %+v", m.Shells)
	}
	if len(m.Tombstones) != 0 {
		t.Fatalf("tombstone survived add: %+v", m.Tombstones)
	}
}
