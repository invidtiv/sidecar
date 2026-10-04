package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDestinationCarriesOnlyVerifiedMatchingPane(t *testing.T) {
	home, socket := setupShellCLI(t, "attachment proof")
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%7")
	stateDir := filepath.Join(home, "sidecar")
	dest, err := resolveOpenDestination(t.Context(), stateDir, "sidecar-sh-sidecar-1", "sidecar", resolveProjectOnly)
	if err != nil || dest.Origin.TmuxPane != "%7" {
		t.Fatalf("matching live caller: %+v %v", dest, err)
	}
	// The same session on another socket is not evidence for this destination.
	tmux := filepath.Join(filepath.SplitList(os.Getenv("PATH"))[0], "tmux")
	script := "#!/bin/sh\nprintf 'sidecar-sh-sidecar-1\\t/other/socket\\n'\n"
	if err := os.WriteFile(tmux, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	dest, err = resolveOpenDestination(t.Context(), stateDir, "sidecar-sh-sidecar-1", "sidecar", resolveProjectOnly)
	if err != nil || dest.Origin.TmuxPane != "" {
		t.Fatalf("foreign socket leaked pane: %+v %v", dest, err)
	}
	script = "#!/bin/sh\nprintf 'sidecar-sh-other-1\\t%s\\n' " + shellQuote(socket) + "\n"
	if err := os.WriteFile(tmux, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	dest, err = resolveOpenDestination(t.Context(), stateDir, "sidecar-sh-sidecar-1", "sidecar", resolveProjectOnly)
	if err != nil || dest.Origin.TmuxPane != "" {
		t.Fatalf("foreign session leaked pane: %+v %v", dest, err)
	}
	t.Setenv("TMUX_PANE", "")
	dest, err = resolveOpenDestination(t.Context(), stateDir, "sidecar-sh-sidecar-1", "sidecar", resolveProjectOnly)
	if err != nil || dest.Origin.TmuxPane != "" {
		t.Fatalf("missing caller pane: %+v %v", dest, err)
	}
}

func TestExplicitShellUsesDurableWorkspaceOutsideCallerCheckout(t *testing.T) {
	home, _ := setupShellCLI(t, "linked shell")
	root, linked := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(linked, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "sidecar", "projects", "sidecar")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"path":`+quoteJSON(t, root)+`}`), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"shells":[{"tmuxName":"sidecar-sh-sidecar-1","workDir":` + quoteJSON(t, filepath.Join(linked, "src")) + `}]}`
	if err := os.WriteFile(filepath.Join(dir, "shells.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	git := filepath.Join(filepath.SplitList(os.Getenv("PATH"))[0], "git")
	script := "#!/bin/sh\nprintf 'worktree %s\\n\\nworktree %s\\n' " + shellQuote(root) + " " + shellQuote(linked) + "\n"
	if err := os.WriteFile(git, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	dest, err := resolveExplicitDestination(filepath.Join(home, "sidecar"), "sidecar-sh-sidecar-1", "sidecar", resolveProjectOnly)
	if err != nil || dest.Origin.WorkDir != canonicalOpenPath(linked) {
		t.Fatalf("explicit shell: %+v %v", dest, err)
	}
	if got := resolveTargetWorkDirForDest(filepath.Join(home, "sidecar"), dest, "README.md"); got != dest.Origin.WorkDir {
		t.Fatalf("open changed shell workspace to %s", got)
	}
}
