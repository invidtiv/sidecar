package cli

import (
	"fmt"
	"strings"
	"sync"
)

// startupDestination is where a launch command asked the app to open. It is a
// bare page name rather than a typed Configuration page so this package stays
// free of the UI it is routing to; the app resolves the name and falls back to
// its own default when it does not recognize one.
//
// It is deliberately a one-way record made before the TUI starts: nothing here
// renders settings, validates them, or writes them. `sidecar setup` is a launch
// route, not a second configuration surface.
var (
	startupMu          sync.Mutex
	startupConfigPage  string
	startupResidual    []string
	startupResidualSet bool
)

// ConfigPageSetup is the Configuration destination `sidecar setup` asks for.
// The app treats an unknown name as "the default page", which is what keeps the
// plumbing usable by a future caller that names a different one.
const ConfigPageSetup = "setup"

// StartupConfigPage reports the Configuration destination a launch command
// recorded, if any. main passes it to the app; nothing else reads it.
func StartupConfigPage() (string, bool) {
	startupMu.Lock()
	defer startupMu.Unlock()
	return startupConfigPage, startupConfigPage != ""
}

// RemainingArgs is what the process should parse as flags after Run returns
// handled=false. A launch command consumes its own name, so `sidecar setup
// -project /x` parses -project exactly as `sidecar -project /x` does.
func RemainingArgs(args []string) []string {
	startupMu.Lock()
	defer startupMu.Unlock()
	if startupResidualSet {
		return startupResidual
	}
	return args
}

func recordStartup(page string, residual []string) {
	startupMu.Lock()
	defer startupMu.Unlock()
	startupConfigPage = page
	startupResidual = residual
	startupResidualSet = true
}

// resetStartup clears the recorded launch destination. Tests use it; the real
// process records at most one per launch.
func resetStartup() {
	startupMu.Lock()
	defer startupMu.Unlock()
	startupConfigPage = ""
	startupResidual = nil
	startupResidualSet = false
}

// runSetupLaunch answers `sidecar setup`. It prints nothing on success: the
// user asked for the app, so the app is what they get, opened on Sidecar Setup.
// Startup can still fail before the first frame — a malformed config file, a
// terminal that is not interactive — and those failures print the ordinary
// recovery guidance from internal/startupfail rather than anything special to
// this command.
func runSetupLaunch(env Env, args []string) (bool, int) {
	help := RenderHelp(RootCommand().FindSubcommand("setup"))
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			_, _ = fmt.Fprint(env.Stdout, help)
			return true, 0
		}
	}
	// setup names no destination of its own — Configuration always opens on
	// Sidecar Setup — so a word here is a mistake worth naming. Anything
	// starting with "-" is one of Sidecar's ordinary flags (and what follows it
	// is that flag's value), which the process parses for itself.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cliErrf(env.Stderr, "setup takes no arguments\n\n%s", help)
		return true, 2
	}
	recordStartup(ConfigPageSetup, args)
	return false, 0
}
