package uiapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentservice"
)

// A content subscription shares a socket with workspace invalidations without
// widening the paired origin's grant or dropping either stream for full clients.
func TestIntegratedWorkspaceContentScopesAndEvents(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		scopes                   []string
		workspace, content, full bool
	}{
		{"content", []string{ScopeContentRead}, false, true, false},
		{"workspace", []string{ScopeWorkspaceWrite}, true, false, false},
		{"combined", []string{ScopeWorkspaceWrite, ScopeContentRead}, true, true, false},
		{"full", []string{ScopeFull}, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "visible.md")
			if err := os.WriteFile(file, []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			backend := &workspaceEventBackend{&eventBackend{fakeBackend: newFakeBackend(), changes: make(chan struct{}, 1)}}
			h := newHarness(t, func(o *Options) {
				o.Backend = backend
				o.Content = &contentservice.Service{LoadConfig: func() (*config.Config, error) {
					cfg := &config.Config{}
					cfg.Projects.List = []config.ProjectConfig{{Name: "fixture-project", Path: root}}
					return cfg, nil
				}}
			})
			origin := "http://integrated.example"
			token, _, err := h.s.origins.pair(origin, tc.scopes, h.clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			headers := map[string]string{"Authorization": "Bearer " + token, "Origin": origin}
			for _, path := range []string{"/api/v0/projects", "/api/v0/projects/fixture-project/workspace"} {
				status := 403
				if tc.workspace {
					status = 200
				}
				response, data := h.browserDo(req{method: "GET", path: path, header: headers})
				expectWorkspaceStatus(t, response, data, status)
			}
			status := 403
			if tc.content {
				status = 200
			}
			response, data := h.browserDo(req{method: "GET", path: "/api/v0/projects/fixture-project/content?kind=file&target=visible.md", header: headers})
			expectWorkspaceStatus(t, response, data, status)
			response, data = h.browserDo(req{method: "POST", path: "/api/v0/projects/fixture-project/shells/create", body: `{}`, header: mutationHeaders(origin, headers)})
			status = 403
			if tc.workspace {
				status = 200
			}
			expectWorkspaceStatus(t, response, data, status)
			for _, ticket := range []bool{false, true} {
				query := url.Values{}
				ref := ContentRef{Project: "fixture-project", Kind: "file", Target: "visible.md"}
				if tc.content {
					raw, _ := json.Marshal(ref)
					query.Add("content", string(raw))
				}
				wsHeaders := http.Header{"Authorization": {"Bearer " + token}, "Origin": {origin}}
				if ticket {
					response, data := h.browserDo(req{method: "POST", path: "/api/v0/ws-tickets", body: `{}`, header: mutationHeaders(origin, headers)})
					expectWorkspaceStatus(t, response, data, 200)
					var issued TicketResponse
					if err := json.Unmarshal(data, &issued); err != nil {
						t.Fatal(err)
					}
					query.Set("ticket", issued.Ticket)
					wsHeaders.Del("Authorization")
				}
				conn := dialEvents(t, h, "?"+query.Encode(), wsHeaders, false)
				var seq uint64 = 1
				if tc.full {
					initialEvents(t, conn)
					seq = 3
				} else if got := readEvent(t, conn); got.Type != "hello" || got.Seq != 1 {
					t.Fatalf("hello: %+v", got)
				}
				if tc.workspace {
					seq++
					if got := readEvent(t, conn); got.Type != "workspace" || got.Seq != seq || got.Workspace == nil {
						t.Fatalf("workspace baseline: %+v", got)
					}
				}
				if tc.content {
					seq++
					if err := os.WriteFile(file, []byte(tc.name+string(rune(seq))), 0600); err != nil {
						t.Fatal(err)
					}
					if got := readEvent(t, conn); got.Type != "content" || got.Seq != seq || got.Content == nil || len(got.Content.Resources) != 1 || got.Content.Resources[0] != ref {
						t.Fatalf("scoped content event: %+v", got)
					}
				}
				_ = conn.CloseNow()
			}
			// No catalog backend calls are made for either narrower credential.
			if !tc.full && backend.count() != 0 {
				t.Fatalf("scoped events read catalog %d times", backend.count())
			}
		})
	}
}
