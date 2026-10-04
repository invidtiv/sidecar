package contentservice

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func projectGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func TestProjectRefusesGitMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	initGitRepo(t, root)
	projectGit(t, root, "config", "remote.origin.url", "https://synthetic-token@example.invalid/repo")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("EXAMPLE=ordinary-project-content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".git", filepath.Join(root, "admin-alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".git/config", filepath.Join(root, "config-alias")); err != nil {
		t.Fatal(err)
	}
	svc := testService(t, root, nil, nil)
	ctx := context.Background()
	for _, path := range []string{".git/config", ".GIT/config", "nested/.git/config", ".git/missing", "admin-alias/config", "admin-alias/missing", "config-alias"} {
		t.Run(path, func(t *testing.T) {
			result, err := svc.ReadProject(ctx, "demo", "", ReadParams{Kind: KindFile, Operation: OpDocument, Target: path})
			if !IsRejected(err) {
				t.Errorf("metadata read allowed: err=%v, content=%q", err, result.Content)
			}
			if _, err := svc.WatchProject(ctx, "demo", "", ReadParams{Kind: KindFile, Operation: OpDocument, Target: path}); !IsRejected(err) {
				t.Errorf("metadata watch allowed: %v", err)
			}
			for _, op := range []string{OpWorkingTreeFile, OpFullFile, OpCommitFile} {
				if _, err := svc.ReadProject(ctx, "demo", "", ReadParams{Kind: KindDiff, Operation: op, Target: "wt", Path: path}); !IsRejected(err) {
					t.Errorf("metadata %s diff allowed: %v", op, err)
				}
			}
		})
	}
	for _, path := range []string{".git", "admin-alias"} {
		if _, err := svc.TreeProject(ctx, "demo", "", []string{path}); !IsRejected(err) {
			t.Errorf("metadata tree allowed %s: %v", path, err)
		}
		if _, err := svc.WatchProject(ctx, "demo", "", ReadParams{Kind: KindTree, Target: path}); !IsRejected(err) {
			t.Errorf("metadata tree watch allowed %s: %v", path, err)
		}
	}
	tree, err := svc.TreeProject(ctx, "demo", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range tree.Dirs[0].Entries {
		if entry.Name == ".git" || entry.Name == "admin-alias" || entry.Name == "config-alias" {
			t.Errorf("metadata entry listed: %s", entry.Name)
		}
	}
	result, err := svc.ReadProject(ctx, "demo", "", ReadParams{Kind: KindFile, Operation: OpDocument, Target: ".env"})
	if err != nil || result.Content != "EXAMPLE=ordinary-project-content\n" {
		t.Fatalf(".env ordinary content refused: %+v %v", result, err)
	}
	if _, err := svc.WatchProject(ctx, "demo", "", ReadParams{Kind: KindFile, Operation: OpDocument, Target: ".env"}); err != nil {
		t.Fatalf(".env watch: %v", err)
	}
	// The desktop's established content behavior is unaffected by the API policy.
	result, err = svc.Read(ctx, canonical(root)+":worktree:"+canonical(root), KindFile, OpDocument, ".git/config", "")
	if err != nil || !strings.Contains(result.Content, "synthetic-token") {
		t.Fatalf("desktop read changed: %+v %v", result, err)
	}
}

