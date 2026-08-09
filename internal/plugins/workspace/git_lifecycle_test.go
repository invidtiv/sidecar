package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
)

type lifecycleRepo struct {
	root, main, feature, remote string
}

func newLifecycleRepo(t *testing.T) lifecycleRepo {
	t.Helper()
	root := t.TempDir()
	r := lifecycleRepo{
		root: root, main: filepath.Join(root, "repo"), feature: filepath.Join(root, "feature"), remote: filepath.Join(root, "remote.git"),
	}
	mustGit(t, root, "init", "--bare", r.remote)
	mustGit(t, root, "init", r.main)
	mustGit(t, r.main, "config", "user.email", "sidecar-test@example.com")
	mustGit(t, r.main, "config", "user.name", "Sidecar Test")
	mustWrite(t, filepath.Join(r.main, "shared.txt"), "base\n")
	mustGit(t, r.main, "add", "shared.txt")
	mustGit(t, r.main, "commit", "-m", "base")
	mustGit(t, r.main, "branch", "-M", "main")
	mustGit(t, r.main, "remote", "add", "origin", r.remote)
	mustGit(t, r.main, "push", "-u", "origin", "main")
	mustGit(t, r.main, "branch", "feature")
	mustGit(t, r.main, "worktree", "add", r.feature, "feature")
	mustGit(t, r.feature, "config", "user.email", "sidecar-test@example.com")
	mustGit(t, r.feature, "config", "user.name", "Sidecar Test")
	mustWrite(t, filepath.Join(r.feature, "feature.txt"), "feature\n")
	mustGit(t, r.feature, "add", "feature.txt")
	mustGit(t, r.feature, "commit", "-m", "feature")
	return r
}

func TestDirectMergeUsesCheckedOutTargetFromEitherContext(t *testing.T) {
	for _, context := range []string{"main", "feature"} {
		t.Run(context, func(t *testing.T) {
			r := newLifecycleRepo(t)
			repoPath := r.main
			if context == "feature" {
				repoPath = r.feature
			}
			op, err := preflightDirectMerge(repoPath, r.feature, "feature", "main")
			if err != nil {
				t.Fatalf("preflight: %v", err)
			}
			if op.TargetPath != canonicalGitPath(r.main) {
				t.Fatalf("target path = %q, want main checkout %q", op.TargetPath, r.main)
			}
			op = runDirectMerge(op)
			if op.Err != nil {
				t.Fatalf("direct merge: %v\n%s", op.Err, op.GitState)
			}
			if got := strings.TrimSpace(mustRead(t, filepath.Join(r.main, "feature.txt"))); got != "feature" {
				t.Fatalf("main checkout was not updated, feature.txt = %q", got)
			}
			if status := mustGit(t, r.main, "status", "--porcelain"); status != "" {
				t.Fatalf("main checkout desynchronized: %s", status)
			}
			if remote := mustGit(t, r.main, "rev-parse", "origin/main"); remote != op.MergeOID {
				t.Fatalf("remote main = %s, want merge %s", remote, op.MergeOID)
			}
		})
	}
}

func TestDirectMergeDirtyTargetRefusesWithoutMutation(t *testing.T) {
	r := newLifecycleRepo(t)
	before := mustGit(t, r.main, "rev-parse", "HEAD")
	mustWrite(t, filepath.Join(r.main, "dirty.txt"), "do not touch\n")
	_, err := preflightDirectMerge(r.feature, r.feature, "feature", "main")
	if err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("preflight error = %v, want dirty refusal", err)
	}
	if after := mustGit(t, r.main, "rev-parse", "HEAD"); after != before {
		t.Fatalf("dirty refusal mutated target: %s -> %s", before, after)
	}
	if remote := mustGit(t, r.main, "rev-parse", "origin/main"); remote != before {
		t.Fatalf("dirty refusal mutated remote: %s -> %s", before, remote)
	}
}

