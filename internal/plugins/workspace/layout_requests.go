package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/uirequest"
)

// The `sidecar layout` host side. One uirequest action, two modes:
//
//   - get answers with a report built from THIS surface's tree — the grid
//     projection, every pane's kind/targets/session, geometry, and the caps
//     and floors in effect. A tree that escapes the grid vocabulary reports
//     "grid": null plus the raw tree, and is still valid.
//   - apply is all-or-nothing: every descriptor is resolved through the same
//     target resolution `sidecar open` uses, placements are planned against a
//     trial copy of the tree, the COMPOSED trial is fit-tested once against
//     the floors, and only then does anything commit. A decline names the
//     first violation and leaves the tree byte-for-byte untouched.
//
// Neither mode ever queues (a deliberate divergence from `open`, whose queued
// requests re-validate at selection time): an atomic apply validated against a
// tree that may no longer exist by selection time would be a lie, and a stale
// get answer is worse than a refusal. When the origin shell is not on screen,
// both modes decline and say so.
const layoutNotOnScreenReason = "the origin shell is not on screen, and layout requests are never queued"

func (p *Plugin) applyLayoutRequest(req uirequest.Request) tea.Cmd {
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
		// Not this instance's shell: ignore silently, exactly like open.
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
	return p.applyLayoutBatch(req, payload, root, surface)
}

// ackLayout writes one ActionLayout acknowledgement. Layout carries the get
// report verbatim; Items ride beside Status on apply, versioned so callers can
// gate on the shape.
func (p *Plugin) ackLayout(req uirequest.Request, status uirequest.Status, reason string, items []uirequest.AckItem, layout json.RawMessage) tea.Cmd {
	_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
		Instance:     hostInstanceID(),
		Host:         uirequest.HostName(),
		PID:          os.Getpid(),
		Status:       status,
		Reason:       reason,
		At:           time.Now().UTC(),
		ItemsVersion: ackItemsVersion(items),
		Items:        items,
		Layout:       layout,
	})
	return nil
}

func ackItemsVersion(items []uirequest.AckItem) int {
	if len(items) == 0 {
		return 0
	}
	return 1
}

// layoutItemPlan is one requested pane's journey through validation,
// placement planning, and commit. Verdict and reason follow it the whole way
// so a decline can still tell the agent what EVERY pane would have done.
type layoutItemPlan struct {
	spec    uirequest.LayoutPane
	kind    panelayout.Kind
	targets []uirequest.Target
	cell    panelayout.Cell
	paneID  int
	verdict string
	reason  string
}

