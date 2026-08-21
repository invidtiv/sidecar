package contentpanes

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// Config supplies only replaceable viewer dependencies. Constructing a Deck
// remains free of filesystem, database, git, and provider work.
type Config struct {
	Renderer         *markdown.Renderer
	ResourceResolver resourceview.Resolver
	// ConfigureViewer attaches host presentation behavior (for example issue
	// navigation handlers or Diff paint state). It must remain free of I/O.
	ConfigureViewer func(kind panelayout.Kind, model any)
}

type viewer interface {
	model() any
	load(SurfaceContext, contentlink.Ref, int) tea.Cmd
	reload(SurfaceContext, contentlink.Ref, int) tea.Cmd
	arm(SurfaceContext, contentlink.Ref, int, TabState)
	focus(SurfaceContext, contentlink.Ref, int) tea.Cmd
	apply(SurfaceContext, any) (tea.Cmd, bool)
	reference(contentlink.Ref) (contentlink.Ref, string)
	snapshot(contentlink.Ref) TabState
}

func newViewer(cfg Config, kind panelayout.Kind) viewer {
	var v viewer
	switch kind {
	case panelayout.Document:
		v = &documentViewer{view: docview.New(cfg.Renderer)}
	case panelayout.Issue:
		v = &issueViewer{view: issueview.New(cfg.Renderer)}
	case panelayout.Diff:
		v = &diffViewer{view: &workspacediff.View{}}
	case panelayout.Resource:
		v = &resourceViewer{view: resourceview.New(cfg.Renderer, cfg.ResourceResolver)}
	default:
		panic("contentpanes: viewer requested for non-content kind")
	}
	if cfg.ConfigureViewer != nil {
		cfg.ConfigureViewer(kind, v.model())
	}
	return v
}

func normalizeRef(ctx SurfaceContext, ref contentlink.Ref) (contentlink.Ref, panelayout.Kind, string, bool) {
	_ = ctx
	ref.Value = strings.TrimSpace(ref.Value)
	switch ref.Kind {
	case contentlink.KindURL:
		safe, ok := contentlink.SafeHTTPURL(ref.Value)
		if !ok {
			return contentlink.Ref{}, panelayout.Primary, "", false
		}
		ref.Value = safe
		return ref, panelayout.Primary, ref.Value, true
	case contentlink.KindInternal:
		raw := (&url.URL{Scheme: "sidecar", Host: ref.Namespace, Path: "/" + ref.Value}).String()
		parsed, err := contentlink.ParseInternalURI(raw)
		if err != nil {
			return contentlink.Ref{}, panelayout.Primary, "", false
		}
		ref = parsed.Ref
		return ref, panelayout.Primary, ref.Namespace + "\x00" + ref.Value, true
	case contentlink.KindFile:
		path := filepath.Clean(ref.Value)
		// Absolute refs are admitted because the host owns path resolution and
		// may deliberately preview a validated file from another worktree in
		// place. Relative traversal is never a valid resolved host reference.
		if !filepath.IsAbs(path) && (path == "." || path == "" || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator))) {
			return contentlink.Ref{}, panelayout.Document, "", false
		}
		ref.Value = filepath.ToSlash(path)
		if ref.Line < 0 {
			ref.Line = 0
		}
		return ref, panelayout.Document, ref.Value, true
	case contentlink.KindIssue:
		ref.Value = issueview.NormalizeID(ref.Value)
		if ref.Value == "" {
			return contentlink.Ref{}, panelayout.Issue, "", false
		}
		return ref, panelayout.Issue, ref.Value, true
	case contentlink.KindDiff:
		target, ok := workspacediff.ParseSpec(ref.Value)
		if !ok {
			return contentlink.Ref{}, panelayout.Diff, "", false
		}
		ref.Value = target.Identity()
		return ref, panelayout.Diff, ref.Value, true
	case contentlink.KindResource:
		rf := resource.Reference{Instance: ref.Provider, Matcher: ref.Matcher, Locator: ref.Value}
		if !rf.Valid() {
			return contentlink.Ref{}, panelayout.Resource, "", false
		}
		return ref, panelayout.Resource, resourceview.TabKey(rf), true
	default:
		return contentlink.Ref{}, panelayout.Primary, "", false
	}
}

type documentViewer struct{ view *docview.Model }

