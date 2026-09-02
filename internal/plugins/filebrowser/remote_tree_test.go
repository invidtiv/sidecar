package filebrowser

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/plugin"
)

const (
	remoteMarker    = "REMOTE-MARKER.md"
	localTwinMarker = "LOCAL-TWIN.md"
)

// recordingTreeSource answers from a fixed map and counts the calls, so a test
// can prove both what the tree shows and how many round trips it cost.
type recordingTreeSource struct {
	dirs  map[string][]DirEntry
	calls [][]string
}

func (s *recordingTreeSource) ListDirs(rels []string) map[string]DirListing {
	s.calls = append(s.calls, append([]string(nil), rels...))
	out := make(map[string]DirListing, len(rels))
	for _, rel := range rels {
		entries, ok := s.dirs[rel]
		if !ok {
			out[rel] = DirListing{Err: os.ErrNotExist}
			continue
		}
		out[rel] = DirListing{Entries: entries}
	}
	return out
}

func hostFixtureDirs() map[string][]DirEntry {
	return map[string][]DirEntry{
		"": {
			{Name: remoteMarker, Size: 12},
			{Name: "internal", IsDir: true},
			{Name: "noise.log", IsIgnored: true, Size: 3},
			{Name: ".DS_Store", Size: 6},
		},
		"internal": {{Name: "cli", IsDir: true}},
		"internal/cli": {
			{Name: "content.go", Size: 40},
		},
	}
}

// boundFilesPlugin plants a same-named twin on this machine's disk and binds
// the plugin to a host whose tree is served by src. Anything the twin contains
// appearing in the tree is the failure this whole area exists to prevent.
func boundFilesPlugin(t *testing.T, src TreeSource) (*Plugin, string) {
	t.Helper()
	twin := t.TempDir()
	if err := os.WriteFile(filepath.Join(twin, localTwinMarker), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New()
	p.treeSourceOverride = src
	ctx := &plugin.Context{
		WorkDir:         "",
		ProjectRoot:     "",
		HostID:          "aerie",
		ProjectKey:      "/home/me/sidecar",
		RemoteRunner:    func(context.Context, string, []string, any) error { return nil },
		HostVerbs:       func() hostproto.VerbCapabilities { return hostproto.VerbCapabilities{ContentTreeV1: true} },
		HostShows:       func() bool { return true },
		HostWorktreeKey: "",
	}
	if err := p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	return p, twin
}

func applyBuild(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no build command")
	}
	msg := cmd()
	built, ok := msg.(TreeBuiltMsg)
	if !ok {
		t.Fatalf("build produced %T", msg)
	}
	if built.Err != nil {
		t.Fatalf("build error: %v", built.Err)
	}
	if _, cmd := p.Update(built); cmd != nil {
		_ = cmd()
	}
}

func treePaths(p *Plugin) []string {
	var paths []string
	for _, node := range p.tree.Flatten() {
		paths = append(paths, node.Path)
	}
	return paths
}

func TestBoundTreeShowsHostEntriesNotTheLocalTwin(t *testing.T) {
	src := &recordingTreeSource{dirs: hostFixtureDirs()}
	p, twin := boundFilesPlugin(t, src)

	applyBuild(t, p, p.Start())

	paths := treePaths(p)
	if !containsPath(paths, remoteMarker) {
		t.Fatalf("tree = %v, want the host's %s", paths, remoteMarker)
	}
	if containsPath(paths, localTwinMarker) {
		t.Fatalf("tree showed this machine's twin: %v", paths)
	}
	// The twin is real and readable; nothing about the assertion above is
	// vacuous.
	if _, err := os.Stat(filepath.Join(twin, localTwinMarker)); err != nil {
		t.Fatalf("twin fixture missing: %v", err)
	}
}

