package tdmonitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
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

func TestUpdateWithHostIDDoesNotWalkOrPanic(t *testing.T) {
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: t.TempDir(), HostID: "aerie"}); err != nil {
		t.Fatal(err)
	}
	_, cmd := p.Update(app.PluginFocusedMsg{})
	if cmd != nil {
		t.Fatalf("PluginFocusedMsg returned %v", cmd)
	}
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
}
