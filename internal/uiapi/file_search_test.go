package uiapi

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/filefind"
)

func TestFileSearchRankingParityAndConfinement(t *testing.T) {
	h, root := contentHarness(t)
	for _, path := range []string{"README.md", "src/read.go", "src/red.go", ".hidden", "ignored/read.go", "node_modules/read.js", ".git/config"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// .git must be a real repository for content policy discovery.
	if err := os.Remove(filepath.Join(root, ".git/config")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "escape.md")); err != nil {
		t.Fatal(err)
	}
	files, warning := filefind.ScanPaths(root, false)
	if warning != "" {
		t.Fatal(warning)
	}
	safe := files[:0]
	for _, f := range files {
		if f != "escape.md" {
			safe = append(safe, f)
		}
	}
	for _, query := range []string{"", "read", "rg", "no-match"} {
		response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/files/search?q=" + url.QueryEscape(query) + "&recent=src%2Fred.go"})
		expect(t, response, data, 200, "")
		var got contentservice.FileSearchResult
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		want := filefind.Filter(safe, query, 50, filefind.FilterOptions{Recent: []string{"src/red.go"}})
		if len(got.Results) != len(want) {
			t.Fatalf("%q: %s want %+v", query, data, want)
		}
		for i, m := range want {
			if got.Results[i].Path != m.Path || got.Results[i].Score != m.Score {
				t.Fatalf("ranking differs: %s want %+v", data, want)
			}
		}
	}
	for _, suffix := range []string{"?recent=../secret", "?workspace=foreign:worktree:outside", "?recent=" + url.QueryEscape(filepath.Join(outside, "secret.md"))} {
		response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/files/search" + suffix})
		expect(t, response, data, 403, "rejected")
	}
	for _, suffix := range []string{"?limit=0", "?limit=101", "?q=a&q=b", "?path=."} {
		response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/files/search" + suffix})
		expect(t, response, data, 400, CodeInvalidRequest)
	}
	response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/files/search?limit=1"})
	expect(t, response, data, 200, "")
	var got contentservice.FileSearchResult
	_ = json.Unmarshal(data, &got)
	if !got.Truncated || len(got.Results) != 1 {
		t.Fatalf("bounded results: %s", data)
	}
}

