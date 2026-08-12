package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

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
	got := decorateTerminalLinks("visit https://example.com/x", nil)
	if !strings.Contains(got, "\x1b]8;;https://example.com/x\x1b\\") {
		t.Fatalf("validated URL did not receive OSC-8: %q", got)
	}
	if ansi.StringWidth(got) != len("visit https://example.com/x") {
		t.Fatalf("link decoration changed visual width: %d", ansi.StringWidth(got))
	}

	source := "\x1b]8;;javascript:alert(1)\x1b\\label\x1b]8;;\x1b\\"
	cleaned := decorateTerminalLinks(source, nil)
	if strings.Contains(cleaned, "javascript:") || strings.Contains(cleaned, "\x1b]8;;") {
		t.Fatalf("source-supplied OSC-8 survived sanitization: %q", cleaned)
	}
	if ansi.Strip(cleaned) != "label" {
		t.Fatalf("OSC-8 sanitization lost label: %q", cleaned)
	}
}

func TestStripSourceOSC8HandlesC1AndMixedForms(t *testing.T) {
	for name, source := range map[string]string{
		"c1":              "\x9d8;;javascript:alert(1)\x9clabel\x9d8;;\x9c",
		"utf8-c1":         "\u009d8;;javascript:alert(1)\u009clabel\u009d8;;\u009c",
		"esc-intro-c1-st": "\x1b]8;;javascript:alert(1)\x9clabel\x1b]8;;\x9c",
		"c1-intro-esc-st": "\x9d8;;javascript:alert(1)\x1b\\label\x9d8;;\x1b\\",
		"c1-intro-bel":    "\x9d8;;javascript:alert(1)\x07label\x9d8;;\x07",
	} {
		t.Run(name, func(t *testing.T) {
			cleaned := stripSourceOSC8(source)
			if cleaned != "label" {
				t.Fatalf("stripSourceOSC8(%q) = %q, want label", source, cleaned)
			}
		})
	}
}

func TestStripSourceOSC8DropsNestedControlsAndPreservesVisibleLabel(t *testing.T) {
	introducers := map[string]string{
		"esc":     "\x1b]",
		"raw-c1":  "\x9d",
		"utf8-c1": "\u009d",
	}
	terminators := map[string]string{
		"bel":     "\x07",
		"esc-st":  "\x1b\\",
		"raw-st":  "\x9c",
		"utf8-st": "\u009c",
	}
	for outerName, outer := range introducers {
		for nestedName, nested := range introducers {
			for termName, terminator := range terminators {
				name := outerName + "-" + nestedName + "-" + termName
				t.Run(name, func(t *testing.T) {
					source := "safe" + outer + "0;title" +
						nested + "8;;https://evil.example" + terminator +
						"LABEL" + nested + "8;;" + terminator
					cleaned := stripSourceOSC8(source)
					if cleaned != "safeLABEL" {
						t.Fatalf("nested OSC sanitization = %q, want safeLABEL", cleaned)
					}
					if containsOSCIntroducerAtRuneBoundary(cleaned) {
						t.Fatalf("OSC introducer survived nested sanitization: %q", cleaned)
					}
				})
			}
		}
	}
	for introName, intro := range introducers {
		for termName, terminator := range terminators {
			name := introName + "-" + termName
			t.Run("adjacent-"+name, func(t *testing.T) {
				source := "safe" + intro + "0;title" + terminator +
					intro + "8;;https://evil.example" + terminator +
					"LABEL" + intro + "8;;" + terminator
				if cleaned := stripSourceOSC8(source); cleaned != "safeLABEL" {
					t.Fatalf("adjacent OSC sanitization = %q, want safeLABEL", cleaned)
				}
			})
		}
	}
}

func TestStripSourceOSC8PreservesCSIAndOrdinaryEscape(t *testing.T) {
	source := "plain \x1b[31mred\x1b[0m and escape \x1bx"
	if cleaned := stripSourceOSC8(source); cleaned != source {
		t.Fatalf("non-OSC escape changed: got %q, want %q", cleaned, source)
	}
}