func (p *Plugin) applyLayoutBatch(req uirequest.Request, payload uirequest.LayoutPayload, root, surface string) tea.Cmd {
	items := make([]layoutItemPlan, len(payload.Panes))
	trial := clonePaneTree(p.paneRoot)
	boxes := p.lastPaneBoxes()
	firstViolation := -1

	note := func(i int, verdict, reason string) {
		items[i].verdict, items[i].reason = verdict, reason
		if firstViolation < 0 && reason != "" && verdict == uirequest.ItemVerdictDeclined {
			firstViolation = i
		}
	}

	// Phase 1 — resolve every descriptor against the workspace root through
	// the same target classification `sidecar open` runs. Nothing is mutated.
	for i, spec := range payload.Panes {
		item := &items[i]
		item.spec = spec
		kind, ok := panelayout.KindByName(strings.TrimSpace(spec.Kind))
		if !ok {
			note(i, uirequest.ItemVerdictDeclined, fmt.Sprintf("unknown pane kind %q", spec.Kind))
			continue
		}
		item.kind = kind
		switch kind {
		case panelayout.Primary:
			note(i, uirequest.ItemVerdictDeclined, "the primary pane is the host's own content and cannot be opened")
			continue
		case panelayout.Shell:
			// A shell pane opens beside the ORIGIN session (createTerminalSplit,
			// as `create shell --split`); with no origin session there is
			// nothing to split beside, and layout never queues to find one.
			if req.Origin.TmuxSession == "" {
				note(i, uirequest.ItemVerdictDeclined, "a shell pane needs a Sidecar shell to split beside; run from inside one")
				continue
			}
			// A shell's identity is run/type/name; targets mean nothing there.
		default:
			targets, refusal := p.resolveLayoutTargets(kind, spec, root)
			if refusal != "" {
				note(i, uirequest.ItemVerdictDeclined, refusal)
				continue
			}
			item.targets = targets
		}
	}

	// Phase 2 — plan each pane against the shared trial tree, auto or at an
	// explicit cell. A retarget plan means the pane already exists: the batch
	// adds a tab rather than a leaf, which changes no geometry at all.
	for i := range items {
		if items[i].kind == panelayout.Primary || items[i].verdict == uirequest.ItemVerdictDeclined {
			continue
		}
		plan, refusal := p.planLayoutItem(trial, items[i], boxes)
		if refusal != "" {
			note(i, uirequest.ItemVerdictDeclined, refusal)
			continue
		}
		if plan.Retarget != 0 {
			items[i].verdict = uirequest.ItemVerdictRetargeted
			continue
		}
		ApplyPanePlan(trial, plan, &PaneNode{Kind: items[i].kind})
		items[i].verdict = uirequest.ItemVerdictOpened
	}

	// Phase 3 — fit-test the COMPOSED trial once against the floors (Law 2).
	// Only reached when nothing else declined: caps and refusals spoke first.
	if firstViolation < 0 {
		failure := ""
		if peer, placed := p.previewPeerBox(); !placed {
			failure = "the window is too small to split"
		} else if _, _, fits := LayoutPanes(clonePaneTree(trial), peer, paneTreeFloors()); !fits {
			failure = "the composed layout needs a larger window; layout left unchanged"
		}
		if failure != "" {
			for i := range items {
				if items[i].verdict != uirequest.ItemVerdictDeclined {
					note(i, uirequest.ItemVerdictDeclined, failure)
				}
			}
		}
	}

	if firstViolation >= 0 {
		return p.ackLayout(req, uirequest.StatusDeclined, items[firstViolation].reason, p.layoutAcks(items, surface), nil)
	}

	// Commit. Validation promised every pane fits; the real opens now walk
	// the exact paths a single `sidecar open` walks, so the committed tree
	// matches the trial by construction.
	var cmds []tea.Cmd
	retargetCount := 0
	for i := range items {
		item := &items[i]
		if item.kind == panelayout.Shell {
			var cmd tea.Cmd
			item.verdict, item.reason, cmd = p.commitLayoutShell(item.spec, req.Origin.TmuxSession)
			cmds = append(cmds, cmd)
			if item.verdict != uirequest.ItemVerdictOpened {
				continue
			}
			if leaf := p.shellLeaf(); leaf != nil {
				item.paneID = leaf.ID
			}
		} else {
			outcome, cmd := p.performTargetOpen(uirequest.Request{
				Action:  uirequest.ActionOpen,
				Origin:  req.Origin,
				Target:  item.targets[0],
				Options: uirequest.Options{Split: "auto"},
			}, root, surface)
			cmds = append(cmds, cmd)
			item.verdict, item.reason = string(outcome.status), outcome.reason
			if outcome.status == uirequest.StatusDeclined {
				continue
			}
			item.paneID = p.paneFocus
			// Targets after the first join the pane as tabs of the same kind:
			// the existing retarget/openTab path, one call per extra target.
			for _, extra := range item.targets[1:] {
				p.performTargetOpen(uirequest.Request{
					Action: uirequest.ActionOpen,
					Origin: req.Origin,
					Target: extra,
				}, root, surface)
			}
		}
		if item.verdict == uirequest.ItemVerdictRetargeted {
			retargetCount++
		}
	}

	status := uirequest.StatusOpened
	reason := ""
	if retargetCount == len(items) {
		status = uirequest.StatusRetargeted
	}
	p.ackLayout(req, status, reason, p.layoutAcks(items, surface), nil)
	return tea.Batch(cmds...)
}

// planLayoutItem plans ONE pane against the trial tree. The returned refusal
// is empty when a plan was made; otherwise it is the planner's own message,
// worded to stand alone in a toast or an ack.
func (p *Plugin) planLayoutItem(trial *PaneNode, item layoutItemPlan, boxes map[int]Box) (panelayout.OpenPlan, string) {
	if item.cell.Col != 0 {
		return panelayout.PlanOpenAt(trial, item.kind, 0, item.cell)
	}
	if item.kind == panelayout.Shell {
		if !terminalPanelEnabled() {
			return panelayout.OpenPlan{}, features.WorkspaceTerminalPanel.Name + " is off"
		}
		if p.termPanelVisible || panelayout.FirstOfKind(trial, panelayout.Shell) != nil {
			return panelayout.OpenPlan{}, shellCapMessage
		}
	}
	plan, ok := panelayout.PlanOpenContent(trial, item.kind, 0, boxes)
	if !ok {
		grid := panelayout.GridOf(trial)
		switch {
		case panelayout.Duplicable(item.kind) && panelayout.IsLive(item.kind) && panelayout.LiveCapReached(trial):
			return panelayout.OpenPlan{}, panelayout.LiveCapMessage
		case grid != nil && grid.ColumnsAtCap():
			return panelayout.OpenPlan{}, panelayout.GridColumnCapMessage
		case grid != nil:
			return panelayout.OpenPlan{}, panelayout.GridRowCapMessage
		default:
			return panelayout.OpenPlan{}, "no room for another " + item.kind.Name() + " pane"
		}
	}
	return plan, ""
}

