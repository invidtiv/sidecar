package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/reposervice"
	"github.com/marcus/sidecar/internal/shellstate"
)

// The `sidecar repo` verbs are the host half of a viewer's Git pane. What these
// tests hold to is the machine contract: the JSON a viewer decodes, the exit
// code it branches on, and the refusal it must get rather than a guess. A verb
// that answered plausibly with the wrong patch, or that failed where it should
// have said "not a git repository", would leave a viewer showing one machine's
// work under another machine's name.

func TestRepoCommandsAreReadOnly(t *testing.T) {
	root := RootCommand().FindSubcommand("repo")
	if root == nil {
		t.Fatal("repo command is not registered")
	}
	if root.Mutates {
		t.Fatal("repo mutates")
	}
	for _, sub := range root.Sub {
		if sub.Mutates {
			t.Errorf("repo %s mutates", sub.Name)
		}
	}
}

// Decision 3 of docs/plans/active/remote-git-plugin.md lives in the help text,
// because the way this verb family goes wrong is being adopted as a git CLI.
func TestRepoHelpSaysItIsNotAGitCLI(t *testing.T) {
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"repo", "--help"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("help = %v %d %q", handled, code, errOut.String())
	}
	if !strings.Contains(out.String(), "not a git CLI") {
		t.Fatalf("help = %q", out.String())
	}
}

func TestRepoUsageErrors(t *testing.T) {
	id := "x:worktree:y"
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"repo", "nope"}, "unknown repo command"},
		{[]string{"repo", "status"}, "--workspace is required"},
		{[]string{"repo", "status", "--workspace", id, "extra"}, "takes no positional arguments"},
		{[]string{"repo", "diff", "--workspace", id, "--json"}, "--path is required"},
		{[]string{"repo", "diff", "--workspace", id, "--path", "a.txt", "--json"}, "--mode is required"},
		{[]string{"repo", "diff", "--workspace", id, "--path", "a.txt", "--mode", "commit", "--json"}, "--commit is required for --mode commit"},
		{[]string{"repo", "diff", "--workspace", id, "--path", "a.txt", "--mode", "staged", "--commit", "abc", "--json"}, "--commit is only valid with --mode commit"},
		{[]string{"repo", "commit", "--workspace", id, "--json"}, "--commit is required"},
		{[]string{"repo", "history", "--workspace", id, "--limit", "many"}, "--limit must be an integer"},
		{[]string{"repo", "diff", "--workspace", id, "--path", "a.txt", "--mode", "sideways", "--json"}, "unknown diff mode"},
		// A flag that belongs to another sub-verb is a usage error, not a
		// silently ignored argument: a viewer that sent it asked for something
		// this verb is not answering.
		{[]string{"repo", "status", "--workspace", id, "--mode", "staged"}, "unknown option"},
		{[]string{"repo", "refs", "--workspace", id, "--cursor", "abc"}, "unknown option"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			handled, code := Run(tt.args, &out, &errOut)
			if !handled || code != 2 {
				t.Fatalf("Run = %v %d, want exit 2; stderr %q", handled, code, errOut.String())
			}
			if !strings.Contains(out.String()+errOut.String(), tt.want) {
				t.Fatalf("output %q missing %q", out.String()+errOut.String(), tt.want)
			}
		})
	}
}

func TestRepoStatusJSON(t *testing.T) {
	_, id, cfgPath := setupRepoCLI(t)

	var result reposervice.StatusResult
	runRepoJSON(t, cfgPath, &result, "repo", "status", "--workspace", id, "--json")

	if !result.ValidRemoteResult() {
		t.Fatalf("status = %+v", result)
	}
	if result.Branch != "main" || result.Detached {
		t.Fatalf("branch = %q detached=%v", result.Branch, result.Detached)
	}
	byPath := map[string]reposervice.StatusFile{}
	for _, f := range result.Files {
		byPath[f.Path] = f
	}
	// One row per path carrying every sense: tracked.txt is staged and edited
	// again, so it is one file with two patches, and untracked.txt is neither.
	tracked, ok := byPath["tracked.txt"]
	if !ok {
		t.Fatalf("tracked.txt missing from %+v", result.Files)
	}
	if !tracked.Staged || !tracked.Unstaged || tracked.Untracked {
		t.Fatalf("tracked.txt = %+v, want staged and unstaged", tracked)
	}
	untracked, ok := byPath["untracked.txt"]
	if !ok {
		t.Fatalf("untracked.txt missing from %+v", result.Files)
	}
	if !untracked.Untracked || untracked.Staged {
		t.Fatalf("untracked.txt = %+v, want untracked only", untracked)
	}
}

