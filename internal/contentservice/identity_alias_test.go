package contentservice

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
)

func TestShellWorkspaceLookupThroughRegisteredAlias(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	dir, err := projectdir.Resolve(alias)
	if err != nil {
		t.Fatal(err)
	}
	if err = shellstate.AddAtPath(filepath.Join(dir, "shells.json"), shellstate.Definition{TmuxName: "alias-shell", DisplayName: "Alias shell", WorkDir: alias}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "readme.md"), []byte("alias root\n"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := testService(t, alias, nil, nil)
	svc.ListShells = nil // Exercise the actual durable registry, not an adapter.
	id := canonical(root) + ":shell:alias-shell"
	ws, err := svc.LookupWorkspace(t.Context(), id)
	if err != nil || ws.Root != canonical(root) {
		t.Fatalf("catalog-issued canonical shell ID: %+v %v", ws, err)
	}
	ws, err = svc.LookupProject(t.Context(), "demo", id)
	if err != nil || ws.Root != canonical(root) {
		t.Fatalf("project shell alias: %+v %v", ws, err)
	}
	read, err := svc.ReadProject(t.Context(), "demo", id, ReadParams{Kind: KindFile, Operation: OpDocument, Target: "readme.md"})
	if err != nil || read.Content != "alias root\n" {
		t.Fatalf("alias content: %+v %v", read, err)
	}
	// Legacy duplicate registrations must not silently select another shell.
	other := filepath.Join(filepath.Dir(dir), "legacy-duplicate")
	if err = os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(other, "meta.json"), []byte(`{"path":`+`"`+canonical(root)+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = shellstate.AddAtPath(filepath.Join(other, "shells.json"), shellstate.Definition{TmuxName: "alias-shell", DisplayName: "Duplicate"}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.LookupWorkspace(t.Context(), id); !IsRejected(err) {
		t.Fatal("duplicate alias ownership was accepted")
	}
	if err = shellstate.AddAtPath(filepath.Join(dir, "shells.json"), shellstate.Definition{TmuxName: "unique-shell", DisplayName: "Unique"}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.LookupWorkspace(t.Context(), canonical(root)+":shell:unique-shell"); err != nil {
		t.Fatalf("unrelated duplicate blocked a unique shell: %v", err)
	}
}