func TestBoundTreeAppliesTheViewersOwnPresentationRules(t *testing.T) {
	src := &recordingTreeSource{dirs: hostFixtureDirs()}
	p, _ := boundFilesPlugin(t, src)
	applyBuild(t, p, p.Start())

	if containsPath(treePaths(p), ".DS_Store") {
		t.Fatal("a host's OS clutter reached the tree; hiding it is the viewer's rule")
	}
	node := p.tree.FindByPath("noise.log")
	if node == nil || !node.IsIgnored {
		t.Fatalf("noise.log = %+v, want the host's ignored flag carried through", node)
	}
}

func TestBoundTreePrefetchesExpandedPathsInOneCall(t *testing.T) {
	src := &recordingTreeSource{dirs: hostFixtureDirs()}
	p, _ := boundFilesPlugin(t, src)
	applyBuild(t, p, p.Start())

	// Expand down two levels, then rebuild: the rebuild must ask for the root
	// and both expanded directories together, not one round trip per level.
	if err := p.tree.Expand(p.tree.FindByPath("internal")); err != nil {
		t.Fatal(err)
	}
	if err := p.tree.Expand(p.tree.FindByPath(filepath.Join("internal", "cli"))); err != nil {
		t.Fatal(err)
	}
	src.calls = nil

	applyBuild(t, p, p.refresh())

	if len(src.calls) != 1 {
		t.Fatalf("rebuild made %d listing calls, want 1: %v", len(src.calls), src.calls)
	}
	want := map[string]bool{"": true, "internal": true, "internal/cli": true}
	for _, rel := range src.calls[0] {
		delete(want, rel)
	}
	if len(want) != 0 {
		t.Fatalf("batch %v did not ask for %v", src.calls[0], want)
	}
	if !containsPath(treePaths(p), filepath.Join("internal", "cli", "content.go")) {
		t.Fatalf("rebuilt tree lost the expanded branch: %v", treePaths(p))
	}
}

func TestExpandingAnUnvisitedDirectoryIsOneCall(t *testing.T) {
	src := &recordingTreeSource{dirs: hostFixtureDirs()}
	p, _ := boundFilesPlugin(t, src)
	applyBuild(t, p, p.Start())
	src.calls = nil

	if err := p.tree.Expand(p.tree.FindByPath("internal")); err != nil {
		t.Fatal(err)
	}
	if len(src.calls) != 1 || len(src.calls[0]) != 1 || src.calls[0][0] != "internal" {
		t.Fatalf("expand made %v, want one listing of internal", src.calls)
	}
}

// One directory that has gone away must not blank the branch around it.
func TestAMissingRemoteDirectoryKeepsTheRestOfTheTree(t *testing.T) {
	dirs := hostFixtureDirs()
	delete(dirs, "internal")
	src := &recordingTreeSource{dirs: dirs}
	p, _ := boundFilesPlugin(t, src)
	applyBuild(t, p, p.Start())

	if err := p.tree.Expand(p.tree.FindByPath("internal")); err == nil {
		t.Fatal("expanding a missing directory reported success")
	}
	if !containsPath(treePaths(p), remoteMarker) {
		t.Fatalf("the rest of the tree was lost: %v", treePaths(p))
	}
}

func TestBoundFilesRefusesWhenTheHostIsTooOld(t *testing.T) {
	src := &recordingTreeSource{dirs: hostFixtureDirs()}
	p, _ := boundFilesPlugin(t, src)
	p.treeSourceOverride = nil
	p.ctx.HostVerbs = func() hostproto.VerbCapabilities { return hostproto.VerbCapabilities{} }
	if err := p.Init(p.ctx); err != nil {
		t.Fatal(err)
	}

	if p.tree != nil {
		t.Fatal("a host without ContentTreeV1 still built a tree")
	}
	if cmd := p.Start(); cmd != nil {
		t.Fatal("Start built a tree against a host that cannot serve one")
	}
	reason := p.unavailableReason()
	if !strings.Contains(reason, "aerie") || !strings.Contains(reason, "content tree") {
		t.Fatalf("reason = %q, want the host named and the missing contract explained", reason)
	}
	if !strings.Contains(p.View(100, 20), "aerie") {
		t.Fatal("the unavailable view does not name the host")
	}
	_ = src
}

