package workspace

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/layoutapply"
	"github.com/marcus/sidecar/internal/layoutreport"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// The `sidecar layout` host side. Get answers with a report built from THIS
// surface's tree. Apply is the shared all-or-nothing verdict path in
// layoutapply, committing through this plugin's ordinary open / split paths.
//
// Neither mode ever queues: when the origin shell is not on screen, both
// decline and say so.
const layoutNotOnScreenReason = layoutapply.NotOnScreenReason

type layoutItemPlan = layoutapply.ItemPlan
type layoutPaneJSON = layoutreport.Pane

func (p *Plugin) applyLayoutRequest(req uirequest.Request) tea.Cmd {
	if req.Origin.Sessions {
		return nil
	}
	payload, err := uirequest.DecodeLayoutPayload(req.Payload)
	if err != nil {
		return p.ackLayout(req, uirequest.StatusDeclined, "invalid layout payload: "+err.Error(), nil, nil)
	}

	if req.Origin.TmuxSession == "" {
		if req.Origin.ProjectKey == "" || !p.matchesProjectTarget(req) {
			return nil
		}
		root, surface, ok := p.selectedTerminalSurface()
		if !ok {
			return p.ackLayout(req, uirequest.StatusDeclined, layoutNotOnScreenReason, nil, nil)
		}
		return p.answerLayout(req, payload, root, surface)
	}

	var targetShell *ShellSession
	for _, sh := range p.shells {
		if sh.TmuxName == req.Origin.TmuxSession {
			targetShell = sh
			break
		}
	}
	if targetShell == nil {
		return nil
	}
	root, surface, ok := p.selectedTerminalSurface()
	isSelected := ok && surface == "shell:"+targetShell.TmuxName
	if !isSelected {
		return p.ackLayout(req, uirequest.StatusDeclined, layoutNotOnScreenReason, nil, nil)
	}
	return p.answerLayout(req, payload, root, surface)
}

func (p *Plugin) answerLayout(req uirequest.Request, payload uirequest.LayoutPayload, root, surface string) tea.Cmd {
	if payload.Mode == uirequest.LayoutModeGet {
		report := p.buildLayoutReport(root, surface)
		return p.ackLayout(req, uirequest.StatusOpened, "", nil, report)
	}
	return layoutapply.Apply(workspaceLayoutHost{p: p, req: req, root: root, surface: surface}, req, payload, root, surface)
}

func (p *Plugin) ackLayout(req uirequest.Request, status uirequest.Status, reason string, items []uirequest.AckItem, layout json.RawMessage) tea.Cmd {
	if req.Origin.HostID == "" {
		layoutapply.WriteAck(config.StateDir(), hostInstanceID(), req, status, reason, items, layout)
		return nil
	}
	p.ackRemote(req, status, reason, "", 0, layout, items)
	return nil
}

type workspaceLayoutHost struct {
	p             *Plugin
	req           uirequest.Request
	root, surface string
}

func (h workspaceLayoutHost) PaneRoot() *panelayout.Node { return h.p.paneRoot }
func (h workspaceLayoutHost) LastBoxes() map[int]panelayout.Box {
	return h.p.lastPaneBoxes()
}
func (h workspaceLayoutHost) PeerBox() (panelayout.Box, bool) {
	return h.p.previewPeerBox()
}
func (h workspaceLayoutHost) Floors() panelayout.Floors { return paneTreeFloors() }
func (h workspaceLayoutHost) EnsureDeck() {
	h.p.ensureWorkspaceDeck(h.root, h.surface)
}
func (h workspaceLayoutHost) DeckTree() *panelayout.Node {
	if h.p.contentDeck == nil {
		return nil
	}
	return h.p.contentDeck.Tree()
}
func (h workspaceLayoutHost) TerminalEnabled() bool { return terminalPanelEnabled() }
func (h workspaceLayoutHost) TerminalOffReason() string {
	return features.WorkspaceTerminalPanel.Name + " is off"
}
func (h workspaceLayoutHost) ShellCapMessage() string { return shellCapMessage }
func (h workspaceLayoutHost) ShellVisible() bool      { return h.p.shellLeafVisible() }
func (h workspaceLayoutHost) SplitOrigin() string     { return h.req.Origin.TmuxSession }
func (h workspaceLayoutHost) TermPanelSessionName() string {
	return h.p.termPanelSessionName()
}
func (h workspaceLayoutHost) LiveShellSessions() map[string]bool {
	return h.p.liveShellSessions()
}
func (h workspaceLayoutHost) FocusedLeaf() int { return h.p.layoutMoveFocusedLeaf() }
func (h workspaceLayoutHost) CommitMove(plan panelayout.MovePlan) (string, tea.Cmd) {
	return h.p.commitLayoutMove(plan)
}
func (h workspaceLayoutHost) ResolveTargets(kind panelayout.Kind, spec uirequest.LayoutPane) ([]uirequest.Target, string) {
	return h.p.resolveLayoutTargets(kind, spec, h.root)
}
func (h workspaceLayoutHost) CommitPassive(targets []uirequest.Target, plan panelayout.OpenPlan) (string, string, tea.Cmd) {
	if len(targets) == 0 {
		return uirequest.ItemVerdictDeclined, "a pane needs at least one target", nil
	}
	outcome, cmd := h.p.performPlannedOpen(targets[0], h.root, h.surface, plan)
	verdict, reason := string(outcome.status), outcome.reason
	if outcome.status == uirequest.StatusDeclined {
		return verdict, reason, cmd
	}
	for _, extra := range targets[1:] {
		h.p.performTargetOpen(uirequest.Request{
			Action: uirequest.ActionOpen,
			Origin: h.req.Origin,
			Target: extra,
		}, h.root, h.surface)
	}
	return verdict, reason, cmd
}
func (h workspaceLayoutHost) CommitShell(spec uirequest.LayoutPane, plan panelayout.OpenPlan) (string, string, tea.Cmd) {
	return h.p.commitLayoutShell(spec, plan, h.req.Origin.TmuxSession)
}
func (h workspaceLayoutHost) RestoreSpec(layout *state.PaneLayoutJSON) tea.Cmd {
	cmd := h.p.restorePaneLayout(layout)
	h.p.contentDeck = nil
	h.p.hiddenPaneLayout = nil
	return cmd
}
func (h workspaceLayoutHost) AdoptSpecShell(spec uirequest.LayoutPane) (string, string, tea.Cmd) {
	item := layoutapply.ItemPlan{Spec: spec, Kind: panelayout.Shell}
	return h.p.adoptSpecShellLeaf(&item)
}
func (h workspaceLayoutHost) AfterSpecCommit() { h.p.saveSelectionState() }
func (h workspaceLayoutHost) LandedLeaf(kind panelayout.Kind) int {
	return h.p.landedLeaf(kind)
}
func (h workspaceLayoutHost) Ack(req uirequest.Request, status uirequest.Status, reason string, items []uirequest.AckItem, layout json.RawMessage) {
	h.p.ackLayout(req, status, reason, items, layout)
}

