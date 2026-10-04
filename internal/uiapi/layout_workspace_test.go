package uiapi

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/uirequest"
)

func worktreeLayoutHarness(t *testing.T) (*harness, string, string, string) {
	t.Helper()
	h, root := viewerHarness(t)
	gitLayout(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "readme.md"), []byte("main checkout\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitLayout(t, root, "add", "readme.md")
	gitLayout(t, root, "-c", "user.name=Proof", "-c", "user.email=proof@example.invalid", "commit", "-qm", "layout fixture")
	worktree := filepath.Join(t.TempDir(), "worktree")
	gitLayout(t, root, "worktree", "add", "-qb", "layout-proof", worktree)
	worktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	return h, root, worktree, root + ":worktree:" + worktree
}

func gitLayout(t *testing.T, root string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func layoutRead(t *testing.T, h *harness, path string) (LayoutDocument, string) {
	t.Helper()
	r, body := h.localDo(req{method: "GET", path: path})
	expect(t, r, body, 200, "")
	var doc LayoutDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	return doc, r.Header.Get("ETag")
}

func TestWorkspaceLayoutRoutesIsolationAndConditions(t *testing.T) {
	h, root, worktree, workspace := worktreeLayoutHarness(t)
	path := "/api/v0/projects/content/layout"
	scoped := path + "?workspace=" + url.QueryEscape(workspace)
	_, empty := layoutRead(t, h, scoped)
	body := `{"layout":{"kind":"terminal","session":"sidecar-sh-content-1"}}`
	r, data := h.localDo(req{method: "PUT", path: scoped, body: body})
	expect(t, r, data, 428, "precondition_required")
	r, data = h.localDo(req{method: "PUT", path: scoped, body: body, header: map[string]string{"If-Match": empty}})
	expect(t, r, data, 200, "")
	doc, etag := layoutRead(t, h, scoped)
	if doc.Layout == nil || doc.Layout.Session != "sidecar-sh-content-1" || etag == empty {
		t.Fatalf("worktree layout not saved: %+v %s", doc, etag)
	}
	if doc, _ := layoutRead(t, h, path); doc.Layout != nil {
		t.Fatal("worktree layout leaked into main checkout")
	}
	for _, method := range []string{"GET", "HEAD"} {
		r, data = h.localDo(req{method: method, path: scoped, header: map[string]string{"If-None-Match": etag}})
		expect(t, r, data, 304, "")
	}
	for _, stale := range []string{empty, "*"} {
		r, data = h.localDo(req{method: "PUT", path: scoped, body: `{"layout":null}`, header: map[string]string{"If-Match": stale}})
		expect(t, r, data, 412, "precondition_failed")
	}
	token := h.pairOrigin("https://workspace-layout.example")
	r, data = h.browserDo(req{method: "GET", path: scoped, header: map[string]string{"Authorization": "Bearer " + token}})
	expect(t, r, data, 200, "")
	if string(data) != "{\"layout\":null}\n" {
		t.Fatalf("worktree layout leaked between viewers: %s", data)
	}
	// Explicit main-worktree and omitted workspace identify the same root.
	r, data = h.localDo(req{method: "PUT", path: path, body: body, header: map[string]string{"If-Match": empty}})
	expect(t, r, data, 200, "")
	if doc, _ := layoutRead(t, h, path+"?workspace="+url.QueryEscape(root+":worktree:"+root)); doc.Layout == nil {
		t.Fatal("main-worktree alias has a separate store")
	}
	for _, query := range []string{"unknown=x", "workspace=a&workspace=b", "workspace=" + strings.Repeat("x", 8193)} {
		for _, method := range []string{"GET", "PUT"} {
			r, data = h.localDo(req{method: method, path: path + "?" + query, body: body})
			expect(t, r, data, 400, CodeInvalidRequest)
		}
	}
	for _, id := range []string{"other:worktree:" + worktree, "remote-scoped-id", root + ":worktree:" + t.TempDir()} {
		for _, method := range []string{"GET", "PUT"} {
			r, data = h.localDo(req{method: method, path: path + "?workspace=" + url.QueryEscape(id), body: body})
			expect(t, r, data, 403, "rejected")
		}
	}
	gitLayout(t, root, "worktree", "remove", worktree)
	for _, method := range []string{"GET", "PUT"} {
		r, data = h.localDo(req{method: method, path: scoped, body: body, header: map[string]string{"If-Match": etag}})
		expect(t, r, data, 403, "rejected")
	}
}

func worktreePresence(t *testing.T, h *harness, id, workspace string) {
	t.Helper()
	data, _ := json.Marshal(ViewerPresenceRequest{ViewerID: id, Focused: true, Visible: true, Project: "content", Workspace: workspace, Session: "sidecar-sh-content-1", Viewport: Viewport{Width: 1200, Height: 800}, FocusedPane: 1})
	r, body := h.localDo(req{method: "POST", path: viewerPresencePath, body: string(data)})
	expect(t, r, body, 200, "")
}

func TestWorkspaceLayoutRelayUsesPublicScopeAndBoundaries(t *testing.T) {
	h, root, worktree, workspace := worktreeLayoutHarness(t)
	path := "/api/v0/projects/content/layout?workspace=" + url.QueryEscape(workspace)
	_, etag := layoutRead(t, h, path)
	r, body := h.localDo(req{method: "PUT", path: path, body: `{"layout":{"kind":"terminal","session":"sidecar-sh-content-1"}}`, header: map[string]string{"If-Match": etag}})
	expect(t, r, body, 200, "")
	c, id := screen(t, h)
	worktreePresence(t, h, id, workspace)
	request := postScreenRequest(t, h, worktree, uirequest.ActionOpen, nil, filepath.Join(worktree, "readme.md"))
	event := nextUIRequest(t, c)
	if event.Workspace != workspace || event.Document.Layout.Split.B.Tabs[0].Path != "readme.md" {
		t.Fatalf("wrong workspace proposal: %+v", event)
	}
	ackScreen(t, h, id, event, 200)
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusOpened {
		t.Fatal(ack)
	}
	doc, before := layoutRead(t, h, path)
	if doc.Layout.Split == nil {
		t.Fatal("ack did not persist to public workspace route")
	}
	if main, _ := layoutRead(t, h, "/api/v0/projects/content/layout"); main.Layout != nil {
		t.Fatal("relay mutated main checkout layout")
	}
	if err := os.Symlink(filepath.Join(root, ".git"), filepath.Join(worktree, "metadata")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(worktree, "outside")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{".git", "metadata/config", "outside/readme.md", filepath.Join(root, "readme.md"), "../readme.md"} {
		request := postScreenRequest(t, h, worktree, uirequest.ActionOpen, nil, target)
		if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusDeclined {
			t.Fatalf("boundary accepted %q: %+v", target, ack)
		}
	}
	if _, after := layoutRead(t, h, path); before != after {
		t.Fatal("refused proposal mutated scoped layout")
	}
	// A public PUT races the proposal in exactly the same store.
	request = postScreenRequest(t, h, worktree, uirequest.ActionLayout, uirequest.LayoutPayload{Mode: "get"}, "")
	event = nextUIRequest(t, c)
	r, body = h.localDo(req{method: "PUT", path: path, body: `{"layout":{"kind":"terminal","session":"sidecar-sh-content-1","name":"newer window"}}`, header: map[string]string{"If-Match": before}})
	expect(t, r, body, 200, "")
	ackScreen(t, h, id, event, 409)
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusDeclined {
		t.Fatal(ack)
	}
	// Even a terminal-only get must revalidate current Git membership at ack.
	request = postScreenRequest(t, h, worktree, uirequest.ActionLayout, uirequest.LayoutPayload{Mode: "get"}, "")
	event = nextUIRequest(t, c)
	gitLayout(t, root, "worktree", "remove", "--force", worktree)
	ackScreen(t, h, id, event, 409)
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusDeclined {
		t.Fatal(ack)
	}
	// A retained/recreated directory is not proof of Git membership.
	if err := os.Mkdir(worktree, 0700); err != nil {
		t.Fatal(err)
	}
	request = postScreenRequest(t, h, worktree, uirequest.ActionLayout, uirequest.LayoutPayload{Mode: "get"}, "")
	if ack := waitScreenAck(t, h, request); ack.Status != uirequest.StatusDeclined {
		t.Fatal("removed workspace still relayed", ack)
	}
}
