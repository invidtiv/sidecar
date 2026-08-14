package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// A td id is a link only where there is a pane tree to open it in. Without one
// — no tree, or a surface whose root did not resolve — it stays plain text and
// a click on it is an ordinary terminal gesture, because an underline is a
// promise and this host has no other route for the kind.
func TestIssueSpanIsPlainTextWithoutAPaneToOpenItIn(t *testing.T) {
	line := "review td-196c42"
	if links := detectTerminalLinks(line); len(links) != 0 {
		t.Fatalf("an unbound host must ignore issue spans: %#v", links)
	}
	if got := decorateTerminalLinks(line, nil); strings.Contains(got, "\x1b[4m") {
		t.Fatalf("issue id was decorated: %q", got)
	}

	buffer := tty.NewOutputBuffer(20)
	buffer.Update(line)
	p := newSelectionTestPlugin()
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "one", Agent: &Agent{OutputBuf: buffer}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.docs = make(map[int]*docPane)
	// No plugin context, so no terminal surface resolves and no leaf could be
	// bound to one.
	if cmd, ok := p.activateTerminalLink(actionAt(8, 4)); ok || cmd != nil {
		t.Fatal("clicking a td issue id activated a host path with no surface")
	}
	if issue, _ := p.activeIssuePane(); issue != nil {
		t.Fatal("issue click opened a pane with no surface to bind it to")
	}
}

// With a tree and a resolved surface the same id is underlined by the same
// resolver the click reads, so what the row promises and what the click opens
// are one answer.
func TestIssueSpanIsDecoratedWhereItIsClickable(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	line := "follow-up is td-1a2b3c"
	buffer := p.shells[0].Agent.OutputBuf
	buffer.Update(line)

	resolver := p.terminalLinkResolver(false, buffer)
	if resolver == nil {
		t.Fatal("a bound surface produced no link resolver")
	}
	links := resolver.links(line)
	if len(links) != 1 || links[0].Kind != terminalIssueLink || links[0].Value != "td-1a2b3c" {
		t.Fatalf("resolved links = %#v, want one issue link", links)
	}
	decorated := decorateTerminalLinks(line, resolver)
	if ansi.Strip(decorated) != line || !strings.Contains(decorated, "\x1b[4m") {
		t.Fatalf("issue decoration = %q", decorated)
	}
	if strings.Contains(decorated, "\x1b]8;;") {
		t.Fatalf("issue id was given an OSC-8 hyperlink: %q", decorated)
	}
}

