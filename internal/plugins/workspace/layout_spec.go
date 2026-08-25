package workspace

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
)

// The `layout apply --spec` host side: one full layout, all-or-nothing.
//
// A spec names EVERY pane on screen, so validation must account for every
// existing live leaf (decision 5): the primary is carried by exactly one
// {"kind":"primary"} pane, a split terminal by {"kind":"shell","session":...}
// exactly as `layout get` prints them. Passive panes not named are closed
// freely — their content re-opens — but a spec omitting a live leaf declines
// naming the session rather than destroying it.
//
// Commit walks the same Decode-shaped path a relaunch does (validate → build
// → fit-test → commit → load), in two stages so both live-leaf forms land:
//
//  1. restorePaneLayout rebuilds the tree from primary + carried shells +
//     passives — the ordinary PaneLayoutJSON decode that persistence already
//     round-trips. The deck is dropped alongside it (as resetPaneTreeToTerminal
//     does) so its next use adopts the new tree instead of reconciling the old.
//  2. A NEW shell pane (run/type form, at most one — the live cap bounds total
//     live leaves at two) cannot be decoded: no session exists yet. It grafts
//     after stage 1 through PlanOpenAt at the cell the spec gave it. That plan
//     cannot disagree with the fit-tested trial: the committed tree minus new
//     shells has the trial's exact grid shape, and inserting or appending the
//     shell back restores it cell for cell.
//
// Every refusal happens before the first mutation, so a declined spec leaves
// the tree byte-for-byte untouched.

const layoutSpecOriginRequired = "a new shell pane needs a Sidecar shell to split beside; run from inside one"

