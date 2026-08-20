package workspace

import (
	"context"
	"errors"
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

func TestResolveCreateOperationSplitsDisplayNameFromSlug(t *testing.T) {
	r := newCreateRepo(t)
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "Auth Refresh", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DisplayName != "Auth Refresh" {
		t.Fatalf("DisplayName = %q, want Auth Refresh", plan.DisplayName)
	}
	if plan.Branch != "auth-refresh" {
		t.Fatalf("Branch = %q, want auth-refresh", plan.Branch)
	}
	rootReal, _ := filepath.EvalSymlinks(r.root)
	if want := filepath.Join(rootReal, "auth-refresh"); plan.Path != want {
		t.Fatalf("Path = %q, want %q", plan.Path, want)
	}

	plan, err = resolveCreateOperation(context.Background(), r.main, r.main, "feature", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DisplayName != "feature" || plan.Branch != "feature" {
		t.Fatalf("legal name plan = %+v", plan)
	}
	if want := filepath.Join(rootReal, "feature"); plan.Path != want {
		t.Fatalf("legal name path = %q, want %q", plan.Path, want)
	}
}

func TestResolveCreateDirPrefixAppliesToSlugNotDisplayName(t *testing.T) {
	r := newCreateRepo(t)
	mustGit(t, r.main, "remote", "add", "origin", "git@github.com:acme/widgets.git")
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "Auth Refresh", "main", true, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.DisplayName != "Auth Refresh" {
		t.Fatalf("DisplayName = %q, want Auth Refresh", plan.DisplayName)
	}
	if plan.Branch != "auth-refresh" {
		t.Fatalf("Branch = %q, want auth-refresh", plan.Branch)
	}
	rootReal, _ := filepath.EvalSymlinks(r.root)
	if want := filepath.Join(rootReal, "widgets-auth-refresh"); plan.Path != want {
		t.Fatalf("Path = %q, want %q", plan.Path, want)
	}
}

func TestCreateSetupPersistsDisplayNameAcrossRefresh(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	r := newCreateRepo(t)
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "Auth Refresh", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := addCreatedWorktree(context.Background(), "repo", plan)
	if err != nil {
		t.Fatal(err)
	}
	if wt.Name != "Auth Refresh" {
		t.Fatalf("created name = %q, want Auth Refresh", wt.Name)
	}
	result := runCreateSetup(context.Background(), plan, wt)
	if warnings := result.Warnings(); len(warnings) != 0 {
		t.Fatalf("setup warnings = %+v", warnings)
	}
	if got := loadDisplayName(plan.MainWorktree, wt.Path); got != "Auth Refresh" {
		t.Fatalf("persisted display name = %q", got)
	}
	snapshot, err := BuildRepoSnapshot(context.Background(), r.main)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tree := range snapshotToWorktrees(snapshot) {
		if tree.Path != wt.Path {
			continue
		}
		found = true
		if tree.Name != "Auth Refresh" {
			t.Fatalf("refresh name = %q, want Auth Refresh", tree.Name)
		}
		if tree.Branch != "auth-refresh" {
			t.Fatalf("refresh branch = %q, want auth-refresh", tree.Branch)
		}
	}
	if !found {
		t.Fatal("created worktree missing from refresh snapshot")
	}
}

func TestSnapshotToWorktreesKeepsPathNameWithoutDisplayFile(t *testing.T) {
	snapshot := &RepoSnapshot{
		CanonicalRoot: "/repo",
		Worktrees: []WorktreeSnapshot{
			{Path: "/repo", IsMain: true},
			{Path: "/auth-refresh", Branch: "auth-refresh"},
		},
	}
	trees := snapshotToWorktrees(snapshot)
	if len(trees) != 2 || trees[1].Name != "auth-refresh" {
		t.Fatalf("path-derived name = %#v", trees)
	}
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
			mainReal, _ := filepath.EvalSymlinks(r.main)
			if plan.MainWorktree != mainReal || plan.SourceRef != "refs/heads/main" || plan.SourceOID == "" {
				t.Fatalf("resolved plan = %+v", plan)
			}
			rootReal, _ := filepath.EvalSymlinks(r.root)
			if want := filepath.Join(rootReal, "feature", tc.name); plan.Path != want {
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

func TestCreateRejectsSymlinkDestinationParentAtMutation(t *testing.T) {
	r := newCreateRepo(t)
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/auth", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(r.root, "outside")
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.root, "feature")); err != nil {
		t.Fatal(err)
	}
	_, err = addCreatedWorktree(context.Background(), "repo", plan)
	var containmentErr *containmentPathError
	if err == nil || !errors.As(err, &containmentErr) || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink destination error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "auth")); !os.IsNotExist(err) {
		t.Fatalf("outside checkout created: %v", err)
	}
}

