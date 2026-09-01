package app

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestFormatDestination(t *testing.T) {
	t.Run("local project", func(t *testing.T) {
		got := FormatDestination(Destination{ProjectName: "Sidecar"})
		if got != "Sidecar" {
			t.Errorf("FormatDestination() = %q, want %q", got, "Sidecar")
		}
	})
	t.Run("local linked worktree", func(t *testing.T) {
		got := FormatDestination(Destination{
			ProjectName:  "Sidecar",
			WorktreeKey:  "/Users/marcus/code/sidecar-feature",
			WorktreeName: "feature",
		})
		if got != "Sidecar [[feature]]" {
			t.Errorf("FormatDestination() = %q, want %q", got, "Sidecar [[feature]]")
		}
	})
	t.Run("local main checkout omits worktree suffix", func(t *testing.T) {
		got := FormatDestination(Destination{
			ProjectName:  "Sidecar",
			WorktreeName: "main",
		})
		if got != "Sidecar" {
			t.Errorf("FormatDestination() = %q, want %q", got, "Sidecar")
		}
	})
	t.Run("remote project uses host id not SSH target", func(t *testing.T) {
		got := FormatDestination(Destination{
			HostID:      "aerie",
			ProjectName: "Sidecar",
			Root:        "marcus@aerie:sidecar",
		})
		if got != "[aerie] Sidecar" {
			t.Errorf("FormatDestination() = %q, want %q", got, "[aerie] Sidecar")
		}
	})
	t.Run("remote linked worktree", func(t *testing.T) {
		got := FormatDestination(Destination{
			HostID:       "aerie",
			ProjectName:  "Sidecar",
			WorktreeKey:  "/home/me/sidecar-feature",
			WorktreeName: "feature",
		})
		if got != "[aerie] Sidecar [[feature]]" {
			t.Errorf("FormatDestination() = %q, want %q", got, "[aerie] Sidecar [[feature]]")
		}
	})
}

func TestDestinationMatches(t *testing.T) {
	d := Destination{
		HostID:       "aerie",
		ProjectName:  "Sidecar",
		Root:         "/home/me/sidecar",
		WorktreeName: "feature",
	}
	t.Run("empty query matches everything", func(t *testing.T) {
		if !DestinationMatches(d, "") {
			t.Error("empty query should match")
		}
		if !DestinationMatches(Destination{}, "") {
			t.Error("empty query should match an empty destination")
		}
	})
	t.Run("host id", func(t *testing.T) {
		if !DestinationMatches(d, "AER") {
			t.Error("host id should match case-insensitively")
		}
	})
	t.Run("project name", func(t *testing.T) {
		if !DestinationMatches(d, "side") {
			t.Error("project name should match")
		}
	})
	t.Run("path/root", func(t *testing.T) {
		if !DestinationMatches(d, "/HOME/ME") {
			t.Error("Root should match case-insensitively")
		}
	})
	t.Run("worktree name", func(t *testing.T) {
		if !DestinationMatches(d, "Feat") {
			t.Error("worktree name should match")
		}
	})
	t.Run("no field matches", func(t *testing.T) {
		if DestinationMatches(d, "td") {
			t.Error("unrelated query should not match")
		}
	})
}

func TestBoundDestinationNavbarAndTitleHelpers(t *testing.T) {
	local := Destination{ProjectName: "Sidecar"}
	if got := BoundDestinationNavbarLabel(local); got != "Sidecar" {
		t.Errorf("navbar local = %q, want Sidecar", got)
	}
	if got := BoundDestinationTitleProject(local); got != "Sidecar" {
		t.Errorf("title project local = %q, want Sidecar", got)
	}
	if got := BoundDestinationTitleWorktree(local); got != "" {
		t.Errorf("title worktree main = %q, want empty", got)
	}

	linked := Destination{
		HostID:       "aerie",
		ProjectName:  "Sidecar",
		WorktreeKey:  "/home/me/sidecar-feature",
		WorktreeName: "feature",
	}
	if got := BoundDestinationNavbarLabel(linked); got != "[aerie] Sidecar [[feature]]" {
		t.Errorf("navbar remote worktree = %q", got)
	}
	if got := BoundDestinationTitleProject(linked); got != "[aerie] Sidecar" {
		t.Errorf("title project remote = %q, want [aerie] Sidecar", got)
	}
	if got := BoundDestinationTitleWorktree(linked); got != "feature" {
		t.Errorf("title worktree = %q, want feature", got)
	}
}

// Local `@` with no hosts still paints Overview (when available) plus
// config.projects.list only. Remote rows are a later slice.
func TestProjectSwitcherDestinationsWithoutHosts(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "one", Path: "/tmp/one"},
		{Name: "two", Path: "/tmp/two"},
	}

	m := New(plugin.NewRegistry(nil), keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", "")
	if m.overview == nil {
		t.Fatal("Overview should be available so the switcher pins it")
	}
	destinations := m.projectSwitcherDestinations("")
	if len(destinations) != 3 {
		t.Fatalf("destinations = %#v, want Overview plus the two local projects", destinations)
	}
	if destinations[0].Kind != destinationOverview || destinations[0].Name != "Overview" {
		t.Fatalf("first destination = %#v, want Overview", destinations[0])
	}
	if destinations[1].Kind != destinationProject || destinations[1].Name != "one" || destinations[1].Path != "/tmp/one" {
		t.Fatalf("second destination = %#v, want local project one", destinations[1])
	}
	if destinations[2].Kind != destinationProject || destinations[2].Name != "two" || destinations[2].Path != "/tmp/two" {
		t.Fatalf("third destination = %#v, want local project two", destinations[2])
	}
	for _, d := range destinations {
		if d.Path != "" && d.Path != "/tmp/one" && d.Path != "/tmp/two" {
			t.Fatalf("unexpected path in local-only listing: %#v", d)
		}
	}
}
