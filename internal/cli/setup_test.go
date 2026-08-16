package cli

import (
	"bytes"
	"strings"
	"testing"
)

// `sidecar setup` is a launch route: Run must recognize it, record where the
// app should open, and report handled=false so this same process goes on to
// start the TUI. A handled=true here would print nothing and exit — the command
// would silently do nothing at all.
func TestSetupLaunchesTheApp(t *testing.T) {
	t.Cleanup(resetStartup)
	resetStartup()

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"setup"}, &out, &errOut)
	if handled || code != 0 {
		t.Fatalf("Run(setup) = handled %v, code %d; want false, 0", handled, code)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("setup wrote output: stdout %q stderr %q", out.String(), errOut.String())
	}
	page, ok := StartupConfigPage()
	if !ok || page != ConfigPageSetup {
		t.Fatalf("StartupConfigPage() = %q, %v; want %q, true", page, ok, ConfigPageSetup)
	}
}

// Nothing is recorded when the user did not ask for it, so an ordinary launch
// opens the surface it always did.
func TestNoStartupDestinationWithoutSetup(t *testing.T) {
	t.Cleanup(resetStartup)
	resetStartup()

	var out, errOut bytes.Buffer
	if handled, _ := Run([]string{"-project", "/tmp"}, &out, &errOut); handled {
		t.Fatal("an unknown first argument must not be handled by the CLI")
	}
	if page, ok := StartupConfigPage(); ok {
		t.Fatalf("StartupConfigPage() = %q, true; want no destination", page)
	}
}

// The command name is consumed; Sidecar's own flags after it are not, so
// `sidecar setup -project x` parses -project exactly as a plain launch does.
func TestSetupLeavesRemainingArgsForFlagParsing(t *testing.T) {
	t.Cleanup(resetStartup)
	resetStartup()

	args := []string{"setup", "-project", "/tmp/one"}
	var out, errOut bytes.Buffer
	if handled, _ := Run(args, &out, &errOut); handled {
		t.Fatal("setup must not be handled")
	}
	remaining := RemainingArgs(args)
	if len(remaining) != 2 || remaining[0] != "-project" || remaining[1] != "/tmp/one" {
		t.Fatalf("RemainingArgs = %v; want [-project /tmp/one]", remaining)
	}
}

func TestSetupHelpAndUsageErrors(t *testing.T) {
	t.Cleanup(resetStartup)

	resetStartup()
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"setup", "--help"}, &out, &errOut)
	if !handled || code != 0 || !strings.Contains(out.String(), "Usage: sidecar setup") {
		t.Fatalf("setup --help = handled %v code %d out %q", handled, code, out.String())
	}
	if _, ok := StartupConfigPage(); ok {
		t.Fatal("printing help must not record a startup destination")
	}

	resetStartup()
	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"setup", "projects"}, &out, &errOut)
	if !handled || code != 2 || !strings.Contains(errOut.String(), "takes no arguments") {
		t.Fatalf("setup projects = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if _, ok := StartupConfigPage(); ok {
		t.Fatal("a usage error must not record a startup destination")
	}
}

// An agent discovers the command the same way it discovers the others.
func TestSetupListedForAgents(t *testing.T) {
	rendered := RenderAgents(RootCommand())
	if !strings.Contains(rendered, "sidecar setup") {
		t.Fatalf("sidecar agents does not list setup:\n%s", rendered)
	}
}
