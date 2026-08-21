package version

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanInstallPrefersHomebrew(t *testing.T) {
	fake := newFakeEnv()
	plan, err := PlanInstall(fake.env(), TdDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Method != InstallMethodHomebrew {
		t.Fatalf("method = %s, want homebrew", plan.Method)
	}
	if plan.Command != "brew install marcus/tap/td" {
		t.Fatalf("command = %q", plan.Command)
	}
}

func TestPlanInstallFallsBackToGoWhenBrewMissing(t *testing.T) {
	fake := newFakeEnv()
	fake.lookPathErr["brew"] = true
	plan, err := PlanInstall(fake.env(), TdDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Method != InstallMethodGo {
		t.Fatalf("method = %s, want go", plan.Method)
	}
	if plan.Command != "GOWORK=off go install github.com/marcus/td@latest" {
		t.Fatalf("command = %q", plan.Command)
	}
}

func TestPlanInstallErrorsWhenNeitherInstallerExists(t *testing.T) {
	fake := newFakeEnv()
	fake.lookPathErr["brew"] = true
	fake.lookPathErr["go"] = true
	_, err := PlanInstall(fake.env(), TdDescriptor())
	if !errors.Is(err, ErrNoInstaller) {
		t.Fatalf("err = %v, want ErrNoInstaller", err)
	}
}

func TestInstallRunsThePlannedHomebrewCommandAndReprobes(t *testing.T) {
	fake := newFakeEnv()
	fake.lookPathErr["td"] = true
	fake.onRun = func(key string) {
		if key == "brew install marcus/tap/td" {
			fake.lookPathErr["td"] = false
		}
	}
	env := fake.env()
	plan, err := PlanInstall(env, TdDescriptor())
	if err != nil {
		t.Fatal(err)
	}

	outcome := Install(context.Background(), env, TdDescriptor())
	if !outcome.Installed {
		t.Fatalf("not installed: %+v", outcome)
	}
	if outcome.Command != plan.Command {
		t.Fatalf("ran %q, planned %q", outcome.Command, plan.Command)
	}
	if !fake.ran("brew install marcus/tap/td") {
		t.Fatalf("ran %v", fake.calls)
	}
}

func TestInstallGoFallbackRunsDisplayedCommand(t *testing.T) {
	fake := newFakeEnv()
	fake.lookPathErr["brew"] = true
	fake.lookPathErr["td"] = true
	fake.onRun = func(key string) {
		if strings.Contains(key, "go install") {
			fake.lookPathErr["td"] = false
		}
	}
	env := fake.env()
	plan, err := PlanInstall(env, TdDescriptor())
	if err != nil {
		t.Fatal(err)
	}

	outcome := Install(context.Background(), env, TdDescriptor())
	if !outcome.Installed {
		t.Fatalf("not installed: %+v", outcome)
	}
	if outcome.Command != plan.Command {
		t.Fatalf("ran %q, planned %q", outcome.Command, plan.Command)
	}
	if !fake.ran("go install github.com/marcus/td@latest") {
		t.Fatalf("ran %v", fake.calls)
	}
	if !strings.Contains(outcome.Command, "GOWORK=off") {
		t.Fatalf("displayed command omitted GOWORK=off: %q", outcome.Command)
	}
}

func TestInstallGoFindsBinaryInGOBINNotOnPATH(t *testing.T) {
	gobin := t.TempDir()
	t.Setenv("GOBIN", gobin)
	origPATH := os.Getenv("PATH")
	t.Setenv("PATH", "/usr/bin")

	fake := newFakeEnv()
	fake.lookPathErr["brew"] = true
	fake.lookPathErr["td"] = true
	fake.onRun = func(key string) {
		if strings.Contains(key, "go install") {
			p := filepath.Join(gobin, "td")
			if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Errorf("write gobin td: %v", err)
			}
		}
	}
	env := fake.env()
	outcome := Install(context.Background(), env, TdDescriptor())
	if !outcome.Installed {
		t.Fatalf("go install into GOBIN was not detected: %+v", outcome)
	}
	if _, err := env.LookPath("td"); err != nil {
		t.Fatalf("LookPath after install did not find GOBIN td: %v", err)
	}
	if got := os.Getenv("PATH"); !strings.HasPrefix(got, gobin) {
		t.Fatalf("process PATH was not prepended with GOBIN: %q", got)
	}
	if origPATH == os.Getenv("PATH") {
		t.Fatal("PATH was not updated for this process")
	}
}

func TestInstallDoesNotClaimSuccessFromExitCode(t *testing.T) {
	fake := newFakeEnv()
	fake.lookPathErr["td"] = true
	outcome := Install(context.Background(), fake.env(), TdDescriptor())
	if outcome.Installed {
		t.Fatal("claimed success with td still missing")
	}
	if outcome.Err == nil {
		t.Fatal("missing command after install was not reported")
	}
}

func TestInstallFailureLeavesCommandRecorded(t *testing.T) {
	fake := newFakeEnv()
	fake.errs["brew install marcus/tap/td"] = errors.New("formula not found")
	outcome := Install(context.Background(), fake.env(), TdDescriptor())
	if outcome.Installed {
		t.Fatal("failed install claimed success")
	}
	if outcome.Command != "brew install marcus/tap/td" {
		t.Fatalf("command = %q", outcome.Command)
	}
	if outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "formula not found") {
		t.Fatalf("err = %v", outcome.Err)
	}
}

func TestInstallTasksGoFallbackRunsEveryPackage(t *testing.T) {
	fake := newFakeEnv()
	fake.lookPathErr["brew"] = true
	fake.lookPathErr["tasks"] = true
	fake.onRun = func(key string) {
		if strings.Contains(key, "go install") {
			fake.lookPathErr["tasks"] = false
		}
	}
	env := fake.env()
	plan, err := PlanInstall(env, TasksDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	outcome := Install(context.Background(), env, TasksDescriptor())
	if !outcome.Installed {
		t.Fatalf("not installed: %+v", outcome)
	}
	if outcome.Command != plan.Command {
		t.Fatalf("ran %q, planned %q", outcome.Command, plan.Command)
	}
	for _, pkg := range TasksDescriptor().GoPackages {
		want := "go install " + pkg + "@latest"
		if !fake.ran(want) {
			t.Fatalf("missing %s in %v", want, fake.calls)
		}
	}
}
