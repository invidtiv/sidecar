package version

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// fakeEnv is a package-manager harness: it records the exact commands the
// updater runs and answers them from a table, so no real brew/go/product
// binary is ever invoked.
type fakeEnv struct {
	calls       []string
	outputs     map[string]string
	errs        map[string]error
	paths       map[string]string
	lookPathErr map[string]bool
	self        string
}

func newFakeEnv() *fakeEnv {
	return &fakeEnv{
		outputs:     map[string]string{},
		errs:        map[string]error{},
		paths:       map[string]string{},
		lookPathErr: map[string]bool{},
		self:        "/opt/homebrew/Cellar/sidecar/0.95.0/bin/sidecar",
	}
}

func (f *fakeEnv) Run(_ context.Context, name string, args ...string) (string, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, key)
	return f.outputs[key], f.errs[key]
}

func (f *fakeEnv) lookPath(name string) (string, error) {
	if f.lookPathErr[name] {
		return "", fmt.Errorf("%s: not found", name)
	}
	if p, ok := f.paths[name]; ok {
		return p, nil
	}
	return "/opt/homebrew/bin/" + name, nil
}

func (f *fakeEnv) env() *Environment {
	return &Environment{
		Runner:       f,
		LookPath:     f.lookPath,
		EvalSymlinks: func(p string) (string, error) { return p, nil },
		Self:         func() (string, error) { return f.self, nil },
	}
}

func (f *fakeEnv) ran(cmd string) bool {
	for _, c := range f.calls {
		if c == cmd {
			return true
		}
	}
	return false
}

// brewOwns makes the fake answer `brew --cellar <formula>` with a directory
// that contains the product's resolved executable.
func (f *fakeEnv) brewOwns(formula, cellar, exe, path string) {
	f.outputs["brew --cellar "+formula] = cellar + "\n"
	f.paths[exe] = path
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"v1.5.0", "1.5.0"},
		{"1.5.0", "1.5.0"},
		{"tasks version 1.5.0", "1.5.0"},
		{"sidecar 0.95.0 (abc123)", "0.95.0"},
		{"tasks dev-main (cf216f0)", ""},
		{"devel+abc123", ""},
		{"", ""},
		{"unknown", ""},
		{"v2.0.0-rc1", "2.0.0"},
	}
	for _, tt := range tests {
		if got := NormalizeVersion(tt.in); got != tt.want {
			t.Errorf("NormalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSameVersion(t *testing.T) {
	if !SameVersion("v1.5.0", "1.5.0") {
		t.Error("v-prefix should compare equal")
	}
	if SameVersion("1.5.0", "1.5.1") {
		t.Error("different patch versions must not compare equal")
	}
	if SameVersion("dev", "1.5.0") {
		t.Error("an unparsable version must never compare equal")
	}
}

func TestSelectPlan_OrderAndGating(t *testing.T) {
	targets := []Target{
		{Product: ProductTasks, DisplayName: "Tasks", Enabled: true, Installed: true, HasUpdate: true},
		{Product: ProductSidecar, DisplayName: "Sidecar", Enabled: true, Installed: true, HasUpdate: true},
		{Product: ProductTd, DisplayName: "td", Enabled: true, Installed: true, HasUpdate: true},
	}
	plan := SelectPlan(targets)
	if len(plan) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(plan))
	}
	want := []ProductID{ProductSidecar, ProductTd, ProductTasks}
	for i, id := range want {
		if plan[i].Product != id {
			t.Errorf("plan[%d] = %s, want %s", i, plan[i].Product, id)
		}
	}
}

func TestSelectPlan_Exclusions(t *testing.T) {
	base := Target{Product: ProductTasks, Enabled: true, Installed: true, HasUpdate: true}

	cases := map[string]func(Target) Target{
		"disabled feature":  func(t Target) Target { t.Enabled = false; return t },
		"not installed":     func(t Target) Target { t.Installed = false; return t },
		"already current":   func(t Target) Target { t.HasUpdate = false; return t },
		"check did not run": func(t Target) Target { t.CheckFailed = true; return t },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if plan := SelectPlan([]Target{mutate(base)}); len(plan) != 0 {
				t.Errorf("expected empty plan, got %v", plan)
			}
		})
	}
}

