package gitstatus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
)

func TestInitWithHostIDDoesNotOpenLocalTree(t *testing.T) {
	root := t.TempDir()
	run := exec.Command("git", "init")
	run.Dir = root
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New()
	ctx := &plugin.Context{WorkDir: root, ProjectRoot: root, HostID: "aerie"}
	if err := p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if p.tree != nil || p.hasRepo {
		t.Fatalf("opened local repo: tree=%v hasRepo=%v", p.tree != nil, p.hasRepo)
	}
	if cmd := p.Start(); cmd != nil {
		t.Fatal("Start probed the twin git root")
	}
	view := p.View(80, 20)
	if !strings.Contains(view, "aerie") {
		t.Errorf("view = %q, want the host named", view)
	}
	if p.inNoRepoMode() {
		t.Fatal("remote bind must not present the local no-repo init path")
	}
}
