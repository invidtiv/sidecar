package overview

import (
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/terminalperf"
	terminalfixture "github.com/marcus/sidecar/internal/testfixture/terminal"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

var benchmarkGlobalTerminalFrame string

func globalTerminalFixture(b testing.TB) (*Model, terminalfixture.OpenCode, *tty.OutputBuffer) {
	b.Helper()
	root := b.TempDir()
	fixture := terminalfixture.NewOpenCode(160, 44)
	if err := fixture.PopulateRoot(root); err != nil {
		b.Fatal(err)
	}
	buffer := tty.NewOutputBuffer(200)
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: fixture.Frame(0)})
	workspace := workspaceinventory.Workspace{
		ID: "synthetic-workspace", ProjectKey: "synthetic-project", ProjectName: "Fixture",
		Kind: workspaceinventory.KindWorktree, Name: "OpenCode Fixture", Path: root,
		Live: true, PaneID: "%fixture", TmuxName: "synthetic-session", Plain: true,
	}
	m := &Model{
		catalog: map[string]workspaceinventory.Workspace{workspace.ID: workspace},
		preview: previewState{
			workspaceID: workspace.ID,
			paneRoot:    &panelayout.Node{ID: 1, Kind: panelayout.Terminal}, paneFocus: 1,
		},
		workspacesMouse: mouse.NewHandler(), sidebarVisible: true, sidebarWidth: defaultWorkspaceSidebarPercent,
		previewOwnership: &previewOwnershipLease{},
	}
	m.previewTerminalLeaf().Buffer = buffer
	m.workspaces.SetItems([]workspacelist.Item{listItem(workspace.Item(), workspace.ProjectName, 0, false)})
	m.workspaces.SelectID(workspace.ID)
	return m, fixture, buffer
}

func TestGlobalTerminalFixtureViewPerformsNoResolutionWork(t *testing.T) {
	m, _, _ := globalTerminalFixture(t)
	if m.previewTerminalLeaf().RowAnalyzer == nil {
		t.Fatal("global fixture leaf has no row analyzer")
	}
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)
	_ = m.WorkspacesView(200, 50)
	_ = m.WorkspacesView(200, 50)
	snapshot := counters.Snapshot()
	if snapshot.TerminalViewsRendered == 0 {
		t.Fatalf("global counters = %+v, want rendered terminal views", snapshot)
	}
	if snapshot.ContentLinkResolutionRequests != 0 || snapshot.SynchronousResolverCalls != 0 {
		t.Fatalf("global repeated View counters = %+v, want zero resolution work", snapshot)
	}
	if snapshot.RowAnalyzerBypasses != 0 || snapshot.RowCacheMisses == 0 || snapshot.RowCacheHits == 0 {
		t.Fatalf("global repeated View counters = %+v, want durable row reuse without bypass", snapshot)
	}
}

func BenchmarkGlobalTerminalFrameOpenCodeFixture(b *testing.B) {
	m, fixture, buffer := globalTerminalFixture(b)
	_ = m.WorkspacesView(200, 50)
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	b.Cleanup(restore)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		buffer.Update(fixture.Frame(i + 1))
		benchmarkGlobalTerminalFrame = m.WorkspacesView(200, 50)
	}
	b.StopTimer()
	reportTerminalMetrics(b, counters.Snapshot())
}

func reportTerminalMetrics(b *testing.B, snapshot terminalperf.Snapshot) {
	operations := float64(b.N)
	b.ReportMetric(float64(snapshot.TerminalViewsRendered)/operations, "terminal_views/op")
	b.ReportMetric(float64(snapshot.RowCacheHits)/operations, "row_cache_hits/op")
	b.ReportMetric(float64(snapshot.RowCacheMisses)/operations, "row_cache_misses/op")
	b.ReportMetric(float64(snapshot.ContentLinkResolutionRequests)/operations, "resolution_requests/op")
	b.ReportMetric(float64(snapshot.ContentLinkResolutionCacheHits)/operations, "resolution_cache_hits/op")
	b.ReportMetric(float64(snapshot.SynchronousResolverCalls)/operations, "synchronous_resolver_calls/op")
	b.ReportMetric(float64(snapshot.GlobalWorkspaceListRendered)/operations, "workspace_list_renders/op")
	b.ReportMetric(float64(snapshot.GlobalWorkspacePreviewRendered)/operations, "workspace_preview_renders/op")
}
