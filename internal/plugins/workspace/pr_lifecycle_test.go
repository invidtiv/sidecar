package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/projectdir"
)

func installFakeGH(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gh")
	script := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func configureGitHubRemoteAlias(t *testing.T, repo, remote, repository, actualURL string) {
	t.Helper()
	githubURL := "https://github.com/" + repository + ".git"
	mustGit(t, repo, "config", "url."+actualURL+".insteadOf", githubURL)
	mustGit(t, repo, "remote", "set-url", remote, githubURL)
}

func TestExistingPRQueryUsesStructuredForkAndBaseIdentity(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_GH_LOG", log)
	installFakeGH(t, `
printf '%s\n' "$*" > "$FAKE_GH_LOG"
case "$*" in *'--head '*:*) echo 'unsupported owner:branch --head' >&2; exit 9;; esac
printf '%s\n' '[{"number":41,"url":"wrong","id":"wrong","headRefName":"topic","headRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","baseRefName":"release/2","state":"OPEN","mergedAt":"","headRepository":{"nameWithOwner":"other/repo"},"headRepositoryOwner":{"login":"other"},"mergeCommit":null},{"number":42,"url":"https://github.com/base/repo/pull/42","id":"PR_node","headRefName":"topic","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefName":"release/2","state":"OPEN","mergedAt":"","headRepository":{"nameWithOwner":"fork/repo"},"headRepositoryOwner":{"login":"fork"},"mergeCommit":null}]'
`)
	id, err := queryExistingPRContext(context.Background(), t.TempDir(), "base/repo", "fork", "topic", "release/2")
	if err != nil {
		t.Fatal(err)
	}
	if id == nil || id.Number != 42 || id.NodeID != "PR_node" || id.HeadRepo != "fork/repo" || id.BaseRef != "release/2" {
		t.Fatalf("identity = %+v", id)
	}
	args, _ := os.ReadFile(log)
	want := []string{"--state all", "--head topic", "--base release/2", "--json number,url,id"}
	for _, s := range want {
		if !strings.Contains(string(args), s) {
			t.Fatalf("gh args %q missing %q", args, s)
		}
	}
}

func TestImportPRUsesPullRefForSameRepoAndFork(t *testing.T) {
	for _, tc := range []struct {
		name, headRepo, owner string
		collision             bool
	}{
		{"same-repo", "base/repo", "base", false}, {"fork", "contributor/repo", "contributor", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			repo := filepath.Join(root, "repo")
			bare := filepath.Join(root, "remote.git")
			mustGit(t, root, "init", "--bare", bare)
			mustGit(t, root, "init", "-b", "main", repo)
			mustGit(t, repo, "config", "user.email", "test@example.com")
			mustGit(t, repo, "config", "user.name", "Test")
			mustWrite(t, filepath.Join(repo, "base"), "base\n")
			mustGit(t, repo, "add", "base")
			mustGit(t, repo, "commit", "-m", "base")
			mustGit(t, repo, "remote", "add", "origin", "https://github.com/base/repo.git")
			mustGit(t, repo, "config", "url."+bare+".insteadOf", "https://github.com/base/repo.git")
			mustGit(t, repo, "push", "-u", "origin", "main")
			mustGit(t, repo, "checkout", "-b", "topic")
			mustWrite(t, filepath.Join(repo, "topic"), tc.name+"\n")
			mustGit(t, repo, "add", "topic")
			mustGit(t, repo, "commit", "-m", "topic")
			head := mustGit(t, repo, "rev-parse", "HEAD")
			mustGit(t, repo, "push", "origin", "HEAD:refs/pull/12/head")
			mustGit(t, repo, "checkout", "main")
			if tc.collision {
				mustGit(t, repo, "branch", "-f", "topic", "main")
			} else {
				mustGit(t, repo, "branch", "-D", "topic")
			}
			config.SetTestStateDir(filepath.Join(root, "state"))
			t.Cleanup(config.ResetTestStateDir)
			p := New()
			if err := p.Init(&plugin.Context{Epoch: 2, WorkDir: repo, ProjectRoot: repo}); err != nil {
				t.Fatal(err)
			}
			pr := PRListItem{Number: 12, NodeID: "node12", Branch: "topic", HeadOID: head, BaseBranch: "main", Repository: "base/repo", URL: "https://github.com/base/repo/pull/12", HeadRepo: ghRepository{NameWithOwner: tc.headRepo}, HeadOwner: ghOwner{Login: tc.owner}}
			msg := p.fetchAndCreateWorktree(pr)().(FetchPRDoneMsg)
			if msg.Err != nil {
				t.Fatal(msg.Err)
			}
			if got := mustGit(t, msg.Worktree.Path, "rev-parse", "HEAD"); got != head {
				t.Fatalf("imported HEAD = %s, want %s", got, head)
			}
			if tc.collision {
				if msg.Worktree.Branch != "topic-pr-12" {
					t.Fatalf("collision branch = %q", msg.Worktree.Branch)
				}
				if got := mustGit(t, repo, "rev-parse", "topic"); got == head {
					t.Fatal("unrelated local topic branch was overwritten")
				}
			}
			if msg.Worktree.BaseBranch != "main" {
				t.Fatalf("base = %q", msg.Worktree.BaseBranch)
			}
			stored := loadPRIdentityContext(context.Background(), repo, msg.Worktree.Path)
			if stored.Number != 12 || stored.HeadRepo != tc.headRepo || stored.BaseRef != "main" {
				t.Fatalf("stored identity = %+v", stored)
			}
			cmd := exec.Command("git", "for-each-ref", "--format=%(refname)", "refs/sidecar/pr/12")
			cmd.Dir = repo
			if out, err := cmd.Output(); err != nil || len(out) != 0 {
				t.Fatalf("temporary PR refs retained: %q (%v)", out, err)
			}
		})
	}
}

