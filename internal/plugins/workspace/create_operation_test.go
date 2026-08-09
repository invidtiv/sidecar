package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/projectdir"
)

type createRepo struct{ root, main, linked string }

func newCreateRepo(t *testing.T) createRepo {
	t.Helper()
	root := t.TempDir()
	main := filepath.Join(root, "repo")
	mustGit(t, root, "init", "-b", "main", main)
	mustGit(t, main, "config", "user.email", "test@example.com")
	mustGit(t, main, "config", "user.name", "Test")
	mustWrite(t, filepath.Join(main, "README.md"), "start\n")
	mustGit(t, main, "add", "README.md")
	mustGit(t, main, "commit", "-m", "initial")
	linked := filepath.Join(root, "staging")
	mustGit(t, main, "worktree", "add", "-b", "staging", linked)
	return createRepo{root: root, main: main, linked: linked}
}

func TestResolveAndCreateFromEveryRepositoryEntryPoint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source func(createRepo) string
	}{
		{"main", func(r createRepo) string { return r.main }},
		{"linked", func(r createRepo) string { return r.linked }},
		{"subdirectory", func(r createRepo) string {
			d := filepath.Join(r.main, "nested", "dir")
			if err := os.MkdirAll(d, 0755); err != nil {
				t.Fatal(err)
			}
			return d
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newCreateRepo(t)
			branch := "feature/" + tc.name
			plan, err := resolveCreateOperation(context.Background(), tc.source(r), r.main, branch, "main", false, config.WorktreeSetupConfig{})
			if err != nil {
				t.Fatal(err)
			}
			if plan.MainWorktree != r.main || plan.SourceRef != "refs/heads/main" || plan.SourceOID == "" {
				t.Fatalf("resolved plan = %+v", plan)
			}
			if want := filepath.Join(r.root, "feature", tc.name); plan.Path != want {
				t.Fatalf("path = %q, want %q", plan.Path, want)
			}
			wt, err := addCreatedWorktree(context.Background(), "repo", plan)
			if err != nil {
				t.Fatal(err)
			}
			if wt.Branch != branch || mustGit(t, wt.Path, "rev-parse", "HEAD") != plan.SourceOID {
				t.Fatalf("created identity = %+v", wt)
			}
		})
	}
}

