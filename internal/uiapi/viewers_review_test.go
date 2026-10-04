package uiapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/viewerlayout"
)

// A browser tab keeps its events stream across bearer renewals. Presence from
// the same credential holder with its current bearer must keep that viewer
// able to receive and acknowledge requests; otherwise every agent request
// declines fifteen minutes after the tab connected.
func TestAPIViewerSurvivesBrowserBearerRenewal(t *testing.T) {
	h, root := viewerHarness(t)
	key, paired := h.pairBrowserKey()
	client := sessionClient(paired.RegistrationID)
	store := viewerlayout.FileStore{Dir: filepath.Join(h.s.dir, "layouts")}
	_, etag, err := store.Get(client, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Put(client, root, etag, LayoutDocument{Layout: &state.PaneLayoutJSON{Kind: "terminal", Session: "sidecar-sh-content-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.md"), []byte("# a pane\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := dialEvents(t, h, "?viewer=uiRequestRelayV1", http.Header{"Authorization": {"Bearer " + paired.Token}, "Origin": {h.ownOrigin()}}, false)
	if e := readEvent(t, c); e.Type != "hello" {
		t.Fatalf("hello: %+v", e)
	}
	identity := readEvent(t, c)
	if identity.Type != "viewer" {
		t.Fatalf("viewer: %+v", identity)
	}
	id := identity.Viewer.ID
	post := func(path, token string, body any) (*http.Response, []byte) {
		data, _ := json.Marshal(body)
		return h.browserDo(req{method: http.MethodPost, path: path, body: string(data), header: mutationHeaders(h.ownOrigin(), map[string]string{"Authorization": "Bearer " + token})})
	}
	presence := ViewerPresenceRequest{ViewerID: id, Focused: true, Visible: true, Project: "content", Session: "sidecar-sh-content-1", Viewport: Viewport{Width: 1200, Height: 800}, FocusedPane: 1}
	r, b := post(viewerPresencePath, paired.Token, presence)
	expect(t, r, b, 200, "")

	h.clock.Advance(browserBearerTTL)
	fresh := h.renewBrowser(key, paired.RegistrationID).Token
	r, b = post(viewerPresencePath, fresh, presence)
	expect(t, r, b, 200, "")

	request := postScreenRequest(t, h, root, uirequest.ActionOpen, nil, "readme.md")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if acks, _ := uirequest.ReadAcks(h.s.opts.StateDir, request.ID, request.Action); len(acks) > 0 {
			t.Fatalf("renewed viewer was refused: %+v", acks[0])
		}
		h.s.viewer.mu.Lock()
		pending := 0
		if v := h.s.viewer.screens[id]; v != nil {
			pending = len(v.pending)
		}
		h.s.viewer.mu.Unlock()
		if pending == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	event := nextUIRequest(t, c)
	r, b = post(viewerAckPath, fresh, ViewerAckRequest{ViewerID: id, ID: event.ID, Status: uirequest.StatusOpened})
	expect(t, r, b, 200, "")
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusOpened {
		t.Fatalf("ack: %+v", ack)
	}
}

// --sessions addresses the TUI's global Sessions surface. The browser has no
// such surface, so it must decline rather than apply the request to its
// project workspace.
func TestAPIViewerDeclinesSessionsSurfaceRequests(t *testing.T) {
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	_, id := screen(t, h)
	presence(t, h, id, true)
	store := viewerlayout.FileStore{Dir: filepath.Join(h.s.dir, "layouts")}
	_, before, _ := store.Get("local", root)
	for _, action := range []uirequest.Action{uirequest.ActionOpen, uirequest.ActionLayout} {
		raw, _ := json.Marshal(uirequest.LayoutPayload{Mode: "apply", Panes: []uirequest.LayoutPane{{Kind: "file", Targets: []string{"readme.md"}}}})
		request := uirequest.Request{ID: uirequest.NewRequestID(), Version: 1, CreatedAt: time.Now(), TTLMs: 15000, Action: action, Origin: uirequest.Origin{WorkDir: root, Sessions: true}, Payload: raw, Target: uirequest.Target{Kind: "file", Value: "readme.md"}}
		if _, err := uirequest.WriteRequest(h.s.opts.StateDir, request); err != nil {
			t.Fatal(err)
		}
		if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusDeclined {
			t.Fatalf("%s --sessions accepted by the browser: %+v", action, ack)
		}
	}
	if _, after, _ := store.Get("local", root); after != before {
		t.Fatal("sessions request mutated the browser layout")
	}
}

// Proposals reuse the content API's Git metadata policy.
func TestAPIViewerRefusesGitMetadataTargets(t *testing.T) {
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	_, id := screen(t, h)
	presence(t, h, id, true)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[remote]\nurl = https://user:secret@example.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".git", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{".git/config", filepath.Join(root, ".git", "config"), "alias/config"} {
		request := postScreenRequest(t, h, root, uirequest.ActionOpen, nil, target)
		if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusDeclined {
			t.Fatalf("git metadata accepted: %s %+v", target, ack)
		}
	}
}

// ui:control is narrower than full: a viewer's decline reason and its saved
// layout strings reach the CLI's output, so they must not carry terminal
// control sequences.
func TestAPIViewerStripsControlSequencesBeforeTheCLI(t *testing.T) {
	h, root := viewerHarness(t)
	seedScreen(t, h, root)
	c, id := screen(t, h)
	presence(t, h, id, true)
	request := postScreenRequest(t, h, root, uirequest.ActionOpen, nil, "readme.md")
	event := nextUIRequest(t, c)
	data, _ := json.Marshal(ViewerAckRequest{ViewerID: id, ID: event.ID, Status: uirequest.StatusDeclined, Reason: "busy\x1b]52;c;cm0gLXJmIH4=\x07\u009b31m" + strings.Repeat("x", 4096)})
	response, body := h.localDo(req{method: "POST", path: viewerAckPath, body: string(data)})
	expect(t, response, body, 200, "")
	ack := waitScreenAck(t, h, request)
	if strings.IndexFunc(ack.Reason, unicode.IsControl) >= 0 || len(ack.Reason) > 600 || !strings.Contains(ack.Reason, "busy") {
		t.Fatalf("client reason reached the CLI unbounded: %q", ack.Reason)
	}

	store := viewerlayout.FileStore{Dir: filepath.Join(h.s.dir, "layouts")}
	_, etag, _ := store.Get("local", root)
	if _, _, err := store.Put("local", root, etag, LayoutDocument{Layout: &state.PaneLayoutJSON{Kind: "terminal", Session: "sidecar-sh-content-1", Name: "\x1b]52;c;cm0gLXJmIH4=\x07"}}); err != nil {
		t.Fatal(err)
	}
	request = postScreenRequest(t, h, root, uirequest.ActionLayout, uirequest.LayoutPayload{Mode: "get"}, "")
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusDeclined || strings.Contains(string(ack.Layout), "52;c") {
		t.Fatalf("control sequence in saved layout reached the CLI: %+v", ack)
	}
}