// resolveLayoutTargets turns a descriptor's target strings into resolved
// uirequest targets through ResolveTarget — the exact classification the
// CLI's `open` argument goes through, here on the host where the workspace
// root is known. Each result must agree with the descriptor's kind: a file
// pane asked for with a git spec is a refusal, not a guess.
func (p *Plugin) resolveLayoutTargets(kind panelayout.Kind, spec uirequest.LayoutPane, root string) ([]uirequest.Target, string) {
	if len(spec.Targets) == 0 {
		if kind == panelayout.Diff {
			// A diff with no spec IS the working tree, same as `open --diff`.
			return []uirequest.Target{{Kind: uirequest.TargetKindDiff, Value: "wt"}}, ""
		}
		return nil, "a " + kind.Name() + " pane needs at least one target"
	}
	want, ok := layoutWireKind(kind)
	if !ok {
		return nil, "unsupported pane kind " + kind.Name()
	}
	targets := make([]uirequest.Target, 0, len(spec.Targets))
	for _, raw := range spec.Targets {
		var (
			tgt uirequest.Target
			err error
		)
		if kind == panelayout.Resource {
			tgt, err = uirequest.ResolveResourceTarget(spec.Provider, raw)
		} else {
			tgt, err = uirequest.ResolveTarget(root, raw, 0, uirequest.ResolveOptions{Diff: kind == panelayout.Diff})
		}
		if err != nil {
			return nil, fmt.Sprintf("target %q: %v", raw, err)
		}
		if tgt.Kind != want {
			return nil, fmt.Sprintf("target %q resolves to a %s pane, want %s", raw, wireNameForTarget(tgt.Kind), kind.Name())
		}
		targets = append(targets, tgt)
	}
	if kind == panelayout.Resource {
		// Providers are validated against the LIVE matcher snapshot: only the
		// running app knows which matcher claims the locator today.
		ref, refusal := resourceview.ReferenceForLocator(p.resourceMatchers, spec.Provider, targets[0].Value)
		if refusal != "" {
			return nil, refusal
		}
		targets[0].Matcher = ref.Matcher
	}
	return targets, ""
}

func layoutWireKind(kind panelayout.Kind) (uirequest.TargetKind, bool) {
	switch kind {
	case panelayout.Document:
		return uirequest.TargetKindFile, true
	case panelayout.Issue:
		return uirequest.TargetKindIssue, true
	case panelayout.Diff:
		return uirequest.TargetKindDiff, true
	case panelayout.Note:
		return uirequest.TargetKindNote, true
	case panelayout.Resource:
		return uirequest.TargetKindResource, true
	default:
		return "", false
	}
}

func wireNameForTarget(kind uirequest.TargetKind) string {
	if mapped, ok := panelayout.KindByName(string(kind)); ok {
		return mapped.Name()
	}
	return string(kind)
}

// commitLayoutShell opens the batch's shell pane beside the origin session —
// createTerminalSplit and its managed-shell session, exactly as
// `create shell --split` walks them. Placement is "auto": the batch speaks
// the grid policy, not an axis preference.
func (p *Plugin) commitLayoutShell(spec uirequest.LayoutPane, originSession string) (string, string, tea.Cmd) {
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
	cmd := p.createTerminalSplit(spec.Name, "auto")
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

// layoutAcks fills the per-pane ack items AFTER the outcome exists, reading
// each landed pane's cell out of the live tree's grid projection. A declined
// pane reports its own reason and no cell: it never landed anywhere.
func (p *Plugin) layoutAcks(items []layoutItemPlan, surface string) []uirequest.AckItem {
	cells := p.layoutCells()
	out := make([]uirequest.AckItem, 0, len(items))
	for i, item := range items {
		ackItem := uirequest.AckItem{
			Index:   i,
			Verdict: item.verdict,
			Surface: surface,
			Reason:  item.reason,
		}
		if item.paneID != 0 {
			ackItem.Pane = item.paneID
			ackItem.Cell = cells[item.paneID]
		}
		out = append(out, ackItem)
	}
	return out
}

// layoutCells maps every leaf id in the live tree to its "col.row" address.
// A tree outside the grid vocabulary gives no cells, and stays valid.
func (p *Plugin) layoutCells() map[int]string {
	cells := make(map[int]string)
	grid := panelayout.GridOf(p.paneRoot)
	if grid == nil {
		return cells
	}
	for col, column := range grid.Columns {
		for row, leaf := range column.Cells {
			cells[leaf.ID] = panelayout.Cell{Col: col + 1, Row: row + 1}.String()
		}
	}
	return cells
}