func TestPollPRStableIdentityAndTerminalStates(t *testing.T) {
	tests := []struct {
		name, body string
		want       PRPollKind
	}{
		{"open", `printf '%s\n' '{"number":7,"url":"u","id":"node7","headRefName":"topic","headRefOid":"a","baseRefName":"main","state":"OPEN","mergedAt":"","headRepository":{"nameWithOwner":"o/r"},"headRepositoryOwner":{"login":"o"},"mergeCommit":null}'`, PRPollOpen},
		{"merged", `printf '%s\n' '{"number":7,"url":"u","id":"node7","headRefName":"topic","headRefOid":"a","baseRefName":"main","state":"MERGED","mergedAt":"now","headRepository":{"nameWithOwner":"o/r"},"headRepositoryOwner":{"login":"o"},"mergeCommit":{"oid":"m"}}'`, PRPollMerged},
		{"closed", `printf '%s\n' '{"number":7,"url":"u","id":"node7","headRefName":"topic","headRefOid":"a","baseRefName":"main","state":"CLOSED","mergedAt":"","headRepository":{"nameWithOwner":"o/r"},"headRepositoryOwner":{"login":"o"},"mergeCommit":null}'`, PRPollClosed},
		{"auth", `echo 'authentication required' >&2; exit 1`, PRPollAuth},
		{"network", `echo 'could not resolve host github.com' >&2; exit 1`, PRPollNetwork},
		{"repository", `echo 'Could not resolve to a Repository' >&2; exit 1`, PRPollRepository},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installFakeGH(t, tt.body)
			got := pollPRContext(context.Background(), t.TempDir(), PRIdentity{Number: 7, NodeID: "node7", Repository: "o/r"})
			if got.Kind != tt.want {
				t.Fatalf("kind = %q, want %q (%v)", got.Kind, tt.want, got.Err)
			}
		})
	}
	if nextPRPollDelay(99) != 2*time.Minute {
		t.Fatalf("backoff is not capped: %v", nextPRPollDelay(99))
	}
}

