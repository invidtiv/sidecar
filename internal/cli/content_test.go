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
		{[]string{"content", "resolve", "--workspace", "x:shell:y", "--kind", "diff", "--target", "a.md", "--json"}, 2, "unknown content kind"},
		{[]string{"content", "read", "--workspace", "x:shell:y", "--kind", "file", "--operation", "exec", "--target", "a.md", "--json"}, 2, "unknown content operation"},
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