func mustEvalSymlink(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

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

func TestResolveTerminalPathAcceptsAnyRegularFileOnThisMachine(t *testing.T) {
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
	outsideResolved, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	display, abs, ok := resolveTerminalPath(base, outside)
	if !ok || abs != outsideResolved || display != outsideResolved {
		t.Fatalf("outside resolution = display %q abs %q ok=%v", display, abs, ok)
	}
	link := filepath.Join(base, "escape.go")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	display, abs, ok = resolveTerminalPath(base, "escape.go")
	if !ok || abs != outsideResolved {
		t.Fatalf("symlink to a regular file was refused: display %q abs %q ok=%v", display, abs, ok)
	}
	if _, _, ok := resolveTerminalPath(base, filepath.Join(outsideDir, "missing.go")); ok {
		t.Fatal("missing path was accepted")
	}
	if _, _, ok := resolveTerminalPath(base, outsideDir); ok {
		t.Fatal("directory was accepted")
	}
	if _, _, ok := resolveTerminalPath(base, "internal/foo.go\x00"); ok {
		t.Fatal("control character token was accepted")
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
		{"outside absolute", outside, []string{mustEvalSymlink(t, outside)}},
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
	p.interactiveState = &InteractiveState{Active: true}
	p.selection.Clear()
	_, _ = p.activateTerminalLink(actionAt(2, 4))
	if rootCalls != 2 || pathCalls != 2 {
		t.Fatalf("cached render performed I/O or click did more than root revalidation: root=%d path=%d", rootCalls, pathCalls)
	}
}

func TestBareMarkdownClickRefusesPathSwappedOutsideAfterDecoration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	buffer := tty.NewOutputBuffer(20)
	buffer.Update("README.md")
	p := newSelectionTestPlugin()
	p.ctx = &plugin.Context{WorkDir: root}
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "one", Agent: &Agent{OutputBuf: buffer}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.previewScroll = 7
	resolver := p.terminalLinkResolver(false, buffer)
	if links := resolver.links("README.md"); len(links) != 1 || links[0].Raw != "README.md" {
		t.Fatalf("initial resolved links = %#v", links)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if cmd, ok := p.activateTerminalLink(actionAt(2, 4)); ok || cmd != nil {
		t.Fatal("click activated cached link after path became a directory")
	}
	if doc, _ := p.activeDocPane(); doc != nil {
		t.Fatal("directory click created a document pane")
	}
}

func TestBareMarkdownClickRefusesRetargetedSelectedRoot(t *testing.T) {
	parent := t.TempDir()
	rootA := filepath.Join(parent, "a")
	rootB := filepath.Join(parent, "b")
	for _, dir := range []string{rootA, rootB} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(dir), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(parent, "current")
	if err := os.Symlink(rootA, current); err != nil {
		t.Fatal(err)
	}
	buffer := tty.NewOutputBuffer(20)
	buffer.Update("README.md")
	p := newSelectionTestPlugin()
	p.ctx = &plugin.Context{WorkDir: current}
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "one", Agent: &Agent{
		TmuxSession: "session", TmuxPane: "%1", OutputBuf: buffer,
	}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.previewScroll = 7
	resolver := p.terminalLinkResolver(false, buffer)
	links := resolver.links("README.md")
	rootAResolved, err := filepath.EvalSymlinks(rootA)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Root != rootAResolved {
		t.Fatalf("initial link root = %#v, want %q", links, rootAResolved)
	}
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rootB, current); err != nil {
		t.Fatal(err)
	}
	if cmd := p.handleMouseClick(actionAt(2, 4)); cmd != nil {
		t.Fatal("click activated link cached under previous selected root")
	}
	if p.previewScroll != 7 || p.previewFreeze.Active() {
		t.Fatalf("refused click mutated viewport: scroll=%d pinned=%v",
			p.previewScroll, p.previewFreeze.Active())
	}
	if _, found := p.terminalLinkMemo.surfaces["shell:one"]; found {
		t.Fatal("root mismatch did not invalidate stale surface memo")
	}
	if doc, _ := p.activeDocPane(); doc != nil {
		t.Fatal("retargeted-root click created a document pane")
	}
}

func TestClaudeUpdateStyledMarkdownPathDecoratesAndActivates(t *testing.T) {
	root := t.TempDir()
	rel := "docs/plans/active/global-overview-workspaces.md"
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Global overview"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Claude Code paints the operation and path independently. Keep resets
	// inside and immediately after the candidate to exercise plain-to-styled
	// visual-column mapping rather than only an unstyled approximation.
	line := "\x1b[38;5;111m⏺\x1b[0m Update(" +
		"\x1b[1;34mdocs/plans/active/global-\x1b[22moverview-workspaces.md\x1b[39m)"
	buffer := tty.NewOutputBuffer(20)
	initialRows := []string{
		line,
		"  ⎿ Added terminal document navigation",
		"    Running focused tests...",
		"    internal/plugins/workspace: ok",
	}
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output: strings.Join(initialRows, "\n"), BaseLine: 100, Absolute: true,
		HistoryRows: 0, PaneRows: len(initialRows),
	})
	p := newSelectionTestPlugin()
	p.ctx = &plugin.Context{WorkDir: root, Epoch: 7}
	p.width, p.height = 140, 30
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "claude", Agent: &Agent{
		TmuxSession: "session", TmuxPane: "%1", OutputBuf: buffer,
	}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.paneFocus = 1
	p.paneNextID = 2
	p.docs = make(map[int]*docPane)
	p.interactiveState.MouseReportingEnabled = true

	resolver := p.terminalLinkResolver(false, buffer)
	links := resolver.links(line)
	if len(links) != 1 || links[0].Value != rel || links[0].Raw != rel {
		t.Fatalf("Claude Update links = %#v", links)
	}
	wantStart := ansi.StringWidth("⏺ Update(")
	if links[0].StartCol != wantStart || links[0].EndCol != wantStart+ansi.StringWidth(rel)-1 {
		t.Fatalf("link columns = %d..%d, want %d..%d", links[0].StartCol, links[0].EndCol,
			wantStart, wantStart+ansi.StringWidth(rel)-1)
	}
	decorated := decorateTerminalLinks(line, resolver)
	if ansi.Strip(decorated) != "⏺ Update("+rel+")" || !strings.Contains(decorated, "\x1b[4m") {
		t.Fatalf("styled decoration = %q", decorated)
	}

	action := actionAt(wantStart+2, 4)
	cmd := p.handleMouseClick(action)
	if cmd == nil {
		t.Fatal("click on styled Claude Update path did not activate")
	}
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view().Title() != rel {
		t.Fatalf("click opened doc = %#v", doc)
	}
	if p.viewMode != ViewModeList || p.interactiveState != nil || !p.docFocused() {
		t.Fatalf("doc activation retained interactive terminal ownership: mode=%v interactive=%#v focus=%d",
			p.viewMode, p.interactiveState, p.paneFocus)
	}
	if !p.previewFreeze.Active() || p.previewFreeze.Start() != 0 {
		t.Fatalf("doc activation did not freeze clicked viewport: pinned=%v start=%d",
			p.previewFreeze.Active(), p.previewFreeze.Start())
	}
	// Claude redraws after the split: the transcript containing the clicked link
	// moves into history while its new live grid is mostly chrome and blank rows.
	// A live-follow viewport would start at PaneTop and look nearly empty.
	postResizeRows := append(append([]string{}, initialRows...),
		"╭─ Claude Code ─────────────────────────╮", "", "  ❯ ", "")
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output: strings.Join(postResizeRows, "\n"), BaseLine: 100, Absolute: true,
		HistoryRows: len(initialRows), PaneRows: 4,
	})
	if got := ansi.Strip(p.renderCapturedTerminal(nil, "", buffer, 70, 8, false, "empty")); !strings.Contains(got, "Update("+rel+")") || strings.Contains(got, "INTERACTIVE") {
		t.Fatalf("terminal was not coherent immediately after doc-open resize transition: %q", got)
	}
	if cmd := p.closeDocPane(); cmd == nil {
		t.Fatal("closing document did not schedule terminal resize")
	}
	if got := ansi.Strip(p.renderCapturedTerminal(nil, "", buffer, 120, 8, false, "empty")); !strings.Contains(got, "Update("+rel+")") {
		t.Fatalf("closing document lost frozen terminal context: %q", got)
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("document open command = %T, want batch", cmd())
	}
	loaded, ok := batch[0]().(docview.LoadedMsg)
	if !ok || loaded.Path != rel || loaded.Result.Error != nil {
		t.Fatalf("document load = %#v", loaded)
	}
}