func (p *Plugin) resolveLayoutTargets(kind panelayout.Kind, spec uirequest.LayoutPane, root string) ([]uirequest.Target, string) {
	if p.remoteBound() {
		return p.resolveRemoteLayoutTargets(kind, spec)
	}
	return layoutapply.ResolveTargets(kind, spec, root, p.resourceMatchers)
}

func (p *Plugin) resolveRemoteLayoutTargets(kind panelayout.Kind, spec uirequest.LayoutPane) ([]uirequest.Target, string) {
	if kind == panelayout.Resource {
		return layoutapply.ResolveTargets(kind, spec, "", p.resourceMatchers)
	}
	if len(spec.Targets) == 0 {
		if kind == panelayout.Diff {
			spec.Targets = []string{workspacediff.IdentityWorkingTree}
		} else {
			return nil, "a " + kind.Name() + " pane needs at least one target"
		}
	}
	want, ok := layoutapply.WireKind(kind)
	if !ok {
		return nil, "unsupported pane kind " + kind.Name()
	}
	pendingKind, ok := remoteLayoutPendingKind(kind)
	if !ok {
		return nil, "unsupported pane kind " + kind.Name()
	}
	src := p.documentSource()
	var sourceCtx contentpanes.SourceContext
	if root, surface, ok := p.selectedTerminalSurface(); ok {
		sourceCtx = p.workspaceSourceContext(root, surface)
	}
	targets := make([]uirequest.Target, 0, len(spec.Targets))
	for _, raw := range spec.Targets {
		raw = strings.TrimSpace(raw)
		line := 0
		if kind == panelayout.Document {
			raw, line = splitLayoutFileLine(raw)
		}
		if kind == panelayout.Diff && raw == "" {
			raw = workspacediff.IdentityWorkingTree
		}
		ref, err := contentpanes.ResolveDocument(src, sourceCtx, contentlink.Pending{Kind: pendingKind, Raw: raw})
		if err != nil {
			return nil, fmt.Sprintf("target %q: %v", raw, err)
		}
		if ref.Value == "" {
			host := sourceCtx.HostID
			if host == "" {
				host = "that host"
			}
			return nil, fmt.Sprintf("target %q: not found on %s", raw, host)
		}
		tgt := remoteLayoutTarget(ref, line)
		if tgt.Kind != want {
			return nil, fmt.Sprintf("target %q resolves to a %s pane, want %s", raw, string(tgt.Kind), kind.Name())
		}
		targets = append(targets, tgt)
	}
	return targets, ""
}

func remoteLayoutPendingKind(kind panelayout.Kind) (contentlink.Kind, bool) {
	switch kind {
	case panelayout.Document:
		return contentlink.KindFile, true
	case panelayout.Issue:
		return contentlink.KindIssue, true
	case panelayout.Note:
		return contentlink.KindInternal, true
	case panelayout.Diff:
		return contentlink.KindDiff, true
	default:
		return "", false
	}
}

