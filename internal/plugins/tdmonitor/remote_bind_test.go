package tdmonitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
)

func TestInitWithHostIDDoesNotStatTodos(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".todos"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New()
	ctx := &plugin.Context{WorkDir: root, ProjectRoot: root, HostID: "aerie"}
	if err := p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if p.todosConflict {
		t.Fatal("Init stated the twin .todos path")
	}
	if cmd := p.Start(); cmd != nil {
		t.Fatal("Start opened a td store on the twin root")
	}
	view := p.View(80, 20)
	if !strings.Contains(view, "aerie") {
		t.Errorf("view = %q, want the host named", view)
	}
}