// The staging sense is the whole point of this verb existing: contentservice's
// diff kind cannot express it, and answering with the wrong one is a quiet,
// plausible lie about the host's working tree.
func TestRepoDiffAnswersEachStagingSenseSeparately(t *testing.T) {
	_, id, cfgPath := setupRepoCLI(t)

	var staged, unstaged, untracked reposervice.DiffResult
	runRepoJSON(t, cfgPath, &staged, "repo", "diff", "--workspace", id, "--path", "tracked.txt", "--mode", "staged", "--json")
	runRepoJSON(t, cfgPath, &unstaged, "repo", "diff", "--workspace", id, "--path", "tracked.txt", "--mode", "unstaged", "--json")
	runRepoJSON(t, cfgPath, &untracked, "repo", "diff", "--workspace", id, "--path", "untracked.txt", "--mode", "untracked", "--json")

	if !staged.ValidRemoteResult() || !strings.Contains(staged.Patch, "+STAGED") || strings.Contains(staged.Patch, "+UNSTAGED") {
		t.Fatalf("staged patch = %q", staged.Patch)
	}
	if !strings.Contains(unstaged.Patch, "+UNSTAGED") || strings.Contains(unstaged.Patch, "+STAGED") {
		t.Fatalf("unstaged patch = %q", unstaged.Patch)
	}
	if !strings.Contains(untracked.Patch, "+UNTRACKED") {
		t.Fatalf("untracked patch = %q", untracked.Patch)
	}
	if staged.Patch == unstaged.Patch {
		t.Fatal("the two senses of one path returned the same patch")
	}
}

func TestRepoHistoryCommitAndRefsJSON(t *testing.T) {
	_, id, cfgPath := setupRepoCLI(t)

	var history reposervice.HistoryResult
	runRepoJSON(t, cfgPath, &history, "repo", "history", "--workspace", id, "--json")
	if !history.ValidRemoteResult() || len(history.Commits) == 0 {
		t.Fatalf("history = %+v", history)
	}
	head := history.Commits[0]
	if head.Subject != "base" || head.Hash == "" || head.Author == "" || head.Date.IsZero() {
		t.Fatalf("head row = %+v", head)
	}

	var detail reposervice.CommitResult
	runRepoJSON(t, cfgPath, &detail, "repo", "commit", "--workspace", id, "--commit", head.Hash, "--json")
	if !detail.ValidRemoteResult() || detail.Commit == nil {
		t.Fatalf("commit = %+v", detail)
	}
	if detail.Commit.Hash != head.Hash || len(detail.Commit.Files) == 0 {
		t.Fatalf("commit detail = %+v", detail.Commit)
	}

	var refs reposervice.RefsResult
	runRepoJSON(t, cfgPath, &refs, "repo", "refs", "--workspace", id, "--json")
	if !refs.ValidRemoteResult() {
		t.Fatalf("refs = %+v", refs)
	}
	var current bool
	for _, b := range refs.Branches {
		if b.Name == "main" && b.Current {
			current = true
		}
	}
	if !current {
		t.Fatalf("refs did not report main as current: %+v", refs.Branches)
	}
}

