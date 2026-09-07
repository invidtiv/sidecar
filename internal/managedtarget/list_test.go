package managedtarget

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/shellstate"
)

func TestListReturnsShellsForOneProjectAndAll(t *testing.T) {
	stateDir := t.TempDir()
	writeProject(t, stateDir, "demo", "/repos/demo",
		shellstate.Definition{TmuxName: "sidecar-sh-demo-1", DisplayName: "one", Namespace: "n", WorkDir: "/repos/demo"},
		shellstate.Definition{TmuxName: "sidecar-sh-demo-2", DisplayName: "two", Namespace: "n", WorkDir: "/repos/demo"},
	)
	writeProject(t, stateDir, "other", "/repos/other",
		shellstate.Definition{TmuxName: "sidecar-sh-other-1", DisplayName: "other-one", Namespace: "n", WorkDir: "/repos/other"},
	)

	one, err := List(context.Background(), stateDir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	shells := 0
	for _, c := range one {
		if c.Project != "demo" {
			t.Fatalf("out-of-scope candidate: %+v", c)
		}
		if c.Kind == KindShell {
			shells++
		}
	}
	if shells != 2 {
		t.Fatalf("project shells = %d in %+v, want 2", shells, one)
	}

	all, err := List(context.Background(), stateDir, "")
	if err != nil {
		t.Fatal(err)
	}
	shells = 0
	projects := map[string]bool{}
	for _, c := range all {
		projects[c.Project] = true
		if c.Kind == KindShell {
			shells++
		}
	}
	if shells != 3 || !projects["demo"] || !projects["other"] {
		t.Fatalf("all list = %+v, want 3 shells across demo and other", all)
	}
}

func TestListUnknownProjectIsEmpty(t *testing.T) {
	stateDir := t.TempDir()
	writeProject(t, stateDir, "demo", "/repos/demo")
	got, err := List(context.Background(), stateDir, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("missing project = %+v, want empty", got)
	}
}

func writeProject(t *testing.T, stateDir, key, path string, shells ...shellstate.Definition) {
	t.Helper()
	dir := filepath.Join(stateDir, "projects", key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	if len(shells) == 0 {
		return
	}
	payload, err := json.Marshal(map[string]any{"shells": shells})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shells.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
}