func TestDetectInstallation_HomebrewOwnsExecutable(t *testing.T) {
	fake := newFakeEnv()
	fake.brewOwns("marcus/tap/tasks", "/opt/homebrew/Cellar/tasks",
		"tasks", "/opt/homebrew/Cellar/tasks/1.5.0/bin/tasks")

	inst := DetectInstallation(context.Background(), fake.env(), TasksDescriptor(), "v1.6.0")

	if inst.Method != InstallMethodHomebrew || !inst.Managed {
		t.Fatalf("expected managed Homebrew install, got %+v", inst)
	}
	if inst.ManualCommand != "brew upgrade marcus/tap/tasks" {
		t.Errorf("unexpected manual command %q", inst.ManualCommand)
	}
}

// The formula being installed is not enough: an active development selector
// resolves outside the cellar and must stay unmanaged.
func TestDetectInstallation_DevelopmentSelectorIsUnmanaged(t *testing.T) {
	fake := newFakeEnv()
	fake.outputs["brew --cellar marcus/tap/tasks"] = "/opt/homebrew/Cellar/tasks\n"
	fake.paths["tasks"] = "/Users/x/.local/state/tasks/dev-installs/main/tasks"

	inst := DetectInstallation(context.Background(), fake.env(), TasksDescriptor(), "v1.6.0")

	if inst.Managed {
		t.Fatalf("development selector must not be managed: %+v", inst)
	}
	if !strings.Contains(inst.Detail, "unmanaged") {
		t.Errorf("expected an unmanaged detail, got %q", inst.Detail)
	}
}

func TestDetectInstallation_GoBin(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	fake := newFakeEnv()
	fake.errs["brew --cellar marcus/tap/td"] = fmt.Errorf("no such keg")
	fake.errs["brew --prefix marcus/tap/td"] = fmt.Errorf("no such keg")
	fake.paths["td"] = home + "/go/bin/td"

	inst := DetectInstallation(context.Background(), fake.env(), TdDescriptor(), "v1.2.0")

	if inst.Method != InstallMethodGo || !inst.Managed {
		t.Fatalf("expected managed Go install, got %+v", inst)
	}
}

