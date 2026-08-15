package workspace

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/filefind"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/filebrowser"
)

// The file finder has two hosts now: the Files plugin's full-screen quick open
// and a workspace document pane. They share internal/filefind, so this is not a
// test that the two render the same pixels — they cannot, and should not: the
// pane's modal is scoped to a smaller box, reserves a header row above itself,
// and elides paths at whatever width that box leaves. What must hold is what
// the user experiences as "the same finder":
//
//   - the same root and query produce the same matches in the same order,
//   - the arrow keys move the selection the same way, and
//   - the same keys end with the same file open.
//
// Rendering is compared as the ordered list of paths each host's modal is
// showing, which survives the difference in box while still failing if one host
// filters, reorders, or truncates the list differently.
//
// The Files plugin is driven only through its public plugin surface (Init,
// Update, View), which is the point: if this test could reach inside it, it
// would be asserting against the same code the pane runs rather than against
// the host.

// Every fixture has a distinct file name, because a row elided to fit a narrow
// pane keeps its name and little else: two files called view.go would leave the
// comparisons below unable to tell which row a host is showing.
func parityFixtureFiles() []string {
	return []string{
		"README.md",
		"cmd/main.go",
		"docs/guide.md",
		"internal/app/view.go",
		"internal/plugins/filebrowser/browser.go",
	}
}