func TestPushForMergePinsReviewedOIDAndRejectsMovedHEAD(t *testing.T) {
	r := newLifecycleRepo(t)
	configureGitHubRemoteAlias(t, r.feature, "origin", "base/repo", r.remote)
	wt := &Worktree{Key: "feature", RepoKey: "repo", Name: "feature", Path: r.feature, Branch: "feature"}
	p := New()
	if err := p.Init(&plugin.Context{Epoch: 1, WorkDir: r.feature, ProjectRoot: r.main}); err != nil {
		t.Fatal(err)
	}
	p.worktrees = []*Worktree{wt}
	p.newLifecycleScope(wt)
	reviewed := mustGit(t, r.feature, "rev-parse", "HEAD")
	p.mergeState = &MergeWorkflowState{OperationScope: p.lifecycleScope(wt), Worktree: wt, ReviewedOID: reviewed, TargetBranch: "main"}
	msg := p.pushForMerge(wt)().(MergeStepCompleteMsg)
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	if got := mustGit(t, r.main, "ls-remote", r.remote, "refs/heads/feature"); !strings.HasPrefix(got, reviewed) {
		t.Fatalf("remote feature = %q, want %s", got, reviewed)
	}
	mustWrite(t, filepath.Join(r.feature, "moved.txt"), "moved\n")
	mustGit(t, r.feature, "add", "moved.txt")
	mustGit(t, r.feature, "commit", "-m", "moved")
	msg = p.pushForMerge(wt)().(MergeStepCompleteMsg)
	if msg.Err == nil || !strings.Contains(msg.Err.Error(), "HEAD changed") {
		t.Fatalf("moved HEAD error = %v", msg.Err)
	}
}

func TestCreatePRUsesEditedFieldsAndStructuralExistingQuery(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_GH_LOG", log)
	r := newLifecycleRepo(t)
	configureGitHubRemoteAlias(t, r.feature, "origin", "base/repo", r.remote)
	mustGit(t, r.feature, "config", "branch.release.remote", "origin")
	mustGit(t, r.feature, "push", "-u", "origin", "feature")
	reviewed := mustGit(t, r.feature, "rev-parse", "HEAD")
	t.Setenv("FAKE_REVIEWED_OID", reviewed)
	installFakeGH(t, `
printf '%s\n' "$*" >> "$FAKE_GH_LOG"
case "$*" in
  'repo view --json nameWithOwner') echo '{"nameWithOwner":"base/repo"}' ;;
  'pr list '*) echo '[]' ;;
  'pr create '*) echo 'https://github.com/base/repo/pull/9' ;;
  'pr view '*) printf '{"number":9,"url":"https://github.com/base/repo/pull/9","id":"node9","headRefName":"feature","headRefOid":"%s","baseRefName":"release","state":"OPEN","mergedAt":"","headRepository":{"nameWithOwner":"fork/repo"},"headRepositoryOwner":{"login":"fork"},"mergeCommit":null}\n' "$FAKE_REVIEWED_OID" ;;
esac
`)
	wt := &Worktree{Name: "feature", Path: r.feature, Branch: "feature"}
	p := New()
	p.operationCtx = context.Background()
	p.mergeState = &MergeWorkflowState{Worktree: wt, ReviewedOID: reviewed, PushRemote: "origin", PR: PRIdentity{HeadRef: "feature", HeadOwner: "fork", HeadOID: reviewed}}
	msg := p.createPR(wt, "edited title", "edited body", "release")().(MergeStepCompleteMsg)
	if msg.Err != nil || msg.PR.Number != 9 {
		t.Fatalf("create result = %+v", msg)
	}
	args, _ := os.ReadFile(log)
	for _, want := range []string{"pr list --state all", "--head feature", "--base release", "pr create --repo base/repo --title edited title --body edited body"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("gh calls missing %q:\n%s", want, args)
		}
	}
}