func TestCreatePinsDestinationIdentityAcrossParentSwapInRunner(t *testing.T) {
	r := newCreateRepo(t)
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/auth", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	confirmedPath := plan.Path
	outside := filepath.Join(r.root, "outside-runner")
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	validatedParent := filepath.Dir(plan.Path)
	pinnedName := validatedParent + "-pinned"
	wt, err := addCreatedWorktreeWithRunner(context.Background(), "repo", plan, func(cmd *exec.Cmd) ([]byte, error) {
		if renameErr := os.Rename(validatedParent, pinnedName); renameErr != nil {
			t.Fatal(renameErr)
		}
		if linkErr := os.Symlink(outside, validatedParent); linkErr != nil {
			t.Fatal(linkErr)
		}
		return cmd.CombinedOutput()
	})
	if err == nil || wt == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("parent-swap result = wt %+v err %v", wt, err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "auth")); !os.IsNotExist(statErr) {
		t.Fatalf("outside checkout created for %s: %v", confirmedPath, statErr)
	}
	if wt.Path != filepath.Join(pinnedName, "auth") {
		t.Fatalf("retained checkout path = %q", wt.Path)
	}
	if got := mustGit(t, wt.Path, "rev-parse", "HEAD"); got != plan.SourceOID {
		t.Fatalf("retained checkout HEAD = %s", got)
	}
}

func TestCreateReconcilesInterruptedAddAfterGitMutation(t *testing.T) {
	r := newCreateRepo(t)
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/interrupted", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := addCreatedWorktreeWithRunner(context.Background(), "repo", plan, func(cmd *exec.Cmd) ([]byte, error) {
		output, runErr := cmd.CombinedOutput()
		if runErr != nil {
			return output, runErr
		}
		return output, context.Canceled
	})
	if err == nil || wt == nil {
		t.Fatalf("interrupted reconciliation = worktree %+v err %v", wt, err)
	}
	if wt.Path != plan.Path || wt.HEADOID != plan.SourceOID {
		t.Fatalf("partial identity = %+v", wt)
	}
}

func TestCreateSetupRejectsSymlinkArtifactSwaps(t *testing.T) {
	for _, tc := range []struct {
		name, artifact string
		hook           bool
	}{
		{"env", ".env.safe", false}, {"hook", "setup-safe.sh", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newCreateRepo(t)
			artifact := filepath.Join(r.main, tc.artifact)
			mustWrite(t, artifact, "safe\n")
			setup := config.WorktreeSetupConfig{CopyEnvFiles: !tc.hook, EnvFiles: []string{tc.artifact}, RunHook: tc.hook, HookPath: tc.artifact, HookRequired: true}
			plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/"+tc.name, "main", false, setup)
			if err != nil {
				t.Fatal(err)
			}
			wt, err := addCreatedWorktree(context.Background(), "repo", plan)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(artifact); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(r.root, "outside-"+tc.name)
			if tc.hook {
				mustWrite(t, outside, "#!/bin/sh\ntouch \"$WORKTREE_PATH/outside-hook-ran\"\n")
			} else {
				mustWrite(t, outside, "OUTSIDE_SECRET=do-not-copy\n")
			}
			if err := os.Symlink(outside, artifact); err != nil {
				t.Fatal(err)
			}
			result := runCreateSetup(context.Background(), plan, wt)
			if len(result.Warnings()) == 0 {
				t.Fatalf("symlink swap was accepted: %+v", result.Outcomes)
			}
			if tc.hook {
				if _, err := os.Stat(filepath.Join(wt.Path, "outside-hook-ran")); !os.IsNotExist(err) {
					t.Fatalf("outside hook executed: %v", err)
				}
			} else if _, err := os.Stat(filepath.Join(wt.Path, tc.artifact)); !os.IsNotExist(err) {
				t.Fatalf("outside env copied: %v", err)
			}
		})
	}
}

