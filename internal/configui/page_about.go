package configui

import (
	"context"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/version"
)

// About is what Sidecar is, how it got here, and where to go for help. It owns
// none of that: the version comes from the running app, the release check is
// the one the app already runs at startup, and an available update is handed to
// Sidecar's existing updater rather than reimplemented here.

const (
	regionAboutUpdater  = "config-about-updater"
	regionAboutClose    = "config-about-close"
	regionAboutRecheck  = "config-about-recheck"
	regionAboutPalette  = "config-about-palette"
	regionAboutDocs     = "config-about-docs"
	aboutControlWidth   = 48
	aboutInstallTimeout = 20 * time.Second

	// DocsURL is Sidecar's documentation site, the address the website itself
	// is published at (website/static/CNAME).
	DocsURL = "https://sidecar.haplab.com"

	creditLine  = "Built and maintained by Marcus Vorwaller in Seattle, Washington, USA · marcus@vorwaller.net"
	thanksLine  = "Many thanks to all the amazing contributors to this project."
	creditGlyph = "◱"
	sparkle     = "✦"
)

// OpenUpdaterMsg asks the host to open Sidecar's existing updater. Configuration
// does not own release notes, confirmation, progress, or install behavior, so it
// asks for the surface that does.
type OpenUpdaterMsg struct{}

// CloseConfigMsg asks the host to put Configuration away; the chips' Close
// affordance has no page-local close to call.
type CloseConfigMsg struct{}

// CheckUpdatesMsg asks the host to run its release check again, bypassing the
// per-product cache.
type CheckUpdatesMsg struct{}

// OpenPaletteMsg asks the host to open the command palette.
type OpenPaletteMsg struct{}

// OpenURLMsg asks the host to open a URL with the desktop's opener.
type OpenURLMsg struct {
	URL string
}

// aboutState is the page's asynchronous provenance lookup.
type aboutState struct {
	loaded  bool
	install version.Installation
}

func (m *Model) about() *aboutState {
	if m.aboutState == nil {
		m.aboutState = &aboutState{}
	}
	return m.aboutState
}

// installationMsg carries the resolved install provenance back to the surface.
type installationMsg struct {
	install version.Installation
}

func (installationMsg) configMsg() {}

// detectInstallationCmd resolves how this Sidecar was installed. It runs a
// subprocess (`brew --cellar`), so it is a command and never a render.
func (m *Model) detectInstallationCmd() tea.Cmd {
	env := m.installationEnv()
	latest := m.host.Update.LatestVersion
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), aboutInstallTimeout)
		defer cancel()
		return installationMsg{
			install: version.DetectInstallation(ctx, env, version.SidecarDescriptor(), latest),
		}
	}
}

func (m *Model) applyInstallation(msg installationMsg) {
	state := m.about()
	state.install = msg.install
	state.loaded = true
}

// buildRevision is the VCS revision this binary was built from, when the build
// recorded one. A tagged release carries its version in the version string; a
// development build is only identifiable by its revision, so About shows it.
var buildRevision = sync.OnceValue(func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var revision string
	var dirty bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if revision == "" {
		return ""
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if dirty {
		revision += " (modified)"
	}
	return revision
})

func (m *Model) buildAbout(b *paneBuilder) {
	state := m.about()
	b.text(PaneTitle(PageTitle(PageAbout)), "")

	b.text(SectionHeader("Installation"))
	versionText := m.host.Version
	if strings.TrimSpace(versionText) == "" {
		versionText = "unknown"
	}
	b.text(FormRow("Version", StaticField(versionText, b.controlWidth(aboutControlWidth), State{}), State{}))
	if revision := buildRevision(); revision != "" && !strings.Contains(versionText, revision) {
		b.help("Built from " + revision + ".")
	}

	b.text(FormRow("Installed with", StaticField(m.installLabel(), b.controlWidth(aboutControlWidth), State{}), State{}))
	if state.loaded && state.install.Detail != "" {
		b.help(state.install.Detail)
	}

	// No update channel row: Sidecar has no channel concept, and a selector
	// that could only ever say "Stable" would be a control over nothing.
	update := m.host.Update
	b.text(FormRow("Update status", StaticField(updateStatusLabel(update), b.controlWidth(aboutControlWidth), State{}), State{}))

	b.blank()
	if update.Available || update.AnyPending {
		b.keyChips(
			chipSpec{id: regionAboutUpdater, key: "u", label: "Update", run: func(m *Model) tea.Cmd {
				return func() tea.Msg { return OpenUpdaterMsg{} }
			}},
			chipSpec{id: regionAboutRecheck, key: "r", label: "Check again", run: func(m *Model) tea.Cmd {
				return func() tea.Msg { return CheckUpdatesMsg{} }
			}},
			chipSpec{id: regionAboutClose, key: "", keys: "[esc]", label: "Close", run: func(m *Model) tea.Cmd {
				return func() tea.Msg { return CloseConfigMsg{} }
			}},
		)
		b.blank()
		b.note("Release details and confirmation open in Sidecar's existing updater.")
	} else {
		b.keyChips(
			chipSpec{id: regionAboutRecheck, key: "r", label: "Check for updates", run: func(m *Model) tea.Cmd {
				return func() tea.Msg { return CheckUpdatesMsg{} }
			}},
		)
	}

	b.text(SectionHeader("Help"))
	b.buttons(
		buttonSpec{id: regionAboutPalette, key: "", label: "?  Open command palette", run: func(m *Model) tea.Cmd {
			return func() tea.Msg { return OpenPaletteMsg{} }
		}},
		buttonSpec{id: regionAboutDocs, key: "o", label: "O  Open documentation", run: func(m *Model) tea.Cmd {
			return func() tea.Msg { return OpenURLMsg{URL: DocsURL} }
		}},
	)
	b.note("Diagnostics shows installation details when something needs attention.")

	// The signature sits at the bottom of the pane, out of the way of the
	// settings, and is dropped entirely on a pane too short to hold both.
	b.spacer(3)
	b.text(Centered(Muted(strings.Repeat("─", 24)+" "+sparkle+" "+strings.Repeat("─", 24)), b.inner))
	b.text(Centered(Body(creditGlyph)+" "+Muted(creditLine), b.inner))
	b.text(Centered(Muted(thanksLine)+" "+Body(sparkle), b.inner))
}

// installLabel names how this Sidecar was installed, honestly reporting that it
// is still looking.
func (m *Model) installLabel() string {
	state := m.about()
	if !state.loaded {
		return "Checking…"
	}
	switch state.install.Method {
	case version.InstallMethodHomebrew:
		return "Homebrew"
	case version.InstallMethodGo:
		return "go install"
	default:
		return "Binary (unmanaged)"
	}
}

// updateStatusLabel states the release check without ever turning an unknown
// answer into reassurance.
func updateStatusLabel(update UpdateStatus) string {
	switch {
	case !update.Checked:
		return "Checking…"
	case update.Failed:
		return "Check failed"
	case update.Available && update.LatestVersion != "":
		return update.LatestVersion + " available"
	case update.Available:
		return "Update available"
	default:
		return "Up to date"
	}
}
