// Package installui is the shared first-install surface used by td's
// not-installed view and by Configuration's integration enable route.
//
// The version package owns detection and execution. This package owns the
// tea.Cmd, the padded button label, the spinner, and the result message the
// host uses to re-probe PATH. Nothing here runs a package manager from Init
// or a render path.
package installui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/version"
)

// RegionInstall is the mouse hit-region ID for the install button.
const RegionInstall = "install-product"

// ResultMsg is a finished first-install attempt. The host re-probes PATH on
// success and toasts on failure; this package does not.
type ResultMsg struct {
	Product version.ProductID
	Outcome version.InstallOutcome
}

// Phase is where a confirmed install has got to.
type Phase uint8

const (
	PhaseIdle Phase = iota
	PhaseRunning
	PhaseFailed
)

// Model is the interactive install control: one padded button, the exact
// command it will run, and a spinner while that command is in flight.
type Model struct {
	Descriptor version.Descriptor
	Env        *version.Environment
	Plan       version.InstallPlan
	PlanErr    error
	Phase      Phase
	Problem    string
	Hover      bool
	Spinner    ui.BrailleSpinner
}

// New builds an install control for d. PlanInstall runs here, not during
// sidecar startup: callers construct this after the not-installed view is
// already on screen.
func New(d version.Descriptor, env *version.Environment) *Model {
	if env == nil {
		env = version.DefaultEnvironment()
	}
	m := &Model{Descriptor: d, Env: env}
	m.Plan, m.PlanErr = version.PlanInstall(env, d)
	return m
}

// CanInstall reports that Sidecar has a command it is willing to run.
func (m *Model) CanInstall() bool {
	return m != nil && m.PlanErr == nil && len(m.Plan.Steps) > 0
}

// Busy reports that a confirmed install is still running.
func (m *Model) Busy() bool {
	return m != nil && m.Phase == PhaseRunning
}

// ButtonLabel is the padded pill the user confirms. Spaces are part of the
// label so the hit box matches what is painted.
func ButtonLabel(d version.Descriptor) string {
	return " Install " + d.DisplayName + " "
}

// DisplayCommand is the exact string confirmation and execution share.
func (m *Model) DisplayCommand() string {
	if m == nil {
		return ""
	}
	if m.Plan.Command != "" {
		return m.Plan.Command
	}
	return version.InstallCommand(m.Descriptor)
}

// Start runs the displayed command. It is a no-op when there is nothing to
// run or an attempt is already in flight.
func (m *Model) Start() tea.Cmd {
	if m == nil || !m.CanInstall() || m.Phase == PhaseRunning {
		return nil
	}
	m.Phase = PhaseRunning
	m.Problem = ""
	m.Spinner.Start()
	env := m.Env
	d := m.Descriptor
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), version.InstallTimeout)
		defer cancel()
		return ResultMsg{Product: d.Product, Outcome: version.Install(ctx, env, d)}
	}
}

// ApplyResult settles a finished attempt. Success is the host's to act on;
// failure stays on this control so the user can retry.
func (m *Model) ApplyResult(outcome version.InstallOutcome) {
	if m == nil {
		return
	}
	m.Spinner.Stop()
	if outcome.Installed && outcome.Err == nil {
		m.Phase = PhaseIdle
		m.Problem = ""
		return
	}
	m.Phase = PhaseFailed
	if outcome.Err != nil {
		m.Problem = outcome.Err.Error()
	} else {
		m.Problem = m.Descriptor.DisplayName + " is still not on PATH after installing"
	}
}

// Tick advances the spinner. Call it from an existing animation tick so this
// control does not start a second ticker.
func (m *Model) Tick() {
	if m != nil {
		m.Spinner.Tick()
	}
}

// HandleKey treats Enter and i as confirmation of the focused button.
func (m *Model) HandleKey(msg tea.KeyMsg) tea.Cmd {
	if m == nil || m.Phase == PhaseRunning {
		return nil
	}
	switch msg.String() {
	case "enter", "i":
		return m.Start()
	}
	return nil
}

// HandleClick confirms when the pointer is on the install button.
func (m *Model) HandleClick() tea.Cmd {
	if m == nil || m.Phase == PhaseRunning {
		return nil
	}
	return m.Start()
}

// RenderButton paints the padded install pill. focused is the keyboard
// highlight; hover is the pointer.
func (m *Model) RenderButton(focused, hover bool) string {
	if m == nil || !m.CanInstall() || m.Phase == PhaseRunning {
		return ""
	}
	label := ButtonLabel(m.Descriptor)
	style := lipgloss.NewStyle().
		Foreground(styles.OnPrimaryColor).
		Background(styles.Primary).
		Padding(0, 0).
		Bold(true)
	if !focused {
		style = lipgloss.NewStyle().
			Foreground(styles.Primary).
			Background(styles.BgTertiary).
			Bold(true)
	}
	if hover {
		style = style.Background(styles.ButtonHoverColor)
	}
	if m.Phase == PhaseFailed {
		style = style.Foreground(styles.TextPrimary)
	}
	return style.Render(label)
}

// RenderProgress is the in-flight line: spinner plus the command that is
// running, so the user can still see what they confirmed.
func (m *Model) RenderProgress() string {
	if m == nil || m.Phase != PhaseRunning {
		return ""
	}
	spin := m.Spinner.View()
	label := "Installing " + m.Descriptor.DisplayName + "…"
	muted := lipgloss.NewStyle().Foreground(styles.TextMuted)
	if spin != "" {
		return spin + " " + muted.Render(label)
	}
	return muted.Render(label)
}

// RenderProblem is the last failure, if any.
func (m *Model) RenderProblem() string {
	if m == nil || m.Phase != PhaseFailed || m.Problem == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(styles.Error).Render(m.Problem)
}

// FailureToast is the error line a host should surface without leaving this
// view. Empty when the attempt succeeded.
func FailureToast(outcome version.InstallOutcome) string {
	if outcome.Installed && outcome.Err == nil {
		return ""
	}
	if outcome.Err != nil {
		return strings.TrimSpace(outcome.Err.Error())
	}
	return "install did not finish"
}
