package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/shellstate"
)

func TestContentCommandsAreReadOnly(t *testing.T) {
	root := RootCommand().FindSubcommand("content")
	if root == nil {
		t.Fatal("content command is not registered")
	}
	if root.Mutates {
		t.Fatal("content mutates")
	}
	for _, sub := range root.Sub {
		if sub.Mutates {
			t.Errorf("content %s mutates", sub.Name)
		}
	}
}

func TestContentHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"content", "--help"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("help = %v %d %q", handled, code, errOut.String())
	}
	if !strings.Contains(out.String(), "internal transport endpoint") {
		t.Fatalf("help = %q", out.String())
	}
}

func TestContentUsageErrors(t *testing.T) {
	for _, tt := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"content", "nope"}, 2, "unknown content command"},
		{[]string{"content", "resolve"}, 2, "--workspace is required"},
		{[]string{"content", "resolve", "--workspace", "x:shell:y", "--kind", "file"}, 2, "--target is required"},
		{[]string{"content", "resolve", "--workspace", "x:shell:y", "--kind", "nope", "--target", "a.md", "--json"}, 2, "unknown content kind"},
		{[]string{"content", "resolve", "--workspace", "x:shell:y", "--kind", "resource", "--target", "CASH-1", "--json"}, 2, "--provider is required"},
		{[]string{"content", "read", "--workspace", "x:shell:y", "--kind", "diff", "--operation", "exec", "--target", "wt", "--json"}, 2, "unknown content operation"},
		{[]string{"content", "read", "--workspace", "x:shell:y", "--kind", "file", "--operation", "exec", "--target", "a.md", "--json"}, 2, "unknown content operation"},
		{[]string{"content", "catalog"}, 2, "--workspace is required"},
		{[]string{"content", "catalog", "--workspace", "x:shell:y", "--kind", "resource", "--json"}, 2, "unknown content kind"},
		{[]string{"content", "tree"}, 2, "--workspace is required"},
		{[]string{"content", "tree", "--workspace", "x:worktree:y", "--path"}, 2, "--path requires a directory"},
		{[]string{"content", "tree", "--workspace", "x:worktree:y", "internal"}, 2, "takes no positional arguments"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			handled, code := Run(tt.args, &out, &errOut)
			if !handled || code != tt.code {
				t.Fatalf("Run = %v %d stderr %q", handled, code, errOut.String())
			}
			if !strings.Contains(out.String()+errOut.String(), tt.want) {
				t.Fatalf("output %q missing %q", out.String()+errOut.String(), tt.want)
			}
		})
	}
}

func TestContentResolveReadJSON(t *testing.T) {
	repo, id, cfgPath := setupContentCLI(t)
	body := "hello from host\n"
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "content", "resolve", "--workspace", id, "--kind", "file", "--target", "note.md", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("resolve = %v %d stderr %q", handled, code, errOut.String())
	}
	var resolved contentservice.ResolveResult
	if err := json.Unmarshal(out.Bytes(), &resolved); err != nil {
		t.Fatalf("resolve json: %v (%q)", err, out.String())
	}
	if !resolved.ValidRemoteResult() || resolved.Display != "note.md" {
		t.Fatalf("resolve = %+v", resolved)
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"-config", cfgPath, "content", "read", "--workspace", id, "--kind", "file", "--operation", "document", "--target", "note.md", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("read = %v %d stderr %q", handled, code, errOut.String())
	}
	var read contentservice.ReadResult
	if err := json.Unmarshal(out.Bytes(), &read); err != nil {
		t.Fatalf("read json: %v (%q)", err, out.String())
	}
	if !read.ValidRemoteResult() || read.Content != body || read.Revision != resolved.Revision {
		t.Fatalf("read = %+v", read)
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"-config", cfgPath, "content", "read", "--workspace", id, "--kind", "file", "--operation", "document", "--target", "note.md", "--if-revision", read.Revision, "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("notModified = %v %d stderr %q", handled, code, errOut.String())
	}
	var cached contentservice.ReadResult
	if err := json.Unmarshal(out.Bytes(), &cached); err != nil {
		t.Fatalf("notModified json: %v (%q)", err, out.String())
	}
	if !cached.NotModified || !cached.ValidRemoteResult() || cached.Revision != read.Revision {
		t.Fatalf("notModified = %+v", cached)
	}
}

