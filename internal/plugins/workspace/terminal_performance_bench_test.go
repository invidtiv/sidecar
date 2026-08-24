package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/terminalperf"
	terminalfixture "github.com/marcus/sidecar/internal/testfixture/terminal"
	"github.com/marcus/sidecar/internal/tty"
)

var benchmarkProjectTerminalFrame string

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
