package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
)

func TestInitWithHostIDDoesNotOpenStore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tasks.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New()
	ctx := &plugin.Context{WorkDir: root, ProjectRoot: root, HostID: "aerie"}
	if err := p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if p.model != nil {
		t.Fatal("Init built a Tasks model against the twin root")
	}
	if p.unavailable == "" || !strings.Contains(p.unavailable, "aerie") {
		t.Fatalf("unavailable = %q, want host named", p.unavailable)
	}
	if cmd := p.Start(); cmd != nil {
		t.Fatal("Start opened the twin Tasks store")
	}
	view := p.View(80, 20)
	if !strings.Contains(view, "aerie") {
		t.Errorf("view = %q, want the host named", view)
	}
}
