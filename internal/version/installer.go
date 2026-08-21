package version

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
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
//
// Homebrew is preferred when `brew` is on PATH; otherwise a `go install` of
// the product's GoPackages at @latest. Tmux repair deliberately never takes
// this path — that is a different product.

// InstallTimeout bounds one confirmed first-install. A package manager that
// never returns must not leave the UI claiming to be working.
const InstallTimeout = 15 * time.Minute

// ErrNoFormula reports a product with no Homebrew formula to install from.
var ErrNoFormula = errors.New("no Homebrew formula for this product")

// ErrNoHomebrew reports that Homebrew is not available on this machine.
var ErrNoHomebrew = errors.New("homebrew is not installed")

// ErrNoGo reports that the Go toolchain is not available on this machine.
var ErrNoGo = errors.New("go is not installed")

// ErrNoInstaller reports that neither Homebrew nor Go can run a first install.
var ErrNoInstaller = errors.New("no supported installer is available")

// ErrNoGoPackages reports a product with no safe go-install package set.
var ErrNoGoPackages = errors.New("no automated Go install for this product")

// InstallOutcome is the settled result of one confirmed install attempt.
type InstallOutcome struct {
	// Installed is true only when the product's command resolves afterwards.
	Installed bool
	// Command is exactly what was run, for a message the user can act on.
	Command string
	Output  string
	Err     error
}

// InstallPlan is the exact first-install Sidecar would run for a product on
// this machine. Confirmation, the copyable instruction, and execution share
// Command so the user has read the same string that will run.
type InstallPlan struct {
	Method  InstallMethod
	Command string
	Steps   [][]string
}

// HomebrewAvailable reports whether `brew` is on PATH.
func HomebrewAvailable(env *Environment) bool {
	return commandOnPath(env, "brew")
}

// GoAvailable reports whether `go` is on PATH.
func GoAvailable(env *Environment) bool {
	return commandOnPath(env, "go")
}

func commandOnPath(env *Environment, name string) bool {
	if env == nil || env.LookPath == nil {
		return false
	}
	_, err := env.LookPath(name)
	return err == nil
}

// CommandAvailable reports whether a product's primary executable is on PATH.
func CommandAvailable(env *Environment, d Descriptor) bool {
	return commandOnPath(env, d.Executable)
}

// InstallCommand is the exact Homebrew command a brew-backed install would run,
// so the confirmation, the copyable instruction, and the execution are the same
// string when Homebrew is the chosen method.
func InstallCommand(d Descriptor) string { return d.InstallHint() }

// GoInstallCommand is the exact `go install` Sidecar would run, joined so it
// can be shown and copied as one instruction. GOWORK=off is part of the
// command: ExecRunner applies it, and a pasted copy must behave the same way.
func GoInstallCommand(d Descriptor) string {
	return strings.Join(goInstallCommandStrings(d, "latest"), " && ")
}

// PlanInstall chooses Homebrew when brew is on PATH, otherwise go install.
// The returned Command is what Install will run; a caller that shows a
// different string is offering a command it will not honour.
func PlanInstall(env *Environment, d Descriptor) (InstallPlan, error) {
	if env == nil {
		env = DefaultEnvironment()
	}
	if HomebrewAvailable(env) {
		if d.Formula == "" {
			return InstallPlan{}, ErrNoFormula
		}
		return InstallPlan{
			Method:  InstallMethodHomebrew,
			Command: InstallCommand(d),
			Steps:   [][]string{{"brew", "install", d.Formula}},
		}, nil
	}
	if GoAvailable(env) {
		if len(d.GoPackages) == 0 {
			return InstallPlan{}, ErrNoGoPackages
		}
		return InstallPlan{
			Method:  InstallMethodGo,
			Command: GoInstallCommand(d),
			Steps:   goInstallCommands(d, "latest"),
		}, nil
	}
	return InstallPlan{}, ErrNoInstaller
}

