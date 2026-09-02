package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestInitWithHostIDDoesNotOpenLocalStore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".todos"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := New()
	ctx := &plugin.Context{WorkDir: root, ProjectRoot: root, HostID: "marcusbook"}
	if err := p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if p.store != nil {
		t.Fatal("Init opened a notes store against the twin root")
	}
	if p.notes != nil {
		t.Fatalf("notes = %+v, want none", p.notes)
	}
	if cmd := p.Start(); cmd != nil {
		t.Fatal("Start loaded notes from the twin root")
	}
	view := p.View(80, 20)
	if !strings.Contains(view, "marcusbook") {
		t.Errorf("view = %q, want the host named", view)
	}
	if strings.Contains(view, "No notes") || strings.Contains(strings.ToLower(view), "untitled") {
		t.Errorf("view still looks like a local notes list: %q", view)
	}
}

func TestUpdateAndFocusContextWithHostIDDoNotPanic(t *testing.T) {
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: t.TempDir(), HostID: "marcusbook"}); err != nil {
		t.Fatal(err)
	}
	_, cmd := p.Update(app.PluginFocusedMsg{})
	if cmd != nil {
		t.Fatalf("PluginFocusedMsg returned %v", cmd)
	}
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if got := p.FocusContext(); got != "notes" {
		t.Fatalf("FocusContext() = %q", got)
	}
	if cmds := p.Commands(); len(cmds) != 0 {
		t.Fatalf("Commands() = %#v, want none", cmds)
	}
}
