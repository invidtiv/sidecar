package configui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/version"
)

// The enable route is the focused child of Panels & Integrations that runs
// before a beta integration backed by an external command is switched on
// (mockup 08a). It states what it found, offers exactly one install action, and
// waits: nothing installs automatically, nothing uses sudo, and a declined or
// failed install returns to Panels with the panel still off and a reason.
//
// It is parameterized by an Integration so the next external integration gets
// the same route rather than a second copy of it. The mockup illustrates the
// route with Notes, which has no external command at all; Notes therefore never
// opens it (see decisions #2).

// ChildEnableIntegration is the enable route.
const ChildEnableIntegration ChildID = "enable-integration"

const (
	regionEnableInstall = "config-enable-install"
	regionEnableCopy    = "config-enable-copy"
	regionEnableRecheck = "config-enable-recheck"
	regionEnableCancel  = "config-enable-cancel"

	// installTimeout bounds one confirmed `brew install`. A package manager
	// that never returns must not leave the route claiming to be working.
	installTimeout = 15 * time.Minute
)

// installPhase is where a confirmed install has got to.
type installPhase uint8

const (
	installIdle installPhase = iota
	installRunning
	installDone
	installFailed
)

// enableState is the open enable route.
type enableState struct {
	integration Integration
	phase       installPhase
	// problem is why the last attempt failed, in the user's terms.
	problem string
	output  string
}

// installationEnv is the seam the install runs through. Tests substitute it so
// no test ever invokes a real package manager.
func (m *Model) installationEnv() *version.Environment {
	if m.installEnv != nil {
		return m.installEnv
	}
	return version.DefaultEnvironment()
}

// SetInstallEnvironment substitutes the process environment the enable route
// installs through.
func (m *Model) SetInstallEnvironment(env *version.Environment) { m.installEnv = env }

// toggleIntegration is what a beta integration's panel row does. Turning it off
// is just the flag. Turning it on checks that its command exists first, and
// opens the enable route when it does not — enabling a panel whose command is
// missing would be a switch that produces an empty surface.
func (m *Model) toggleIntegration(integration Integration) tea.Cmd {
	if m.flagEnabled(integration.Flag) {
		m.noteRestart()
		return saveFlagCmd(toggleNotice(integration.Name+" panel", false), integration.Flag, false)
	}
	if !integration.NeedsCommand() || (m.probed && m.commandFound(integration.Descriptor.Executable)) {
		m.noteRestart()
		return saveFlagCmd(toggleNotice(integration.Name+" panel", true), integration.Flag, true)
	}
	m.openEnableRoute(integration)
	return nil
}

// openEnableRoute opens the dependency check for an integration.
func (m *Model) openEnableRoute(integration Integration) {
	m.PushChild(ChildEnableIntegration, "Enable "+integration.Name)
	m.enable = &enableState{integration: integration}
	m.rowCursor = 0
	m.detailFocus = false
}

// buildEnableRoute paints the route. It is one screen with one recommended
// action, whichever state the machine is in.
func (m *Model) buildEnableRoute(b *paneBuilder) {
	state := m.enable
	if state == nil {
		b.lead("This integration is already enabled.")
		return
	}
	integration := state.integration
	descriptor := integration.Descriptor
	found := m.commandFound(descriptor.Executable)

	if found {
		b.text(Body(integration.Name + " is installed and ready to enable."))
	} else {
		b.text(Warning(integration.Name + " needs to be installed"))
	}
	b.text(Indented(integration.Why))

	b.text(SectionHeader("System check"))
	b.text(FormRow(integration.Name+" command", checkValue(m.probed, found, "Found on PATH", "Not found on PATH"), State{}))
	b.text(FormRow("Homebrew", checkValue(m.probed, m.brewFound, "Available", "Not found"), State{}))

	switch state.phase {
	case installRunning:
		b.blank()
		b.text(Indented("Installing " + integration.Name + " with Homebrew…"))
		b.note(version.InstallCommand(descriptor))
		b.blank()
		b.lead("This runs in the background. Sidecar will say what happened either way.")
		return
	case installFailed:
		b.blank()
		b.text(Warning("The install did not finish"))
		b.note(state.problem)
		b.blank()
		b.buttons(
			buttonSpec{id: regionEnableInstall, key: "enter", label: "Enter  Try the install again", primary: true,
				run: func(m *Model) tea.Cmd { return m.startInstall() }},
			buttonSpec{id: regionEnableCopy, key: "c", label: "C  Copy command", run: func(m *Model) tea.Cmd {
				return copyCmd(version.InstallCommand(descriptor), "Copied install command")
			}},
			buttonSpec{id: regionEnableCancel, key: "", label: "Esc  Back", run: func(m *Model) tea.Cmd {
				m.Back()
				return nil
			}},
		)
		b.blank()
		b.text(Muted(integration.Name + " is still turned off. Nothing was changed."))
		return
	}

	b.blank()
	switch {
	case found:
		b.buttons(
			buttonSpec{id: regionEnableInstall, key: "enter", label: "Enter  Enable " + integration.Name, primary: true,
				run: func(m *Model) tea.Cmd { return m.finishEnable(integration) }},
			buttonSpec{id: regionEnableCancel, key: "", label: "Esc  Cancel", run: func(m *Model) tea.Cmd {
				m.Back()
				return nil
			}},
		)
	case m.brewFound:
		b.buttons(
			buttonSpec{id: regionEnableInstall, key: "enter",
				label: "Enter  Install " + integration.Name + " with Homebrew", primary: true,
				run: func(m *Model) tea.Cmd { return m.startInstall() }},
			buttonSpec{id: regionEnableCancel, key: "", label: "Esc  Cancel", run: func(m *Model) tea.Cmd {
				m.Back()
				return nil
			}},
		)
		b.blank()
		b.text(IndentedRaw(CodeChip(version.InstallCommand(descriptor))))
		b.blank()
		b.note("Sidecar shows the install action and waits for your confirmation before it runs.")
		b.note("It never installs automatically and never uses sudo.")
	default:
		// No Homebrew: Sidecar has nothing safe to run, so it says exactly what
		// to do instead of offering an action it cannot honour.
		b.text(Indented("Homebrew is not available, so Sidecar cannot install " + integration.Name + " for you."))
		b.blank()
		b.text(Indented("Install it yourself with either of these, then return here:"))
		b.blank()
		b.text(IndentedRaw(CodeChip(version.InstallCommand(descriptor))))
		b.note(descriptor.ReleasesURL)
		b.blank()
		b.buttons(
			buttonSpec{id: regionEnableCopy, key: "c", label: "C  Copy install command", primary: true,
				run: func(m *Model) tea.Cmd {
					return copyCmd(version.InstallCommand(descriptor), "Copied install command")
				}},
			buttonSpec{id: regionEnableRecheck, key: "r", label: "R  Recheck", run: func(m *Model) tea.Cmd {
				return m.Recheck()
			}},
			buttonSpec{id: regionEnableCancel, key: "", label: "Esc  Cancel", run: func(m *Model) tea.Cmd {
				m.Back()
				return nil
			}},
		)
	}
}