func TestContentIssueNoteResolveJSON(t *testing.T) {
	_, id, cfgPath := setupContentCLI(t)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "content", "resolve", "--workspace", id, "--kind", "issue", "--target", "td-a4dd72", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("issue resolve = %v %d stderr %q", handled, code, errOut.String())
	}
	var resolved contentservice.ResolveResult
	if err := json.Unmarshal(out.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if !resolved.ValidRemoteResult() || resolved.Kind != contentservice.KindIssue || resolved.Target != "td-a4dd72" {
		t.Fatalf("issue resolve = %+v", resolved)
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"-config", cfgPath, "content", "resolve", "--workspace", id, "--kind", "note", "--target", "nt-host01", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("note resolve = %v %d stderr %q", handled, code, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if !resolved.ValidRemoteResult() || resolved.Kind != contentservice.KindNote || resolved.Target != "nt-host01" {
		t.Fatalf("note resolve = %+v", resolved)
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"-config", cfgPath, "content", "read", "--workspace", id, "--kind", "issue", "--operation", "document", "--target", "td-a4dd72", "--json"}, &out, &errOut)
	if !handled || code != 2 {
		t.Fatalf("wrong operation = %v %d %q", handled, code, errOut.String())
	}
	if !strings.Contains(out.String()+errOut.String(), "unknown content operation") {
		t.Fatalf("wrong operation output %q", out.String()+errOut.String())
	}
}

func TestContentDiffResolveReadJSON(t *testing.T) {
	repo, id, cfgPath := setupContentCLI(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.txt")
	run("commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\nhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "content", "resolve", "--workspace", id, "--kind", "diff", "--target", "wt", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("resolve = %v %d stderr %q", handled, code, errOut.String())
	}
	var resolved contentservice.ResolveResult
	if err := json.Unmarshal(out.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if !resolved.ValidRemoteResult() || resolved.Target != "wt" {
		t.Fatalf("resolve = %+v", resolved)
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"-config", cfgPath, "content", "read", "--workspace", id, "--kind", "diff", "--operation", "working-tree", "--target", "wt", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("read = %v %d stderr %q", handled, code, errOut.String())
	}
	var read contentservice.ReadResult
	if err := json.Unmarshal(out.Bytes(), &read); err != nil {
		t.Fatalf("read json: %v (%q)", err, out.String())
	}
	if !read.ValidRemoteResult() || read.Diff == nil || read.Diff.Snapshot == nil {
		t.Fatalf("read = %+v", read)
	}
	if !strings.Contains(read.Diff.Snapshot.WorkingTree, "host") {
		t.Fatalf("working-tree missing host change: %+v", read.Diff.Snapshot)
	}
}

func TestContentCatalogJSON(t *testing.T) {
	repo, id, cfgPath := setupContentCLI(t)
	if err := os.WriteFile(filepath.Join(repo, "HOST-ONLY.md"), []byte("host\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "content", "catalog", "--workspace", id, "--kind", "file", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("catalog = %v %d stderr %q", handled, code, errOut.String())
	}
	var catalog contentservice.CatalogResult
	if err := json.Unmarshal(out.Bytes(), &catalog); err != nil {
		t.Fatalf("catalog json: %v (%q)", err, out.String())
	}
	if !catalog.ValidRemoteResult() || catalog.KindFilter != contentservice.KindFile {
		t.Fatalf("catalog = %+v", catalog)
	}
	found := false
	for _, path := range catalog.Files {
		if path == "HOST-ONLY.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("files = %v, want HOST-ONLY.md", catalog.Files)
	}
}

func TestContentTreeJSON(t *testing.T) {
	repo, id, cfgPath := setupContentCLI(t)
	if err := os.MkdirAll(filepath.Join(repo, "internal", "cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", "cli", "HOST-ONLY.go"), []byte("package cli\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "noise.log"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "content", "tree", "--workspace", id, "--path", ".", "--path", "internal/cli", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("tree = %v %d stderr %q", handled, code, errOut.String())
	}
	var tree contentservice.TreeResult
	if err := json.Unmarshal(out.Bytes(), &tree); err != nil {
		t.Fatalf("tree json: %v (%q)", err, out.String())
	}
	if !tree.ValidRemoteResult() || len(tree.Dirs) != 2 {
		t.Fatalf("tree = %+v", tree)
	}
	if tree.Dirs[0].Path != "" || tree.Dirs[1].Path != "internal/cli" {
		t.Fatalf("listings came back out of request order: %+v", tree.Dirs)
	}
	var sawIgnored, sawDir bool
	for _, entry := range tree.Dirs[0].Entries {
		if entry.Name == "noise.log" && entry.Ignored {
			sawIgnored = true
		}
		if entry.Name == "internal" && entry.Dir {
			sawDir = true
		}
	}
	if !sawIgnored || !sawDir {
		t.Fatalf("root listing = %+v, want an ignored log and a directory", tree.Dirs[0].Entries)
	}
	if len(tree.Dirs[1].Entries) != 1 || tree.Dirs[1].Entries[0].Name != "HOST-ONLY.go" {
		t.Fatalf("internal/cli = %+v", tree.Dirs[1].Entries)
	}
}

func TestContentTreeRejectsEscapeAndUnknownWorkspace(t *testing.T) {
	_, id, cfgPath := setupContentCLI(t)
	for _, tt := range []struct {
		name string
		args []string
	}{
		{"escape", []string{"-config", cfgPath, "content", "tree", "--workspace", id, "--path", "../..", "--json"}},
		{"absolute", []string{"-config", cfgPath, "content", "tree", "--workspace", id, "--path", "/etc", "--json"}},
		{"unknown workspace", []string{"-config", cfgPath, "content", "tree", "--workspace", "nope:worktree:/nowhere", "--json"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			handled, code := Run(tt.args, &out, &errOut)
			if !handled || code != 5 {
				t.Fatalf("Run = %v %d, want exit 5; stderr %q", handled, code, errOut.String())
			}
		})
	}
}

func TestContentDescribeJSON(t *testing.T) {
	_, _, cfgPath := setupContentCLI(t)
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "content", "describe", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("describe = %v %d stderr %q", handled, code, errOut.String())
	}
	var described contentservice.DescribeResult
	if err := json.Unmarshal(out.Bytes(), &described); err != nil {
		t.Fatalf("describe json: %v (%q)", err, out.String())
	}
	if !described.ValidRemoteResult() || described.Fingerprint == "" {
		t.Fatalf("describe = %+v", described)
	}
	if described.Fingerprint != contentservice.FingerprintDescriptors(described.Descriptors) {
		t.Fatalf("fingerprint did not match descriptors: %+v", described)
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"-config", cfgPath, "content", "describe", "--if-revision", described.Fingerprint, "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("notModified = %v %d %q", handled, code, errOut.String())
	}
	var cached contentservice.DescribeResult
	if err := json.Unmarshal(out.Bytes(), &cached); err != nil {
		t.Fatal(err)
	}
	if !cached.NotModified || !cached.ValidRemoteResult() || cached.Fingerprint != described.Fingerprint {
		t.Fatalf("notModified = %+v", cached)
	}
}

func TestContentResourceResolveRequiresProvider(t *testing.T) {
	_, id, cfgPath := setupContentCLI(t)
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "content", "resolve", "--workspace", id, "--kind", "resource", "--target", "CASH-1245", "--provider", "jira-work", "--matcher", "issue-key", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("resolve = %v %d %q", handled, code, errOut.String())
	}
	var resolved contentservice.ResolveResult
	if err := json.Unmarshal(out.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if !resolved.ValidRemoteResult() || resolved.Kind != contentservice.KindResource || resolved.Provider != "jira-work" {
		t.Fatalf("resolve = %+v", resolved)
	}
}

func TestContentContainmentAndUnknownWorkspace(t *testing.T) {
	_, id, cfgPath := setupContentCLI(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "content", "resolve", "--workspace", id, "--kind", "file", "--target", "../secret.txt", "--json"}, &out, &errOut)
	if !handled || code != 5 {
		t.Fatalf("traversal = %v %d %q", handled, code, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"-config", cfgPath, "content", "resolve", "--workspace", "/nope:shell:sidecar-sh-1", "--kind", "file", "--target", "note.md", "--json"}, &out, &errOut)
	if !handled || code != 5 {
		t.Fatalf("unknown workspace = %v %d %q", handled, code, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	handled, code = Run([]string{"-config", cfgPath, "content", "resolve", "--workspace", id, "--kind", "file", "--target", outside, "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("absolute = %v %d %q", handled, code, errOut.String())
	}
	var resolved contentservice.ResolveResult
	if err := json.Unmarshal(out.Bytes(), &resolved); err != nil {
		t.Fatal(err)
	}
	if !resolved.ValidRemoteResult() {
		t.Fatalf("absolute resolve = %+v", resolved)
	}
}

func TestContentEncodedOversizeIsValidJSON(t *testing.T) {
	repo, id, cfgPath := setupContentCLI(t)
	quotes := strings.Repeat("\"", 400<<10)
	if err := os.WriteFile(filepath.Join(repo, "quotes.txt"), []byte(quotes), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "content", "read", "--workspace", id, "--kind", "file", "--operation", "document", "--target", "quotes.txt", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("oversize = %v %d %q", handled, code, errOut.String())
	}
	var read contentservice.ReadResult
	if err := json.Unmarshal(out.Bytes(), &read); err != nil {
		t.Fatalf("invalid JSON (%d bytes): %v", out.Len(), err)
	}
	if !read.ValidRemoteResult() {
		t.Fatalf("oversize result refused: %+v", read)
	}
	if !read.Truncated && !read.Oversize {
		t.Fatalf("quote-heavy file was not truncated: content=%d", len(read.Content))
	}
	if out.Len() > contentservice.MaxEncodedBytes+8 {
		t.Fatalf("stdout %d exceeds encoded cap", out.Len())
	}
}

func TestContentShellWorkspace(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	repo := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	initContentRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "note.md"), []byte("shell\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeProjectMeta(t, stateDir, "demo", repo)
	writeProjectShell(t, stateDir, "demo", shellstate.Definition{TmuxName: "sidecar-sh-demo-1", DisplayName: "Demo", WorkDir: repo})
	cfgPath := filepath.Join(filepath.Dir(stateDir), "config", "config.json")
	writeContentConfig(t, cfgPath, "demo", repo)
	id := repo + ":shell:sidecar-sh-demo-1"

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "content", "read", "--workspace", id, "--kind", "file", "--operation", "document", "--target", "note.md", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("shell read = %v %d %q", handled, code, errOut.String())
	}
	var read contentservice.ReadResult
	if err := json.Unmarshal(out.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.Content != "shell\n" {
		t.Fatalf("content = %q", read.Content)
	}
}

func setupContentCLI(t *testing.T) (repo, workspaceID, cfgPath string) {
	t.Helper()
	_, stateDir := setupIsolatedCLI(t)
	repo = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	initContentRepo(t, repo)
	writeProjectMeta(t, stateDir, "demo", repo)
	cfgPath = filepath.Join(filepath.Dir(stateDir), "config", "config.json")
	writeContentConfig(t, cfgPath, "demo", repo)
	return repo, repo + ":worktree:" + repo, cfgPath
}

func writeContentConfig(t *testing.T, cfgPath, name, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"projects":{"list":[{"name":` + quoteJSON(t, name) + `,"path":` + quoteJSON(t, path) + `}]}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initContentRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
}
