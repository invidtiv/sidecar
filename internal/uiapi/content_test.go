package uiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/noteview"
)

func contentHarness(t *testing.T) (*harness, string) {
	root := t.TempDir()
	service := &contentservice.Service{
		LoadConfig: func() (*config.Config, error) {
			cfg := &config.Config{}
			cfg.Projects.List = []config.ProjectConfig{{Name: "content", Path: root}}
			return cfg, nil
		},
		LookupIssue: func(context.Context, string, string, []issueview.ProjectRef) (*issueview.Data, *issueview.Owner, error) {
			return &issueview.Data{ID: "td-123456", Title: "An issue"}, nil, nil
		},
		LookupNote: func(context.Context, string, string) (*noteview.Data, error) {
			return &noteview.Data{ID: "nt-123456", Title: "A note", Content: "Note body"}, nil
		},
	}
	h := newHarness(t, func(o *Options) { o.Content = service })
	return h, root
}

func TestProjectContentDTOsAndPathBoundary(t *testing.T) {
	h, root := contentHarness(t)
	if err := os.WriteFile(filepath.Join(root, "readme.md"), []byte("# Markdown\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"file", "issue", "note"} {
		target := "readme.md"
		if kind == "issue" {
			target = "td-123456"
		}
		if kind == "note" {
			target = "nt-123456"
		}
		response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/content?kind=" + kind + "&target=" + target})
		expect(t, response, data, 200, "")
		var doc contentservice.ReadResult
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Kind != kind || !doc.ValidRemoteResult() {
			t.Fatalf("invalid DTO: %s", data)
		}
		if kind == "file" {
			if doc.Content != "# Markdown\n" {
				t.Fatalf("content: %s", data)
			}
			response, data = h.localDo(req{method: "GET", path: "/api/v0/projects/content/content?kind=file&target=readme.md&if_revision=" + url.QueryEscape(doc.Revision)})
			expect(t, response, data, 200, "")
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			if !doc.NotModified {
				t.Fatal("conditional content did not match")
			}
		}
	}
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../secret", "sub/../readme.md", secret, "escape/secret"} {
		response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/content?kind=file&target=" + url.QueryEscape(path)})
		expect(t, response, data, 403, "rejected")
	}
	for _, path := range []string{"../", "escape"} {
		response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/tree?path=" + url.QueryEscape(path)})
		expect(t, response, data, 403, "rejected")
	}
	response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/tree"})
	expect(t, response, data, 200, "")
	var tree contentservice.TreeResult
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}
	if len(tree.Dirs) != 1 || len(tree.Dirs[0].Entries) != 2 {
		t.Fatalf("tree: %s", data)
	}
	response, data = h.localDo(req{method: "GET", path: "/api/v0/projects/content/tree?path=.&path=deleted"})
	expect(t, response, data, 200, "")
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}
	if len(tree.Dirs) != 2 || len(tree.Dirs[0].Entries) != 2 || tree.Dirs[1].Err != "directory deleted no longer exists" {
		t.Fatalf("missing expansion blanked the tree: %s", data)
	}
	response, data = h.localDo(req{method: "GET", path: "/api/v0/projects/content/content?kind=file&target=readme.md&workspace=another:shell:key"})
	expect(t, response, data, 403, "rejected")
}