func TestStripSourceOSC8PreservesUTF8ContainingC1ContinuationBytes(t *testing.T) {
	const ordinary = "plain Ý, Ü, ʝ, and ݝ text"
	if cleaned := stripSourceOSC8(ordinary); cleaned != ordinary {
		t.Fatalf("ordinary UTF-8 text changed: got %q, want %q", cleaned, ordinary)
	}

	source := "\x1b]8;;https://hidden.example/\u00dc/https://evil.example\x07label\x1b]8;;\x07"
	cleaned := decorateTerminalLinks(source, nil)
	if ansi.Strip(cleaned) != "label" {
		t.Fatalf("UTF-8 URI payload leaked into label: %q", cleaned)
	}
	if strings.Contains(cleaned, "evil.example") || strings.Contains(cleaned, "\x1b]8;;") {
		t.Fatalf("hidden UTF-8 URI payload survived or was linkified: %q", cleaned)
	}
}

func TestStripSourceOSC8DropsMalformedHyperlinkRemainders(t *testing.T) {
	for name, source := range map[string]string{
		"esc-unclosed":  "safe\x1b]8;;javascript:alert(1)",
		"c1-unclosed":   "safe\x9d8;;javascript:alert(1)\x1b[31m",
		"utf8-unclosed": "safe\u009d8;;javascript:alert(1)",
		"short":         "safe\x9d8;",
		"short-command": "safe\x1b]8",
		"bare-intro":    "safe\x9d",
		"nested-intro":  "safe\x9d8\x9d",
	} {
		t.Run(name, func(t *testing.T) {
			cleaned := stripSourceOSC8(source)
			if cleaned != "safe" {
				t.Fatalf("stripSourceOSC8(%q) = %q, want safe prefix", source, cleaned)
			}
		})
	}
}

func TestStripSourceOSC8RecognizesRawC1BesideInvalidUTF8(t *testing.T) {
	source := string([]byte{'a', 0xff, 0x9d, '8', ';', ';', 0xfe, 0x9c, 'b'})
	want := string([]byte{'a', 0xff, 'b'})
	if cleaned := stripSourceOSC8(source); cleaned != want {
		t.Fatalf("raw C1 beside malformed UTF-8 = %q, want %q", cleaned, want)
	}
}

func FuzzStripSourceOSC8(f *testing.F) {
	for _, source := range []string{
		"\x1b]8;;https://example.com\x07label\x1b]8;;\x07",
		"\x9d8;;javascript:alert(1)\x9clabel\x9d8;;\x9c",
		"\u009d8;;javascript:alert(1)\x1b\\label\u009d8;;\x9c",
		"\x1b]8;",
		"\x9d8",
		"ݝ8;",
		"\x9d\x9d8;\x9c",
		"\x1b\x1b]\x07]",
		"\x1b\x1b\x1b]\x07]",
		string([]byte{0xc2, 0x1b, ']', '8', ';', ';', 0x07, 0x9d, '8', ';'}),
		"\x1b]\x9d8;;https://evil.example\u009cLABEL\x1b]8;;\x07",
		"safe\x1b]0;title\x1b]8;;https://evil.example\x07LABEL\x1b]8;;\x07",
		string([]byte{0x9d, '8', ';', ';', 0, 0x9c}),
	} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		cleaned := stripSourceOSC8(source)
		if containsOSCIntroducerAtRuneBoundary(cleaned) {
			t.Fatalf("OSC control survived sanitization: input=%q output=%q", source, cleaned)
		}
	})
}

