package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugins/filebrowser"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

type terminalLinkKind int

const (
	terminalURLLink terminalLinkKind = iota + 1
	terminalPathLink
)

type terminalLink struct {
	Kind     terminalLinkKind
	StartCol int
	EndCol   int
	Value    string
	Line     int
	Root     string // canonical selected surface root for resolved bare paths
	Raw      string // original candidate, revalidated on activation
}

type terminalLinkMemo struct {
	surfaces map[string]terminalLinkSurfaceMemo
}

type terminalLinkSurfaceMemo struct {
	rawRoot  string
	root     string
	target   string
	buffer   *tty.OutputBuffer
	revision uint64
	paths    map[string]terminalLinkResolution
}

func (p *Plugin) terminalLinkTarget(termPanel bool) string {
	if termPanel {
		return p.termPanelSession + "\x00" + p.termPanelPaneID
	}
	if p.shellSelected {
		if shell := p.getSelectedShell(); shell != nil && shell.Agent != nil {
			return shell.Agent.TmuxSession + "\x00" + shell.Agent.TmuxPane
		}
		return ""
	}
	if wt := p.selectedWorktree(); wt != nil && wt.Agent != nil {
		return wt.Agent.TmuxSession + "\x00" + wt.Agent.TmuxPane
	}
	return ""
}

type terminalLinkResolution struct {
	rel string
	ok  bool
}

type terminalLineLinkResolver struct {
	plugin  *Plugin
	context terminalLinkSurfaceContext
	buffer  *tty.OutputBuffer
}

func (r *terminalLineLinkResolver) links(line string) []terminalLink {
	return r.plugin.resolvedTerminalLinks(r.context, r.buffer, line)
}

type terminalLinkSurfaceContext struct {
	rawRoot string
	root    string
	surface string
	target  string
	ok      bool
}

func safeHTTPURL(raw string) (string, bool) {
	return terminallink.SafeHTTPURL(raw)
}

func detectTerminalLinks(line string) []terminalLink {
	return activatableTerminalLinks(terminallink.Scan(line, nil))
}

// activatableTerminalLinks keeps url and file spans. Issue spans are parsed
// so a later split can bind them to a td pane; this host ignores the kind and
// must not open the issue-preview modal.
func activatableTerminalLinks(spans []terminallink.Span) []terminalLink {
	links := make([]terminalLink, 0, len(spans))
	for _, span := range spans {
		switch span.Kind {
		case terminallink.KindURL:
			links = append(links, terminalLink{
				Kind:     terminalURLLink,
				StartCol: span.StartCol,
				EndCol:   span.EndCol,
				Value:    span.Value,
			})
		case terminallink.KindFile:
			links = append(links, terminalLink{
				Kind:     terminalPathLink,
				StartCol: span.StartCol,
				EndCol:   span.EndCol,
				Value:    span.Value,
				Line:     span.Extra.Line,
				Raw:      span.Extra.Raw,
			})
		}
	}
	return links
}

func decorateTerminalLinks(line string, resolved *terminalLineLinkResolver) string {
	// tmux output is untrusted. Remove source-supplied OSC controls and
	// synthesize OSC-8 only for URLs that pass safeHTTPURL.
	line = stripSourceOSC8(line)
	links := detectTerminalLinks(line)
	if resolved != nil {
		links = resolved.links(line)
	}
	// Apply from right to left so wrappers do not disturb later visual ranges.
	for i := len(links) - 1; i >= 0; i-- {
		link := links[i]
		open, close := "\x1b[4m", "\x1b[24m"
		if link.Kind == terminalURLLink {
			open = "\x1b]8;;" + link.Value + "\x1b\\\x1b[4m"
			close = "\x1b[24m\x1b]8;;\x1b\\"
		}
		line = wrapTerminalVisualRange(line, link.StartCol, link.EndCol, open, close)
	}
	return line
}