func TestDirectMergeRefusesTargetSwitchedToBranchAtSameOID(t *testing.T) {
	r := newLifecycleRepo(t)
	op, err := preflightDirectMerge(r.feature, r.feature, "feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	mainBefore := mustGit(t, r.main, "rev-parse", "refs/heads/main")
	remoteBefore := mustGit(t, r.main, "rev-parse", "origin/main")
	mustGit(t, r.main, "switch", "-c", "parking")
	if parking := mustGit(t, r.main, "rev-parse", "HEAD"); parking != op.TargetOID {
		t.Fatalf("fixture parking OID = %s, want reviewed target %s", parking, op.TargetOID)
	}

	op = runDirectMerge(op)
	if op.Err == nil || !strings.Contains(op.Err.Error(), "expected \"main\"") {
		t.Fatalf("merge result = %+v, want target branch identity refusal", op)
	}
	if mainAfter := mustGit(t, r.main, "rev-parse", "refs/heads/main"); mainAfter != mainBefore {
		t.Fatalf("local main mutated: %s -> %s", mainBefore, mainAfter)
	}
	if remoteAfter := mustGit(t, r.main, "rev-parse", "origin/main"); remoteAfter != remoteBefore {
		t.Fatalf("remote main mutated: %s -> %s", remoteBefore, remoteAfter)
	}
	if _, err := os.Stat(filepath.Join(r.main, "feature.txt")); !os.IsNotExist(err) {
		t.Fatalf("parking checkout was mutated by feature merge: %v", err)
	}
}

func TestDirectMergeRefusesCleanTargetCommitAfterPostPullPin(t *testing.T) {
	r := newLifecycleRepo(t)
	op, err := preflightDirectMerge(r.feature, r.feature, "feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	remoteBefore := mustGit(t, r.main, "rev-parse", "origin/main")
	op = runDirectMergeWithBeforeMerge(op, func() {
		mustWrite(t, filepath.Join(r.main, "concurrent.txt"), "concurrent target work\n")
		mustGit(t, r.main, "add", "concurrent.txt")
		mustGit(t, r.main, "commit", "-m", "concurrent target commit")
	})
	if op.Err == nil || !strings.Contains(op.Err.Error(), "HEAD changed after review") {
		t.Fatalf("merge result = %+v, want post-pull target OID refusal", op)
	}
	if op.PreMergeOID == "" {
		t.Fatal("post-pull target OID was not pinned")
	}
	if remoteAfter := mustGit(t, r.main, "rev-parse", "origin/main"); remoteAfter != remoteBefore {
		t.Fatalf("remote main mutated: %s -> %s", remoteBefore, remoteAfter)
	}
	if _, err := os.Stat(filepath.Join(r.main, "feature.txt")); !os.IsNotExist(err) {
		t.Fatalf("feature merge mutated target after concurrent commit: %v", err)
	}
}

func TestDirectMergeRefusesSameOIDBranchSwitchAfterFetchBeforePull(t *testing.T) {
	r := newLifecycleRepo(t)
	op, err := preflightDirectMerge(r.feature, r.feature, "feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	mainBefore := mustGit(t, r.main, "rev-parse", "refs/heads/main")
	clone := filepath.Join(r.root, "publisher-direct-pull")
	mustGit(t, r.root, "clone", r.remote, clone)
	mustGit(t, clone, "config", "user.email", "sidecar-test@example.com")
	mustGit(t, clone, "config", "user.name", "Sidecar Test")
	mustGit(t, clone, "checkout", "main")
	mustWrite(t, filepath.Join(clone, "remote-direct.txt"), "remote direct update\n")
	mustGit(t, clone, "add", "remote-direct.txt")
	mustGit(t, clone, "commit", "-m", "remote direct update")
	mustGit(t, clone, "push", "origin", "main")
	remoteOID := mustGit(t, clone, "rev-parse", "HEAD")

	op = runDirectMergeWithBeforePull(op, func() {
		mustGit(t, r.main, "switch", "-c", "parking")
	})
	if op.Err == nil || !strings.Contains(op.Err.Error(), "expected \"main\"") {
		t.Fatalf("direct merge result = %+v, want pre-pull checkout refusal", op)
	}
	if mainAfter := mustGit(t, r.main, "rev-parse", "refs/heads/main"); mainAfter != mainBefore {
		t.Fatalf("local main moved: %s -> %s", mainBefore, mainAfter)
	}
	if parking := mustGit(t, r.main, "rev-parse", "HEAD"); parking != mainBefore {
		t.Fatalf("parking moved: got %s want %s", parking, mainBefore)
	}
	if _, err := os.Stat(filepath.Join(r.main, "remote-direct.txt")); !os.IsNotExist(err) {
		t.Fatalf("remote target was pulled into parking: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.main, "feature.txt")); !os.IsNotExist(err) {
		t.Fatalf("feature was merged into parking: %v", err)
	}
	if tracking := mustGit(t, r.main, "rev-parse", "origin/main"); tracking != remoteOID {
		t.Fatalf("fetched origin/main = %s, want %s", tracking, remoteOID)
	}
	remoteAfter, err := remoteBranchOID(r.main, "origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	if remoteAfter != remoteOID {
		t.Fatalf("remote main changed unexpectedly: got %s want %s", remoteAfter, remoteOID)
	}
}

func TestDirectMergeConflictCanAbortToPreMergeTarget(t *testing.T) {
	r := newLifecycleRepo(t)
	mustWrite(t, filepath.Join(r.main, "shared.txt"), "main change\n")
	mustGit(t, r.main, "add", "shared.txt")
	mustGit(t, r.main, "commit", "-m", "main change")
	mustGit(t, r.main, "push", "origin", "main")
	mustWrite(t, filepath.Join(r.feature, "shared.txt"), "feature change\n")
	mustGit(t, r.feature, "add", "shared.txt")
	mustGit(t, r.feature, "commit", "-m", "feature conflict")

	op, err := preflightDirectMerge(r.feature, r.feature, "feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	op = runDirectMerge(op)
	if op.Recovery != DirectMergeRecoveryConflict || op.Err == nil {
		t.Fatalf("result = recovery %q error %v, want conflict", op.Recovery, op.Err)
	}
	if state := gitOperationState(r.main); state != "merge" {
		t.Fatalf("merge state = %q, want visible in-progress merge", state)
	}
	preMerge := op.PreMergeOID
	op = abortDirectMerge(op)
	if op.Err != nil || !op.Aborted {
		t.Fatalf("abort: %+v", op)
	}
	if head := mustGit(t, r.main, "rev-parse", "HEAD"); head != preMerge {
		t.Fatalf("abort restored %s, want %s", head, preMerge)
	}
	if status := mustGit(t, r.main, "status", "--porcelain"); status != "" {
		t.Fatalf("abort left dirty target: %s", status)
	}
}

func TestDirectMergeConflictCanContinueAfterResolution(t *testing.T) {
	r := newLifecycleRepo(t)
	mustWrite(t, filepath.Join(r.main, "shared.txt"), "main change\n")
	mustGit(t, r.main, "add", "shared.txt")
	mustGit(t, r.main, "commit", "-m", "main change")
	mustGit(t, r.main, "push", "origin", "main")
	mustWrite(t, filepath.Join(r.feature, "shared.txt"), "feature change\n")
	mustGit(t, r.feature, "add", "shared.txt")
	mustGit(t, r.feature, "commit", "-m", "feature conflict")

	op, err := preflightDirectMerge(r.feature, r.feature, "feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	op = runDirectMerge(op)
	if op.Recovery != DirectMergeRecoveryConflict {
		t.Fatalf("recovery = %q, want conflict", op.Recovery)
	}
	mustWrite(t, filepath.Join(r.main, "shared.txt"), "resolved\n")
	mustGit(t, r.main, "add", "shared.txt")
	op = continueDirectMerge(op)
	if op.Err != nil || op.Recovery != DirectMergeRecoveryNone || op.MergeOID == "" {
		t.Fatalf("continue result: %+v", op)
	}
	if remote := mustGit(t, r.main, "rev-parse", "origin/main"); remote != op.MergeOID {
		t.Fatalf("continued merge was not pushed: %s != %s", remote, op.MergeOID)
	}
}

func TestMergeErrorWrongRecoveryKeyPreservesConflictControls(t *testing.T) {
	r := newLifecycleRepo(t)
	mustWrite(t, filepath.Join(r.main, "shared.txt"), "main change\n")
	mustGit(t, r.main, "add", "shared.txt")
	mustGit(t, r.main, "commit", "-m", "main change")
	mustGit(t, r.main, "push", "origin", "main")
	mustWrite(t, filepath.Join(r.feature, "shared.txt"), "feature change\n")
	mustGit(t, r.feature, "add", "shared.txt")
	mustGit(t, r.feature, "commit", "-m", "feature conflict")
	op, err := preflightDirectMerge(r.feature, r.feature, "feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	op = runDirectMerge(op)
	if op.Recovery != DirectMergeRecoveryConflict {
		t.Fatalf("fixture recovery = %q", op.Recovery)
	}
	p := &Plugin{viewMode: ViewModeMerge, width: 100, height: 40, mergeState: &MergeWorkflowState{
		Worktree: &Worktree{Name: "feature"}, Step: MergeStepError, TargetBranch: "main",
		DirectOperation: op, StepStatus: map[MergeWorkflowStep]string{MergeStepDirectMerge: "error"},
		ErrorTitle: "Direct Merge Failed", ErrorDetail: op.Err.Error(),
	}}
	if cmd := p.handleMergeKeys(tea.KeyPressMsg{Code: 'r', Text: "r"}); cmd != nil {
		t.Fatal("wrong recovery key unexpectedly returned a command")
	}
	if op.Recovery != DirectMergeRecoveryConflict || gitOperationState(r.main) != "merge" {
		t.Fatalf("wrong recovery key changed conflict state: recovery=%q git=%q", op.Recovery, gitOperationState(r.main))
	}
	ids := commandIDs(p.Commands())
	if !ids["continue-merge"] || !ids["abort-merge"] {
		t.Fatalf("wrong recovery key removed conflict controls: %v", ids)
	}
	if aborted := abortDirectMerge(op); aborted.Err != nil {
		t.Fatalf("cleanup abort: %v", aborted.Err)
	}
}

func TestMergeErrorWrongRecoveryKeysPreservePushRetry(t *testing.T) {
	op := &DirectMergeOperation{Recovery: DirectMergeRecoveryPushFailure}
	p := &Plugin{viewMode: ViewModeMerge, width: 100, height: 40, mergeState: &MergeWorkflowState{
		Worktree: &Worktree{Name: "feature"}, Step: MergeStepError, DirectOperation: op,
		StepStatus: map[MergeWorkflowStep]string{MergeStepDirectMerge: "error"}, ErrorTitle: "Push Failed", ErrorDetail: "rejected",
	}}
	for _, key := range []rune{'c', 'a'} {
		if cmd := p.handleMergeKeys(tea.KeyPressMsg{Code: key, Text: string(key)}); cmd != nil {
			t.Fatalf("wrong recovery key %q unexpectedly returned a command", key)
		}
		if op.Recovery != DirectMergeRecoveryPushFailure {
			t.Fatalf("wrong recovery key %q changed recovery to %q", key, op.Recovery)
		}
	}
	if !commandIDs(p.Commands())["retry-push"] {
		t.Fatal("wrong recovery keys removed Retry Push")
	}
}

func TestAbortRefusesReplacementMergeAndPreservesResolution(t *testing.T) {
	r, op := newSidecarConflict(t)
	unrelatedOID, resolution, statusBefore := replaceWithUnrelatedConflict(t, r)
	headBefore := mustGit(t, r.main, "rev-parse", "HEAD")

	result := abortDirectMerge(op)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "MERGE_HEAD changed") {
		t.Fatalf("abort result = %+v, want replacement merge refusal", result)
	}
	if result.Recovery != DirectMergeRecoveryConflict {
		t.Fatalf("abort refusal removed conflict recovery: %q", result.Recovery)
	}
	assertReplacementConflictUnchanged(t, r, unrelatedOID, headBefore, resolution, statusBefore)
}

func TestContinueRefusesReplacementMergeAndPreservesResolution(t *testing.T) {
	r, op := newSidecarConflict(t)
	remoteBefore := mustGit(t, r.main, "rev-parse", "origin/main")
	unrelatedOID, resolution, statusBefore := replaceWithUnrelatedConflict(t, r)
	headBefore := mustGit(t, r.main, "rev-parse", "HEAD")

	result := continueDirectMerge(op)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "MERGE_HEAD changed") {
		t.Fatalf("continue result = %+v, want replacement merge refusal", result)
	}
	if result.Recovery != DirectMergeRecoveryConflict {
		t.Fatalf("continue refusal removed conflict recovery: %q", result.Recovery)
	}
	assertReplacementConflictUnchanged(t, r, unrelatedOID, headBefore, resolution, statusBefore)
	if remoteAfter := mustGit(t, r.main, "rev-parse", "origin/main"); remoteAfter != remoteBefore {
		t.Fatalf("replacement merge was pushed: %s -> %s", remoteBefore, remoteAfter)
	}
}

func newSidecarConflict(t *testing.T) (lifecycleRepo, *DirectMergeOperation) {
	t.Helper()
	r := newLifecycleRepo(t)
	mustWrite(t, filepath.Join(r.main, "shared.txt"), "main change\n")
	mustGit(t, r.main, "add", "shared.txt")
	mustGit(t, r.main, "commit", "-m", "main change")
	mustGit(t, r.main, "push", "origin", "main")
	mustWrite(t, filepath.Join(r.feature, "shared.txt"), "feature change\n")
	mustGit(t, r.feature, "add", "shared.txt")
	mustGit(t, r.feature, "commit", "-m", "feature conflict")
	op, err := preflightDirectMerge(r.feature, r.feature, "feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	op = runDirectMerge(op)
	if op.Recovery != DirectMergeRecoveryConflict {
		t.Fatalf("fixture recovery = %q, want conflict", op.Recovery)
	}
	return r, op
}

func replaceWithUnrelatedConflict(t *testing.T, r lifecycleRepo) (string, string, string) {
	t.Helper()
	mustGit(t, r.main, "merge", "--abort")
	base := mustGit(t, r.main, "merge-base", "main", "feature")
	mustGit(t, r.main, "branch", "unrelated", base)
	unrelatedPath := filepath.Join(r.root, "unrelated")
	mustGit(t, r.main, "worktree", "add", unrelatedPath, "unrelated")
	mustWrite(t, filepath.Join(unrelatedPath, "shared.txt"), "unrelated change\n")
	mustGit(t, unrelatedPath, "add", "shared.txt")
	mustGit(t, unrelatedPath, "commit", "-m", "unrelated conflict")
	unrelatedOID := mustGit(t, unrelatedPath, "rev-parse", "HEAD")
	cmd := exec.Command("git", "merge", "--no-ff", "unrelated", "-m", "unrelated merge")
	cmd.Dir = r.main
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("replacement merge unexpectedly succeeded: %s", out)
	}
	if mergeHead := mustGit(t, r.main, "rev-parse", "MERGE_HEAD"); mergeHead != unrelatedOID {
		t.Fatalf("replacement MERGE_HEAD = %s, want %s", mergeHead, unrelatedOID)
	}
	resolution := "resolved unrelated work\n"
	mustWrite(t, filepath.Join(r.main, "shared.txt"), resolution)
	mustGit(t, r.main, "add", "shared.txt")
	return unrelatedOID, resolution, mustGit(t, r.main, "status", "--porcelain=v1")
}

func assertReplacementConflictUnchanged(t *testing.T, r lifecycleRepo, mergeHead, head, resolution, status string) {
	t.Helper()
	if got := mustGit(t, r.main, "rev-parse", "MERGE_HEAD"); got != mergeHead {
		t.Fatalf("MERGE_HEAD changed: got %s want %s", got, mergeHead)
	}
	if got := mustGit(t, r.main, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD changed: got %s want %s", got, head)
	}
	if got := mustRead(t, filepath.Join(r.main, "shared.txt")); got != resolution {
		t.Fatalf("resolution changed: got %q want %q", got, resolution)
	}
	if got := mustGit(t, r.main, "status", "--porcelain=v1"); got != status {
		t.Fatalf("index/worktree status changed: got %q want %q", got, status)
	}
}

func TestDirectMergePushFailurePreservesMergeAndRetries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable Git hook fixture")
	}
	r := newLifecycleRepo(t)
	hook := filepath.Join(r.remote, "hooks", "pre-receive")
	mustWrite(t, hook, "#!/bin/sh\necho rejected for test >&2\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	op, err := preflightDirectMerge(r.main, r.feature, "feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	op = runDirectMerge(op)
	if op.Recovery != DirectMergeRecoveryPushFailure || op.MergeOID == "" {
		t.Fatalf("result = recovery %q merge %q error %v", op.Recovery, op.MergeOID, op.Err)
	}
	if head := mustGit(t, r.main, "rev-parse", "HEAD"); head != op.MergeOID {
		t.Fatalf("local merge not preserved: %s != %s", head, op.MergeOID)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	op = retryDirectMergePush(op)
	if op.Err != nil || op.Recovery != DirectMergeRecoveryNone {
		t.Fatalf("retry: %v (%s)", op.Err, op.GitState)
	}
	if remote := mustGit(t, r.main, "rev-parse", "origin/main"); remote != op.MergeOID {
		t.Fatalf("retry did not push merge: %s != %s", remote, op.MergeOID)
	}
}

func TestUpdateCheckedOutBaseUpdatesFilesAndIndexNotOnlyRef(t *testing.T) {
	r := newLifecycleRepo(t)
	clone := filepath.Join(r.root, "publisher")
	mustGit(t, r.root, "clone", r.remote, clone)
	mustGit(t, clone, "config", "user.email", "sidecar-test@example.com")
	mustGit(t, clone, "config", "user.name", "Sidecar Test")
	mustGit(t, clone, "checkout", "main")
	mustWrite(t, filepath.Join(clone, "remote.txt"), "published\n")
	mustGit(t, clone, "add", "remote.txt")
	mustGit(t, clone, "commit", "-m", "remote change")
	mustGit(t, clone, "push", "origin", "main")

	result := updateCheckedOutBase(r.feature, "main", "origin")
	if result.Err != nil || !result.Updated || result.TargetPath != canonicalGitPath(r.main) {
		t.Fatalf("update result: %+v", result)
	}
	if got := strings.TrimSpace(mustRead(t, filepath.Join(r.main, "remote.txt"))); got != "published" {
		t.Fatalf("checked-out files were not updated: %q", got)
	}
	if status := mustGit(t, r.main, "status", "--porcelain"); status != "" {
		t.Fatalf("base checkout index/files desynchronized: %s", status)
	}
}

func TestUpdateCheckedOutBaseWithoutTargetOnlyFetches(t *testing.T) {
	r := newLifecycleRepo(t)
	mainBefore := mustGit(t, r.main, "rev-parse", "refs/heads/main")
	mustGit(t, r.main, "switch", "-c", "parking")
	clone := filepath.Join(r.root, "publisher")
	mustGit(t, r.root, "clone", r.remote, clone)
	mustGit(t, clone, "config", "user.email", "sidecar-test@example.com")
	mustGit(t, clone, "config", "user.name", "Sidecar Test")
	mustGit(t, clone, "checkout", "main")
	mustWrite(t, filepath.Join(clone, "remote-only.txt"), "published\n")
	mustGit(t, clone, "add", "remote-only.txt")
	mustGit(t, clone, "commit", "-m", "remote only")
	mustGit(t, clone, "push", "origin", "main")

	result := updateCheckedOutBase(r.feature, "main", "origin")
	if result.Err != nil || !result.Fetched || !result.LeftUnchanged || result.Updated {
		t.Fatalf("update result: %+v", result)
	}
	if mainAfter := mustGit(t, r.feature, "rev-parse", "refs/heads/main"); mainAfter != mainBefore {
		t.Fatalf("un-checked-out base ref moved behind a checkout: %s -> %s", mainBefore, mainAfter)
	}
	if tracking := mustGit(t, r.feature, "rev-parse", "origin/main"); tracking == mainBefore {
		t.Fatalf("fetch did not update origin/main")
	}
}

func TestUpdateCheckedOutBaseRefusesSameOIDBranchSwitchBeforePull(t *testing.T) {
	r := newLifecycleRepo(t)
	mainBefore := mustGit(t, r.main, "rev-parse", "refs/heads/main")
	clone := filepath.Join(r.root, "publisher-switch")
	mustGit(t, r.root, "clone", r.remote, clone)
	mustGit(t, clone, "config", "user.email", "sidecar-test@example.com")
	mustGit(t, clone, "config", "user.name", "Sidecar Test")
	mustGit(t, clone, "checkout", "main")
	mustWrite(t, filepath.Join(clone, "remote-switch.txt"), "remote update\n")
	mustGit(t, clone, "add", "remote-switch.txt")
	mustGit(t, clone, "commit", "-m", "remote switch update")
	mustGit(t, clone, "push", "origin", "main")
	remoteOID := mustGit(t, clone, "rev-parse", "HEAD")

	result := updateCheckedOutBaseWithBeforePull(r.feature, "main", "origin", func() {
		mustGit(t, r.main, "switch", "-c", "parking")
	})
	if result.Err == nil || !result.LeftUnchanged || !strings.Contains(result.Err.Error(), "expected \"main\"") {
		t.Fatalf("update result: %+v", result)
	}
	if mainAfter := mustGit(t, r.main, "rev-parse", "refs/heads/main"); mainAfter != mainBefore {
		t.Fatalf("local main moved: %s -> %s", mainBefore, mainAfter)
	}
	if parking := mustGit(t, r.main, "rev-parse", "HEAD"); parking != mainBefore {
		t.Fatalf("parking moved: got %s want %s", parking, mainBefore)
	}
	if _, err := os.Stat(filepath.Join(r.main, "remote-switch.txt")); !os.IsNotExist(err) {
		t.Fatalf("remote update was pulled into parking: %v", err)
	}
	if tracking := mustGit(t, r.main, "rev-parse", "origin/main"); tracking != remoteOID {
		t.Fatalf("fetch result origin/main = %s, want %s", tracking, remoteOID)
	}
}

func TestCleanupRunsFromSurvivingCheckoutAndRevalidatesIdentity(t *testing.T) {
	r := newLifecycleRepo(t)
	mustGit(t, r.main, "merge", "--ff-only", "feature")
	expected := mustGit(t, r.feature, "rev-parse", "HEAD")
	results := runCleanupPlan(CleanupPlan{
		RepoPath: r.main, WorktreePath: r.feature, Branch: "feature", ExpectedOID: expected,
		DeleteWorktree: true, DeleteBranch: true,
	})
	if len(results.Errors) > 0 || !results.LocalWorktreeDeleted || !results.LocalBranchDeleted {
		t.Fatalf("cleanup result: %+v", results)
	}
	if _, err := os.Stat(r.feature); !os.IsNotExist(err) {
		t.Fatalf("feature worktree still exists: %v", err)
	}
	if branch := mustGit(t, r.main, "branch", "--list", "feature"); branch != "" {
		t.Fatalf("feature branch still exists: %q", branch)
	}
}

func TestCleanupDirtyWorktreeRefusesWithoutForceOrOtherDeletion(t *testing.T) {
	r := newLifecycleRepo(t)
	mustGit(t, r.feature, "push", "-u", "origin", "feature")
	localOID := mustGit(t, r.feature, "rev-parse", "HEAD")
	remoteOID, err := remoteBranchOID(r.main, "origin", "feature")
	if err != nil {
		t.Fatal(err)
	}
	valuablePath := filepath.Join(r.feature, "valuable.txt")
	mustWrite(t, valuablePath, "irreplaceable untracked work\n")

	results := runCleanupPlan(CleanupPlan{
		RepoPath: r.main, WorktreePath: r.feature, Branch: "feature", ExpectedOID: localOID,
		BranchRemote: "origin", ExpectedRemoteOID: remoteOID,
		DeleteWorktree: true, DeleteBranch: true, DeleteRemote: true,
	})
	if len(results.Errors) == 0 || !strings.Contains(strings.Join(results.Errors, "\n"), "dirty") {
		t.Fatalf("cleanup result = %+v, want dirty refusal", results)
	}
	if results.LocalWorktreeDeleted || results.LocalBranchDeleted || results.RemoteBranchDeleted {
		t.Fatalf("dirty refusal reported destructive cleanup: %+v", results)
	}
	if got := mustRead(t, valuablePath); got != "irreplaceable untracked work\n" {
		t.Fatalf("valuable untracked file changed: %q", got)
	}
	if branchOID := mustGit(t, r.main, "rev-parse", "refs/heads/feature"); branchOID != localOID {
		t.Fatalf("local feature changed/deleted: got %s want %s", branchOID, localOID)
	}
	remoteAfter, err := remoteBranchOID(r.main, "origin", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if remoteAfter != remoteOID {
		t.Fatalf("remote feature changed/deleted: got %s want %s", remoteAfter, remoteOID)
	}
	worktrees, err := readGitWorktrees(r.main)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, wt := range worktrees {
		if wt.Path == canonicalGitPath(r.feature) && wt.Branch == "feature" {
			found = true
		}
	}
	if !found {
		t.Fatal("dirty feature worktree is no longer registered")
	}
}

func TestCleanupRefusesRemoteDeleteWhenBranchAdvanced(t *testing.T) {
	r := newLifecycleRepo(t)
	mustGit(t, r.feature, "push", "-u", "origin", "feature")
	expectedRemoteOID, err := remoteBranchOID(r.main, "origin", "feature")
	if err != nil {
		t.Fatal(err)
	}
	localOID := mustGit(t, r.feature, "rev-parse", "HEAD")
	plan := CleanupPlan{
		RepoPath: r.main, WorktreePath: r.feature, Branch: "feature", ExpectedOID: localOID,
		BranchRemote: "origin", ExpectedRemoteOID: expectedRemoteOID, DeleteRemote: true,
	}

	publisher := filepath.Join(r.root, "publisher-feature")
	mustGit(t, r.root, "clone", r.remote, publisher)
	mustGit(t, publisher, "config", "user.email", "sidecar-test@example.com")
	mustGit(t, publisher, "config", "user.name", "Sidecar Test")
	mustGit(t, publisher, "checkout", "feature")
	mustWrite(t, filepath.Join(publisher, "remote-advance.txt"), "new remote work\n")
	mustGit(t, publisher, "add", "remote-advance.txt")
	mustGit(t, publisher, "commit", "-m", "advance remote feature")
	mustGit(t, publisher, "push", "origin", "feature")
	advancedOID := mustGit(t, publisher, "rev-parse", "HEAD")

	results := runCleanupPlan(plan)
	if len(results.Errors) == 0 || !strings.Contains(strings.Join(results.Errors, "\n"), "changed since cleanup was confirmed") {
		t.Fatalf("cleanup result = %+v, want remote identity refusal", results)
	}
	if results.RemoteBranchDeleted {
		t.Fatal("advanced remote branch reported deleted")
	}
	remoteAfter, err := remoteBranchOID(r.main, "origin", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if remoteAfter != advancedOID {
		t.Fatalf("advanced remote branch was changed/deleted: got %s want %s", remoteAfter, advancedOID)
	}
}

func TestCleanupDeletesUnchangedRemoteBranchWithLease(t *testing.T) {
	r := newLifecycleRepo(t)
	mustGit(t, r.feature, "push", "-u", "origin", "feature")
	expectedRemoteOID, err := remoteBranchOID(r.main, "origin", "feature")
	if err != nil {
		t.Fatal(err)
	}
	results := runCleanupPlan(CleanupPlan{
		RepoPath: r.main, WorktreePath: r.feature, Branch: "feature",
		BranchRemote: "origin", ExpectedRemoteOID: expectedRemoteOID, DeleteRemote: true,
	})
	if len(results.Errors) > 0 || !results.RemoteBranchDeleted {
		t.Fatalf("leased remote deletion result: %+v", results)
	}
	remoteAfter, err := remoteBranchOID(r.main, "origin", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if remoteAfter != "" {
		t.Fatalf("remote feature still exists at %s", remoteAfter)
	}
}

func TestWorktreeActionRefusal(t *testing.T) {
	tests := []struct {
		name string
		wt   *Worktree
		want string
	}{
		{"main", &Worktree{IsMain: true}, "main"},
		{"detached", &Worktree{Path: t.TempDir(), IsDetached: true}, "branch"},
		{"bare", &Worktree{Path: t.TempDir(), IsBare: true}, "bare"},
		{"locked", &Worktree{Path: t.TempDir(), Branch: "feature", IsLocked: true}, "locked"},
		{"missing", &Worktree{Path: filepath.Join(t.TempDir(), "gone"), Branch: "feature", IsMissing: true}, "missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WorktreeActionRefusal(tt.wt, WorktreeActionMerge); !strings.Contains(got, tt.want) {
				t.Fatalf("refusal %q does not contain %q", got, tt.want)
			}
		})
	}
}

func TestCommandsHideUnsafeActionsAndExposeRecovery(t *testing.T) {
	unsafe := &Worktree{Name: "main", IsMain: true}
	p := &Plugin{worktrees: []*Worktree{unsafe}, selectedIdx: 0, viewMode: ViewModeList}
	ids := commandIDs(p.Commands())
	for _, id := range []string{"delete-workspace", "push", "merge-workflow"} {
		if ids[id] {
			t.Fatalf("unsafe command %q was advertised for main worktree", id)
		}
	}

	p.viewMode = ViewModeMerge
	p.mergeState = &MergeWorkflowState{
		Worktree: &Worktree{Name: "feature"}, Step: MergeStepError,
		DirectOperation: &DirectMergeOperation{Recovery: DirectMergeRecoveryConflict},
	}
	ids = commandIDs(p.Commands())
	if !ids["continue-merge"] || !ids["abort-merge"] || ids["retry-push"] {
		t.Fatalf("conflict recovery commands = %v", ids)
	}
	p.mergeState.DirectOperation.Recovery = DirectMergeRecoveryPushFailure
	ids = commandIDs(p.Commands())
	if !ids["retry-push"] || ids["continue-merge"] || ids["abort-merge"] {
		t.Fatalf("push recovery commands = %v", ids)
	}
}

func commandIDs(commands []plugin.Command) map[string]bool {
	ids := make(map[string]bool, len(commands))
	for _, command := range commands {
		ids[command.ID] = true
	}
	return ids
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
	}
	return strings.TrimSpace(string(out))
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