func (p *Plugin) applyLayoutSpec(req uirequest.Request, payload uirequest.LayoutPayload, root, surface string) tea.Cmd {
	columns, err := uirequest.DecodeLayoutColumns(payload.Columns)
	if err != nil {
		return p.ackLayout(req, uirequest.StatusDeclined, "invalid layout spec: "+err.Error(), nil, nil)
	}
	spec := uirequest.LayoutSpec{Columns: columns}
	if err := uirequest.ValidateLayoutSpec(spec); err != nil {
		return p.ackLayout(req, uirequest.StatusDeclined, err.Error(), nil, nil)
	}

	items := make([]layoutItemPlan, 0, len(spec.Columns)*panelayout.MaxGridRows)
	for _, column := range spec.Columns {
		for _, pane := range column.Panes {
			items = append(items, layoutItemPlan{spec: pane})
		}
	}

	liveSessions := p.liveShellSessions()
	firstViolation := -1
	note := func(i int, verdict, reason string) {
		items[i].verdict, items[i].reason = verdict, reason
		if firstViolation < 0 && verdict == uirequest.ItemVerdictDeclined && reason != "" {
			firstViolation = i
		}
	}

	// Phase 1 — per-pane semantics: target resolution through the same
	// classification `sidecar open` uses, one passive leaf per kind (the
	// deck's own rule), and each carried session's existence on screen now.
	newShells := 0
	passiveSeen := make(map[panelayout.Kind]int)
	for i := range items {
		item := &items[i]
		item.kind, _ = panelayout.KindByName(strings.TrimSpace(item.spec.Kind))
		switch item.kind {
		case panelayout.Primary:
			continue
		case panelayout.Shell:
			if item.spec.Session != "" {
				if !liveSessions[item.spec.Session] {
					note(i, uirequest.ItemVerdictDeclined,
						fmt.Sprintf("no live terminal named %q is on screen; shells are carried by session as layout get prints them", item.spec.Session))
				}
				continue
			}
			if req.Origin.TmuxSession == "" {
				note(i, uirequest.ItemVerdictDeclined, layoutSpecOriginRequired)
				continue
			}
			newShells++
		default:
			passiveSeen[item.kind]++
			if passiveSeen[item.kind] > 1 {
				note(i, uirequest.ItemVerdictDeclined,
					fmt.Sprintf("a %s pane is already part of this spec; passive kinds keep one pane each", item.kind.Name()))
				continue
			}
			targets, refusal := p.resolveLayoutTargets(item.kind, item.spec, root)
			if refusal != "" {
				note(i, uirequest.ItemVerdictDeclined, refusal)
				continue
			}
			item.targets = targets
		}
	}

	// Phase 2 — global accounting: every live terminal the current tree holds
	// must be carried by name, and the composed live count stays within the
	// cap. These violations belong to no single requested pane; every pane
	// still standing takes the reason, exactly as the batch's fit failure does.
	globalFailure := ""
	carried := 0
	for i := range items {
		if items[i].kind == panelayout.Shell && items[i].spec.Session != "" && items[i].verdict != uirequest.ItemVerdictDeclined {
			carried++
		}
	}
	for _, session := range sortedSessionKeys(liveSessions) {
		covered := false
		for i := range items {
			if items[i].kind == panelayout.Shell && items[i].spec.Session == session {
				covered = true
				break
			}
		}
		if !covered {
			globalFailure = fmt.Sprintf("this spec omits the live terminal %q; carry it with {\"kind\":\"shell\",\"session\":\"%s\"} or close it first", session, session)
			break
		}
	}
	if globalFailure == "" && 1+carried+newShells > panelayout.LiveLeafCap {
		globalFailure = panelayout.LiveCapMessage
	}
	if globalFailure != "" {
		for i := range items {
			if items[i].verdict != uirequest.ItemVerdictDeclined {
				note(i, uirequest.ItemVerdictDeclined, globalFailure)
			}
		}
	}

	// Phase 3 — build both projections of the spec: the fit-test trial (every
	// pane, new shells included — what the SCREEN will show) and the
	// PaneLayoutJSON stage 1 decodes from (new shells excluded — what restore
	// can build). Cells are recorded per item so stage 2 grafts a new shell
	// where the spec put it.
	trial, layout, cells := p.buildSpecTrees(spec, items)

	// Phase 4 — fit-test the COMPOSED trial once against the floors (Law 2),
	// only reached when nothing else declined.
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
		// Valid items keep the verdict validation evaluated for them but say
		// the spec stopped before commit, never silently claiming an open.
		for i := range items {
			if items[i].verdict != uirequest.ItemVerdictDeclined && items[i].reason == "" {
				note(i, uirequest.ItemVerdictDeclined, "would have opened; the spec declined before commit")
			}
		}
		return p.ackLayout(req, uirequest.StatusDeclined, items[firstViolation].reason, p.layoutAcks(items, surface, false), nil)
	}

	// Validation is done: everything that survived it is going to land.
	for i := range items {
		if items[i].verdict == "" {
			items[i].verdict = uirequest.ItemVerdictOpened
		}
	}

	// Commit stage 1 — decode the applied tree through the ordinary relaunch
	// path, which resets the content maps (passive panes not in the spec close
	// with their content re-armed from its targets) and rebuilds every leaf.
	layout.Root = root
	layout.Surface = surface
	layout.Open = true
	cmds := []tea.Cmd{}
	if cmd := p.restorePaneLayout(layout); cmd != nil {
		cmds = append(cmds, cmd)
	}
	p.contentDeck = nil
	p.hiddenPaneLayout = nil

	// Commit stage 2 — graft new shells where the spec put them. Unreachable
	// refusals stay honest: if planning ever disagrees with the trial, the ack
	// says so instead of claiming a landing.
	status := uirequest.StatusOpened
	stageTwoRefusal := ""
	for i := range items {
		item := &items[i]
		if item.kind != panelayout.Shell || item.spec.Session != "" {
			continue
		}
		plan, refusal := panelayout.PlanOpenAt(p.paneRoot, panelayout.Shell, 0, cells[i])
		if refusal != "" {
			item.verdict, item.reason = uirequest.ItemVerdictDeclined, refusal
			status = uirequest.StatusDeclined
			if stageTwoRefusal == "" {
				stageTwoRefusal = refusal
			}
			continue
		}
		verdict, reason, shellCmd := p.commitLayoutShell(item.spec, plan, req.Origin.TmuxSession)
		item.verdict, item.reason = verdict, reason
		if shellCmd != nil {
			cmds = append(cmds, shellCmd)
		}
		if verdict != uirequest.ItemVerdictOpened {
			status = uirequest.StatusDeclined
			if stageTwoRefusal == "" && reason != "" {
				stageTwoRefusal = reason
			}
		}
	}

	// The applied tree is ordinary deck/PaneLayoutJSON state: persisting it is
	// the ordinary selection save, and a relaunch restores it under the
	// existing rules — no new format anywhere.
	p.saveSelectionState()
	p.ackLayout(req, status, stageTwoRefusal, p.layoutAcks(items, surface, true), nil)
	return tea.Batch(cmds...)
}

// buildSpecTrees compiles one validated spec into its two projections:
// trialPaneTree with EVERY pane for the fit-test and caps, and the saved
// PaneLayoutJSON stage 1 decodes from, new shells excluded because nothing
// exists yet for them to attach to (stage 2 grafts those back at their
// recorded cells). Both walk the same column-major order, so their shapes
// correspond cell for cell; a column holding only a new shell is simply
// absent from stage 1, and stage 2's cell lands one past its end.
func (p *Plugin) buildSpecTrees(spec uirequest.LayoutSpec, items []layoutItemPlan) (*PaneNode, *state.PaneLayoutJSON, map[int]panelayout.Cell) {
	type builtColumn struct {
		node  *PaneNode
		saved *state.PaneLayoutJSON
	}
	built := make([]builtColumn, 0, len(spec.Columns))
	cells := make(map[int]panelayout.Cell)
	index, nextID := 0, 1
	for c, column := range spec.Columns {
		nodes := make([]*PaneNode, 0, len(column.Panes))
		var saved []*state.PaneLayoutJSON
		for r := range column.Panes {
			item := &items[index]
			cells[index] = panelayout.Cell{Col: c + 1, Row: r + 1}
			nodes = append(nodes, &PaneNode{ID: nextID, Kind: item.kind})
			nextID++
			if !isNewShellItem(item) {
				saved = append(saved, p.specLeafJSON(item))
			}
			index++
		}
		built = append(built, builtColumn{node: stackRows(nodes), saved: stackSavedRows(saved)})
	}
	columnNodes := make([]*PaneNode, 0, len(built))
	columnSaved := make([]*state.PaneLayoutJSON, 0, len(built))
	for _, b := range built {
		columnNodes = append(columnNodes, b.node)
		if b.saved != nil {
			columnSaved = append(columnSaved, b.saved)
		}
	}
	return chainColumns(columnNodes), chainColumnsSaved(columnSaved), cells
}

