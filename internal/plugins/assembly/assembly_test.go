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

func TestConversationsWanted(t *testing.T) {
	tests := []struct {
		name          string
		flags         map[string]bool
		configEnabled bool
		want          bool
	}{
		{
			name:          "default flag off, config enabled",
			flags:         nil,
			configEnabled: true,
			want:          false,
		},
		{
			name:          "flag on, config enabled",
			flags:         map[string]bool{features.ConversationsPlugin.Name: true},
			configEnabled: true,
			want:          true,
		},
		{
			name:          "flag on, config disabled",
			flags:         map[string]bool{features.ConversationsPlugin.Name: true},
			configEnabled: false,
			want:          false,
		},
		{
			name:          "flag off, config enabled",
			flags:         map[string]bool{features.ConversationsPlugin.Name: false},
			configEnabled: true,
			want:          false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			initFeatures(t, tc.flags)
			cfg := config.Default()
			cfg.Plugins.Conversations.Enabled = tc.configEnabled
			if got := ConversationsWanted(cfg); got != tc.want {
				t.Errorf("ConversationsWanted = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("nil config uses defaults (flag off)", func(t *testing.T) {
		initFeatures(t, nil)
		if ConversationsWanted(nil) {
			t.Error("nil config should not want conversations with default flag off")
		}
	})
}

func TestPlan_DefaultOmitsConversations(t *testing.T) {
	initFeatures(t, nil)
	got := join(Plan(config.Default()))
	want := "td-monitor,git-status,file-browser,workspace-manager"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if ConversationsWanted(config.Default()) {
		t.Error("default config must not want conversations")
	}
}

func TestPlan_ConversationsWhenFlagOn(t *testing.T) {
	initFeatures(t, map[string]bool{features.ConversationsPlugin.Name: true})
	got := join(Plan(config.Default()))
	want := "td-monitor,git-status,file-browser,conversations,workspace-manager"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPlan_ConversationsFlagOnConfigOff(t *testing.T) {
	initFeatures(t, map[string]bool{features.ConversationsPlugin.Name: true})
	cfg := config.Default()
	cfg.Plugins.Conversations.Enabled = false
	got := join(Plan(cfg))
	want := "td-monitor,git-status,file-browser,workspace-manager"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Tasks is a global tab owned by the app shell, not a project plugin: no
// combination of its feature flag, its configured position, or the plugins
// around it may put it back in the project tab order, where registry.Reinit
// would rebuild it on every project switch.
func TestPlan_NeverPlansTasks(t *testing.T) {
	positions := []string{config.TasksPositionAfterWorkspaces, config.TasksPositionAfterNotes}
	for _, flag := range []bool{false, true} {
		for _, position := range positions {
			initFeatures(t, map[string]bool{
				features.TasksPlugin.Name: flag,
				features.NotesPlugin.Name: true,
			})
			cfg := config.Default()
			cfg.Plugins.Tasks.Position = position
			got := join(Plan(cfg))
			if strings.Contains(got, "tasks") {
				t.Fatalf("tasks planned as a project plugin (flag=%v position=%q): %q", flag, position, got)
			}
			want := "td-monitor,git-status,file-browser,workspace-manager,notes"
			if got != want {
				t.Fatalf("plan (flag=%v position=%q) = %q, want %q", flag, position, got, want)
			}
		}
	}
}

// Disabling preceding plugins shifts the tabs after them: the shortcut number
// is derived state, never a fixed promise.
func TestPlan_TabIndexIsDerived(t *testing.T) {
	initFeatures(t, map[string]bool{features.NotesPlugin.Name: true})

	full := Plan(config.Default())
	// Default: td, git, files, workspace, notes — conversations off by flag.
	if got := indexOf(full, IDNotes); got != 4 {
		t.Fatalf("notes index with default plugins = %d, want 4", got)
	}

	// With conversations enabled, notes shifts right by one.
	initFeatures(t, map[string]bool{
		features.NotesPlugin.Name:         true,
		features.ConversationsPlugin.Name: true,
	})
	withConv := Plan(config.Default())
	if got := indexOf(withConv, IDNotes); got != 5 {
		t.Fatalf("notes index with conversations = %d, want 5", got)
	}

	initFeatures(t, map[string]bool{features.NotesPlugin.Name: true})
	cfg := config.Default()
	cfg.Plugins.TDMonitor.Enabled = false
	cfg.Plugins.GitStatus.Enabled = false
	cfg.Plugins.FileBrowser.Enabled = false
	cfg.Plugins.Conversations.Enabled = false
	trimmed := Plan(cfg)
	if got := join(trimmed); got != "workspace-manager,notes" {
		t.Fatalf("trimmed plan = %q", got)
	}
	if got := indexOf(trimmed, IDNotes); got != 1 {
		t.Errorf("notes index with plugins disabled = %d, want 1", got)
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
		features.NotesPlugin.Name:         true,
		features.ConversationsPlugin.Name: true,
	})
	for _, e := range Plan(config.Default()) {
		p := e.New()
		if p.ID() != e.ID {
			t.Errorf("entry %q constructed plugin with ID %q", e.ID, p.ID())
		}
	}
}
