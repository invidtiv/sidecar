// Package configchecks answers "is Sidecar ready to work here?" It is the
// decision layer behind Sidecar Setup and Diagnostics: it inspects the
// environment and the configuration, and reports what it found, why it matters,
// and which focused repair route addresses it.
//
// Nothing here renders, and nothing here mutates as a side effect of checking.
// The one mutation the package offers — adding the Sidecar line to a project's
// agent instructions — is an explicit call a caller makes only after the user
// has seen the exact addition and confirmed it.
//
// Every check reaches the outside world through Env, so tests describe an
// environment instead of arranging one.
package configchecks

import (
	"os"
	"os/exec"
	"runtime"

	"github.com/marcus/sidecar/internal/config"
)

// ID names a check. It is the stable key a page looks a result up by.
type ID string

const (
	// CheckProjects is whether Sidecar knows where the user's code lives.
	CheckProjects ID = "projects"
	// CheckTmux is tmux availability and version.
	CheckTmux ID = "tmux"
	// CheckTerminalColors is 24-bit color capability.
	CheckTerminalColors ID = "terminal-colors"
	// CheckAgentInstructions is whether the active project's agent file points
	// agents at `sidecar agents`.
	CheckAgentInstructions ID = "agent-instructions"
	// CheckConfiguration is whether the config file on disk still reads.
	CheckConfiguration ID = "configuration"
)

// RepairID names the focused route that addresses a problem. It is a plain
// string rather than a route type so the checks stay independent of the
// Configuration surface that renders them.
type RepairID string

const (
	RepairNone              RepairID = ""
	RepairTmux              RepairID = "repair-tmux"
	RepairTerminalColors    RepairID = "repair-terminal-colors"
	RepairAgentInstructions RepairID = "repair-agent-instructions"
	RepairAddProject        RepairID = "repair-add-project"
	RepairConfiguration     RepairID = "repair-configuration"
)

// Badge labels the action a row offers. An empty badge is a quiet, healthy row
// that must not look clickable.
const (
	BadgeFix  = "FIX"
	BadgeAdd  = "ADD"
	BadgeOpen = "OPEN"
)

// Result is one check's finding: its status, the evidence behind it, and the
// repair that addresses it.
type Result struct {
	ID    ID
	Title string
	OK    bool

	// Summary is the one-line detail shown beside the title, in either state.
	Summary string
	// Evidence is what the check actually observed, shown in the repair route
	// so a user can see why Sidecar reached its conclusion.
	Evidence []string

	// Action and ActionDetail are how Sidecar Setup phrases the work when this
	// check needs attention.
	Action       string
	ActionDetail string

	// Badge is the label on an actionable row; Repair is the route it opens.
	Badge  string
	Repair RepairID
}

// Actionable reports that this result should render as a control.
func (r Result) Actionable() bool { return r.Repair != RepairNone && r.Badge != "" }

// Results is a run's findings, in check order.
type Results []Result

// Get returns a result by ID.
func (rs Results) Get(id ID) (Result, bool) {
	for _, r := range rs {
		if r.ID == id {
			return r, true
		}
	}
	return Result{}, false
}

// Problems returns the results that need attention, in check order.
func (rs Results) Problems() Results {
	var out Results
	for _, r := range rs {
		if !r.OK {
			out = append(out, r)
		}
	}
	return out
}

// Healthy returns the results that are fine, in check order.
func (rs Results) Healthy() Results {
	var out Results
	for _, r := range rs {
		if r.OK {
			out = append(out, r)
		}
	}
	return out
}

// Env is everything the checks touch outside their own process state. The zero
// value is unusable; call DefaultEnv and override the pieces a test cares about.
type Env struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// Output runs a command and returns its combined stdout.
	Output func(name string, args ...string) ([]byte, error)
	// ReadFile reads a file. Repair routes share it so a test can describe a
	// project's AGENTS.md without writing one.
	ReadFile func(string) ([]byte, error)
	// Stat reports whether a path exists and what it is.
	Stat func(string) (os.FileInfo, error)
	GOOS string
}

// DefaultEnv is the real environment.
func DefaultEnv() Env {
	return Env{
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Output: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).Output()
		},
		ReadFile: os.ReadFile,
		Stat:     os.Stat,
		GOOS:     runtime.GOOS,
	}
}

func (e Env) withDefaults() Env {
	base := DefaultEnv()
	if e.Getenv == nil {
		e.Getenv = base.Getenv
	}
	if e.LookPath == nil {
		e.LookPath = base.LookPath
	}
	if e.Output == nil {
		e.Output = base.Output
	}
	if e.ReadFile == nil {
		e.ReadFile = base.ReadFile
	}
	if e.Stat == nil {
		e.Stat = base.Stat
	}
	if e.GOOS == "" {
		e.GOOS = base.GOOS
	}
	return e
}

// Input is what a run needs to know about this Sidecar.
type Input struct {
	// Config is the configuration the app is running with. The file on disk is
	// re-read separately: the app loaded one at startup, but the file may have
	// been edited since.
	Config *config.Config
	// ConfigPath is the config file to re-read. Empty means config.ConfigPath().
	ConfigPath string
	// ProjectDir is the active project's directory; empty when there is none.
	ProjectDir string
	// ProjectName is what to call that project in copy.
	ProjectName string

	Env Env
}

func (in Input) env() Env { return in.Env.withDefaults() }

func (in Input) configPath() string {
	if in.ConfigPath != "" {
		return in.ConfigPath
	}
	return config.ConfigPath()
}

// Run performs every check. It does filesystem and subprocess work, so callers
// run it inside a tea.Cmd and cache the results — never on a render path.
func Run(in Input) Results {
	return Results{
		checkTerminalColors(in),
		checkTmux(in),
		checkConfiguration(in),
		checkProjects(in),
		checkAgentInstructions(in),
	}
}

// checkProjects reports whether Sidecar has been told where the user's code is.
func checkProjects(in Input) Result {
	count := 0
	if in.Config != nil {
		count = len(in.Config.Projects.List)
	}
	if count > 0 {
		summary := "1 configured"
		if count != 1 {
			summary = plural(count, "configured")
		}
		return Result{ID: CheckProjects, Title: "Projects", OK: true, Summary: summary}
	}
	return Result{
		ID:           CheckProjects,
		Title:        "Projects",
		OK:           false,
		Summary:      "No projects configured · add a project to get started",
		Action:       "Add a project",
		ActionDetail: "Tell Sidecar where your code lives",
		Badge:        BadgeAdd,
		Repair:       RepairAddProject,
	}
}
