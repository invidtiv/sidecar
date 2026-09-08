// Package workspacecatalog projects collected workspaces into the shared
// Sessions list model. Collection and presentation stay separate: callers own
// inventory, while workspacelist owns filtering, sorting, and grouping.
package workspacecatalog

import (
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// ProjectItem converts one collected workspace into the display and ordering
// fields shared by every Sessions catalog surface.
func ProjectItem(item workspaceinventory.Item, projectName string, order int, stale bool) workspacelist.Item {
	row := workspacelist.Item{
		ID:           item.ID,
		Name:         item.Name,
		Project:      projectName,
		ProjectKey:   item.ProjectKey,
		ProjectOrder: order,
		Branch:       item.Branch,
		Task:         item.TaskID,
		Provider:     item.Provider,
		TmuxName:     item.TmuxName,
		Kind:         string(item.Kind),
		Host:         item.HostID,
	}
	if row.Project == "" {
		row.Project = item.ProjectName
	}
	switch {
	case item.Agent != nil:
		row.Status = item.Agent.Label
		row.ChangedAt = item.Agent.ChangedAt
		row.Group = workspacelist.GroupForLane(string(item.Agent.Lane))
		row.Marker = workspacelist.RowMarker{Icon: item.Agent.Icon, Lane: string(item.Agent.Lane)}
		if item.Agent.Health {
			row.Marker.Lane = ""
			switch item.Agent.Icon {
			case "✗":
				row.Marker.Tone = workspacelist.MarkerError
			case "⚠":
				row.Marker.Tone = workspacelist.MarkerWarning
			default:
				row.Marker.Tone = workspacelist.MarkerMuted
			}
		}
		if !item.Live && !item.Ambiguous {
			row.Group = workspacelist.GroupNoSession
		}
	case item.Ambiguous:
		row.Status, row.Group = "ambiguous panes", workspacelist.GroupPaused
		row.Marker = workspacelist.RowMarker{Icon: "?", Tone: workspacelist.MarkerWarning}
	case item.Live:
		row.Status, row.Group = "live", workspacelist.GroupLive
		row.Marker = workspacelist.RowMarker{Icon: "◎", Tone: workspacelist.MarkerLive}
	default:
		row.Status, row.Group = "no session", workspacelist.GroupNoSession
		if item.IsMain {
			row.Marker = workspacelist.RowMarker{Icon: "◉", Tone: workspacelist.MarkerMain}
		} else {
			row.Marker = workspacelist.RowMarker{Icon: "○", Tone: workspacelist.MarkerMuted}
		}
	}
	if stale {
		row.Status += " · stale"
	}
	row.Detail = item.TaskID
	if row.Detail == "" {
		row.Detail = item.Branch
	}
	return row
}