func (p *Plugin) terminalLinkResolver(termPanel bool, buffer *tty.OutputBuffer) *terminalLineLinkResolver {
	if p.paneRoot == nil || buffer == nil {
		return nil
	}
	context := p.terminalLinkSurfaceContext(termPanel)
	if !context.ok {
		return nil
	}
	return &terminalLineLinkResolver{plugin: p, context: context, buffer: buffer}
}

func (p *Plugin) terminalLinkSurfaceContext(termPanel bool) terminalLinkSurfaceContext {
	return p.terminalLinkSurfaceContextWithFreshRoot(termPanel, false)
}

func (p *Plugin) terminalLinkSurfaceContextWithFreshRoot(termPanel, freshRoot bool) terminalLinkSurfaceContext {
	if p.ctx == nil {
		return terminalLinkSurfaceContext{}
	}
	rawRoot := p.ctx.WorkDir
	surface := ""
	if p.shellSelected {
		shell := p.getSelectedShell()
		if shell == nil || shell.TmuxName == "" {
			return terminalLinkSurfaceContext{}
		}
		surface = "shell:" + shell.TmuxName
	} else {
		wt := p.selectedWorktree()
		if wt == nil {
			return terminalLinkSurfaceContext{}
		}
		rawRoot = wt.Path
		surface = "workspace:" + stablePathKey(wt.Path)
	}
	if termPanel {
		surface += ":panel"
	}
	target := p.terminalLinkTarget(termPanel)
	if !freshRoot && p.terminalLinkMemo.surfaces != nil {
		if memo, found := p.terminalLinkMemo.surfaces[surface]; found &&
			memo.rawRoot == filepath.Clean(rawRoot) && memo.target == target && memo.root != "" {
			return terminalLinkSurfaceContext{rawRoot: memo.rawRoot, root: memo.root, surface: surface, target: target, ok: true}
		}
	}
	rootResolver := filepath.EvalSymlinks
	if p.terminalRootResolver != nil {
		rootResolver = p.terminalRootResolver
	}
	root, err := rootResolver(rawRoot)
	if err != nil {
		return terminalLinkSurfaceContext{}
	}
	return terminalLinkSurfaceContext{rawRoot: filepath.Clean(rawRoot), root: filepath.Clean(root), surface: surface, target: target, ok: true}
}

func (p *Plugin) invalidateTerminalLinkSurface(surface string) {
	if p.terminalLinkMemo.surfaces != nil {
		delete(p.terminalLinkMemo.surfaces, surface)
	}
}

func (p *Plugin) resolvedTerminalLinks(context terminalLinkSurfaceContext, buffer *tty.OutputBuffer, line string) []terminalLink {
	if p.paneRoot == nil || buffer == nil || !context.ok {
		return detectTerminalLinks(line)
	}
	revision := buffer.Revision()
	if p.terminalLinkMemo.surfaces == nil {
		p.terminalLinkMemo.surfaces = make(map[string]terminalLinkSurfaceMemo)
	}
	memo, found := p.terminalLinkMemo.surfaces[context.surface]
	if !found || memo.root != context.root || memo.target != context.target || memo.buffer != buffer || memo.revision != revision {
		memo = terminalLinkSurfaceMemo{rawRoot: context.rawRoot, root: context.root, target: context.target, buffer: buffer, revision: revision,
			paths: make(map[string]terminalLinkResolution)}
	}
	resolver := resolveTerminalPathFromResolvedBase
	if p.terminalPathResolver != nil {
		resolver = p.terminalPathResolver
	}
	links := activatableTerminalLinks(terminallink.Scan(line, func(raw string) (string, terminallink.Extra, bool) {
		resolution, found := memo.paths[raw]
		if !found {
			rel, _, ok := resolver(context.root, raw)
			resolution = terminalLinkResolution{rel: rel, ok: ok}
			memo.paths[raw] = resolution
		}
		if !resolution.ok {
			return "", terminallink.Extra{}, false
		}
		return resolution.rel, terminallink.Extra{Raw: raw}, true
	}))
	for i := range links {
		if links[i].Kind == terminalPathLink && links[i].Raw != "" {
			links[i].Root = context.root
		}
	}
	p.terminalLinkMemo.surfaces[context.surface] = memo
	return links
}