func TestInteractiveMouseReportingNonLinkClickStillForwards(t *testing.T) {
	buffer := tty.NewOutputBuffer(20)
	buffer.Update("ordinary terminal text")
	p := newSelectionTestPlugin()
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "claude", Agent: &Agent{OutputBuf: buffer}}}
	attachLiveTerminal(p, true)

	action := actionAt(3, 4)
	_ = p.handleMouseClick(action)
	if p.pointer.Resolution != tty.ClickForward {
		t.Fatalf("non-link click resolution = %v, want forward", p.pointer.Resolution)
	}

	action.Shift = true
	p.pointer.Resolution = tty.ClickNone
	_ = p.handleMouseClick(action)
	if p.pointer.Resolution != tty.ClickNone {
		t.Fatalf("shift click resolution = %v, want local selection gesture", p.pointer.Resolution)
	}
}

func TestInteractiveAuthoritativeMarkdownPathLineUsesDocViewportTransition(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	buffer := tty.NewOutputBuffer(20)
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: "README.md:12\nworking", PaneRows: 2})
	p := newSelectionTestPlugin()
	p.ctx = &plugin.Context{WorkDir: root}
	p.width, p.height = 100, 24
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "claude", Agent: &Agent{OutputBuf: buffer}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.paneFocus, p.paneNextID = 1, 2
	p.docs = make(map[int]*docPane)

	if cmd := p.handleMouseClick(actionAt(2, 4)); cmd == nil {
		t.Fatal("interactive path:line did not activate")
	}
	if doc, _ := p.activeDocPane(); doc == nil || doc.view().Title() != "README.md" {
		t.Fatalf("interactive path:line opened doc = %#v", doc)
	}
	if p.viewMode != ViewModeList || p.interactiveState != nil || !p.previewFreeze.Active() {
		t.Fatalf("path:line retained live ownership/follow: mode=%v interactive=%#v pinned=%v",
			p.viewMode, p.interactiveState, p.previewFreeze.Active())
	}
}