func TestPinnedArtifactOpenRejectsParentSwapAfterValidation(t *testing.T) {
	for _, hook := range []bool{false, true} {
		name := "env"
		if hook {
			name = "hook"
		}
		t.Run(name, func(t *testing.T) {
			r := newCreateRepo(t)
			safeDir := filepath.Join(r.main, "safe")
			if err := os.Mkdir(safeDir, 0755); err != nil {
				t.Fatal(err)
			}
			artifactName := "artifact"
			mustWrite(t, filepath.Join(safeDir, artifactName), "safe\n")
			outside := filepath.Join(r.root, "outside-"+name)
			if err := os.Mkdir(outside, 0755); err != nil {
				t.Fatal(err)
			}
			outsideArtifact := "OUTSIDE_SECRET\n"
			if hook {
				outsideArtifact = "#!/bin/sh\ntouch \"$WORKTREE_PATH/outside-parent-hook-ran\"\n"
			}
			mustWrite(t, filepath.Join(outside, artifactName), outsideArtifact)
			plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/parent-"+name, "main", false, config.WorktreeSetupConfig{})
			if err != nil {
				t.Fatal(err)
			}
			wt, err := addCreatedWorktree(context.Background(), "repo", plan)
			if err != nil {
				t.Fatal(err)
			}
			plan.HookPath = filepath.Join("safe", artifactName)
			swap := func() {
				if err := os.Rename(safeDir, safeDir+"-pinned"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, safeDir); err != nil {
					t.Fatal(err)
				}
			}
			if hook {
				err = runSetupHookContextWithHook(context.Background(), plan, swap)
				if err == nil {
					t.Fatal("hook parent swap was accepted")
				}
				if _, statErr := os.Stat(filepath.Join(wt.Path, "outside-parent-hook-ran")); !os.IsNotExist(statErr) {
					t.Fatalf("outside hook executed: %v", statErr)
				}
			} else {
				file, openErr := openContainedRegularFileWithHook(plan.MainWorktree, filepath.Join("safe", artifactName), swap)
				if file != nil {
					_ = file.Close()
				}
				if openErr == nil {
					t.Fatal("env parent swap was accepted")
				}
			}
		})
	}
}