// filesPluginHost boots the Files plugin over root, with no view cache and no
// watcher of its own.
func filesPluginHost(t *testing.T, root string) *filebrowser.Plugin {
	t.Helper()
	registry := keymap.NewRegistry()
	keymap.RegisterDefaults(registry)
	p := filebrowser.New()
	if err := p.Init(&plugin.Context{
		WorkDir:     root,
		ProjectRoot: root,
		Config:      config.Default(),
		Keymap:      registry,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Epoch:       1,
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

// send drives the Files plugin the way the app does, running whatever command
// each message produces until the traffic settles. Only the finder's own
// messages are fed back; anything else (preview loads, watcher arming) is left
// alone, which keeps the test off the disk beyond the fixture.
func sendToFiles(t *testing.T, p *filebrowser.Plugin, msg tea.Msg) {
	t.Helper()
	_, cmd := p.Update(msg)
	for range 8 {
		if cmd == nil {
			return
		}
		out := cmd()
		cmd = nil
		switch m := out.(type) {
		case filefind.ScannedMsg:
			_, cmd = p.Update(m)
		case tea.BatchMsg:
			for _, child := range m {
				if child == nil {
					continue
				}
				if scan, ok := child().(filefind.ScannedMsg); ok {
					_, cmd = p.Update(scan)
				}
			}
		}
	}
}

// modalPaths reads the ordered fixture paths out of a rendered surface. The two
// hosts draw at different widths, so a row may be elided; a path is counted as
// shown when its file name survives, which is what the reader picks the row by.
func modalPaths(rendered string, files []string) []string {
	var shown []string
	for _, row := range strings.Split(ansi.Strip(rendered), "\n") {
		for _, path := range files {
			base := path
			if idx := strings.LastIndex(path, "/"); idx >= 0 {
				base = path[idx+1:]
			}
			if strings.Contains(row, base) {
				shown = append(shown, path)
				break
			}
		}
	}
	return shown
}

func TestFinderBehavesTheSameInBothHosts(t *testing.T) {
	// One root, one fixture set, both hosts.
	root := t.TempDir()
	for _, path := range parityFixtureFiles() {
		writeDocPaneFixture(t, root, path, "package fixture\n")
	}

	files := filesPluginHost(t, root)
	sendToFiles(t, files, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})

	pane := docPaneTestPlugin(t, root, true)
	applyDocOpen(t, pane, pane.openTerminalPath("README.md", 0))
	doc := pane.focusedDocPane()
	if doc == nil {
		t.Fatal("no document pane to search from")
	}
	scanFinder(t, pane, pane.openDocFinder(doc))
	composePaneTree(t, pane, 140, 36)

	for _, query := range []string{"view", "g", "zzzz"} {
		for _, r := range query {
			sendToFiles(t, files, tea.KeyPressMsg{Code: r, Text: string(r)})
			pane.handleDocSearchKey(doc, tea.KeyPressMsg{Code: r, Text: string(r)})
		}

		filesShown := modalPaths(files.View(120, 40), parityFixtureFiles())
		paneShown := modalPaths(strings.Join(composePaneTree(t, pane, 140, 36), "\n"), parityFixtureFiles())
		if strings.Join(filesShown, ",") != strings.Join(paneShown, ",") {
			t.Fatalf("query %q: Files shows %v, the pane shows %v", query, filesShown, paneShown)
		}
		if query != "zzzz" && len(paneShown) == 0 {
			t.Fatalf("query %q matched nothing in either host; the case proves nothing", query)
		}

		// Back to an empty query for the next case, one backspace at a time.
		for range query {
			sendToFiles(t, files, tea.KeyPressMsg{Code: tea.KeyBackspace})
			pane.handleDocSearchKey(doc, tea.KeyPressMsg{Code: tea.KeyBackspace})
		}
	}

	// The selection moves the same way, and lands on the same file. The query
	// is one that matches several files, so the arrow keys have somewhere to go
	// — a single-match query would agree in both hosts no matter what the keys
	// did.
	for _, r := range "e" {
		sendToFiles(t, files, tea.KeyPressMsg{Code: r, Text: string(r)})
		pane.handleDocSearchKey(doc, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := len(doc.mode.finder.Matches()); got < 3 {
		t.Fatalf("the navigation query matched %d files; it needs several", got)
	}
	for range 2 {
		sendToFiles(t, files, tea.KeyPressMsg{Code: tea.KeyDown})
		pane.handleDocSearchKey(doc, tea.KeyPressMsg{Code: tea.KeyDown})
	}

	filesSelected := selectedRowPath(t, files.View(120, 40), parityFixtureFiles())
	paneSelected := selectedRowPath(t, strings.Join(composePaneTree(t, pane, 140, 36), "\n"), parityFixtureFiles())
	if filesSelected != paneSelected {
		t.Fatalf("after two downs, Files has %q selected and the pane has %q", filesSelected, paneSelected)
	}

	// And enter opens that same file in each host's own terms: a preview tab in
	// the Files plugin, a document tab in the pane.
	cmd := pane.handleDocSearchKey(doc, tea.KeyPressMsg{Code: tea.KeyEnter})
	applyDocOpen(t, pane, cmd)
	if doc.view() == nil || doc.view().Title() != paneSelected {
		t.Fatalf("the pane opened %#v, want %q", doc.view(), paneSelected)
	}

	sendToFiles(t, files, tea.KeyPressMsg{Code: tea.KeyEnter})
	rendered := ansi.Strip(files.View(120, 40))
	if strings.Contains(rendered, "Quick Open") {
		t.Fatal("enter left the Files plugin's quick open up")
	}
	base := filesSelected
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	if !strings.Contains(rendered, base) {
		t.Fatalf("the Files plugin does not show %q after opening it:\n%s", base, rendered)
	}
}

// selectedRowPath returns the fixture path on the surface's selected row, which
// both surfaces mark with the same cursor glyph.
func selectedRowPath(t *testing.T, rendered string, files []string) string {
	t.Helper()
	for _, row := range strings.Split(ansi.Strip(rendered), "\n") {
		if !strings.Contains(row, "> ") {
			continue
		}
		for _, path := range files {
			base := path
			if idx := strings.LastIndex(path, "/"); idx >= 0 {
				base = path[idx+1:]
			}
			if strings.Contains(row, base) {
				return path
			}
		}
	}
	t.Fatalf("no selected row in:\n%s", rendered)
	return ""
}