func (v *documentViewer) model() any { return v.view }
func (v *documentViewer) load(ctx SurfaceContext, ref contentlink.Ref, id int) tea.Cmd {
	root := ctx.Root
	// A resolved file reference may deliberately name a regular file outside
	// the source surface (for example ~/.config/sidecar/config.json or another
	// worktree). filepath.Join does not preserve an absolute second argument,
	// so give absolute references no root to join while relative references
	// retain the surface that gives them meaning.
	if filepath.IsAbs(filepath.FromSlash(ref.Value)) {
		root = ""
	}
	cmd := v.view.Load(id, root, ref.Value, ref.Line, ctx.Epoch)
	// Load defaults every line-zero target to rendered. The shared content rule
	// is narrower: only Markdown opens rendered; source and plain text stay raw.
	v.view.SetRendered(terminallink.Markdown(ref.Value) && ref.Line == 0)
	return cmd
}
func (v *documentViewer) loadFile(ctx SurfaceContext, ref contentlink.Ref, id int, file *os.File) tea.Cmd {
	cmd := v.view.LoadFile(id, file, ref.Value, ref.Line, ctx.Epoch)
	v.view.SetRendered(terminallink.Markdown(ref.Value) && ref.Line == 0)
	return cmd
}
func (v *documentViewer) reload(ctx SurfaceContext, ref contentlink.Ref, id int) tea.Cmd {
	if v.view.NeedsLoad() {
		return v.load(ctx, ref, id)
	}
	return v.view.Reload()
}
func (v *documentViewer) arm(ctx SurfaceContext, ref contentlink.Ref, id int, state TabState) {
	v.view.Arm(id, ref.Value, ctx.Epoch)
	v.view.SetRendered(state.Rendered)
	v.view.SetWrap(state.Wrap)
	v.view.SetPendingScroll(state.Scroll)
}
func (v *documentViewer) focus(ctx SurfaceContext, ref contentlink.Ref, id int) tea.Cmd {
	if v.view.NeedsLoad() {
		return v.load(ctx, ref, id)
	}
	v.view.ApplyLine(ref.Line)
	return nil
}
func (v *documentViewer) apply(_ SurfaceContext, msg any) (tea.Cmd, bool) {
	m, ok := msg.(docview.LoadedMsg)
	return nil, ok && v.view.SetResult(m)
}
func (v *documentViewer) reference(ref contentlink.Ref) (contentlink.Ref, string) {
	return ref, ref.Value
}
func (v *documentViewer) snapshot(ref contentlink.Ref) TabState {
	return TabState{Ref: ref, Scroll: v.view.ScrollOffset(), Wrap: v.view.Wrap(), Rendered: v.view.Rendered()}
}

type issueViewer struct{ view *issueview.Model }

func (v *issueViewer) model() any { return v.view }
func (v *issueViewer) load(ctx SurfaceContext, ref contentlink.Ref, id int) tea.Cmd {
	return v.view.Load(id, ctx.Root, ref.Value, ctx.Epoch)
}
func (v *issueViewer) reload(ctx SurfaceContext, ref contentlink.Ref, id int) tea.Cmd {
	return v.load(ctx, ref, id)
}
func (v *issueViewer) arm(ctx SurfaceContext, ref contentlink.Ref, id int, state TabState) {
	v.view.Arm(id, ref.Value, ctx.Epoch)
	v.view.SetPendingScroll(state.Scroll)
}
func (v *issueViewer) focus(ctx SurfaceContext, ref contentlink.Ref, id int) tea.Cmd {
	if v.view.NeedsLoad() {
		return v.load(ctx, ref, id)
	}
	return nil
}
func (v *issueViewer) apply(_ SurfaceContext, msg any) (tea.Cmd, bool) {
	m, ok := msg.(issueview.LoadedMsg)
	return nil, ok && v.view.SetResult(m)
}
func (v *issueViewer) reference(ref contentlink.Ref) (contentlink.Ref, string) {
	return ref, ref.Value
}
func (v *issueViewer) snapshot(ref contentlink.Ref) TabState {
	return TabState{Ref: ref, Scroll: v.view.ScrollOffset()}
}

type diffViewer struct{ view *workspacediff.View }

