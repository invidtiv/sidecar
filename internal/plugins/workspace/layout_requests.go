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
// so a decline can still tell the agent what EVERY pane would have done, and
// plan is the placement the batch fit-tested — the one commit must apply
// verbatim, not re-derive.
type layoutItemPlan struct {
	spec    uirequest.LayoutPane
	kind    panelayout.Kind
	targets []uirequest.Target
	cell    panelayout.Cell
	plan    panelayout.OpenPlan
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
		// "at" is a requirement, not a preference: it must parse here so an
		// unaddressable cell declines before anything is planned or opened.
		if spec.At != "" {
			cell, ok := panelayout.ParseCell(spec.At)
			if !ok {
				note(i, uirequest.ItemVerdictDeclined, fmt.Sprintf("cell %q is not a grid address like 2.1", spec.At))
				continue
			}
			item.cell = cell
		}
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

	// Phase 2 — plan each pane. Two trees, two jobs:
	//
	//   trial is the SCREEN truth (pane tree incl. shell leaves): it answers
	//   cell addressing, caps, and the composed fit-test.
	//
	//   deckTrial is where passive plans come FROM: a clone of the deck's own
	//   tree, advanced by each planned open exactly as commit will advance the
	//   real deck. Plans handed to deck.Open are therefore valid for it by
	//   construction — the committed tree is the fit-tested one, not a re-plan.
	//
	// Shell plans stay pane-tree-native (openShellLeaf splits that tree
	// directly), so they alone are planned on trial.
	//
	// One ordering rule keeps positional promises honest: a batch that places
	// a shell item declines later at-cell passives. Committing the shell's
	// split and then mutating the deck rebuilds the pane tree around it (the
	// terminal split is re-derived on projection), so no static translation
	// can promise an addressed row survives that — and "at" is a requirement,
	// never a preference. All-or-nothing means nothing opens at all in that
	// case; agents reorder the shell last or drop the cell.
	var deckTrial *panelayout.Node
	shellPlannedAt := -1
	for i := range items {
		item := &items[i]
		if item.kind == panelayout.Primary || item.verdict == uirequest.ItemVerdictDeclined {
			continue
		}
		var plan panelayout.OpenPlan
		var refusal string
		switch {
		case item.kind == panelayout.Shell:
			plan, refusal = p.planShellItem(trial, *item)
			if refusal == "" && plan.Retarget == 0 {
				ApplyPanePlan(trial, plan, &PaneNode{Kind: item.kind})
				shellPlannedAt = i
			}
		case item.cell.Col != 0 && shellPlannedAt >= 0:
			refusal = fmt.Sprintf("cell %s cannot be addressed after this batch places a live terminal; put the shell last or drop \"at\"", item.cell.String())
		default:
			if deckTrial == nil {
				p.ensureWorkspaceDeck(root, surface)
				deckTrial = p.contentDeck.Tree()
			}
			plan, refusal = p.planPassiveItem(trial, deckTrial, *item, boxes)
			if refusal == "" && plan.Retarget == 0 {
				panelayout.ApplyPlan(deckTrial, plan, &panelayout.Node{Kind: item.kind})
				// Mirror the structure into the screen trial so later cells,
				// caps, and the composed fit-test see the batch's own shapes.
				// Ids may drift between the two trees; structure is what the
				// screen trial is for — commit never takes targets from it.
				ApplyPanePlan(trial, plan, &PaneNode{Kind: item.kind})
			}
		}
		if refusal != "" {
			note(i, uirequest.ItemVerdictDeclined, refusal)
			continue
		}
		item.plan = plan
		if plan.Retarget != 0 {
			item.verdict = uirequest.ItemVerdictRetargeted
			continue
		}
		item.verdict = uirequest.ItemVerdictOpened
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
		// Would-have-opened items keep the verdict validation evaluated for
		// them, but never silently: without a reason an agent reads "opened"
		// as "on screen now". Say the batch stopped before they were applied.
		for i := range items {
			if items[i].verdict == uirequest.ItemVerdictDeclined || items[i].reason != "" {
				continue
			}
			if items[i].verdict == uirequest.ItemVerdictRetargeted {
				items[i].reason = "would have retargeted; the batch declined before commit"
			} else {
				items[i].reason = "would have opened; the batch declined before commit"
			}
		}
		return p.ackLayout(req, uirequest.StatusDeclined, items[firstViolation].reason, p.layoutAcks(items, surface, false), nil)
	}

	// Commit. Validation promised every pane fits; each open now applies THE
	// PLAN that was fit-tested — performPlannedOpen hands it to deck.Open,
	// commitLayoutShell hands it to openShellLeaf — so the committed tree is
	// the trial tree by construction rather than by re-planning luck.
	var cmds []tea.Cmd
	retargetCount := 0
	for i := range items {
		item := &items[i]
		if item.kind == panelayout.Shell {
			var cmd tea.Cmd
			item.verdict, item.reason, cmd = p.commitLayoutShell(item.spec, item.plan, req.Origin.TmuxSession)
			cmds = append(cmds, cmd)
			if item.verdict != uirequest.ItemVerdictOpened {
				continue
			}
		} else {
			outcome, cmd := p.performPlannedOpen(item.targets[0], root, surface, item.plan)
			cmds = append(cmds, cmd)
			item.verdict, item.reason = string(outcome.status), outcome.reason
			if outcome.status == uirequest.StatusDeclined {
				continue
			}
			// Targets after the first join the pane as tabs of the same kind:
			// the existing retarget/openTab path, one call per extra target.
			// They carry no plan — the pane exists, so a split plan would be
			// wrong twice over.
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
	p.ackLayout(req, status, reason, p.layoutAcks(items, surface, true), nil)
	return tea.Batch(cmds...)
}

// performPlannedOpen opens one target through the ordinary per-kind paths with
// ONE difference: deck.Open applies the given plan instead of re-running its
// own placement policy. The override is scoped to this single open — extra-tab
// opens and every other caller re-plan (and retarget) exactly as before.
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

// planShellItem plans the batch's shell pane against the SCREEN trial: shells
// split the pane tree directly (openShellLeaf), never the deck's, so their
// placement is decided there — auto grid policy or an explicit cell.
func (p *Plugin) planShellItem(trial *PaneNode, item layoutItemPlan) (panelayout.OpenPlan, string) {
	if !terminalPanelEnabled() {
		return panelayout.OpenPlan{}, features.WorkspaceTerminalPanel.Name + " is off"
	}
	if p.termPanelVisible || panelayout.FirstOfKind(trial, panelayout.Shell) != nil {
		return panelayout.OpenPlan{}, shellCapMessage
	}
	if item.cell.Col != 0 {
		return panelayout.PlanOpenAt(trial, item.kind, 0, item.cell)
	}
	plan, ok := panelayout.PlanOpenContent(trial, item.kind, 0, p.lastPaneBoxes())
	if !ok {
		return panelayout.OpenPlan{}, panelayout.LiveCapMessage
	}
	return plan, ""
}

// planPassiveItem plans ONE content pane. Addressing and refusals are stated
// against the SCREEN grid the agent sees (the full pane tree, shell rows
// included); the plan itself is then resolved against deckTrial — the deck's
// own tree, advanced by every earlier planned open — so commit can apply it
// verbatim instead of re-planning. A retarget means the pane already exists:
// the batch adds a tab rather than a leaf, changing no geometry at all.
func (p *Plugin) planPassiveItem(screen, deckTrial *PaneNode, item layoutItemPlan, boxes map[int]Box) (panelayout.OpenPlan, string) {
	cell := item.cell
	if cell.Col != 0 {
		// The screen answer speaks first: ranges, caps, and retarget
		// conflicts in the user's own vocabulary, including the shell row.
		if _, refusal := panelayout.PlanOpenAt(screen, item.kind, 0, cell); refusal != "" {
			return panelayout.OpenPlan{}, refusal
		}
		translated, refusal := deckCellFor(screen, cell)
		if refusal != "" {
			return panelayout.OpenPlan{}, refusal
		}
		cell = translated
	}
	plan, ok := panelayout.PlanOpenAtOrContent(deckTrial, item.kind, cell, boxes)
	if !ok {
		return panelayout.OpenPlan{}, passivePlanRefusal(deckTrial, item.kind)
	}
	return plan, ""
}

// deckCellFor translates a screen cell onto the deck's grid. At most ONE shell
// leaf can exist (LiveLeafCap), sitting beside the primary terminal, so the
// only difference between the two grids is that shell's row: cells above it
// keep their address, the shell's own row belongs to no content pane, and
// cells below shift up by one — but only while the column keeps a non-shell
// anchor. A column that is nothing BUT the live terminal has no deck-side
// existence at all, so no cell in it translates.
func deckCellFor(screen *PaneNode, cell panelayout.Cell) (panelayout.Cell, string) {
	grid := panelayout.GridOf(screen)
	if grid == nil || cell.Col > grid.ColumnCount() {
		return cell, ""
	}
	column := grid.Columns[cell.Col-1]
	anchored := false
	for row, leaf := range column.Cells {
		screenRow := row + 1
		if leaf.Kind != panelayout.Shell {
			anchored = true
			continue
		}
		switch {
		case screenRow == cell.Row:
			return panelayout.Cell{}, fmt.Sprintf("cell %s holds the live terminal; content panes cannot take its place", cell.String())
		case screenRow < cell.Row:
			return panelayout.Cell{Col: cell.Col, Row: cell.Row - 1}, ""
		default:
			if !anchored {
				return panelayout.Cell{}, fmt.Sprintf("cell %s sits inside the live terminal's own column; close or move the terminal first", cell.String())
			}
			return cell, ""
		}
	}
	return cell, ""
}

// passivePlanRefusal explains a failed deck-side plan with the planner's own
// vocabulary, worded to stand alone in a toast or an ack.
func passivePlanRefusal(deckTrial *PaneNode, kind panelayout.Kind) string {
	grid := panelayout.GridOf(deckTrial)
	switch {
	case grid != nil && grid.ColumnsAtCap():
		return panelayout.GridColumnCapMessage
	case grid != nil:
		return panelayout.GridRowCapMessage
	default:
		return "no room for another " + kind.Name() + " pane"
	}
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
// `create shell --split` walks them. The batch's fit-tested plan scopes the
// split (pendingShellPlan → shellLeafOpenPlan), so an at-cell or a planned
// auto placement lands the leaf where planning said; placement stays "auto"
// for everything else, speaking the grid policy rather than an axis
// preference.
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

// layoutAcks fills the per-pane ack items AFTER the whole batch has run. On a
// committed batch, landed panes are resolved from the FINAL tree — one leaf
// per passive kind plus at most one shell, so kind alone identifies the pane —
// which is what makes an ack and a later `layout get` agree for every terminal
// state, whatever later reconciles did to intermediate ids. committed=false
// (a validation decline) resolves nothing: nothing landed, and a would-have-
// opened verdict must not borrow a pre-existing pane's address.
func (p *Plugin) layoutAcks(items []layoutItemPlan, surface string, committed bool) []uirequest.AckItem {
	cells := p.layoutCells()
	out := make([]uirequest.AckItem, 0, len(items))
	for i, item := range items {
		ackItem := uirequest.AckItem{
			Index:   i,
			Verdict: item.verdict,
			Surface: surface,
			Reason:  item.reason,
		}
		if committed && item.verdict != uirequest.ItemVerdictDeclined {
			if leafID := p.landedLeaf(item.kind); leafID != 0 {
				ackItem.Pane = leafID
				ackItem.Cell = cells[leafID]
			}
		}
		out = append(out, ackItem)
	}
	return out
}

// landedLeaf names where a batch's pane of this kind ended up in the final
// tree: the kind's own leaf for passives, the shell leaf for shells.
func (p *Plugin) landedLeaf(kind panelayout.Kind) int {
	if kind == panelayout.Shell {
		if leaf := p.shellLeaf(); leaf != nil {
			return leaf.ID
		}
		return 0
	}
	if leaf := panelayout.FirstOfKind(p.paneRoot, kind); leaf != nil {
		return leaf.ID
	}
	return 0
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