func TestCreatePRRefusesLocalOrRemoteChangeDuringEdit(t *testing.T) {
	for _, change := range []string{"local", "remote"} {
		t.Run(change, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "args")
			t.Setenv("FAKE_GH_LOG", log)
			installFakeGH(t, `
printf '%s\n' "$*" >> "$FAKE_GH_LOG"
case "$*" in
  'repo view --json nameWithOwner') echo '{"nameWithOwner":"base/repo"}' ;;
  'pr list '*) echo '[]' ;;
  'pr create '*) echo 'SHOULD_NOT_CREATE' ;;
esac`)
			r := newLifecycleRepo(t)
			configureGitHubRemoteAlias(t, r.feature, "origin", "base/repo", r.remote)
			mustGit(t, r.feature, "push", "-u", "origin", "feature")
			reviewed := mustGit(t, r.feature, "rev-parse", "HEAD")
			if change == "local" {
				mustWrite(t, filepath.Join(r.feature, "later"), "later\n")
				mustGit(t, r.feature, "add", "later")
				mustGit(t, r.feature, "commit", "-m", "later")
			} else {
				mustWrite(t, filepath.Join(r.feature, "remote-later"), "later\n")
				mustGit(t, r.feature, "add", "remote-later")
				mustGit(t, r.feature, "commit", "-m", "later")
				mustGit(t, r.feature, "push", "origin", "feature")
				mustGit(t, r.feature, "reset", "--hard", reviewed)
			}
			wt := &Worktree{Name: "feature", Path: r.feature, Branch: "feature"}
			p := New()
			p.operationCtx = context.Background()
			p.mergeState = &MergeWorkflowState{Worktree: wt, ReviewedOID: reviewed, PushRemote: "origin", PR: PRIdentity{HeadRef: "feature", HeadOID: reviewed}}
			msg := p.createPR(wt, "edited", "body", "main")().(MergeStepCompleteMsg)
			if !errors.Is(msg.Err, errReviewedSourceChanged) {
				t.Fatalf("change refusal = %v", msg.Err)
			}
			calls, _ := os.ReadFile(log)
			if strings.Contains(string(calls), "pr create") {
				t.Fatalf("gh pr create ran after %s change:\n%s", change, calls)
			}
		})
	}
}

func TestForkTopologyUsesBaseRemoteRepositoryAndHeadRemoteOwner(t *testing.T) {
	r := newLifecycleRepo(t)
	mustGit(t, r.feature, "remote", "rename", "origin", "upstream")
	configureGitHubRemoteAlias(t, r.feature, "upstream", "upstream/repo", r.remote)
	forkRemote := filepath.Join(r.root, "fork.git")
	mustGit(t, r.root, "init", "--bare", forkRemote)
	mustGit(t, r.feature, "remote", "add", "origin", "https://github.com/me/repo.git")
	mustGit(t, r.feature, "config", "url."+forkRemote+".insteadOf", "https://github.com/me/repo.git")
	mustGit(t, r.feature, "config", "branch.feature.remote", "origin")
	mustGit(t, r.feature, "config", "branch.main.remote", "upstream")
	reviewed := mustGit(t, r.feature, "rev-parse", "HEAD")
	log := filepath.Join(t.TempDir(), "gh-args")
	t.Setenv("FAKE_GH_LOG", log)
	t.Setenv("FAKE_REVIEWED_OID", reviewed)
	installFakeGH(t, `
printf '%s\n' "$*" >> "$FAKE_GH_LOG"
case "$*" in
  'repo view '*) echo 'bare repo inference forbidden' >&2; exit 9 ;;
  'pr list '*) echo '[]' ;;
  'pr create '*) echo 'https://github.com/upstream/repo/pull/17' ;;
  'pr view '*) printf '{"number":17,"url":"https://github.com/upstream/repo/pull/17","id":"node17","headRefName":"feature","headRefOid":"%s","baseRefName":"main","state":"OPEN","mergedAt":"","headRepository":{"nameWithOwner":"me/repo"},"headRepositoryOwner":{"login":"me"},"mergeCommit":null}\n' "$FAKE_REVIEWED_OID" ;;
  *) echo "unexpected gh args: $*" >&2; exit 8 ;;
esac`)
	wt := &Worktree{Key: "feature", RepoKey: "repo", Name: "feature", Path: r.feature, Branch: "feature"}
	p := New()
	if err := p.Init(&plugin.Context{Epoch: 3, WorkDir: r.feature, ProjectRoot: r.main}); err != nil {
		t.Fatal(err)
	}
	p.worktrees = []*Worktree{wt}
	p.newLifecycleScope(wt)
	p.mergeState = &MergeWorkflowState{OperationScope: p.lifecycleScope(wt), Worktree: wt, Step: MergeStepPush, ReviewedOID: reviewed, TargetBranch: "main", StepStatus: map[MergeWorkflowStep]string{}}
	push := p.pushForMerge(wt)().(MergeStepCompleteMsg)
	if push.Err != nil {
		t.Fatal(push.Err)
	}
	if push.Data != "origin" || push.BaseRemote != "upstream" || push.PR.Repository != "upstream/repo" || push.PR.HeadRepo != "me/repo" || push.PR.HeadOwner != "me" {
		t.Fatalf("resolved topology = %+v push=%q base=%q", push.PR, push.Data, push.BaseRemote)
	}
	p.mergeState.PushRemote, p.mergeState.BaseRemote, p.mergeState.PR = push.Data, push.BaseRemote, push.PR
	created := p.createPR(wt, "fork title", "fork body", "main")().(MergeStepCompleteMsg)
	if created.Err != nil || created.PR.Repository != "upstream/repo" {
		t.Fatalf("create = %+v", created)
	}
	calls, _ := os.ReadFile(log)
	for _, want := range []string{"pr list --state all --head feature --base main", "--repo upstream/repo", "pr create --repo upstream/repo", "--head me:feature", "--base main"} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("fork gh calls missing %q:\n%s", want, calls)
		}
	}
	if strings.Contains(string(calls), "repo view") {
		t.Fatalf("used current-directory repository inference:\n%s", calls)
	}
}

