package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/terminalperf"
	"github.com/marcus/sidecar/internal/termpanes"
	terminalfixture "github.com/marcus/sidecar/internal/testfixture/terminal"
	"github.com/marcus/sidecar/internal/tty"
)

var benchmarkProjectTerminalFrame string

const activeSessionDocument = `# Active session

- file: README.md
- issue: td-deadbeef
- diff: abcdef0
- resource: RES-1234
- URL: https://example.com/performance
`

type projectActiveSessionFixture struct {
	p        *Plugin
	terminal terminalfixture.OpenCode
	buffer   *tty.OutputBuffer
	viewer   *docview.Model
	root     string
}

func prepareProjectActiveSessionRoot(tb testing.TB) (string, terminalfixture.OpenCode) {
	tb.Helper()
	root := tb.TempDir()
	fixture := terminalfixture.NewOpenCode(160, 44)
	if err := fixture.PopulateRoot(root); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active-session.md"), []byte(activeSessionDocument), 0o644); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Fixture\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	return root, fixture
}

func newProjectActiveSessionFixture(tb testing.TB, root string, fixture terminalfixture.OpenCode) projectActiveSessionFixture {
	tb.Helper()
	buffer := tty.NewOutputBuffer(200)
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: fixture.Frame(0)})
	p := New()
	p.ctx = &plugin.Context{WorkDir: root, ProjectRoot: root, Epoch: 1}
	p.worktrees = []*Worktree{{
		Name: "OpenCode Fixture", Path: root, Branch: "synthetic", Status: StatusActive,
		Agent: &Agent{
			Type: AgentOpenCode, TmuxSession: "synthetic-session", TmuxPane: "%fixture", OutputBuf: buffer,
			Activity: agentactivity.Tracker{State: agentactivity.StateWorking},
		},
	}}
	p.activePane = PanePreview
	p.sidebarVisible = true
	p.focused = true
	p.applicationFocused = true
	p.paneRoot = &panelayout.Node{ID: 3, Split: &panelayout.Split{Axis: panelayout.Columns, Ratio: 50,
		A: &panelayout.Node{ID: 1, Kind: panelayout.Terminal},
		B: &panelayout.Node{ID: 2, Kind: panelayout.Document, ContentID: 2},
	}}
	p.paneFocus = 1
	p.paneNextID = 4
	p.docs = make(map[int]*docPane)
	viewer := docview.New(nil)
	loaded, ok := viewer.Load(2, root, "active-session.md", 0, p.ctx.Epoch)().(docview.LoadedMsg)
	if !ok || !viewer.SetResult(loaded) {
		tb.Fatal("active-session document did not load")
	}
	p.docs[2] = newDocPane(2, root, "worktree:fixture", viewer)
	p.SetResourceMatchers([]contentlink.ResourceMatcher{{Provider: "fixture", ID: "resource", Re: regexp.MustCompile(`RES-[0-9]+`)}})

	// Recreate the deck the same way decoded, split, and extracted live leaves
	// do. Slice 0 intentionally does not patch the constructor: before Slice 1
	// this makes the nil-analyzer fallback and its cold row misses observable.
	p.terminalPanes = termpanes.New()
	p.primaryTermPane()
	return projectActiveSessionFixture{p: p, terminal: fixture, buffer: buffer, viewer: viewer, root: root}
}

func projectTerminalFixture(b testing.TB) (*Plugin, terminalfixture.OpenCode, *tty.OutputBuffer) {
	b.Helper()
	root := b.TempDir()
	fixture := terminalfixture.NewOpenCode(160, 44)
	if err := fixture.PopulateRoot(root); err != nil {
		b.Fatal(err)
	}
	buffer := tty.NewOutputBuffer(200)
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: fixture.Frame(0)})
	p := New()
	p.ctx = &plugin.Context{WorkDir: root, ProjectRoot: root, Epoch: 1}
	p.worktrees = []*Worktree{{
		Name: "OpenCode Fixture", Path: root, Branch: "synthetic", IsMain: false,
		Agent: &Agent{Type: AgentOpenCode, TmuxSession: "synthetic-session", TmuxPane: "%fixture", OutputBuf: buffer},
	}}
	p.activePane = PanePreview
	p.sidebarVisible = false
	p.paneRoot = &panelayout.Node{ID: 1, Kind: panelayout.Terminal}
	p.paneFocus = 1
	return p, fixture, buffer
}