func TestInteractiveURLBesideExistingDocKeepsTerminalOwnership(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	buffer := tty.NewOutputBuffer(20)
	buffer.Update("https://example.com/docs")
	p := newSelectionTestPlugin()
	p.ctx = &plugin.Context{WorkDir: root}
	p.width, p.height = 100, 24
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "claude", Agent: &Agent{OutputBuf: buffer}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.paneFocus, p.paneNextID = 1, 2
	p.docs = make(map[int]*docPane)
	if cmd := p.openDocPane(root, "README.md", 0); cmd == nil {
		t.Fatal("fixture did not open existing document")
	}
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, MouseReportingEnabled: true, PaneOnEntry: PanePreview}

	if cmd := p.handleMouseClick(actionAt(10, 4)); cmd == nil {
		t.Fatal("interactive URL did not activate")
	}
	if p.viewMode != ViewModeInteractive || p.interactiveState == nil ||
		p.previewScroll != 0 || p.previewFreeze.Active() {
		t.Fatalf("URL beside doc changed terminal ownership: mode=%v interactive=%#v scroll=%d pinned=%v",
			p.viewMode, p.interactiveState, p.previewScroll, p.previewFreeze.Active())
	}
}

func TestDocViewportFreezePinsTerminalPanelByAbsoluteRow(t *testing.T) {
	rows := []string{"clicked docs/plan.md", "result", "tests", "done"}
	buffer := tty.NewOutputBuffer(20)
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output: strings.Join(rows, "\n"), BaseLine: 50, Absolute: true,
		PaneRows: len(rows),
	})
	p := newSelectionTestPlugin()
	p.width, p.height = 100, 24
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.termPanelVisible = true
	p.termPanelLayout = TermPanelRight
	p.termPanelOutput = buffer
	p.interactiveState.TermPanel = true
	p.selectionTermPanel = true

	freeze := p.captureTerminalViewportForDocOpen(true)
	p.applyTerminalViewportFreeze(freeze)
	postResize := append(append([]string{}, rows...), "Claude chrome", "", "❯", "")
	buffer.ApplySnapshot(tty.PaneSnapshot{
		Output: strings.Join(postResize, "\n"), BaseLine: 50, Absolute: true,
		HistoryRows: len(rows), PaneRows: 4,
	})
	follow, offset, fromBottom := p.terminalScrollState(true)
	if follow || fromBottom || offset != freeze.start {
		t.Fatalf("panel freeze = follow %v offset %d fromBottom %v, want absolute %d",
			follow, offset, fromBottom, freeze.start)
	}
	if p.previewScroll != 0 || p.previewFreeze.Active() {
		t.Fatal("panel freeze disturbed independent primary follow state")
	}
}

