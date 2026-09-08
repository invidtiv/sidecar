package workspacecatalog

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

func TestProjectItemPreservesSessionsSemantics(t *testing.T) {
	changed := time.Unix(100, 0)
	tests := []struct {
		name      string
		item      workspaceinventory.Item
		stale     bool
		wantGroup workspacelist.Group
		wantState string
	}{
		{name: "blocked agent", item: workspaceinventory.Item{Name: "agent", Live: true, Agent: &agentstatus.Presentation{Lane: agentstatus.LaneBlocked, Label: "needs input", ChangedAt: changed}}, wantGroup: workspacelist.GroupNeedsAttention, wantState: "needs input"},
		{name: "ambiguous plain workspace", item: workspaceinventory.Item{Name: "ambiguous", Ambiguous: true}, wantGroup: workspacelist.GroupPaused, wantState: "ambiguous panes"},
		{name: "live shell", item: workspaceinventory.Item{Name: "shell", Live: true}, wantGroup: workspacelist.GroupLive, wantState: "live"},
		{name: "stale idle", item: workspaceinventory.Item{Name: "idle", Agent: &agentstatus.Presentation{Lane: agentstatus.LaneIdle, Label: "idle"}}, stale: true, wantGroup: workspacelist.GroupNoSession, wantState: "idle · stale"},
		{name: "main without session", item: workspaceinventory.Item{Name: "main", IsMain: true}, wantGroup: workspacelist.GroupNoSession, wantState: "no session"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ProjectItem(test.item, "sidecar", 3, test.stale)
			if got.Group != test.wantGroup || got.Status != test.wantState || got.Project != "sidecar" || got.ProjectOrder != 3 {
				t.Fatalf("ProjectItem() = %+v", got)
			}
		})
	}
}
