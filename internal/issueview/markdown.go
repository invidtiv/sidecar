package issueview

import (
	"fmt"
	"strings"
)

// FormatMarkdown renders one issue the way td monitor yanks it: a heading,
// identity, metadata, then description and acceptance when they exist. An
// epic includes its child titles so a yank of the card is still useful when
// the full child bodies were never fetched.
func FormatMarkdown(d *Data) string {
	if d == nil {
		return ""
	}
	if strings.EqualFold(d.Type, "epic") {
		return formatEpicMarkdown(d)
	}
	return formatIssueMarkdown(d)
}

func formatIssueMarkdown(d *Data) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n", d.Title)
	fmt.Fprintf(&sb, "**ID:** `%s`\n", d.ID)
	fmt.Fprintf(&sb, "**Type:** %s | **Priority:** %s | **Status:** %s\n",
		d.Type, d.Priority, d.Status)
	if d.ParentID != "" {
		fmt.Fprintf(&sb, "**Parent:** `%s`\n", d.ParentID)
	}
	writeMarkdownSection(&sb, "Description", d.Description)
	writeMarkdownSection(&sb, "Acceptance Criteria", d.Acceptance)
	return sb.String()
}

func formatEpicMarkdown(d *Data) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Epic: %s\n", d.Title)
	fmt.Fprintf(&sb, "**ID:** `%s`\n", d.ID)
	fmt.Fprintf(&sb, "**Priority:** %s | **Status:** %s\n", d.Priority, d.Status)
	writeMarkdownSection(&sb, "Description", d.Description)
	writeMarkdownSection(&sb, "Acceptance Criteria", d.Acceptance)
	if len(d.Children) == 0 {
		return sb.String()
	}
	sb.WriteString("\n## Tasks\n\n")
	for i, child := range d.Children {
		if i > 0 {
			sb.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&sb, "### %s %s\n", markdownStatusIcon(child.Status), child.Title)
		fmt.Fprintf(&sb, "**ID:** `%s`\n", child.ID)
		fmt.Fprintf(&sb, "**Type:** %s | **Priority:** %s | **Status:** %s\n",
			child.Type, child.Priority, child.Status)
	}
	return sb.String()
}

func writeMarkdownSection(sb *strings.Builder, title, body string) {
	if body == "" {
		return
	}
	fmt.Fprintf(sb, "\n## %s\n\n", title)
	sb.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		sb.WriteByte('\n')
	}
}

func markdownStatusIcon(status string) string {
	switch strings.ToLower(status) {
	case "closed":
		return "[x]"
	case "in_progress":
		return "[-]"
	case "in_review":
		return "[~]"
	case "blocked":
		return "[!]"
	default:
		return "[ ]"
	}
}