func TestFileSearchScopesAndUnicodePositions(t *testing.T) {
	h, root := contentHarness(t)
	if err := os.WriteFile(filepath.Join(root, "é🙂file.md"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{ScopeContentRead, ScopeUIControl} {
		origin := "http://" + scope[:2] + ".example"
		response, data := h.localDo(req{method: "POST", path: "/api/v0/origins", body: `{"origin":"` + origin + `","scopes":["` + scope + `"]}`})
		expect(t, response, data, 200, "")
		var registration OriginRegistration
		_ = json.Unmarshal(data, &registration)
		response, data = h.browserDo(req{method: "GET", path: "/api/v0/projects/content/files/search?q=" + url.QueryEscape("🙂f"), header: map[string]string{"Authorization": "Bearer " + registration.Token, "Origin": origin}})
		if scope != ScopeContentRead {
			expect(t, response, data, 403, "scope_refused")
			continue
		}
		expect(t, response, data, 200, "")
		var got contentservice.FileSearchResult
		_ = json.Unmarshal(data, &got)
		if len(got.Results) != 1 || !reflect.DeepEqual(got.Results[0].Positions, []int{1, 2}) {
			t.Fatalf("Unicode positions: %s", data)
		}
	}
}

func TestFileSearchSelectedWorkspace(t *testing.T) {
	h, root := contentHarness(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "main-only.md"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Proof", "-c", "user.email=proof@example.invalid", "commit", "-qm", "Initial")
	wt := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "-qb", "finder-proof", wt)
	if err := os.WriteFile(filepath.Join(wt, "linked-only.md"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, _ := filepath.EvalSymlinks(root)
	canonicalWT, _ := filepath.EvalSymlinks(wt)
	workspace := canonicalRoot + ":worktree:" + canonicalWT
	response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/files/search?q=linked&workspace=" + url.QueryEscape(workspace)})
	expect(t, response, data, 200, "")
	var got contentservice.FileSearchResult
	_ = json.Unmarshal(data, &got)
	if len(got.Results) != 1 || got.Results[0].Path != "linked-only.md" || got.Root != canonicalWT {
		t.Fatalf("workspace root: %s", data)
	}
	response, data = h.localDo(req{method: "GET", path: "/api/v0/projects/content/files/search?q=linked"})
	expect(t, response, data, 200, "")
	_ = json.Unmarshal(data, &got)
	if len(got.Results) != 0 {
		t.Fatalf("workspace leaked to root: %s", data)
	}
}

func TestFileSearchDoesNotSpendContentBudget(t *testing.T) {
	h, _ := contentHarness(t)
	for i := 0; i < 4; i++ {
		if !h.s.contentRequests.reserve("local") {
			t.Fatal("reserve")
		}
	}
	defer func() {
		for i := 0; i < 4; i++ {
			h.s.contentRequests.release("local")
		}
	}()
	response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/files/search"})
	expect(t, response, data, 200, "")
}

func TestFileSearchCachedSymlinkReplacement(t *testing.T) {
	h, root := contentHarness(t)
	file := filepath.Join(root, "document.md")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	path := "/api/v0/projects/content/files/search?q=document"
	response, data := h.localDo(req{method: "GET", path: path})
	expect(t, response, data, 200, "")
	var got contentservice.FileSearchResult
	_ = json.Unmarshal(data, &got)
	if len(got.Results) != 1 {
		t.Fatalf("initial result: %s", data)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "secret"), file); err != nil {
		t.Fatal(err)
	}
	response, data = h.localDo(req{method: "GET", path: path})
	expect(t, response, data, 200, "")
	_ = json.Unmarshal(data, &got)
	if len(got.Results) != 0 {
		t.Fatalf("escaping cached result: %s", data)
	}
}

func TestFileSearchCustomGitMetadataExcluded(t *testing.T) {
	h, root := contentHarness(t)
	cmd := exec.Command("git", "-C", root, "init", "-q", "--separate-git-dir", filepath.Join(root, "administration"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/files/search"})
	expect(t, response, data, 200, "")
	var got contentservice.FileSearchResult
	_ = json.Unmarshal(data, &got)
	if len(got.Results) != 1 || got.Results[0].Path != "README.md" {
		t.Fatalf("metadata was indexed: %s", data)
	}
}

func TestFileSearchDeniedCachedLeadersDoNotHideValidMatches(t *testing.T) {
	h, root := contentHarness(t)
	for _, name := range []string{"a.md", "b.md", "c.md", "d.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v0/projects/content/files/search?q=md&limit=1&recent=a.md&recent=b.md&recent=c.md"
	response, data := h.localDo(req{method: "GET", path: path})
	expect(t, response, data, 200, "")
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(root, "b.md")); err != nil {
		t.Fatal(err)
	}
	response, data = h.localDo(req{method: "GET", path: path})
	expect(t, response, data, 200, "")
	var got contentservice.FileSearchResult
	_ = json.Unmarshal(data, &got)
	if len(got.Results) != 1 || got.Results[0].Path != "d.md" || got.Truncated {
		t.Fatalf("valid lower match hidden: %s", data)
	}
}

func TestFixtureFileSearchNeverWalksHost(t *testing.T) {
	fixture, err := LoadFixtures(fixtureDir())
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.Backend = fixture })
	response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/fixture-project/files/search?q=read"})
	expect(t, response, data, 200, "")
	var result contentservice.FileSearchResult
	_ = json.Unmarshal(data, &result)
	if len(result.Results) != 1 || result.Results[0].Path != "README.md" {
		t.Fatalf("fixture search: %s", data)
	}
	response, data = h.localDo(req{method: "GET", path: "/api/v0/projects/host-project/files/search"})
	expect(t, response, data, 403, "rejected")
}
