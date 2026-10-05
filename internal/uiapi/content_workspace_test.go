package uiapi

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/state"
)

func TestFixtureCatalogContentSelectorsUseMatchingRoutes(t *testing.T) {
	b, err := LoadFixtures(fixtureDir())
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.Backend = b })
	c, viewer := screen(t, h)
	defer func() { _ = c.CloseNow() }()
	r, body := h.localDo(req{method: "GET", path: "/api/v0/sessions"})
	expect(t, r, body, 200, "")
	var snapshot mobileproto.CatalogSnapshot
	if err = json.Unmarshal(body, &snapshot); err != nil {
		t.Fatal(err)
	}
	linked := 0
	selectors := make(map[string]string)
	for _, section := range snapshot.Sections {
		for _, row := range section.Rows {
			selector := row.ContentWorkspaceID
			selectors[selector] = row.DisplayName
			if selector != "" {
				linked++
			}
			base := "/api/v0/projects/fixture-project/"
			r, body = h.localDo(req{method: "GET", path: base + "content?kind=file&target=README.md&workspace=" + url.QueryEscape(selector)})
			expect(t, r, body, 200, "")
			var doc contentservice.ReadResult
			if err = json.Unmarshal(body, &doc); err != nil || doc.Path != filepath.Join(row.Path, "README.md") {
				t.Fatalf("fixture owning root: %s %v", body, err)
			}
			r, body = h.localDo(req{method: "GET", path: base + "tree?workspace=" + url.QueryEscape(selector)})
			expect(t, r, body, 200, "")
			layout := base + "layout?workspace=" + url.QueryEscape(selector)
			_, etag := layoutRead(t, h, layout)
			layoutBody, _ := json.Marshal(LayoutDocument{Layout: &state.PaneLayoutJSON{Kind: "terminal", Session: "fixture-echo", Name: row.DisplayName}})
			r, body = h.localDo(req{method: "PUT", path: layout, body: string(layoutBody), header: map[string]string{"If-Match": etag}})
			expect(t, r, body, 200, "")
			for _, candidate := range row.Candidates {
				if candidate.ContentWorkspaceID != selector {
					t.Fatalf("fixture candidate scope disagrees with parent: %+v", candidate)
				}
				var input, output bytes.Buffer
				encoder := json.NewEncoder(&input)
				_ = encoder.Encode(mobileproto.Request{Version: 0, Type: "hello", RequestID: "hello"})
				_ = encoder.Encode(mobileproto.Request{Version: 0, Type: "resolve", RequestID: "resolve", Target: candidate.Selector, ExpectedTarget: &candidate.ExpectedTarget})
				if err := b.ServeTerminal(t.Context(), &input, &output); err != nil {
					t.Fatal(err)
				}
				decoder := json.NewDecoder(&output)
				var response mobileproto.Response
				if err := decoder.Decode(&response); err != nil || response.Type != "hello" {
					t.Fatalf("fixture hello: %+v %v", response, err)
				}
				if err := decoder.Decode(&response); err != nil || response.Type != "resolved" || response.Target == nil || response.Target.Identity() != candidate.ExpectedTarget {
					t.Fatalf("fixture candidate lost terminal authority: %+v %v", response, err)
				}
			}
			p, _ := json.Marshal(ViewerPresenceRequest{ViewerID: viewer, Focused: true, Visible: true, Project: "fixture-project", Workspace: selector, Session: "fixture-echo", Viewport: Viewport{Width: 1000, Height: 800}})
			r, body = h.localDo(req{method: "POST", path: viewerPresencePath, body: string(p)})
			expect(t, r, body, 200, "")
		}
	}
	if linked != 2 {
		t.Fatalf("fixture needs linked shell and worktree selectors, got %d", linked)
	}
	// Legacy fixture IDs retain root behavior, regardless of terminal IDs.
	for _, id := range []string{"", "fixture-project", "fixture-shell"} {
		ws, err := b.LookupProject(t.Context(), "fixture-project", id)
		if err != nil || ws.Root != "/workspace/fixture" {
			t.Fatalf("legacy fixture root %q: %+v %v", id, ws, err)
		}
	}
	if _, err := b.LookupProject(t.Context(), "fixture-project", "foreign"); err == nil {
		t.Fatal("unknown fixture selector accepted")
	}
	// Routes accept the project's display name as well as its key, as the real server does.
	if b.workspace == nil || b.workspace.Project.Name == "" || b.workspace.Project.Name == "fixture-project" {
		t.Fatalf("fixture project needs a display name distinct from its key: %+v", b.workspace)
	}
	if ws, err := b.LookupProject(t.Context(), b.workspace.Project.Name, ""); err != nil || ws.Root != "/workspace/fixture" {
		t.Fatalf("fixture project by name: %+v %v", ws, err)
	}
	if _, err := b.Workspace(t.Context(), b.workspace.Project.Name, "", mobileproto.CatalogQuery{}); err != nil {
		t.Fatalf("fixture workspace by name: %v", err)
	}
	main, _ := layoutRead(t, h, "/api/v0/projects/fixture-project/layout")
	legacy, _ := layoutRead(t, h, "/api/v0/projects/fixture-project/layout?workspace=fixture-shell")
	if legacy.Layout == nil || main.Layout == nil || legacy.Layout.Name != main.Layout.Name {
		t.Fatal("legacy shell did not share root layout")
	}
	for selector, name := range selectors {
		doc, _ := layoutRead(t, h, "/api/v0/projects/fixture-project/layout?workspace="+url.QueryEscape(selector))
		if doc.Layout == nil || doc.Layout.Name != name {
			t.Fatalf("fixture scopes did not share by owning root: %q %+v, want %q", selector, doc, name)
		}
	}
}