func TestLayoutConditionalWritesViewerIsolationAndPersistence(t *testing.T) {
	h, _ := contentHarness(t)
	path := "/api/v0/projects/content/layout"
	response, data := h.localDo(req{method: "GET", path: path})
	expect(t, response, data, 200, "")
	etag := response.Header.Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	body := `{"layout":{"split":{"axis":"cols","ratio":50,"a":{"kind":"terminal","session":"one"},"b":{"kind":"shell","session":"two"}}}}`
	response, data = h.localDo(req{method: "PUT", path: path, body: body})
	expect(t, response, data, 428, "precondition_required")
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, _ := h.localDo(req{method: "PUT", path: path, body: body, header: map[string]string{"If-Match": etag}})
			results <- response.StatusCode
		}()
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for code := range results {
		counts[code]++
	}
	if counts[200] != 1 || counts[412] != 1 {
		t.Fatalf("conditional writers: %v", counts)
	}
	response, data = h.localDo(req{method: "GET", path: path})
	expect(t, response, data, 200, "")
	etag = response.Header.Get("ETag")
	response, data = h.localDo(req{method: "GET", path: path, header: map[string]string{"If-None-Match": etag}})
	expect(t, response, data, 304, "")
	token := h.pairOrigin("http://layout.example")
	response, data = h.browserDo(req{method: "GET", path: path, header: map[string]string{"Authorization": "Bearer " + token}})
	expect(t, response, data, 200, "")
	if string(data) != "{\"layout\":null}\n" {
		t.Fatalf("viewer leaked layout: %s", data)
	}
	for _, bad := range []string{`{}`, `null`, `{"layout":{"kind":"unknown"}}`, `{"layout":{"kind":"doc","tabs":[{"path":"a"}],"active":3}}`, `{"layout":{"kind":"terminal","columns":80}}`, body + body} {
		response, data = h.localDo(req{method: "PUT", path: path, body: bad, header: map[string]string{"If-Match": etag}})
		expect(t, response, data, 400, CodeInvalidRequest)
	}
	// A new server-side store read must preserve the exact multi-terminal tree.
	files, err := filepath.Glob(filepath.Join(h.s.dir, "layouts", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("persisted files %v %v", files, err)
	}
	saved, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var doc LayoutDocument
	if err = json.Unmarshal(saved, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Layout.Split.B.Session != "two" {
		t.Fatalf("lost peer terminal: %s", saved)
	}
}

func TestContentScopeIsEnforcedAndCORSExposesETag(t *testing.T) {
	h, _ := contentHarness(t)
	app := "http://content.example"
	response, data := h.localDo(req{method: "POST", path: "/api/v0/origins", body: `{"origin":"` + app + `","scopes":["content:read"]}`})
	expect(t, response, data, 200, "")
	var registration OriginRegistration
	if err := json.Unmarshal(data, &registration); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + registration.Token, "Origin": app}
	response, data = h.browserDo(req{method: "GET", path: "/api/v0/projects/content/layout", header: headers})
	expect(t, response, data, 200, "")
	if response.Header.Get("Access-Control-Expose-Headers") != "ETag, X-Sidecar-Exit-Code" {
		t.Fatal("ETag not exposed")
	}
	response, data = h.browserDo(req{method: "GET", path: "/api/v0/sessions", header: headers})
	expect(t, response, data, 403, "scope_refused")
	h.expectBrowserClose(t, "content-only terminal", "", http.Header{"Authorization": {headers["Authorization"]}, "Origin": {app}}, CloseOriginRefused)
	response, data = h.browserDo(req{method: "OPTIONS", path: "/api/v0/projects/content/layout", header: map[string]string{"Origin": app}})
	expect(t, response, data, 204, "")
	if response.Header.Get("Access-Control-Allow-Methods") != corsAllowedMethods || response.Header.Get("Access-Control-Allow-Headers") != corsAllowedHeaders {
		t.Fatal("CORS missing conditional PUT")
	}
}

