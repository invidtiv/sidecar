package assembly

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

func ids(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}

func join(entries []Entry) string { return strings.Join(ids(entries), ",") }

// initFeatures points the feature manager at an in-memory config so tests
// never read the developer's real ~/.config/sidecar/config.json.
func initFeatures(t *testing.T, flags map[string]bool) {
	t.Helper()
	cfg := config.Default()
	for k, v := range flags {
		cfg.Features.Flags[k] = v
	}
	features.Init(cfg)
}

func entriesFor(ids ...string) []Entry {
	out := make([]Entry, 0, len(ids))
	for _, id := range ids {
		out = append(out, Entry{ID: id})
	}
	return out
}

// insertTasks covers every enabled/disabled combination of the two anchors.
func TestInsertTasks_AnchorCombinations(t *testing.T) {
	tests := []struct {
		name     string
		base     []Entry
		position string
		want     string
	}{
		{
			name:     "both anchors, default position",
			base:     entriesFor("git-status", IDWorkspace, IDNotes),
			position: config.TasksPositionAfterWorkspaces,
			want:     "git-status,workspace-manager,tasks,notes",
		},
		{
			name:     "both anchors, after-notes",
			base:     entriesFor("git-status", IDWorkspace, IDNotes),
			position: config.TasksPositionAfterNotes,
			want:     "git-status,workspace-manager,notes,tasks",
		},
		{
			name:     "notes disabled, default position",
			base:     entriesFor("git-status", IDWorkspace),
			position: config.TasksPositionAfterWorkspaces,
			want:     "git-status,workspace-manager,tasks",
		},
		{
			name:     "notes disabled, after-notes falls back to workspaces",
			base:     entriesFor("git-status", IDWorkspace),
			position: config.TasksPositionAfterNotes,
			want:     "git-status,workspace-manager,tasks",
		},
		{
			name:     "workspaces absent, default position falls back to notes",
			base:     entriesFor("git-status", IDNotes, "conversations"),
			position: config.TasksPositionAfterWorkspaces,
			want:     "git-status,notes,tasks,conversations",
		},
		{
			name:     "workspaces absent, after-notes",
			base:     entriesFor("git-status", IDNotes, "conversations"),
			position: config.TasksPositionAfterNotes,
			want:     "git-status,notes,tasks,conversations",
		},
		{
			name:     "no anchors, default position appends last",
			base:     entriesFor("git-status", "conversations"),
			position: config.TasksPositionAfterWorkspaces,
			want:     "git-status,conversations,tasks",
		},
		{
			name:     "no anchors, after-notes appends last",
			base:     entriesFor("git-status", "conversations"),
			position: config.TasksPositionAfterNotes,
			want:     "git-status,conversations,tasks",
		},
		{
			name:     "empty base",
			base:     nil,
			position: config.TasksPositionAfterWorkspaces,
			want:     "tasks",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := join(insertTasks(tc.base, tc.position))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInsertTasks_DoesNotMutateBase(t *testing.T) {
	base := entriesFor("git-status", IDWorkspace, IDNotes)
	before := join(base)
	insertTasks(base, config.TasksPositionAfterWorkspaces)
	if join(base) != before {
		t.Errorf("base mutated: %q", join(base))
	}
}

func TestPlan_TasksFlagOff(t *testing.T) {
	initFeatures(t, nil)
	got := join(Plan(config.Default()))
	want := "td-monitor,git-status,file-browser,conversations,workspace-manager"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPlan_TasksAfterWorkspacesByDefault(t *testing.T) {
	initFeatures(t, map[string]bool{features.TasksPlugin.Name: true})
	got := join(Plan(config.Default()))
	want := "td-monitor,git-status,file-browser,conversations,workspace-manager,tasks"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPlan_TasksAfterNotesWhenConfigured(t *testing.T) {
	initFeatures(t, map[string]bool{
		features.TasksPlugin.Name: true,
		features.NotesPlugin.Name: true,
	})
	cfg := config.Default()
	cfg.Plugins.Tasks.Position = config.TasksPositionAfterNotes
	got := join(Plan(cfg))
	want := "td-monitor,git-status,file-browser,conversations,workspace-manager,notes,tasks"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPlan_TasksBeforeNotesByDefault(t *testing.T) {
	initFeatures(t, map[string]bool{
		features.TasksPlugin.Name: true,
		features.NotesPlugin.Name: true,
	})
	got := join(Plan(config.Default()))
	want := "td-monitor,git-status,file-browser,conversations,workspace-manager,tasks,notes"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Disabling preceding plugins shifts the Tasks tab: the shortcut number is
// derived state, never a fixed promise.
func TestPlan_TabIndexIsDerived(t *testing.T) {
	initFeatures(t, map[string]bool{features.TasksPlugin.Name: true})

	full := Plan(config.Default())
	if got := indexOf(full, IDTasks); got != 5 {
		t.Fatalf("tasks index with all plugins = %d, want 5", got)
	}

	cfg := config.Default()
	cfg.Plugins.TDMonitor.Enabled = false
	cfg.Plugins.GitStatus.Enabled = false
	cfg.Plugins.FileBrowser.Enabled = false
	cfg.Plugins.Conversations.Enabled = false
	trimmed := Plan(cfg)
	if got := join(trimmed); got != "workspace-manager,tasks" {
		t.Fatalf("trimmed plan = %q", got)
	}
	if got := indexOf(trimmed, IDTasks); got != 1 {
		t.Errorf("tasks index with plugins disabled = %d, want 1", got)
	}
}

func TestPlan_NilConfigUsesDefaults(t *testing.T) {
	initFeatures(t, nil)
	if got := join(Plan(nil)); got != join(Plan(config.Default())) {
		t.Errorf("nil config plan = %q", got)
	}
}

// Every planned entry must be constructible and report the ID the plan
// promised, or tab order would diverge from what the registry sees.
func TestPlan_EntryIDsMatchPluginIDs(t *testing.T) {
	initFeatures(t, map[string]bool{
		features.TasksPlugin.Name: true,
		features.NotesPlugin.Name: true,
	})
	for _, e := range Plan(config.Default()) {
		p := e.New()
		if p.ID() != e.ID {
			t.Errorf("entry %q constructed plugin with ID %q", e.ID, p.ID())
		}
	}
}