func containsOSCIntroducerAtRuneBoundary(value string) bool {
	for pos := 0; pos < len(value); {
		switch {
		case pos+1 < len(value) && value[pos] == '\x1b' && value[pos+1] == ']':
			return true
		case value[pos] == '\x9d':
			return true
		case pos+1 < len(value) && value[pos] == '\xc2' && value[pos+1] == '\x9d':
			return true
		}
		_, size := utf8.DecodeRuneInString(value[pos:])
		pos += size
	}
	return false
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

func TestBareMarkdownLinksResolveConservativelyAndPreserveCoordinates(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# doc\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md")
	write("docs/guide.markdown")
	write("docs/overlap.md")
	if err := os.Mkdir(filepath.Join(root, "directory.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.ctx = &plugin.Context{WorkDir: root}
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "docs", Agent: &Agent{OutputBuf: tty.NewOutputBuffer(20)}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	buffer := p.shells[0].Agent.OutputBuf
	buffer.Update("accepted capture")

	tests := []struct {
		name string
		line string
		want []string
	}{
		{"prose", "please read README.md before continuing", []string{"README.md"}},
		{"table", "| plan | docs/guide.markdown | ready |", []string{"docs/guide.markdown"}},
		{"backticks", "opened `docs/guide.markdown`", []string{"docs/guide.markdown"}},
		{"punctuation", "See (docs/guide.markdown).", []string{"docs/guide.markdown"}},
		{"unicode columns", "✓ 日本語 docs/guide.markdown", []string{"docs/guide.markdown"}},
		{"path line overlap", "docs/overlap.md:12", nil},
		{"dangling", "missing.md", nil},
		{"directory", "directory.md", nil},
		{"outside absolute", outside, nil},
		{"url overlap", "https://example.test/docs/guide.markdown", nil},
		{"numeric suffix", "README.md5", nil},
		{"identifier suffix", "README.mdfoo", nil},
		{"colon without line", "README.md: prose", nil},
		{"markdown suffix", "docs/guide.markdowned", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			links := p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(false), buffer, tc.line)
			decorated := decorateTerminalLinks(tc.line, p.terminalLinkResolver(false, buffer))
			if ansi.Strip(decorated) != tc.line {
				t.Fatalf("decoration changed terminal text: %q", decorated)
			}
			if got := strings.Count(decorated, "\x1b[4m"); got != len(links) {
				t.Fatalf("drawn link count = %d, resolved link count = %d: %q", got, len(links), decorated)
			}
			var bare []string
			for _, link := range links {
				if link.Kind == terminalPathLink && link.Line == 0 {
					bare = append(bare, link.Value)
				}
			}
			if !reflect.DeepEqual(bare, tc.want) {
				t.Fatalf("bare links = %#v, want %#v; all=%#v", bare, tc.want, links)
			}
			if tc.name == "path line overlap" && (len(links) != 1 || links[0].Line != 12) {
				t.Fatalf("existing path:line link was not authoritative: %#v", links)
			}
			for _, link := range links {
				if link.Value == "docs/guide.markdown" && tc.name == "unicode columns" {
					wantStart := ansi.StringWidth("✓ 日本語 ")
					if link.StartCol != wantStart {
						t.Fatalf("start column = %d, want visual column %d", link.StartCol, wantStart)
					}
				}
			}
		})
	}
}

func TestBareMarkdownResolutionMemoizesHitsAndMissesPerAcceptedCaptureAndSurface(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	for _, dir := range []string{root, otherRoot} {
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# readme"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	buffer := tty.NewOutputBuffer(20)
	buffer.Update("README.md missing.md README.md")
	p := New()
	p.ctx = &plugin.Context{WorkDir: root}
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "one", Agent: &Agent{TmuxSession: "session-one", TmuxPane: "%1", OutputBuf: buffer}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	calls := 0
	p.terminalPathResolver = func(base, raw string) (string, string, bool) {
		calls++
		return resolveTerminalPathFromResolvedBase(base, raw)
	}

	line := "README.md missing.md README.md"
	p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(false), buffer, line)
	p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(false), buffer, line) // unrelated render
	if calls != 2 {
		t.Fatalf("resolver calls = %d, want one per unique hit/miss", calls)
	}
	p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(true), buffer, line)
	p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(false), buffer, line)
	if calls != 4 {
		t.Fatalf("independent panel memo calls = %d, want 4 without evicting primary", calls)
	}
	buffer.Update(line + " changed")
	p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(false), buffer, line)
	if calls != 6 {
		t.Fatalf("resolver calls after accepted capture = %d, want 6", calls)
	}
	buffer.Update(line + " changed") // rejected duplicate publication
	p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(false), buffer, line)
	if calls != 6 {
		t.Fatalf("duplicate capture invalidated memo: calls=%d", calls)
	}
	p.shells[0].Agent.TmuxPane = "%2"
	p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(false), buffer, line)
	if calls != 8 {
		t.Fatalf("terminal target change calls = %d, want 8", calls)
	}
	p.shells[0] = &ShellSession{TmuxName: "two", Agent: &Agent{TmuxSession: "session-two", TmuxPane: "%1", OutputBuf: buffer}}
	p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(false), buffer, line)
	if calls != 10 {
		t.Fatalf("surface identity change calls = %d, want 10", calls)
	}
	p.ctx.WorkDir = otherRoot
	p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(false), buffer, line)
	if calls != 12 {
		t.Fatalf("surface root change calls = %d, want 12", calls)
	}
	p.shellSelected = false
	p.worktrees = []*Worktree{{Name: "workspace", Path: root, Agent: &Agent{OutputBuf: buffer}}}
	p.selectedIdx = 0
	p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(false), buffer, line)
	if calls != 14 {
		t.Fatalf("shell to workspace surface change calls = %d, want 14", calls)
	}
}

