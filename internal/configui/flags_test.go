package configui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

// flagsFixture is a model on Feature Flags with a temp config file and the
// feature manager pointed at it.
func flagsFixture(t *testing.T, mutate func(*config.Config)) *Model {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	m, _ := configFixture(t, cfg)
	m.Open(PageFlags)
	return m
}

// Registering a feature is all it takes to make it reachable. Without this, a
// flag added to internal/features is settable only by hand-editing
// config.json — which is how five of them sat unreachable behind a curated
// four-item list.
func TestFlagsPageListsEveryRegisteredFlag(t *testing.T) {
	m := flagsFixture(t, nil)
	// Tall enough that the pane's height clamp cuts nothing: the detail pane
	// truncates rather than scrolling, so a short view would pass this test by
	// hiding the very rows it is meant to check.
	view := ansi.Strip(m.View(160, 200))

	listed := make(map[string]bool, len(previews()))
	for _, item := range previews() {
		listed[item.flag] = true
	}
	for _, feature := range features.ListAll() {
		if !listed[feature.Name] {
			t.Fatalf("%s is registered but Feature Flags does not list it", feature.Name)
		}
		item := previewCopy[feature.Name]
		label := item.label
		if label == "" {
			label = feature.Name
		}
		if !strings.Contains(view, label) {
			t.Fatalf("%s is listed as %q but does not render:\n%s", feature.Name, label, view)
		}
	}
}

// A flag with no hand-written copy still renders, using the registry's own name
// and description. This is what lets a new flag land without touching configui.
func TestFlagsPageFallsBackToRegistryCopy(t *testing.T) {
	for _, item := range previews() {
		if _, curated := previewCopy[item.flag]; curated {
			continue
		}
		if item.label != item.flag {
			t.Fatalf("uncurated %s labelled %q, want the flag name", item.flag, item.label)
		}
		if item.help == "" {
			t.Fatalf("uncurated %s has no help text", item.flag)
		}
	}
}

// A flag another page owns is reported here but not settable here: activating
// the row navigates to the owning control instead of writing the flag. Two
// switches over one value is how the pair Panels keeps consistent — the flag
// and the plugin's own enabled key — would start disagreeing.
func TestFlagsPageDefersOwnedFlagsToTheirPage(t *testing.T) {
	owned := 0
	for _, item := range previews() {
		if item.owner == "" {
			continue
		}
		owned++
		t.Run(item.flag, func(t *testing.T) {
			m := flagsFixture(t, nil)
			before := m.flagEnabled(item.flag)
			activate(t, m, regionFlag+item.flag)

			if _, ok := loadSaved(t).Features.Flags[item.flag]; ok {
				t.Fatalf("%s was written from a page that does not own it", item.flag)
			}
			if m.flagEnabled(item.flag) != before {
				t.Fatalf("%s changed on a read-only row", item.flag)
			}
			if m.Route().Page != item.owner {
				t.Fatalf("activating %s landed on %q, want %q", item.flag, m.Route().Page, item.owner)
			}
		})
	}
	if owned == 0 {
		t.Fatal("no owned flags in the list, so the deferral is untested")
	}
}

// Every settable flag round-trips to features.flags under its real name.
func TestFlagsRoundTrip(t *testing.T) {
	for _, item := range previews() {
		if item.owner != "" {
			continue
		}
		t.Run(item.flag, func(t *testing.T) {
			m := flagsFixture(t, nil)
			before := m.flagEnabled(item.flag)
			activate(t, m, regionFlag+item.flag)
			got, ok := loadSaved(t).Features.Flags[item.flag]
			if !ok {
				t.Fatalf("%s was not written to features.flags", item.flag)
			}
			if got == before {
				t.Fatalf("%s did not change (still %v)", item.flag, got)
			}
			if m.flagEnabled(item.flag) != got {
				t.Fatalf("the page disagrees with the file for %s", item.flag)
			}
			// The live answer must move with the file, or a saved flag would do
			// nothing until a restart it does not need.
			if features.IsEnabled(item.flag) != got {
				t.Fatalf("features.IsEnabled(%s) = %v, file says %v", item.flag, features.IsEnabled(item.flag), got)
			}
		})
	}
}

// Only a flag that is genuinely read once at startup claims a restart.
func TestFlagsRestartNoteIsPerFlag(t *testing.T) {
	m := flagsFixture(t, nil)
	activate(t, m, regionFlag+features.WorkspaceDocPanes.Name)
	if view := ansi.Strip(m.View(160, 200)); strings.Contains(view, panelRestartNote) {
		t.Fatalf("a live flag claimed it needed a restart:\n%s", view)
	}

	m = flagsFixture(t, nil)
	activate(t, m, regionFlag+features.CrossProjectOverview.Name)
	if view := ansi.Strip(m.View(160, 200)); !strings.Contains(view, panelRestartNote) {
		t.Fatalf("a startup-scoped flag did not mention the restart:\n%s", view)
	}
}

// Turning on Full tmux attach is what makes the Terminal page's attach chord
// editable — the two pages share one config state, not a copy each.
func TestFullAttachUnlocksTheTerminalAttachControl(t *testing.T) {
	m := flagsFixture(t, func(cfg *config.Config) {
		cfg.Plugins.Workspace.InteractiveAttachKey = "ctrl+]"
	})
	m.Navigate(PageTerminal)
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "Turn on Full tmux attach under Feature Flags") {
		t.Fatalf("the attach control was not locked to begin with:\n%s", view)
	}

	m.Navigate(PageFlags)
	activate(t, m, regionFlag+features.TmuxFullAttach.Name)
	m.Navigate(PageTerminal)
	view := ansi.Strip(m.View(160, 45))
	if strings.Contains(view, "Turn on Full tmux attach under Feature Flags") {
		t.Fatalf("the attach control stayed locked after enabling the flag:\n%s", view)
	}
	if !strings.Contains(view, "Ctrl+]") {
		t.Fatalf("the attach chord is not editable after enabling the flag:\n%s", view)
	}
}

// Search finds a flag by the name a user reads in config.json, and lands them
// on a page that can actually set it.
func TestSearchFindsFlagsByConfigName(t *testing.T) {
	for _, item := range previews() {
		t.Run(item.flag, func(t *testing.T) {
			matches := Search(item.flag)
			if len(matches) == 0 {
				t.Fatalf("searching %q found nothing", item.flag)
			}
			want := PageFlags
			if item.owner != "" {
				want = item.owner
			}
			for _, match := range matches {
				if match.Label == item.label && match.Page == want {
					return
				}
			}
			t.Fatalf("searching %q did not offer %q on %q: %+v", item.flag, item.label, want, matches)
		})
	}
}
