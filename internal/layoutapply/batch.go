package layoutapply

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/uirequest"
)

func applyBatch(h Host, req uirequest.Request, payload uirequest.LayoutPayload, root, surface string) tea.Cmd {
	items := make([]ItemPlan, len(payload.Panes))
	trial := panelayout.Clone(h.PaneRoot())
	boxes := h.LastBoxes()
	firstViolation := -1

	note := func(i int, verdict, reason string) {
		items[i].Verdict, items[i].Reason = verdict, reason
		if firstViolation < 0 && reason != "" && verdict == uirequest.ItemVerdictDeclined {
			firstViolation = i
		}
	}

	for i, spec := range payload.Panes {
		item := &items[i]
		item.Spec = spec
		kind, ok := panelayout.KindByName(strings.TrimSpace(spec.Kind))
		if !ok {
			note(i, uirequest.ItemVerdictDeclined, fmt.Sprintf("unknown pane kind %q", spec.Kind))
			continue
		}
		item.Kind = kind
		if spec.At != "" {
			cell, ok := panelayout.ParseCell(spec.At)
			if !ok {
				note(i, uirequest.ItemVerdictDeclined, fmt.Sprintf("cell %q is not a grid address like 2.1", spec.At))
				continue
			}
			item.Cell = cell
		}
		switch kind {
		case panelayout.Primary:
			note(i, uirequest.ItemVerdictDeclined, "the primary pane is the host's own content and cannot be opened")
			continue
		case panelayout.Shell:
			if h.SplitOrigin() == "" {
				note(i, uirequest.ItemVerdictDeclined, "a shell pane needs a Sidecar shell to split beside; run from inside one")
				continue
			}
		default:
			targets, refusal := h.ResolveTargets(kind, spec)
			if refusal != "" {
				note(i, uirequest.ItemVerdictDeclined, refusal)
				continue
			}
			item.Targets = targets
		}
	}

	var deckTrial *panelayout.Node
	shellPlannedAt := -1
	for i := range items {
		item := &items[i]
		if item.Kind == panelayout.Primary || item.Verdict == uirequest.ItemVerdictDeclined {
			continue
		}
		var plan panelayout.OpenPlan
		var refusal string
		switch {
		case item.Kind == panelayout.Shell:
			plan, refusal = PlanShellItem(h, trial, *item)
			if refusal == "" && plan.Retarget == 0 {
				panelayout.ApplyPlan(trial, plan, &panelayout.Node{Kind: item.Kind})
				shellPlannedAt = i
			}
		case item.Cell.Col != 0 && shellPlannedAt >= 0:
			refusal = fmt.Sprintf("cell %s cannot be addressed after this batch places a live terminal; put the shell last or drop \"at\"", item.Cell.String())
		default:
			if deckTrial == nil {
				h.EnsureDeck()
				deckTrial = h.DeckTree()
			}
			plan, refusal = PlanPassiveItem(trial, deckTrial, *item, boxes)
			if refusal == "" && plan.Retarget == 0 {
				panelayout.ApplyPlan(deckTrial, plan, &panelayout.Node{Kind: item.Kind})
				panelayout.ApplyPlan(trial, plan, &panelayout.Node{Kind: item.Kind})
			}
		}
		if refusal != "" {
			note(i, uirequest.ItemVerdictDeclined, refusal)
			continue
		}
		item.Plan = plan
		if plan.Retarget != 0 {
			item.Verdict = uirequest.ItemVerdictRetargeted
			continue
		}
		item.Verdict = uirequest.ItemVerdictOpened
	}

	if firstViolation < 0 {
		failure := ""
		if peer, placed := h.PeerBox(); !placed {
			failure = tooSmall
		} else if _, _, fits := panelayout.LayoutPanes(panelayout.Clone(trial), peer, h.Floors()); !fits {
			failure = needsLarger
		}
		if failure != "" {
			for i := range items {
				if items[i].Verdict != uirequest.ItemVerdictDeclined {
					note(i, uirequest.ItemVerdictDeclined, failure)
				}
			}
		}
	}

	if firstViolation >= 0 {
		for i := range items {
			if items[i].Verdict == uirequest.ItemVerdictDeclined || items[i].Reason != "" {
				continue
			}
			if items[i].Verdict == uirequest.ItemVerdictRetargeted {
				items[i].Reason = "would have retargeted; the batch declined before commit"
			} else {
				items[i].Reason = "would have opened; the batch declined before commit"
			}
		}
		h.Ack(req, uirequest.StatusDeclined, items[firstViolation].Reason, LayoutAcks(h, items, surface, false), nil)
		return nil
	}

	var cmds []tea.Cmd
	retargetCount := 0
	for i := range items {
		item := &items[i]
		if item.Kind == panelayout.Shell {
			var cmd tea.Cmd
			item.Verdict, item.Reason, cmd = h.CommitShell(item.Spec, item.Plan)
			cmds = append(cmds, cmd)
			if item.Verdict != uirequest.ItemVerdictOpened {
				continue
			}
		} else {
			var cmd tea.Cmd
			item.Verdict, item.Reason, cmd = h.CommitPassive(item.Targets, item.Plan)
			cmds = append(cmds, cmd)
		}
		if item.Verdict == uirequest.ItemVerdictRetargeted {
			retargetCount++
		}
	}

	status := uirequest.StatusOpened
	reason := ""
	if retargetCount == len(items) {
		status = uirequest.StatusRetargeted
	}
	h.Ack(req, status, reason, LayoutAcks(h, items, surface, true), nil)
	return tea.Batch(cmds...)
}