func TestProjectRefusesInRootWorktreeGitDirectories(t *testing.T) {
	t.Parallel()
	for _, linkedWorktree := range []bool{false, true} {
		t.Run(map[bool]string{false: "separate-git-dir", true: "linked-worktree"}[linkedWorktree], func(t *testing.T) {
			root := t.TempDir()
			main := filepath.Join(root, "main")
			if err := os.Mkdir(main, 0700); err != nil {
				t.Fatal(err)
			}
			initGitRepo(t, main)
			projectGit(t, main, "commit", "-q", "--allow-empty", "-m", "initial")
			view := main
			if linkedWorktree {
				view = filepath.Join(root, "linked")
				projectGit(t, main, "worktree", "add", "-q", "--detach", view)
			}
			admin := filepath.Join(view, "administration")
			if err := os.Rename(filepath.Join(main, ".git"), admin); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(main, ".git"), []byte("gitdir: "+admin+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			gitDir := admin
			if linkedWorktree {
				gitDir = filepath.Join(admin, "worktrees", "linked")
				if err := os.WriteFile(filepath.Join(view, ".git"), []byte("gitdir: "+gitDir+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Exercise Git itself so a broken synthetic fixture cannot prove a refusal.
			discovered := projectGit(t, view, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
			if !strings.Contains(discovered, gitDir) || !strings.Contains(discovered, admin) {
				t.Fatalf("git directory discovery: %s", discovered)
			}
			if err := os.Symlink("administration", filepath.Join(view, "admin-alias")); err != nil {
				t.Fatal(err)
			}
			svc := testService(t, view, nil, nil)
			for _, path := range []string{".git", "administration/config", "ADMINISTRATION/config", "admin-alias/config", "administration/missing", "admin-alias/missing"} {
				p := ReadParams{Kind: KindFile, Operation: OpDocument, Target: path}
				if _, err := svc.ReadProject(context.Background(), "demo", "", p); !IsRejected(err) {
					t.Errorf("gitdir read %s: %v", path, err)
				}
				if _, err := svc.WatchProject(context.Background(), "demo", "", p); !IsRejected(err) {
					t.Errorf("gitdir watch %s: %v", path, err)
				}
			}
			for _, path := range []string{"administration", "admin-alias"} {
				if _, err := svc.TreeProject(context.Background(), "demo", "", []string{path}); !IsRejected(err) {
					t.Errorf("gitdir tree %s: %v", path, err)
				}
			}
			tree, err := svc.TreeProject(context.Background(), "demo", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range tree.Dirs[0].Entries {
				if entry.Name == "administration" || entry.Name == "admin-alias" || entry.Name == ".git" {
					t.Errorf("gitdir entry listed: %s", entry.Name)
				}
			}
		})
	}
}

func TestProjectSymlinkPolicyDoesNotRevealOutsideExistence(t *testing.T) {
	t.Parallel()
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "present"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	svc := testService(t, root, nil, nil)
	for _, kind := range []string{KindFile, KindTree, KindDiff} {
		var reasons []string
		for _, name := range []string{"present", "absent"} {
			p := ReadParams{Kind: kind, Target: "escape/" + name, Operation: OpDocument}
			var err error
			if kind == KindTree {
				_, err = svc.TreeProject(context.Background(), "demo", "", []string{p.Target})
			} else {
				if kind == KindDiff {
					p.Operation = OpWorkingTreeFile
					p.Path = p.Target
					p.Target = "wt"
				}
				_, err = svc.ReadProject(context.Background(), "demo", "", p)
			}
			if !IsRejected(err) {
				t.Fatalf("%s %s read: %v", kind, name, err)
			}
			reasons = append(reasons, err.Error())
			_, err = svc.WatchProject(context.Background(), "demo", "", p)
			if !IsRejected(err) {
				t.Fatalf("%s %s watch: %v", kind, name, err)
			}
		}
		if strings.ReplaceAll(reasons[0], "present", "absent") != reasons[1] {
			t.Errorf("%s existence oracle: %v", kind, reasons)
		}
	}
}

func TestProjectResolvesSymlinkComponentsInOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, dir := range []string{"a", "b", "b/child"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "b", "note.md"), []byte("resolved through b"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("b/child", filepath.Join(root, "via")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("via/../note.md", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	svc := testService(t, root, nil, nil)
	result, err := svc.ReadProject(context.Background(), "demo", "", ReadParams{Kind: KindFile, Operation: OpDocument, Target: "alias"})
	if err != nil || result.Content != "resolved through b" || result.Display != "alias" {
		t.Fatalf("symlink resolution: %+v %v", result, err)
	}
	if err := os.Symlink("missing/../../outside", filepath.Join(root, "missing-escape")); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{KindFile, KindTree} {
		if _, err := svc.WatchProject(context.Background(), "demo", "", ReadParams{Kind: kind, Operation: OpDocument, Target: "missing-escape"}); !IsRejected(err) {
			t.Errorf("missing-target escape watch %s: %v", kind, err)
		}
	}
}

func TestProjectDiffPreservesTrackedSymlinkIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	initGitRepo(t, root)
	for _, name := range []string{"first.md", "second.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("same contents\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("first.md", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	projectGit(t, root, "add", "--", "first.md", "second.md", "alias")
	projectGit(t, root, "commit", "-q", "-m", "initial symlink")
	if err := os.Remove(filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("second.md", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	svc := testService(t, root, nil, nil)
	p := ReadParams{Kind: KindDiff, Operation: OpWorkingTreeFile, Target: "wt", Path: "alias"}
	api, err := svc.ReadProject(context.Background(), "demo", "", p)
	if err != nil {
		t.Fatal(err)
	}
	p.WorkspaceID = canonical(root) + ":worktree:" + canonical(root)
	desktop, err := svc.ReadParams(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if api.Diff == nil || api.Diff.File == nil || desktop.Diff == nil || desktop.Diff.File == nil {
		t.Fatalf("missing diff API=%+v desktop=%+v", api, desktop)
	}
	if api.Diff.File.Path != "alias" || api.Diff.File.Raw != desktop.Diff.File.Raw || !strings.Contains(api.Diff.File.Raw, "-first.md") || !strings.Contains(api.Diff.File.Raw, "+second.md") {
		t.Fatalf("API changed Git symlink identity: %+v desktop=%+v", api.Diff.File, desktop.Diff.File)
	}
}

func TestProjectAliasWatchesPreserveSourceAndResolvedTargets(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, dir := range []string{"docs", "other"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "other", "note.md"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../other/note.md", filepath.Join(root, "docs", "alias.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../other", filepath.Join(root, "docs", "alias-dir")); err != nil {
		t.Fatal(err)
	}
	svc := testService(t, root, nil, nil)
	for _, tc := range []struct {
		kind, target, resolved string
		dir                    bool
	}{
		{KindFile, "docs/alias.md", "other/note.md", false},
		{KindTree, "docs/alias-dir", "other", true},
	} {
		targets, err := svc.WatchProject(context.Background(), "demo", "", ReadParams{Kind: tc.kind, Operation: OpDocument, Target: tc.target})
		if err != nil {
			t.Fatal(err)
		}
		if len(targets) != 2 || targets[0].Path != filepath.Join(canonical(root), tc.target) || targets[1].Path != filepath.Join(canonical(root), tc.resolved) || targets[0].Dir != tc.dir || targets[1].Dir != tc.dir {
			t.Errorf("%s watches lost source or target: %+v", tc.kind, targets)
		}
	}
}

func TestProjectAggregateDiffsExcludeGitMetadataBeforeRead(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	initGitRepo(t, root)
	projectGit(t, root, "config", "remote.origin.url", "https://synthetic-aggregate-token@example.invalid/repo")
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	projectGit(t, root, "add", "note.md")
	projectGit(t, root, "commit", "-q", "-m", "base")
	base := projectGit(t, root, "rev-parse", "HEAD")
	if err := os.Mkdir(filepath.Join(root, "administration"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "administration", "config"), []byte("synthetic-aggregate-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("ordinary-change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	projectGit(t, root, "add", "note.md", "administration/config")
	projectGit(t, root, "commit", "-q", "-m", "ordinary and future administrative files")
	head := projectGit(t, root, "rev-parse", "HEAD")
	if err := os.Remove(filepath.Join(root, "administration", "config")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "administration")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, ".git"), filepath.Join(root, "administration")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: administration\n"), 0600); err != nil {
		t.Fatal(err)
	}
	projectGit(t, root, "config", "core.worktree", canonical(root))
	if err := os.WriteFile(filepath.Join(root, "administration", "aggregate-untracked-probe"), []byte("synthetic-aggregate-token\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("working-change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// A separate gitdir is a real fixture, not an assumed path-name rule.
	if got := projectGit(t, root, "rev-parse", "--path-format=absolute", "--git-dir"); got != filepath.Join(canonical(root), "administration") {
		t.Fatalf("gitdir=%s", got)
	}
	svc := testService(t, root, nil, nil)
	for _, tc := range []struct{ op, target string }{
		{OpWorkingTree, "wt"}, {OpRange, "r:" + base + ".." + head}, {OpCommit, "c:" + head},
	} {
		t.Run(tc.op, func(t *testing.T) {
			params := ReadParams{Kind: KindDiff, Operation: tc.op, Target: tc.target}
			result, err := svc.ReadProject(context.Background(), "demo", "", params)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "synthetic-aggregate-token") || strings.Contains(string(raw), "administration/config") {
				t.Errorf("aggregate %s disclosed Git metadata", tc.op)
			}
			if !strings.Contains(string(raw), "note.md") {
				t.Errorf("aggregate %s omitted ordinary file", tc.op)
			}
			desktop, err := svc.readDiffAt(context.Background(), canonical(root), params)
			if err != nil {
				t.Fatal(err)
			}
			desktopRaw, err := json.Marshal(desktop)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(desktopRaw), "administration/config") {
				t.Errorf("desktop %s changed metadata behavior", tc.op)
			}
		})
	}
	// A working-tree subscription retains its internal Git invalidators without
	// turning excluded metadata into public content references or refusing the pane.
	if _, err := svc.WatchProject(context.Background(), "demo", "", ReadParams{Kind: KindDiff, Operation: OpWorkingTree, Target: "wt"}); err != nil {
		t.Fatalf("aggregate watch: %v", err)
	}
}

func TestProjectAggregateWatchSkipsTrackedMetadataAlias(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	alias := filepath.Join(root, "admin-link")
	if err := os.Symlink(".git/config", alias); err != nil {
		t.Fatal(err)
	}
	projectGit(t, root, "add", "admin-link")
	projectGit(t, root, "commit", "-qm", "tracked symlink")
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".git/HEAD", alias); err != nil {
		t.Fatal(err)
	}
	svc := testService(t, root, nil, nil)
	targets, err := svc.WatchProject(context.Background(), "demo", "", ReadParams{Kind: KindDiff, Operation: OpWorkingTree, Target: "wt"})
	if err != nil {
		t.Fatalf("aggregate subscription refused due to tracked symlink target string: %v", err)
	}
	for _, target := range targets {
		if target.Path == alias {
			t.Fatal("metadata alias registered as project content")
		}
	}
	if len(targets) == 0 {
		t.Fatal("Git invalidators were lost")
	}
}