func TestResolveCreateRejectsSymlinkArtifactsBeforeConfirmation(t *testing.T) {
	r := newCreateRepo(t)
	outside := filepath.Join(r.root, "outside")
	mustWrite(t, outside, "outside\n")
	for _, tc := range []struct {
		name  string
		setup config.WorktreeSetupConfig
	}{
		{"env", config.WorktreeSetupConfig{CopyEnvFiles: true, EnvFiles: []string{".env.link"}}},
		{"hook", config.WorktreeSetupConfig{RunHook: true, HookPath: "setup-link.sh", HookRequired: true}},
	} {
		link := ".env.link"
		if tc.name == "hook" {
			link = "setup-link.sh"
		}
		if err := os.Symlink(outside, filepath.Join(r.main, link)); err != nil {
			t.Fatal(err)
		}
		_, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/"+tc.name, "main", false, tc.setup)
		var containmentErr *containmentPathError
		if err == nil || !errors.As(err, &containmentErr) || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlink %s preflight error = %v", tc.name, err)
		}
		if err := os.Remove(filepath.Join(r.main, link)); err != nil {
			t.Fatal(err)
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
	if got := loadTaskLink(plan.MainWorktree, wt.Path); got != plan.TaskID {
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
	stateDir, err := projectdir.WorktreeDir(plan.MainWorktree, wt.Path)
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

func TestProjectCreatePlanLeavesTaskEmpty(t *testing.T) {
	r := newCreateRepo(t)
	p := New()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: r.main, ProjectRoot: r.main}
	p.initCreateModalNamed("feature/auth")
	p.createForm.SetBranches([]string{"main"}, "main")
	msg := p.resolveCreatePlan()().(CreatePlanResolvedMsg)
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	if msg.Plan == nil {
		t.Fatal("expected create plan")
	}
	if msg.Plan.TaskID != "" || msg.Plan.TaskTitle != "" {
		t.Fatalf("create plan carried task %q %q", msg.Plan.TaskID, msg.Plan.TaskTitle)
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
	for _, want := range []string{"refs/heads/main", strings.Repeat("a", 40), "/feature/auth", ".env.local", setupScriptName, "no remote push"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Task:") || strings.Contains(view, "td-123") {
		t.Fatalf("confirm modal should not mention a task:\n%s", view)
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
	if msg.Result.Err == nil || !strings.Contains(msg.Result.Err.Error(), "HEAD changed") {
		t.Fatalf("identity refusal = %v", msg.Result.Err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("changed worktree was removed: %v", err)
	}
}

func TestStaleCreatedResultIsDeferredWithoutCrossRepoUIAndReconciledOnReturn(t *testing.T) {
	original := newCreateRepo(t)
	other := newCreateRepo(t)
	plan, err := resolveCreateOperation(context.Background(), original.main, original.main, "feature/stale", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	originalSnapshot, err := BuildRepoSnapshot(context.Background(), original.main)
	if err != nil {
		t.Fatal(err)
	}
	plan.RepoKey, plan.OperationID = originalSnapshot.Key, "1-create"
	wt, err := addCreatedWorktree(context.Background(), originalSnapshot.Key, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistPendingCreation(context.Background(), plan, wt); err != nil {
		t.Fatal(err)
	}
	msg := CreateWorktreeAddedMsg{OperationScope: OperationScope{Epoch: 1, OperationID: "1-create", RepoKey: originalSnapshot.Key, Lifecycle: true}, Plan: plan, Worktree: wt}

	p := New()
	cfg := config.Default()
	if err := p.Init(&plugin.Context{Epoch: 2, WorkDir: other.main, ProjectRoot: other.main, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	otherSnapshot, err := BuildRepoSnapshot(context.Background(), other.main)
	if err != nil {
		t.Fatal(err)
	}
	p.repoSnapshot, p.worktrees = otherSnapshot, snapshotToWorktrees(otherSnapshot)
	p.activeLifecycleOperationID = "2-other"
	p.Update(msg)
	if p.viewMode == ViewModeCreate || p.createPlan != nil {
		t.Fatal("stale creation UI was applied to another repository")
	}
	if len(p.deferredCreations) != 1 {
		t.Fatal("stale created result was silently dropped")
	}

	if err := p.Init(&plugin.Context{Epoch: 3, WorkDir: original.main, ProjectRoot: plan.MainWorktree, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	returned, err := BuildRepoSnapshot(context.Background(), original.main)
	if err != nil {
		t.Fatal(err)
	}
	p.repoSnapshot, p.worktrees = returned, snapshotToWorktrees(returned)
	if !p.reconcilePendingCreation() {
		t.Fatal("deferred creation was not reconciled")
	}
	if p.viewMode != ViewModeCreate || p.createSetupResult == nil || p.createSetupResult.Worktree.Path != plan.Path {
		t.Fatalf("recovery state = mode %v plan %+v result %+v", p.viewMode, p.createPlan, p.createSetupResult)
	}
}

func TestPendingCreationJournalRecoversAfterRestart(t *testing.T) {
	r := newCreateRepo(t)
	snapshot, err := BuildRepoSnapshot(context.Background(), r.main)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/restart", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	plan.RepoKey, plan.OperationID = snapshot.Key, "7-create"
	wt, err := addCreatedWorktree(context.Background(), snapshot.Key, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistPendingCreation(context.Background(), plan, wt); err != nil {
		t.Fatal(err)
	}
	journalPath, err := pendingCreationPath(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(journalPath); err != nil || !strings.Contains(string(data), plan.Path) || !strings.Contains(string(data), plan.OperationID) {
		t.Fatalf("inspectable journal = %q, %v", data, err)
	}

	restarted := New()
	if err := restarted.Init(&plugin.Context{Epoch: 8, WorkDir: r.main, ProjectRoot: plan.MainWorktree, Config: config.Default()}); err != nil {
		t.Fatal(err)
	}
	restartedSnapshot, err := BuildRepoSnapshot(context.Background(), r.main)
	if err != nil {
		t.Fatal(err)
	}
	restarted.repoSnapshot, restarted.worktrees = restartedSnapshot, snapshotToWorktrees(restartedSnapshot)
	if !restarted.reconcilePendingCreation() {
		t.Fatal("restart did not find pending creation journal")
	}
	if restarted.createSetupResult == nil || len(restarted.createSetupResult.Warnings()) == 0 {
		t.Fatal("restart did not surface recovery")
	}
}

func TestPendingCreationJournalRemovalFailuresAndDurableSuccess(t *testing.T) {
	newPending := func(t *testing.T, branch string) (*CreateOperationPlan, *Worktree, string) {
		r := newCreateRepo(t)
		snapshot, err := BuildRepoSnapshot(context.Background(), r.main)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := resolveCreateOperation(context.Background(), r.main, r.main, branch, "main", false, config.WorktreeSetupConfig{})
		if err != nil {
			t.Fatal(err)
		}
		plan.RepoKey, plan.OperationID = snapshot.Key, "journal-"+branch
		wt, err := addCreatedWorktree(context.Background(), snapshot.Key, plan)
		if err != nil {
			t.Fatal(err)
		}
		if err := persistPendingCreation(context.Background(), plan, wt); err != nil {
			t.Fatal(err)
		}
		path, err := pendingCreationPath(context.Background(), plan)
		if err != nil {
			t.Fatal(err)
		}
		return plan, wt, path
	}

	t.Run("unlink failure", func(t *testing.T) {
		plan, _, path := newPending(t, "feature/unlink-fail")
		err := removePendingCreationWithOps(plan, func(string) error { return os.ErrPermission }, func(string) error { t.Fatal("sync called after failed unlink"); return nil })
		if err == nil {
			t.Fatal("unlink failure was ignored")
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("journal lost after unlink failure: %v", statErr)
		}
	})

	t.Run("directory sync failure", func(t *testing.T) {
		plan, _, path := newPending(t, "feature/sync-fail")
		err := removePendingCreationWithOps(plan, os.Remove, func(string) error { return os.ErrInvalid })
		if err == nil || !strings.Contains(err.Error(), "sync") {
			t.Fatalf("sync failure = %v", err)
		}
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("journal still exists after successful unlink: %v", statErr)
		}
	})

	t.Run("success does not resurrect", func(t *testing.T) {
		plan, wt, path := newPending(t, "feature/no-resurrection")
		if err := removePendingCreation(plan); err != nil {
			t.Fatal(err)
		}
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("journal still exists: %v", statErr)
		}
		restarted := New()
		if err := restarted.Init(&plugin.Context{Epoch: 12, WorkDir: plan.MainWorktree, ProjectRoot: plan.MainWorktree, Config: config.Default()}); err != nil {
			t.Fatal(err)
		}
		snapshot, err := BuildRepoSnapshot(context.Background(), plan.MainWorktree)
		if err != nil {
			t.Fatal(err)
		}
		restarted.repoSnapshot, restarted.worktrees = snapshot, snapshotToWorktrees(snapshot)
		if restarted.reconcilePendingCreation() || restarted.createSetupResult != nil {
			t.Fatalf("completed creation resurrected for %s", wt.Path)
		}
	})
}

func TestOpenAnywayKeepsRecoveryWhenJournalCannotBeRemoved(t *testing.T) {
	r := newCreateRepo(t)
	snapshot, err := BuildRepoSnapshot(context.Background(), r.main)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/open-fail", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	plan.RepoKey, plan.OperationID = snapshot.Key, "open-fail"
	wt, err := addCreatedWorktree(context.Background(), snapshot.Key, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistPendingCreation(context.Background(), plan, wt); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.ctx = &plugin.Context{Epoch: 14, WorkDir: plan.MainWorktree, ProjectRoot: plan.MainWorktree, Config: config.Default()}
	p.viewMode, p.createPlan = ViewModeCreate, plan
	p.createSetupResult = &CreateSetupResult{Worktree: wt, Outcomes: []CreateSetupOutcome{{Kind: CreateOutcomeHook, Action: "hook", Required: true, Err: os.ErrInvalid}}}
	p.removePendingCreationFn = func(*CreateOperationPlan) error { return os.ErrPermission }
	_, cmd := p.Update(CreateOpenAnywayMsg{})
	if cmd != nil || p.viewMode != ViewModeCreate || p.createSetupResult == nil {
		t.Fatal("Open Anyway cleared recovery after journal removal failure")
	}
	warnings := p.createSetupResult.Warnings()
	if len(warnings) < 2 || !strings.Contains(warnings[len(warnings)-1].Action, "journal") {
		t.Fatalf("journal failure not surfaced: %+v", warnings)
	}

	restarted := New()
	if err := restarted.Init(&plugin.Context{Epoch: 15, WorkDir: plan.MainWorktree, ProjectRoot: plan.MainWorktree, Config: config.Default()}); err != nil {
		t.Fatal(err)
	}
	restartedSnapshot, err := BuildRepoSnapshot(context.Background(), plan.MainWorktree)
	if err != nil {
		t.Fatal(err)
	}
	restarted.repoSnapshot, restarted.worktrees = restartedSnapshot, snapshotToWorktrees(restartedSnapshot)
	if !restarted.reconcilePendingCreation() {
		t.Fatal("failed journal removal did not remain restart-recoverable")
	}
}

func TestDeletePartialSuccessClearsInvalidRecoveryActions(t *testing.T) {
	r := newCreateRepo(t)
	plan, err := resolveCreateOperation(context.Background(), r.main, r.main, "feature/partial-delete", "main", false, config.WorktreeSetupConfig{})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := addCreatedWorktree(context.Background(), "repo", plan)
	if err != nil {
		t.Fatal(err)
	}
	otherOID := mustGit(t, r.main, "commit-tree", wt.HEADOID+"^{tree}", "-m", "replacement")
	result := deleteNewlyCreated(context.Background(), plan, wt.HEADOID, func() {
		mustGit(t, r.main, "update-ref", "refs/heads/"+plan.Branch, otherOID, wt.HEADOID)
	})
	if !result.WorktreeRemoved || !result.BranchRetained || result.BranchDeleted || result.Err == nil {
		t.Fatalf("partial cleanup = %+v", result)
	}
	if _, err := os.Stat(plan.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists: %v", err)
	}

	p := New()
	p.width, p.height = 100, 40
	p.viewMode = ViewModeCreate
	p.createPlan = plan
	p.createSetupResult = &CreateSetupResult{Worktree: wt, Outcomes: []CreateSetupOutcome{{Kind: CreateOutcomeHook, Err: os.ErrInvalid}}}
	p.worktrees = []*Worktree{wt}
	p.Update(CreateRecoveryDeleteDoneMsg{Result: result})
	if len(p.worktrees) != 0 || p.createDeleteResult == nil {
		t.Fatal("partial cleanup did not remove stale worktree UI identity")
	}
	p.ensureCreateOperationModal()
	view := ansi.Strip(p.createOperationModal.Render(p.width, p.height, p.mouseHandler))
	if !strings.Contains(view, "worktree directory was removed") || !strings.Contains(view, "Branch retained") {
		t.Fatalf("partial cleanup not surfaced:\n%s", view)
	}
	for _, invalid := range []string{"Retry Setup", "Open Anyway", "Delete Newly Created"} {
		if strings.Contains(view, invalid) {
			t.Fatalf("invalid action %q remains:\n%s", invalid, view)
		}
	}
}
