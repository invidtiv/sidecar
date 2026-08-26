package overview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/layoutapply"
	"github.com/marcus/sidecar/internal/layoutreport"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacecreate"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func layoutSessionsModel(t *testing.T) (*Model, string) {
	t.Helper()
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	config.SetTestStateDir(filepath.Join(stateHome, "sidecar"))
	t.Cleanup(config.ResetTestStateDir)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := previewModel(t)
	result := m.results["sidecar"]
	for i := range result.Workspaces {
		if result.Workspaces[i].ID == "a" {
			result.Workspaces[i].Path = root
		}
	}
	m.results["sidecar"] = result
	m.syncBoard()
	m.workspaces.SelectID("a")
	run(t, m, m.SetWorkspacesVisible(true))
	m.WorkspacesView(previewWide, previewTall)
	if !m.preview.visible {
		t.Fatal("Sessions preview is not visible")
	}
	return m, root
}

func sessionsLayoutPayload(t *testing.T, mode string, panes ...uirequest.LayoutPane) uirequest.Request {
	t.Helper()
	raw, err := json.Marshal(uirequest.LayoutPayload{Mode: mode, Panes: panes})
	if err != nil {
		t.Fatal(err)
	}
	return uirequest.Request{
		ID: "layout-" + uirequest.NewRequestID(), Action: uirequest.ActionLayout,
		CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin:  uirequest.Origin{Sessions: true},
		Payload: raw,
	}
}

func readSessionsLayoutAck(t *testing.T, req uirequest.Request) uirequest.Ack {
	t.Helper()
	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil || len(acks) != 1 {
		t.Fatalf("acks = %+v err=%v", acks, err)
	}
	return acks[0]
}

func TestOverviewLayoutGet_SelectedRowTree(t *testing.T) {
	m, root := layoutSessionsModel(t)
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, "Document"))

	req := sessionsLayoutPayload(t, uirequest.LayoutModeGet)
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatal("get emitted a command")
	}
	ack := readSessionsLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened || len(ack.Layout) == 0 {
		t.Fatalf("get ack = %s layout %d bytes", ack.Status, len(ack.Layout))
	}

	var report layoutreport.Report
	if err := json.Unmarshal(ack.Layout, &report); err != nil {
		t.Fatalf("report does not parse: %v\n%s", err, ack.Layout)
	}
	if report.Version != 1 || report.Surface != "a" {
		t.Fatalf("report header = %+v", report)
	}
	if report.Root != root && report.Root != workspaceinventory.CanonicalPath(root) {
		if resolved, err := filepath.EvalSymlinks(root); err != nil || report.Root != resolved {
			t.Fatalf("root = %q, want %q", report.Root, root)
		}
	}
	if report.Caps.MaxColumns != panelayout.MaxGridColumns || report.Caps.LiveLeaves != panelayout.LiveLeafCap {
		t.Fatalf("caps = %+v", report.Caps)
	}
	if report.Floors[panelayout.KindNameFile].Height == 0 || report.Floors[panelayout.KindNamePrimary].Width == 0 {
		t.Fatalf("floors missing: %+v", report.Floors)
	}
	if report.Grid == nil {
		t.Fatal("grid is null for a simple primary+file tree")
	}
	kinds := map[string]string{}
	for _, col := range report.Grid.Columns {
		for _, pane := range col.Panes {
			kinds[pane.Cell] = pane.Kind
		}
	}
	if kinds["1.1"] != "primary" {
		t.Errorf("1.1 = %q", kinds["1.1"])
	}
	foundFile := false
	for _, kind := range kinds {
		if kind == "file" {
			foundFile = true
		}
	}
	if !foundFile {
		t.Fatalf("file pane missing from grid: %+v", kinds)
	}
}

func TestOverviewLayoutGet_OffScreenDeclines(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	run(t, m, m.SetWorkspacesVisible(false))
	req := sessionsLayoutPayload(t, uirequest.LayoutModeGet)
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatal("off-screen get emitted a command")
	}
	ack := readSessionsLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || ack.Reason != layoutapply.SessionsNotOnScreenReason {
		t.Fatalf("ack = %s %q, want the Sessions off-screen decline", ack.Status, ack.Reason)
	}
}