func TestResolveCreateRejectsExistingRefAndPath(t *testing.T) {
	r := newCreateRepo(t)
	if _, err := resolveCreateOperation(context.Background(), r.main, r.main, "staging", "main", false, config.WorktreeSetupConfig{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing ref error = %v", err)
	}
	pathName := "occupied/path"
	if err := os.MkdirAll(filepath.Join(r.root, pathName), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveCreateOperation(context.Background(), r.main, r.main, pathName, "main", false, config.WorktreeSetupConfig{}); err == nil || !strings.Contains(err.Error(), "destination path") {
		t.Fatalf("existing path error = %v", err)
	}
}

func TestResolveCreateRejectsSetupPathsOutsideMainWorktree(t *testing.T) {
	r := newCreateRepo(t)
	for _, setup := range []config.WorktreeSetupConfig{
		{CopyEnvFiles: true, EnvFiles: []string{"../secret"}},
		{RunHook: true, HookPath: "../../setup.sh"},
	} {
		if _, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/safe", "main", false, setup); err == nil || !strings.Contains(err.Error(), "must stay within") {
			t.Fatalf("unsafe setup path error = %v", err)
		}
	}
}

func TestCreateSetupUsesMainArtifactsAndAccurateEnvironment(t *testing.T) {
	r := newCreateRepo(t)
	mustWrite(t, filepath.Join(r.main, ".env"), "SECRET_CONTENT=not-logged\n")
	hook := "#!/bin/sh\nprintf '%s\n%s\n%s\n%s\n' \"$MAIN_WORKTREE\" \"$SOURCE_WORKTREE\" \"$WORKTREE_PATH\" \"$WORKTREE_BRANCH\" > \"$WORKTREE_PATH/setup-vars\"\n"
	mustWrite(t, filepath.Join(r.main, setupScriptName), hook)
	plan, err := resolveCreateOperation(context.Background(), r.linked, r.main, "feature/env", "main", false, config.WorktreeSetupConfig{CopyEnvFiles: true, EnvFiles: []string{".env"}, RunHook: true, HookPath: setupScriptName, HookRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := addCreatedWorktree(context.Background(), "repo", plan)
	if err != nil {
		t.Fatal(err)
	}
	result := runCreateSetup(context.Background(), plan, wt)
	if warnings := result.Warnings(); len(warnings) != 0 {
		t.Fatalf("setup warnings = %+v", warnings)
	}
	if got := mustRead(t, filepath.Join(wt.Path, ".env")); got != "SECRET_CONTENT=not-logged\n" {
		t.Fatalf("copied env = %q", got)
	}
	got := strings.Split(strings.TrimSpace(mustRead(t, filepath.Join(wt.Path, "setup-vars"))), "\n")
	want := []string{plan.MainWorktree, plan.SourceWorktree, plan.Path, "feature/env"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("hook environment = %q, want %q", got, want)
	}
}

func TestCreateSetupReportsMissingTDAndKeepsTaskLink(t *testing.T) {
	r := newCreateRepo(t)
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/task", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	plan.TaskID = "td-missing"
	wt, err := addCreatedWorktree(context.Background(), "repo", plan)
	if err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	result := runCreateSetup(context.Background(), plan, wt)
	var tdWarning bool
	for _, warning := range result.Warnings() {
		if warning.Kind == CreateOutcomeTDStart {
			tdWarning = true
		}
	}
	if !tdWarning {
		t.Fatalf("missing td not reported: %+v", result.Outcomes)
	}
	if got := loadTaskLink(r.main, wt.Path); got != plan.TaskID {
		t.Fatalf("durable task link = %q", got)
	}
}

func TestCreateSetupSeparatesStateAndRequiredHookFailures(t *testing.T) {
	r := newCreateRepo(t)
	mustWrite(t, filepath.Join(r.main, setupScriptName), "#!/bin/sh\nexit 7\n")
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/fail", "main", false, config.WorktreeSetupConfig{RunHook: true, HookPath: setupScriptName, HookRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := addCreatedWorktree(context.Background(), "repo", plan)
	if err != nil {
		t.Fatal(err)
	}
	stateDir, err := projectdir.WorktreeDir(r.main, wt.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(stateDir, "meta.json")); err != nil {
		t.Fatal(err)
	}
	result := runCreateSetup(context.Background(), plan, wt)
	if !result.HasRequiredFailure() {
		t.Fatalf("required failures missing: %+v", result.Outcomes)
	}
	kinds := map[CreateOutcomeKind]bool{}
	for _, warning := range result.Warnings() {
		kinds[warning.Kind] = true
	}
	if !kinds[CreateOutcomeIdentity] || !kinds[CreateOutcomeAgent] || !kinds[CreateOutcomeHook] {
		t.Fatalf("structured warnings = %+v", result.Warnings())
	}
}

func TestCreationBusyRejectsDuplicateSubmitAndCancel(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 3}
	p.viewMode = ViewModeCreate
	p.createBusyStep = "Creating Git worktree"
	p.activeLifecycleOperationID = "3-live"
	if cmd := p.validateAndCreateWorktree(); cmd != nil {
		t.Fatal("duplicate submit returned a command")
	}
	if cmd := p.handleCreateKeys(tea.KeyPressMsg{Code: tea.KeyEscape}); cmd != nil || p.viewMode != ViewModeCreate {
		t.Fatal("busy cancel changed creation state")
	}
	if p.activeLifecycleOperationID != "3-live" {
		t.Fatal("duplicate submit replaced operation identity")
	}
}

func TestCreationConfirmationAndRecoveryAreVisibleAndNoAgentAutostarts(t *testing.T) {
	p := New()
	p.width, p.height = 100, 40
	p.createPlan = &CreateOperationPlan{SourceRef: "refs/heads/main", SourceOID: strings.Repeat("a", 40), SourceWorktree: "/repo", MainWorktree: "/repo", Path: "/feature/auth", Branch: "feature/auth", RemotePolicy: "local branch only; no remote push", TaskID: "td-123", EnvFiles: []string{".env.local"}, CopyEnv: true, RunHook: true, HookPath: setupScriptName, HookRequired: true}
	p.createCopyEnv, p.createRunHook = true, true
	p.ensureCreateOperationModal()
	view := ansi.Strip(p.createOperationModal.Render(p.width, p.height, p.mouseHandler))
	for _, want := range []string{"refs/heads/main", strings.Repeat("a", 40), "/feature/auth", "td-123", ".env.local", setupScriptName, "no remote push"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation missing %q:\n%s", want, view)
		}
	}
	wt := &Worktree{Key: "new", Path: "/feature/auth", Branch: "feature/auth", HEADOID: strings.Repeat("b", 40)}
	result := &CreateSetupResult{Worktree: wt, Outcomes: []CreateSetupOutcome{{Kind: CreateOutcomeHook, Action: "run hook", Required: true, Err: os.ErrPermission}}}
	p.createSetupResult, p.createOperationModal = result, nil
	p.ensureCreateOperationModal()
	view = ansi.Strip(p.createOperationModal.Render(p.width, p.height, p.mouseHandler))
	for _, want := range []string{"Retry Setup", "Open Anyway", "Delete Newly Created"} {
		if !strings.Contains(view, want) {
			t.Fatalf("recovery missing %q:\n%s", want, view)
		}
	}
	p.viewMode = ViewModeCreate
	p.worktrees = nil
	p.agents = make(map[string]*Agent)
	_, cmd := p.Update(CreateSetupDoneMsg{Plan: p.createPlan, Result: result})
	if cmd != nil {
		t.Fatal("failed required setup scheduled agent/attach command")
	}
	if p.viewMode != ViewModeCreate || len(p.agents) != 0 {
		t.Fatal("failed setup did not remain in recovery")
	}
}

func TestDeleteNewlyCreatedRevalidatesHEADAndPathIdentity(t *testing.T) {
	r := newCreateRepo(t)
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/delete", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := addCreatedWorktree(context.Background(), "repo", plan)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(wt.Path, "changed.txt"), "changed\n")
	mustGit(t, wt.Path, "add", "changed.txt")
	mustGit(t, wt.Path, "commit", "-m", "advance created worktree")
	p := New()
	p.ctx = &plugin.Context{Epoch: 9, WorkDir: r.main, ProjectRoot: r.main}
	p.operationCtx = context.Background()
	p.activeLifecycleOperationID = "9-create"
	p.createPlan = plan
	p.createSetupResult = &CreateSetupResult{Worktree: wt}
	msg := p.deleteNewlyCreatedCmd()().(CreateRecoveryDeleteDoneMsg)
	if msg.Err == nil || !strings.Contains(msg.Err.Error(), "HEAD changed") {
		t.Fatalf("identity refusal = %v", msg.Err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("changed worktree was removed: %v", err)
	}
}
