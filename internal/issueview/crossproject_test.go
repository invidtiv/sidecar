package issueview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/tdroot"
)

// newTDStore makes a directory tdroot treats as a project with a database.
func newTDStore(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, ".todos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".todos", "issues.db"), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// isolateState keeps BuildCandidates' root resolution out of the real state
// tree: ResolveTDRoot consults the centralized td-root file under it.
func isolateState(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

func TestBuildCandidatesExcludesCurrentCollapsesWorktreesAndPrunesMissing(t *testing.T) {
	isolateState(t)
	base := t.TempDir()
	current := filepath.Join(base, "current")
	projB := filepath.Join(base, "proj-b")
	worktreeB := filepath.Join(base, "worktree-b")
	missing := filepath.Join(base, "missing")
	newTDStore(t, current)
	newTDStore(t, projB)
	// A worktree sharing proj-b's store through the legacy .td-root link.
	if err := os.MkdirAll(worktreeB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreeB, ".td-root"), []byte(projB+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	refs := []ProjectRef{
		{Name: "B", Root: projB},
		{Name: "B-worktree", Root: worktreeB},
		{Name: "Missing", Root: missing},
	}
	got := BuildCandidates(current, refs)
	if len(got) != 1 {
		t.Fatalf("candidates = %#v, want one", got)
	}
	wantRoot := filepath.Clean(projB)
	if got[0].Name != "B" || got[0].Root != wantRoot {
		t.Fatalf("candidate = %#v, want {B %s} — first name must win and shared stores collapse", got[0], wantRoot)
	}
}

func TestBuildCandidatesResolvesThroughCentralizedTDRootFile(t *testing.T) {
	isolateState(t)
	base := t.TempDir()
	current := filepath.Join(base, "current")
	newTDStore(t, current)
	storeRoot := filepath.Join(base, "shared-store")
	newTDStore(t, storeRoot)
	memberDir := filepath.Join(base, "member")
	if err := os.MkdirAll(memberDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The centralized td-root file is what sidecar-created worktrees use.
	if err := tdroot.CreateTDRoot(memberDir, memberDir, storeRoot); err != nil {
		t.Fatalf("CreateTDRoot: %v", err)
	}

	got := BuildCandidates(current, []ProjectRef{{Name: "Shared", Root: memberDir}})
	if len(got) != 1 || got[0].Root != filepath.Clean(storeRoot) {
		t.Fatalf("candidates = %#v, want the shared store resolved from the centralized td-root file", got)
	}
}

func TestBuildCandidatesEmpty(t *testing.T) {
	isolateState(t)
	if got := BuildCandidates("", nil); got != nil {
		t.Fatalf("BuildCandidates(nil) = %#v, want nil", got)
	}
	base := t.TempDir()
	root := filepath.Join(base, "p")
	newTDStore(t, root)
	// A single candidate that IS the current project yields nothing.
	if got := BuildCandidates(root, []ProjectRef{{Name: "Self", Root: root}}); len(got) != 0 {
		t.Fatalf("candidates = %#v, want none for the current project itself", got)
	}
}

// writeCrossProjectShim installs a `td` shim that identifies the project by a
// marker file in the current directory (robust against tmpdir symlinks): a
// cp-fast marker answers instantly, cp-slow sleeps before answering, anything
// else misses like td does for an unknown issue.
func writeCrossProjectShim(t *testing.T, slowDelay string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" != show ]; then exit 1; fi\n" +
		"if [ -f ./cp-slow ]; then sleep " + slowDelay + "; fi\n" +
		"title=Fast\nflag=cp-fast\n" +
		"if [ -f ./cp-local ]; then title=Local; flag=cp-local; fi\n" +
		"if [ -f \"./$flag\" ]; then\n" +
		"  printf '{\"id\":\"td-x\",\"title\":\"%s\",\"status\":\"open\",\"type\":\"task\",\"priority\":\"P2\"}\\n' \"$title\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo \"ERROR: issue not found: $2\" >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestFindAcrossProjectsFirstSuccessWins(t *testing.T) {
	isolateState(t)
	base := t.TempDir()
	slow := filepath.Join(base, "slow")
	fast := filepath.Join(base, "fast")
	for _, p := range []*string{&slow, &fast} {
		newTDStore(t, *p)
	}
	writeCrossProjectShim(t, "30")
	touch := func(dir, marker string) {
		if err := os.WriteFile(filepath.Join(dir, marker), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	touch(fast, "cp-fast")
	touch(slow, "cp-slow")

	cands := []SearchCandidate{
		{Name: "Slow", Root: filepath.Clean(slow)},
		{Name: "Fast", Root: filepath.Clean(fast)},
	}
	started := time.Now()
	h := findAcrossProjects(context.Background(), "td-x", cands)
	if h == nil {
		t.Fatal("findAcrossProjects = nil, want a hit")
	}
	if h.Cand.Name != "Fast" || h.Data == nil || h.Data.Title != "Fast" {
		t.Fatalf("hit = %#v, want the fast project to win", h)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("fan-out took %s; first success should not wait on losers", elapsed)
	}
}

func TestFindAcrossProjectsNilWhenEveryCandidateMisses(t *testing.T) {
	isolateState(t)
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	newTDStore(t, a)
	newTDStore(t, b)
	shim := writeCrossProjectShim(t, "0")
	_ = shim

	old := crossProjectTimeout
	crossProjectTimeout = 2 * time.Second
	t.Cleanup(func() { crossProjectTimeout = old })

	h := findAcrossProjects(context.Background(), "td-x",
		[]SearchCandidate{{Name: "A", Root: a}, {Name: "B", Root: b}})
	if h != nil {
		t.Fatalf("hit = %#v, want nil when every store misses", h)
	}
}

func TestLoadIssueFallsBackToOwningProjectOnLocalMiss(t *testing.T) {
	isolateState(t)
	base := t.TempDir()
	local := filepath.Join(base, "local")
	other := filepath.Join(base, "other")
	newTDStore(t, local)
	newTDStore(t, other)
	writeCrossProjectShim(t, "0")
	if err := os.WriteFile(filepath.Join(other, "cp-fast"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	data, owner, err := loadIssue(local, "td-x", []ProjectRef{{Name: "Other", Root: other}})
	if err != nil {
		t.Fatal(err)
	}
	if data == nil || data.Title != "Fast" {
		t.Fatalf("data = %#v, want the foreign issue", data)
	}
	if owner == nil || owner.Name != "Other" || owner.Root != filepath.Clean(other) {
		t.Fatalf("owner = %#v, want Other at its cleaned root", owner)
	}
}

func TestLoadIssueTotalMissNamesTheProjectsSearched(t *testing.T) {
	isolateState(t)
	base := t.TempDir()
	local := filepath.Join(base, "local")
	other := filepath.Join(base, "other")
	newTDStore(t, local)
	newTDStore(t, other)
	writeCrossProjectShim(t, "0")

	_, owner, err := loadIssue(local, "td-nope", []ProjectRef{{Name: "Other", Root: other}})
	if err == nil {
		t.Fatal("err = nil, want the total-miss message")
	}
	if owner != nil {
		t.Fatalf("owner = %#v, want nil", owner)
	}
	want := `issue "td-nope" not found in 2 project(s)`
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}
}

func TestLoadIssueRealErrorShortCircuitsWithoutSearching(t *testing.T) {
	isolateState(t)
	base := t.TempDir()
	local := filepath.Join(base, "local")
	other := filepath.Join(base, "other")
	newTDStore(t, local)
	newTDStore(t, other)

	counts := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\n" +
		"echo called >> \"" + counts + "\"\n" +
		"echo \"ERROR: database is locked\" >&2\nexit 1\n"
	shimDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(shimDir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, owner, err := loadIssue(local, "td-x", []ProjectRef{{Name: "Other", Root: other}})
	if err == nil || !strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("err = %v, want the real td error passed through", err)
	}
	if owner != nil {
		t.Fatalf("owner = %#v, want nil — real errors must not trigger search", owner)
	}
	n, readErr := os.ReadFile(counts)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if lines := strings.Count(string(n), "called"); lines != 1 {
		t.Fatalf("td invocations = %d, want exactly the local attempt", lines)
	}
}

func TestLoadIssueLocalHitSkipsFallbacks(t *testing.T) {
	isolateState(t)
	base := t.TempDir()
	local := filepath.Join(base, "local")
	other := filepath.Join(base, "other")
	newTDStore(t, local)
	newTDStore(t, other)
	writeCrossProjectShim(t, "0")
	// Only `local` carries the local marker, so a hit titled Local proves the
	// search never ran.
	for dir, marker := range map[string]string{local: "cp-local", other: "cp-fast"} {
		if err := os.WriteFile(filepath.Join(dir, marker), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	data, owner, err := loadIssue(local, "td-x", []ProjectRef{{Name: "Other", Root: other}})
	if err != nil {
		t.Fatal(err)
	}
	if owner != nil {
		t.Fatalf("owner = %#v, want nil for a local hit", owner)
	}
	if data == nil || data.Title != "Local" {
		t.Fatalf("data = %#v, want the LOCAL issue (fallbacks must be skipped on a local hit)", data)
	}
}

func TestSetResultAdoptsOwningProjectAndRendersBadge(t *testing.T) {
	isolateState(t)
	m := New(nil)
	cmd := m.Load(1, "/tmp/local", "td-x", 0)
	if cmd == nil {
		t.Fatal("Load returned nil")
	}
	msg := cmd().(LoadedMsg)
	msg.Data = &Data{ID: "td-x", Title: "Foreign", Status: "open"}
	msg.Error = nil
	msg.FoundIn = &Owner{Name: "Other", Root: "/tmp/other"}
	if !m.SetResult(msg) {
		t.Fatal("SetResult rejected its own result")
	}
	if name, root := m.Owner(); name != "Other" || root != "/tmp/other" {
		t.Fatalf("Owner() = %q, %q; want Other at /tmp/other", name, root)
	}
	if m.WorkDir() != "/tmp/other" {
		t.Fatalf("WorkDir() = %q, want the owning store", m.WorkDir())
	}
	if got := m.Title(); !strings.HasPrefix(got, "[Other] ") {
		t.Fatalf("Title() = %q, want the [Other] prefix", got)
	}
	rows := m.ensureRows()
	badged := false
	for _, r := range rows {
		if strings.Contains(r.text, "[Other]") && strings.Contains(r.text, "td-x") {
			badged = true
		}
	}
	if !badged {
		t.Fatalf("status header rows lack the project badge:\n%s",
			strings.Join(rowsText(rows), "\n"))
	}
}

func TestLocalResultClearsBadgeAndKeepsLocalStore(t *testing.T) {
	isolateState(t)
	m := New(nil)
	msg := cmdMsg(m.Load(1, "/tmp/local", "td-x", 0))
	msg.Data = &Data{ID: "td-x", Title: "Foreign", Status: "open"}
	msg.FoundIn = &Owner{Name: "Other", Root: "/tmp/other"}
	m.SetResult(msg)

	msg2 := cmdMsg(m.Load(2, "/tmp/local2", "td-y", 0))
	msg2.Data = &Data{ID: "td-y", Title: "Home", Status: "open"}
	if !m.SetResult(msg2) {
		t.Fatal("SetResult rejected a local result")
	}
	if m.FoundIn() != "" {
		t.Fatalf("FoundIn() = %q after a local reload, want cleared", m.FoundIn())
	}
	if m.WorkDir() != "/tmp/local2" {
		t.Fatalf("WorkDir() = %q, want the load's own directory", m.WorkDir())
	}
	for _, r := range m.ensureRows() {
		if strings.Contains(r.text, "[") && strings.Contains(r.text, "]") &&
			strings.Contains(r.text, "td-y") {
			t.Fatalf("local card shows a badge row: %q", r.text)
		}
	}
}

// TestRefreshAddressesOwningStoreAfterAdoption proves live refresh reads the
// store that owns the card, not the one the click came from.
func TestRefreshAddressesOwningStoreAfterAdoption(t *testing.T) {
	isolateState(t)
	base := t.TempDir()
	local := filepath.Join(base, "local")
	other := filepath.Join(base, "other")
	newTDStore(t, local)
	newTDStore(t, other)
	writeCrossProjectShim(t, "0")
	if err := os.WriteFile(filepath.Join(other, "cp-fast"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	cwdLog := filepath.Join(t.TempDir(), "cwds")
	shimExtra := "#!/bin/sh\npwd -P >> \"" + cwdLog + "\"\n"
	// Re-install the shim with cwd logging prepended.
	dir := t.TempDir()
	script := shimExtra +
		"if [ \"$1\" != show ]; then exit 1; fi\n" +
		"if [ -f ./cp-fast ]; then printf '{\"id\":\"td-x\",\"title\":\"Fast\",\"status\":\"open\",\"type\":\"task\",\"priority\":\"P2\"}\\n'; exit 0; fi\n" +
		"echo \"ERROR: issue not found: $2\" >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := New(nil)
	m.FallbackRefs = func() []ProjectRef {
		return []ProjectRef{{Name: "Other", Root: other}}
	}
	msg := cmdMsg(m.Load(1, local, "td-x", 0))
	if msg.Error != nil {
		t.Fatal(msg.Error)
	}
	if msg.FoundIn == nil || msg.FoundIn.Name != "Other" {
		t.Fatalf("FoundIn = %#v, want the Other owner from the load itself", msg.FoundIn)
	}
	if !m.SetResult(msg) {
		t.Fatal("SetResult rejected its own result")
	}

	_ = os.Remove(cwdLog)
	// Observe records a store change, making one re-read owed — the same
	// signal a host's watcher would deliver.
	m.Observe()
	refresh := m.Refresh(false)
	if refresh == nil {
		t.Fatal("Refresh = nil, want an owed re-read command")
	}
	out := refresh().(LoadedMsg)
	if out.Refresh != true || out.Data == nil || out.Data.Title != "Fast" {
		t.Fatalf("refresh result = %#v", out)
	}
	logged, err := os.ReadFile(cwdLog)
	if err != nil {
		t.Fatal(err)
	}
	dirs := strings.Fields(string(logged))
	last := dirs[len(dirs)-1]
	wantReal, _ := filepath.EvalSymlinks(other)
	if last != wantReal {
		t.Fatalf("refresh ran in %s, want the owning store %s\ncwds=%v", last, wantReal, dirs)
	}
}

func cmdMsg(cmd tea.Cmd) LoadedMsg {
	msg, _ := cmd().(LoadedMsg)
	return msg
}

func rowsText(rows []row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.text
	}
	return out
}