func TestContentEventsWatchOnlyOpenPanes(t *testing.T) {
	h, root := contentHarness(t)
	file := filepath.Join(root, "visible.md")
	if err := os.WriteFile(file, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	ref := ContentRef{Project: "content", Kind: "file", Target: "visible.md"}
	raw, _ := json.Marshal(ref)
	conn := dialEvents(t, h, "?"+url.Values{"content": {string(raw)}}.Encode(), nil, true)
	initialEvents(t, conn)
	received := make(chan EventMessage, 1)
	readErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, data, err := conn.Read(ctx)
		if err != nil {
			readErr <- err
			return
		}
		var event EventMessage
		err = json.Unmarshal(data, &event)
		if err != nil {
			readErr <- err
			return
		}
		received <- event
	}()
	if err := os.WriteFile(filepath.Join(root, "closed.md"), []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-received:
		t.Fatalf("closed pane invalidated an open pane: %+v", event)
	case err := <-readErr:
		t.Fatal(err)
	case <-time.After(350 * time.Millisecond):
	}
	if err := os.WriteFile(file, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	var event EventMessage
	select {
	case event = <-received:
	case err := <-readErr:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("no content event")
	}
	if event.Type != "content" || event.Seq != 4 || len(event.Content.Resources) != 1 || event.Content.Resources[0] != ref {
		t.Fatalf("invalidation: %+v", event)
	}
	response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/content?kind=file&target=visible.md"})
	expect(t, response, data, 200, "")
	var doc contentservice.ReadResult
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Content != "after" {
		t.Fatal("refetch stale")
	}
	if err := os.WriteFile(filepath.Join(root, "closed.md"), []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	// A ping completes only when the peer drains control frames; no content
	// watcher for closed.md was ever registered.
	if err := conn.CloseNow(); err != nil {
		t.Fatal(err)
	}
}

func TestFixtureContentUsesRecordedDTOsWithoutHostFallback(t *testing.T) {
	fixture, err := LoadFixtures(fixtureDir())
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.Backend = fixture })
	for _, query := range []string{"kind=file&target=README.md", "kind=issue&target=td-123456", "kind=note&target=nt-123456", "kind=diff"} {
		response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/fixture-project/content?" + query})
		expect(t, response, data, 200, "")
		var doc contentservice.ReadResult
		if err = json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if !doc.ValidRemoteResult() {
			t.Fatalf("fixture DTO: %s", data)
		}
	}
	response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/tree"})
	expect(t, response, data, 403, "rejected")
}