func TestAliasProjectShellContentLayoutAndPresence(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	dir, err := projectdir.Resolve(alias)
	if err != nil {
		t.Fatal(err)
	}
	if err = shellstate.AddAtPath(filepath.Join(dir, "shells.json"), shellstate.Definition{TmuxName: "alias-shell", DisplayName: "Alias shell", WorkDir: alias}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "readme.md"), []byte("alias content"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &contentservice.Service{LoadConfig: func() (*config.Config, error) {
		cfg := config.Default()
		cfg.Projects.List = []config.ProjectConfig{{Name: "alias", Path: alias}}
		return cfg, nil
	}}
	h := newHarness(t, func(o *Options) { o.Content = svc })
	selector := canonical + ":shell:alias-shell"
	base := "/api/v0/projects/alias/"
	for _, route := range []string{"content?kind=file&target=readme.md&", "tree?"} {
		r, body := h.localDo(req{method: "GET", path: base + route + "workspace=" + url.QueryEscape(selector)})
		expect(t, r, body, 200, "")
	}
	layout := base + "layout?workspace=" + url.QueryEscape(selector)
	_, etag := layoutRead(t, h, layout)
	r, body := h.localDo(req{method: "PUT", path: layout, body: `{"layout":{"kind":"terminal","session":"alias-shell"}}`, header: map[string]string{"If-Match": etag}})
	expect(t, r, body, 200, "")
	if doc, _ := layoutRead(t, h, base+"layout"); doc.Layout == nil || doc.Layout.Session != "alias-shell" {
		t.Fatal("alias shell layout disagrees with configured root")
	}
	c, viewer := screen(t, h)
	defer func() { _ = c.CloseNow() }()
	p, _ := json.Marshal(ViewerPresenceRequest{ViewerID: viewer, Focused: true, Visible: true, Project: "alias", Workspace: selector, Session: "alias-shell", Viewport: Viewport{Width: 1000, Height: 800}})
	r, body = h.localDo(req{method: "POST", path: viewerPresencePath, body: string(p)})
	expect(t, r, body, 200, "")
}
