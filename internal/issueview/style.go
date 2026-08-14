package issueview

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/styles"
)

// StatusLabel turns a td status token into the uppercase label the card
// draws, using the model's names rather than invented ones (in_review stays
// IN REVIEW, not REVIEWABLE).
func StatusLabel(status string) string {
	s := strings.TrimSpace(status)
	if s == "" {
		return ""
	}
	return strings.ToUpper(strings.ReplaceAll(s, "_", " "))
}

func statusStyle(status string) lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true)
	switch strings.ToLower(status) {
	case "in_review":
		return s.Foreground(styles.Primary)
	case "in_progress":
		return s.Foreground(styles.Warning)
	case "open":
		return s.Foreground(styles.Info)
	case "blocked":
		return s.Foreground(styles.Error)
	case "closed":
		return s.Foreground(styles.Success)
	default:
		return s.Foreground(styles.TextMuted)
	}
}

func priorityStyle(priority string) lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true)
	switch strings.ToUpper(priority) {
	case "P0":
		return s.Foreground(styles.Error)
	case "P1":
		return s.Foreground(styles.Error)
	case "P2":
		return s.Foreground(styles.Info)
	default:
		return lipgloss.NewStyle().Foreground(styles.TextMuted)
	}
}

func typeIcon(t string) string {
	switch strings.ToLower(t) {
	case "epic":
		return "◆"
	case "feature":
		return "●"
	case "bug":
		return "✗"
	case "task":
		return "■"
	case "chore":
		return "○"
	default:
		return "•"
	}
}

func typeStyle(t string) lipgloss.Style {
	switch strings.ToLower(t) {
	case "epic":
		return lipgloss.NewStyle().Foreground(styles.Primary)
	case "feature":
		return lipgloss.NewStyle().Foreground(styles.Success)
	case "bug":
		return lipgloss.NewStyle().Foreground(styles.Error)
	case "task":
		return lipgloss.NewStyle().Foreground(styles.Info)
	default:
		return lipgloss.NewStyle().Foreground(styles.TextMuted)
	}
}

func statusPlainStyle(status string) lipgloss.Style {
	switch strings.ToLower(status) {
	case "in_review":
		return lipgloss.NewStyle().Foreground(styles.Primary)
	case "in_progress":
		return lipgloss.NewStyle().Foreground(styles.Warning)
	case "open":
		return lipgloss.NewStyle().Foreground(styles.Info)
	case "blocked":
		return lipgloss.NewStyle().Foreground(styles.Error)
	case "closed":
		return lipgloss.NewStyle().Foreground(styles.TextMuted)
	default:
		return lipgloss.NewStyle().Foreground(styles.TextMuted)
	}
}