func (v *diffViewer) model() any { return v.view }
func diffRoot(ctx SurfaceContext) string {
	if ctx.DiffRoot != "" {
		return ctx.DiffRoot
	}
	return ctx.Root
}
func (v *diffViewer) load(ctx SurfaceContext, ref contentlink.Ref, id int) tea.Cmd {
	target, ok := workspacediff.ParseSpec(ref.Value)
	if !ok {
		return nil
	}
	v.view.Target = target
	if target.Kind == workspacediff.TargetCommit {
		v.view.Focus = workspacediff.FocusCommitFiles
	}
	surface := ctx.DiffSurface
	if surface == "" {
		surface = ctx.Surface
	}
	root := diffRoot(ctx)
	v.view.BindGeneration(root, surface, ctx.Epoch, uint64(id))
	v.view.State = workspacediff.LoadStateLoading
	switch target.Kind {
	case workspacediff.TargetWorkingTree:
		return workspacediff.LoadSnapshotCmdBound(root, ctx.BaseRef, surface, ctx.Epoch, target.Identity(), uint64(id))
	case workspacediff.TargetRange:
		return v.view.LoadRange()
	case workspacediff.TargetCommit:
		return v.view.LoadCommit(target.A)
	default:
		return nil
	}
}
func (v *diffViewer) reload(ctx SurfaceContext, ref contentlink.Ref, id int) tea.Cmd {
	return v.load(ctx, ref, id)
}
func (v *diffViewer) arm(ctx SurfaceContext, ref contentlink.Ref, id int, state TabState) {
	target, _ := workspacediff.ParseSpec(ref.Value)
	v.view.Target = target
	surface := ctx.DiffSurface
	if surface == "" {
		surface = ctx.Surface
	}
	root := diffRoot(ctx)
	v.view.BindGeneration(root, surface, ctx.Epoch, uint64(id))
	v.view.State = workspacediff.LoadStateUnknown
	v.view.Scope = workspacediff.ParseScope(state.Scope)
	v.view.ViewMode = workspacediff.ParseViewMode(state.Mode)
	v.view.DiffScroll = max(state.Scroll, 0)
	if state.Path != "" {
		v.view.Target.Path = state.Path
	}
}
func (v *diffViewer) focus(ctx SurfaceContext, ref contentlink.Ref, id int) tea.Cmd {
	if v.view.State == workspacediff.LoadStateUnknown || v.view.State == workspacediff.LoadStateLoading {
		return v.load(ctx, ref, id)
	}
	return nil
}
func (v *diffViewer) apply(ctx SurfaceContext, msg any) (tea.Cmd, bool) {
	expectedSurface := ctx.DiffSurface
	if expectedSurface == "" {
		expectedSurface = ctx.Surface
	}
	accepts := func(epoch uint64, surface, identity string) bool {
		return epoch == ctx.Epoch && surface == expectedSurface && identity == v.view.Target.Identity()
	}
	switch m := msg.(type) {
	case workspacediff.SnapshotMsg:
		if !accepts(m.Epoch, m.WorkspaceID, m.Identity) {
			return nil, false
		}
		return v.view.ApplySnapshotMsg(m, diffRoot(ctx), ctx.Surface), true
	case workspacediff.RangeMsg:
		if !accepts(m.Epoch, m.WorkspaceID, m.Identity) {
			return nil, false
		}
		return v.view.ApplyRangeMsg(m), true
	case workspacediff.CommitDetailMsg:
		if !accepts(m.Epoch, m.WorkspaceID, m.Identity) {
			return nil, false
		}
		return v.view.ApplyCommitDetail(m), true
	case workspacediff.CommitFileDiffMsg:
		if !accepts(m.Epoch, m.WorkspaceID, m.Identity) {
			return nil, false
		}
		return v.view.ApplyCommitFileDiff(m), true
	default:
		return nil, false
	}
}
func (v *diffViewer) reference(ref contentlink.Ref) (contentlink.Ref, string) {
	ref.Value = v.view.Target.Identity()
	return ref, ref.Value
}
func (v *diffViewer) snapshot(ref contentlink.Ref) TabState {
	path := v.view.SelectedFileName()
	return TabState{Ref: ref, Scope: v.view.Scope.Persist(), Mode: v.view.ViewMode.Persist(), Scroll: v.view.DiffScroll, Path: path}
}

type resourceViewer struct{ view *resourceview.Model }

func (v *resourceViewer) model() any { return v.view }
func (v *resourceViewer) load(ctx SurfaceContext, ref contentlink.Ref, id int) tea.Cmd {
	return v.view.Load(id, resourceRef(ref), ctx.Epoch)
}
func (v *resourceViewer) reload(ctx SurfaceContext, ref contentlink.Ref, id int) tea.Cmd {
	return v.load(ctx, ref, id)
}
func (v *resourceViewer) arm(ctx SurfaceContext, ref contentlink.Ref, id int, state TabState) {
	v.view.Arm(id, resourceRef(ref), ctx.Epoch)
	v.view.SetPendingScroll(state.Scroll)
}
func (v *resourceViewer) focus(_ SurfaceContext, _ contentlink.Ref, _ int) tea.Cmd {
	return v.view.Resolve()
}
func (v *resourceViewer) apply(_ SurfaceContext, msg any) (tea.Cmd, bool) {
	m, ok := msg.(resourceview.ResolvedMsg)
	return nil, ok && v.view.Apply(m)
}
func (v *resourceViewer) reference(ref contentlink.Ref) (contentlink.Ref, string) {
	rf := v.view.Reference()
	ref.Provider, ref.Matcher, ref.Value = rf.Instance, rf.Matcher, rf.Locator
	return ref, resourceview.TabKey(rf)
}
func (v *resourceViewer) snapshot(ref contentlink.Ref) TabState {
	rf := v.view.Reference()
	ref.Provider, ref.Matcher, ref.Value = rf.Instance, rf.Matcher, rf.Locator
	return TabState{Ref: ref, Scroll: v.view.Scroll()}
}

func resourceRef(ref contentlink.Ref) resource.Reference {
	return resource.Reference{Instance: ref.Provider, Matcher: ref.Matcher, Locator: ref.Value}
}