func remoteLayoutTarget(ref contentlink.Ref, line int) uirequest.Target {
	switch ref.Kind {
	case contentlink.KindFile:
		return uirequest.Target{Kind: uirequest.TargetKindFile, Value: ref.Value, Line: line}
	case contentlink.KindIssue:
		return uirequest.Target{Kind: uirequest.TargetKindIssue, Value: ref.Value}
	case contentlink.KindInternal:
		if ref.Namespace == "note" {
			return uirequest.Target{Kind: uirequest.TargetKindNote, Value: ref.Value}
		}
	case contentlink.KindDiff:
		return uirequest.Target{Kind: uirequest.TargetKindDiff, Value: ref.Value}
	case contentlink.KindResource:
		return uirequest.Target{Kind: uirequest.TargetKindResource, Value: ref.Value, Provider: ref.Provider, Matcher: ref.Matcher}
	}
	return uirequest.Target{}
}

func splitLayoutFileLine(raw string) (string, int) {
	if colonIdx := strings.LastIndex(raw, ":"); colonIdx > 0 && colonIdx < len(raw)-1 {
		if n, err := strconv.Atoi(raw[colonIdx+1:]); err == nil && n > 0 {
			return raw[:colonIdx], n
		}
	}
	return raw, 0
}

func (p *Plugin) planPassiveItem(screen, deckTrial *PaneNode, item layoutItemPlan, boxes map[int]Box) (panelayout.OpenPlan, string) {
	return layoutapply.PlanPassiveItem(screen, deckTrial, item, boxes)
}

func deckCellFor(screen *PaneNode, cell panelayout.Cell) (panelayout.Cell, string) {
	return layoutapply.DeckCellFor(screen, cell)
}

func (p *Plugin) performPlannedOpen(target uirequest.Target, root, surface string, plan panelayout.OpenPlan) (openOutcome, tea.Cmd) {
	prev := p.pendingOpenPlan
	p.pendingOpenPlan = &plan
	defer func() { p.pendingOpenPlan = prev }()
	return p.performTargetOpen(uirequest.Request{
		Action:  uirequest.ActionOpen,
		Target:  target,
		Options: uirequest.Options{Split: "auto"},
	}, root, surface)
}

func (p *Plugin) commitLayoutShell(spec uirequest.LayoutPane, plan panelayout.OpenPlan, originSession string) (string, string, tea.Cmd) {
	if !p.selectCreateSplitOrigin(originSession) {
		return uirequest.ItemVerdictDeclined, layoutNotOnScreenReason, nil
	}
	if !terminalPanelEnabled() {
		return uirequest.ItemVerdictDeclined, features.WorkspaceTerminalPanel.Name + " is off", nil
	}
	before := p.shellLeaf()
	session := p.termPanelSessionName()
	if spec.Run != "" || spec.Type != "" {
		p.pendingTermPanelSeed = &termPanelSeed{
			session: session,
			run:     spec.Run,
			typeCmd: spec.Type,
		}
	}
	prevPlan := p.pendingShellPlan
	if plan.Split != 0 {
		p.pendingShellPlan = &plan
	}
	cmd := p.createTerminalSplit(spec.Name, "auto")
	p.pendingShellPlan = prevPlan
	if p.shellLeaf() == nil || p.shellLeaf() == before {
		p.pendingTermPanelSeed = nil
		reason := p.toastMessage
		if reason == "" {
			reason = "the window is too small to split"
		}
		return uirequest.ItemVerdictDeclined, reason, cmd
	}
	return uirequest.ItemVerdictOpened, "", cmd
}

func (p *Plugin) landedLeaf(kind panelayout.Kind) int {
	return layoutapply.LandedLeafID(p.paneRoot, kind)
}

func (p *Plugin) liveShellSessions() map[string]bool {
	out := make(map[string]bool)
	var walk func(node *PaneNode)
	walk = func(node *PaneNode) {
		if node == nil {
			return
		}
		if node.Split == nil {
			if node.Kind == panelayout.Shell && p.requireShellTermPane().Session != "" {
				out[p.requireShellTermPane().Session] = true
			}
			return
		}
		walk(node.Split.A)
		walk(node.Split.B)
	}
	walk(p.paneRoot)
	return out
}

func (p *Plugin) adoptSpecShellLeaf(item *layoutItemPlan) (string, string, tea.Cmd) {
	if item.Spec.Run != "" || item.Spec.Type != "" {
		p.pendingTermPanelSeed = &termPanelSeed{
			session: p.termPanelSessionName(),
			run:     item.Spec.Run,
			typeCmd: item.Spec.Type,
		}
	}
	cmd := p.attachWorkspaceTerminalSplit()
	p.shellLeafName = strings.TrimSpace(item.Spec.Name)
	p.setShellLeafFocused(true)
	p.activePane = PanePreview
	if p.requireShellTermPane().Session == "" {
		reason := p.toastMessage
		if reason == "" {
			reason = features.WorkspaceTerminalPanel.Name + " is off"
		}
		return uirequest.ItemVerdictDeclined, reason, cmd
	}
	return uirequest.ItemVerdictOpened, "", cmd
}