// Exit 5 is "value rejected". It is a distinct code because a viewer branches
// on it: a rejected workspace or a path that escapes the root is a bad request,
// not a host that broke.
func TestRepoRejectsEscapeUnknownWorkspaceAndUnknownCommit(t *testing.T) {
	_, id, cfgPath := setupRepoCLI(t)
	for _, tt := range []struct {
		name string
		args []string
	}{
		{"escape", []string{"repo", "diff", "--workspace", id, "--path", "../../etc/passwd", "--mode", "unstaged", "--json"}},
		{"absolute", []string{"repo", "diff", "--workspace", id, "--path", "/etc/passwd", "--mode", "unstaged", "--json"}},
		{"unknown workspace", []string{"repo", "status", "--workspace", "nope:worktree:/nowhere", "--json"}},
		{"unknown commit", []string{"repo", "commit", "--workspace", id, "--commit", "0123456789abcdef0123456789abcdef01234567", "--json"}},
		// A ref name is not an object name: accepting one would let a viewer's
		// argument reach git's option parser.
		{"ref instead of hash", []string{"repo", "commit", "--workspace", id, "--commit", "main", "--json"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			handled, code := Run(append([]string{"-config", cfgPath}, tt.args...), &out, &errOut)
			if !handled || code != 5 {
				t.Fatalf("Run = %v %d, want exit 5; stdout %q stderr %q", handled, code, out.String(), errOut.String())
			}
		})
	}
}

// A workspace that is not a repository is an answer, not a failure. A viewer
// that got an error here would have nothing to say except "something went
// wrong", and the local fallback it must never take is offering `git init`.
//
// The workspace is a shell rather than a worktree because that is the only
// identity that can name a non-repository at all: contentservice resolves a
// worktree id through `git worktree list`, so a project root without a .git is
// rejected before this service is reached. Rendering that rejection honestly is
// the viewer's job in slice 4g.
func TestRepoAnswersNoRepositoryWithoutFailing(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	plain := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(plain); err == nil {
		plain = resolved
	}
	writeProjectMeta(t, stateDir, "plain", plain)
	writeProjectShell(t, stateDir, "plain", shellstate.Definition{TmuxName: "sidecar-sh-plain-1", DisplayName: "Plain", WorkDir: plain})
	cfgPath := filepath.Join(filepath.Dir(stateDir), "config", "config.json")
	writeContentConfig(t, cfgPath, "plain", plain)

	var result reposervice.StatusResult
	runRepoJSON(t, cfgPath, &result, "repo", "status", "--workspace", plain+":shell:sidecar-sh-plain-1", "--json")
	if !result.NoRepository {
		t.Fatalf("status = %+v, want noRepository", result)
	}
	if len(result.Files) != 0 || result.Branch != "" {
		t.Fatalf("a non-repository reported repository state: %+v", result)
	}
}

func runRepoJSON(t *testing.T, cfgPath string, into any, args ...string) {
	t.Helper()
	var out, errOut bytes.Buffer
	handled, code := Run(append([]string{"-config", cfgPath}, args...), &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("%v = %v %d; stderr %q", args, handled, code, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), into); err != nil {
		t.Fatalf("%v json: %v (%q)", args, err, out.String())
	}
}

// setupRepoCLI is a workspace whose repository has one commit, one path that is
// both staged and edited again, and one untracked file — the three senses the
// status and diff verbs must keep apart.
func setupRepoCLI(t *testing.T) (repo, workspaceID, cfgPath string) {
	t.Helper()
	_, stateDir := setupIsolatedCLI(t)
	repo = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	write("tracked.txt", "base\n")
	git("add", "tracked.txt")
	git("commit", "-qm", "base")
	write("tracked.txt", "base\nSTAGED\n")
	git("add", "tracked.txt")
	write("tracked.txt", "base\nSTAGED\nUNSTAGED\n")
	write("untracked.txt", "UNTRACKED\n")

	writeProjectMeta(t, stateDir, "demo", repo)
	cfgPath = filepath.Join(filepath.Dir(stateDir), "config", "config.json")
	writeContentConfig(t, cfgPath, "demo", repo)
	return repo, repo + ":worktree:" + repo, cfgPath
}