func TestGitHubRemoteParsingAndTemporaryRefs(t *testing.T) {
	for input, want := range map[string]string{
		"git@github.com:owner/repo.git":       "owner/repo",
		"ssh://git@github.com/owner/repo.git": "owner/repo",
		"https://github.com/owner/repo.git":   "owner/repo",
	} {
		got, err := parseGitHubRepositoryURL(input)
		if err != nil || got != want {
			t.Fatalf("parse %q = %q, %v", input, got, err)
		}
	}
	if _, err := parseGitHubRepositoryURL("/tmp/repo.git"); err == nil {
		t.Fatal("local non-GitHub remote was accepted")
	}
	a, err := newTemporaryPRRefPrefix(9)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newTemporaryPRRefPrefix(9)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || !strings.HasPrefix(a, "refs/sidecar/pr/9/") || !strings.HasPrefix(b, "refs/sidecar/pr/9/") {
		t.Fatalf("temporary refs are not independently unique: %q %q", a, b)
	}
}

func TestBaseTopologyRejectsLocalOrNonGitHubRemote(t *testing.T) {
	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	for _, tc := range []struct {
		name, remote, remoteURL string
	}{
		{name: "local-dot", remote: "."},
		{name: "non-github", remote: "origin", remoteURL: "/tmp/local.git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustGit(t, repo, "config", "branch.main.remote", tc.remote)
			if tc.remoteURL != "" {
				mustGit(t, repo, "config", "remote."+tc.remote+".url", tc.remoteURL)
			}
			if _, _, err := resolveBaseTopologyContext(context.Background(), repo, "main"); err == nil {
				t.Fatalf("base remote %q (%q) was accepted", tc.remote, tc.remoteURL)
			}
		})
	}
}

func TestSquashMergeRequiresExplicitForceDeletion(t *testing.T) {
	r := newLifecycleRepo(t)
	reviewed := mustGit(t, r.feature, "rev-parse", "HEAD")
	mustGit(t, r.main, "merge", "--squash", reviewed)
	mustGit(t, r.main, "commit", "-m", "squash feature")
	mergeOID := mustGit(t, r.main, "rev-parse", "HEAD")
	mustGit(t, r.main, "push", "origin", "main")
	identity := PRIdentity{Number: 1, State: "MERGED", HeadOID: reviewed, BaseRef: "main", MergeOID: mergeOID}
	required, err := validateMergedPRForCleanupContext(context.Background(), r.feature, reviewed, "main", identity)
	if err != nil {
		t.Fatal(err)
	}
	if !required {
		t.Fatal("squash merge did not require an explicit force deletion")
	}
	if err := deleteBranchAfterMergeContext(context.Background(), r.main, "feature", false); err == nil {
		t.Fatal("ordinary -d unexpectedly deleted squash-merged branch")
	}
	if got := mustGit(t, r.main, "rev-parse", "refs/heads/feature"); got != reviewed {
		t.Fatalf("branch changed after -d refusal: %s", got)
	}
}