// checkValue renders one system-check answer, distinguishing "still looking"
// from a settled no.
func checkValue(known, ok bool, yes, no string) string {
	if !known {
		return Muted("Checking…")
	}
	if ok {
		return Body(yes)
	}
	return Warning(no)
}

// installResultMsg carries a finished install attempt back to the surface.
type installResultMsg struct {
	integration Integration
	outcome     version.InstallOutcome
}

func (installResultMsg) configMsg() {}

// startInstall runs the confirmed install in a command. The user has read the
// exact command on screen; this is the only thing that runs it.
func (m *Model) startInstall() tea.Cmd {
	state := m.enable
	if state == nil {
		return nil
	}
	state.phase = installRunning
	state.problem = ""
	integration := state.integration
	env := m.installationEnv()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
		defer cancel()
		return installResultMsg{
			integration: integration,
			outcome:     version.InstallWithHomebrew(ctx, env, integration.Descriptor),
		}
	}
}

// applyInstallResult settles a finished install. Success enables the panel and
// returns to Panels; failure stays put with the reason and the panel still off.
func (m *Model) applyInstallResult(msg installResultMsg) tea.Cmd {
	state := m.enable
	if state == nil || state.integration.ID != msg.integration.ID {
		return m.applyDetachedInstallResult(msg)
	}
	// The route is back on screen for the same integration, so the attempt kept
	// when it was left is this one.
	if m.installing != nil && m.installing.integration.ID == msg.integration.ID {
		m.installing = nil
	}
	if msg.outcome.Err != nil {
		state.phase = installFailed
		state.problem = msg.outcome.Err.Error()
		state.output = msg.outcome.Output
		return nil
	}
	state.phase = installDone
	// The probe cache is what the rest of the surface reads, so record the new
	// command there rather than letting the page disagree with itself.
	if m.probes == nil {
		m.probes = map[string]commandProbe{}
	}
	m.probes[msg.integration.Descriptor.Executable] = commandProbe{Found: true}
	return m.finishEnable(msg.integration)
}

// applyDetachedInstallResult settles an install whose route was left while it
// ran. Escape abandons the route, not the package manager: the user confirmed
// this install, so it is still reported, and a successful one still turns the
// panel on rather than silently installing a command and leaving the panel off.
func (m *Model) applyDetachedInstallResult(msg installResultMsg) tea.Cmd {
	pending := m.installing
	if pending == nil || pending.integration.ID != msg.integration.ID {
		return nil
	}
	m.installing = nil
	name := msg.integration.Name
	if msg.outcome.Err != nil {
		problem := msg.outcome.Err.Error()
		return func() tea.Msg {
			return NoticeMsg{Message: name + " install did not finish: " + problem}
		}
	}
	if m.probes == nil {
		m.probes = map[string]commandProbe{}
	}
	m.probes[msg.integration.Descriptor.Executable] = commandProbe{Found: true}
	return saveFlagCmd("Installed "+name+" — "+name+" panel on", msg.integration.Flag, true)
}

// finishEnable turns the panel on and returns to Panels.
func (m *Model) finishEnable(integration Integration) tea.Cmd {
	m.enable = nil
	m.Back()
	m.noteRestart()
	return saveFlagCmd(toggleNotice(integration.Name+" panel", true), integration.Flag, true)
}
