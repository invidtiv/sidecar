package filebrowser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
)

func TestInitWithHostIDDoesNotWalkWorkDir(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "would-be-wrong.txt")
	if err := os.WriteFile(marker, []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New()
	ctx := &plugin.Context{WorkDir: root, ProjectRoot: root, HostID: "aerie"}
	if err := p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if p.tree != nil {
		t.Fatalf("tree = %+v, want nil so Files does not open the twin path", p.tree)
	}
	if cmd := p.Start(); cmd != nil {
		t.Fatal("Start walked the remote root")
	}
	view := p.View(80, 20)
	if !strings.Contains(view, "aerie") {
		t.Errorf("view = %q, want the host named", view)
	}
}
