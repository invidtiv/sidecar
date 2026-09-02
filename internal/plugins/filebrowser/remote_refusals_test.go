package filebrowser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
)

func pressKey(t *testing.T, p *Plugin, key string) tea.Cmd {
	t.Helper()
	updated, cmd := p.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
	if updated == nil {
		t.Fatal("Update dropped the plugin")
	}
	return cmd
}

func flashText(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		return ""
	}
	flash, ok := cmd().(msg.FlashMsg)
	if !ok {
		return ""
	}
	return flash.Text
}

// Every write gesture must say which machine it is refusing for. A silent
// no-op and a write to a same-named path on this disk look identical to the
// user right up until the wrong file is gone.
func TestBoundWritesRefuseByName(t *testing.T) {
	for key, want := range map[string]string{
		"R": "renaming",
		"m": "moving",
		"a": "creating a file",
		"A": "creating a directory",
		"D": "deleting",
		"p": "pasting",
		"e": "editing",
		"E": "opening an external editor",
	} {
		t.Run(key, func(t *testing.T) {
			p, twin := boundFilesPlugin(t, &recordingTreeSource{dirs: hostFixtureDirs()})
			applyBuild(t, p, p.Start())

			text := flashText(t, pressKey(t, p, key))
			if !strings.Contains(text, want) || !strings.Contains(text, "aerie") {
				t.Fatalf("%q flashed %q, want %q and the host named", key, text, want)
			}
			if p.fileOpMode != FileOpNone {
				t.Fatalf("%q opened file-operation mode %v while bound", key, p.fileOpMode)
			}
			// Nothing on either machine moved.
			entries, err := os.ReadDir(twin)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != localTwinMarker {
				t.Fatalf("%q changed this machine's disk: %v", key, entries)
			}
		})
	}
}

// Blame, file info, project search, and reveal-in-file-manager are reads, but
// no host verb answers them and every one of them would otherwise run a local
// process against an empty working directory.
func TestBoundReadsWithNoHostVerbRefuseByName(t *testing.T) {
	for key, want := range map[string]string{
		"B":      "git blame",
		"I":      "file info",
		"f":      "project search",
		"ctrl+r": "revealing in a file manager",
	} {
		t.Run(key, func(t *testing.T) {
			p, _ := boundFilesPlugin(t, &recordingTreeSource{dirs: hostFixtureDirs()})
			applyBuild(t, p, p.Start())

			// ctrl+r arrives as a modified key; drive the refusal table directly
			// for it and the plain keys through Update.
			var text string
			if strings.HasPrefix(key, "ctrl+") {
				what, refused := p.remoteRefusal(key)
				if !refused {
					t.Fatalf("%q is not refused while bound", key)
				}
				text = flashText(t, p.refuseRemoteKey(what))
			} else {
				text = flashText(t, pressKey(t, p, key))
			}
			if !strings.Contains(text, want) || !strings.Contains(text, "aerie") {
				t.Fatalf("%q flashed %q, want %q and the host named", key, text, want)
			}
			if p.blameMode || p.infoMode || p.projectSearchMode {
				t.Fatalf("%q entered a mode with no host verb behind it", key)
			}
		})
	}
}

func TestBoundNavigationStillWorks(t *testing.T) {
	p, _ := boundFilesPlugin(t, &recordingTreeSource{dirs: hostFixtureDirs()})
	applyBuild(t, p, p.Start())
	before := p.treeCursor

	_ = pressKey(t, p, "j")
	if p.treeCursor == before {
		t.Fatal("the refusal table swallowed ordinary navigation")
	}
	if _, refused := p.remoteRefusal("r"); refused {
		t.Fatal("refresh is refused while bound; it is the tree's only manual update")
	}
}

func TestBoundDragIsNeverArmed(t *testing.T) {
	p, _ := boundFilesPlugin(t, &recordingTreeSource{dirs: hostFixtureDirs()})
	applyBuild(t, p, p.Start())

	if node := p.draggableNode(0); node != nil {
		t.Fatalf("a bound tree armed a drag on %q; drag-to-move is a write", node.Path)
	}
}

func TestBoundCommandsAreTheReachableSubset(t *testing.T) {
	p, _ := boundFilesPlugin(t, &recordingTreeSource{dirs: hostFixtureDirs()})
	applyBuild(t, p, p.Start())

	got := map[string]bool{}
	for _, cmd := range p.Commands() {
		got[cmd.ID] = true
	}
	if len(got) == 0 {
		t.Fatal("a bound Files offered no commands at all; the footer should say what it can do")
	}
	for _, id := range []string{"edit", "edit-external", "blame", "project-search", "info"} {
		if got[id] {
			t.Fatalf("footer offered %q, which answers unavailable on [aerie]", id)
		}
	}
	if !got["quick-open"] {
		t.Fatalf("footer dropped find-by-name, which does work while bound: %v", got)
	}
}

// The host's snapshot is the only change signal that crosses the boundary.
func TestHostInventoryRefreshesABoundTree(t *testing.T) {
	src := &recordingTreeSource{dirs: hostFixtureDirs()}
	p, _ := boundFilesPlugin(t, src)
	applyBuild(t, p, p.Start())
	src.calls = nil

	_, cmd := p.Update(plugin.HostInventoryMsg{})
	if cmd == nil {
		t.Fatal("a host snapshot did not refresh the bound tree")
	}
	applyBuild(t, p, cmd)
	if len(src.calls) == 0 {
		t.Fatal("the refresh did not list the host")
	}
	if !containsPath(treePaths(p), remoteMarker) {
		t.Fatalf("refreshed tree = %v", treePaths(p))
	}
}

func TestLocalFilesStillWritesAndSearches(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: root, ProjectRoot: root}); err != nil {
		t.Fatal(err)
	}
	if _, refused := p.remoteRefusal("D"); refused {
		t.Fatal("a local project refused a write")
	}
	if len(p.Commands()) <= len(remoteCommandIDs) {
		t.Fatal("a local project lost commands to the bound subset")
	}
}
