package uiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/layoutapply"
	"github.com/marcus/sidecar/internal/layoutreport"
	"github.com/marcus/sidecar/internal/panecodec"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/viewerlayout"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// viewerLayoutHost adapts the existing all-or-nothing planner, with CSS-pixel
// floors and no TUI, tmux or privileged content path.
type viewerLayoutHost struct {
	s      *Server
	ctx    context.Context
	v      *apiScreen
	tree   *panelayout.Node
	leaves map[int]*state.PaneLayoutJSON
	ack    uirequest.Ack
}

func (s *Server) planViewerRequest(ctx context.Context, v *apiScreen, req uirequest.Request) (*viewerPlan, error) {
	store := viewerlayout.FileStore{Dir: filepath.Join(s.dir, "layouts")}
	doc, etag, err := store.Get(v.caller.client, v.ws.Root)
	if err != nil {
		return nil, err
	}
	if doc.Layout == nil {
		return nil, fmt.Errorf("the origin shell is not on screen, and open/layout requests are never queued; save the displayed layout first")
	}
	h := &viewerLayoutHost{s: s, ctx: ctx, v: v}
	if err := viewerlayout.Validate(doc); err != nil {
		return nil, err
	}
	h.restore(doc.Layout)
	if req.Origin.TmuxSession != "" {
		found := false
		for _, leaf := range h.leaves {
			if (leaf.Kind == panecodec.KindTerminal || leaf.Kind == panecodec.KindShell) && leaf.Session == req.Origin.TmuxSession {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("the origin shell is not on screen, and open/layout requests are never queued")
		}
	}
	var payload uirequest.LayoutPayload
	if req.Action == uirequest.ActionLayout {
		payload, err = uirequest.DecodeLayoutPayload(req.Payload)
	} else {
		kind, ok := panelayout.KindByName(string(req.Target.Kind))
		if !ok || kind == panelayout.Primary || kind == panelayout.Shell {
			return nil, fmt.Errorf("this API viewer does not support %s panes", req.Target.Kind)
		}
		spec := uirequest.LayoutPane{Kind: kind.Name(), Targets: []string{req.Target.Value}, At: req.Options.At}
		targets, reason := h.ResolveTargets(kind, spec)
		if reason != "" {
			return nil, fmt.Errorf("%s", reason)
		}
		targets[0].Line = req.Target.Line
		plan, ok := panelayout.PlanOpenContent(h.tree, kind, 0, h.LastBoxes())
		if !ok {
			return nil, fmt.Errorf("no room for another %s pane", kind.Name())
		}
		if req.Options.At != "" {
			cell, valid := panelayout.ParseCell(req.Options.At)
			if !valid {
				return nil, fmt.Errorf("invalid cell")
			}
			var reason string
			plan, reason = panelayout.PlanOpenAt(h.tree, kind, 0, cell)
			if reason != "" {
				return nil, fmt.Errorf("%s", reason)
			}
		} else {
			plan = panelayout.ApplyAxisOverride(plan, req.Options.Split)
		}
		verdict, reason, _ := h.CommitPassive(targets, plan)
		if reason != "" {
			return nil, fmt.Errorf("%s", reason)
		}
		h.ack.Status = uirequest.Status(verdict)
	}
	if err != nil {
		return nil, err
	}
	if req.Action == uirequest.ActionLayout {
		if payload.Mode == uirequest.LayoutModeGet {
			h.ack.Status = uirequest.StatusOpened
			h.ack.Layout = h.report()
		} else {
			layoutapply.Apply(h, req, payload, v.ws.Root, "browser")
		}
	}
	if h.ack.Status == uirequest.StatusDeclined {
		return nil, fmt.Errorf("%s", h.ack.Reason)
	}
	if req.Action == uirequest.ActionLayout && len(payload.Columns) > 0 {
		live := 0
		primaries := 0
		for _, leaf := range savedLeaves(doc.Layout) {
			if leaf.Kind == "terminal" {
				primaries++
			}
			if leaf.Kind == "terminal" || leaf.Kind == "shell" {
				live++
			}
		}
		if live > panelayout.LiveLeafCap || primaries != 1 {
			return nil, fmt.Errorf("this spec cannot carry all displayed live terminals; layout left unchanged")
		}
	}
	// Revalidate the complete proposed document, including untrusted saved tabs.
	next := LayoutDocument{Layout: h.encode(h.tree)}
	if err := viewerlayout.Validate(next); err != nil {
		return nil, err
	}
	if err := h.validateSaved(next.Layout); err != nil {
		return nil, err
	}
	if _, _, fits := panelayout.LayoutPanes(panelayout.Clone(h.tree), h.box(), h.Floors()); !fits {
		return nil, fmt.Errorf("the composed layout needs a larger window; layout left unchanged")
	}
	h.ack.Instance = req.Viewer
	h.ack.Host = uirequest.HostName()
	h.ack.PID = os.Getpid()
	h.ack.Surface = "browser"
	ttl := time.Duration(req.TTLMs) * time.Millisecond
	if ttl <= 0 || ttl > uirequest.DefaultTTL {
		ttl = uirequest.DefaultTTL
	}
	return &viewerPlan{event: UIRequestEvent{ID: req.ID, Action: req.Action, Project: v.presence.Project, Workspace: v.presence.Workspace, Request: req, Document: next, ETag: etag, ExpiresAt: req.CreatedAt.Add(ttl)}, ack: h.ack, root: v.ws.Root, viewer: v.id}, nil
}
func (h *viewerLayoutHost) restore(layout *state.PaneLayoutJSON) {
	h.leaves = map[int]*state.PaneLayoutJSON{}
	next := 0
	var build func(*state.PaneLayoutJSON) *panelayout.Node
	build = func(j *state.PaneLayoutJSON) *panelayout.Node {
		if j == nil {
			return nil
		}
		next++
		n := &panelayout.Node{ID: next}
		if j.Split != nil {
			axis := panelayout.Columns
			if j.Split.Axis == "rows" {
				axis = panelayout.Rows
			}
			n.Split = &panelayout.Split{Axis: axis, Ratio: j.Split.Ratio, A: build(j.Split.A), B: build(j.Split.B)}
		} else {
			name := j.Kind
			if name == "terminal" {
				name = "primary"
			}
			if name == "doc" {
				name = "file"
			}
			n.Kind, _ = panelayout.KindByName(name)
			data, _ := json.Marshal(j)
			var copy state.PaneLayoutJSON
			_ = json.Unmarshal(data, &copy)
			h.leaves[n.ID] = &copy
		}
		return n
	}
	h.tree = build(layout)
}
func (h *viewerLayoutHost) encode(n *panelayout.Node) *state.PaneLayoutJSON {
	if n == nil {
		return nil
	}
	if n.Split == nil {
		return h.leaves[n.ID]
	}
	axis := "cols"
	if n.Split.Axis == panelayout.Rows {
		axis = "rows"
	}
	return &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{Axis: axis, Ratio: n.Split.Ratio, A: h.encode(n.Split.A), B: h.encode(n.Split.B)}}
}
func (h *viewerLayoutHost) validateSaved(n *state.PaneLayoutJSON) error {
	if n == nil {
		return nil
	}
	if n.Split != nil {
		if err := h.validateSaved(n.Split.A); err != nil {
			return err
		}
		return h.validateSaved(n.Split.B)
	}
	var spec uirequest.LayoutPane
	switch n.Kind {
	case "terminal", "shell":
		return nil
	case "doc":
		spec.Kind = "file"
		for _, t := range n.Tabs {
			spec.Targets = append(spec.Targets, t.Path)
		}
	case "issue":
		spec.Kind = "issue"
		for _, t := range n.IssueTabs {
			spec.Targets = append(spec.Targets, t.Issue)
		}
	case "note":
		spec.Kind = "note"
		for _, t := range n.NoteTabs {
			spec.Targets = append(spec.Targets, t.Note)
		}
	case "diff":
		spec.Kind = "diff"
		for _, t := range n.DiffTabs {
			spec.Targets = append(spec.Targets, t.Spec)
		}
	default:
		return fmt.Errorf("this API viewer does not support %s panes", n.Kind)
	}
	kind, _ := panelayout.KindByName(spec.Kind)
	_, reason := h.resolveTargets(kind, spec, true)
	if reason != "" {
		return fmt.Errorf("%s", reason)
	}
	return nil
}
func (h *viewerLayoutHost) ResolveTargets(kind panelayout.Kind, spec uirequest.LayoutPane) ([]uirequest.Target, string) {
	return h.resolveTargets(kind, spec, false)
}
func (h *viewerLayoutHost) resolveTargets(kind panelayout.Kind, spec uirequest.LayoutPane, boundaryOnly bool) ([]uirequest.Target, string) {
	wire, ok := layoutapply.WireKind(kind)
	if !ok || kind == panelayout.Resource {
		return nil, "this API viewer does not support resource panes"
	}
	if kind == panelayout.Diff && len(spec.Targets) == 0 {
		spec.Targets = []string{"working-tree"}
	}
	if len(spec.Targets) == 0 || len(spec.Targets) > 64 {
		return nil, "a content pane needs 1..64 targets"
	}
	out := []uirequest.Target{}
	for _, raw := range spec.Targets {
		target, line := layoutapply.SplitFileLine(raw)
		if kind != panelayout.Document {
			target = raw
			line = 0
		}
		if kind == panelayout.Note {
			target = strings.TrimPrefix(target, "sidecar://note/")
		}
		if kind == panelayout.Document && filepath.IsAbs(target) {
			rel, err := filepath.Rel(h.v.ws.Root, target)
			if err != nil {
				return nil, err.Error()
			}
			target = rel
		}
		p := contentservice.ReadParams{Kind: string(wire), Target: target}
		switch kind {
		case panelayout.Document:
			p.Operation = contentservice.OpDocument
		case panelayout.Issue:
			p.Operation = contentservice.OpCard
		case panelayout.Note:
			p.Operation = contentservice.OpNote
		case panelayout.Diff:
			spec, ok := workspacediff.ParseSpec(target)
			if !ok {
				return nil, "not a git spec"
			}
			p.Operation = contentservice.OpWorkingTree
			if spec.Kind == workspacediff.TargetCommit {
				p.Operation = contentservice.OpCommit
			}
			if spec.Kind == workspacediff.TargetRange {
				p.Operation = contentservice.OpRange
			}
		}
		var err error
		if source, ok := h.s.contentBackend().(ContentWatchSource); ok && boundaryOnly {
			_, err = source.WatchProject(h.ctx, h.v.presence.Project, h.v.presence.Workspace, p)
		} else {
			_, err = h.s.contentBackend().ReadProject(h.ctx, h.v.presence.Project, h.v.presence.Workspace, p)
		}
		if err != nil {
			return nil, err.Error()
		}
		out = append(out, uirequest.Target{Kind: wire, Value: target, Line: line})
	}
	return out, ""
}
func (h *viewerLayoutHost) box() panelayout.Box {
	return panelayout.Box{W: h.v.presence.Viewport.Width, H: h.v.presence.Viewport.Height}
}
func (h *viewerLayoutHost) PaneRoot() *panelayout.Node { return h.tree }
func (h *viewerLayoutHost) LastBoxes() map[int]panelayout.Box {
	return layoutreport.LiveBoxes(h.tree, h.box(), h.Floors())
}
func (h *viewerLayoutHost) PeerBox() (panelayout.Box, bool) { return h.box(), true }
func (h *viewerLayoutHost) Floors() panelayout.Floors {
	f := panelayout.Floor{Width: 160, Height: 96}
	return panelayout.Floors{Primary: f, Shell: f, Doc: f, Issue: f, Note: f, Diff: f, Resource: f}
}
func (h *viewerLayoutHost) EnsureDeck()                {}
func (h *viewerLayoutHost) DeckTree() *panelayout.Node { return panelayout.Clone(h.tree) }
func (h *viewerLayoutHost) TerminalEnabled() bool      { return false }
func (h *viewerLayoutHost) TerminalOffReason() string {
	return "create a shell through the workspace API first, then carry its displayed session in a layout spec"
}
func (h *viewerLayoutHost) ShellCapMessage() string { return panelayout.LiveCapMessage }
func (h *viewerLayoutHost) ShellVisible() bool {
	return panelayout.FirstOfKind(h.tree, panelayout.Shell) != nil
}
func (h *viewerLayoutHost) SplitOrigin() string          { return h.v.presence.Session }
func (h *viewerLayoutHost) TermPanelSessionName() string { return "" }
func (h *viewerLayoutHost) LiveShellSessions() map[string]bool {
	out := map[string]bool{}
	for _, j := range h.leaves {
		if j.Kind == "shell" {
			out[j.Session] = true
		}
	}
	return out
}
func (h *viewerLayoutHost) FocusedLeaf() int { return h.v.presence.FocusedPane }
func (h *viewerLayoutHost) CommitMove(plan panelayout.MovePlan) (string, tea.Cmd) {
	h.tree, _ = panelayout.ApplyMove(h.tree, plan)
	return "", nil
}
func (h *viewerLayoutHost) CommitPassive(targets []uirequest.Target, plan panelayout.OpenPlan) (string, string, tea.Cmd) {
	kind, _ := panelayout.KindByName(string(targets[0].Kind))
	leaf := layoutapply.SpecLeafJSON(&layoutapply.ItemPlan{Kind: kind, Targets: targets})
	if plan.Retarget != 0 {
		existing := h.leaves[plan.Retarget]
		// Tabs retain the existing ones and select the last requested tab.
		switch kind {
		case panelayout.Document:
			existing.Tabs, existing.Active = retargetViewerTabs(existing.Tabs, leaf.Tabs, func(t state.PaneDocTabJSON) string { return t.Path })
		case panelayout.Issue:
			existing.IssueTabs, existing.Active = retargetViewerTabs(existing.IssueTabs, leaf.IssueTabs, func(t state.PaneIssueTabJSON) string { return t.Issue })
		case panelayout.Note:
			existing.NoteTabs, existing.Active = retargetViewerTabs(existing.NoteTabs, leaf.NoteTabs, func(t state.PaneNoteTabJSON) string { return t.Note })
		case panelayout.Diff:
			existing.DiffTabs, existing.Active = retargetViewerTabs(existing.DiffTabs, leaf.DiffTabs, func(t state.PaneDiffTabJSON) string { return t.Spec })
		}
		return uirequest.ItemVerdictRetargeted, "", nil
	}
	var id int
	h.tree, id = panelayout.ApplyPlan(h.tree, plan, &panelayout.Node{Kind: kind})
	h.leaves[id] = leaf
	return uirequest.ItemVerdictOpened, "", nil
}
func (h *viewerLayoutHost) CommitShell(uirequest.LayoutPane, panelayout.OpenPlan) (string, string, tea.Cmd) {
	return uirequest.ItemVerdictDeclined, h.TerminalOffReason(), nil
}
func (h *viewerLayoutHost) RestoreSpec(layout *state.PaneLayoutJSON) tea.Cmd {
	layout.Root = ""
	layout.Surface = ""
	layout.Open = false
	// The primary spec means the existing primary, including its session.
	var primary *state.PaneLayoutJSON
	for _, j := range h.leaves {
		if j.Kind == "terminal" {
			primary = j
			break
		}
	}
	var carry func(*state.PaneLayoutJSON)
	carry = func(j *state.PaneLayoutJSON) {
		if j == nil {
			return
		}
		if j.Split != nil {
			carry(j.Split.A)
			carry(j.Split.B)
		} else if j.Kind == "terminal" && primary != nil {
			j.Session = primary.Session
		}
	}
	carry(layout)
	h.restore(layout)
	return nil
}
func (h *viewerLayoutHost) AdoptSpecShell(uirequest.LayoutPane) (string, string, tea.Cmd) {
	return uirequest.ItemVerdictDeclined, h.TerminalOffReason(), nil
}
func (h *viewerLayoutHost) AfterSpecCommit() {}
func (h *viewerLayoutHost) LandedLeaf(kind panelayout.Kind) int {
	n := panelayout.FirstOfKind(h.tree, kind)
	if n == nil {
		return 0
	}
	return n.ID
}
func (h *viewerLayoutHost) Ack(_ uirequest.Request, status uirequest.Status, reason string, items []uirequest.AckItem, layout json.RawMessage) {
	h.ack.Status = status
	h.ack.Reason = reason
	h.ack.Items = items
	h.ack.Layout = layout
	if len(items) > 0 {
		h.ack.ItemsVersion = 1
	}
}
func (h *viewerLayoutHost) report() json.RawMessage {
	return layoutreport.Build(layoutreport.Source{Surface: "browser", Root: h.v.ws.Root, Tree: h.tree, Layout: h.encode(h.tree), LeafLayouts: h.leaves, CSSViewport: &h.v.presence.Viewport, FloorsUnit: "css_pixels", Floors: h.Floors()})
}

func savedLeaves(j *state.PaneLayoutJSON) []*state.PaneLayoutJSON {
	if j == nil {
		return nil
	}
	if j.Split == nil {
		return []*state.PaneLayoutJSON{j}
	}
	return append(savedLeaves(j.Split.A), savedLeaves(j.Split.B)...)
}

// Retargeting an already open reference selects its tab and preserves its view.
func retargetViewerTabs[T any](current, incoming []T, key func(T) string) ([]T, int) {
	active := 0
	for _, tab := range incoming {
		found := -1
		for i, existing := range current {
			if key(existing) == key(tab) {
				found = i
				break
			}
		}
		if found < 0 {
			current = append(current, tab)
			found = len(current) - 1
		}
		active = found
	}
	return current, active
}
