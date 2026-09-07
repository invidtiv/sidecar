package broadcastmodal

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/agentbroadcast"
	"github.com/marcus/sidecar/internal/notify"
)

// reported is the result minus the rows the user deselected. Unchecking a box
// is a decision the user just made and watched happen; reporting it back as a
// skipped recipient buries the rows that carry news — what was sent, and what
// refused — under a list of things nobody asked for.
func reported(result agentbroadcast.Result) []agentbroadcast.Recipient {
	out := make([]agentbroadcast.Recipient, 0, len(result.Recipients))
	for _, row := range result.Recipients {
		if deselected(row) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func deselected(row agentbroadcast.Recipient) bool {
	return row.Outcome == agentbroadcast.OutcomeSkipped &&
		row.Reason != nil && row.Reason.Code == agentbroadcast.ReasonExcluded
}

func SummaryTitle(result agentbroadcast.Result) string {
	sum := result.Summary
	for _, row := range result.Recipients {
		if deselected(row) {
			sum.Skipped--
		}
	}
	sum.Skipped = max(0, sum.Skipped)
	if sum.Unknown > 0 {
		return fmt.Sprintf("Broadcast sent to %d agents (%d skipped, %d unknown)", sum.Submitted, sum.Skipped, sum.Unknown)
	}
	return fmt.Sprintf("Broadcast sent to %d agents (%d skipped)", sum.Submitted, sum.Skipped)
}

// Notifications is the toast plus the per-target centre rows for one result.
func Notifications(result agentbroadcast.Result) []notify.Notification {
	summary := notify.Alert(notify.SourceBroadcast, notify.SeverityInfo, SummaryTitle(result)).Notification
	exp := time.Now().UTC().Add(12 * time.Second)
	summary.ExpiresAt = &exp
	out := []notify.Notification{summary}
	for _, row := range reported(result) {
		n := notify.Notification{
			Source:   notify.SourceBroadcast,
			Severity: rowSeverity(row),
			Title:    rowTitle(row),
		}
		if row.Reason != nil {
			n.Body = row.Reason.Code
			if row.Reason.Message != "" {
				n.Body += ": " + row.Reason.Message
			}
		}
		out = append(out, n)
	}
	return out
}

func NotifyCmd(result agentbroadcast.Result) tea.Cmd {
	notes := Notifications(result)
	cmds := make([]tea.Cmd, 0, len(notes))
	for _, n := range notes {
		n := n
		cmds = append(cmds, func() tea.Msg { return notify.PostMsg{Notification: n} })
	}
	return tea.Batch(cmds...)
}

func NotifyError(err error) tea.Cmd {
	return func() tea.Msg {
		return notify.Alert(notify.SourceBroadcast, notify.SeverityError, err.Error())
	}
}

func rowTitle(row agentbroadcast.Recipient) string {
	return fmt.Sprintf("%s %s", recipientName(row), row.Outcome)
}

func rowSeverity(row agentbroadcast.Recipient) notify.Severity {
	switch row.Outcome {
	case agentbroadcast.OutcomeSubmitted:
		return notify.SeverityInfo
	case agentbroadcast.OutcomeUnknown:
		return notify.SeverityWarning
	default:
		return notify.SeverityWarning
	}
}