func TestInstallCommands(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		want   [][]string
	}{
		{
			name: "sidecar homebrew",
			target: Target{Product: ProductSidecar, LatestVersion: "v0.96.0",
				Install: Installation{Method: InstallMethodHomebrew, Managed: true}},
			want: [][]string{{"brew", "upgrade", "marcus/tap/sidecar"}},
		},
		{
			name: "tasks homebrew upgrades the whole suite once",
			target: Target{Product: ProductTasks, LatestVersion: "v1.6.0",
				Install: Installation{Method: InstallMethodHomebrew, Managed: true}},
			want: [][]string{{"brew", "upgrade", "marcus/tap/tasks"}},
		},
		{
			name: "tasks go install covers all three binaries",
			target: Target{Product: ProductTasks, LatestVersion: "v1.6.0",
				Install: Installation{Method: InstallMethodGo, Managed: true}},
			want: [][]string{
				{"go", "install", "github.com/marcus/tasks/cmd/tasks@v1.6.0"},
				{"go", "install", "github.com/marcus/tasks/cmd/tasks-tui@v1.6.0"},
				{"go", "install", "github.com/marcus/tasks/cmd/tasks-api@v1.6.0"},
			},
		},
		{
			name: "sidecar go install stamps the version",
			target: Target{Product: ProductSidecar, LatestVersion: "v0.96.0",
				Install: Installation{Method: InstallMethodGo, Managed: true}},
			want: [][]string{
				{"go", "install", "-ldflags", "-X main.Version=v0.96.0",
					"github.com/marcus/sidecar/cmd/sidecar@v0.96.0"},
			},
		},
		{
			name: "unmanaged target has no automated command",
			target: Target{Product: ProductTasks, LatestVersion: "v1.6.0",
				Install: Installation{Method: InstallMethodBinary}},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InstallCommands(tt.target)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("InstallCommands() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A Homebrew Tasks update is one suite upgrade, verified across all three
// binaries.
func TestApply_TasksHomebrewSuite(t *testing.T) {
	fake := newFakeEnv()
	for _, bin := range TasksDescriptor().SuiteBinaries {
		path := "/opt/homebrew/Cellar/tasks/1.6.0/bin/" + bin.Name
		fake.paths[bin.Name] = path
		fake.outputs[path+" "+strings.Join(bin.VersionArgs, " ")] = bin.Name + " 1.6.0"
	}
	fake.outputs["brew --cellar marcus/tap/tasks"] = "/opt/homebrew/Cellar/tasks\n"
	fake.outputs["brew upgrade marcus/tap/tasks"] = "==> Upgrading marcus/tap/tasks"

	target := Target{
		Product: ProductTasks, DisplayName: "Tasks", Enabled: true, Installed: true,
		CurrentVersion: "1.5.0", LatestVersion: "v1.6.0", HasUpdate: true,
		Install: Installation{
			Method: InstallMethodHomebrew, Managed: true,
			ExecutablePath: "/opt/homebrew/Cellar/tasks/1.6.0/bin/tasks",
		},
	}

	res := Apply(context.Background(), fake.env(), target)

	if res.Status != StatusUpdated {
		t.Fatalf("expected updated, got %s (%v)", res.Status, res.Err)
	}
	if !fake.ran("brew upgrade marcus/tap/tasks") {
		t.Errorf("expected one suite upgrade, ran %v", fake.calls)
	}
	upgrades := 0
	for _, c := range fake.calls {
		if strings.HasPrefix(c, "brew upgrade") {
			upgrades++
		}
	}
	if upgrades != 1 {
		t.Errorf("expected exactly one brew upgrade, got %d: %v", upgrades, fake.calls)
	}
}

// A partially updated suite is a failure, never a success.
func TestApply_PartialSuiteFails(t *testing.T) {
	fake := newFakeEnv()
	for _, bin := range TasksDescriptor().SuiteBinaries[:2] {
		path := "/opt/homebrew/Cellar/tasks/1.6.0/bin/" + bin.Name
		fake.paths[bin.Name] = path
		fake.outputs[path+" "+strings.Join(bin.VersionArgs, " ")] = bin.Name + " 1.6.0"
	}
	stale := "/usr/local/bin/tasks-api"
	fake.paths["tasks-api"] = stale
	fake.outputs[stale+" --version"] = "tasks-api 1.5.0"
	fake.outputs["brew --cellar marcus/tap/tasks"] = "/opt/homebrew/Cellar/tasks\n"
	fake.outputs["brew upgrade marcus/tap/tasks"] = "==> Upgrading"

	target := Target{
		Product: ProductTasks, DisplayName: "Tasks", LatestVersion: "v1.6.0",
		Install: Installation{
			Method: InstallMethodHomebrew, Managed: true,
			ExecutablePath: "/opt/homebrew/Cellar/tasks/1.6.0/bin/tasks",
		},
	}

	res := Apply(context.Background(), fake.env(), target)

	if res.Status != StatusFailed {
		t.Fatalf("expected a partial suite to fail, got %s", res.Status)
	}
	if !strings.Contains(res.Err.Error(), "tasks-api") {
		t.Errorf("error should name the stale binary: %v", res.Err)
	}
}

// Exit status alone never means success: a no-op brew upgrade is reported as
// a failure with an actionable command.
func TestApply_BrewNoopIsFailure(t *testing.T) {
	fake := newFakeEnv()
	fake.paths["td"] = "/opt/homebrew/Cellar/td/1.2.0/bin/td"
	fake.outputs["brew --cellar marcus/tap/td"] = "/opt/homebrew/Cellar/td\n"
	fake.outputs["brew upgrade marcus/tap/td"] = "Warning: marcus/tap/td 1.2.0 already installed"

	target := Target{
		Product: ProductTd, DisplayName: "td", LatestVersion: "v1.3.0",
		Install: Installation{
			Method: InstallMethodHomebrew, Managed: true,
			ExecutablePath: "/opt/homebrew/Cellar/td/1.2.0/bin/td",
			ManualCommand:  "brew upgrade marcus/tap/td",
		},
	}

	res := Apply(context.Background(), fake.env(), target)

	if res.Status != StatusFailed {
		t.Fatalf("expected failure, got %s", res.Status)
	}
	if !strings.Contains(res.Err.Error(), "brew update") {
		t.Errorf("expected an actionable recovery command: %v", res.Err)
	}
}

// An unmanaged target is reported as manual and is never overwritten.
func TestApply_UnmanagedTargetRunsNothing(t *testing.T) {
	fake := newFakeEnv()
	fake.paths["tasks"] = "/Users/x/.local/state/tasks/dev-installs/main/tasks"
	fake.outputs["brew --cellar marcus/tap/tasks"] = "/opt/homebrew/Cellar/tasks\n"

	target := Target{
		Product: ProductTasks, DisplayName: "Tasks", LatestVersion: "v1.6.0",
		Install: Installation{
			Method:         InstallMethodBinary,
			ExecutablePath: "/Users/x/.local/state/tasks/dev-installs/main/tasks",
			ManualCommand:  "brew install marcus/tap/tasks",
		},
	}

	res := Apply(context.Background(), fake.env(), target)

	if res.Status != StatusManual {
		t.Fatalf("expected manual, got %s", res.Status)
	}
	for _, c := range fake.calls {
		if strings.HasPrefix(c, "brew upgrade") || strings.HasPrefix(c, "go install") {
			t.Errorf("unmanaged target must not be installed: ran %q", c)
		}
	}
}

// Provenance is revalidated immediately before the command runs.
func TestApply_ProvenanceChangedSinceConfirmation(t *testing.T) {
	fake := newFakeEnv()
	fake.paths["td"] = "/Users/x/bin/td"
	fake.errs["brew --cellar marcus/tap/td"] = fmt.Errorf("no keg")
	fake.errs["brew --prefix marcus/tap/td"] = fmt.Errorf("no keg")

	target := Target{
		Product: ProductTd, DisplayName: "td", LatestVersion: "v1.3.0",
		Install: Installation{
			Method: InstallMethodHomebrew, Managed: true,
			ExecutablePath: "/opt/homebrew/Cellar/td/1.2.0/bin/td",
		},
	}

	res := Apply(context.Background(), fake.env(), target)

	if res.Status != StatusManual {
		t.Fatalf("expected manual after provenance change, got %s", res.Status)
	}
	if fake.ran("brew upgrade marcus/tap/td") {
		t.Error("must not run the confirmed command against a different install")
	}
}

func TestRefreshPackageMetadata_OncePerBatch(t *testing.T) {
	fake := newFakeEnv()
	plan := []Target{
		{Product: ProductSidecar, Install: Installation{Method: InstallMethodHomebrew, Managed: true}},
		{Product: ProductTd, Install: Installation{Method: InstallMethodHomebrew, Managed: true}},
	}

	RefreshPackageMetadata(context.Background(), fake.env(), plan)

	updates := 0
	for _, c := range fake.calls {
		if c == "brew update" {
			updates++
		}
	}
	if updates != 1 {
		t.Errorf("expected exactly one brew update, got %d", updates)
	}
}

func TestRefreshPackageMetadata_SkippedWithoutHomebrew(t *testing.T) {
	fake := newFakeEnv()
	RefreshPackageMetadata(context.Background(), fake.env(), []Target{
		{Product: ProductTd, Install: Installation{Method: InstallMethodGo, Managed: true}},
	})
	if len(fake.calls) != 0 {
		t.Errorf("expected no package-manager refresh, ran %v", fake.calls)
	}
}

func TestRestartRequired(t *testing.T) {
	tdOnly := []Result{{Target: Target{Product: ProductTd}, Status: StatusUpdated}}
	if RestartRequired(tdOnly) {
		t.Error("a td-only update must not require restarting Sidecar")
	}
	withSidecar := append(tdOnly, Result{Target: Target{Product: ProductSidecar}, Status: StatusUpdated})
	if !RestartRequired(withSidecar) {
		t.Error("a Sidecar update requires a restart")
	}
	failedSidecar := []Result{{Target: Target{Product: ProductSidecar}, Status: StatusFailed}}
	if RestartRequired(failedSidecar) {
		t.Error("a failed Sidecar update must not claim a restart is needed")
	}
}

// A later failure must not erase an earlier success, and retry selects only
// the failed target.
func TestRetryTargets(t *testing.T) {
	results := []Result{
		{Target: Target{Product: ProductSidecar}, Status: StatusUpdated},
		{Target: Target{Product: ProductTd}, Status: StatusUpdated},
		{Target: Target{Product: ProductTasks, DisplayName: "Tasks"}, Status: StatusFailed},
	}
	retry := RetryTargets(results)
	if len(retry) != 1 || retry[0].Product != ProductTasks {
		t.Fatalf("expected only Tasks to be retried, got %v", retry)
	}
}

func TestSummarize(t *testing.T) {
	got := Summarize([]Result{
		{Target: Target{DisplayName: "Sidecar"}, Status: StatusUpdated},
		{Target: Target{DisplayName: "td"}, Status: StatusUpdated},
		{Target: Target{DisplayName: "Tasks"}, Status: StatusFailed},
	})
	if got != "2 updated; Tasks failed" {
		t.Errorf("Summarize() = %q", got)
	}
}

// The released CLIs disagree about how to ask for a version: `tasks` dispatches
// on a subcommand and rejects an unknown first argument, while `tasks-tui` and
// `tasks-api` parse flags and reject positional arguments. Getting this wrong
// makes the product look like a development build and silently disables its
// update path, so the exact arguments are pinned here.
func TestDescriptorVersionArgs(t *testing.T) {
	want := map[string][]string{
		"sidecar":   {"--version"},
		"td":        {"version", "--short"},
		"tasks":     {"version"},
		"tasks-tui": {"--version"},
		"tasks-api": {"--version"},
	}
	for _, d := range []Descriptor{SidecarDescriptor(), TdDescriptor(), TasksDescriptor()} {
		for _, bin := range d.SuiteBinaries {
			got := strings.Join(bin.VersionArgs, " ")
			if exp := strings.Join(want[bin.Name], " "); got != exp {
				t.Errorf("%s version args = %q, want %q", bin.Name, got, exp)
			}
		}
		if strings.Join(d.VersionArgs(), " ") != strings.Join(d.SuiteBinaries[0].VersionArgs, " ") {
			t.Errorf("%s: VersionArgs() should be the primary binary's args", d.Product)
		}
	}
}

// Verification asks every binary in its own dialect.
func TestApply_VerifiesEachBinaryWithItsOwnArgs(t *testing.T) {
	fake := newFakeEnv()
	for _, bin := range TasksDescriptor().SuiteBinaries {
		path := "/opt/homebrew/Cellar/tasks/1.6.0/bin/" + bin.Name
		fake.paths[bin.Name] = path
		fake.outputs[path+" "+strings.Join(bin.VersionArgs, " ")] = bin.Name + " 1.6.0"
	}
	fake.outputs["brew --cellar marcus/tap/tasks"] = "/opt/homebrew/Cellar/tasks\n"
	fake.outputs["brew upgrade marcus/tap/tasks"] = "==> Upgrading"

	res := Apply(context.Background(), fake.env(), Target{
		Product: ProductTasks, DisplayName: "Tasks", LatestVersion: "v1.6.0",
		Install: Installation{Method: InstallMethodHomebrew, Managed: true,
			ExecutablePath: "/opt/homebrew/Cellar/tasks/1.6.0/bin/tasks"},
	})
	if res.Status != StatusUpdated {
		t.Fatalf("expected updated, got %s (%v)", res.Status, res.Err)
	}
	for _, want := range []string{
		"/opt/homebrew/Cellar/tasks/1.6.0/bin/tasks version",
		"/opt/homebrew/Cellar/tasks/1.6.0/bin/tasks-tui --version",
		"/opt/homebrew/Cellar/tasks/1.6.0/bin/tasks-api --version",
	} {
		if !fake.ran(want) {
			t.Errorf("expected %q to be run; ran %v", want, fake.calls)
		}
	}
}

// An installed copy Sidecar does not manage must never be told to `brew
// install`: that creates a second, shadowing installation.
func TestUnmanagedHintDoesNotInstallASecondCopy(t *testing.T) {
	fake := newFakeEnv()
	fake.paths["sidecar"] = "/Users/x/.local/bin/sidecar"
	fake.self = "/Users/x/.local/bin/sidecar"
	fake.errs["brew --cellar marcus/tap/sidecar"] = fmt.Errorf("no keg")
	fake.errs["brew --prefix marcus/tap/sidecar"] = fmt.Errorf("no keg")

	inst := DetectInstallation(context.Background(), fake.env(), SidecarDescriptor(), "v0.96.0")

	if inst.Managed {
		t.Fatal("a downloaded binary must not be managed")
	}
	if strings.Contains(inst.ManualCommand, "brew install") {
		t.Errorf("unmanaged install must not be told to brew install: %q", inst.ManualCommand)
	}
	if !strings.Contains(inst.ManualCommand, "releases") {
		t.Errorf("unmanaged install should point at releases: %q", inst.ManualCommand)
	}
}

// Provenance that changed since confirmation is refused outright, even when
// the new location would also be updatable.
func TestApply_RefusesDifferentManagedProvenance(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	fake := newFakeEnv()
	fake.paths["td"] = home + "/go/bin/td"
	fake.errs["brew --cellar marcus/tap/td"] = fmt.Errorf("no keg")
	fake.errs["brew --prefix marcus/tap/td"] = fmt.Errorf("no keg")

	res := Apply(context.Background(), fake.env(), Target{
		Product: ProductTd, DisplayName: "td", LatestVersion: "v1.3.0",
		Install: Installation{Method: InstallMethodHomebrew, Managed: true,
			ExecutablePath: "/opt/homebrew/Cellar/td/1.2.0/bin/td"},
	})

	if res.Status != StatusManual {
		t.Fatalf("expected refusal, got %s", res.Status)
	}
	for _, c := range fake.calls {
		if strings.HasPrefix(c, "go install") || strings.HasPrefix(c, "brew upgrade") {
			t.Errorf("must not run an unconfirmed command: %q", c)
		}
	}
}