func TestOverviewLayoutIgnoresNonSessionsOrigin(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	req := sessionsLayoutPayload(t, uirequest.LayoutModeGet)
	req.Origin.Sessions = false
	req.Origin.TmuxSession = "sc-alpha"
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("non-sessions layout returned cmd %v", cmd)
	}
	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil {
		t.Fatal(err)
	}
	if len(acks) != 0 {
		t.Fatalf("overview answered a project-surface layout request: %+v", acks)
	}
}

func TestOverviewLayoutApply_DocAndTerminalMatchModal(t *testing.T) {
	modal, _ := layoutSessionsModel(t)
	original := ensurePreviewTerminalSession
	ensurePreviewTerminalSession = func(session, workDir string) (string, error) {
		return "%peer", nil
	}
	t.Cleanup(func() { ensurePreviewTerminalSession = original })

	run(t, modal, modal.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, "Document"))
	modal.OpenPaneSwitcher()
	modal.createForm.SetKind(workspacecreate.KindTerminalSplit)
	modal.createForm.SetPlacement(workspacecreate.PlacementAuto)
	run(t, modal, modal.createPreviewTerminalSplit())
	modalGrid := panelayout.GridOf(modal.preview.paneRoot)
	if modalGrid == nil {
		t.Fatal("modal tree has no grid")
	}

	applied, _ := layoutSessionsModel(t)
	ensurePreviewTerminalSession = func(session, workDir string) (string, error) {
		return "%peer", nil
	}
	req := sessionsLayoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "file", Targets: []string{"README.md"}},
		uirequest.LayoutPane{Kind: "shell", Name: "Terminal"},
	)
	run(t, applied, applied.handleUIRequest(req))
	ack := readSessionsLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened {
		t.Fatalf("apply status = %s reason %q", ack.Status, ack.Reason)
	}
	appliedGrid := panelayout.GridOf(applied.preview.paneRoot)
	if appliedGrid == nil || appliedGrid.ColumnCount() != modalGrid.ColumnCount() {
		t.Fatalf("applied grid %+v vs modal %+v", appliedGrid, modalGrid)
	}
	for col := 1; col <= appliedGrid.ColumnCount(); col++ {
		if appliedGrid.RowCount(col) != modalGrid.RowCount(col) {
			t.Fatalf("col %d rows applied %d modal %d", col, appliedGrid.RowCount(col), modalGrid.RowCount(col))
		}
		for row := 1; row <= appliedGrid.RowCount(col); row++ {
			a, b := appliedGrid.Cell(col, row), modalGrid.Cell(col, row)
			if a == nil || b == nil || a.Kind != b.Kind {
				t.Errorf("cell %d.%d applied %v modal %v", col, row, a, b)
			}
		}
	}
}

func TestOverviewLayoutApply_LiveCapMatchesModal(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	original := ensurePreviewTerminalSession
	ensurePreviewTerminalSession = func(session, workDir string) (string, error) {
		return "%peer", nil
	}
	t.Cleanup(func() { ensurePreviewTerminalSession = original })

	before, _ := json.Marshal(m.sessionsPaneLayoutJSON())
	req := sessionsLayoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "shell", Name: "one"},
		uirequest.LayoutPane{Kind: "shell", Name: "two"},
	)
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatal("cap-refused apply emitted a command")
	}
	ack := readSessionsLayoutAck(t, req)
	if ack.Status != uirequest.StatusDeclined || ack.Reason != termpanes.CapMessage {
		t.Fatalf("ack = %s %q, want %q", ack.Status, ack.Reason, termpanes.CapMessage)
	}
	after, _ := json.Marshal(m.sessionsPaneLayoutJSON())
	if string(after) != string(before) {
		t.Fatalf("cap refusal mutated the tree:\nbefore %s\nafter  %s", before, after)
	}
}

