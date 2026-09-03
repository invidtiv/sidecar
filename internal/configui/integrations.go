package configui

import (
	"os/exec"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/version"
)

// Some Sidecar surfaces are backed by a command the user has to have. Whether
// that command is present is a fact about the machine, so it is answered in a
// command and cached here — the same rule the readiness checks follow. Nothing
// on a render path touches PATH.

// Integration is the install half of a plugin descriptor: what the enable route
// needs to check a command, offer a confirmed install, and explain a failure.
// It is a projection of plugin.Descriptor rather than a second catalog —
// enablement itself belongs to the descriptor, which knows the config key.
type Integration struct {
	// ID is the plugin's descriptor ID, used for region IDs and probe lookups.
	ID string
	// Name is what the user calls it.
	Name string
	// Why is one line saying what enabling it gets the user.
	Why string
	// Descriptor names the command and the formula. A zero Descriptor means the
	// integration ships inside Sidecar and has nothing to install — Notes is
	// exactly that, and it must never reach the enable route.
	Descriptor version.Descriptor
}

// NeedsCommand reports whether enabling this integration depends on something
// outside Sidecar.
func (i Integration) NeedsCommand() bool { return i.Descriptor.Executable != "" }

// integrationFor projects one plugin descriptor onto the enable route.
func integrationFor(d plugin.Descriptor) Integration {
	return Integration{ID: d.ID, Name: d.Name, Why: d.Why, Descriptor: d.Integration}
}

// TasksIntegration is the Tasks command suite: a real external product with a
// Homebrew formula, so enabling it can genuinely fail for a reason Sidecar can
// explain and offer to fix.
//
// It exists apart from the injected catalog because probeCommands runs before
// anything has handed this surface a descriptor list, and what to look for on
// PATH cannot wait for that.
func TasksIntegration() Integration {
	return Integration{
		ID:         "tasks",
		Name:       "Tasks",
		Why:        "Tasks adds an embedded task board to Sidecar's global space. It is a beta integration.",
		Descriptor: version.TasksDescriptor(),
	}
}

// defaultLookPath is the real PATH lookup, used when no Env has been supplied.
func defaultLookPath(name string) (string, error) { return exec.LookPath(name) }

// commandProbe is what Sidecar found out about one external command.
type commandProbe struct {
	Found bool
	Path  string
}

// probeMsg carries a completed environment probe back to the surface.
type probeMsg struct {
	commands map[string]commandProbe
	brew     bool
	goTool   bool
}

func (probeMsg) configMsg() {}

// probeCommands is the set of commands the Configuration surface reports on:
// the integrations' own executables plus td, whose panel is unavailable in a
// way worth explaining rather than silently rendering as an empty tab.
func probeCommands() []string {
	return []string{"td", TasksIntegration().Descriptor.Executable}
}

// ProbeCmd looks for the commands Configuration reports on. It runs through the
// same Env seam the readiness checks use, so a test describes a machine instead
// of having one.
func (m *Model) ProbeCmd() tea.Cmd {
	env := m.checkInput.Env
	lookPath := env.LookPath
	if lookPath == nil {
		lookPath = defaultLookPath
	}
	names := probeCommands()
	return func() tea.Msg {
		msg := probeMsg{commands: make(map[string]commandProbe, len(names))}
		for _, name := range names {
			path, err := lookPath(name)
			msg.commands[name] = commandProbe{Found: err == nil, Path: path}
		}
		_, err := lookPath("brew")
		msg.brew = err == nil
		_, err = lookPath("go")
		msg.goTool = err == nil
		return msg
	}
}

// applyProbe caches a completed probe.
func (m *Model) applyProbe(msg probeMsg) {
	m.probes = msg.commands
	m.brewFound = msg.brew
	m.goFound = msg.goTool
	m.probed = true
}

// commandFound reports the cached answer for one command. Before the probe has
// finished this is false, and pages say "checking" rather than "missing".
func (m *Model) commandFound(name string) bool { return m.probes[name].Found }

// flagEnabled reads a feature flag from the configuration the surface is
// editing, falling back to the build's own default. It is deliberately not
// features.IsEnabled: this page writes the config file, and a flag absent from
// features.flags means "the default", not "off".
func (m *Model) flagEnabled(name string) bool {
	if flags := m.Config().Features.Flags; flags != nil {
		if enabled, ok := flags[name]; ok {
			return enabled
		}
	}
	return features.DefaultEnabled(name)
}

// saveFlagCmd writes a feature flag through the features package, which is the
// only thing that both persists the flag and keeps the running process's answer
// to features.IsEnabled in step with the file.
func saveFlagCmd(notice, flag string, enabled bool) tea.Cmd {
	return SaveCmd(notice, func() error { return features.SetEnabled(flag, enabled) })
}