func TestProjectTerminalFixtureViewPerformsNoResolutionWork(t *testing.T) {
	p, _, _ := projectTerminalFixture(t)
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)
	_ = p.View(200, 50)
	_ = p.View(200, 50)
	snapshot := counters.Snapshot()
	if snapshot.TerminalViewsRendered == 0 {
		t.Fatalf("project counters = %+v, want rendered terminal views", snapshot)
	}
	if snapshot.ContentLinkResolutionRequests != 0 || snapshot.SynchronousResolverCalls != 0 {
		t.Fatalf("project repeated View counters = %+v, want zero resolution work", snapshot)
	}
}

func BenchmarkProjectTerminalFrameOpenCodeFixture(b *testing.B) {
	p, fixture, buffer := projectTerminalFixture(b)
	_ = p.View(200, 50)
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	b.Cleanup(restore)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		buffer.Update(fixture.Frame(i + 1))
		benchmarkProjectTerminalFrame = p.View(200, 50)
	}
	b.StopTimer()
	operations := float64(b.N)
	snapshot := counters.Snapshot()
	b.ReportMetric(float64(snapshot.TerminalViewsRendered)/operations, "terminal_views/op")
	b.ReportMetric(float64(snapshot.RowCacheHits)/operations, "row_cache_hits/op")
	b.ReportMetric(float64(snapshot.RowCacheMisses)/operations, "row_cache_misses/op")
	b.ReportMetric(float64(snapshot.ContentLinkResolutionRequests)/operations, "resolution_requests/op")
	b.ReportMetric(float64(snapshot.ContentLinkResolutionCacheHits)/operations, "resolution_cache_hits/op")
	b.ReportMetric(float64(snapshot.SynchronousResolverCalls)/operations, "synchronous_resolver_calls/op")
}

func TestProjectActiveSessionPulseAttribution(t *testing.T) {
	root, terminal := prepareProjectActiveSessionRoot(t)
	fixture := newProjectActiveSessionFixture(t, root, terminal)
	leaf := fixture.p.primaryTermPane()

	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)
	_ = fixture.p.View(220, 58)
	fixture.p.activityAnimationFrame++
	_ = fixture.p.View(220, 58)

	snapshot := counters.Snapshot()
	if snapshot.ProjectWorkspaceViewsRendered != 2 || snapshot.ProjectSidebarRendered != 2 ||
		snapshot.ProjectPreviewComposes != 2 || snapshot.TerminalViewsRendered != 2 {
		t.Fatalf("pulse attribution = %+v, want two complete project frames", snapshot)
	}
	if snapshot.DocumentFramesBuilt != 2 || snapshot.DocumentLinkScans != 2 {
		t.Fatalf("document attribution = %+v, want one frame and scan per pulse", snapshot)
	}
	if snapshot.DocumentResolutionRequests == 0 {
		t.Fatalf("document attribution = %+v, want initial file/diff resolution work", snapshot)
	}
	if snapshot.ProjectPreviewCacheHits != 0 || snapshot.DocumentFrameCacheHits != 0 {
		t.Fatalf("pre-cache fixture unexpectedly reused prepared presentation: %+v", snapshot)
	}
	if leaf.RowAnalyzer == nil {
		if snapshot.RowAnalyzerBypasses != 2 || snapshot.RowCacheMisses == 0 || snapshot.RowCacheHits != 0 {
			t.Fatalf("nil analyzer attribution = %+v, want two bypasses and cold misses", snapshot)
		}
	} else if snapshot.RowAnalyzerBypasses != 0 || snapshot.RowCacheHits == 0 {
		t.Fatalf("durable analyzer attribution = %+v, want reuse without bypass", snapshot)
	}
}