func TestTerminalPanelDocFreezeReleasesOnPassiveNavigation(t *testing.T) {
	root := t.TempDir()
	rel := "docs/panel.md"
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("# Panel"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := []string{rel, "panel result", "tests pass", "done"}
	panel := tty.NewOutputBuffer(40)
	panel.ApplySnapshot(tty.PaneSnapshot{
		Output: strings.Join(rows, "\n"), BaseLine: 50, Absolute: true,
		PaneRows: len(rows),
	})
	p := newSelectionTestPlugin()
	p.ctx = &plugin.Context{WorkDir: root}
	p.width, p.height = 120, 30
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "claude", Agent: &Agent{OutputBuf: tty.NewOutputBuffer(20)}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.paneFocus, p.paneNextID = 1, 2
	p.docs = make(map[int]*docPane)
	p.termPanelVisible = true
	p.termPanelLayout = TermPanelRight
	p.termPanelSession = "panel-session"
	p.termPanelPaneID = "%2"
	p.termPanelOutput = panel
	p.interactiveState.TermPanel = true
	p.selectionTermPanel = true

	surface := p.terminalSurfaceGeometry(true)
	if !surface.OK {
		t.Fatal("terminal panel fixture has no surface")
	}
	action := mouse.MouseAction{
		Type: mouse.ActionClick, X: surface.X + 2, Y: surface.Y,
		Region: &mouse.Region{ID: regionTermPanelContent, Rect: mouse.Rect{
			X: surface.X, Y: surface.HeaderY, W: surface.Width, H: surface.Height + terminalHeaderRows,
		}},
	}
	if cmd := p.handleMouseClick(action); cmd == nil || !p.termPanelFreeze.Active() {
		t.Fatalf("panel doc activation = cmd %v frozen %v", cmd != nil, p.termPanelFreeze.Active())
	}

	postResize := append(append([]string{}, rows...), "Claude chrome", "", "❯", "")
	panel.ApplySnapshot(tty.PaneSnapshot{
		Output: strings.Join(postResize, "\n"), BaseLine: 50, Absolute: true,
		HistoryRows: len(rows), PaneRows: 4,
	})
	if cmd := p.closeDocPane(); cmd == nil || !p.termPanelFreeze.Active() {
		t.Fatalf("close lost preserved panel context: cmd %v frozen %v", cmd != nil, p.termPanelFreeze.Active())
	}
	frozenStart := p.termPanelFreeze.Start()
	follow, offset, fromBottom := p.terminalScrollState(true)
	if follow || fromBottom || offset != frozenStart {
		t.Fatalf("closed panel context = follow %v offset %d fromBottom %v", follow, offset, fromBottom)
	}

	p.activePane = PanePreview
	p.termPanelFocused = true
	p.handleListKeys(tea.KeyPressMsg{Code: 'G', Text: "G"})
	follow, offset, fromBottom = p.terminalScrollState(true)
	if p.termPanelFreeze.Active() || !follow || !fromBottom || offset != 0 {
		t.Fatalf("G did not return panel live: frozen %v follow %v offset %d fromBottom %v",
			p.termPanelFreeze.Active(), follow, offset, fromBottom)
	}
	if p.previewScroll != 0 || p.previewFreeze.Active() {
		t.Fatal("panel navigation disturbed independent primary follow")
	}

	// Recreate the frozen state and prove a wheel gesture first translates the
	// same visible row, then applies the requested upward movement. The four-row
	// panel above has nowhere to move under the shared window rule, so the
	// gesture is replayed over a buffer with rows above the window: where the
	// notch lands is that rule's, and it stops at the loaded top (td-c3649a).
	history := make([]string, 0, 80)
	for i := range 80 {
		history = append(history, fmt.Sprintf("history row %02d", i))
	}
	panel.ApplySnapshot(tty.PaneSnapshot{
		Output: strings.Join(history, "\n"), BaseLine: 50, Absolute: true,
		PaneRows: len(history),
	})
	pinned := p.termPanelMaxScroll() / 2
	if pinned <= 0 {
		t.Fatal("test premise: the panel has no rows above its window to pin over")
	}
	p.termPanelFreeze.Release()
	p.termPanelFreeze.Freeze(pinned)
	p.thawTermPanelWindow()
	before := p.termPanelScroll
	p.termPanelFreeze.Release()
	p.termPanelFreeze.Freeze(pinned)
	p.handleMouseScroll(mouse.MouseAction{Type: mouse.ActionScrollUp, Delta: -1, Region: action.Region})
	if p.termPanelFreeze.Active() || p.termPanelScroll <= before {
		t.Fatalf("wheel did not release and move panel: frozen %v scroll %d, translated %d",
			p.termPanelFreeze.Active(), p.termPanelScroll, before)
	}
}

func TestScrolledClaudeDocProjectionSurvivesDestructiveResizeRedraw(t *testing.T) {
	root := t.TempDir()
	rel := "docs/scrolled.md"
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("# Scrolled"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		"earlier Claude response",
		"⏺ Update(" + rel + ")",
		"  ⎿ preserved clicked neighborhood",
		"working below the fold",
		"more output",
		"live prompt",
	}
	live := tty.NewOutputBuffer(40)
	live.ApplySnapshot(tty.PaneSnapshot{
		Output: strings.Join(rows, "\n"), BaseLine: 700, Absolute: true,
		HistoryRows: 2, PaneRows: 4,
	})
	p := newSelectionTestPlugin()
	p.ctx = &plugin.Context{WorkDir: root}
	p.width, p.height = 120, 30
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "claude", Agent: &Agent{
		TmuxSession: "claude", TmuxPane: "%3", OutputBuf: live,
	}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.paneFocus, p.paneNextID = 1, 2
	p.docs = make(map[int]*docPane)
	p.terminalHistory = make(map[string]tty.HistoryReach)
	// The reader has scrolled back off the live grid, so the rows they clicked
	// are the transcript above it rather than the pane's current frame. Two
	// notches, because the 4-row pane is letterboxed into this viewport and the
	// window is counted back from where the live edge draws it: the same drawn
	// window this fixture always meant, named in the corrected offset (td-bbbbfe).
	p.previewScroll = 2

	if cmd := p.handleMouseClick(actionAt(12, 5)); cmd == nil {
		t.Fatal("scrolled Claude link did not activate")
	}
	projected := p.projectedTerminalBuffer(false)
	if projected == nil || projected.LineCount() > p.height {
		t.Fatalf("projection = %#v lines %d, want bounded visible copy", projected, projected.LineCount())
	}
	// SIGWINCH lets Claude replace its alternate-screen grid entirely. The new
	// absolute base and wrapping have no coordinate relationship to the clicked
	// screen; the live buffer must still accept it while the doc projection stays.
	live.ApplySnapshot(tty.PaneSnapshot{
		Output: "╭─ Claude Code narrow ─╮\n\n❯\n", BaseLine: 930, Absolute: true,
		PaneRows: 4,
	})
	if got := ansi.Strip(p.renderCapturedTerminal(nil, "", live, 55, 8, false, "empty")); !strings.Contains(got, rel) || !strings.Contains(got, "preserved clicked neighborhood") {
		t.Fatalf("projection lost clicked Claude screen after destructive redraw: %q", got)
	}
	if got := strings.Join(live.LinesRange(0, live.LineCount()), "\n"); !strings.Contains(got, "Claude Code narrow") || strings.Contains(got, rel) {
		t.Fatalf("live buffer was blocked or overwritten by projection: %q", got)
	}

	p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollDown, Delta: 1,
		Region: &mouse.Region{ID: regionPreviewPane},
	})
	if got := ansi.Strip(p.renderCapturedTerminal(nil, "", live, 55, 8, false, "empty")); !strings.Contains(got, "Claude Code narrow") || strings.Contains(got, rel) {
		t.Fatalf("passive wheel did not release and reveal current live grid: %q", got)
	}

	// A passive non-link click is also an explicit handoff to the terminal. It
	// releases before gesture hit-testing so pixels and buffer coordinates share
	// the live source.
	p.terminalDocProjection = terminalDocProjection{
		buffer: projected, source: live, identity: p.terminalProjectionIdentity(false),
	}
	p.handleMouseClick(actionAt(1, 4))
	if p.terminalDocProjection.buffer != nil {
		t.Fatal("passive click retained projected pixels while hit-testing live data")
	}
}