func TestBareMarkdownCachedDecorationAndActivationDoNoFilesystemWork(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	buffer := tty.NewOutputBuffer(20)
	buffer.Update("README.md missing.md")
	p := New()
	p.ctx = &plugin.Context{WorkDir: root}
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "one", Agent: &Agent{
		TmuxSession: "session", TmuxPane: "%1", OutputBuf: buffer,
	}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	rootCalls, pathCalls := 0, 0
	p.terminalRootResolver = func(raw string) (string, error) {
		rootCalls++
		return filepath.EvalSymlinks(raw)
	}
	p.terminalPathResolver = func(base, raw string) (string, string, bool) {
		pathCalls++
		return resolveTerminalPathFromResolvedBase(base, raw)
	}

	resolver := p.terminalLinkResolver(false, buffer)
	_ = decorateTerminalLinks("README.md missing.md", resolver)
	if rootCalls != 1 || pathCalls != 2 {
		t.Fatalf("setup calls root=%d path=%d, want 1 and 2", rootCalls, pathCalls)
	}
	for range 5 {
		// Decoration and activation both obtain an immutable surface context;
		// neither may canonicalize or stat again after the accepted capture's
		// hit and miss have populated the memo.
		_ = decorateTerminalLinks("README.md missing.md", p.terminalLinkResolver(false, buffer))
		context := p.terminalLinkSurfaceContext(false)
		_ = p.resolvedTerminalLinks(context, buffer, "README.md missing.md")
	}
	// Exercise the real pointer activation lookup as well. The narrow synthetic
	// viewport may refuse to open a pane, but resolving the link under the click
	// must still be entirely memo-backed.
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, VisibleStart: 0, VisibleEnd: 2}
	p.selection.Clear()
	_, _ = p.activateTerminalLink(actionAt(2, 4))
	if rootCalls != 1 || pathCalls != 2 {
		t.Fatalf("cached render/click performed filesystem work: root=%d path=%d", rootCalls, pathCalls)
	}
}

func TestBareMarkdownClickRefusesPathSwappedOutsideAfterDecoration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outPath, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	buffer := tty.NewOutputBuffer(20)
	buffer.Update("README.md")
	p := newSelectionTestPlugin()
	p.ctx = &plugin.Context{WorkDir: root}
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "one", Agent: &Agent{OutputBuf: buffer}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	resolver := p.terminalLinkResolver(false, buffer)
	if links := resolver.links("README.md"); len(links) != 1 || links[0].Raw != "README.md" {
		t.Fatalf("initial resolved links = %#v", links)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outPath, path); err != nil {
		t.Fatal(err)
	}
	if cmd, ok := p.activateTerminalLink(actionAt(2, 4)); ok || cmd != nil {
		t.Fatal("click activated cached link after path escaped selected root")
	}
	if doc, _ := p.activeDocPane(); doc != nil {
		t.Fatal("escaping click created a document pane")
	}
}

func BenchmarkDecorateTerminalBareMarkdownLinks(b *testing.B) {
	root := b.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "plan.md"), []byte("# Plan"), 0o644); err != nil {
		b.Fatal(err)
	}
	buffer := tty.NewOutputBuffer(20)
	buffer.Update("| result | `docs/plan.md`, | missing.md |")
	p := New()
	p.ctx = &plugin.Context{WorkDir: root}
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "bench", Agent: &Agent{OutputBuf: buffer}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	resolver := p.terminalLinkResolver(false, buffer)
	b.ResetTimer()
	for range b.N {
		_ = decorateTerminalLinks("| result | `docs/plan.md`, | missing.md |", resolver)
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
	if got := terminalTextLines(result)[0]; got != "go https://example.com" {
		t.Fatalf("combined rendering corrupted text: %q", got)
	}
	if !strings.Contains(result.Content, "\x1b]8;;https://example.com\x1b\\") ||
		!strings.Contains(result.Content, ui.GetSelectionBgANSI()) {
		t.Fatalf("combined rendering lost link/highlight controls: %q", result.Content)
	}
}