func TestContentSubscriptionRejectsEscapesAndBounds(t *testing.T) {
	h, root := contentHarness(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []ContentRef{{Project: "content", Kind: "file", Target: "../secret"}, {Project: "content", Kind: "tree", Target: "../"}, {Project: "missing", Kind: "file", Target: "a"}, {Project: "content", Kind: "file", Target: "escape/not-created"}, {Project: "content", Kind: "tree", Target: "escape/not-created"}} {
		raw, _ := json.Marshal(ref)
		conn := dialEvents(t, h, "?"+url.Values{"content": {string(raw)}}.Encode(), nil, true)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, err := conn.Read(ctx)
		cancel()
		_ = conn.CloseNow()
		if websocket.CloseStatus(err) != CloseProtocolViolation {
			t.Fatalf("invalid subscription: %v", err)
		}
	}
	q := url.Values{}
	for range 33 {
		q.Add("content", `{"project":"content","kind":"tree"}`)
	}
	if _, err := parseContentRefs(q); err == nil {
		t.Fatal("unbounded subscriptions")
	}
}

func TestContentEventsWatchMissingInRootFile(t *testing.T) {
	h, root := contentHarness(t)
	raw, _ := json.Marshal(ContentRef{Project: "content", Kind: "file", Target: "not-created.md"})
	conn := dialEvents(t, h, "?"+url.Values{"content": {string(raw)}}.Encode(), nil, true)
	defer func() { _ = conn.CloseNow() }()
	initialEvents(t, conn)
	if err := os.WriteFile(filepath.Join(root, "not-created.md"), []byte("created"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var event EventMessage
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "content" || event.Content == nil || len(event.Content.Resources) != 1 || event.Content.Resources[0].Target != "not-created.md" {
		t.Fatalf("creation was not invalidated: %s", data)
	}
}

func TestContentDiffAndEncodedPreviewBounds(t *testing.T) {
	h, root := contentHarness(t)
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	file := filepath.Join(root, "README.md")
	if err := os.WriteFile(file, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("-c", "user.name=Proof", "-c", "user.email=proof@example.invalid", "commit", "-qm", "Initial proof")
	if err := os.WriteFile(file, []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/content?kind=diff"})
	expect(t, response, data, 200, "")
	var doc contentservice.ReadResult
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Diff == nil || doc.Diff.Snapshot == nil || len(doc.Diff.Snapshot.Files) != 1 {
		t.Fatalf("diff DTO: %s", data)
	}
	if err := os.WriteFile(file, bytes.Repeat([]byte("a"), 900<<10), 0600); err != nil {
		t.Fatal(err)
	}
	response, data = h.localDo(req{method: "GET", path: "/api/v0/projects/content/content?kind=file&target=README.md"})
	expect(t, response, data, 200, "")
	if len(data) > contentservice.MaxEncodedBytes {
		t.Fatalf("HTTP bypassed shared encoder: %d", len(data))
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Truncated {
		t.Fatal("missing truncation")
	}
}

// Each open pane owns a watcher, and on macOS kqueue spends a descriptor on
// every entry of every watched directory. A per-socket union of directories
// let one credential register 16 streams x 32 panes of the same directory, so
// the bound is on registrations, per credential, across all of its streams.
func TestContentWatchBudgetIsPerClientAcrossStreams(t *testing.T) {
	h, _ := contentHarness(t)
	header := http.Header{"Authorization": {"Bearer " + h.pairOrigin("http://watch.example")}}
	full := url.Values{}
	for range 32 {
		full.Add("content", `{"project":"content","kind":"tree"}`)
	}
	one := "?" + url.Values{"content": {`{"project":"content","kind":"tree"}`}}.Encode()
	first := dialEvents(t, h, "?"+full.Encode(), header, false)
	if e := readEvent(t, first); e.Type != "hello" {
		t.Fatalf("first stream: %+v", e)
	}
	second := dialEvents(t, h, "?"+full.Encode(), header, false)
	if e := readEvent(t, second); e.Type != "hello" {
		t.Fatalf("second stream: %+v", e)
	}
	refused := dialEvents(t, h, one, header, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, _, err := refused.Read(ctx)
	cancel()
	if websocket.CloseStatus(err) != CloseTooManyTerminals {
		t.Fatalf("watch budget not enforced across streams: %v", err)
	}
	// Closing a stream gives its registrations back.
	_ = first.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn := dialEvents(t, h, one, header, false)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		_ = conn.CloseNow()
		if err == nil {
			var e EventMessage
			if json.Unmarshal(data, &e) != nil || e.Type != "hello" {
				t.Fatalf("after release: %s", data)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("closed stream never released its watches: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A project symlink that points outside the root must not become an oracle
// for what exists out there: an existing and a missing path beneath it are
// refused identically by reads, tree listings and watch subscriptions.
func TestEscapingSymlinkRefusalsDoNotRevealOutsideExistence(t *testing.T) {
	h, root := contentHarness(t)
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "present"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	same := func(what, present, absent string) {
		t.Helper()
		if strings.ReplaceAll(present, "present", "absent") != absent {
			t.Errorf("%s reveals out-of-root existence:\n present: %s\n  absent: %s", what, present, absent)
		}
	}
	for _, route := range []string{"content?kind=file&target=", "tree?path="} {
		var bodies []string
		for _, name := range []string{"present", "absent"} {
			response, data := h.localDo(req{method: "GET", path: "/api/v0/projects/content/" + route + "escape/" + name})
			if response.StatusCode != 403 {
				t.Fatalf("%s%s: %d %s", route, name, response.StatusCode, data)
			}
			bodies = append(bodies, string(data))
		}
		same(route, bodies[0], bodies[1])
	}
	for _, kind := range []string{"file", "tree"} {
		var reasons []string
		for _, name := range []string{"present", "absent"} {
			raw, _ := json.Marshal(ContentRef{Project: "content", Kind: kind, Target: "escape/" + name})
			conn := dialEvents(t, h, "?"+url.Values{"content": {string(raw)}}.Encode(), nil, true)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _, err := conn.Read(ctx)
			cancel()
			var closeErr websocket.CloseError
			if !errors.As(err, &closeErr) || closeErr.Code != CloseProtocolViolation {
				t.Fatalf("%s watch %s: %v", kind, name, err)
			}
			reasons = append(reasons, closeErr.Reason)
		}
		same(kind+" watch", reasons[0], reasons[1])
	}
}