func TestTerminalDocProjectionRejectsChangedSurfaceIdentity(t *testing.T) {
	p := newSelectionTestPlugin()
	p.shellSelected = true
	live := tty.NewOutputBuffer(2)
	p.shells = []*ShellSession{{TmuxName: "one", Agent: &Agent{TmuxPane: "%1", OutputBuf: live}}}
	p.terminalDocProjection = terminalDocProjection{
		buffer: tty.NewOutputBuffer(1), source: live, identity: "shell:one\x00\x00%1",
	}
	p.terminalDocProjection.buffer.Update("old screen")
	if p.projectedTerminalBuffer(false) == nil {
		t.Fatal("fixture projection did not match original surface")
	}
	p.shells[0].TmuxName = "two"
	if p.projectedTerminalBuffer(false) != nil {
		t.Fatal("projection resurrected after selected surface identity changed")
	}
	p.shells[0].TmuxName = "one"
	p.loadSelectedContent()
	if p.terminalDocProjection.buffer != nil {
		t.Fatal("selection lifecycle retained stale terminal projection")
	}
}

func TestTerminalDocProjectionRejectsRecreatedBufferWithSameTargetIDs(t *testing.T) {
	t.Run("primary", func(t *testing.T) {
		p := newSelectionTestPlugin()
		p.shellSelected = true
		original := tty.NewOutputBuffer(2)
		p.shells = []*ShellSession{{TmuxName: "same", Agent: &Agent{
			TmuxSession: "same", TmuxPane: "%1", OutputBuf: original,
		}}}
		projected := tty.NewOutputBuffer(1)
		projected.Update("old primary screen")
		p.terminalDocProjection = terminalDocProjection{
			buffer: projected, source: original, identity: p.terminalProjectionIdentity(false),
		}
		original.Update("same buffer mutation")
		if p.projectedTerminalBuffer(false) != projected {
			t.Fatal("ordinary live buffer mutation invalidated projection")
		}
		p.shells[0].Agent = &Agent{
			TmuxSession: "same", TmuxPane: "%1", OutputBuf: tty.NewOutputBuffer(2),
		}
		if p.projectedTerminalBuffer(false) != nil {
			t.Fatal("primary projection resurrected over recreated buffer with reused target IDs")
		}
	})

	t.Run("panel", func(t *testing.T) {
		p := newSelectionTestPlugin()
		p.termPanelSession, p.termPanelPaneID = "S", "%2"
		original := tty.NewOutputBuffer(2)
		p.termPanelOutput = original
		projected := tty.NewOutputBuffer(1)
		projected.Update("old panel screen")
		p.terminalDocProjection = terminalDocProjection{
			buffer: projected, source: original, termPanel: true,
			identity: p.terminalProjectionIdentity(true),
		}
		p.cleanupTermPanelSession()
		p.termPanelSession, p.termPanelPaneID = "S", "%2"
		p.termPanelOutput = tty.NewOutputBuffer(2)
		if p.projectedTerminalBuffer(true) != nil || p.terminalDocProjection.buffer != nil {
			t.Fatal("panel projection survived cleanup and resurrected over recreated target")
		}
	})
}