func stripSourceOSC8(line string) string {
	out := make([]byte, 0, len(line))
	inOSC := false
	for pos := 0; pos < len(line); {
		if inOSC {
			if terminatorLen := oscTerminatorLen(line, pos); terminatorLen > 0 {
				pos += terminatorLen
				inOSC = false
				continue
			}
			if introLen := oscIntroducerLen(line, pos); introLen > 0 {
				// Real terminal parsers restart OSC parsing on a nested
				// introducer. Remain in the discard state so the nested
				// payload cannot become an active hyperlink.
				pos += introLen
				continue
			}
			_, size := utf8.DecodeRuneInString(line[pos:])
			pos += size
			continue
		}

		if introLen := oscIntroducerLen(line, pos); introLen > 0 {
			pos += introLen
			inOSC = true
			continue
		}
		_, size := utf8.DecodeRuneInString(line[pos:])
		segment := line[pos : pos+size]
		if segment[0] == ']' {
			// Removing an intervening OSC must not concatenate an ordinary
			// trailing ESC with a later ']' into a fresh OSC introducer.
			for len(out) > 0 && out[len(out)-1] == '\x1b' {
				out = out[:len(out)-1]
			}
		}
		out = append(out, segment...)
		pos += size
	}
	cleaned := string(out)
	if containsSourceOSCIntroducer(cleaned) {
		// The scan removes variable-length controls. Fail closed if bytes on
		// either side of a removal ever concatenate into a new OSC introducer.
		return ""
	}
	return cleaned
}

func oscIntroducerLen(value string, pos int) int {
	switch {
	case pos+1 < len(value) && value[pos] == '\x1b' && value[pos+1] == ']':
		return 2
	case value[pos] == '\x9d':
		return 1
	case pos+1 < len(value) && value[pos] == '\xc2' && value[pos+1] == '\x9d':
		return 2
	default:
		return 0
	}
}

func oscTerminatorLen(value string, pos int) int {
	switch {
	case value[pos] == '\x07' || value[pos] == '\x9c':
		return 1
	case pos+1 < len(value) && value[pos] == '\x1b' && value[pos+1] == '\\':
		return 2
	case pos+1 < len(value) && value[pos] == '\xc2' && value[pos+1] == '\x9c':
		return 2
	default:
		return 0
	}
}

func containsSourceOSCIntroducer(value string) bool {
	for pos := 0; pos < len(value); {
		if oscIntroducerLen(value, pos) > 0 {
			return true
		}
		_, size := utf8.DecodeRuneInString(value[pos:])
		pos += size
	}
	return false
}

func wrapTerminalVisualRange(line string, startCol, endCol int, open, close string) string {
	var out strings.Builder
	state := ansi.NormalState
	col := 0
	wrapping := false
	for len(line) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(line, state, nil)
		if n <= 0 {
			out.WriteString(line)
			break
		}
		inRange := width > 0 && col >= startCol && col <= endCol
		if inRange && !wrapping {
			out.WriteString(open)
			wrapping = true
		} else if !inRange && wrapping && width > 0 {
			out.WriteString(close)
			wrapping = false
		}
		out.WriteString(seq)
		col += width
		state = newState
		line = line[n:]
	}
	if wrapping {
		out.WriteString(close)
	}
	return out.String()
}

func (p *Plugin) terminalLinkAt(action mouse.MouseAction) (terminalLink, terminalLinkSurfaceContext, bool, bool) {
	point, line, ok := p.terminalPointAndLine(action)
	if !ok {
		return terminalLink{}, terminalLinkSurfaceContext{}, false, false
	}
	termPanel := action.Region != nil && action.Region.ID == regionTermPanelContent
	buffer := p.terminalOutputBuffer(termPanel)
	context := p.terminalLinkSurfaceContext(termPanel)
	for _, link := range p.resolvedTerminalLinks(context, buffer, ui.ExpandTabs(line, tabStopWidth)) {
		if point.Col >= link.StartCol && point.Col <= link.EndCol {
			return link, context, termPanel, true
		}
	}
	return terminalLink{}, context, termPanel, false
}

