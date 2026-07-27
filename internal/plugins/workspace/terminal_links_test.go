package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/filebrowser"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

func TestDetectTerminalLinksFindsSafeURLAndPathLine(t *testing.T) {
	line := "see https://example.com/docs?q=1, then internal/foo.go:123"
	links := detectTerminalLinks(line)
	if len(links) != 2 {
		t.Fatalf("links = %#v, want URL and path", links)
	}
	if links[0].Kind != terminalURLLink || links[0].Value != "https://example.com/docs?q=1" {
		t.Fatalf("URL link = %#v", links[0])
	}
	if links[1].Kind != terminalPathLink || links[1].Value != "internal/foo.go" || links[1].Line != 123 {
		t.Fatalf("path link = %#v", links[1])
	}
}

func TestSafeHTTPURLRejectsNonHTTPAndControls(t *testing.T) {
	for _, value := range []string{
		"javascript:alert(1)",
		"file:///etc/passwd",
		"https://example.com/\x1b]8;;evil",
		"https:///missing-host",
	} {
		if _, ok := safeHTTPURL(value); ok {
			t.Fatalf("unsafe URL accepted: %q", value)
		}
	}
	if openInBrowser("file:///etc/passwd") != nil {
		t.Fatal("browser command accepted non-http URL")
	}
}

func TestDecorateTerminalLinksSynthesizesOnlyValidatedOSC8(t *testing.T) {
	got := decorateTerminalLinks("visit https://example.com/x")
	if !strings.Contains(got, "\x1b]8;;https://example.com/x\x1b\\") {
		t.Fatalf("validated URL did not receive OSC-8: %q", got)
	}
	if ansi.StringWidth(got) != len("visit https://example.com/x") {
		t.Fatalf("link decoration changed visual width: %d", ansi.StringWidth(got))
	}

	source := "\x1b]8;;javascript:alert(1)\x1b\\label\x1b]8;;\x1b\\"
	cleaned := decorateTerminalLinks(source)
	if strings.Contains(cleaned, "javascript:") || strings.Contains(cleaned, "\x1b]8;;") {
		t.Fatalf("source-supplied OSC-8 survived sanitization: %q", cleaned)
	}
	if ansi.Strip(cleaned) != "label" {
		t.Fatalf("OSC-8 sanitization lost label: %q", cleaned)
	}
}

func TestResolveTerminalPathStaysInsideWorkspaceAndRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "internal", "foo.go")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("package internal"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, absolute, ok := resolveTerminalPath(base, "internal/foo.go")
	insideResolved, err := filepath.EvalSymlinks(inside)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rel != "internal/foo.go" || absolute != insideResolved {
		t.Fatalf("inside resolution = rel %q absolute %q ok=%v", rel, absolute, ok)
	}

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.go")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := resolveTerminalPath(base, outside); ok {
		t.Fatal("absolute path outside workspace was accepted")
	}
	link := filepath.Join(base, "escape.go")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := resolveTerminalPath(base, "escape.go"); ok {
		t.Fatal("symlink escape outside workspace was accepted")
	}
	if _, _, ok := resolveTerminalPath(base, "../secret.go"); ok {
		t.Fatal("parent traversal outside workspace was accepted")
	}
}

func TestActivateTerminalLinkMapsClickThroughViewportCoordinates(t *testing.T) {
	p := newSelectionTestPlugin()
	buffer := tty.NewOutputBuffer(20)
	buffer.Write("go https://example.com/docs now")
	p.shellSelected = true
	p.shells = []*ShellSession{{Agent: &Agent{OutputBuf: buffer}}}
	p.selectedShellIdx = 0

	action := actionAt(8, 4)
	cmd, ok := p.activateTerminalLink(action)
	if !ok || cmd == nil {
		t.Fatal("click inside URL did not activate link")
	}
	outside := actionAt(1, 4)
	if cmd, ok := p.activateTerminalLink(outside); ok || cmd != nil {
		t.Fatal("click outside URL activated a link")
	}
}

func TestOpenTerminalPathSequencesWorktreeSwitchBeforeNavigation(t *testing.T) {
	mainDir := t.TempDir()
	worktreeDir := t.TempDir()
	path := filepath.Join(worktreeDir, "internal", "foo.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package internal"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New()
	p.ctx = &plugin.Context{WorkDir: mainDir}
	p.worktrees = []*Worktree{{Name: "feature", Path: worktreeDir}}

	cmd := p.openTerminalPath("internal/foo.go", 37)
	if cmd == nil {
		t.Fatal("valid worktree path returned no command")
	}
	msg := cmd()
	sequence := reflect.ValueOf(msg)
	if sequence.Kind() != reflect.Slice || sequence.Len() != 3 {
		t.Fatalf("path command = %T len=%d, want three-command sequence", msg, sequence.Len())
	}
	commands := make([]tea.Cmd, sequence.Len())
	for i := range commands {
		commands[i] = sequence.Index(i).Interface().(tea.Cmd)
	}
	switchMsg, ok := commands[0]().(app.SwitchWorktreeMsg)
	if !ok {
		t.Fatalf("first sequence message = %T, want SwitchWorktreeMsg", commands[0]())
	}
	resolvedWorktree, err := filepath.EvalSymlinks(worktreeDir)
	if err != nil {
		t.Fatal(err)
	}
	if switchMsg.WorktreePath != resolvedWorktree {
		t.Fatalf("switch path = %q, want %q", switchMsg.WorktreePath, resolvedWorktree)
	}
	if _, ok := commands[1]().(app.FocusPluginByIDMsg); !ok {
		t.Fatalf("second sequence message = %T, want FocusPluginByIDMsg", commands[1]())
	}
	navigate, ok := commands[2]().(filebrowser.NavigateToFileMsg)
	if !ok || navigate.Path != "internal/foo.go" || navigate.Line != 37 {
		t.Fatalf("third sequence message = %#v, want file navigation at line 37", commands[2]())
	}
}

func TestLinkDecorationPreservesSearchAndSelectionRendering(t *testing.T) {
	buffer := tty.NewOutputBuffer(10)
	buffer.Write("go https://example.com")
	selection := &ui.SelectionState{}
	selection.Clear()
	selection.SelectRange(
		ui.SelectionPoint{Line: 0, Col: 0},
		ui.SelectionPoint{Line: 0, Col: 1},
		false,
	)
	result := renderTerminalViewport(terminalViewportInput{
		Buffer:    buffer,
		Width:     40,
		Height:    1,
		Selection: selection,
		SearchMatches: &terminalSearchMatches{Items: []terminalSearchMatch{{
			Line:     0,
			StartCol: 11,
			EndCol:   17,
		}}},
	}, ui.NewTruncateCache(16))
	if got := ansi.Strip(result.Content); got != "go https://example.com" {
		t.Fatalf("combined rendering corrupted text: %q", got)
	}
	if !strings.Contains(result.Content, "\x1b]8;;https://example.com\x1b\\") ||
		!strings.Contains(result.Content, ui.GetSelectionBgANSI()) {
		t.Fatalf("combined rendering lost link/highlight controls: %q", result.Content)
	}
}