func reportActiveSessionMetrics(b *testing.B, counters *terminalperf.Counters) {
	b.Helper()
	operations := float64(b.N)
	if operations < 1 {
		operations = 1
	}
	snapshot := counters.Snapshot()
	metrics := map[string]uint64{
		"workspace_views/op":              snapshot.ProjectWorkspaceViewsRendered,
		"sidebar_renders/op":              snapshot.ProjectSidebarRendered,
		"preview_composes/op":             snapshot.ProjectPreviewComposes,
		"preview_cache_hits/op":           snapshot.ProjectPreviewCacheHits,
		"terminal_views/op":               snapshot.TerminalViewsRendered,
		"row_cache_hits/op":               snapshot.RowCacheHits,
		"row_cache_misses/op":             snapshot.RowCacheMisses,
		"row_analyzer_bypasses/op":        snapshot.RowAnalyzerBypasses,
		"document_frames_built/op":        snapshot.DocumentFramesBuilt,
		"document_frame_cache_hits/op":    snapshot.DocumentFrameCacheHits,
		"document_link_scans/op":          snapshot.DocumentLinkScans,
		"document_resolution_requests/op": snapshot.DocumentResolutionRequests,
		"document_resolution_hits/op":     snapshot.DocumentResolutionCacheHits,
	}
	for name, value := range metrics {
		b.ReportMetric(float64(value)/operations, name)
	}
}

// BenchmarkProjectActiveSessionFrame is the exact steady-state journey from
// the performance plan: one OpenCode-shaped terminal, one rendered Markdown
// document with every supported token family, and one visible working marker.
// Each sub-benchmark times only frame projection; setup and mutations are kept
// outside the timer so the result attributes View rather than fixture I/O.
func BenchmarkProjectActiveSessionFrame(b *testing.B) {
	root, terminal := prepareProjectActiveSessionRoot(b)

	b.Run("cold", func(b *testing.B) {
		counters := &terminalperf.Counters{}
		restore := terminalperf.Install(counters)
		defer restore()
		b.ReportAllocs()
		for range b.N {
			b.StopTimer()
			fixture := newProjectActiveSessionFixture(b, root, terminal)
			b.StartTimer()
			benchmarkProjectTerminalFrame = fixture.p.View(220, 58)
		}
		b.StopTimer()
		reportActiveSessionMetrics(b, counters)
	})

	benchmarkWarmProjectActiveSessionFrame(b, "pulse_only", root, terminal, func(_ testing.TB, fixture *projectActiveSessionFixture, iteration int) {
		fixture.p.activityAnimationFrame = iteration + 1
	})
	benchmarkWarmProjectActiveSessionFrame(b, "terminal_only", root, terminal, func(_ testing.TB, fixture *projectActiveSessionFixture, iteration int) {
		fixture.buffer.Update(fixture.terminal.Frame(iteration + 1))
	})
	benchmarkWarmProjectActiveSessionFrame(b, "document_resolution", root, terminal, func(_ testing.TB, fixture *projectActiveSessionFixture, iteration int) {
		candidate := contentlink.Pending{Kind: contentlink.KindFile, Raw: "README.md"}
		ref := contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}
		fixture.p.ensureDocLinkResolution().Put(candidate, ref, iteration%2 == 0)
	})
	benchmarkWarmProjectActiveSessionFrame(b, "document_refresh", root, terminal, func(tb testing.TB, fixture *projectActiveSessionFixture, iteration int) {
		body := activeSessionDocument + fmt.Sprintf("\nrefresh generation %d\n", iteration)
		if err := os.WriteFile(filepath.Join(fixture.root, "active-session.md"), []byte(body), 0o644); err != nil {
			tb.Fatal(err)
		}
		fixture.viewer.Observe()
		cmd := fixture.viewer.Refresh(false)
		if cmd == nil {
			tb.Fatal("document refresh was not scheduled")
		}
		msg, ok := cmd().(docview.LoadedMsg)
		if !ok || !fixture.viewer.SetResult(msg) {
			tb.Fatal("document refresh result was not applied")
		}
	})
}

func benchmarkWarmProjectActiveSessionFrame(
	b *testing.B,
	name, root string,
	terminal terminalfixture.OpenCode,
	mutate func(testing.TB, *projectActiveSessionFixture, int),
) {
	b.Helper()
	b.Run(name, func(b *testing.B) {
		fixture := newProjectActiveSessionFixture(b, root, terminal)
		_ = fixture.p.View(220, 58)
		counters := &terminalperf.Counters{}
		restore := terminalperf.Install(counters)
		defer restore()
		b.ReportAllocs()
		b.ResetTimer()
		for iteration := range b.N {
			b.StopTimer()
			mutate(b, &fixture, iteration)
			b.StartTimer()
			benchmarkProjectTerminalFrame = fixture.p.View(220, 58)
		}
		b.StopTimer()
		reportActiveSessionMetrics(b, counters)
	})
}
