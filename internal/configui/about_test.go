package configui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/version"
)

// aboutFixture is a model on About with a described version and update state.
func aboutFixture(t *testing.T, update UpdateStatus, present map[string]bool) *Model {
	t.Helper()
	m, _ := configFixture(t, config.Default())
	m.SetInstallEnvironment(stubEnvironment(present))
	state := m.host
	state.Version = "1.0.0"
	state.Update = update
	m.SetHostState(state)
	m.Open(PageAbout)
	return m
}

func TestAboutRendersInstallationHelpAndCredit(t *testing.T) {
	m := aboutFixture(t, UpdateStatus{Checked: true}, nil)
	view := ansi.Strip(m.View(160, 45))

	for _, want := range []string{
		"About Sidecar", "Installation", "Version", "1.0.0",
		"Installed with", "Update status", "Up to date",
		"Help", "Open command palette", "Open documentation",
		"Diagnostics shows installation details when something needs attention.",
		creditLine, thanksLine,
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("About is missing %q:\n%s", want, view)
		}
	}
	// Decision #1: there is no channel subsystem, so there is no channel row.
	if strings.Contains(view, "Update channel") {
		t.Fatalf("About rendered an update channel control:\n%s", view)
	}
}

// An unknown answer is never reported as reassurance.
func TestAboutUpdateStatusIsHonest(t *testing.T) {
	cases := []struct {
		status UpdateStatus
		want   string
	}{
		{UpdateStatus{}, "Checking…"},
		{UpdateStatus{Checked: true}, "Up to date"},
		{UpdateStatus{Checked: true, Failed: true}, "Check failed"},
		{UpdateStatus{Checked: true, Available: true, LatestVersion: "1.1.0"}, "1.1.0 available"},
	}
	for _, tc := range cases {
		if got := updateStatusLabel(tc.status); got != tc.want {
			t.Fatalf("updateStatusLabel(%#v) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// An available update is handed to the updater Sidecar already has.
func TestAboutOpensTheExistingUpdater(t *testing.T) {
	m := aboutFixture(t, UpdateStatus{Checked: true, Available: true, LatestVersion: "1.1.0"}, nil)
	view := ansi.Strip(m.View(160, 45))
	for _, want := range []string{"1.1.0 available", "Open updater",
		"Release details and confirmation open in Sidecar's existing updater."} {
		if !strings.Contains(view, want) {
			t.Fatalf("the update state is missing %q:\n%s", want, view)
		}
	}

	cmd := runByID(t, m, regionAboutUpdater)
	if cmd == nil {
		t.Fatal("Open updater did nothing")
	}
	if _, ok := cmd().(OpenUpdaterMsg); !ok {
		t.Fatalf("Open updater sent %#v, not a request for the existing updater", cmd())
	}
}

// With nothing to install, the page still offers to look again.
func TestAboutRechecksUpdates(t *testing.T) {
	m := aboutFixture(t, UpdateStatus{Checked: true}, nil)
	m.View(160, 45)
	cmd := runByID(t, m, regionAboutRecheck)
	if _, ok := cmd().(CheckUpdatesMsg); !ok {
		t.Fatalf("Check again sent %#v", cmd())
	}
	for _, c := range m.controls {
		if c.id == regionAboutUpdater {
			t.Fatal("an updater action was offered with no update available")
		}
	}
}

// The documentation link goes to the OS opener, at the address the docs site is
// actually published at.
func TestAboutDocumentationOpensTheDocsURL(t *testing.T) {
	m := aboutFixture(t, UpdateStatus{Checked: true}, nil)
	m.View(160, 45)
	cmd := runByID(t, m, regionAboutDocs)
	msg, ok := cmd().(OpenURLMsg)
	if !ok {
		t.Fatalf("Open documentation sent %#v", cmd())
	}
	if msg.URL != DocsURL {
		t.Fatalf("Open documentation opened %q", msg.URL)
	}
}

func TestAboutCommandPaletteAction(t *testing.T) {
	m := aboutFixture(t, UpdateStatus{Checked: true}, nil)
	m.View(160, 45)
	cmd := runByID(t, m, regionAboutPalette)
	if _, ok := cmd().(OpenPaletteMsg); !ok {
		t.Fatalf("Open command palette sent %#v", cmd())
	}
}

// Provenance is resolved in a command; until it settles the page says so.
func TestAboutProvenanceLoadsAsynchronously(t *testing.T) {
	m := aboutFixture(t, UpdateStatus{Checked: true}, map[string]bool{"sidecar": true})
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "Checking…") {
		t.Fatalf("About claimed provenance before resolving it:\n%s", view)
	}

	msg := m.detectInstallationCmd()()
	installed, ok := msg.(installationMsg)
	if !ok {
		t.Fatalf("provenance produced %#v", msg)
	}
	m.Handle(installed)
	view := ansi.Strip(m.View(160, 45))
	if strings.Contains(view, "Checking…") {
		t.Fatalf("About still says it is checking after the result arrived:\n%s", view)
	}
	if !strings.Contains(view, "Binary (unmanaged)") {
		t.Fatalf("the resolved install method is not on screen:\n%s", view)
	}
}

func TestAboutProvenanceNamesEveryMethod(t *testing.T) {
	cases := map[version.InstallMethod]string{
		version.InstallMethodHomebrew: "Homebrew",
		version.InstallMethodGo:       "go install",
		version.InstallMethodBinary:   "Binary (unmanaged)",
	}
	for method, want := range cases {
		m := aboutFixture(t, UpdateStatus{Checked: true}, nil)
		m.Handle(installationMsg{install: version.Installation{Method: method}})
		if got := m.installLabel(); got != want {
			t.Fatalf("install method %q rendered as %q, want %q", method, got, want)
		}
	}
}
