package broadcastmodal

import (
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/agentbroadcast"
	"github.com/marcus/sidecar/internal/agentcontrol"
)

type lineKind int

const (
	lineRow lineKind = iota
	lineSection
	lineCount
	lineNote
)

// Line is one visual row of the modal checklist. Recipient lines agree
// row-for-row with Plan.Recipients (and therefore with dry-run JSON).
type Line struct {
	Kind    lineKind
	ID      string
	Name    string
	Agent   string
	Status  string
	Reason  string
	Checked bool
	Label   string
}

// Checklist renders plan as the modal's recipient list. group inserts project
// section labels. selected overrides the initial would_send checks; nil means
// use the plan's own verdicts.
func Checklist(plan agentbroadcast.Plan, group bool, selected map[string]bool) []Line {
	var out []Line
	lastProject := ""
	for _, row := range plan.Recipients {
		if group && row.Target.Project != lastProject {
			lastProject = row.Target.Project
			out = append(out, Line{Kind: lineSection, Label: row.Target.Project})
		}
		id := agentbroadcast.RecipientID(row)
		checked := row.Outcome == agentbroadcast.OutcomeWouldSend
		if selected != nil {
			checked = selected[id]
		}
		out = append(out, Line{
			Kind:    lineRow,
			ID:      id,
			Name:    recipientName(row),
			Agent:   string(row.Agent.Kind),
			Status:  string(row.Agent.Status),
			Reason:  shortReason(row),
			Checked: checked,
		})
	}
	if plan.ShellsWithoutAgent > 0 {
		out = append(out, Line{Kind: lineCount, Label: shellsWithoutAgentLine(plan.ShellsWithoutAgent)})
	}
	return out
}

func recipientName(row agentbroadcast.Recipient) string {
	if row.Target.Name != "" {
		return row.Target.Name
	}
	return row.Target.Session
}

func shortReason(row agentbroadcast.Recipient) string {
	if row.Reason == nil {
		return ""
	}
	switch row.Reason.Code {
	case string(agentcontrol.ErrNotReady):
		if strings.Contains(row.Reason.Message, "not current") {
			return "not current"
		}
		return "not ready"
	case string(agentcontrol.ErrBlocked):
		return "answering"
	case string(agentcontrol.ErrPaneBusy):
		return "busy"
	case "sender":
		return "sender"
	case "status":
		return "status"
	case "excluded":
		return "deselected"
	default:
		return row.Reason.Code
	}
}

func shellsWithoutAgentLine(n int) string {
	if n == 1 {
		return "1 shell has no live agent and is not listed"
	}
	return fmt.Sprintf("%d shells have no live agent and are not listed", n)
}

func selectedCount(lines []Line) (checked, total int) {
	for _, line := range lines {
		if line.Kind != lineRow {
			continue
		}
		total++
		if line.Checked {
			checked++
		}
	}
	return checked, total
}

func recipientLines(lines []Line) []Line {
	out := make([]Line, 0, len(lines))
	for _, line := range lines {
		if line.Kind == lineRow {
			out = append(out, line)
		}
	}
	return out
}
