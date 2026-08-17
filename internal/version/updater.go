package version

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Installation records how one product's resolved executable is managed.
type Installation struct {
	Method         InstallMethod
	ExecutablePath string
	// Managed is true only when Sidecar can safely run an automated update for
	// this exact executable. An unmanaged target is reported with a manual
	// command instead of being overwritten.
	Managed       bool
	ManualCommand string
	// Detail explains an unmanaged target (e.g. a local development selector).
	Detail string
}

// Target is one product Sidecar may offer to update.
type Target struct {
	Product        ProductID
	DisplayName    string
	Installed      bool
	Enabled        bool
	CurrentVersion string
	LatestVersion  string
	HasUpdate      bool
	// CheckFailed marks a release check that could not complete. It is
	// "unknown", never "up to date".
	CheckFailed bool
	Install     Installation
}

// ResultStatus is the settled outcome of one target in a confirmed batch.
type ResultStatus string

const (
	StatusUpdated ResultStatus = "updated"
	StatusManual  ResultStatus = "manual"
	StatusFailed  ResultStatus = "failed"
)

// Result is the immutable outcome of attempting one target.
type Result struct {
	Target Target
	Status ResultStatus
	// Version is the version actually verified after a successful update.
	Version string
	Output  string
	Err     error
}

// Runner executes an external command and returns its combined output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// ExecRunner is the real process runner.
type ExecRunner struct{}

// Run executes name with args and returns combined output. `go` invocations get
// the adjusted environment from GoCommandEnv so an automated update is not
// broken by an active workspace or by SDK header warnings.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if filepath.Base(name) == "go" {
		cmd.Env = GoCommandEnv(os.Environ())
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Environment is the adapter edge for everything the updater needs from the
// host: process execution and executable resolution. Tests substitute it to
// assert exact commands without invoking real package managers.
type Environment struct {
	Runner       Runner
	LookPath     func(string) (string, error)
	EvalSymlinks func(string) (string, error)
	// Self resolves the running Sidecar binary.
	Self func() (string, error)
}

// DefaultEnvironment returns the real host environment.
func DefaultEnvironment() *Environment {
	return &Environment{
		Runner:       ExecRunner{},
		LookPath:     exec.LookPath,
		EvalSymlinks: filepath.EvalSymlinks,
		Self:         os.Executable,
	}
}

// ResolveExecutable returns the resolved on-disk path of a product's primary
// executable, following symlinks.
func (e *Environment) ResolveExecutable(d Descriptor) (string, error) {
	var path string
	var err error
	if d.Product == ProductSidecar {
		path, err = e.Self()
	} else {
		path, err = e.LookPath(d.Executable)
	}
	if err != nil {
		return "", err
	}
	if resolved, rerr := e.EvalSymlinks(path); rerr == nil {
		return resolved, nil
	}
	return path, nil
}

// DetectInstallation resolves how the product the user actually runs was
// installed. Provenance is per product: a Homebrew Sidecar may coexist with a
// Go-installed td or a local Tasks development selector.
func DetectInstallation(ctx context.Context, env *Environment, d Descriptor, latestVersion string) Installation {
	inst := Installation{Method: InstallMethodBinary, ManualCommand: d.InstallHint()}

	path, err := env.ResolveExecutable(d)
	if err != nil {
		inst.Detail = "not installed"
		return inst
	}
	// Installed but unmanaged: never tell the user to install a second copy
	// through a package manager that would shadow the one they run.
	inst.ManualCommand = d.UnmanagedHint()
	inst.ExecutablePath = path

	if d.Formula != "" && ownedByHomebrew(ctx, env, d.Formula, path) {
		inst.Method = InstallMethodHomebrew
		inst.Managed = true
		inst.ManualCommand = "brew upgrade " + d.Formula
		return inst
	}

	if isGoBinDir(filepath.Dir(path)) {
		inst.Method = InstallMethodGo
		if len(d.GoPackages) > 0 {
			inst.Managed = true
			inst.ManualCommand = strings.Join(goInstallCommandStrings(d, latestVersion), " && ")
			return inst
		}
		// No safe package set to install; never update part of a suite.
		inst.Managed = false
		inst.Detail = "no automated Go install for this product"
		return inst
	}

	inst.Detail = "unmanaged install at " + path
	return inst
}

// ownedByHomebrew reports whether the resolved executable actually belongs to
// the formula, rather than merely whether the formula is installed. This is
// what keeps an active local development selector from being overwritten or
// falsely verified.
func ownedByHomebrew(ctx context.Context, env *Environment, formula, resolvedPath string) bool {
	if _, err := env.LookPath("brew"); err != nil {
		return false
	}
	for _, sub := range []string{"--cellar", "--prefix"} {
		out, err := env.Runner.Run(ctx, "brew", sub, formula)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			dir := strings.TrimSpace(line)
			if dir == "" || !filepath.IsAbs(dir) {
				continue
			}
			// Compare resolved against resolved: the executable path has already
			// been through EvalSymlinks, and a prefix that itself traverses a
			// symlink (/tmp on macOS, or a symlinked Homebrew prefix) would
			// otherwise never match its own cellar.
			candidates := []string{dir}
			if resolvedDir, err := env.EvalSymlinks(dir); err == nil && resolvedDir != dir {
				candidates = append(candidates, resolvedDir)
			}
			for _, c := range candidates {
				if resolvedPath == c || strings.HasPrefix(resolvedPath, c+string(filepath.Separator)) {
					return true
				}
			}
		}
	}
	return false
}