// Install runs PlanInstall and verifies the product's command is resolvable
// afterwards. It is never called without an explicit user confirmation.
func Install(ctx context.Context, env *Environment, d Descriptor) InstallOutcome {
	if env == nil {
		env = DefaultEnvironment()
	}
	plan, err := PlanInstall(env, d)
	if err != nil {
		return InstallOutcome{Command: fallbackCommand(d, err), Err: err}
	}
	return runInstall(ctx, env, d, plan)
}

func fallbackCommand(d Descriptor, err error) string {
	if err == ErrNoHomebrew || err == ErrNoFormula {
		return InstallCommand(d)
	}
	if err == ErrNoGoPackages || err == ErrNoGo {
		return GoInstallCommand(d)
	}
	if cmd := InstallCommand(d); cmd != "" {
		return cmd
	}
	return GoInstallCommand(d)
}

// InstallWithHomebrew installs a product's formula. It is never called without
// an explicit user confirmation, and it verifies the result rather than
// trusting the package manager's exit code. Callers that should fall back to
// go install use Install instead.
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
	return runInstall(ctx, env, d, InstallPlan{
		Method:  InstallMethodHomebrew,
		Command: InstallCommand(d),
		Steps:   [][]string{{"brew", "install", d.Formula}},
	})
}

func runInstall(ctx context.Context, env *Environment, d Descriptor, plan InstallPlan) InstallOutcome {
	outcome := InstallOutcome{Command: plan.Command}
	if env.Runner == nil {
		outcome.Err = errors.New("no command runner")
		return outcome
	}
	var output strings.Builder
	for _, step := range plan.Steps {
		if len(step) == 0 {
			continue
		}
		out, err := env.Runner.Run(ctx, step[0], step[1:]...)
		if output.Len() > 0 && out != "" {
			output.WriteByte('\n')
		}
		output.WriteString(out)
		if err != nil {
			outcome.Output = strings.TrimSpace(output.String())
			outcome.Err = err
			return outcome
		}
	}
	outcome.Output = strings.TrimSpace(output.String())
	// A clean exit is not the claim; a resolvable command is.
	if CommandAvailable(env, d) {
		outcome.Installed = true
		return outcome
	}
	// `go install` writes GOBIN (or GOPATH/bin, or ~/go/bin). That directory
	// is often missing from this process's PATH even though the install
	// succeeded. Search it, then put it on this process's PATH so later
	// LookPath/re-probe/setup see the new binary. The user's shell rc is not
	// touched.
	if plan.Method == InstallMethodGo {
		if path := goBinExecutable(d.Executable); path != "" {
			rememberInstalledCommand(env, d.Executable, path)
			outcome.Installed = true
			return outcome
		}
	}
	outcome.Err = errors.New(d.Executable + " is still not on PATH after installing")
	return outcome
}

// goInstallBinDir is the directory `go install` writes to for this process:
// GOBIN if set, otherwise the first GOPATH/bin, otherwise ~/go/bin.
func goInstallBinDir() string {
	if v := strings.TrimSpace(os.Getenv("GOBIN")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("GOPATH")); v != "" {
		if i := strings.IndexByte(v, os.PathListSeparator); i >= 0 {
			v = v[:i]
		}
		if v != "" {
			return filepath.Join(v, "bin")
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "go", "bin")
	}
	return ""
}

func goBinExecutable(name string) string {
	dir := goInstallBinDir()
	if dir == "" || name == "" {
		return ""
	}
	p := filepath.Join(dir, name)
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return ""
	}
	return p
}

func rememberInstalledCommand(env *Environment, name, path string) {
	if env == nil || path == "" {
		return
	}
	prependProcessPATH(filepath.Dir(path))
	orig := env.LookPath
	env.LookPath = func(n string) (string, error) {
		if orig != nil {
			if p, err := orig(n); err == nil {
				return p, nil
			}
		}
		if n == name {
			return path, nil
		}
		return "", errors.New(n + ": not found")
	}
}

func prependProcessPATH(dir string) {
	if dir == "" {
		return
	}
	path := os.Getenv("PATH")
	sep := string(os.PathListSeparator)
	for _, p := range filepath.SplitList(path) {
		if p == dir {
			return
		}
	}
	_ = os.Setenv("PATH", dir+sep+path)
}
