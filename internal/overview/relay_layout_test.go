package overview

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/layoutapply"
	"github.com/marcus/sidecar/internal/layoutreport"
	"github.com/marcus/sidecar/internal/uirequest"
)

func relayedLayoutGetAnnouncement(session string) hostproto.UIRequest {
	return relayedLayoutAnnouncement("req-relay-layout-get", session, uirequest.LayoutPayload{Mode: uirequest.LayoutModeGet})
}

func relayedLayoutAnnouncement(id, session string, payload uirequest.LayoutPayload) hostproto.UIRequest {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return hostproto.UIRequest{
		ID: id, Action: hostproto.UIRequestActionLayout, TTLMs: 5000,
		CreatedAt: time.Now().UTC(),
		Origin:    hostproto.UIRequestOrigin{TmuxSession: session, HostID: "mac-mini"},
		Payload:   raw,
	}
}

func relayedLayoutApplyPanes(session string, panes ...uirequest.LayoutPane) hostproto.UIRequest {
	return relayedLayoutAnnouncement("req-relay-layout-apply", session, uirequest.LayoutPayload{
		Mode: uirequest.LayoutModeApply, Panes: panes,
	})
}

func relayedLayoutApplySpec(session string, columns []uirequest.LayoutSpecColumn) hostproto.UIRequest {
	raw, err := json.Marshal(columns)
	if err != nil {
		panic(err)
	}
	return relayedLayoutAnnouncement("req-relay-layout-spec", session, uirequest.LayoutPayload{
		Mode: uirequest.LayoutModeApply, Columns: raw,
	})
}

func relayedLayoutMove(session string, move uirequest.LayoutMove) hostproto.UIRequest {
	return relayedLayoutAnnouncement("req-relay-layout-move", session, uirequest.LayoutPayload{
		Mode: uirequest.LayoutModeMove, Move: &move,
	})
}

func refuseLocalTerminalSplit(t *testing.T) {
	t.Helper()
	original := ensurePreviewTerminalSession
	ensurePreviewTerminalSession = func(session, workDir string) (string, error) {
		t.Errorf("created a local tmux session %q in %q for a remote row", session, workDir)
		return "", nil
	}
	t.Cleanup(func() { ensurePreviewTerminalSession = original })
}

func previewLayoutSnapshot(m *Model) string {
	raw, err := json.Marshal(m.sessionsPaneLayoutJSON())
	if err != nil {
		return "<marshal error>"
	}
	return string(raw)
}

func layoutJSONFromAckArgs(t *testing.T, args []string) json.RawMessage {
	t.Helper()
	for i, arg := range args {
		if arg == "--layout" {
			if i+1 >= len(args) {
				t.Fatalf("ack --layout missing value: %v", args)
			}
			raw := json.RawMessage(args[i+1])
			if !json.Valid(raw) {
				t.Fatalf("ack --layout is not JSON: %s", args[i+1])
			}
			return raw
		}
		if strings.HasPrefix(arg, "--layout=") {
			raw := json.RawMessage(strings.TrimPrefix(arg, "--layout="))
			if !json.Valid(raw) {
				t.Fatalf("ack --layout is not JSON: %s", arg)
			}
			return raw
		}
	}
	t.Fatalf("ack argv missing --layout: %v", args)
	return nil
}

func TestRelayedLayoutGetReturnsViewerReport(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	selected, ok := m.SelectedWorkspace()
	if !ok || !selected.Remote() {
		t.Fatal("fixture did not select a remote row")
	}
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "twin.txt"}, "Document"))

	req := requestFromAnnouncement(relayedLayoutGetAnnouncement(selected.TmuxName))
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("relayed get emitted a command: %v", cmd)
	}
	assertRemoteAck(t, stub, "opened", "--id req-relay-layout-get")
	var report layoutreport.Report
	if err := json.Unmarshal(layoutJSONFromAckArgs(t, stub.argv(t, 0)), &report); err != nil {
		t.Fatalf("report does not parse: %v", err)
	}
	if report.Version != 1 || report.Surface != selected.ID {
		t.Fatalf("report header = %+v, want surface %q", report, selected.ID)
	}
	if report.Grid == nil {
		t.Fatal("grid is null for a primary+file tree")
	}
	kinds := map[string]bool{}
	for _, col := range report.Grid.Columns {
		for _, pane := range col.Panes {
			kinds[pane.Kind] = true
		}
	}
	if !kinds["primary"] || !kinds["file"] {
		t.Fatalf("grid kinds = %v, want primary+file", kinds)
	}
}

func TestRelayedLayoutGetOffScreenDeclines(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	run(t, m, m.SetWorkspacesVisible(false))

	req := requestFromAnnouncement(relayedLayoutGetAnnouncement(selected.TmuxName))
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatalf("off-screen relayed get returned cmd %v", cmd)
	}
	assertRemoteAck(t, stub, "declined", layoutapply.SessionsNotOnScreenReason)
}

func TestForwardHostUIRequestLayoutGetThroughAnnouncement(t *testing.T) {
	m, _, _ := showingRemoteTwinModel(t, nil)
	stub := &remoteRunnerStub{}
	stub.install(t)
	selected, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace")
	}
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "twin.txt"}, "Document"))

	event := relayedLayoutGetAnnouncement(selected.TmuxName)
	if cmd := m.forwardHostUIRequests(hosts.Update{HostID: "mac-mini", UIRequest: []hostproto.UIRequest{event}}); cmd != nil {
		t.Fatalf("forwarded layout get emitted a command: %v", cmd)
	}
	assertRemoteAck(t, stub, "opened", "--action layout")
	var report layoutreport.Report
	if err := json.Unmarshal(layoutJSONFromAckArgs(t, stub.argv(t, 0)), &report); err != nil {
		t.Fatalf("report does not parse: %v", err)
	}
	if report.Surface != selected.ID {
		t.Fatalf("surface = %q, want %q", report.Surface, selected.ID)
	}
}

func TestHostTUILayoutGetAnswersLocallyWhenLeaseHeld(t *testing.T) {
	m, _ := layoutSessionsModel(t)
	stub := &remoteRunnerStub{}
	stub.install(t)
	run(t, m, m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, "Document"))

	req := sessionsLayoutPayload(t, uirequest.LayoutModeGet)
	if cmd := m.handleUIRequest(req); cmd != nil {
		t.Fatal("local get emitted a command")
	}
	if len(stub.calls) != 0 {
		t.Fatalf("host TUI acked remotely: %v", stub.calls)
	}
	ack := readSessionsLayoutAck(t, req)
	if ack.Status != uirequest.StatusOpened || len(ack.Layout) == 0 {
		t.Fatalf("local get ack = %s layout %d bytes", ack.Status, len(ack.Layout))
	}
	var report layoutreport.Report
	if err := json.Unmarshal(ack.Layout, &report); err != nil {
		t.Fatalf("report does not parse: %v", err)
	}
	if report.Surface != "a" {
		t.Fatalf("surface = %q", report.Surface)
	}
	if report.Grid == nil {
		t.Fatal("grid is null")
	}
	foundFile := false
	for _, col := range report.Grid.Columns {
		for _, pane := range col.Panes {
			if pane.Kind == "file" {
				foundFile = true
			}
		}
	}
	if !foundFile {
		t.Fatalf("file pane missing from local get: %+v", report.Grid)
	}
}