// isGoBinDir reports whether dir is the active Go install location.
func isGoBinDir(dir string) bool {
	if gobin := os.Getenv("GOBIN"); gobin != "" && dir == gobin {
		return true
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" && dir == filepath.Join(gopath, "bin") {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && dir == filepath.Join(home, "go", "bin") {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasSuffix(dir, sep+"go"+sep+"bin")
}

// productOrder is the deterministic execution order for a confirmed batch.
var productOrder = []ProductID{ProductSidecar, ProductTd, ProductTasks}

// SelectPlan returns, in deterministic order, the targets a confirmed update
// will act on. It is a state-free function over discovered products: only
// enabled, installed products with a real available update take part.
func SelectPlan(targets []Target) []Target {
	byID := make(map[ProductID]Target, len(targets))
	for _, t := range targets {
		byID[t.Product] = t
	}
	var plan []Target
	for _, id := range productOrder {
		t, ok := byID[id]
		if !ok {
			continue
		}
		if !t.Enabled || !t.Installed || !t.HasUpdate || t.CheckFailed {
			continue
		}
		plan = append(plan, t)
	}
	return plan
}

// goInstallCommandStrings renders the go install commands as display strings.
func goInstallCommandStrings(d Descriptor, version string) []string {
	var out []string
	for _, cmd := range goInstallCommands(d, version) {
		// Show the same environment the automated path uses, so a copied command
		// behaves identically to the in-app update.
		out = append(out, "GOWORK=off "+strings.Join(cmd, " "))
	}
	return out
}

func goInstallCommands(d Descriptor, version string) [][]string {
	var cmds [][]string
	for _, pkg := range d.GoPackages {
		args := []string{"go", "install"}
		if d.GoLdflags {
			args = append(args, "-ldflags", fmt.Sprintf("-X main.Version=%s", version))
		}
		args = append(args, fmt.Sprintf("%s@%s", pkg, version))
		cmds = append(cmds, args)
	}
	return cmds
}

// InstallCommands returns the exact commands an automated update runs for a
// target, or nil when the target is not safely updatable.
func InstallCommands(t Target) [][]string {
	d, ok := DescriptorFor(t.Product)
	if !ok || !t.Install.Managed {
		return nil
	}
	switch t.Install.Method {
	case InstallMethodHomebrew:
		return [][]string{{"brew", "upgrade", d.Formula}}
	case InstallMethodGo:
		return goInstallCommands(d, t.LatestVersion)
	}
	return nil
}

// RefreshPackageMetadata refreshes Homebrew metadata once per confirmed batch
// so `brew upgrade` can see new releases. Best effort: a failure here is not a
// target failure.
func RefreshPackageMetadata(ctx context.Context, env *Environment, plan []Target) {
	for _, t := range plan {
		if t.Install.Managed && t.Install.Method == InstallMethodHomebrew {
			_, _ = env.Runner.Run(ctx, "brew", "update")
			return
		}
	}
}

// Apply installs and verifies one target. It never infers success from exit
// status alone: every binary the release ships must report the exact version
// that was confirmed.
func Apply(ctx context.Context, env *Environment, t Target) Result {
	d, ok := DescriptorFor(t.Product)
	if !ok {
		return Result{Target: t, Status: StatusFailed, Err: fmt.Errorf("unknown product %q", t.Product)}
	}

	// Revalidate provenance immediately before touching anything. If the target
	// no longer resolves to exactly what the user confirmed, refuse: running a
	// different command against a different executable is never what they
	// agreed to, even when that other install would also be updatable.
	current := DetectInstallation(ctx, env, d, t.LatestVersion)
	if current.Method != t.Install.Method || current.Managed != t.Install.Managed ||
		current.ExecutablePath != t.Install.ExecutablePath {
		t.Install = current
		return Result{
			Target: t, Status: StatusManual,
			Err: fmt.Errorf("%s now resolves to a different install than the one confirmed; nothing was changed",
				d.DisplayName),
		}
	}

	if !t.Install.Managed {
		return Result{Target: t, Status: StatusManual}
	}

	cmds := InstallCommands(t)
	if len(cmds) == 0 {
		return Result{Target: t, Status: StatusManual}
	}

	var output strings.Builder
	for _, cmd := range cmds {
		out, err := env.Runner.Run(ctx, cmd[0], cmd[1:]...)
		output.WriteString(out)
		if err != nil {
			return Result{
				Target: t, Status: StatusFailed, Output: output.String(),
				Err: fmt.Errorf("%s: %v", strings.Join(cmd, " "), err),
			}
		}
		if t.Install.Method == InstallMethodHomebrew && brewReportsNoop(out) {
			return Result{
				Target: t, Status: StatusFailed, Output: output.String(),
				Err: fmt.Errorf("brew reports %s is already at the latest version — the tap may be out of date. Try: brew update && %s",
					d.DisplayName, t.Install.ManualCommand),
			}
		}
	}

	if err := verifySuite(ctx, env, d, t.LatestVersion, t.Install.ManualCommand); err != nil {
		return Result{Target: t, Status: StatusFailed, Output: output.String(), Err: err}
	}

	return Result{Target: t, Status: StatusUpdated, Version: t.LatestVersion, Output: output.String()}
}

// FailureDetail returns the diagnostic lines to show for a failed result: the
// error itself, followed by the tail of the failing command's combined output.
//
// The output is the part that matters and it used to be discarded entirely, so
// a failed update reported only the command and "exit status 1" — enough to see
// that `go install` failed, never enough to see why. The tail is what carries
// the compiler or toolchain message; earlier lines are download progress.
//
// State-free so the exact reported lines are testable without a UI.
func FailureDetail(r Result, maxOutputLines int) []string {
	var lines []string
	if r.Err != nil {
		lines = append(lines, r.Err.Error())
	}
	if maxOutputLines <= 0 {
		return lines
	}
	var out []string
	for _, line := range strings.Split(r.Output, "\n") {
		if trimmed := strings.TrimRight(line, " \t\r"); strings.TrimSpace(trimmed) != "" {
			out = append(out, strings.TrimSpace(trimmed))
		}
	}
	if len(out) > maxOutputLines {
		out = out[len(out)-maxOutputLines:]
	}
	return append(lines, out...)
}

func brewReportsNoop(out string) bool {
	lower := strings.ToLower(out)
	return strings.Contains(lower, "already installed") || strings.Contains(lower, "already up-to-date")
}

// verifySuite checks that every binary in the release resolves and reports the
// expected version. A partially updated suite is a failure, not a success.
func verifySuite(ctx context.Context, env *Environment, d Descriptor, want, manualCommand string) error {
	for _, bin := range d.SuiteBinaries {
		path, err := env.LookPath(bin.Name)
		if err != nil {
			return fmt.Errorf("%s not found in PATH after update", bin.Name)
		}
		out, err := env.Runner.Run(ctx, path, bin.VersionArgs...)
		if err != nil {
			return fmt.Errorf("%s is not executable after update: %v", bin.Name, err)
		}
		got := NormalizeVersion(out)
		if !SameVersion(got, want) {
			if got == "" {
				got = strings.TrimSpace(out)
			}
			// The recovery command is this install's own update command, never
			// `brew install`: that would add a second, shadowing copy.
			recovery := manualCommand
			if recovery == "" {
				recovery = d.UnmanagedHint()
			}
			return fmt.Errorf("%s reports %s after update, expected %s — update it manually with: %s",
				bin.Name, got, NormalizeVersion(want), recovery)
		}
	}
	return nil
}

// InstalledVersion reads a product's installed version by running its
// executable. It returns "" when the product is not installed, the command
// fails, or the output carries no release version (a development build).
func InstalledVersion(ctx context.Context, env *Environment, d Descriptor) string {
	path, err := env.LookPath(d.Executable)
	if err != nil {
		return ""
	}
	out, err := env.Runner.Run(ctx, path, d.VersionArgs()...)
	if err != nil {
		return ""
	}
	return NormalizeVersion(out)
}

// RestartRequired reports whether any settled result changed Sidecar itself.
// Standalone td/Tasks updates do not require quitting Sidecar.
func RestartRequired(results []Result) bool {
	for _, r := range results {
		if r.Target.Product == ProductSidecar && r.Status == StatusUpdated {
			return true
		}
	}
	return false
}

// RetryTargets returns the targets from a settled batch that are worth
// retrying: failures only. Successful upgrades are never re-run.
func RetryTargets(results []Result) []Target {
	var out []Target
	for _, r := range results {
		if r.Status == StatusFailed {
			out = append(out, r.Target)
		}
	}
	return out
}

// Summarize renders a one-line summary of a settled batch for a toast.
func Summarize(results []Result) string {
	var updated, failed, manual []string
	for _, r := range results {
		switch r.Status {
		case StatusUpdated:
			updated = append(updated, r.Target.DisplayName)
		case StatusFailed:
			failed = append(failed, r.Target.DisplayName)
		case StatusManual:
			manual = append(manual, r.Target.DisplayName)
		}
	}
	var parts []string
	if len(updated) > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", len(updated)))
	}
	if len(failed) > 0 {
		parts = append(parts, strings.Join(failed, ", ")+" failed")
	}
	if len(manual) > 0 {
		parts = append(parts, strings.Join(manual, ", ")+" needs a manual update")
	}
	if len(parts) == 0 {
		return "Nothing to update"
	}
	return strings.Join(parts, "; ")
}
