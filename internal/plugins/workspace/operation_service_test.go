package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"

	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/workspaceops"
)

func TestServiceShellCreationPreservesLoadedManifestRecovery(t *testing.T) {
	if !workspaceops.TmuxInstalled() {
		t.Skip("tmux unavailable")
	}
	root := t.TempDir()
	path := filepath.Join(root, "shells.json")
	if err := os.WriteFile(path, []byte("corrupt manifest"), 0644); err != nil {
		t.Fatal(err)
	}
	manifest := &ShellManifest{path: path, Version: manifestVersion, Shells: []ShellDefinition{{TmuxName: "existing-cached-shell", DisplayName: "Existing", Namespace: tmuxenv.Namespace()}}}
	svc := (&Plugin{shellManifest: manifest}).shellOperationService()
	spec := workspaceops.ManagedShellSpec{ShellSpec: workspaceops.ShellSpec{SessionName: "sidecar-sh-recovery-proof", DisplayName: "Recovered", WorkDir: root}, ProjectRoot: root}
	if _, err := svc.CreateShell(spec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.DeleteShell(root, spec.SessionName, tmuxenv.Namespace()) })
	loaded, err := LoadShellManifest(path)
	if err != nil || len(loaded.Shells) != 2 || loaded.FindShell(spec.SessionName) == nil || loaded.FindShell("existing-cached-shell") == nil {
		t.Fatalf("recovery lost cached or created shell: %+v err=%v", loaded, err)
	}
	before := manifest.Revision()
	if err := svc.DeleteShell(root, spec.SessionName, tmuxenv.Namespace()); err != nil {
		t.Fatal(err)
	}
	if manifest.Revision() <= before {
		t.Fatal("service delete did not fence the loaded projection")
	}
	if workspaceops.SessionExists(spec.SessionName) {
		t.Fatal("service delete left its session running")
	}
	loaded, err = LoadShellManifest(path)
	if err != nil || len(loaded.Shells) != 1 || loaded.FindShell("existing-cached-shell") == nil {
		t.Fatalf("delete affected another record: %+v err=%v", loaded, err)
	}
}

// Delay command execution across the same context/manifest replacement used by
// Init. The operation must retain the adapter of the project that requested it.
func TestDelayedShellOperationsRetainProjectAdapter(t *testing.T) {
	if !workspaceops.TmuxInstalled() {
		t.Skip("tmux unavailable")
	}
	original, next := t.TempDir(), t.TempDir()
	originalManifest, err := LoadShellManifest(filepath.Join(original, "shells.json"))
	if err != nil {
		t.Fatal(err)
	}
	nextManifest, err := LoadShellManifest(filepath.Join(next, "shells.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := &Plugin{ctx: &plugin.Context{Epoch: 1, ProjectRoot: original, WorkDir: original, Config: config.Default()}, shellManifest: originalManifest}
	create := p.createShell(shellCreateOpts{CustomName: "Original project"})
	p.ctx = &plugin.Context{Epoch: 2, ProjectRoot: next, WorkDir: next, Config: config.Default()}
	p.shellManifest = nextManifest
	msg, ok := create().(ShellCreatedMsg)
	if !ok || msg.Err != nil {
		t.Fatalf("create: %#v", msg)
	}
	svc := workspaceops.Service{Shells: originalManifest}
	t.Cleanup(func() { _ = svc.DeleteShell(original, msg.SessionName, tmuxenv.Namespace()) })
	if originalManifest.FindShell(msg.SessionName) == nil || nextManifest.FindShell(msg.SessionName) != nil {
		t.Fatal("delayed create wrote the new project's manifest")
	}
	p.ctx = &plugin.Context{Epoch: 3, ProjectRoot: original, WorkDir: original, Config: config.Default()}
	p.shellManifest = originalManifest
	kill := p.killShellSessionByName(msg.SessionName)
	p.ctx = &plugin.Context{Epoch: 4, ProjectRoot: next, WorkDir: next, Config: config.Default()}
	p.shellManifest = nextManifest
	if result := kill(); result != (ShellKilledMsg{SessionName: msg.SessionName}) {
		t.Fatalf("delete: %#v", result)
	}
	if originalManifest.FindShell(msg.SessionName) != nil {
		t.Fatal("delayed delete left the original record")
	}
	if _, err := os.Stat(nextManifest.path); !os.IsNotExist(err) {
		t.Fatalf("delayed operation wrote the next manifest: %v", err)
	}
}
