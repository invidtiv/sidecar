package uiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/viewerlayout"
)

func screen(t *testing.T, h *harness) (*websocket.Conn, string) {
	t.Helper()
	c := dialEvents(t, h, "?viewer=uiRequestRelayV1", nil, true)
	if e := readEvent(t, c); e.Type != "hello" {
		t.Fatalf("hello: %+v", e)
	}
	e := readEvent(t, c)
	if e.Type != "viewer" || e.Viewer.Capability != uirequest.APIViewerRelay {
		t.Fatalf("viewer: %+v", e)
	}
	return c, e.Viewer.ID
}
func presence(t *testing.T, h *harness, id string, focused bool) bool {
	t.Helper()
	data, _ := json.Marshal(ViewerPresenceRequest{ViewerID: id, Focused: focused, Visible: true, Project: "content", Session: "sidecar-sh-content-1", Viewport: Viewport{Width: 1200, Height: 800}, FocusedPane: 1})
	response, body := h.localDo(req{method: "POST", path: viewerPresencePath, body: string(data)})
	expect(t, response, body, 200, "")
	var out ViewerPresenceResponse
	_ = json.Unmarshal(body, &out)
	return out.Holder
}
func seedScreen(t *testing.T, h *harness, root string) {
	t.Helper()
	store := viewerlayout.FileStore{Dir: filepath.Join(h.s.dir, "layouts")}
	_, etag, err := store.Get("local", root)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Put("local", root, etag, LayoutDocument{Layout: &state.PaneLayoutJSON{Kind: "terminal", Session: "sidecar-sh-content-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "readme.md"), []byte("# a pane\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
func postScreenRequest(t *testing.T, h *harness, root string, action uirequest.Action, payload any, target string) uirequest.Request {
	t.Helper()
	raw, _ := json.Marshal(payload)
	req := uirequest.Request{ID: uirequest.NewRequestID(), Version: 1, CreatedAt: time.Now(), TTLMs: 15000, Action: action, Origin: uirequest.Origin{WorkDir: root, TmuxSession: "sidecar-sh-content-1"}, Payload: raw, Target: uirequest.Target{Kind: "file", Value: target}}
	if _, err := uirequest.WriteRequest(h.s.opts.StateDir, req); err != nil {
		t.Fatal(err)
	}
	return req
}
func nextUIRequest(t *testing.T, c *websocket.Conn) UIRequestEvent {
	t.Helper()
	for {
		e := readEvent(t, c)
		if e.Type == "ui_request" {
			return *e.UIRequest
		}
	}
}
func ackScreen(t *testing.T, h *harness, id string, event UIRequestEvent, status int) {
	t.Helper()
	data, _ := json.Marshal(ViewerAckRequest{ViewerID: id, ID: event.ID, Status: uirequest.StatusOpened})
	response, body := h.localDo(req{method: "POST", path: viewerAckPath, body: string(data)})
	expect(t, response, body, status, "")
}
func waitScreenAck(t *testing.T, h *harness, req uirequest.Request) uirequest.Ack {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		acks, _ := uirequest.ReadAcks(h.s.opts.StateDir, req.ID, req.Action)
		if len(acks) > 0 {
			return acks[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no UI acknowledgement")
	return uirequest.Ack{}
}
func TestAPIViewerOpenLayoutAndMoveJourney(t *testing.T) {
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	c, id := screen(t, h)
	if !presence(t, h, id, true) {
		t.Fatal("focused viewer did not hold screen")
	}
	req := postScreenRequest(t, h, root, uirequest.ActionOpen, nil, filepath.Join(root, "readme.md"))
	event := nextUIRequest(t, c)
	if event.Document.Layout.Split == nil || event.Document.Layout.Split.B.Tabs[0].Path != "readme.md" {
		t.Fatalf("proposal: %+v", event.Document)
	}
	store := viewerlayout.FileStore{Dir: filepath.Join(h.s.dir, "layouts")}
	doc, _, _ := store.Get("local", root)
	if doc.Layout.Split != nil {
		t.Fatal("unacknowledged proposal was committed")
	}
	ackScreen(t, h, id, event, 200)
	if ack := waitScreenAck(t, h, req); ack.Status != uirequest.StatusOpened {
		t.Fatalf("ack: %+v", ack)
	}
	req = postScreenRequest(t, h, root, uirequest.ActionLayout, uirequest.LayoutPayload{Mode: "get"}, "")
	event = nextUIRequest(t, c)
	ackScreen(t, h, id, event, 200)
	ack := waitScreenAck(t, h, req)
	if !strings.Contains(string(ack.Layout), "viewport_css_pixels") || strings.Contains(string(ack.Layout), "\"viewport\"") {
		t.Fatalf("terminal cells in browser report: %s", ack.Layout)
	}
	req = postScreenRequest(t, h, root, uirequest.ActionLayout, uirequest.LayoutPayload{Mode: "move", Move: &uirequest.LayoutMove{From: "2.1", To: "left"}}, "")
	event = nextUIRequest(t, c)
	ackScreen(t, h, id, event, 200)
	if ack := waitScreenAck(t, h, req); ack.Status != uirequest.StatusMoved {
		t.Fatalf("move: %+v", ack)
	}
	req = postScreenRequest(t, h, root, uirequest.ActionLayout, uirequest.LayoutPayload{Mode: "apply", Columns: json.RawMessage(`[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"file","targets":["readme.md"]}]}]`)}, "")
	event = nextUIRequest(t, c)
	ackScreen(t, h, id, event, 200)
	if ack := waitScreenAck(t, h, req); ack.Status != uirequest.StatusOpened {
		t.Fatalf("spec: %+v", ack)
	}
	doc, _, _ = store.Get("local", root)
	if doc.Layout.Split.A.Session != "sidecar-sh-content-1" {
		t.Fatal("spec lost primary session")
	}
}
func TestAPIViewerFocusTransitionsDoNotStealOnHeartbeat(t *testing.T) {
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	_, a := screen(t, h)
	_, b := screen(t, h)
	if !presence(t, h, a, true) || !presence(t, h, b, true) || presence(t, h, a, true) {
		t.Fatal("heartbeat stole screen from latest focus transition")
	}
	if presence(t, h, b, false) {
		t.Fatal("unfocused holder")
	}
	if !presence(t, h, a, true) {
		t.Fatal("remaining focused holder")
	}
	presence(t, h, a, false)
	if v, ok := uirequest.ReadAPIViewer(h.s.opts.StateDir, time.Now()); !ok || v.Focused {
		t.Fatalf("blur not announced: %+v %v", v, ok)
	}
}
func TestAPIViewerLateAckAndPathEscapesDeclineWithoutStoreMutation(t *testing.T) {
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	c, id := screen(t, h)
	presence(t, h, id, true)
	store := viewerlayout.FileStore{Dir: filepath.Join(h.s.dir, "layouts")}
	_, before, _ := store.Get("local", root)
	req := postScreenRequest(t, h, root, uirequest.ActionOpen, nil, "readme.md")
	event := nextUIRequest(t, c)
	presence(t, h, id, false)
	ackScreen(t, h, id, event, 409)
	if ack := waitScreenAck(t, h, req); ack.Status != uirequest.StatusDeclined {
		t.Fatal(ack)
	}
	_, after, _ := store.Get("local", root)
	if before != after {
		t.Fatal("blur mutated layout")
	}
	presence(t, h, id, true)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"../secret", filepath.Join(outside, "secret"), "escape/secret"} {
		req := postScreenRequest(t, h, root, uirequest.ActionOpen, nil, target)
		if ack := waitScreenAck(t, h, req); ack.Status != uirequest.StatusDeclined {
			t.Fatalf("escape accepted: %s %+v", target, ack)
		}
	}
	_, after, _ = store.Get("local", root)
	if before != after {
		t.Fatal("escape mutated layout")
	}
}
func TestAPIViewerScopesAndAckOwnership(t *testing.T) {
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	c, id := screen(t, h)
	presence(t, h, id, true)
	request := postScreenRequest(t, h, root, uirequest.ActionOpen, nil, "readme.md")
	event := nextUIRequest(t, c)
	for _, scope := range []string{ScopeContentRead, ScopeWorkspaceWrite, ScopeUIControl} {
		origin := "https://" + strings.ReplaceAll(scope, ":", "-") + ".example"
		response, data := h.localDo(req{method: "POST", path: "/api/v0/origins", body: `{"origin":"` + origin + `","scopes":["` + scope + `"]}`})
		expect(t, response, data, 200, "")
		var reg OriginRegistration
		_ = json.Unmarshal(data, &reg)
		token := reg.Token
		if scope == ScopeUIControl {
			ackBody, _ := json.Marshal(ViewerAckRequest{ViewerID: id, ID: event.ID, Status: uirequest.StatusOpened})
			response, body := h.browserDo(req{method: "POST", path: viewerAckPath, header: map[string]string{"Origin": origin, "Authorization": "Bearer " + token, "Content-Type": "application/json", "X-Sidecar-Request": "1"}, body: string(ackBody)})
			expect(t, response, body, 409, "viewer_unavailable")
		}
		response, body := h.browserDo(req{method: "POST", path: viewerPresencePath, header: map[string]string{"Origin": origin, "Authorization": "Bearer " + token, "Content-Type": "application/json", "X-Sidecar-Request": "1"}, body: `{}`})
		if scope != ScopeUIControl {
			expect(t, response, body, 403, "scope_refused")
		} else if response.StatusCode == 403 {
			t.Fatalf("ui:control refused: %s", body)
		}
		headers := http.Header{"Authorization": {"Bearer " + token}}
		c := dialEvents(t, h, "?viewer=uiRequestRelayV1", headers, false)
		_ = readEvent(t, c)
		if scope == ScopeUIControl {
			if readEvent(t, c).Type != "viewer" {
				t.Fatal("missing viewer")
			}
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_, _, err := c.Read(ctx)
			cancel()
			if websocket.CloseStatus(err) != CloseOriginRefused {
				t.Fatalf("scope %s: %v", scope, err)
			}
		}
	}
	ackScreen(t, h, id, event, 200)
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusOpened {
		t.Fatal(ack)
	}
}

func viewerHarness(t *testing.T) (*harness, string) {
	h, root := contentHarness(t)
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return h, canonical
}

func TestAPIViewerBatchesAreAtomicAndAckUsesCurrentETag(t *testing.T) {
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	c, id := screen(t, h)
	presence(t, h, id, true)
	store := viewerlayout.FileStore{Dir: filepath.Join(h.s.dir, "layouts")}
	_, before, _ := store.Get("local", root)
	request := postScreenRequest(t, h, root, uirequest.ActionLayout, uirequest.LayoutPayload{Mode: "apply", Panes: []uirequest.LayoutPane{{Kind: "issue", Targets: []string{"td-123456"}}, {Kind: "note", Targets: []string{"nt-123456"}}}}, "")
	event := nextUIRequest(t, c)
	if len(savedLeaves(event.Document.Layout)) != 3 {
		t.Fatal("batch did not open both panes")
	}
	busy := postScreenRequest(t, h, root, uirequest.ActionOpen, nil, "readme.md")
	if ack := waitScreenAck(t, h, busy); ack.Status != uirequest.StatusDeclined {
		t.Fatal(ack)
	}
	_, etag, _ := store.Get("local", root)
	replacement := LayoutDocument{Layout: &state.PaneLayoutJSON{Kind: "terminal", Session: "sidecar-sh-content-1"}}
	replacement.Layout.Session = "sidecar-sh-content-1"
	replacement.Layout.Name = "Changed by another window"
	if _, _, err := store.Put("local", root, etag, replacement); err != nil {
		t.Fatal(err)
	}
	ackScreen(t, h, id, event, 409)
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusDeclined {
		t.Fatal(ack)
	}
	doc, after, _ := store.Get("local", root)
	if before == after || doc.Layout.Name != replacement.Layout.Name {
		t.Fatal("stale proposal replaced current tree")
	}
	request = postScreenRequest(t, h, root, uirequest.ActionLayout, uirequest.LayoutPayload{Mode: "apply", Panes: []uirequest.LayoutPane{{Kind: "issue", Targets: []string{"td-123456"}}, {Kind: "file", Targets: []string{"../escape"}}}}, "")
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusDeclined || ack.ItemsVersion != 1 || len(ack.Items) != 2 {
		t.Fatalf("batch refusal lost item verdicts: %+v", ack)
	}
	_, etag, _ = store.Get("local", root)
	if etag != after {
		t.Fatal("invalid batch partially committed")
	}
	request = postScreenRequest(t, h, root, uirequest.ActionLayout, uirequest.LayoutPayload{Mode: "apply", Panes: []uirequest.LayoutPane{{Kind: "issue", Targets: []string{"td-123456"}}, {Kind: "note", Targets: []string{"nt-123456"}}}}, "")
	event = nextUIRequest(t, c)
	ackScreen(t, h, id, event, 200)
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusOpened {
		t.Fatal(ack)
	}
	doc, _, _ = store.Get("local", root)
	if len(savedLeaves(doc.Layout)) != 3 {
		t.Fatal("valid batch not saved")
	}
	request = postScreenRequest(t, h, root, uirequest.ActionLayout, uirequest.LayoutPayload{Mode: "apply", Panes: []uirequest.LayoutPane{{Kind: "issue", Targets: []string{"td-123456"}}}}, "")
	event = nextUIRequest(t, c)
	ackScreen(t, h, id, event, 200)
	waitScreenAck(t, h, request)
	doc, _, _ = store.Get("local", root)
	for _, leaf := range savedLeaves(doc.Layout) {
		if leaf.Kind == "issue" && len(leaf.IssueTabs) != 1 {
			t.Fatal("retarget duplicated issue tab")
		}
	}

}
