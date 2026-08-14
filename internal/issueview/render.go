package issueview

import (
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/markdown"
)

// The pieces below are the issue's rendering, shared by every host so a modal
// and a pane show the same issue. Each returns "" when it has nothing to say,
// which lets a host omit the corresponding row.

// Heading is the issue's headline: "<id>: <title>".
func Heading(d *Data) string {
	if d == nil {
		return ""
	}
	title := d.ID
	if d.Title != "" {
		title += ": " + d.Title
	}
	return title
}

// StatusLine joins the issue's status, type, priority, and points.
func StatusLine(d *Data) string {
	if d == nil {
		return ""
	}
	var parts []string
	if d.Status != "" {
		parts = append(parts, "["+d.Status+"]")
	}
	if d.Type != "" {
		parts = append(parts, d.Type)
	}
	if d.Priority != "" {
		parts = append(parts, d.Priority)
	}
	if d.Points > 0 {
		parts = append(parts, fmt.Sprintf("%dp", d.Points))
	}
	return strings.Join(parts, "  ")
}

// ParentLine names the issue's parent.
func ParentLine(d *Data) string {
	if d == nil || d.ParentID == "" {
		return ""
	}
	return "Parent: " + d.ParentID
}

// LabelsLine lists the issue's labels.
func LabelsLine(d *Data) string {
	if d == nil || len(d.Labels) == 0 {
		return ""
	}
	return "Labels: " + strings.Join(d.Labels, ", ")
}

// Description renders the issue body as markdown wrapped to width, falling back
// to the raw text when no renderer is available. A nil renderer builds the
// default one.
func Description(renderer *markdown.Renderer, d *Data, width int) string {
	if d == nil || d.Description == "" {
		return ""
	}
	if renderer == nil {
		var err error
		if renderer, err = markdown.NewRenderer(); err != nil {
			return d.Description
		}
	}
	return strings.Join(renderer.RenderContent(d.Description, width), "\n")
}
