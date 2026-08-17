package version

import (
	"context"
	"errors"
	"strings"
)

// Installing a product Sidecar does not yet have is a different act from
// updating one it does. The updater deliberately never crosses that line — a
// confirmed update plan may run `brew upgrade`, never `brew install` — so the
// one place a first install can happen lives here, behind an explicit call
// nothing else makes on its own.
//
// The rules this path keeps:
//   - It runs only when a person has read the exact command and confirmed it.
//   - It never uses sudo and never elevates.
//   - It never claims success from an exit code alone: the command has to be
//     resolvable afterwards, or the install is reported as failed.

// ErrNoFormula reports a product with no Homebrew formula to install from.
var ErrNoFormula = errors.New("no Homebrew formula for this product")

// ErrNoHomebrew reports that Homebrew is not available on this machine.
var ErrNoHomebrew = errors.New("homebrew is not installed")

// InstallOutcome is the settled result of one confirmed install attempt.
type InstallOutcome struct {
	// Installed is true only when the product's command resolves afterwards.
	Installed bool
	// Command is exactly what was run, for a message the user can act on.
	Command string
	Output  string
	Err     error
}

// HomebrewAvailable reports whether `brew` is on PATH.
func HomebrewAvailable(env *Environment) bool {
	if env == nil || env.LookPath == nil {
		return false
	}
	_, err := env.LookPath("brew")
	return err == nil
}

// CommandAvailable reports whether a product's primary executable is on PATH.
func CommandAvailable(env *Environment, d Descriptor) bool {
	if env == nil || env.LookPath == nil {
		return false
	}
	_, err := env.LookPath(d.Executable)
	return err == nil
}

// InstallCommand is the exact command InstallWithHomebrew would run, so the
// confirmation, the copyable instruction, and the execution are the same string.
func InstallCommand(d Descriptor) string { return d.InstallHint() }

// InstallWithHomebrew installs a product's formula. It is never called without
// an explicit user confirmation, and it verifies the result rather than
// trusting the package manager's exit code.
func InstallWithHomebrew(ctx context.Context, env *Environment, d Descriptor) InstallOutcome {
	outcome := InstallOutcome{Command: InstallCommand(d)}
	if d.Formula == "" {
		outcome.Err = ErrNoFormula
		return outcome
	}
	if env == nil {
		env = DefaultEnvironment()
	}
	if !HomebrewAvailable(env) {
		outcome.Err = ErrNoHomebrew
		return outcome
	}
	out, err := env.Runner.Run(ctx, "brew", "install", d.Formula)
	outcome.Output = strings.TrimSpace(out)
	if err != nil {
		outcome.Err = err
		return outcome
	}
	// A clean exit is not the claim; a resolvable command is.
	if !CommandAvailable(env, d) {
		outcome.Err = errors.New(d.Executable + " is still not on PATH after installing")
		return outcome
	}
	outcome.Installed = true
	return outcome
}