func TestRefusedInteractiveMarkdownClickDoesNotCaptureProjection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	buffer := tty.NewOutputBuffer(20)
	buffer.Update("README.md")
	p := newSelectionTestPlugin()
	p.ctx = &plugin.Context{WorkDir: root}
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "one", Agent: &Agent{OutputBuf: buffer}}}
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	if links := p.terminalLinkResolver(false, buffer).links("README.md"); len(links) != 1 {
		t.Fatalf("fixture links = %#v", links)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if cmd := p.handleMouseClick(actionAt(2, 4)); cmd != nil {
		t.Fatal("directory swapped link activated")
	}
	if p.terminalDocProjection.buffer != nil {
		t.Fatal("refused link captured terminal projection")
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

func TestBareGoPathAndLineOpenRawDocPane(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buffer := tty.NewOutputBuffer(20)
	buffer.Update("see main.go then main.go:37")
	p := docPaneTestPlugin(t, root, true)
	p.shells[0].Agent.OutputBuf = buffer
	links := p.resolvedTerminalLinks(p.terminalLinkSurfaceContext(false), buffer, "see main.go then main.go:37")
	if len(links) != 2 {
		t.Fatalf("links = %#v", links)
	}
	var bare, lined bool
	for _, link := range links {
		if link.Value == "main.go" && link.Line == 0 {
			bare = true
		}
		if link.Value == "main.go" && link.Line == 37 {
			lined = true
		}
	}
	if !bare || !lined {
		t.Fatalf("expected bare and :line file spans: %#v", links)
	}
	if cmd := p.openTerminalPath("main.go", 37); cmd == nil {
		t.Fatal("main.go:37 did not open a preview")
	}
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view().Title() != "main.go" || doc.view().Rendered() {
		t.Fatalf("raw go preview = %#v rendered=%v", doc, doc != nil && doc.view().Rendered())
	}
}

func TestOpenTerminalPathPreviewsOtherWorktreeFileInPlace(t *testing.T) {
	mainDir := t.TempDir()
	worktreeDir := t.TempDir()
	path := filepath.Join(worktreeDir, "internal", "foo.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package internal"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, mainDir, true)
	p.worktrees = []*Worktree{{Name: "feature", Path: worktreeDir}}

	cmd := p.openTerminalPath(path, 37)
	if cmd == nil {
		t.Fatal("outside worktree file returned no preview command")
	}
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view().Title() != mustEvalSymlink(t, path) {
		t.Fatalf("previewed doc = %#v", doc)
	}
	if doc.view().Rendered() {
		t.Fatal("non-markdown preview opened rendered")
	}
	msg := cmd()
	if _, ok := msg.(app.SwitchWorktreeMsg); ok {
		t.Fatal("previewing another worktree switched project")
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			if child == nil {
				continue
			}
			if _, switched := child().(app.SwitchWorktreeMsg); switched {
				t.Fatal("preview batch switched project")
			}
		}
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

// A mouse-down that a link takes never arms a gesture, so the previous press's
// resolution must not survive it: the link's own release would otherwise fire a
// click into the application on top of opening the link.
func TestALinkClaimedPressDropsThePreviousGesturesClick(t *testing.T) {
	buffer := tty.NewOutputBuffer(20)
	buffer.Update("https://example.com/docs and ordinary text")
	p := newSelectionTestPlugin()
	p.width, p.height = 100, 24
	p.shellSelected = true
	p.shells = []*ShellSession{{TmuxName: "claude", Agent: &Agent{OutputBuf: buffer}}}
	attachLiveTerminal(p, true)

	// An ordinary press over a mouse-reporting pane arms the application's click.
	_ = p.handleMouseClick(actionAt(30, 4))
	if p.pointer.Resolution != tty.ClickForward {
		t.Fatalf("press over a mouse-reporting pane armed %v, want forward", p.pointer.Resolution)
	}

	// The next press lands on a link, which takes the click outright.
	if cmd := p.handleMouseClick(actionAt(4, 4)); cmd == nil {
		t.Fatal("the click on the URL did not activate it")
	}
	if p.pointer.Resolution != tty.ClickNone {
		t.Fatalf("a link-claimed press left %v armed from the gesture before it",
			p.pointer.Resolution)
	}
}