func (p *Plugin) activateTerminalLink(action mouse.MouseAction) (tea.Cmd, bool) {
	link, context, termPanel, ok := p.terminalLinkAt(action)
	if !ok {
		return nil, false
	}
	return p.activateResolvedTerminalLink(link, context, termPanel)
}

func (p *Plugin) activateResolvedTerminalLink(link terminalLink, context terminalLinkSurfaceContext, termPanel bool) (tea.Cmd, bool) {
	if link.Kind == terminalURLLink {
		p.clearTerminalSelection()
		return openInBrowser(link.Value), true
	}
	if link.Kind != terminalPathLink {
		return nil, false
	}
	raw := link.Raw
	if raw == "" {
		raw = link.Value
	}
	root := link.Root
	if root == "" {
		root = context.root
	}
	if link.Root != "" {
		fresh := p.terminalLinkSurfaceContextWithFreshRoot(termPanel, true)
		if !fresh.ok || fresh.surface != context.surface || fresh.target != context.target || fresh.root != link.Root {
			p.invalidateTerminalLinkSurface(context.surface)
			return nil, false
		}
		root = fresh.root
	}
	if root == "" {
		cmd := p.openTerminalPath(raw, link.Line)
		if cmd != nil {
			p.clearTerminalSelection()
		}
		return cmd, cmd != nil
	}
	display, abs, ok := resolveTerminalPathFromResolvedBase(root, raw)
	if !ok {
		return nil, false
	}
	surface := strings.TrimSuffix(context.surface, ":panel")
	cmd := p.openResolvedFilePreview(root, surface, display, abs, link.Line)
	if cmd != nil {
		p.clearTerminalSelection()
	}
	return cmd, cmd != nil
}

func (p *Plugin) openResolvedFilePreview(root, surface, display, abs string, line int) tea.Cmd {
	var file *os.File
	var err error
	if display != "" && !filepath.IsAbs(filepath.FromSlash(display)) {
		file, err = openContainedRegularFile(root, display)
	} else {
		file, err = terminallink.OpenRegular(abs)
	}
	if err != nil {
		return nil
	}
	if p.paneRoot == nil {
		_ = file.Close()
		return p.openFileBrowserIfCurrentProject(root, display, line)
	}
	return p.openDocPaneFileForSurface(root, surface, display, line, file)
}

func (p *Plugin) openFileBrowserIfCurrentProject(root, display string, line int) tea.Cmd {
	if p.ctx == nil || display == "" || filepath.IsAbs(filepath.FromSlash(display)) {
		return nil
	}
	ctxResolved, err := filepath.EvalSymlinks(p.ctx.WorkDir)
	if err != nil {
		ctxResolved = filepath.Clean(p.ctx.WorkDir)
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	if filepath.Clean(rootResolved) != filepath.Clean(ctxResolved) {
		return nil
	}
	return tea.Batch(app.FocusPlugin("file-browser"), func() tea.Msg {
		return filebrowser.NavigateToFileMsg{Path: filepath.ToSlash(display), Line: line}
	})
}

func (p *Plugin) openTerminalPath(raw string, line int) tea.Cmd {
	if p.ctx == nil {
		return nil
	}
	base := p.ctx.WorkDir
	if !p.shellSelected {
		if wt := p.selectedWorktree(); wt != nil {
			base = wt.Path
		}
	}
	display, abs, ok := resolveTerminalPath(base, raw)
	if !ok {
		return nil
	}
	baseResolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		baseResolved = filepath.Clean(base)
	}
	_, surface, _ := p.selectedTerminalSurface()
	return p.openResolvedFilePreview(filepath.Clean(baseResolved), surface, display, abs, line)
}

func resolveTerminalPath(base, raw string) (relative, absolute string, ok bool) {
	baseResolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", "", false
	}
	return resolveTerminalPathFromResolvedBase(baseResolved, raw)
}

func resolveTerminalPathFromResolvedBase(baseResolved, raw string) (relative, absolute string, ok bool) {
	return terminallink.ResolveFile(baseResolved, raw)
}
