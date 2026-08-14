package workspace

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
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
	terminalIssueLink
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
	if p.selectingShell() {
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
	return activatableTerminalLinks(terminallink.Scan(line, nil), false)
}

// activatableTerminalLinks keeps the spans this host can act on. Issue spans
// are among them only when issues is true — a td id opens a leaf of the pane
// tree, so without a tree there is nothing to open and an underline would
// promise a click that goes nowhere. The issue-preview modal is not this
// host's route and never was.
func activatableTerminalLinks(spans []terminallink.Span, issues bool) []terminalLink {
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
		case terminallink.KindIssue:
			if !issues {
				continue
			}
			links = append(links, terminalLink{
				Kind:     terminalIssueLink,
				StartCol: span.StartCol,
				EndCol:   span.EndCol,
				Value:    span.Value,
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
	return terminallink.Decorate(line, spansFromTerminalLinks(links))
}

func spansFromTerminalLinks(links []terminalLink) []terminallink.Span {
	spans := make([]terminallink.Span, 0, len(links))
	for _, link := range links {
		span := terminallink.Span{StartCol: link.StartCol, EndCol: link.EndCol, Value: link.Value}
		switch link.Kind {
		case terminalURLLink:
			span.Kind = terminallink.KindURL
		case terminalPathLink:
			span.Kind = terminallink.KindFile
			span.Extra = terminallink.Extra{Line: link.Line, Raw: link.Raw}
		case terminalIssueLink:
			span.Kind = terminallink.KindIssue
		default:
			continue
		}
		spans = append(spans, span)
	}
	return spans
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
	if p.selectingShell() {
		shell := p.getSelectedShell()
		if shell == nil || shell.TmuxName == "" {
			return terminalLinkSurfaceContext{}
		}
		if shell.WorkDir != "" {
			rawRoot = shell.WorkDir
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
	}), true)
	for i := range links {
		if links[i].Kind == terminalPathLink && links[i].Raw != "" {
			links[i].Root = context.root
		}
	}
	p.terminalLinkMemo.surfaces[context.surface] = memo
	return links
}

func stripSourceOSC8(line string) string {
	return terminallink.StripOSC8(line)
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
	if link.Kind == terminalIssueLink {
		return p.activateIssueLink(link.Value)
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
	if shell := p.getSelectedShell(); shell != nil {
		if shell.WorkDir != "" {
			base = shell.WorkDir
		}
	} else {
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