func isNewShellItem(item *layoutItemPlan) bool {
	return item.kind == panelayout.Shell && item.spec.Session == ""
}

// specLeafJSON is one spec pane's persisted-shape leaf: what stage 1 decodes.
// New shell panes never reach here — they have no session to persist, and
// stage 2 grafts them onto the live tree instead.
func (p *Plugin) specLeafJSON(item *layoutItemPlan) *state.PaneLayoutJSON {
	switch item.kind {
	case panelayout.Primary:
		return &state.PaneLayoutJSON{Kind: contentKindTerminal}
	case panelayout.Shell:
		return &state.PaneLayoutJSON{Kind: contentKindShell, Session: item.spec.Session}
	default:
		saved := &state.PaneLayoutJSON{Kind: specStateKind(item.kind)}
		for _, t := range item.targets {
			switch item.kind {
			case panelayout.Document:
				saved.Tabs = append(saved.Tabs, state.PaneDocTabJSON{Path: t.Value})
			case panelayout.Issue:
				saved.IssueTabs = append(saved.IssueTabs, state.PaneIssueTabJSON{Issue: t.Value})
			case panelayout.Note:
				saved.NoteTabs = append(saved.NoteTabs, state.PaneNoteTabJSON{Note: t.Value})
			case panelayout.Diff:
				saved.DiffTabs = append(saved.DiffTabs, state.PaneDiffTabJSON{Spec: t.Value})
			case panelayout.Resource:
				saved.ResourceTabs = append(saved.ResourceTabs, state.PaneResourceTabJSON{
					Provider: t.Provider, Matcher: t.Matcher, Locator: t.Value,
				})
			}
		}
		return saved
	}
}

// specStateKind maps a vocabulary kind onto its persisted PaneLayoutJSON name.
func specStateKind(kind panelayout.Kind) string {
	switch kind {
	case panelayout.Document:
		return contentKindDoc
	case panelayout.Issue:
		return contentKindIssue
	case panelayout.Note:
		return contentKindNote
	case panelayout.Diff:
		return contentKindDiff
	default:
		return contentKindResource
	}
}

// liveShellSessions names the sessions of every Shell leaf on screen. At most
// one exists today (LiveLeafCap bounds live leaves at two including the
// primary), but the accounting reads the tree rather than assuming.
func (p *Plugin) liveShellSessions() map[string]bool {
	out := make(map[string]bool)
	var walk func(node *PaneNode)
	walk = func(node *PaneNode) {
		if node == nil {
			return
		}
		if node.Split == nil {
			if node.Kind == panelayout.Shell && p.termPanelSession != "" {
				out[p.termPanelSession] = true
			}
			return
		}
		walk(node.Split.A)
		walk(node.Split.B)
	}
	walk(p.paneRoot)
	return out
}

func sortedSessionKeys(sessions map[string]bool) []string {
	out := make([]string, 0, len(sessions))
	for session := range sessions {
		out = append(out, session)
	}
	sort.Strings(out)
	return out
}

func stackRows(nodes []*PaneNode) *PaneNode {
	root := nodes[0]
	for _, next := range nodes[1:] {
		root = &PaneNode{ID: panelayout.MaxID(root) + 1, Split: &PaneSplit{
			Axis: SplitRows, Ratio: 50, A: root, B: next,
		}}
	}
	return root
}

func stackSavedRows(saved []*state.PaneLayoutJSON) *state.PaneLayoutJSON {
	if len(saved) == 0 {
		return nil
	}
	root := saved[0]
	for _, next := range saved[1:] {
		root = &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{Axis: "rows", Ratio: 50, A: root, B: next}}
	}
	return root
}

func chainColumns(columns []*PaneNode) *PaneNode {
	root := columns[0]
	for _, next := range columns[1:] {
		root = &PaneNode{ID: panelayout.MaxID(root) + 1, Split: &PaneSplit{
			Axis: SplitCols, Ratio: 50, A: root, B: next,
		}}
	}
	return root
}

func chainColumnsSaved(columns []*state.PaneLayoutJSON) *state.PaneLayoutJSON {
	root := columns[0]
	for _, next := range columns[1:] {
		root = &state.PaneLayoutJSON{Split: &state.PaneSplitJSON{Axis: "cols", Ratio: 50, A: root, B: next}}
	}
	return root
}