func TestPRDraftIsOptInEditableAndBounded(t *testing.T) {
	wt := &Worktree{Name: "topic", Path: t.TempDir(), Branch: "topic", ChosenAgentType: AgentClaude}
	p := New()
	p.width, p.height = 60, 24
	p.operationCtx = context.Background()
	p.viewMode = ViewModeMerge
	p.mergeState = &MergeWorkflowState{Worktree: wt, Step: MergeStepGeneratePR, StepStatus: map[MergeWorkflowStep]string{MergeStepGeneratePR: "running"}}
	p.ensureMergeModal()
	rendered := p.mergeModal.Render(60, 24, p.mouseHandler)
	plain := ansi.Strip(rendered)
	for _, want := range []string{"Commit Summary", "Use Agent", "external provider"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("draft modal missing %q:\n%s", want, plain)
		}
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 60 {
			t.Fatalf("modal line width %d > 60: %q", ansi.StringWidth(line), ansi.Strip(line))
		}
	}
	if len(strings.Split(rendered, "\n")) > 24 {
		t.Fatalf("modal height exceeds allocation: %d", len(strings.Split(rendered, "\n")))
	}
	cmd := p.handleMergeKeys(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || !p.mergeState.PRGenerationActive {
		t.Fatal("default local draft action did not start")
	}

	p.mergeState.PRGenerationActive = false
	p.mergeState.PRTitleInput = textinput.New()
	p.mergeState.PRTitleInput.SetValue("generated")
	p.mergeState.PRBodyInput = textarea.New()
	p.mergeState.PRBodyInput.SetValue("generated body")
	p.mergeState.Step = MergeStepEditPR
	p.clearMergeModal()
	if !p.ConsumesTextInput() {
		t.Fatal("edit step does not claim text input")
	}
	p.ensureMergeModal()
	p.mergeModal.Render(60, 24, p.mouseHandler)
	p.mergeModal.SetFocus("merge-pr-body")
	p.mergeModal.Render(60, 24, p.mouseHandler)
	beforeBody := p.mergeState.PRBodyInput.Value()
	if cmd := p.handleMergeKeys(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil && p.mergeState.PRBodyInput.Value() == beforeBody {
		t.Fatal("Enter was not routed to the body textarea")
	}
	if p.mergeState.Step != MergeStepEditPR || !strings.Contains(p.mergeState.PRBodyInput.Value(), "\n") {
		t.Fatalf("textarea Enter submitted instead of inserting newline: step=%v body=%q", p.mergeState.Step, p.mergeState.PRBodyInput.Value())
	}
	p.mergeState.PRTitleInput.SetValue("edited title")
	p.mergeState.PRBodyInput.SetValue("edited body")
	createCmd := p.advanceMergeStep()
	if createCmd == nil || p.mergeState.PRTitle != "edited title" || p.mergeState.PRBody != "edited body" || p.mergeState.Step != MergeStepCreatePR {
		t.Fatalf("edited draft not preserved: %+v", p.mergeState)
	}
}

func TestClosedAndUnavailablePollingStopWithoutLosingURL(t *testing.T) {
	wt := &Worktree{Name: "topic", Path: t.TempDir(), Branch: "topic"}
	p := New()
	p.viewMode = ViewModeMerge
	p.mergeState = &MergeWorkflowState{Worktree: wt, Step: MergeStepWaitingMerge, PRURL: "https://github.com/o/r/pull/7", StepStatus: map[MergeWorkflowStep]string{}}
	p.update(CheckPRMergedMsg{WorkspaceName: "topic", Result: PRPollResult{Kind: PRPollClosed, Identity: PRIdentity{Number: 7, URL: p.mergeState.PRURL}}})
	if !p.mergeState.PRWatchStopped || p.mergeState.PRPollKind != PRPollClosed || p.mergeState.PRURL == "" {
		t.Fatalf("closed state lost terminal identity: %+v", p.mergeState)
	}
	if wt.PRState != "closed" {
		t.Fatalf("closed poll worktree state = %q", wt.PRState)
	}
	p.mergeState.PRWatchStopped = false
	for range 5 {
		p.update(CheckPRMergedMsg{WorkspaceName: "topic", Result: PRPollResult{Kind: PRPollNetwork, Err: context.DeadlineExceeded}})
	}
	if !p.mergeState.PRWatchStopped || p.mergeState.PRURL == "" {
		t.Fatalf("bounded network failure lost URL: %+v", p.mergeState)
	}
	if wt.PRState != "unavailable" {
		t.Fatalf("network failure worktree state = %q", wt.PRState)
	}
}

func TestWorktreePRMetadataHydratesAcrossRefreshAndRestart(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)

	for _, tc := range []struct {
		name, stored, want string
	}{
		{name: "open", stored: "OPEN", want: "open"},
		{name: "merged", stored: "MERGED", want: "merged"},
		{name: "closed", stored: "CLOSED", want: "closed"},
		{name: "unknown structured state", stored: "", want: "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "repo")
			worktree := filepath.Join(root, "worktrees", tc.name)
			if err := os.MkdirAll(worktree, 0755); err != nil {
				t.Fatal(err)
			}
			identity := PRIdentity{Number: 9, URL: "https://github.com/o/r/pull/9", Repository: "o/r", State: tc.stored}
			if err := savePRIdentityContext(context.Background(), root, worktree, identity); err != nil {
				t.Fatal(err)
			}

			for _, phase := range []string{"refresh", "restart"} {
				wt := &Worktree{Path: worktree}
				hydrateWorktreePRMetadata(context.Background(), root, wt)
				if wt.PRURL != identity.URL || wt.PRState != tc.want {
					t.Fatalf("%s metadata = url %q state %q, want %q %q", phase, wt.PRURL, wt.PRState, identity.URL, tc.want)
				}
				labels := strings.Join((&Plugin{}).worktreeStateLabels(wt), " | ")
				if !strings.Contains(labels, "PR "+tc.want) {
					t.Fatalf("%s labels = %q, missing state %q", phase, labels, tc.want)
				}
			}
		})
	}

	t.Run("legacy URL never defaults open", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		worktree := filepath.Join(root, "worktrees", "legacy")
		if err := os.MkdirAll(worktree, 0755); err != nil {
			t.Fatal(err)
		}
		wtDir, err := projectdir.WorktreeDir(root, worktree)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wtDir, sidecarPRFile), []byte("https://github.com/o/r/pull/10\n"), 0644); err != nil {
			t.Fatal(err)
		}
		wt := &Worktree{Path: worktree}
		hydrateWorktreePRMetadata(context.Background(), root, wt)
		if wt.PRState != "unavailable" {
			t.Fatalf("legacy URL state = %q, want unavailable", wt.PRState)
		}
		labels := strings.Join((&Plugin{}).worktreeStateLabels(wt), " | ")
		if strings.Contains(labels, "PR open") || !strings.Contains(labels, "PR unavailable") {
			t.Fatalf("legacy URL labels inferred stale state: %q", labels)
		}
	})
}

func TestMovedHEADResultReturnsWorkflowToReview(t *testing.T) {
	wt := &Worktree{Name: "topic", Path: t.TempDir(), Branch: "topic"}
	p := New()
	p.operationCtx = context.Background()
	p.viewMode = ViewModeMerge
	p.mergeState = &MergeWorkflowState{Worktree: wt, Step: MergeStepCreatePR, ReviewedOID: strings.Repeat("a", 40), StepStatus: map[MergeWorkflowStep]string{}}
	_, cmd := p.update(MergeStepCompleteMsg{WorkspaceName: "topic", Step: MergeStepCreatePR, Err: fmt.Errorf("%w: remote moved", errReviewedSourceChanged)})
	if p.mergeState.Step != MergeStepReviewDiff || p.mergeState.ReviewedOID != "" || cmd == nil {
		t.Fatalf("moved HEAD did not return to review: %+v", p.mergeState)
	}
}