func TestBoundFilesRefusesWhenTheHostIsNotConnected(t *testing.T) {
	p, _ := boundFilesPlugin(t, nil)
	p.treeSourceOverride = nil
	p.ctx.HostShows = func() bool { return false }
	if err := p.Init(p.ctx); err != nil {
		t.Fatal(err)
	}

	reason := p.unavailableReason()
	if !strings.Contains(reason, "not connected") {
		t.Fatalf("reason = %q, want the disconnected reason rather than a version complaint", reason)
	}
	if strings.Contains(reason, "content tree") {
		t.Fatalf("a disconnected host was blamed for being out of date: %q", reason)
	}
}

func TestBoundFilesNeverStartsAWatcher(t *testing.T) {
	src := &recordingTreeSource{dirs: hostFixtureDirs()}
	p, _ := boundFilesPlugin(t, src)
	applyBuild(t, p, p.Start())
	if p.watcher != nil {
		t.Fatal("a bound Files started a filesystem watcher; livewatch does not cross the host boundary")
	}
}

func TestRemoteWorkspaceIDNamesTheBoundWorktree(t *testing.T) {
	p, _ := boundFilesPlugin(t, &recordingTreeSource{dirs: hostFixtureDirs()})
	if got := p.remoteWorkspaceID(); got != "/home/me/sidecar:worktree:/home/me/sidecar" {
		t.Fatalf("main checkout workspace id = %q", got)
	}
	p.ctx.HostWorktreeKey = "/home/me/sidecar-feature"
	if got := p.remoteWorkspaceID(); got != "/home/me/sidecar:worktree:/home/me/sidecar-feature" {
		t.Fatalf("worktree workspace id = %q", got)
	}
}

// The remote source speaks the host verb's own contract, including "." for the
// root, so a viewer and a host cannot disagree about which directory was asked
// for.
func TestRemoteTreeSourceSpeaksTheHostVerb(t *testing.T) {
	var got []string
	src := &remoteTreeSource{
		hostID:      "aerie",
		workspaceID: "/home/me/sidecar:worktree:/home/me/sidecar",
		run: func(_ context.Context, _ string, args []string, out any) error {
			got = args
			raw, err := json.Marshal(contentservice.TreeResult{
				Kind:      contentservice.KindTree,
				Workspace: "/home/me/sidecar:worktree:/home/me/sidecar",
				Dirs: []contentservice.TreeDir{
					{Path: "", Entries: []contentservice.TreeEntry{{Name: remoteMarker, Size: 3}}},
					{Path: "internal", Err: "directory internal no longer exists"},
				},
			})
			if err != nil {
				return err
			}
			return json.Unmarshal(raw, out)
		},
	}

	listings := src.ListDirs([]string{"", "internal"})
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "content tree") || !strings.Contains(joined, "--path . ") || !strings.Contains(joined, "--path internal") {
		t.Fatalf("args = %v", got)
	}
	if len(listings[""].Entries) != 1 || listings[""].Entries[0].Name != remoteMarker {
		t.Fatalf("root listing = %+v", listings[""])
	}
	if listings["internal"].Err == nil {
		t.Fatal("a per-directory host error was dropped")
	}
}

func TestRemoteTreeSourceReportsTransportFailurePerDirectory(t *testing.T) {
	src := &remoteTreeSource{
		hostID:      "aerie",
		workspaceID: "w",
		run:         func(context.Context, string, []string, any) error { return os.ErrDeadlineExceeded },
	}
	listings := src.ListDirs([]string{"", "internal"})
	if listings[""].Err == nil || listings["internal"].Err == nil {
		t.Fatalf("a transport failure left a directory looking empty rather than failed: %+v", listings)
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}