func TestOverviewLayoutGetApplyShapeMatchesProjectSurface(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	original := ensurePreviewTerminalSession
	ensurePreviewTerminalSession = func(session, workDir string) (string, error) {
		return "%peer", nil
	}
	t.Cleanup(func() { ensurePreviewTerminalSession = original })

	apply := sessionsLayoutPayload(t, uirequest.LayoutModeApply,
		uirequest.LayoutPane{Kind: "file", Targets: []string{"README.md"}},
		uirequest.LayoutPane{Kind: "shell", Name: "dev"},
	)
	run(t, m, m.handleUIRequest(apply))
	if ack := readSessionsLayoutAck(t, apply); ack.Status != uirequest.StatusOpened {
		t.Fatalf("apply = %s %q", ack.Status, ack.Reason)
	}

	get := sessionsLayoutPayload(t, uirequest.LayoutModeGet)
	_ = m.handleUIRequest(get)
	ack := readSessionsLayoutAck(t, get)

	var report map[string]json.RawMessage
	if err := json.Unmarshal(ack.Layout, &report); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "surface", "root", "grid", "caps", "floors"} {
		if _, ok := report[key]; !ok {
			t.Errorf("sessions get missing %q (project-surface field)", key)
		}
	}
	var caps map[string]int
	if err := json.Unmarshal(report["caps"], &caps); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"maxColumns", "maxRows", "liveLeaves"} {
		if caps[key] == 0 {
			t.Errorf("caps.%s missing", key)
		}
	}
	var floors map[string]map[string]int
	if err := json.Unmarshal(report["floors"], &floors); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{
		panelayout.KindNamePrimary, panelayout.KindNameFile, panelayout.KindNameIssue,
		panelayout.KindNameDiff, panelayout.KindNameResource, panelayout.KindNameShell, panelayout.KindNameNote,
	} {
		if floors[kind]["width"] == 0 && floors[kind]["height"] == 0 {
			t.Errorf("floors.%s missing width/height", kind)
		}
	}
	var grid struct {
		Columns []struct {
			Column int `json:"column"`
			Panes  []struct {
				Cell string `json:"cell"`
				Kind string `json:"kind"`
				Pane int    `json:"pane"`
			} `json:"panes"`
		} `json:"columns"`
	}
	if err := json.Unmarshal(report["grid"], &grid); err != nil {
		t.Fatalf("grid: %v", err)
	}
	if len(grid.Columns) == 0 {
		t.Fatal("grid.columns empty")
	}
	found := map[string]bool{}
	for _, col := range grid.Columns {
		if col.Column == 0 {
			t.Error("column index missing")
		}
		for _, pane := range col.Panes {
			found[pane.Kind] = true
			if pane.Cell == "" || pane.Kind == "" {
				t.Errorf("pane missing cell/kind: %+v", pane)
			}
		}
	}
	if !found["primary"] || !found["file"] || !found["shell"] {
		t.Fatalf("grid kinds = %v, want primary+file+shell", found)
	}

	applyAck := readSessionsLayoutAck(t, apply)
	if applyAck.ItemsVersion != 1 || len(applyAck.Items) != 2 {
		t.Fatalf("apply items = %+v", applyAck.Items)
	}
	for _, item := range applyAck.Items {
		if item.Verdict == "" || item.Cell == "" {
			t.Errorf("apply item missing verdict/cell: %+v", item)
		}
	}
}

func TestOverviewLayoutGet_NamedRow(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, "Document"))

	req := sessionsLayoutPayload(t, uirequest.LayoutModeGet)
	req.Origin.SessionsRow = "a"
	_ = m.handleUIRequest(req)
	ack := readSessionsLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened {
		t.Fatalf("named-row get = %s %q", ack.Status, ack.Reason)
	}
	var report layoutreport.Report
	if err := json.Unmarshal(ack.Layout, &report); err != nil {
		t.Fatal(err)
	}
	if report.Surface != "a" {
		t.Fatalf("surface = %q, want the named row", report.Surface)
	}
}
